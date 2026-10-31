package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"picagateway/internal/outbound"
)

var methodPattern = regexp.MustCompile(`^[A-Z]+$`)

// allowedMethods keeps the tunnel to normal HTTP verbs. CONNECT/TRACE are
// intentionally unsupported.
var allowedMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodPost:    true,
	http.MethodPut:     true,
	http.MethodPatch:   true,
	http.MethodDelete:  true,
	http.MethodOptions: true,
}

func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimSpace(r.Header.Get("X-Target-URL"))
	if target == "" {
		writeError(w, http.StatusBadRequest, "missing_target", "X-Target-URL header is required")
		return
	}
	method := strings.ToUpper(strings.TrimSpace(r.Header.Get("X-Target-Method")))
	if method == "" {
		method = http.MethodGet
	}
	if !methodPattern.MatchString(method) || !allowedMethods[method] {
		writeError(w, http.StatusBadRequest, "invalid_method", "unsupported X-Target-Method")
		return
	}
	follow := true
	switch strings.TrimSpace(r.Header.Get("X-Pica-Follow")) {
	case "", "1":
		follow = true
	case "0":
		follow = false
	default:
		writeError(w, http.StatusBadRequest, "invalid_follow", "X-Pica-Follow must be 0 or 1")
		return
	}

	// Decode before touching the target/body so malformed input always yields
	// 400 invalid_req_headers.
	var overlay http.Header
	if encoded := r.Header.Get("X-Pica-Req-Headers"); encoded != "" {
		decoded, decErr := decodeRequestHeaders(encoded)
		if decErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_req_headers", decErr.Error())
			return
		}
		overlay = decoded
	}

	body, err := s.readRequestBody(w, r)
	if err != nil {
		return // readRequestBody already wrote the error
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Timeout())
	defer cancel()

	u, _, err := s.policy.ValidateURL(ctx, target)
	if err != nil {
		// 源站换域名/新增 CDN 时最先在这里暴露：记录被拒目标，方便补白名单。
		s.log.Warn("tunnel target rejected", "target", target, "error", err.Error())
		s.writePolicyError(w, err)
		return
	}

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bodyReader)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	copyTunnelRequestHeaders(req.Header, r.Header)
	if overlay != nil {
		mergeDecodedRequestHeaders(req.Header, overlay)
	}

	var redirectErr error
	client := &http.Client{
		Transport: s.upstream,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if !follow {
				return http.ErrUseLastResponse
			}
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
			s.writePolicyError(w, redirectErr)
			return
		}
		var policyErr *outbound.Error
		if errors.As(err, &policyErr) {
			s.log.Warn("tunnel request rejected by policy", "target", target, "error", err.Error())
			s.writePolicyError(w, err)
			return
		}
		if errors.Is(err, outbound.ErrTooManyRedirects) {
			writeError(w, http.StatusBadGateway, "too_many_redirects", "upstream redirected too many times")
			return
		}
		if ctx.Err() != nil {
			writeError(w, http.StatusBadGateway, "upstream_timeout", "upstream request timed out")
			return
		}
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	defer resp.Body.Close()

	s.saveResponseCookies(resp)

	// 上游 4xx/5xx 会被浏览器打进 console（比如 JM 登录前的线路探测 401），
	// 这里留一条带目标地址的日志，方便和用户报错对上。
	if resp.StatusCode >= 400 {
		level := slog.LevelInfo
		if resp.StatusCode >= 500 {
			level = slog.LevelWarn
		}
		s.log.Log(r.Context(), level, "tunnel upstream status",
			"target", target, "status", resp.StatusCode)
	}

	headers := sanitizeResponseHeaders(resp.Header)
	if resp.ContentLength > s.cfg.RespLimit() {
		writeError(w, http.StatusBadGateway, "response_too_large",
			fmt.Sprintf("upstream response exceeds %d MiB", s.cfg.MaxRespMB))
		return
	}

	setCookies := []string{}
	if s.cfg.MetaSetCookies {
		setCookies = append(setCookies, resp.Header.Values("Set-Cookie")...)
	}
	meta := &tunnelMeta{
		Status:     resp.StatusCode,
		Headers:    map[string][]string(headers),
		SetCookies: setCookies,
		FinalURL:   resp.Request.URL.String(),
	}
	encoded, err := encodeMeta(meta)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "meta_encode_failed", err.Error())
		return
	}

	// fetch() follows 3xx automatically. When the client asked for manual
	// redirect handling, expose the real status through X-Pica-Meta but send a
	// transport-level 200 so the browser cannot silently follow the redirect.
	transportStatus := resp.StatusCode
	if !follow && transportStatus >= 300 && transportStatus < 400 {
		transportStatus = http.StatusOK
	}

	for k, vv := range headers {
		w.Header()[k] = append([]string(nil), vv...)
	}
	w.Header().Set("X-Pica-Meta", encoded)
	w.WriteHeader(transportStatus)

	if method == http.MethodHead || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		return
	}

	var dst io.Writer = w
	if f, ok := w.(http.Flusher); ok {
		dst = &flushWriter{w: w, f: f}
	}
	counter := &maxWriter{w: dst, max: s.cfg.RespLimit()}
	if _, err := io.Copy(counter, resp.Body); err != nil {
		if errors.Is(err, errResponseTooLarge) {
			s.log.Warn("upstream response truncated at limit", "limit_bytes", s.cfg.RespLimit())
		} else if r.Context().Err() == nil {
			s.log.Debug("tunnel body copy stopped", "error", err)
		}
	}
}

// readRequestBody buffers at most maxBodyMB. Tunnel bodies are replayed on
// redirects, so buffering is required anyway.
func (s *Server) readRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	limit := s.cfg.BodyLimit()
	if r.ContentLength > limit {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large",
			fmt.Sprintf("request body exceeds %d MiB", s.cfg.MaxBodyMB))
		return nil, errTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
		return nil, err
	}
	if int64(len(body)) > limit {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large",
			fmt.Sprintf("request body exceeds %d MiB", s.cfg.MaxBodyMB))
		return nil, errTooLarge
	}
	return body, nil
}

var errTooLarge = errors.New("body too large")

func (s *Server) saveResponseCookies(resp *http.Response) {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return
	}
	lines := resp.Header.Values("Set-Cookie")
	if len(lines) == 0 {
		return
	}
	if _, _, err := s.vault.SaveSetCookies(s.tenant, resp.Request.URL, lines, time.Now()); err != nil {
		s.log.Warn("cookie vault save failed", "host", resp.Request.URL.Hostname(), "error", err)
	}
}

func (s *Server) writePolicyError(w http.ResponseWriter, err error) {
	var pErr *outbound.Error
	if errors.As(err, &pErr) {
		status := pErr.Status
		if status == 0 {
			status = http.StatusForbidden
		}
		writeError(w, status, pErr.Code, pErr.Message)
		return
	}
	writeError(w, http.StatusForbidden, "target_blocked", err.Error())
}
