package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"picagateway/internal/config"
	"picagateway/internal/imgproxy"
)

// postJSON sends body as JSON to path using the env's cookie-carrying client.
func postJSON(t *testing.T, e *testEnv, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case string:
		reader = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(http.MethodPost, e.gw.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func signPost(t *testing.T, e *testEnv, body any) (string, string) {
	t.Helper()
	resp, raw := postJSON(t, e, "/api/img/sign", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign status = %d body=%q", resp.StatusCode, raw)
	}
	var doc struct {
		OK  bool   `json:"ok"`
		URL string `json:"url"`
		Exp string `json:"expiresAt"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.OK || doc.URL == "" || doc.Exp == "" {
		t.Fatalf("sign response = %+v", doc)
	}
	return doc.URL, strings.TrimPrefix(doc.URL, "/api/img/")
}

// TestImageSignPostReplaysHeadersAndIsCacheable verifies the browser-facing
// contract: deterministic URL, sanitized header replay, MISS/HIT caching and
// a private Cache-Control lifetime bounded by the token.
func TestImageSignPostReplaysHeadersAndIsCacheable(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n0123456789abcdef")
	var hits atomic.Int64
	var lastHeaders http.Header
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		lastHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(png)))
		_, _ = w.Write(png)
	}))
	defer up.Close()

	// The gateway-level userAgent override would mask the replayed token
	// header; clear it so this test observes the token's own User-Agent.
	e := newTestEnv(t, func(c *config.Config) { c.UserAgent = "" })
	body := map[string]any{
		"url": up.URL + "/pic.png",
		"headers": map[string]string{
			"User-Agent":      "Dalvik/2.1.0 (Linux; Android 10; K)",
			"Referer":         "https://localhost/",
			"Cookie":          "sid=must-not-leak",
			"Host":            "evil.example",
			"X-Forwarded-For": "1.2.3.4",
			"Accept-Encoding": "br",
			"Connection":      "keep-alive",
		},
	}

	signedURL, token := signPost(t, e, body)
	again, _ := signPost(t, e, body)
	if signedURL != again {
		t.Fatalf("sign is not deterministic:\n%s\n%s", signedURL, again)
	}

	payload, err := e.srv.signer.Verify(token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Headers) != 2 ||
		payload.Headers["User-Agent"] == "" || payload.Headers["Referer"] == "" {
		t.Fatalf("payload headers = %v", payload.Headers)
	}
	for _, blocked := range []string{"Cookie", "Host", "X-Forwarded-For", "Accept-Encoding", "Connection"} {
		if _, ok := payload.Headers[blocked]; ok {
			t.Fatalf("blocked header %s was signed", blocked)
		}
	}

	get := func() (*http.Response, []byte) {
		t.Helper()
		resp, err := e.client.Get(e.gw.URL + signedURL)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp, raw
	}

	resp, got := get()
	if resp.StatusCode != http.StatusOK || !bytes.Equal(got, png) {
		t.Fatalf("first image = %d len=%d", resp.StatusCode, len(got))
	}
	if got := resp.Header.Get("X-Cache"); got != "MISS" {
		t.Fatalf("X-Cache = %q, want MISS", got)
	}
	cacheControl := resp.Header.Get("Cache-Control")
	if !strings.HasPrefix(cacheControl, "private, max-age=") {
		t.Fatalf("Cache-Control = %q", cacheControl)
	}
	maxAge, err := strconv.Atoi(strings.TrimPrefix(cacheControl, "private, max-age="))
	if err != nil || maxAge <= 0 || maxAge > 86400 {
		t.Fatalf("Cache-Control max-age = %d err=%v", maxAge, err)
	}

	if got := lastHeaders.Get("Referer"); got != "https://localhost/" {
		t.Fatalf("upstream Referer = %q", got)
	}
	if got := lastHeaders.Get("User-Agent"); got != "Dalvik/2.1.0 (Linux; Android 10; K)" {
		t.Fatalf("upstream User-Agent = %q", got)
	}
	if got := lastHeaders.Get("Cookie"); got != "" {
		t.Fatalf("upstream Cookie = %q", got)
	}
	if got := lastHeaders.Get("X-Forwarded-For"); got != "" {
		t.Fatalf("upstream X-Forwarded-For = %q", got)
	}

	resp, got = get()
	if resp.StatusCode != http.StatusOK || !bytes.Equal(got, png) {
		t.Fatalf("cached image = %d len=%d", resp.StatusCode, len(got))
	}
	if got := resp.Header.Get("X-Cache"); got != "HIT" {
		t.Fatalf("second X-Cache = %q, want HIT", got)
	}
	if got := resp.Header.Get("Cache-Control"); !strings.HasPrefix(got, "private, max-age=") {
		t.Fatalf("cached Cache-Control = %q", got)
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream hits = %d, want 1", hits.Load())
	}
}

// Headers are part of the signature and the disk cache key: the same URL
// fetched with a different Referer must not reuse another variant's bytes.
func TestImageSignHeadersAffectCacheKey(t *testing.T) {
	var hits atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png-" + r.Header.Get("Referer")))
	}))
	defer up.Close()

	e := newTestEnv(t, nil)
	first, _ := signPost(t, e, map[string]any{
		"url": up.URL + "/x.png", "headers": map[string]string{"Referer": "https://a/"},
	})
	second, _ := signPost(t, e, map[string]any{
		"url": up.URL + "/x.png", "headers": map[string]string{"Referer": "https://b/"},
	})
	if first == second {
		t.Fatal("tokens with different headers must differ")
	}

	for _, signed := range []string{first, second} {
		resp, err := e.client.Get(e.gw.URL + signed)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if got := resp.Header.Get("X-Cache"); got != "MISS" {
			t.Fatalf("X-Cache = %q, want MISS", got)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("upstream hits = %d, want 2", hits.Load())
	}

	// The same header set signs to the same URL and hits the disk cache.
	third, _ := signPost(t, e, map[string]any{
		"url": up.URL + "/x.png", "headers": map[string]string{"Referer": "https://a/"},
	})
	if third != first {
		t.Fatal("same headers must sign identically")
	}
	resp, err := e.client.Get(e.gw.URL + third)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if got := resp.Header.Get("X-Cache"); got != "HIT" {
		t.Fatalf("X-Cache = %q, want HIT", got)
	}
}

func TestImageSignPostAuthAndValidation(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.Password = "s3cret" })

	// Unauthenticated requests are rejected before parsing the body.
	resp, _ := postJSON(t, e, "/api/img/sign", map[string]any{"url": "https://example.com/a.png"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", resp.StatusCode)
	}

	loginReq, _ := http.NewRequest(http.MethodPost, e.gw.URL+"/api/auth/login",
		strings.NewReader(`{"password":"s3cret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp, err := e.client.Do(loginReq)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, loginResp.Body)
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", loginResp.StatusCode)
	}

	cases := []struct {
		name string
		body any
		want int
		code string
	}{
		{"missing url", map[string]any{}, http.StatusBadRequest, "missing_url"},
		{"empty body", "", http.StatusBadRequest, "missing_url"},
		{"invalid json", "{", http.StatusBadRequest, "invalid_json"},
		{"bad ttl", map[string]any{"url": "https://example.com/a.png", "ttl": 0}, http.StatusBadRequest, "invalid_ttl"},
		{"negative ttl", map[string]any{"url": "https://example.com/a.png", "ttl": -5}, http.StatusBadRequest, "invalid_ttl"},
		{"unknown profile", map[string]any{"url": "https://example.com/a.png", "profile": "nope"}, http.StatusBadRequest, "unknown_profile"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, raw := postJSON(t, e, "/api/img/sign", c.body)
			if resp.StatusCode != c.want {
				t.Fatalf("status = %d body=%q", resp.StatusCode, raw)
			}
			var doc struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(raw, &doc)
			if doc.Error != c.code {
				t.Fatalf("error = %q, want %q (body=%q)", doc.Error, c.code, raw)
			}
		})
	}

	// The legacy GET endpoint keeps working with the same signer.
	getResp, err := e.client.Get(e.gw.URL + "/api/img/sign?url=" + "https%3A%2F%2Fexample.com%2Fa.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, getResp.Body)
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET sign status = %d", getResp.StatusCode)
	}
}

func TestImageTokenHeadersDoNotBypassCookieVault(t *testing.T) {
	// A token signed with a raw Cookie header must still be sanitized on
	// verification (SignPayload filters, Verify filters again).
	s := imgproxy.NewSigner("test-master-secret")
	token, _ := s.SignPayload(imgproxy.Payload{
		URL:     "https://example.com/a.png",
		Headers: map[string]string{"Cookie": "sid=1"},
	}, 0, time.Now())
	p, err := s.Verify(token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Headers) != 0 {
		t.Fatalf("headers = %v", p.Headers)
	}
}
