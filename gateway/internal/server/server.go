// Package server wires the HTTP API: tunnel, image proxy, auth, cookie
// inspection and static Flutter Web hosting.
package server

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"picagateway/internal/auth"
	"picagateway/internal/config"
	"picagateway/internal/downloads"
	"picagateway/internal/imgproxy"
	"picagateway/internal/outbound"
	"picagateway/internal/ratelimit"
	"picagateway/internal/srcapi"
	"picagateway/internal/vault"
)

// Options are the server dependencies.
type Options struct {
	Config   *config.Config
	Vault    *vault.Vault
	Policy   *outbound.Policy
	Cache    *imgproxy.Cache
	Sessions *auth.Manager
	Signer   *imgproxy.Signer
	Limiter  *ratelimit.Limiter
	Logger   *slog.Logger
	Version  string

	// DownloadRetryDelays overrides the per-page retry backoff (2s/8s/30s by
	// default); mainly for tests.
	DownloadRetryDelays []time.Duration
	// DownloadPageTimeout overrides the per-page fetch timeout (60s by
	// default); mainly for tests.
	DownloadPageTimeout time.Duration
}

// Server is the gateway HTTP application.
type Server struct {
	cfg      *config.Config
	vault    *vault.Vault
	policy   *outbound.Policy
	cache    *imgproxy.Cache
	sessions *auth.Manager
	signer   *imgproxy.Signer
	limiter  *ratelimit.Limiter
	log      *slog.Logger
	started  time.Time
	version  string
	tenant   string

	transport *http.Transport
	upstream  http.RoundTripper
	downloads *downloads.Manager
	src       *srcapi.Registry
	handler   http.Handler
}

// New constructs the server and its routes.
func New(o Options) (*Server, error) {
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		cfg:      o.Config,
		vault:    o.Vault,
		policy:   o.Policy,
		cache:    o.Cache,
		sessions: o.Sessions,
		signer:   o.Signer,
		limiter:  o.Limiter,
		log:      log,
		started:  time.Now(),
		version:  o.Version,
		tenant:   auth.Tenant,
	}
	s.transport = outbound.NewTransport(o.Policy, o.Config.Timeout())
	s.upstream = &cookieTransport{s: s}

	dl, err := downloads.New(downloads.Options{
		Dir:         o.Config.DownloadsDir,
		Fetch:       s.downloadsFetch,
		Logger:      log,
		RetryDelays: o.DownloadRetryDelays,
		PageTimeout: o.DownloadPageTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("downloads manager: %w", err)
	}
	s.downloads = dl

	s.ensureHeaderProfiles()
	s.src = srcapi.NewRegistry(srcapi.Deps{
		Policy:    o.Policy,
		Transport: s.transport, // raw policy transport: sources set their own headers
		Signer:    o.Signer,
		TokenTTL:  o.Config.ImgTokenTTLDuration(),
		Timeout:   o.Config.Timeout(),
		Tenant:    s.tenant,
		Store:     o.Vault,
		Log:       log,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("GET /login", s.handleLoginPage)

	mux.Handle("GET /api/src/sources", s.requireAuth(http.HandlerFunc(s.handleSrcSources)))
	mux.Handle("GET /api/src/{src}/tabs", s.requireAuth(http.HandlerFunc(s.handleSrcTabs)))
	mux.Handle("GET /api/src/{src}/explore", s.requireAuth(http.HandlerFunc(s.handleSrcExplore)))
	mux.Handle("GET /api/src/{src}/search", s.requireAuth(http.HandlerFunc(s.handleSrcSearch)))
	mux.Handle("GET /api/src/{src}/comic/{id}", s.requireAuth(http.HandlerFunc(s.handleSrcComic)))
	mux.Handle("GET /api/src/{src}/comic/{id}/pages", s.requireAuth(http.HandlerFunc(s.handleSrcPages)))
	mux.Handle("POST /api/src/{src}/login", s.requireAuth(http.HandlerFunc(s.handleSrcLogin)))
	mux.Handle("POST /api/src/{src}/logout", s.requireAuth(http.HandlerFunc(s.handleSrcLogout)))
	mux.Handle("POST /api/tunnel", s.requireAuth(http.HandlerFunc(s.handleTunnel)))
	mux.Handle("GET /api/img/sign", s.requireAuth(http.HandlerFunc(s.handleImageSign)))
	mux.Handle("GET /api/img/{token}", s.requireAuth(http.HandlerFunc(s.handleImage)))
	mux.Handle("GET /api/cookies", s.requireAuth(http.HandlerFunc(s.handleCookiesList)))
	mux.Handle("DELETE /api/cookies", s.requireAuth(http.HandlerFunc(s.handleCookiesDelete)))
	mux.Handle("GET /api/store/{key}", s.requireAuth(http.HandlerFunc(s.handleStoreGet)))
	mux.Handle("PUT /api/store/{key}", s.requireAuth(http.HandlerFunc(s.handleStorePut)))
	mux.Handle("DELETE /api/store/{key}", s.requireAuth(http.HandlerFunc(s.handleStoreDelete)))
	mux.Handle("POST /api/downloads/jobs", s.requireAuth(http.HandlerFunc(s.handleDownloadsSubmit)))
	mux.Handle("GET /api/downloads/jobs", s.requireAuth(http.HandlerFunc(s.handleDownloadsList)))
	mux.Handle("DELETE /api/downloads/jobs", s.requireAuth(http.HandlerFunc(s.handleDownloadsDelete)))
	mux.Handle("GET /api/downloads/file", s.requireAuth(http.HandlerFunc(s.handleDownloadsFile)))
	mux.Handle("GET /api/downloads/healthz", s.requireAuth(http.HandlerFunc(s.handleDownloadsHealth)))
	mux.HandleFunc("/api/", s.handleAPINotFound)
	mux.HandleFunc("GET /v2", s.handleV2Static)
	mux.HandleFunc("GET /v2/", s.handleV2Static)
	mux.HandleFunc("/", s.handleStatic)

	s.handler = s.withAccessLog(s.withRecover(s.withSecurityHeaders(s.withRateLimit(mux))))
	return s, nil
}

// ensureHeaderProfiles registers the built-in image header profiles used by
// the semantic source API unless the operator overrides them.
func (s *Server) ensureHeaderProfiles() {
	if s.cfg.HeaderProfiles == nil {
		s.cfg.HeaderProfiles = map[string]map[string]string{}
	}
	if _, ok := s.cfg.HeaderProfiles["jm"]; !ok {
		s.cfg.HeaderProfiles["jm"] = map[string]string{
			"Accept":                   "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8",
			"Accept-Language":          "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7",
			"Referer":                  "https://localhost/",
			"Sec-Fetch-Dest":           "image",
			"Sec-Fetch-Mode":           "no-cors",
			"Sec-Fetch-Site":           "cross-site",
			"Sec-Fetch-Storage-Access": "active",
			"User-Agent":               srcapi.JMUserAgent,
			"X-Requested-With":         "com.example.app",
		}
	}
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// Close releases idle outbound connections and stops the download workers.
func (s *Server) Close() {
	if s.downloads != nil {
		s.downloads.Close()
	}
	if s.transport != nil {
		s.transport.CloseIdleConnections()
	}
}

// cookieTransport injects vault cookies and the configured User-Agent on every
// request, including redirect hops. The browser's own Cookie header is never
// forwarded.
type cookieTransport struct {
	s *Server
}

func (t *cookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	if clone.Header.Get("Cookie") == "" {
		header, err := t.s.vault.CookieHeader(t.s.tenant, req.URL, time.Now())
		if err != nil {
			t.s.log.Warn("cookie vault lookup failed", "host", req.URL.Hostname(), "error", err)
		}
		if header != "" {
			clone.Header.Set("Cookie", header)
		}
	}
	if t.s.cfg.UserAgent != "" {
		clone.Header.Set("User-Agent", t.s.cfg.UserAgent)
	}
	return t.s.transport.RoundTrip(clone)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"version":   s.version,
		"uptime":    int64(time.Since(s.started).Seconds()),
		"downloads": s.downloadsStats(),
	})
}

func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "no such API endpoint")
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.devModeBlocked(r) {
			writeError(w, http.StatusForbidden, "dev_mode_loopback_only",
				"no password is configured; /api is restricted to loopback clients")
			return
		}
		if s.cfg.Password == "" {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(auth.CookieName)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "login required")
			return
		}
		if _, ok := s.sessions.Validate(cookie.Value, time.Now()); !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "session is invalid or expired")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// devModeBlocked reports whether an unauthenticated deployment must reject the
// request because it did not come from loopback. RemoteAddr is used (never
// proxy headers) so the check cannot be spoofed.
func (s *Server) devModeBlocked(r *http.Request) bool {
	if s.cfg.Password != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

func (s *Server) cookieSecure(r *http.Request) bool {
	if s.cfg.CookieSecure || r.TLS != nil {
		return true
	}
	if s.cfg.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if net.ParseIP(first) != nil {
				return first
			}
		}
		if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(xr) != nil {
			return xr
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- middleware ----

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *statusRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		s.log.Info("request",
			"method", r.Method,
			"path", redactPath(r.URL.Path),
			"status", status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", s.clientIP(r),
		)
	})
}

func (s *Server) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic serving request", "path", redactPath(r.URL.Path), "panic", rec)
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if !s.limiter.Allow(s.clientIP(r)) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func redactPath(p string) string {
	if strings.HasPrefix(p, "/api/img/") && p != "/api/img/sign" {
		return "/api/img/{token}"
	}
	return p
}
