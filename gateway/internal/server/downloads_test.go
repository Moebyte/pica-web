package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"picagateway/internal/config"
	"picagateway/internal/downloads"
)

// pngBytes is a tiny valid PNG (1x1 px).
var pngBytes = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func dlWaitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// dlList fetches the job list.
func dlList(t *testing.T, e *testEnv) []downloads.JobView {
	t.Helper()
	resp, err := e.client.Get(e.gw.URL + "/api/downloads/jobs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	var payload struct {
		OK   bool                `json:"ok"`
		Jobs []downloads.JobView `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload.Jobs
}

func dlJob(t *testing.T, e *testEnv, id string) downloads.JobView {
	t.Helper()
	for _, j := range dlList(t, e) {
		if j.ID == id {
			return j
		}
	}
	t.Fatalf("job %q not found", id)
	return downloads.JobView{}
}

// dlSubmit posts a job payload and decodes the response.
func dlSubmit(t *testing.T, e *testEnv, body string) (int, downloads.JobView, string) {
	t.Helper()
	resp, err := e.client.Post(e.gw.URL+"/api/downloads/jobs", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var payload struct {
		OK  bool              `json:"ok"`
		Job downloads.JobView `json:"job"`
	}
	_ = json.Unmarshal(raw, &payload)
	return resp.StatusCode, payload.Job, string(raw)
}

func dlPayload(id string, pages string) string {
	return `{"id":"` + id + `","title":"T","source":"nh","comicId":"42","ep":"1","epName":"e1",` +
		`"pages":[` + pages + `],"createdAt":1690000000}`
}

func TestDownloadsJobLifecycle(t *testing.T) {
	var hits atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer up.Close()

	e := newTestEnv(t, nil)

	status, view, raw := dlSubmit(t, e, dlPayload("nh-42-1",
		`{"url":"`+up.URL+`/1.png"},{"url":"`+up.URL+`/2.png"}`))
	if status != http.StatusOK || view.Dir != "nh/42/1" || raw == "" {
		t.Fatalf("submit = %d %s raw=%s", status, view.Status, raw)
	}
	dlWaitFor(t, 5*time.Second, func() bool { return dlJob(t, e, "nh-42-1").Status == "done" })
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream hits = %d, want 2", got)
	}

	job := dlJob(t, e, "nh-42-1")
	if job.Total != 2 || job.Done != 2 || job.Failed != 0 || job.Error != "" {
		t.Fatalf("job = %+v", job)
	}

	// Idempotent resubmission: 200, no new fetches.
	status, view2, _ := dlSubmit(t, e, dlPayload("nh-42-1", `{"url":"`+up.URL+`/1.png"}`))
	if status != http.StatusOK || view2.Status != "done" || view2.Done != 2 {
		t.Fatalf("resubmit = %d %+v", status, view2)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream hits after resubmit = %d, want 2", got)
	}

	// Fetch a page file back.
	fetch := func(headers map[string]string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, e.gw.URL+"/api/downloads/file?path=nh/42/1/001.png", nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := fetch(nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, pngBytes) {
		t.Fatalf("file = %d len=%d", resp.StatusCode, len(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type = %q", ct)
	}

	resp = fetch(map[string]string{"Range": "bytes=0-9"})
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(body, pngBytes[:10]) {
		t.Fatalf("range = %d len=%d", resp.StatusCode, len(body))
	}

	// jobs list ordering: newest first (same createdAt here, id desc as fallback).
	if jobs := dlList(t, e); len(jobs) != 1 {
		t.Fatalf("jobs = %+v", jobs)
	}

	// Delete without files: files survive.
	req, _ := http.NewRequest(http.MethodDelete, e.gw.URL+"/api/downloads/jobs?id=nh-42-1", nil)
	delResp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, delResp.Body)
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete = %d", delResp.StatusCode)
	}
	if jobs := dlList(t, e); len(jobs) != 0 {
		t.Fatalf("jobs after delete = %+v", jobs)
	}
	resp = fetch(nil)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("file after delete(files=false) = %d", resp.StatusCode)
	}

	// Deleting an unknown job is 404.
	req, _ = http.NewRequest(http.MethodDelete, e.gw.URL+"/api/downloads/jobs?id=nope", nil)
	delResp, _ = e.client.Do(req)
	_, _ = io.Copy(io.Discard, delResp.Body)
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete missing = %d", delResp.StatusCode)
	}
}

func TestDownloadsCancelRunningJob(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer up.Close()

	e := newTestEnv(t, nil)
	status, _, raw := dlSubmit(t, e, dlPayload("nh-42-1", `{"url":"`+up.URL+`/slow.png"}`))
	if status != http.StatusOK {
		t.Fatalf("submit = %d %s", status, raw)
	}
	<-started
	dlWaitFor(t, 5*time.Second, func() bool { return dlJob(t, e, "nh-42-1").Status == "running" })

	req, _ := http.NewRequest(http.MethodDelete, e.gw.URL+"/api/downloads/jobs?id=nh-42-1&files=true", nil)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	close(block)
	if jobs := dlList(t, e); len(jobs) != 0 {
		t.Fatalf("jobs after cancel = %+v", jobs)
	}
	if _, err := os.Stat(filepath.Join(e.cfg.DownloadsDir, "nh", "42", "1")); !os.IsNotExist(err) {
		t.Fatalf("files still present: %v", err)
	}
}

func TestDownloadsFileTraversal(t *testing.T) {
	e := newTestEnv(t, nil)
	root := e.cfg.DownloadsDir
	dir := filepath.Join(root, "nh", "42", "1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secret, []byte("top"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("out"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.png")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "001.png"), []byte("page"), 0o644); err != nil {
		t.Fatal(err)
	}

	get := func(path string) int {
		resp, err := e.client.Get(e.gw.URL + "/api/downloads/file?path=" + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}

	cases := []struct {
		name, path, wantCode string
	}{
		{"relative traversal", "..%2Fsecret.txt", "400"},
		{"nested traversal", "nh%2F..%2F..%2Fsecret.txt", "400"},
		{"encoded dotdot", "%2e%2e%2fsecret.txt", "400"},
		{"absolute", "%2Fetc%2Fpasswd", "400"},
		{"absolute windows-ish", "C:%2Fetc%2Fpasswd", "200_or_404"}, // no "..": C: is a name -> not found
		{"dot", ".", "400"},
		{"empty", "", "400"},
	}
	for _, c := range cases {
		code := get(c.path)
		switch c.wantCode {
		case "400":
			if code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400", c.name, code)
			}
		default:
			if code != http.StatusNotFound {
				t.Errorf("%s: status = %d, want 404", c.name, code)
			}
		}
	}

	if code := get("nh%2F42%2F1%2Flink.png"); code != http.StatusForbidden {
		t.Errorf("symlink: status = %d, want 403", code)
	}
	if code := get("nh%2F42%2F1"); code != http.StatusBadRequest {
		t.Errorf("directory: status = %d, want 400", code)
	}
	if code := get("nh%2F42%2F1%2F001.png"); code != http.StatusOK {
		t.Errorf("regular file: status = %d, want 200", code)
	}
	if code := get("nh%2F42%2F1%2Fmissing.png"); code != http.StatusNotFound {
		t.Errorf("missing file: status = %d, want 404", code)
	}
}

func TestDownloadsAllowlist(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer up.Close()
	goodURL := strings.Replace(up.URL, "127.0.0.1", "allowed.example", 1)

	e := newTestEnv(t, func(c *config.Config) {
		c.AllowAll = false
		c.AllowHosts = []string{"allowed.example"}
		c.HostOverrides = map[string]string{"allowed.example": "127.0.0.1"}
	})

	// Mixed job: one allowed page, one host outside the allowlist.
	status, _, raw := dlSubmit(t, e, dlPayload("nh-42-1",
		`{"url":"`+goodURL+`/1.png"},{"url":"https://denied.example/2.png"}`))
	if status != http.StatusOK {
		t.Fatalf("submit = %d %s", status, raw)
	}
	dlWaitFor(t, 5*time.Second, func() bool { return dlJob(t, e, "nh-42-1").Status == "partial" })
	job := dlJob(t, e, "nh-42-1")
	if job.Done != 1 || job.Failed != 1 {
		t.Fatalf("job = %+v", job)
	}
	if !strings.Contains(job.Error, "host_not_allowed") {
		t.Fatalf("job error = %q, want host_not_allowed", job.Error)
	}

	// Every denied page keeps its own error message in job.json.
	rawJSON, err := os.ReadFile(filepath.Join(e.cfg.DownloadsDir, "nh", "42", "1", "job.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawJSON), "host_not_allowed") {
		t.Fatalf("job.json does not mention host_not_allowed: %s", rawJSON)
	}
}

func TestDownloadsJMRecombineEndToEnd(t *testing.T) {
	// Build a 10-stripe scrambled JPEG (num=10 for epsId=250000 < 268850).
	const width, height, num = 40, 100, 10
	orig := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < num; i++ {
		v := uint8(30 + i*20)
		for y := i * 10; y < (i+1)*10; y++ {
			for x := 0; x < width; x++ {
				orig.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
			}
		}
	}
	// Scrambled = blocks stacked in reverse.
	scrambled := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := num - 1; i >= 0; i-- {
		dstY := (num - 1 - i) * 10
		for y := 0; y < 10; y++ {
			for x := 0; x < width; x++ {
				scrambled.SetRGBA(x, dstY+y, orig.RGBAAt(x, i*10+y))
			}
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scrambled, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	scrambledBytes := buf.Bytes()

	var mu sync.Mutex
	servedPassthrough := false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		mu.Lock()
		passthrough := servedPassthrough
		mu.Unlock()
		if passthrough {
			_, _ = w.Write(scrambledBytes) // served again below for the num=0 job
			return
		}
		_, _ = w.Write(scrambledBytes)
	}))
	defer up.Close()

	e := newTestEnv(t, nil)
	pages := `{"url":"` + up.URL + `/00001.jpg","jm":{"epsId":"250000","scrambleId":"220880","bookId":"00001"}}`
	jmPayload := `{"id":"jm-123-1","title":"T","source":"jm","comicId":"123","ep":"1","epName":"e1",` +
		`"pages":[` + pages + `],"createdAt":1690000000}`
	status, _, raw := dlSubmit(t, e, jmPayload)
	if status != http.StatusOK {
		t.Fatalf("submit = %d %s", status, raw)
	}
	dlWaitFor(t, 5*time.Second, func() bool { return dlJob(t, e, "jm-123-1").Status == "done" })

	out, err := os.ReadFile(filepath.Join(e.cfg.DownloadsDir, "jm", "123", "1", "001.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("recombined file is not a JPEG: %v", err)
	}
	// Top stripe of the output must be the top stripe of the original.
	r0, g0, b0, _ := got.At(width/2, 5).RGBA()
	or0, og0, ob0, _ := orig.At(width/2, 5).RGBA()
	dr, dg, db := int(r0>>8)-int(or0>>8), int(g0>>8)-int(og0>>8), int(b0>>8)-int(ob0>>8)
	if dr < -4 || dr > 4 || dg < -4 || dg > 4 || db < -4 || db > 4 {
		t.Fatalf("top stripe = (%d,%d,%d), want (%d,%d,%d)", r0>>8, g0>>8, b0>>8, or0>>8, og0>>8, ob0>>8)
	}

	// epsId < scrambleId -> num 0 -> the payload is saved byte-identical.
	status, _, raw = dlSubmit(t, e,
		`{"id":"jm-123-0","title":"T","source":"jm","comicId":"123","ep":"0","epName":"e0","pages":[`+
			`{"url":"`+up.URL+`/00002.jpg","jm":{"epsId":"100","scrambleId":"220880","bookId":"00002"}}]}`)
	if status != http.StatusOK {
		t.Fatalf("submit passthrough = %d %s", status, raw)
	}
	dlWaitFor(t, 5*time.Second, func() bool { return dlJob(t, e, "jm-123-0").Status == "done" })
	out2, err := os.ReadFile(filepath.Join(e.cfg.DownloadsDir, "jm", "123", "0", "001.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out2, scrambledBytes) {
		t.Fatal("num=0 page should be saved byte-identical")
	}
}

func TestDownloadsRestartRecovery(t *testing.T) {
	var hits atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer up.Close()

	dir := filepath.Join(t.TempDir(), "downloads")
	e := newTestEnv(t, func(c *config.Config) { c.DownloadsDir = dir })
	if _, _, raw := dlSubmit(t, e, dlPayload("nh-42-1", `{"url":"`+up.URL+`/1.png"}`)); raw == "" {
		t.Fatal("submit failed")
	}
	dlWaitFor(t, 5*time.Second, func() bool { return dlJob(t, e, "nh-42-1").Status == "done" })

	// Simulate a restart: tear the server down and rebuild over the same dir.
	e.gw.Close()
	e.srv.Close()
	_ = e.vault.Close()

	e2 := newTestEnv(t, func(c *config.Config) { c.DownloadsDir = dir })
	jobs := dlList(t, e2)
	if len(jobs) != 1 || jobs[0].ID != "nh-42-1" || jobs[0].Status != "done" || jobs[0].Done != 1 {
		t.Fatalf("restored jobs = %+v", jobs)
	}
	time.Sleep(100 * time.Millisecond)
	if got := hits.Load(); got != 1 {
		t.Fatalf("done job re-fetched after restart: %d hits", got)
	}
}

func TestDownloadsBodyLimitAndAuth(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(pngBytes)
	}))
	defer up.Close()

	e := newTestEnv(t, func(c *config.Config) { c.Password = "s3cret" })

	// Unauthenticated access is rejected on every downloads endpoint.
	for _, path := range []string{
		"/api/downloads/jobs",
		"/api/downloads/file?path=a/b.png",
		"/api/downloads/healthz",
	} {
		resp, err := e.client.Get(e.gw.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s unauthenticated = %d, want 401", path, resp.StatusCode)
		}
	}

	// Login, then submit an oversized body.
	login, err := e.client.Post(e.gw.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"password":"s3cret"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, login.Body)
	login.Body.Close()

	big := make([]byte, 4<<20+1)
	resp, err := e.client.Post(e.gw.URL+"/api/downloads/jobs", "application/json", bytes.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d, want 413", resp.StatusCode)
	}

	// Invalid job JSON fields.
	status, _, raw := dlSubmit(t, e, `{"id":"x","pages":[]}`)
	if status != http.StatusBadRequest || !strings.Contains(raw, "invalid_job") {
		t.Fatalf("invalid job = %d %s", status, raw)
	}
}

func TestDownloadsHealth(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer up.Close()

	e := newTestEnv(t, nil)
	resp, err := e.client.Get(e.gw.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	var health struct {
		Downloads struct {
			Queued  int `json:"queued"`
			Running int `json:"running"`
		} `json:"downloads"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if health.Downloads.Queued != 0 || health.Downloads.Running != 0 {
		t.Fatalf("health = %+v", health)
	}

	resp2, err := e.client.Get(e.gw.URL + "/api/downloads/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("downloads healthz = %d", resp2.StatusCode)
	}
}
