package server

import (
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"picagateway/internal/vault"
)

// storeKeyPattern keeps stored blob names small and URL-safe. The account
// sync client uses "account"; the namespace is deliberately generic so
// settings/history sync can reuse it later.
var storeKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// storeValueLimit caps a single blob. The account payload (source
// credentials + cookie jar) is a few kilobytes; 512 KiB leaves ample room.
const storeValueLimit = 7 << 20

func (s *Server) handleStoreGet(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !storeKeyPattern.MatchString(key) {
		writeError(w, http.StatusBadRequest, "invalid_key", "key must match ^[a-z0-9][a-z0-9_-]{0,31}$")
		return
	}
	value, err := s.vault.GetStore(s.tenant, key)
	switch {
	case errors.Is(err, vault.ErrStoreNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no value stored for this key")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": key, "value": value})
	}
}

func (s *Server) handleStorePut(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !storeKeyPattern.MatchString(key) {
		writeError(w, http.StatusBadRequest, "invalid_key", "key must match ^[a-z0-9][a-z0-9_-]{0,31}$")
		return
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, storeValueLimit+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
		return
	}
	if len(body) > storeValueLimit {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large",
			"value exceeds 7 MiB")
		return
	}
	if err := s.vault.PutStore(s.tenant, key, string(body), time.Now()); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": key, "bytes": len(body)})
}

func (s *Server) handleStoreDelete(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !storeKeyPattern.MatchString(key) {
		writeError(w, http.StatusBadRequest, "invalid_key", "key must match ^[a-z0-9][a-z0-9_-]{0,31}$")
		return
	}
	if err := s.vault.DeleteStore(s.tenant, key); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": key})
}
