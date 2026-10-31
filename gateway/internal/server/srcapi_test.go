package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"picagateway/internal/imgproxy"
	"picagateway/internal/jmimage"
	"picagateway/internal/srcapi"
)

func TestSrcAPISources(t *testing.T) {
	e := newTestEnv(t, nil)
	resp, err := e.client.Get(e.gw.URL + "/api/src/sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var doc struct {
		OK      bool `json:"ok"`
		Sources []struct {
			Key       string `json:"key"`
			Name      string `json:"name"`
			NeedLogin bool   `json:"needLogin"`
			LoggedIn  bool   `json:"loggedIn"`
		} `json:"sources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if !doc.OK || len(doc.Sources) != 2 {
		t.Fatalf("doc = %+v", doc)
	}
	if doc.Sources[0].Key != "jm" || doc.Sources[0].Name != "禁漫天堂" || doc.Sources[0].NeedLogin || doc.Sources[0].LoggedIn {
		t.Fatalf("jm entry = %+v", doc.Sources[0])
	}
	if doc.Sources[1].Key != "picacg" || doc.Sources[1].Name != "PicACG" || !doc.Sources[1].NeedLogin || doc.Sources[1].LoggedIn {
		t.Fatalf("picacg entry = %+v", doc.Sources[1])
	}
}

func TestSrcAPIUnknownSourceAndTabs(t *testing.T) {
	e := newTestEnv(t, nil)
	resp, err := e.client.Get(e.gw.URL + "/api/src/nope/tabs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Pica-Error"); got != "unknown_source" {
		t.Fatalf("X-Pica-Error = %q", got)
	}

	resp2, err := e.client.Get(e.gw.URL + "/api/src/jm/tabs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	body := map[string]any{}
	_ = json.NewDecoder(resp2.Body).Decode(&body)
	tabs, _ := body["tabs"].([]any)
	if len(tabs) != 2 {
		t.Fatalf("jm tabs = %v", tabs)
	}
}

func TestSrcAPIPicacgUnauthorized(t *testing.T) {
	e := newTestEnv(t, nil)
	resp, err := e.client.Get(e.gw.URL + "/api/src/picacg/explore?tab=latest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Pica-Error"); got != "login_required" {
		t.Fatalf("X-Pica-Error = %q", got)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["ok"] != false || body["error"] == nil {
		t.Fatalf("error body = %v", body)
	}
}

func TestSrcAPIJMLoginNotSupported(t *testing.T) {
	e := newTestEnv(t, nil)
	resp, err := e.client.Post(e.gw.URL+"/api/src/jm/login", "application/json",
		bytes.NewReader([]byte(`{"username":"a","password":"b"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Pica-Error"); got != "not_supported" {
		t.Fatalf("X-Pica-Error = %q", got)
	}
}

func TestSrcAPIExploreInvalidPage(t *testing.T) {
	e := newTestEnv(t, nil)
	resp, err := e.client.Get(e.gw.URL + "/api/src/jm/explore?tab=latest&page=zero")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// TestImageJMRecombine drives the whole /api/img pipeline for a JM token: on a
// cache MISS the scrambled JPEG is recombined (and cached); the HIT serves the
// same recombined bytes.
func TestImageJMRecombine(t *testing.T) {
	// Build an original image, then produce the scrambled variant by applying
	// the same block reversal JM uses (reversing twice restores the layout).
	const width, height = 64, 210
	src := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			src.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y), B: 0x80, A: 0xff})
		}
	}
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	original := raw.Bytes()

	const epsID, scrambleID = 250000, 100000 // 250000 >= 100000 and < 268850 -> 10 blocks
	if got := jmimage.SegmentationNum(epsID, scrambleID, "00001"); got != 10 {
		t.Fatalf("segmentation num = %d, want 10", got)
	}
	scrambled, err := jmimage.Recombine(original, 10)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(scrambled, original) {
		t.Fatal("scrambled variant is identical to the original")
	}

	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The jm profile headers must reach the image CDN.
		if r.Header.Get("Referer") != "https://localhost/" || r.Header.Get("X-Requested-With") != "com.example.app" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(scrambled)
	}))
	t.Cleanup(imgSrv.Close)

	e := newTestEnv(t, nil)
	token, _ := e.srv.signer.SignPayload(imgproxy.Payload{
		URL:     imgSrv.URL + "/media/photos/250000/00001.jpg",
		Profile: "jm",
		JM:      &imgproxy.JMParams{EpsID: "250000", ScrambleID: "100000", BookID: "00001"},
	}, time.Hour, time.Now())

	fetch := func() *http.Response {
		t.Helper()
		resp, err := e.client.Get(e.gw.URL + "/api/img/" + token)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// MISS: recombined.
	resp := fetch()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("content type = %q", got)
	}
	if got := resp.Header.Get("X-Cache"); got != "MISS" {
		t.Fatalf("first X-Cache = %q", got)
	}
	var got bytes.Buffer
	if _, err := got.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	want, err := jmimage.Recombine(scrambled, 10) // unscrambled == original layout
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("MISS body is not the recombined image (%d vs %d bytes)", got.Len(), len(want))
	}
	if bytes.Equal(got.Bytes(), scrambled) {
		t.Fatal("served the scrambled bytes unchanged")
	}
	// The recombined result decodes to the original geometry.
	decoded, _, err := image.Decode(bytes.NewReader(got.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != width || decoded.Bounds().Dy() != height {
		t.Fatalf("decoded bounds = %v", decoded.Bounds())
	}

	// HIT: same bytes from the cache.
	resp2 := fetch()
	defer resp2.Body.Close()
	if got := resp2.Header.Get("X-Cache"); got != "HIT" {
		t.Fatalf("second X-Cache = %q", got)
	}
	var cached bytes.Buffer
	if _, err := cached.ReadFrom(resp2.Body); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cached.Bytes(), got.Bytes()) {
		t.Fatal("HIT bytes differ from MISS bytes")
	}
}

// TestImageJMRecombinesWebP checks WebP payloads with jm params are
// recombined too. JM serves most pages as WebP and those files are scrambled
// exactly like the JPEGs (verified against live chapters), so passing them
// through unchanged would show broken pages on /v2. The fixture is a
// lossless 8x24 WebP holding 12 solid gray stripes in reverse order.
func TestImageJMRecombinesWebP(t *testing.T) {
	const scrambledWebP = "UklGRkYAAABXRUJQVlA4TDoAAAAvB8AFAF8gFkwkdudPGsMpPE7hMf/Z4YCQtp1MYQpTmEIKKaSQQgoppBDfdYjof34el8NmMRk0RRIA"
	webp, err := base64.StdEncoding.DecodeString(scrambledWebP)
	if err != nil {
		t.Fatal(err)
	}
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write(webp)
	}))
	t.Cleanup(imgSrv.Close)

	e := newTestEnv(t, nil)
	// eps 500000 + book 00001 -> 12 blocks of 2px each.
	token, _ := e.srv.signer.SignPayload(imgproxy.Payload{
		URL: imgSrv.URL + "/media/photos/500000/00001.webp",
		JM:  &imgproxy.JMParams{EpsID: "500000", ScrambleID: "220980", BookID: "00001"},
	}, time.Hour, time.Now())

	fetch := func() (*http.Response, []byte) {
		t.Helper()
		resp, err := e.client.Get(e.gw.URL + "/api/img/" + token)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp, raw
	}

	resp, body := fetch()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("content type = %q, want image/jpeg", got)
	}
	if !strings.HasPrefix(resp.Header.Get("Cache-Control"), "private, max-age=") {
		t.Fatalf("cache-control = %q", resp.Header.Get("Cache-Control"))
	}
	img, err := jpeg.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode recombined jpeg: %v", err)
	}
	if img.Bounds().Dx() != 8 || img.Bounds().Dy() != 24 {
		t.Fatalf("bounds = %v, want 8x24", img.Bounds())
	}
	// Stripe i (2px) must carry gray value 30+i*20 in the original order.
	for i := 0; i < 12; i++ {
		want := uint8(30 + i*20)
		r, g, b, _ := img.At(4, i*2+1).RGBA()
		for _, got := range []uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)} {
			diff := int(got) - int(want)
			if diff < -6 || diff > 6 {
				t.Fatalf("stripe %d color = (%d,%d,%d), want %d", i, r>>8, g>>8, b>>8, want)
			}
		}
	}

	resp2, body2 := fetch()
	if got := resp2.Header.Get("X-Cache"); got != "HIT" {
		t.Fatalf("second X-Cache = %q", got)
	}
	if !bytes.Equal(body, body2) {
		t.Fatal("cached recombined image differs")
	}
}

func TestV2Static(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("VUE-INDEX"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.1234.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}

	e := newTestEnv(t, func(c *config.Config) { c.WebDir2 = dir })

	get := func(path string) *http.Response {
		t.Helper()
		resp, err := e.client.Get(e.gw.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// Root of /v2 serves the SPA shell.
	resp := get("/v2/")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v2/ status = %d", resp.StatusCode)
	}
	buf := make([]byte, len("VUE-INDEX"))
	_, _ = resp.Body.Read(buf)
	if string(buf) != "VUE-INDEX" {
		t.Fatalf("index body = %q", buf)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("index cache-control = %q", got)
	}

	// /v2 (no trailing slash) also serves the shell.
	resp2 := get("/v2")
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("GET /v2 status = %d", resp2.StatusCode)
	}

	// Immutable asset caching.
	resp3 := get("/v2/assets/app.1234.js")
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("asset status = %d", resp3.StatusCode)
	}
	if got := resp3.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset cache-control = %q", got)
	}

	// SPA fallback for a client route.
	resp4 := get("/v2/comic/jm/123")
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusOK {
		t.Fatalf("spa route status = %d", resp4.StatusCode)
	}
	fallback := make([]byte, len("VUE-INDEX"))
	_, _ = resp4.Body.Read(fallback)
	if string(fallback) != "VUE-INDEX" {
		t.Fatalf("spa fallback body = %q", fallback)
	}

	// Unknown asset-looking paths 404 instead of returning HTML.
	resp5 := get("/v2/assets/missing.js")
	defer resp5.Body.Close()
	if resp5.StatusCode != http.StatusNotFound {
		t.Fatalf("missing asset status = %d", resp5.StatusCode)
	}

	// Method guard.
	req, _ := http.NewRequest(http.MethodPost, e.gw.URL+"/v2/", nil)
	resp6, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp6.Body.Close()
	if resp6.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /v2/ status = %d", resp6.StatusCode)
	}
}

func TestV2StaticMissingBuild(t *testing.T) {
	e := newTestEnv(t, nil) // webDir2 default points at a nonexistent temp path
	resp, err := e.client.Get(e.gw.URL + "/v2/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// ---- semantic-source download endpoint ----

// fakeDownloadSource is a minimal Source+Downloader for the endpoint tests.
type fakeDownloadSource struct {
	key   string
	comic srcapi.Comic
	pages []downloads.Page
	err   error

	mu  sync.Mutex
	eps []string
}

func (f *fakeDownloadSource) Key() string                   { return f.key }
func (f *fakeDownloadSource) Name() string                  { return f.key }
func (f *fakeDownloadSource) NeedLogin() bool               { return false }
func (f *fakeDownloadSource) LoggedIn(context.Context) bool { return true }
func (f *fakeDownloadSource) Tabs() []srcapi.Tab            { return nil }

func (f *fakeDownloadSource) Explore(context.Context, string, int) (*srcapi.ListResult, error) {
	return &srcapi.ListResult{}, nil
}

func (f *fakeDownloadSource) Search(context.Context, string, string, int) (*srcapi.ListResult, error) {
	return &srcapi.ListResult{}, nil
}

func (f *fakeDownloadSource) Comic(context.Context, string) (*srcapi.Comic, error) {
	comic := f.comic
	return &comic, nil
}

func (f *fakeDownloadSource) Pages(context.Context, string, string) ([]string, error) {
	return nil, nil
}

func (f *fakeDownloadSource) Login(context.Context, string, string) (*srcapi.LoginResult, error) {
	return &srcapi.LoginResult{Name: "fake"}, nil
}

func (f *fakeDownloadSource) Logout(context.Context) error { return nil }

func (f *fakeDownloadSource) DownloadPages(_ context.Context, comicID, ep string) ([]downloads.Page, error) {
	f.mu.Lock()
	f.eps = append(f.eps, ep)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.pages, nil
}

func (f *fakeDownloadSource) downloadedEps() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.eps...)
}

func TestSrcAPIDownloadEndpoint(t *testing.T) {
	var hits atomic.Int64
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	t.Cleanup(imgSrv.Close)

	fake := &fakeDownloadSource{
		key: "jm",
		comic: srcapi.Comic{
			ComicBrief: srcapi.ComicBrief{ID: "42", Title: "Fake Comic"},
			Chapters:   []srcapi.Chapter{{ID: "250000", Title: "第一話", Order: 1}},
		},
		pages: []downloads.Page{
			{URL: imgSrv.URL + "/001.png", Headers: map[string]string{"Referer": "https://localhost/"},
				JM: &downloads.JMInfo{EpsID: "250000", ScrambleID: "220980", BookID: "001"}},
			{URL: imgSrv.URL + "/002.png"},
		},
	}
	e := newTestEnvWith(t, nil, func(o *Options) { o.Sources = srcapi.NewRegistryFrom(fake) })

	post := func(body string) (int, map[string]any) {
		t.Helper()
		resp, err := e.client.Post(e.gw.URL+"/api/src/jm/comic/42/download", "application/json",
			bytes.NewReader([]byte(body)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var doc map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, doc
	}
	decodeView := func(doc map[string]any) downloads.JobView {
		t.Helper()
		raw, err := json.Marshal(doc["job"])
		if err != nil {
			t.Fatal(err)
		}
		var view downloads.JobView
		if err := json.Unmarshal(raw, &view); err != nil {
			t.Fatal(err)
		}
		return view
	}

	status, doc := post(`{}`)
	if status != http.StatusOK || doc["ok"] != true || doc["existed"] != false {
		t.Fatalf("submit = %d %+v", status, doc)
	}
	view := decodeView(doc)
	if view.ID != "jm-42-250000" || view.Title != "Fake Comic" || view.Source != "jm" ||
		view.ComicID != "42" || view.EP != "250000" || view.EPName != "第一話" ||
		view.Dir != "jm/42/250000" || view.Total != 2 {
		t.Fatalf("job view = %+v", view)
	}
	if eps := fake.downloadedEps(); len(eps) != 1 || eps[0] != "250000" {
		t.Fatalf("DownloadPages eps = %v", eps)
	}

	// The job is persisted and visible through the downloads list API.
	dlWaitFor(t, 5*time.Second, func() bool { return dlJob(t, e, "jm-42-250000").Status == "done" })
	job := dlJob(t, e, "jm-42-250000")
	if job.Done != 2 || job.Failed != 0 || job.Error != "" {
		t.Fatalf("job = %+v", job)
	}
	if _, err := os.Stat(filepath.Join(e.cfg.DownloadsDir, "jm", "42", "250000", "job.json")); err != nil {
		t.Fatalf("job.json: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream hits = %d, want 2", got)
	}

	// Idempotent resubmission: 200 + existed, nothing re-downloaded.
	status, doc = post(`{"ep":"250000","epName":"ignored"}`)
	if status != http.StatusOK || doc["existed"] != true {
		t.Fatalf("resubmit = %d %+v", status, doc)
	}
	view = decodeView(doc)
	if view.Status != "done" || view.Done != 2 || view.EPName != "第一話" {
		t.Fatalf("resubmit view = %+v", view)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream hits after resubmit = %d, want 2", got)
	}
}

func TestSrcAPIDownloadSourceErrors(t *testing.T) {
	comic := srcapi.Comic{
		ComicBrief: srcapi.ComicBrief{ID: "c1", Title: "T"},
		Chapters:   []srcapi.Chapter{{ID: "1", Title: "第1话", Order: 1}},
	}
	badEp := &fakeDownloadSource{
		key: "picacg", comic: comic,
		err: &srcapi.StatusError{Status: http.StatusBadRequest, Code: "invalid_ep", Message: "bad ep"},
	}
	upstream := &fakeDownloadSource{
		key: "boom", comic: comic,
		err: errors.New("connection reset"),
	}
	e := newTestEnvWith(t, nil, func(o *Options) {
		o.Sources = srcapi.NewRegistryFrom(badEp, upstream)
	})

	post := func(path string) (int, string) {
		t.Helper()
		resp, err := e.client.Post(e.gw.URL+path, "application/json", bytes.NewReader([]byte(`{"ep":"x"}`)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var doc map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&doc)
		code, _ := doc["code"].(string)
		return resp.StatusCode, code
	}

	if status, code := post("/api/src/picacg/comic/c1/download"); status != http.StatusBadRequest || code != "invalid_ep" {
		t.Fatalf("invalid ep = %d %q", status, code)
	}
	if status, code := post("/api/src/boom/comic/c1/download"); status != http.StatusBadGateway || code != srcapi.CodeUpstreamError {
		t.Fatalf("upstream error = %d %q", status, code)
	}
	if jobs := dlList(t, e); len(jobs) != 0 {
		t.Fatalf("failed submissions left jobs: %+v", jobs)
	}
}

func TestSrcAPIDownloadPageLimit(t *testing.T) {
	pages := make([]downloads.Page, downloads.MaxPages+1)
	for i := range pages {
		pages[i] = downloads.Page{URL: "https://example.com/001.jpg"}
	}
	fake := &fakeDownloadSource{
		key: "jm",
		comic: srcapi.Comic{
			ComicBrief: srcapi.ComicBrief{ID: "c1", Title: "T"},
			Chapters:   []srcapi.Chapter{{ID: "1", Title: "第1话", Order: 1}},
		},
		pages: pages,
	}
	e := newTestEnvWith(t, nil, func(o *Options) { o.Sources = srcapi.NewRegistryFrom(fake) })

	resp, err := e.client.Post(e.gw.URL+"/api/src/jm/comic/c1/download", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var doc map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	if resp.StatusCode != http.StatusBadRequest || doc["code"] != "invalid_job" {
		t.Fatalf("page limit = %d %+v", resp.StatusCode, doc)
	}
}

func TestSrcAPIDownloadAuthAndUnknownSource(t *testing.T) {
	e := newTestEnv(t, nil)

	post := func(path string) *http.Response {
		t.Helper()
		resp, err := e.client.Post(e.gw.URL+path, "application/json", bytes.NewReader([]byte(`{"ep":"1"}`)))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}

	if resp := post("/api/src/nope/comic/1/download"); resp.StatusCode != http.StatusNotFound ||
		resp.Header.Get("X-Pica-Error") != "unknown_source" {
		t.Fatalf("unknown source = %d %q", resp.StatusCode, resp.Header.Get("X-Pica-Error"))
	}
	// The real picacg source implements Downloader but needs credentials.
	if resp := post("/api/src/picacg/comic/c1/download"); resp.StatusCode != http.StatusUnauthorized ||
		resp.Header.Get("X-Pica-Error") != "login_required" {
		t.Fatalf("picacg without creds = %d %q", resp.StatusCode, resp.Header.Get("X-Pica-Error"))
	}
	if jobs := dlList(t, e); len(jobs) != 0 {
		t.Fatalf("unauthorized submission left jobs: %+v", jobs)
	}
}
