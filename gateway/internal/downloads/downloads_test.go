package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testOptions(t *testing.T) Options {
	t.Helper()
	return Options{
		Dir:         filepath.Join(t.TempDir(), "downloads"),
		Fetch:       func(context.Context, Page) ([]byte, string, error) { return nil, "", errors.New("not wired") },
		RetryDelays: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
		PageTimeout: 2 * time.Second,
		Logger:      quietLogger(),
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func findByID(jobs []JobView, id string) (JobView, bool) {
	for _, j := range jobs {
		if j.ID == id {
			return j, true
		}
	}
	return JobView{}, false
}

func simpleJob(id string, urls ...string) Job {
	pages := make([]Page, len(urls))
	for i, u := range urls {
		pages[i] = Page{URL: u}
	}
	return Job{
		// ComicID derives from the id so two jobs in one test never collide
		// on the same directory.
		ID: id, Title: "t", Source: "jm", ComicID: "c" + id, EP: "1", EPName: "e",
		Pages: pages, CreatedAt: time.Now().Unix(),
	}
}

func TestSubmitIdempotentAndListOrder(t *testing.T) {
	o := testOptions(t)
	var calls atomic.Int64
	o.Fetch = func(_ context.Context, p Page) ([]byte, string, error) {
		calls.Add(1)
		return []byte("img-" + p.URL), "image/jpeg", nil
	}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	base := time.Now().Unix()
	j1 := simpleJob("a-1", "http://up/1.jpg")
	j1.CreatedAt = base
	j2 := simpleJob("b-2", "http://up/2.jpg")
	j2.CreatedAt = base + 10

	if _, _, err := m.Submit(j1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Submit(j2); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m.List(), "b-2")
		return v.Status == StatusDone
	})

	// Resubmitting a done job is idempotent: same view, no extra fetches.
	view, existed, err := m.Submit(j1)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m.List(), "a-1")
		return v.Status == StatusDone
	})
	_ = view
	before := calls.Load()
	dup, existed, err := m.Submit(j1)
	if err != nil || !existed {
		t.Fatalf("existed=%v err=%v", existed, err)
	}
	if dup.Status != StatusDone {
		t.Fatalf("dup status = %s", dup.Status)
	}
	waitFor(t, time.Second, func() bool { return calls.Load() == before })

	// List is ordered by createdAt desc.
	jobs := m.List()
	if len(jobs) != 2 || jobs[0].ID != "b-2" || jobs[1].ID != "a-1" {
		t.Fatalf("list order = %+v", jobs)
	}
	if jobs[0].Dir != "jm/cb-2/1" || jobs[0].Total != 1 || jobs[0].Done != 1 || jobs[0].Failed != 0 {
		t.Fatalf("view = %+v", jobs[0])
	}
}

func TestSubmitValidation(t *testing.T) {
	m, err := New(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	cases := map[string]func(*Job){
		"empty id":        func(j *Job) { j.ID = "" },
		"missing source":  func(j *Job) { j.Source = "" },
		"traversal":       func(j *Job) { j.Source = ".." },
		"slash in source": func(j *Job) { j.Source = "a/b" },
		"empty pages":     func(j *Job) { j.Pages = nil },
		"empty url":       func(j *Job) { j.Pages[0].URL = "" },
		"bad jm epsId": func(j *Job) {
			j.Pages[0].JM = &JMInfo{EpsID: "x", ScrambleID: "1", BookID: "00001"}
		},
	}
	for name, mutate := range cases {
		j := simpleJob("j-1", "http://up/1.jpg")
		mutate(&j)
		if _, _, err := m.Submit(j); !errors.Is(err, ErrInvalidJob) {
			t.Errorf("%s: err = %v, want ErrInvalidJob", name, err)
		}
	}
}

func TestSubmitDirConflict(t *testing.T) {
	m, err := New(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if _, _, err := m.Submit(simpleJob("one", "http://up/1.jpg")); err != nil {
		t.Fatal(err)
	}
	two := simpleJob("two", "http://up/2.jpg")
	two.ComicID = "cone" // same directory as job "one"
	if _, _, err := m.Submit(two); !errors.Is(err, ErrDirConflict) {
		t.Fatalf("err = %v, want ErrDirConflict", err)
	}
}

func TestPartialAndErrorStatus(t *testing.T) {
	o := testOptions(t)
	o.Fetch = func(_ context.Context, p Page) ([]byte, string, error) {
		if p.URL == "http://up/bad.jpg" {
			return nil, "", errors.New("host_not_allowed: host \"evil.example\" is not in allowHosts")
		}
		return []byte("ok"), "image/jpeg", nil
	}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if _, _, err := m.Submit(simpleJob("mixed", "http://up/1.jpg", "http://up/bad.jpg")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m.List(), "mixed")
		return v.Status == StatusPartial
	})
	v, _ := findByID(m.List(), "mixed")
	if v.Done != 1 || v.Failed != 1 {
		t.Fatalf("view = %+v", v)
	}
	if v.Error == "" || !contains(v.Error, "host_not_allowed") {
		t.Fatalf("error = %q", v.Error)
	}

	if _, _, err := m.Submit(simpleJob("allbad", "http://up/bad.jpg")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m.List(), "allbad")
		return v.Status == StatusError
	})
	v, _ = findByID(m.List(), "allbad")
	if v.Failed != 1 || v.Error == "" {
		t.Fatalf("view = %+v", v)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestRetryThenSuccess(t *testing.T) {
	o := testOptions(t)
	var attempts atomic.Int64
	o.Fetch = func(_ context.Context, _ Page) ([]byte, string, error) {
		if attempts.Add(1) < 3 {
			return nil, "", errors.New("transient")
		}
		return []byte("ok"), "image/jpeg", nil
	}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if _, _, err := m.Submit(simpleJob("retry", "http://up/1.jpg")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m.List(), "retry")
		return v.Status == StatusDone
	})
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestFilesWrittenAndContentPreserved(t *testing.T) {
	o := testOptions(t)
	o.Fetch = func(_ context.Context, p Page) ([]byte, string, error) {
		return []byte("body-of-" + filepath.Base(p.URL)), "image/jpeg", nil
	}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if _, _, err := m.Submit(simpleJob("files", "http://up/a.jpg", "http://up/b.png")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m.List(), "files")
		return v.Status == StatusDone
	})
	dir := filepath.Join(m.Dir(), "jm", "cfiles", "1")
	for name, want := range map[string]string{
		"001.jpg": "body-of-a.jpg",
		"002.png": "body-of-b.png",
	} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(raw) != want {
			t.Fatalf("%s = %q, want %q", name, raw, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, jobJSONFile)); err != nil {
		t.Fatalf("job.json missing: %v", err)
	}
}

func TestDeleteRunningJob(t *testing.T) {
	o := testOptions(t)
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	o.Fetch = func(ctx context.Context, _ Page) ([]byte, string, error) {
		started <- struct{}{}
		select {
		case <-block:
			return []byte("late"), "image/jpeg", nil
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if _, _, err := m.Submit(simpleJob("cancelme", "http://up/1.jpg")); err != nil {
		t.Fatal(err)
	}
	<-started // job is now running and blocked inside fetch
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m.List(), "cancelme")
		return v.Status == StatusRunning
	})

	dir := filepath.Join(m.Dir(), "jm", "123", "1")

	if err := m.Delete("cancelme", true); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := findByID(m.List(), "cancelme"); ok {
		t.Fatal("job still listed after delete")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("files dir still exists: %v", err)
	}
	close(block) // unblock the abandoned fetch

	if err := m.Delete("missing", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteKeepsFilesWhenAsked(t *testing.T) {
	o := testOptions(t)
	o.Fetch = func(_ context.Context, _ Page) ([]byte, string, error) {
		return []byte("x"), "image/jpeg", nil
	}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if _, _, err := m.Submit(simpleJob("keepfiles", "http://up/1.jpg")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m.List(), "keepfiles")
		return v.Status == StatusDone
	})
	if err := m.Delete("keepfiles", false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(m.Dir(), "jm", "ckeepfiles", "1")
	if _, err := os.Stat(filepath.Join(dir, "001.jpg")); err != nil {
		t.Fatalf("page file removed despite files=false: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, jobJSONFile)); !os.IsNotExist(err) {
		t.Fatalf("job.json still exists: %v", err)
	}
}

func TestRestartResumeSkipsDonePages(t *testing.T) {
	o := testOptions(t)
	root := o.Dir
	var calls atomic.Int64
	o.Fetch = func(_ context.Context, _ Page) ([]byte, string, error) {
		calls.Add(1)
		return []byte("ok"), "image/jpeg", nil
	}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Submit(simpleJob("resume", "http://up/1.jpg", "http://up/2.jpg")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m.List(), "resume")
		return v.Status == StatusDone
	})
	m.Close() // no more fetches counted
	doneCalls := calls.Load()

	// Fresh manager over the same directory: done job listed, not re-run.
	o2 := testOptions(t)
	o2.Dir = root
	o2.Fetch = func(_ context.Context, _ Page) ([]byte, string, error) {
		calls.Add(1)
		return []byte("ok"), "image/jpeg", nil
	}
	m2, err := New(o2)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	v, ok := findByID(m2.List(), "resume")
	if !ok || v.Status != StatusDone || v.Done != 2 {
		t.Fatalf("restored view = %+v ok=%v", v, ok)
	}
	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != doneCalls {
		t.Fatalf("done job was re-fetched: %d calls (was %d)", got, doneCalls)
	}

	// A job persisted as running with one page ok resumes only pending pages.
	man := Job{
		ID: "interrupted", Source: "jm", ComicID: "9", EP: "2",
		Pages:     []Page{{URL: "http://up/a.jpg"}, {URL: "http://up/b.jpg"}},
		CreatedAt: time.Now().Unix(),
		Status:    StatusRunning,
		States: []PageState{
			{Status: PageOK, File: "001.jpg"},
			{Status: PagePending},
		},
	}
	raw, _ := json.Marshal(man)
	dir := filepath.Join(root, "jm", "9", "2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, jobJSONFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	var gotURLs sync.Map
	o3 := testOptions(t)
	o3.Dir = root
	o3.Fetch = func(_ context.Context, p Page) ([]byte, string, error) {
		gotURLs.Store(p.URL, true)
		return []byte("ok"), "image/jpeg", nil
	}
	m3, err := New(o3)
	if err != nil {
		t.Fatal(err)
	}
	defer m3.Close()
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m3.List(), "interrupted")
		return v.Status == StatusDone
	})
	if _, loaded := gotURLs.Load("http://up/a.jpg"); loaded {
		t.Fatal("already-ok page was re-fetched")
	}
	if _, loaded := gotURLs.Load("http://up/b.jpg"); !loaded {
		t.Fatal("pending page was not fetched")
	}
}

func TestSubmitAfterErrorRestartsJob(t *testing.T) {
	o := testOptions(t)
	var fail atomic.Bool
	fail.Store(true)
	o.Fetch = func(_ context.Context, _ Page) ([]byte, string, error) {
		if fail.Load() {
			return nil, "", errors.New("boom")
		}
		return []byte("ok"), "image/jpeg", nil
	}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if _, _, err := m.Submit(simpleJob("restart", "http://up/1.jpg")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m.List(), "restart")
		return v.Status == StatusError
	})
	fail.Store(false)
	view, existed, err := m.Submit(simpleJob("restart", "http://up/1.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if existed || view.Status != StatusQueued {
		t.Fatalf("existed=%v status=%s", existed, view.Status)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m.List(), "restart")
		return v.Status == StatusDone
	})
}

func TestStatsAndConcurrency(t *testing.T) {
	o := testOptions(t)
	release := make(chan struct{})
	var inflight atomic.Int64
	var maxInflight atomic.Int64
	o.Fetch = func(_ context.Context, _ Page) ([]byte, string, error) {
		cur := inflight.Add(1)
		for {
			old := maxInflight.Load()
			if cur <= old || maxInflight.CompareAndSwap(old, cur) {
				break
			}
		}
		<-release
		inflight.Add(-1)
		return []byte("ok"), "image/jpeg", nil
	}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	for i := 0; i < 4; i++ {
		j := simpleJob(fmt.Sprintf("c-%d", i), "http://up/1.jpg")
		j.ComicID = fmt.Sprintf("%d", i)
		if _, _, err := m.Submit(j); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 2*time.Second, func() bool { return maxInflight.Load() >= 2 })
	queued, running := m.Stats()
	if running != 2 || queued != 2 {
		t.Fatalf("stats queued=%d running=%d", queued, running)
	}
	close(release)
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m.List(), "c-3")
		return v.Status == StatusDone
	})
	if maxInflight.Load() > 2 {
		t.Fatalf("max inflight = %d, want <= 2", maxInflight.Load())
	}
}

// TestPageExtWhereContentTypeWins pins the extension inference rules.
func TestPageExt(t *testing.T) {
	cases := []struct {
		url, ct, want string
	}{
		{"http://u/a.jpg", "image/jpeg", "jpg"},
		{"http://u/a.JPEG", "", "jpg"},
		{"http://u/a.png", "image/jpeg", "png"},
		{"http://u/a", "image/png", "png"},
		{"http://u/a", "image/webp", "webp"},
		{"http://u/a", "image/gif", "gif"},
		{"http://u/a", "application/octet-stream", "jpg"},
		{"http://u/a", "", "jpg"},
		{"http://u/a.xyz", "image/jpeg", "jpg"},
	}
	for _, c := range cases {
		if got := pageExt(c.url, c.ct); got != c.want {
			t.Errorf("pageExt(%q, %q) = %q, want %q", c.url, c.ct, got, c.want)
		}
	}
}

// TestMaxPageBytes guards the per-page size limit.
func TestMaxPageBytes(t *testing.T) {
	o := testOptions(t)
	big := bytes1KB()
	o.Fetch = func(_ context.Context, _ Page) ([]byte, string, error) {
		return big, "image/jpeg", nil
	}
	o.MaxPageBytes = 1024
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, _, err := m.Submit(simpleJob("big", "http://up/1.jpg")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m.List(), "big")
		return v.Status == StatusError
	})
	v, _ := findByID(m.List(), "big")
	if !contains(v.Error, "exceeds") {
		t.Fatalf("error = %q", v.Error)
	}
}

func bytes1KB() []byte {
	return make([]byte, 1025)
}

// TestUpstreamHeadersPassed verifies the fetcher receives page-level headers.
func TestUpstreamHeadersPassed(t *testing.T) {
	var mu sync.Mutex
	var gotReferer, gotCookie string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotReferer = r.Header.Get("Referer")
		gotCookie = r.Header.Get("Cookie")
		mu.Unlock()
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("img"))
	}))
	defer up.Close()

	o := testOptions(t)
	o.Fetch = func(ctx context.Context, p Page) ([]byte, string, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
		for k, v := range p.Headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return b, resp.Header.Get("Content-Type"), nil
	}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	j := simpleJob("hdrs", up.URL+"/1.jpg")
	j.Pages[0].Headers = map[string]string{
		"Referer": "https://source.example/",
		"Cookie":  "sid=abc",
	}
	if _, _, err := m.Submit(j); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, _ := findByID(m.List(), "hdrs")
		return v.Status == StatusDone
	})
	mu.Lock()
	defer mu.Unlock()
	if gotReferer != "https://source.example/" || gotCookie != "sid=abc" {
		t.Fatalf("referer=%q cookie=%q", gotReferer, gotCookie)
	}
}

func TestJobViewExposesFiles(t *testing.T) {
	j := Job{ID: "x", Pages: make([]Page, 3), States: []PageState{
		{Status: PageOK, File: "001.png"},
		{Status: PageFailed, Error: "boom"},
		{Status: PageOK, File: "003.jpg"},
	}}
	v := viewFromJob(j, "nh/1/0")
	if v.Done != 2 || v.Failed != 1 || v.Total != 3 {
		t.Fatalf("counts = %+v", v)
	}
	if len(v.Files) != 2 || v.Files[0] != "001.png" || v.Files[1] != "003.jpg" {
		t.Fatalf("files = %v", v.Files)
	}
}
