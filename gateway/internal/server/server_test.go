package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"picagateway/internal/auth"
	"picagateway/internal/config"
	"picagateway/internal/imgproxy"
	"picagateway/internal/outbound"
	"picagateway/internal/ratelimit"
	"picagateway/internal/vault"
)

type testEnv struct {
	cfg    *config.Config
	srv    *Server
	gw     *httptest.Server
	vault  *vault.Vault
	cache  *imgproxy.Cache
	client *http.Client
}

func newTestEnv(t *testing.T, mutate func(*config.Config)) *testEnv {
	t.Helper()
	return newTestEnvWith(t, mutate, nil)
}

// newTestEnvWith builds a test environment and lets the caller adjust the
// server Options (e.g. inject a srcapi.Registry).
func newTestEnvWith(t *testing.T, mutate func(*config.Config), mutateOpts func(*Options)) *testEnv {
	t.Helper()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.CacheDir = filepath.Join(t.TempDir(), "cache")
	cfg.DownloadsDir = filepath.Join(cfg.DataDir, "downloads")
	cfg.WebDir = filepath.Join(t.TempDir(), "web")
	cfg.Secret = "test-master-secret"
	cfg.AllowAll = true
	cfg.Password = ""
	cfg.TimeoutSec = 10
	cfg.CacheTTL = 3600
	cfg.CacheMaxMB = 64
	cfg.MetaSetCookies = false
	if mutate != nil {
		mutate(&cfg)
	}

	if err := os.MkdirAll(filepath.Join(cfg.WebDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.WebDir, "index.html"), []byte("INDEX"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.WebDir, "assets", "app.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := vault.New(filepath.Join(cfg.DataDir, "vault.db"), cfg.Secret)
	if err != nil {
		t.Fatalf("vault.New: %v", err)
	}
	cache, err := imgproxy.New(cfg.CacheDir, cfg.CacheTTLDuration(), cfg.CacheMaxBytes())
	if err != nil {
		t.Fatalf("imgproxy.New: %v", err)
	}
	policy := outbound.New(cfg.AllowHosts, cfg.AllowAll, cfg.HostOverrides, nil)
	policy.AllowLoopback = true // httptest upstreams listen on 127.0.0.1

	opts := Options{
		Config:   &cfg,
		Vault:    v,
		Policy:   policy,
		Cache:    cache,
		Sessions: auth.NewManager(cfg.Secret, 24*time.Hour),
		Signer:   imgproxy.NewSigner(cfg.Secret),
		Limiter:  ratelimit.New(0, 0),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version:  "test",
		// Keep download retry tests fast; production uses 2s/8s/30s.
		DownloadRetryDelays: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
		DownloadPageTimeout: 5 * time.Second,
	}
	if mutateOpts != nil {
		mutateOpts(&opts)
	}
	srv, err := New(opts)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	gw := httptest.NewServer(srv.Handler())
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	t.Cleanup(func() {
		gw.Close()
		srv.Close()
		_ = v.Close()
	})
	return &testEnv{cfg: &cfg, srv: srv, gw: gw, vault: v, cache: cache, client: client}
}

func (e *testEnv) newTunnelRequest(t *testing.T, target, method string, body []byte, follow *bool, headers map[string]string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.gw.URL+"/api/tunnel", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Target-URL", target)
	if method != "" {
		req.Header.Set("X-Target-Method", method)
	}
	if follow != nil {
		if *follow {
			req.Header.Set("X-Pica-Follow", "1")
		} else {
			req.Header.Set("X-Pica-Follow", "0")
		}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func (e *testEnv) tunnel(t *testing.T, target, method string, body []byte, follow *bool, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	resp, err := e.client.Do(e.newTunnelRequest(t, target, method, body, follow, headers))
	if err != nil {
		t.Fatalf("tunnel request: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read tunnel body: %v", err)
	}
	return resp, raw
}

func decodeMeta(t *testing.T, resp *http.Response) tunnelMeta {
	t.Helper()
	encoded := resp.Header.Get("X-Pica-Meta")
	if encoded == "" {
		t.Fatal("missing X-Pica-Meta")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	var meta tunnelMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	return meta
}

func encodeHeaderOverlay(t *testing.T, headers map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(headers)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

type reqCapture struct {
	method         string
	host           string
	contentLength  int64
	userAgent      string
	referer        string
	origin         string
	cookie         string
	custom         string
	acceptEncoding string
	keepAlive      string
	xHop           string
	xPica          string
	xTarget        string
	proxyAuth      string
	xTest          string
	multi          []string
}

func startCaptureServer(t *testing.T, setCookie bool) (*httptest.Server, <-chan reqCapture) {
	t.Helper()
	captures := make(chan reqCapture, 8)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captures <- reqCapture{
			method:         r.Method,
			host:           r.Host,
			contentLength:  r.ContentLength,
			userAgent:      r.Header.Get("User-Agent"),
			referer:        r.Header.Get("Referer"),
			origin:         r.Header.Get("Origin"),
			cookie:         r.Header.Get("Cookie"),
			custom:         r.Header.Get("X-Custom"),
			acceptEncoding: r.Header.Get("Accept-Encoding"),
			keepAlive:      r.Header.Get("Keep-Alive"),
			xHop:           r.Header.Get("X-Hop-From-Map"),
			xPica:          r.Header.Get("X-Pica-Foo"),
			xTarget:        r.Header.Get("X-Target-URL"),
			proxyAuth:      r.Header.Get("Proxy-Authorization"),
			xTest:          r.Header.Get("X-Test"),
			multi:          append([]string(nil), r.Header.Values("X-Multi")...),
		}
		if setCookie {
			http.SetCookie(w, &http.Cookie{Name: "newsid", Value: "42", Path: "/"})
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(up.Close)
	return up, captures
}

func TestTunnelStreamsResponseAndForwardsBody(t *testing.T) {
	gotBody := make(chan []byte, 1)
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody <- body
		if r.Method != http.MethodPost {
			t.Errorf("upstream method = %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/x-test")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "first-")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "second")
	}))
	defer up.Close()

	e := newTestEnv(t, nil)
	payload := []byte{0x00, 0x01, 0xff, 'h', 'i'}
	req := e.newTunnelRequest(t, up.URL+"/echo", http.MethodPost, payload, nil, map[string]string{
		"Content-Type": "application/octet-stream",
		"X-Custom":     "yes",
	})
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	// The first chunk must arrive while the upstream is still blocked; this
	// proves the gateway streams instead of buffering the whole response.
	first := make([]byte, len("first-"))
	if _, err := io.ReadFull(resp.Body, first); err != nil {
		t.Fatalf("read first chunk: %v", err)
	}
	if string(first) != "first-" {
		t.Fatalf("first chunk = %q", first)
	}
	close(release)
	rest, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read rest: %v", err)
	}
	if got := string(first) + string(rest); got != "first-second" {
		t.Fatalf("body = %q", got)
	}

	select {
	case sent := <-gotBody:
		if !bytes.Equal(sent, payload) {
			t.Fatalf("upstream body = %v, want %v", sent, payload)
		}
	case <-time.After(time.Second):
		t.Fatal("upstream never received body")
	}

	meta := decodeMeta(t, resp)
	if meta.Status != http.StatusCreated {
		t.Fatalf("meta status = %d", meta.Status)
	}
	if meta.FinalURL != up.URL+"/echo" {
		t.Fatalf("meta finalUrl = %q", meta.FinalURL)
	}
	if got := meta.Headers["Content-Type"]; len(got) == 0 || got[0] != "application/x-test" {
		t.Fatalf("meta content-type = %v", got)
	}
}

func TestTunnelRangePassthrough(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Range"); got != "bytes=10-19" {
			t.Errorf("upstream Range = %q", got)
		}
		w.Header().Set("Content-Range", "bytes 10-19/100")
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(bytes.Repeat([]byte("x"), 10))
	}))
	defer up.Close()

	e := newTestEnv(t, nil)
	resp, body := e.tunnel(t, up.URL+"/file", http.MethodGet, nil, nil, map[string]string{"Range": "bytes=10-19"})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes 10-19/100" {
		t.Fatalf("Content-Range = %q", got)
	}
	if len(body) != 10 {
		t.Fatalf("body length = %d", len(body))
	}
	if meta := decodeMeta(t, resp); meta.Status != http.StatusPartialContent {
		t.Fatalf("meta status = %d", meta.Status)
	}
}

func TestTunnelCookiesStayInVault(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Cookie"), "sid=session-value") {
			_, _ = io.WriteString(w, "authorized")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "session-value", Path: "/", HttpOnly: true, MaxAge: 3600})
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "login")
	}))
	defer up.Close()

	e := newTestEnv(t, nil)
	resp, body := e.tunnel(t, up.URL+"/login", http.MethodGet, nil, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized || string(body) != "login" {
		t.Fatalf("first response = %d %q", resp.StatusCode, body)
	}
	if got := resp.Header.Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("gateway leaked Set-Cookie: %v", got)
	}
	meta := decodeMeta(t, resp)
	if len(meta.SetCookies) != 0 {
		t.Fatalf("meta setCookies should be empty by default: %v", meta.SetCookies)
	}
	views, err := e.vault.List("default", "127.0.0.1")
	if err != nil || len(views) != 1 {
		t.Fatalf("vault views = %+v, err=%v", views, err)
	}
	if views[0].Masked == "session-value" {
		t.Fatal("vault API leaked full cookie value")
	}

	resp, body = e.tunnel(t, up.URL+"/private", http.MethodGet, nil, nil, nil)
	if resp.StatusCode != http.StatusOK || string(body) != "authorized" {
		t.Fatalf("second response = %d %q", resp.StatusCode, body)
	}
}

func TestTunnelReqHeadersOverlayAndCookiePriority(t *testing.T) {
	up, captures := startCaptureServer(t, true)
	e := newTestEnv(t, func(c *config.Config) { c.UserAgent = "" })

	upURL, err := url.Parse(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.vault.SaveSetCookies("default", upURL, []string{"vault=vault-cookie; Path=/"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	overlay := encodeHeaderOverlay(t, map[string]any{
		"User-Agent":          "source-ua/1.0",
		"Referer":             "https://source.example/page",
		"Origin":              "https://source.example",
		"Cookie":              "client=client-cookie",
		"X-Custom":            "custom-value",
		"X-Multi":             []string{"one", "two"},
		"Host":                "evil.example",
		"Content-Length":      "9999",
		"Accept-Encoding":     "br",
		"Connection":          "X-Hop-From-Map",
		"X-Hop-From-Map":      "secret",
		"Keep-Alive":          "timeout=5",
		"X-Pica-Foo":          "secret",
		"X-Target-URL":        "https://evil.example/",
		"Proxy-Authorization": "Basic abc",
	})
	resp, body := e.tunnel(t, up.URL+"/x", http.MethodPost, []byte("abc"), nil, map[string]string{
		"X-Pica-Req-Headers": overlay,
	})
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("overlay response = %d %q", resp.StatusCode, body)
	}
	first := <-captures

	if first.userAgent != "source-ua/1.0" {
		t.Errorf("User-Agent = %q", first.userAgent)
	}
	if first.referer != "https://source.example/page" {
		t.Errorf("Referer = %q", first.referer)
	}
	if first.origin != "https://source.example" {
		t.Errorf("Origin = %q", first.origin)
	}
	if first.cookie != "client=client-cookie" {
		t.Errorf("Cookie = %q; client cookie must win over vault", first.cookie)
	}
	if first.custom != "custom-value" {
		t.Errorf("X-Custom = %q", first.custom)
	}
	if len(first.multi) != 2 || first.multi[0] != "one" || first.multi[1] != "two" {
		t.Errorf("X-Multi = %v", first.multi)
	}
	if first.host != upURL.Host {
		t.Errorf("Host = %q, want %q", first.host, upURL.Host)
	}
	if first.contentLength != 3 {
		t.Errorf("ContentLength = %d, want 3 (forged 9999 must be dropped)", first.contentLength)
	}
	if first.acceptEncoding == "br" || first.acceptEncoding == "" {
		t.Errorf("Accept-Encoding = %q; must be left to the Go transport", first.acceptEncoding)
	}
	for name, got := range map[string]string{
		"Keep-Alive":          first.keepAlive,
		"X-Hop-From-Map":      first.xHop,
		"X-Pica-Foo":          first.xPica,
		"X-Target-URL":        first.xTarget,
		"Proxy-Authorization": first.proxyAuth,
	} {
		if got != "" {
			t.Errorf("%s leaked upstream: %q", name, got)
		}
	}

	// Set-Cookie from the upstream is still persisted even when the client
	// supplied its own Cookie header.
	views, err := e.vault.List("default", upURL.Hostname())
	if err != nil {
		t.Fatal(err)
	}
	foundNewsid := false
	for _, v := range views {
		if v.Name == "newsid" {
			foundNewsid = true
		}
	}
	if !foundNewsid {
		t.Fatalf("upstream Set-Cookie was not stored; views=%+v", views)
	}

	// Without a client Cookie, the vault is used again.
	resp, body = e.tunnel(t, up.URL+"/x", http.MethodGet, nil, nil, nil)
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("vault response = %d %q", resp.StatusCode, body)
	}
	second := <-captures
	if !strings.Contains(second.cookie, "vault=vault-cookie") {
		t.Errorf("Cookie = %q; vault cookie was not injected", second.cookie)
	}
	if !strings.Contains(second.cookie, "newsid=42") {
		t.Errorf("Cookie = %q; upstream Set-Cookie was not injected", second.cookie)
	}
	if strings.Contains(second.cookie, "client=client-cookie") {
		t.Errorf("Cookie = %q; stale client cookie leaked", second.cookie)
	}
}

func TestTunnelIgnoresOriginalForbiddenHeaders(t *testing.T) {
	up, captures := startCaptureServer(t, false)
	e := newTestEnv(t, func(c *config.Config) { c.UserAgent = "" })

	resp, body := e.tunnel(t, up.URL+"/x", http.MethodGet, nil, nil, map[string]string{
		"Cookie":          "leak=1",
		"User-Agent":      "browser-ua",
		"Referer":         "https://gateway.example/",
		"Origin":          "https://gateway.example",
		"Accept-Encoding": "br",
		"X-Test":          "kept",
	})
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}
	got := <-captures
	if got.cookie != "" {
		t.Errorf("original Cookie forwarded: %q", got.cookie)
	}
	if got.userAgent == "browser-ua" {
		t.Errorf("original User-Agent forwarded: %q", got.userAgent)
	}
	if got.referer != "" || got.origin != "" {
		t.Errorf("original Referer/Origin forwarded: %q / %q", got.referer, got.origin)
	}
	if got.acceptEncoding == "br" {
		t.Errorf("original Accept-Encoding forwarded: %q", got.acceptEncoding)
	}
	if got.xTest != "kept" {
		t.Errorf("normal header lost: X-Test=%q", got.xTest)
	}
}

func TestTunnelUserAgentConfigOverridesOverlay(t *testing.T) {
	up, captures := startCaptureServer(t, false)
	e := newTestEnv(t, func(c *config.Config) { c.UserAgent = "forced-ua/1.0" })

	overlay := encodeHeaderOverlay(t, map[string]any{"User-Agent": "client-ua/1.0"})
	resp, body := e.tunnel(t, up.URL+"/x", http.MethodGet, nil, nil, map[string]string{
		"X-Pica-Req-Headers": overlay,
	})
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}
	if got := (<-captures).userAgent; got != "forced-ua/1.0" {
		t.Fatalf("User-Agent = %q; configured userAgent must win", got)
	}
}

func TestTunnelInvalidReqHeaders(t *testing.T) {
	up, captures := startCaptureServer(t, false)
	e := newTestEnv(t, nil)

	cases := map[string]string{
		"bad_base64": "!!!not-base64!!!",
		"bad_json":   base64.RawURLEncoding.EncodeToString([]byte(`{"X-Test":}`)),
		"bad_type":   encodeHeaderOverlay(t, map[string]any{"X-Test": 123}),
		"bad_name":   encodeHeaderOverlay(t, map[string]any{"Bad Header": "x"}),
		"bad_value":  encodeHeaderOverlay(t, map[string]any{"X-Test": "ok\r\nInjected: yes"}),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			resp, body := e.tunnel(t, up.URL+"/x", http.MethodGet, nil, nil, map[string]string{
				"X-Pica-Req-Headers": value,
			})
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d body=%q", resp.StatusCode, body)
			}
			if got := resp.Header.Get("X-Pica-Error"); got != "invalid_req_headers" {
				t.Fatalf("X-Pica-Error = %q", got)
			}
			if !strings.Contains(string(body), "invalid_req_headers") {
				t.Fatalf("body = %q", body)
			}
		})
	}
	select {
	case got := <-captures:
		t.Fatalf("invalid overlay reached upstream: %+v", got)
	default:
	}
}

func TestTunnelRedirects(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.SetCookie(w, &http.Cookie{Name: "rc", Value: "1", Path: "/"})
			w.Header().Set("Location", "/final")
			w.WriteHeader(http.StatusFound)
			_, _ = io.WriteString(w, "redirect-body")
		case "/final":
			if !strings.Contains(r.Header.Get("Cookie"), "rc=1") {
				t.Errorf("redirect cookie not injected; Cookie=%q", r.Header.Get("Cookie"))
			}
			_, _ = io.WriteString(w, "final")
		case "/to-private":
			http.Redirect(w, r, "http://private.example.com/secret", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()

	e := newTestEnv(t, func(c *config.Config) {
		c.HostOverrides = map[string]string{"private.example.com": "192.168.1.10"}
	})

	resp, body := e.tunnel(t, up.URL+"/start", http.MethodGet, nil, nil, nil)
	if resp.StatusCode != http.StatusOK || string(body) != "final" {
		t.Fatalf("follow response = %d %q", resp.StatusCode, body)
	}
	if meta := decodeMeta(t, resp); !strings.HasSuffix(meta.FinalURL, "/final") {
		t.Fatalf("finalUrl = %q", meta.FinalURL)
	}

	follow := false
	resp, redirectBody := e.tunnel(t, up.URL+"/start", http.MethodGet, nil, &follow, nil)
	// fetch() would auto-follow a real 302, so the transport status is 200;
	// the real status stays in X-Pica-Meta.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no-follow transport status = %d, want 200", resp.StatusCode)
	}
	if string(redirectBody) != "redirect-body" {
		t.Fatalf("no-follow body = %q", redirectBody)
	}
	meta := decodeMeta(t, resp)
	if meta.Status != http.StatusFound {
		t.Fatalf("meta status = %d, want 302", meta.Status)
	}
	if got := resp.Header.Get("Location"); got != "/final" {
		t.Fatalf("Location = %q", got)
	}
	if got := meta.Headers["Location"]; len(got) == 0 || got[0] != "/final" {
		t.Fatalf("meta Location = %v", got)
	}

	resp, raw := e.tunnel(t, up.URL+"/to-private", http.MethodGet, nil, nil, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("private redirect status = %d, body=%q", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "private_ip_blocked") {
		t.Fatalf("private redirect error = %s", raw)
	}
}

func TestTunnelBlocksPrivateTarget(t *testing.T) {
	e := newTestEnv(t, nil)
	resp, body := e.tunnel(t, "http://192.168.1.10/", http.MethodGet, nil, nil, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d body=%q", resp.StatusCode, body)
	}
}

func TestTunnelNetworkErrorIs502JSON(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) {
		c.HostOverrides = map[string]string{"unreachable.example": "127.0.0.1"}
	})
	resp, body := e.tunnel(t, "http://unreachable.example:1/", http.MethodGet, nil, nil, nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d body=%q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	if !strings.Contains(string(body), "upstream_error") {
		t.Fatalf("body = %q", body)
	}
}

func TestAuthLoginAndUnauthorized(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer up.Close()

	e := newTestEnv(t, func(c *config.Config) { c.Password = "s3cret" })

	resp, body := e.tunnel(t, up.URL+"/x", http.MethodGet, nil, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated tunnel status = %d body=%q", resp.StatusCode, body)
	}

	statusResp, err := e.client.Get(e.gw.URL + "/api/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	_ = json.NewDecoder(statusResp.Body).Decode(&status)
	statusResp.Body.Close()
	if status["authenticated"] != false {
		t.Fatalf("status = %v", status)
	}

	login := func(password string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, e.gw.URL+"/api/auth/login",
			strings.NewReader(`{"password":"`+password+`"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}

	if resp := login("wrong"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d", resp.StatusCode)
	}
	resp = login("s3cret")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	setCookie := resp.Header.Get("Set-Cookie")
	if !strings.Contains(setCookie, "HttpOnly") || !strings.Contains(setCookie, "SameSite=Lax") {
		t.Fatalf("session cookie flags = %q", setCookie)
	}

	statusResp, err = e.client.Get(e.gw.URL + "/api/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	status = map[string]any{}
	_ = json.NewDecoder(statusResp.Body).Decode(&status)
	statusResp.Body.Close()
	if status["authenticated"] != true {
		t.Fatalf("authenticated status = %v", status)
	}

	resp, body = e.tunnel(t, up.URL+"/x", http.MethodGet, nil, nil, nil)
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("authenticated tunnel = %d %q", resp.StatusCode, body)
	}

	req, _ := http.NewRequest(http.MethodPost, e.gw.URL+"/api/auth/logout", nil)
	logoutResp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, logoutResp.Body)
	logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d", logoutResp.StatusCode)
	}
	resp, _ = e.tunnel(t, up.URL+"/x", http.MethodGet, nil, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tunnel after logout = %d", resp.StatusCode)
	}
}

func TestLoginPageAndStaticRedirect(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.Password = "s3cret" })
	noRedirect := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Jar: e.client.Jar,
	}

	resp, err := noRedirect.Get(e.gw.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("unauthenticated / status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login?next=") {
		t.Fatalf("redirect location = %q", loc)
	}

	resp, err = noRedirect.Get(e.gw.URL + "/login?next=%2F")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("密码")) {
		t.Fatalf("login page status=%d body=%q", resp.StatusCode, page)
	}

	form := url.Values{"password": {"s3cret"}}
	resp, err = e.client.PostForm(e.gw.URL+"/api/auth/login", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("form login status = %d", resp.StatusCode)
	}

	resp, err = noRedirect.Get(e.gw.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	index, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(index) != "INDEX" {
		t.Fatalf("authenticated / status=%d body=%q", resp.StatusCode, index)
	}
}

func TestDevModeRestrictsAPIToLoopback(t *testing.T) {
	e := newTestEnv(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/cookies?host=example.com", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("public dev-mode request = %d, want 403", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/cookies?host=example.com", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec = httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("loopback dev-mode request = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	rec = httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("public healthz = %d, want 200", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	rec = httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("public dev-mode auth status = %d, want 403", rec.Code)
	}
}

func TestImageSignCacheAndRange(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n0123456789abcdef")
	var hits atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(png)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(png)
	}))
	defer up.Close()

	e := newTestEnv(t, nil)
	signResp, err := e.client.Get(e.gw.URL + "/api/img/sign?url=" + url.QueryEscape(up.URL+"/pic.png"))
	if err != nil {
		t.Fatal(err)
	}
	var signed struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(signResp.Body).Decode(&signed); err != nil {
		t.Fatal(err)
	}
	signResp.Body.Close()
	if signResp.StatusCode != http.StatusOK || signed.URL == "" {
		t.Fatalf("sign status = %d, url=%q", signResp.StatusCode, signed.URL)
	}

	get := func(headers map[string]string) (*http.Response, []byte) {
		req, _ := http.NewRequest(http.MethodGet, e.gw.URL+signed.URL, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp, raw
	}

	resp, body := get(nil)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, png) {
		t.Fatalf("first image = %d len=%d", resp.StatusCode, len(body))
	}
	if got := resp.Header.Get("X-Cache"); got != "MISS" {
		t.Fatalf("X-Cache = %q, want MISS", got)
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream hits = %d", hits.Load())
	}

	resp, body = get(nil)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, png) {
		t.Fatalf("cached image = %d len=%d", resp.StatusCode, len(body))
	}
	if got := resp.Header.Get("X-Cache"); got != "HIT" {
		t.Fatalf("X-Cache = %q, want HIT", got)
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream hits after cache = %d", hits.Load())
	}

	resp, body = get(map[string]string{"Range": "bytes=0-3"})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("range status = %d", resp.StatusCode)
	}
	if !bytes.Equal(body, png[:4]) {
		t.Fatalf("range body = %v", body)
	}
	if got := resp.Header.Get("Content-Range"); !strings.HasPrefix(got, "bytes 0-3/") {
		t.Fatalf("Content-Range = %q", got)
	}

	// Forged token must fail clearly.
	badResp, err := e.client.Get(e.gw.URL + signed.URL + "x")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, badResp.Body)
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusForbidden {
		t.Fatalf("tampered token status = %d", badResp.StatusCode)
	}
}

func TestStaticSPAFallback(t *testing.T) {
	e := newTestEnv(t, nil)
	get := func(path string) (int, string) {
		resp, err := e.client.Get(e.gw.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}

	for _, path := range []string{"/", "/deep/route"} {
		status, body := get(path)
		if status != http.StatusOK || body != "INDEX" {
			t.Fatalf("%s = %d %q", path, status, body)
		}
	}
	if status, body := get("/assets/app.js"); status != http.StatusOK || body != "console.log(1)" {
		t.Fatalf("asset = %d %q", status, body)
	}
	if status, _ := get("/missing.js"); status != http.StatusNotFound {
		t.Fatalf("missing asset status = %d", status)
	}
}
