package server

import (
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// handleStatic serves the Flutter Web build with an SPA fallback to
// index.html. Requests under /api/ never reach this handler.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	s.serveWebApp(w, r, s.cfg.WebDir, "")
}

// handleV2Static serves the Vue webui build (webDir2) under /v2 with the same
// SPA fallback and caching rules. The route is always mounted; a missing
// build simply 404s.
func (s *Server) handleV2Static(w http.ResponseWriter, r *http.Request) {
	s.serveWebApp(w, r, s.cfg.WebDir2Resolved(), "/v2")
}

// serveWebApp is the shared static handler. urlPrefix is "" for the root app
// and "/v2" for the Vue app; incoming paths are stripped of it before mapping
// onto root.
func (s *Server) serveWebApp(w http.ResponseWriter, r *http.Request, webDir, urlPrefix string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and HEAD are supported")
		return
	}
	root, err := filepath.Abs(webDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "web_dir_error", err.Error())
		return
	}
	index := filepath.Join(root, "index.html")
	if info, err := os.Stat(index); err != nil || info.IsDir() {
		if urlPrefix == "/v2" {
			http.Error(w, "Vue web build not found (set webDir2 in config.json)", http.StatusNotFound)
			return
		}
		http.Error(w, "Flutter Web build not found (set webDir in config.json)", http.StatusServiceUnavailable)
		return
	}

	fullPath := r.URL.Path
	if urlPrefix != "" {
		if r.URL.Path == urlPrefix {
			fullPath = "/"
		} else {
			fullPath = strings.TrimPrefix(r.URL.Path, urlPrefix)
		}
	}
	clean := path.Clean("/" + fullPath)
	// Password mode: the app shell requires a session. Static assets stay
	// public so the login page and its CSS/JS load without a session.
	if s.cfg.Password != "" && !s.authenticated(r) && (clean == "/" || clean == "/index.html") {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	fsPath := filepath.Join(root, filepath.FromSlash(clean))
	if fsPath != root && !strings.HasPrefix(fsPath, root+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}

	info, err := os.Stat(fsPath)
	switch {
	case err == nil && !info.IsDir():
		s.serveStaticFile(w, r, fsPath, clean)
	case err == nil && info.IsDir():
		candidate := filepath.Join(fsPath, "index.html")
		if fi, statErr := os.Stat(candidate); statErr == nil && !fi.IsDir() {
			s.serveStaticFile(w, r, candidate, clean)
			return
		}
		s.serveIndex(w, r, index)
	default:
		// Assets with an extension should 404 rather than receive HTML.
		if path.Ext(clean) != "" && clean != "/index.html" {
			http.NotFound(w, r)
			return
		}
		s.serveIndex(w, r, index)
	}
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request, index string) {
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, index)
}

func (s *Server) serveStaticFile(w http.ResponseWriter, r *http.Request, fsPath, clean string) {
	if strings.Contains(clean, "/assets/") || clean == "/index.html" {
		if clean == "/index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFile(w, r, fsPath)
}
