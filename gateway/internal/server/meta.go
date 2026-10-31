package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// tunnelMeta is the JSON document carried in X-Pica-Meta.
type tunnelMeta struct {
	Status     int                 `json:"status"`
	Headers    map[string][]string `json:"headers"`
	SetCookies []string            `json:"setCookies"`
	FinalURL   string              `json:"finalUrl"`
}

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	// Marks gateway-generated errors so adapters can distinguish them from a
	// tunneled upstream response with the same status code.
	w.Header().Set("X-Pica-Error", code)
	writeJSON(w, status, errorBody{Error: code, Message: message})
}

// flushWriter flushes after every chunk so tunneled bodies stream.
type flushWriter struct {
	w io.Writer
	f http.Flusher
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fw.f != nil {
		fw.f.Flush()
	}
	return n, err
}

var errResponseTooLarge = errors.New("upstream response exceeds limit")

// maxWriter writes at most max bytes and then returns errResponseTooLarge.
type maxWriter struct {
	w   io.Writer
	max int64
	n   int64
}

func (m *maxWriter) Write(p []byte) (int, error) {
	if m.n >= m.max {
		return 0, errResponseTooLarge
	}
	remaining := m.max - m.n
	if int64(len(p)) > remaining {
		n, err := m.w.Write(p[:remaining])
		m.n += int64(n)
		if err != nil {
			return n, err
		}
		return n, errResponseTooLarge
	}
	n, err := m.w.Write(p)
	m.n += int64(n)
	return n, err
}
