package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"picagateway/internal/downloads"
	"picagateway/internal/outbound"
)

// maxDownloadsBodyBytes bounds the POST /api/downloads/jobs payload
// (pages can reach a few thousand entries).
const maxDownloadsBodyBytes = 4 << 20

// handleDownloadsSubmit accepts a job submission. Resubmitting an id that is
// queued, running or done returns the current state without re-downloading.
func (s *Server) handleDownloadsSubmit(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > maxDownloadsBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large",
			fmt.Sprintf("request body exceeds %d MiB", maxDownloadsBodyBytes>>20))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxDownloadsBodyBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
		return
	}
	if int64(len(body)) > maxDownloadsBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large",
			fmt.Sprintf("request body exceeds %d MiB", maxDownloadsBodyBytes>>20))
		return
	}

	var job downloads.Job
	if err := json.Unmarshal(body, &job); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON: "+err.Error())
		return
	}

	view, _, err := s.downloads.Submit(job)
	if err != nil {
		switch {
		case errors.Is(err, downloads.ErrInvalidJob):
			writeError(w, http.StatusBadRequest, "invalid_job", err.Error())
		case errors.Is(err, downloads.ErrDirConflict):
			writeError(w, http.StatusConflict, "dir_conflict", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "submit_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": view})
}

// handleDownloadsList lists all jobs, newest first.
func (s *Server) handleDownloadsList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "jobs": s.downloads.List()})
}

// handleDownloadsDelete cancels and removes a job; files=true also deletes the
// downloaded files.
func (s *Server) handleDownloadsDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing_id", "id query parameter is required")
		return
	}
	files := false
	switch v := strings.TrimSpace(r.URL.Query().Get("files")); v {
	case "", "false", "0":
	case "true", "1":
		files = true
	default:
		writeError(w, http.StatusBadRequest, "invalid_files", "files must be true or false")
		return
	}
	if err := s.downloads.Delete(id, files); err != nil {
		if errors.Is(err, downloads.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no such download job")
			return
		}
		writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDownloadsFile serves a file from the downloads directory. The path
// must be relative to the downloads root; traversal, absolute paths and
// symlinks are rejected. Range requests are supported.
func (s *Server) handleDownloadsFile(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if strings.TrimSpace(rel) == "" {
		writeError(w, http.StatusBadRequest, "missing_path", "path query parameter is required")
		return
	}
	if strings.Contains(rel, "..") {
		writeError(w, http.StatusBadRequest, "path_traversal", "path must not contain '..'")
		return
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || clean == "." {
		writeError(w, http.StatusBadRequest, "path_traversal", "path must be relative to the downloads directory")
		return
	}
	root, err := filepath.Abs(s.cfg.DownloadsDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	full := filepath.Join(root, clean)
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		writeError(w, http.StatusBadRequest, "path_traversal", "path escapes the downloads directory")
		return
	}

	fi, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "not_found", "no such file")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		writeError(w, http.StatusForbidden, "symlink_rejected", "symbolic links are not served")
		return
	}
	if fi.IsDir() {
		writeError(w, http.StatusBadRequest, "is_directory", "path is a directory, not a file")
		return
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "not_found", "no such file")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.IsDir() { // TOCTOU guard
		writeError(w, http.StatusBadRequest, "is_directory", "path is a directory, not a file")
		return
	}

	// Content-Type comes from the extension (ServeContent), Range/If-* headers
	// are handled by ServeContent.
	w.Header().Set("Content-Type", contentTypeFor(fi.Name()))
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// contentTypeFor maps the file name to a MIME type, defaulting to
// application/octet-stream (never sniffed into something executable).
func contentTypeFor(name string) string {
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// handleDownloadsHealth reports the queue stats.
func (s *Server) handleDownloadsHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "downloads": s.downloadsStats()})
}

func (s *Server) downloadsStats() map[string]any {
	if s.downloads == nil {
		return map[string]any{"queued": 0, "running": 0}
	}
	queued, running := s.downloads.Stats()
	return map[string]any{"queued": queued, "running": running}
}

// downloadsFetch downloads one page through the shared outbound pipeline:
// allowlist/SSRF validation (also on every redirect), the cookie-vault
// transport, and vault persistence of any Set-Cookie seen on the way.
func (s *Server) downloadsFetch(ctx context.Context, page downloads.Page) ([]byte, string, error) {
	u, _, err := s.policy.ValidateURL(ctx, page.URL)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	if err := applyDownloadHeaders(req.Header, page.Headers); err != nil {
		return nil, "", fmt.Errorf("invalid page headers: %w", err)
	}

	var redirectErr error
	client := &http.Client{
		Transport: s.upstream,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return outbound.ErrTooManyRedirects
			}
			if _, _, err := s.policy.ValidateURL(next.Context(), next.URL.String()); err != nil {
				redirectErr = err
				return err
			}
			if prev := next.Response; prev != nil {
				s.saveResponseCookies(prev)
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		if redirectErr != nil {
			return nil, "", redirectErr
		}
		return nil, "", err
	}
	defer resp.Body.Close()

	s.saveResponseCookies(resp)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, downloads.MaxPageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > downloads.MaxPageBytes {
		return nil, "", fmt.Errorf("page exceeds the %d byte limit", downloads.MaxPageBytes)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// applyDownloadHeaders copies page-level headers onto the outbound request.
// Hop-by-hop headers, Host, Content-Length, Accept-Encoding and gateway
// control headers are dropped. Cookie is kept as-is: the cookie transport
// injects vault cookies only when the request has none.
func applyDownloadHeaders(dst http.Header, src map[string]string) error {
	if len(src) == 0 {
		return nil
	}
	overlay := make(http.Header, len(src))
	for k, v := range src {
		if !validHeaderName(k) {
			return fmt.Errorf("invalid header name %q", k)
		}
		if !validHeaderValue(v) {
			return fmt.Errorf("header %q contains an invalid value", k)
		}
		overlay.Set(http.CanonicalHeaderKey(k), v)
	}
	hop := hopByHopHeaders(overlay)
	for name, values := range overlay {
		if tunnelOverlayHeaderBlocked(name, hop) {
			continue
		}
		for _, v := range values {
			dst.Add(name, v)
		}
	}
	return nil
}
