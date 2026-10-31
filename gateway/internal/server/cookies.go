package server

import (
	"net/http"
	"strings"
)

func (s *Server) handleCookiesList(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("host")))
	if host == "" {
		writeError(w, http.StatusBadRequest, "missing_host", "host query parameter is required")
		return
	}
	cookies, err := s.vault.List(s.tenant, host)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "vault_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "host": host, "cookies": cookies})
}

func (s *Server) handleCookiesDelete(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("host")))
	if host == "" {
		writeError(w, http.StatusBadRequest, "missing_host", "host query parameter is required")
		return
	}
	deleted, err := s.vault.DeleteHost(s.tenant, host)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "vault_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "host": host, "deleted": deleted})
}
