package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"picagateway/internal/config"
	"picagateway/internal/imgproxy"
	"picagateway/internal/jmimage"
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

// TestImageJMPassthroughNonJPEG checks webp payloads with jm params are served
// unchanged (JM now ships webp for most albums; recombine never applies).
func TestImageJMPassthroughNonJPEG(t *testing.T) {
	webp := []byte("RIFF....WEBPvp8 fake payload")
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write(webp)
	}))
	t.Cleanup(imgSrv.Close)

	e := newTestEnv(t, nil)
	token, _ := e.srv.signer.SignPayload(imgproxy.Payload{
		URL: imgSrv.URL + "/media/photos/500000/00001.webp",
		JM:  &imgproxy.JMParams{EpsID: "500000", ScrambleID: "220980", BookID: "00001"},
	}, time.Hour, time.Now())
	resp, err := e.client.Get(e.gw.URL + "/api/img/" + token)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/webp" {
		t.Fatalf("content type = %q", got)
	}
	var body bytes.Buffer
	_, _ = body.ReadFrom(resp.Body)
	if !bytes.Equal(body.Bytes(), webp) {
		t.Fatal("webp payload was modified")
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
