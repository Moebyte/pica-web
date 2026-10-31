package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"picagateway/internal/downloads"
	"picagateway/internal/srcapi"
)

// maxSrcLoginBody bounds the login payload.
const maxSrcLoginBody = 16 << 10

// maxSrcDownloadBody bounds the download submission payload.
const maxSrcDownloadBody = 16 << 10

// srcSource resolves {src}; unknown keys get a 404.
func (s *Server) srcSource(w http.ResponseWriter, r *http.Request) srcapi.Source {
	key := r.PathValue("src")
	src, ok := s.src.Get(key)
	if !ok {
		s.writeSrcError(w, http.StatusNotFound, "unknown_source", "no such source: "+key)
		return nil
	}
	return src
}

// writeSrcError keeps the contract shape {"ok":false,"error":...} while also
// carrying the gateway error code in the X-Pica-Error header.
func (s *Server) writeSrcError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("X-Pica-Error", code)
	writeJSON(w, status, map[string]any{"ok": false, "error": message, "code": code})
}

// writeSrcAPIError maps srcapi errors onto HTTP responses.
func (s *Server) writeSrcAPIError(w http.ResponseWriter, err error) {
	var se *srcapi.StatusError
	if errors.As(err, &se) {
		status := se.Status
		if status == 0 {
			status = http.StatusBadGateway
		}
		s.writeSrcError(w, status, se.Code, se.Message)
		return
	}
	s.writeSrcError(w, http.StatusBadGateway, srcapi.CodeUpstreamError, err.Error())
}

// srcPage parses the page query parameter (1-based, default 1).
func (s *Server) srcPage(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("page"))
	if raw == "" {
		return 1, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		s.writeSrcError(w, http.StatusBadRequest, "invalid_page", "page must be a positive integer")
		return 0, false
	}
	return n, true
}

func (s *Server) handleSrcSources(w http.ResponseWriter, r *http.Request) {
	sources := s.src.Sources()
	out := make([]map[string]any, 0, len(sources))
	for _, src := range sources {
		out = append(out, map[string]any{
			"key":       src.Key(),
			"name":      src.Name(),
			"needLogin": src.NeedLogin(),
			"loggedIn":  src.LoggedIn(r.Context()),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sources": out})
}

func (s *Server) handleSrcTabs(w http.ResponseWriter, r *http.Request) {
	src := s.srcSource(w, r)
	if src == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tabs": src.Tabs()})
}

func (s *Server) handleSrcExplore(w http.ResponseWriter, r *http.Request) {
	src := s.srcSource(w, r)
	if src == nil {
		return
	}
	page, ok := s.srcPage(w, r)
	if !ok {
		return
	}
	res, err := src.Explore(r.Context(), strings.TrimSpace(r.URL.Query().Get("tab")), page)
	if err != nil {
		s.writeSrcAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"comics":  res.Comics,
		"page":    res.Page,
		"maxPage": res.MaxPage,
	})
}

func (s *Server) handleSrcSearch(w http.ResponseWriter, r *http.Request) {
	src := s.srcSource(w, r)
	if src == nil {
		return
	}
	page, ok := s.srcPage(w, r)
	if !ok {
		return
	}
	res, err := src.Search(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("sort"), page)
	if err != nil {
		s.writeSrcAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"comics":  res.Comics,
		"page":    res.Page,
		"maxPage": res.MaxPage,
	})
}

func (s *Server) handleSrcComic(w http.ResponseWriter, r *http.Request) {
	src := s.srcSource(w, r)
	if src == nil {
		return
	}
	comic, err := src.Comic(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeSrcAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "comic": comic})
}

func (s *Server) handleSrcPages(w http.ResponseWriter, r *http.Request) {
	src := s.srcSource(w, r)
	if src == nil {
		return
	}
	pages, err := src.Pages(r.Context(), r.PathValue("id"), r.URL.Query().Get("ep"))
	if err != nil {
		s.writeSrcAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pages": pages})
}

// handleSrcComicDownload resolves a chapter into raw upstream pages and
// enqueues it on the shared download manager. The source must implement
// srcapi.Downloader. An omitted ep selects the first chapter (or the source's
// anonymous single chapter). Resubmitting the same job is idempotent and
// reports "existed":true without downloading anything twice.
func (s *Server) handleSrcComicDownload(w http.ResponseWriter, r *http.Request) {
	src := s.srcSource(w, r)
	if src == nil {
		return
	}
	downloader, ok := src.(srcapi.Downloader)
	if !ok {
		s.writeSrcError(w, http.StatusNotFound, "download_not_supported",
			"source "+src.Key()+" does not support server-side downloads")
		return
	}

	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxSrcDownloadBody+1))
	if err != nil {
		s.writeSrcError(w, http.StatusBadRequest, "body_read_error", err.Error())
		return
	}
	if len(raw) > maxSrcDownloadBody {
		s.writeSrcError(w, http.StatusRequestEntityTooLarge, "body_too_large", "download body is too large")
		return
	}
	var req struct {
		EP     string `json:"ep"`
		EPName string `json:"epName"`
	}
	if strings.TrimSpace(string(raw)) != "" {
		if err := json.Unmarshal(raw, &req); err != nil {
			s.writeSrcError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON: "+err.Error())
			return
		}
	}

	comicID := r.PathValue("id")
	comic, err := src.Comic(r.Context(), comicID)
	if err != nil {
		s.writeSrcAPIError(w, err)
		return
	}

	ep := strings.TrimSpace(req.EP)
	if ep == "" && len(comic.Chapters) > 0 {
		ep = comic.Chapters[0].ID
	}
	epName := strings.TrimSpace(req.EPName)
	if epName == "" {
		for _, ch := range comic.Chapters {
			if ch.ID == ep {
				epName = ch.Title
				break
			}
		}
		if epName == "" && len(comic.Chapters) > 0 {
			epName = comic.Chapters[0].Title
		}
	}
	if epName == "" {
		epName = ep
	}

	pages, err := downloader.DownloadPages(r.Context(), comicID, ep)
	if err != nil {
		s.writeSrcAPIError(w, err)
		return
	}
	if len(pages) == 0 {
		s.writeSrcError(w, http.StatusBadGateway, srcapi.CodeUpstreamError,
			"source returned no pages for this chapter")
		return
	}

	// downloads requires non-empty path segments; an empty ep means the source
	// has no chapter list and exposes one anonymous chapter.
	jobEP := ep
	if jobEP == "" {
		jobEP = "all"
	}
	job := downloads.Job{
		ID:        src.Key() + "-" + comicID + "-" + jobEP,
		Title:     comic.Title,
		Source:    src.Key(),
		ComicID:   comicID,
		EP:        jobEP,
		EPName:    epName,
		Pages:     pages,
		CreatedAt: time.Now().Unix(),
	}
	view, existed, err := s.downloads.Submit(job)
	if err != nil {
		switch {
		case errors.Is(err, downloads.ErrInvalidJob):
			s.writeSrcError(w, http.StatusBadRequest, "invalid_job", err.Error())
		case errors.Is(err, downloads.ErrDirConflict):
			s.writeSrcError(w, http.StatusConflict, "dir_conflict", err.Error())
		default:
			s.writeSrcError(w, http.StatusInternalServerError, "submit_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": view, "existed": existed})
}

func (s *Server) handleSrcLogin(w http.ResponseWriter, r *http.Request) {
	src := s.srcSource(w, r)
	if src == nil {
		return
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSrcLoginBody+1))
	if err != nil {
		s.writeSrcError(w, http.StatusBadRequest, "body_read_error", err.Error())
		return
	}
	if len(body) > maxSrcLoginBody {
		s.writeSrcError(w, http.StatusRequestEntityTooLarge, "body_too_large", "login body is too large")
		return
	}
	var cred struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(body, &cred); err != nil {
		s.writeSrcError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON: "+err.Error())
		return
	}
	res, err := src.Login(r.Context(), cred.Username, cred.Password)
	if err != nil {
		s.writeSrcAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": res.Name})
}

func (s *Server) handleSrcLogout(w http.ResponseWriter, r *http.Request) {
	src := s.srcSource(w, r)
	if src == nil {
		return
	}
	if err := src.Logout(r.Context()); err != nil {
		s.writeSrcAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
