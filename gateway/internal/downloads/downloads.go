// Package downloads implements the server-side download queue: jobs of image
// pages fetched through the shared outbound pipeline (policy + cookie vault,
// injected as a FetchFunc) and stored under
//
//	<downloadsDir>/<source>/<comicId>/<ep>/<NNN>.<ext>
//
// with job.json metadata beside the page files. Job state is rebuilt from
// job.json on startup; finished jobs are never re-run.
package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"picagateway/internal/jmimage"
)

// Job statuses.
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusError   = "error"
	StatusPartial = "partial"
)

// Page statuses.
const (
	PagePending = "pending"
	PageOK      = "ok"
	PageFailed  = "failed"
)

const jobJSONFile = "job.json"

// Limits for one job submission.
const (
	MaxIDLen      = 200
	MaxSegmentLen = 100
	MaxPages      = 20000
	MaxURLLen     = 8192
	MaxPageBytes  = 30 << 20 // 30 MiB per page
)

// DefaultRetryDelays is the per-page exponential backoff (2s/8s/30s).
var DefaultRetryDelays = []time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second}

// DefaultPageTimeout bounds a single page fetch attempt.
const DefaultPageTimeout = 60 * time.Second

// JMInfo carries the JM scramble parameters for a page.
type JMInfo struct {
	EpsID      string `json:"epsId"`
	ScrambleID string `json:"scrambleId"`
	BookID     string `json:"bookId"`
}

// Page is a single image to download.
type Page struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	JM      *JMInfo           `json:"jm,omitempty"`
}

// PageState is the persisted per-page progress.
type PageState struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	File   string `json:"file,omitempty"`
}

// Job is a download job: both the submission payload (client fields) and the
// persisted job.json document.
type Job struct {
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	Source    string      `json:"source"`
	ComicID   string      `json:"comicId"`
	EP        string      `json:"ep"`
	EPName    string      `json:"epName"`
	Pages     []Page      `json:"pages"`
	CreatedAt int64       `json:"createdAt"`
	UpdatedAt int64       `json:"updatedAt,omitempty"`
	Status    string      `json:"status"`
	Error     string      `json:"error,omitempty"`
	States    []PageState `json:"pageStates,omitempty"`
}

// JobView is the API projection of a job.
type JobView struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Source  string `json:"source"`
	ComicID string `json:"comicId"`
	EP      string `json:"ep"`
	EPName  string `json:"epName"`
	Total   int    `json:"total"`
	Done    int    `json:"done"`
	Failed  int    `json:"failed"`
	Status  string `json:"status"`
	Error   string `json:"error"`
	Dir     string `json:"dir"`
	// Files lists the successfully downloaded file names (relative to Dir)
	// in page order, so clients can browse without guessing extensions.
	Files []string `json:"files"`
}

// FetchFunc downloads one page through the outbound pipeline. It returns the
// body and the Content-Type of the response.
type FetchFunc func(ctx context.Context, page Page) (body []byte, contentType string, err error)

// Options configures a Manager.
type Options struct {
	Dir          string          // root downloads directory (required)
	Fetch        FetchFunc       // outbound fetcher (required)
	Concurrency  int             // parallel jobs; default 2
	RetryDelays  []time.Duration // per-page retry backoff; default 2s/8s/30s
	PageTimeout  time.Duration   // per attempt timeout; default 60s
	MaxPageBytes int64           // per-page body limit; default 30 MiB
	Logger       *slog.Logger
}

// Sentinel errors returned by the manager.
var (
	ErrNotFound    = errors.New("download job not found")
	ErrInvalidJob  = errors.New("invalid download job")
	ErrDirConflict = errors.New("another job already owns this directory")
)

// Manager owns the download queue and its workers.
type Manager struct {
	dir   string
	fetch FetchFunc
	log   *slog.Logger

	concurrency  int
	retryDelays  []time.Duration
	pageTimeout  time.Duration
	maxPageBytes int64

	mu    sync.Mutex
	cond  *sync.Cond
	jobs  map[string]*jobEntry // by job id
	dirs  map[string]string    // relative dir -> job id
	ready []*jobEntry          // FIFO of queued entries

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type jobEntry struct {
	job    Job
	dir    string // absolute job directory
	relDir string // slash-separated, relative to root

	cancel  context.CancelFunc // non-nil while running
	runDone chan struct{}      // closed when the current run returns
}

// New builds a Manager, scans dir for existing job.json files and starts the
// workers. Jobs found in queued/running state are resumed (pages already
// marked ok are skipped); done/error/partial jobs are only listed.
func New(o Options) (*Manager, error) {
	if o.Dir == "" {
		return nil, errors.New("downloads: Dir is required")
	}
	if o.Fetch == nil {
		return nil, errors.New("downloads: Fetch is required")
	}
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{
		dir:          o.Dir,
		fetch:        o.Fetch,
		log:          log,
		concurrency:  o.Concurrency,
		retryDelays:  o.RetryDelays,
		pageTimeout:  o.PageTimeout,
		maxPageBytes: o.MaxPageBytes,
		jobs:         make(map[string]*jobEntry),
		dirs:         make(map[string]string),
	}
	if m.concurrency <= 0 {
		m.concurrency = 2
	}
	if len(m.retryDelays) == 0 {
		m.retryDelays = DefaultRetryDelays
	}
	if m.pageTimeout <= 0 {
		m.pageTimeout = DefaultPageTimeout
	}
	if m.maxPageBytes <= 0 {
		m.maxPageBytes = MaxPageBytes
	}
	m.cond = sync.NewCond(&m.mu)
	m.ctx, m.cancel = context.WithCancel(context.Background())

	if err := m.scan(); err != nil {
		m.cancel()
		return nil, err
	}
	for i := 0; i < m.concurrency; i++ {
		m.wg.Add(1)
		go m.worker()
	}
	return m, nil
}

// Dir returns the root downloads directory.
func (m *Manager) Dir() string { return m.dir }

// scan rebuilds the in-memory index from job.json files under the root.
func (m *Manager) scan() error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return fmt.Errorf("downloads: create %s: %w", m.dir, err)
	}
	var jobs []*jobEntry
	err := filepath.WalkDir(m.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != jobJSONFile {
			return nil
		}
		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			m.log.Warn("downloads: cannot read job.json", "path", p, "error", readErr)
			return nil
		}
		var j Job
		if unmarshalErr := json.Unmarshal(raw, &j); unmarshalErr != nil || j.ID == "" || len(j.Pages) == 0 {
			m.log.Warn("downloads: skipping corrupt job.json", "path", p, "error", unmarshalErr)
			return nil
		}
		if normErr := normalizeJob(&j); normErr != nil {
			m.log.Warn("downloads: skipping invalid job.json", "path", p, "error", normErr)
			return nil
		}
		e := &jobEntry{job: j, dir: filepath.Dir(p)}
		e.relDir = relDirFromParts(j.Source, j.ComicID, j.EP)
		if e.job.Status == StatusRunning || e.job.Status == StatusQueued {
			e.job.Status = StatusQueued // interrupted: resume pending pages
		}
		jobs = append(jobs, e)
		return nil
	})
	if err != nil {
		return fmt.Errorf("downloads: scan %s: %w", m.dir, err)
	}
	sort.Slice(jobs, func(a, b int) bool { // oldest first: FIFO resume order
		ja, jb := jobs[a].job, jobs[b].job
		if ja.CreatedAt != jb.CreatedAt {
			return ja.CreatedAt < jb.CreatedAt
		}
		return ja.ID < jb.ID
	})
	for _, e := range jobs {
		if prev, ok := m.jobs[e.job.ID]; ok {
			m.log.Warn("downloads: duplicate job id in scan, keeping first",
				"id", e.job.ID, "dir", prev.relDir, "dropped", e.relDir)
			continue
		}
		if prevID, ok := m.dirs[e.relDir]; ok {
			m.log.Warn("downloads: duplicate job dir in scan, keeping first",
				"dir", e.relDir, "id", prevID, "dropped", e.job.ID)
			continue
		}
		m.jobs[e.job.ID] = e
		m.dirs[e.relDir] = e.job.ID
		if e.job.Status == StatusQueued {
			m.ready = append(m.ready, e)
		}
	}
	if n := len(m.jobs); n > 0 {
		m.log.Info("downloads: restored jobs", "count", n, "queued", len(m.ready), "dir", m.dir)
	}
	return nil
}

// Submit validates and enqueues a job. If a job with the same id exists and is
// queued, running or done, its current state is returned and nothing is
// re-downloaded (idempotent). Resubmitting an error/partial job restarts it
// from scratch. The bool result reports whether such a job already existed.
func (m *Manager) Submit(j Job) (JobView, bool, error) {
	if j.CreatedAt <= 0 {
		j.CreatedAt = time.Now().Unix()
	}
	if err := ValidateJob(&j); err != nil {
		return JobView{}, false, fmt.Errorf("%w: %s", ErrInvalidJob, err.Error())
	}

	rel := relDirFromParts(j.Source, j.ComicID, j.EP)
	abs := filepath.Join(m.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return JobView{}, false, fmt.Errorf("create job dir: %w", err)
	}

	m.mu.Lock()
	if e, ok := m.jobs[j.ID]; ok {
		switch e.job.Status {
		case StatusQueued, StatusRunning, StatusDone:
			view := viewOf(e)
			m.mu.Unlock()
			return view, true, nil
		}
		// error/partial: replace the entry and start over.
		delete(m.jobs, j.ID)
		delete(m.dirs, e.relDir)
	}
	if other, ok := m.dirs[rel]; ok && other != j.ID {
		m.mu.Unlock()
		return JobView{}, false, fmt.Errorf("%w: %s belongs to job %s", ErrDirConflict, rel, other)
	}
	j.Status = StatusQueued
	j.Error = ""
	j.UpdatedAt = time.Now().Unix()
	j.States = freshStates(len(j.Pages))
	entry := cloneForPersist(j) // the entry owns its States slice; j stays Submit-local
	e := &jobEntry{job: entry, dir: abs, relDir: rel}
	m.jobs[j.ID] = e
	m.dirs[rel] = j.ID
	m.ready = append(m.ready, e)
	view := viewFromJob(j, rel)
	m.cond.Signal()
	m.mu.Unlock()

	m.persistJob(e)
	return view, false, nil
}

// List returns all jobs, newest (by createdAt) first.
func (m *Manager) List() []JobView {
	type row struct {
		view      JobView
		createdAt int64
		id        string
	}
	m.mu.Lock()
	rows := make([]row, 0, len(m.jobs))
	for _, e := range m.jobs {
		rows = append(rows, row{view: viewOf(e), createdAt: e.job.CreatedAt, id: e.job.ID})
	}
	m.mu.Unlock()
	sort.Slice(rows, func(a, b int) bool {
		if rows[a].createdAt != rows[b].createdAt {
			return rows[a].createdAt > rows[b].createdAt
		}
		return rows[a].id > rows[b].id
	})
	views := make([]JobView, len(rows))
	for i, r := range rows {
		views[i] = r.view
	}
	return views
}

// Stats returns the queue length and the number of running jobs.
func (m *Manager) Stats() (queued, running int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.jobs {
		switch e.job.Status {
		case StatusQueued:
			queued++
		case StatusRunning:
			running++
		}
	}
	return queued, running
}

// Delete removes a job from the queue and the list. A running job is
// cancelled first (waiting for its worker to stop). job.json is always
// removed; with files=true the whole job directory is removed too.
func (m *Manager) Delete(id string, files bool) error {
	m.mu.Lock()
	e, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	dir := e.dir
	var wait chan struct{}
	if e.cancel != nil {
		e.cancel()
		wait = e.runDone
	}
	delete(m.jobs, id)
	delete(m.dirs, e.relDir)
	m.mu.Unlock()

	if wait != nil {
		select {
		case <-wait:
		case <-time.After(15 * time.Second):
			m.log.Warn("downloads: timed out waiting for cancelled job", "id", id)
		}
	}
	if err := os.Remove(filepath.Join(dir, jobJSONFile)); err != nil && !os.IsNotExist(err) {
		m.log.Warn("downloads: remove job.json failed", "id", id, "error", err)
	}
	if files {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove files: %w", err)
		}
	}
	return nil
}

// Close stops the workers. Running jobs are cancelled; their job.json keeps
// the interrupted state so the next start resumes them.
func (m *Manager) Close() {
	m.cancel()
	m.mu.Lock()
	m.cond.Broadcast()
	m.mu.Unlock()
	m.wg.Wait()
}

// ---- worker ----

func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		m.mu.Lock()
		for len(m.ready) == 0 && m.ctx.Err() == nil {
			m.cond.Wait()
		}
		if m.ctx.Err() != nil {
			m.mu.Unlock()
			return
		}
		var e *jobEntry
		for len(m.ready) > 0 {
			candidate := m.ready[0]
			m.ready = m.ready[1:]
			if cur, ok := m.jobs[candidate.job.ID]; ok && cur == candidate {
				e = candidate
				break
			}
			// stale entry (deleted or replaced) - skip
		}
		if e == nil {
			m.mu.Unlock()
			continue
		}
		m.mu.Unlock()

		m.run(e)
	}
}

// run downloads every pending page of the job sequentially, persisting
// progress after each page.
func (m *Manager) run(e *jobEntry) {
	ctx, cancel := context.WithCancel(m.ctx)
	defer cancel()

	m.mu.Lock()
	e.cancel = cancel
	e.runDone = make(chan struct{})
	e.job.Status = StatusRunning
	e.job.UpdatedAt = time.Now().Unix()
	snapshot := cloneForPersist(e.job)
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		e.cancel = nil
		if e.runDone != nil {
			close(e.runDone)
			e.runDone = nil
		}
		m.mu.Unlock()
	}()

	m.persist(snapshot, e.dir)
	m.log.Info("download job started", "id", snapshot.ID, "dir", e.relDir, "pages", len(snapshot.Pages))

	canceled := false
	for i := range snapshot.Pages {
		if ctx.Err() != nil {
			canceled = true
			break
		}
		m.mu.Lock()
		if e.job.States[i].Status == PageOK {
			m.mu.Unlock()
			continue
		}
		page := e.job.Pages[i]
		m.mu.Unlock()

		body, contentType, ext, err := m.fetchPage(ctx, page)
		if err == nil && page.JM != nil {
			body, ext, err = m.applyJM(page, body, ext, contentType)
		}
		if err == nil {
			err = m.storePage(e, i, body, ext)
		}
		m.mu.Lock()
		if err != nil {
			e.job.States[i] = PageState{Status: PageFailed, Error: err.Error()}
			m.mu.Unlock()
			m.log.Warn("download page failed", "id", snapshot.ID, "index", i+1,
				"target", page.URL, "error", err.Error())
			m.persistJob(e)
			continue
		}
		e.job.States[i] = PageState{Status: PageOK, File: fileName(i+1, ext)}
		m.mu.Unlock()
		m.persistJob(e)
	}

	// A cancel that lands during the last page's fetch ends the loop without
	// hitting the break above; re-check so interrupted jobs resume as queued.
	if ctx.Err() != nil {
		canceled = true
	}

	m.mu.Lock()
	status, msg := finalize(&e.job, canceled)
	e.job.Status = status
	e.job.Error = msg
	e.job.UpdatedAt = time.Now().Unix()
	snap := cloneForPersist(e.job)
	m.mu.Unlock()
	m.persist(snap, e.dir)

	switch status {
	case StatusDone:
		m.log.Info("download job done", "id", snap.ID, "dir", e.relDir, "pages", len(snap.Pages))
	case StatusPartial:
		m.log.Info("download job partial", "id", snap.ID, "dir", e.relDir,
			"pages", len(snap.Pages), "failed", countPages(snap, PageFailed))
	case StatusError:
		m.log.Warn("download job failed", "id", snap.ID, "dir", e.relDir, "error", msg)
	case StatusQueued:
		m.log.Info("download job interrupted, will resume", "id", snap.ID)
	}
}

// fetchPage runs one page through the fetcher with retries.
func (m *Manager) fetchPage(ctx context.Context, page Page) (body []byte, contentType, ext string, err error) {
	var lastErr error
	for attempt := 0; attempt <= len(m.retryDelays); attempt++ {
		if ctx.Err() != nil {
			return nil, "", "", ctx.Err()
		}
		pageCtx, cancel := context.WithTimeout(ctx, m.pageTimeout)
		body, contentType, err = m.fetch(pageCtx, page)
		cancel()
		if err == nil {
			if int64(len(body)) > m.maxPageBytes {
				return nil, "", "", fmt.Errorf("page body %d bytes exceeds the %d byte limit", len(body), m.maxPageBytes)
			}
			return body, contentType, pageExt(page.URL, contentType), nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, "", "", lastErr
		}
		if attempt < len(m.retryDelays) {
			if !sleepCtx(ctx, m.retryDelays[attempt]) {
				return nil, "", "", lastErr
			}
		}
	}
	return nil, "", "", fmt.Errorf("all %d attempts failed: %w", len(m.retryDelays)+1, lastErr)
}

// applyJM reverses the JM block scramble when the page carries jm info.
// Non-JPEG payloads are saved unchanged with a warning.
func (m *Manager) applyJM(page Page, body []byte, ext, contentType string) ([]byte, string, error) {
	info := page.JM
	epsID, err := parseJMInt("epsId", info.EpsID)
	if err != nil {
		return nil, "", err
	}
	scrambleID, err := parseJMInt("scrambleId", info.ScrambleID)
	if err != nil {
		return nil, "", err
	}
	num := jmimage.SegmentationNum(epsID, scrambleID, info.BookID)
	if num <= 1 {
		return body, ext, nil
	}
	if !jmimage.IsJPEG(ext, contentType) {
		m.log.Warn("download page: jm scramble on non-JPEG, saving as-is",
			"target", page.URL, "ext", ext, "contentType", contentType)
		return body, ext, nil
	}
	out, err := jmimage.Recombine(body, num)
	if err != nil {
		return nil, "", fmt.Errorf("jm recombine: %w", err)
	}
	return out, ext, nil
}

func parseJMInt(name, v string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("jm.%s %q is not a number", name, v)
	}
	return n, nil
}

func (m *Manager) storePage(e *jobEntry, idx int, body []byte, ext string) error {
	name := fileName(idx+1, ext)
	if err := os.WriteFile(filepath.Join(e.dir, name), body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

// cloneForPersist returns a snapshot safe to marshal outside the manager
// lock: the page slice is immutable after submit, only States elements are
// mutated by workers, so copying that slice is sufficient.
func cloneForPersist(j Job) Job {
	if j.States == nil {
		return j
	}
	states := make([]PageState, len(j.States))
	copy(states, j.States)
	j.States = states
	return j
}

// persistJob snapshots and persists the entry's current state.
func (m *Manager) persistJob(e *jobEntry) {
	m.mu.Lock()
	j := cloneForPersist(e.job)
	dir := e.dir
	m.mu.Unlock()
	m.persist(j, dir)
}

// persist writes the job metadata atomically (tmp file + rename).
func (m *Manager) persist(j Job, dir string) {
	raw, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		m.log.Warn("downloads: marshal job.json failed", "id", j.ID, "error", err)
		return
	}
	tmp := filepath.Join(dir, jobJSONFile+".tmp")
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		m.log.Warn("downloads: write job.json failed", "id", j.ID, "error", err)
		return
	}
	if err := os.Rename(tmp, filepath.Join(dir, jobJSONFile)); err != nil {
		m.log.Warn("downloads: rename job.json failed", "id", j.ID, "error", err)
	}
}

// ---- helpers ----

func relDirFromParts(source, comicID, ep string) string {
	return path.Join(source, comicID, ep)
}

func viewOf(e *jobEntry) JobView { return viewFromJob(e.job, e.relDir) }

func viewFromJob(j Job, relDir string) JobView {
	v := JobView{
		ID:      j.ID,
		Title:   j.Title,
		Source:  j.Source,
		ComicID: j.ComicID,
		EP:      j.EP,
		EPName:  j.EPName,
		Total:   len(j.Pages),
		Status:  j.Status,
		Error:   j.Error,
		Dir:     relDir,
	}
	for i := range j.States {
		switch j.States[i].Status {
		case PageOK:
			v.Done++
			if j.States[i].File != "" {
				v.Files = append(v.Files, j.States[i].File)
			}
		case PageFailed:
			v.Failed++
		}
	}
	return v
}

func freshStates(n int) []PageState {
	states := make([]PageState, n)
	for i := range states {
		states[i].Status = PagePending
	}
	return states
}

func countPages(j Job, status string) int {
	n := 0
	for i := range j.States {
		if j.States[i].Status == status {
			n++
		}
	}
	return n
}

func firstPageError(j Job) string {
	for i := range j.States {
		if j.States[i].Status == PageFailed {
			return fmt.Sprintf("page %d: %s", i+1, j.States[i].Error)
		}
	}
	return ""
}

// finalize computes the status of a finished run.
func finalize(j *Job, canceled bool) (string, string) {
	if canceled {
		return StatusQueued, ""
	}
	done := countPages(*j, PageOK)
	failed := countPages(*j, PageFailed)
	switch {
	case failed == 0:
		return StatusDone, ""
	case done == 0:
		return StatusError, fmt.Sprintf("all %d pages failed: %s", len(j.Pages), firstPageError(*j))
	default:
		return StatusPartial, fmt.Sprintf("%d of %d pages failed: %s", failed, len(j.Pages), firstPageError(*j))
	}
}

func fileName(idx int, ext string) string {
	return fmt.Sprintf("%03d.%s", idx, ext)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// pageExt infers the page file extension from the URL path, then the content
// type, defaulting to jpg.
func pageExt(pageURL, contentType string) string {
	if u, err := url.Parse(pageURL); err == nil && u.Path != "" {
		switch ext := strings.ToLower(strings.TrimPrefix(path.Ext(u.Path), ".")); ext {
		case "jpg", "jpeg":
			return "jpg"
		case "png", "webp", "gif", "avif", "bmp":
			return ext
		}
	}
	switch mediaType(contentType) {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/avif":
		return "avif"
	case "image/bmp":
		return "bmp"
	}
	return "jpg"
}

func mediaType(contentType string) string {
	if contentType == "" {
		return ""
	}
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	return strings.ToLower(strings.TrimSpace(contentType))
}

// ValidateJob checks a submission (without mutating it).
func ValidateJob(j *Job) error {
	if l := len(j.ID); l == 0 || l > MaxIDLen {
		return fmt.Errorf("id must be 1..%d characters", MaxIDLen)
	}
	if err := validateSegment("source", j.Source); err != nil {
		return err
	}
	if err := validateSegment("comicId", j.ComicID); err != nil {
		return err
	}
	if err := validateSegment("ep", j.EP); err != nil {
		return err
	}
	if len(j.Pages) == 0 {
		return errors.New("pages must not be empty")
	}
	if len(j.Pages) > MaxPages {
		return fmt.Errorf("pages must not exceed %d entries", MaxPages)
	}
	for i := range j.Pages {
		p := &j.Pages[i]
		if p.URL == "" || len(p.URL) > MaxURLLen {
			return fmt.Errorf("pages[%d].url must be 1..%d characters", i, MaxURLLen)
		}
		if len(p.Headers) > 32 {
			return fmt.Errorf("pages[%d].headers must not exceed 32 entries", i)
		}
		if p.JM != nil {
			if _, err := parseJMInt("epsId", p.JM.EpsID); err != nil {
				return fmt.Errorf("pages[%d]: %w", i, err)
			}
			if _, err := parseJMInt("scrambleId", p.JM.ScrambleID); err != nil {
				return fmt.Errorf("pages[%d]: %w", i, err)
			}
			if len(p.JM.BookID) > MaxSegmentLen {
				return fmt.Errorf("pages[%d].jm.bookId is too long", i)
			}
		}
	}
	return nil
}

// normalizeJob validates a job loaded from disk and realigns its page states.
func normalizeJob(j *Job) error {
	if err := ValidateJob(j); err != nil {
		return err
	}
	switch j.Status {
	case "", StatusQueued, StatusRunning, StatusDone, StatusError, StatusPartial:
	default:
		j.Status = StatusQueued
	}
	if j.Status == "" {
		j.Status = StatusQueued
	}
	if len(j.States) != len(j.Pages) {
		states := freshStates(len(j.Pages))
		copy(states, j.States)
		j.States = states
	}
	for i := range j.States {
		switch j.States[i].Status {
		case PageOK, PageFailed, PagePending:
		default:
			j.States[i].Status = PagePending
		}
	}
	return nil
}

// validateSegment ensures the value is a safe single path component.
func validateSegment(name, v string) error {
	if n := len(v); n == 0 || n > MaxSegmentLen {
		return fmt.Errorf("%s must be 1..%d bytes", name, MaxSegmentLen)
	}
	if v == "." || v == ".." {
		return fmt.Errorf("%s must not be %q", name, v)
	}
	if strings.ContainsAny(v, "/\\\x00") {
		return fmt.Errorf("%s must not contain path separators", name)
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] == 0x7f {
			return fmt.Errorf("%s must not contain control characters", name)
		}
	}
	return nil
}
