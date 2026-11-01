package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"picagateway/internal/imgproxy"
	"picagateway/internal/jmimage"
	"picagateway/internal/outbound"
)

// maxImageSignBody bounds the JSON body of POST /api/img/sign.
const maxImageSignBody = 64 << 10

// maxImageCacheAge caps the browser Cache-Control lifetime of an image
// response. The token itself may live longer (up to the aligned window plus
// grace), but one day is the useful ceiling for an episode page.
const maxImageCacheAge = 86400

func (s *Server) handleImageSign(w http.ResponseWriter, r *http.Request) {
	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	profile := strings.TrimSpace(r.URL.Query().Get("profile"))
	ttl, ok := s.imageSignTTL(w, r.URL.Query().Get("ttl"))
	if !ok {
		return
	}
	s.writeImageSign(w, r, rawURL, profile, nil, nil, ttl)
}

// handleImageSignPost issues a signed image URL from a JSON body:
//
//	{"url":"https://...","headers":{"Referer":"https://..."},"ttl":86400}
//
// Profile and JM scramble parameters are accepted as optional extensions so
// the Flutter web client can ask the gateway to recombine JM pages. The
// response has the same shape as the legacy GET endpoint.
func (s *Server) handleImageSignPost(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxImageSignBody+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
		return
	}
	if len(raw) > maxImageSignBody {
		writeError(w, http.StatusBadRequest, "body_too_large",
			fmt.Sprintf("request body exceeds %d bytes", maxImageSignBody))
		return
	}
	var req struct {
		URL     string             `json:"url"`
		Headers map[string]string  `json:"headers"`
		Profile string             `json:"profile"`
		TTL     *int               `json:"ttl"`
		JM      *imgproxy.JMParams `json:"jm"`
	}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON: "+err.Error())
			return
		}
	}

	ttl := s.cfg.ImgTokenTTLDuration()
	if req.TTL != nil {
		if *req.TTL <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_ttl", "ttl must be a positive number of seconds")
			return
		}
		ttl = time.Duration(*req.TTL) * time.Second
		if ttl > imgproxy.MaxTokenTTL {
			ttl = imgproxy.MaxTokenTTL
		}
	}

	s.writeImageSign(w, r, strings.TrimSpace(req.URL), strings.TrimSpace(req.Profile),
		req.Headers, req.JM, ttl)
}

// imageSignTTL parses the optional GET ttl query parameter.
func (s *Server) imageSignTTL(w http.ResponseWriter, rawTTL string) (time.Duration, bool) {
	ttl := s.cfg.ImgTokenTTLDuration()
	rawTTL = strings.TrimSpace(rawTTL)
	if rawTTL == "" {
		return ttl, true
	}
	seconds, err := strconv.Atoi(rawTTL)
	if err != nil || seconds <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_ttl", "ttl must be a positive number of seconds")
		return 0, false
	}
	ttl = time.Duration(seconds) * time.Second
	if ttl > imgproxy.MaxTokenTTL {
		ttl = imgproxy.MaxTokenTTL
	}
	return ttl, true
}

// writeImageSign validates the common signing inputs and returns the token.
// parseImageSignTTL validates an optional ttl (seconds) and clamps it to the
// token maximum. It writes an error response and returns ok=false on failure.
func (s *Server) parseImageSignTTL(w http.ResponseWriter, ttlParam *int) (time.Duration, bool) {
	ttl := s.cfg.ImgTokenTTLDuration()
	if ttlParam != nil {
		if *ttlParam <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_ttl", "ttl must be a positive number of seconds")
			return 0, false
		}
		ttl = time.Duration(*ttlParam) * time.Second
		if ttl > imgproxy.MaxTokenTTL {
			ttl = imgproxy.MaxTokenTTL
		}
	}
	return ttl, true
}

func (s *Server) writeImageSign(w http.ResponseWriter, r *http.Request, rawURL, profile string,
	headers map[string]string, jm *imgproxy.JMParams, ttl time.Duration) {
	if rawURL == "" {
		writeError(w, http.StatusBadRequest, "missing_url", "url is required")
		return
	}
	if profile != "" {
		if _, ok := s.cfg.HeaderProfiles[profile]; !ok {
			writeError(w, http.StatusBadRequest, "unknown_profile", "no such header profile")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Timeout())
	defer cancel()
	if _, _, err := s.policy.ValidateURL(ctx, rawURL); err != nil {
		s.writePolicyError(w, err)
		return
	}

	now := time.Now()
	token, exp := s.signer.SignPayload(imgproxy.Payload{
		URL:     rawURL,
		Profile: profile,
		Headers: headers,
		JM:      jm,
	}, ttl, now)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"token":     token,
		"url":       "/api/img/" + token,
		"expiresAt": exp.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	payload, err := s.signer.Verify(r.PathValue("token"), time.Now())
	if err != nil {
		switch {
		case errors.Is(err, imgproxy.ErrExpiredToken):
			writeError(w, http.StatusForbidden, "image_token_expired", "image token has expired")
		default:
			writeError(w, http.StatusForbidden, "invalid_image_token", "image token is invalid")
		}
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Timeout())
	defer cancel()

	target, _, err := s.policy.ValidateURL(ctx, payload.URL)
	if err != nil {
		s.writePolicyError(w, err)
		return
	}

	key := imageCacheKey(payload)

	if s.cache.Enabled() {
		if entry, ok := s.cache.Get(key); ok {
			if entry.Size > s.cfg.RespLimit() {
				entry.File.Close()
				writeError(w, http.StatusBadGateway, "response_too_large", "cached image exceeds the response limit")
				return
			}
			defer entry.File.Close()
			w.Header().Set("Content-Type", entry.Mime)
			w.Header().Set("ETag", `"`+key+`"`)
			w.Header().Set("X-Cache", "HIT")
			// Signed GETs must be browser-cacheable: the URL is deterministic
			// for the token's lifetime, so a second page load can be served
			// entirely from the browser cache.
			w.Header().Set("Cache-Control", imageCacheControl(payload, time.Now()))
			addImageSandbox(w, entry.Mime)
			http.ServeContent(w, r, "", time.Time{}, entry.File)
			return
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if payload.Profile != "" {
		for k, v := range s.cfg.HeaderProfiles[payload.Profile] {
			req.Header.Set(k, v)
		}
	}
	// Headers carried by the token (already filtered at signing time and
	// filtered again by Verify) override the profile defaults.
	for k, v := range payload.Headers {
		req.Header.Set(k, v)
	}
	// JM tokens buffer the whole body for recombination; range requests are
	// only honored on the streaming path (and later, on cache hits).
	if payload.JM == nil {
		if rg := r.Header.Get("Range"); rg != "" {
			req.Header.Set("Range", rg)
			if ir := r.Header.Get("If-Range"); ir != "" {
				req.Header.Set("If-Range", ir)
			}
		}
	}

	var redirectErr error
	client := &http.Client{
		Transport: s.upstream,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
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
			s.writePolicyError(w, err)
			return
		}
		if errors.Is(err, outbound.ErrTooManyRedirects) {
			writeError(w, http.StatusBadGateway, "too_many_redirects", "image redirected too many times")
			return
		}
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	defer resp.Body.Close()

	s.saveResponseCookies(resp)

	if resp.ContentLength > s.cfg.RespLimit() {
		writeError(w, http.StatusBadGateway, "response_too_large", "image exceeds the response limit")
		return
	}

	if payload.JM != nil {
		s.serveJMImage(w, r, resp, payload, key)
		return
	}

	outHeaders := sanitizeResponseHeaders(resp.Header)
	for k, vv := range outHeaders {
		w.Header()[k] = append([]string(nil), vv...)
	}

	mimeType := mediaType(resp.Header.Get("Content-Type"))
	isImage := strings.HasPrefix(mimeType, "image/")
	rangeRequested := r.Header.Get("Range") != ""
	cacheable := s.cache.Enabled() && resp.StatusCode == http.StatusOK && isImage &&
		!rangeRequested && resp.Header.Get("Content-Encoding") == ""
	if resp.StatusCode == http.StatusOK {
		w.Header().Set("Cache-Control", imageCacheControl(payload, time.Now()))
	}
	if cacheable {
		w.Header().Set("X-Cache", "MISS")
	} else {
		w.Header().Set("X-Cache", "BYPASS")
	}
	addImageSandbox(w, mimeType)
	w.WriteHeader(resp.StatusCode)

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		return
	}
	var dst io.Writer = w
	if f, ok := w.(http.Flusher); ok {
		dst = &flushWriter{w: w, f: f}
	}
	out := &maxWriter{w: dst, max: s.cfg.RespLimit()}

	writer := (*imgproxy.Writer)(nil)
	if cacheable {
		w, err := s.cache.Begin(key)
		if err != nil {
			s.log.Debug("image cache begin failed", "error", err)
		} else {
			writer = w
		}
	}
	if writer != nil {
		defer writer.Abort()
		out.w = io.MultiWriter(dst, writer)
	}
	_, copyErr := io.Copy(out, resp.Body)
	if copyErr == nil && writer != nil {
		if err := writer.Commit(mimeType); err != nil {
			s.log.Debug("image cache commit failed", "error", err)
		}
	}
	if copyErr != nil && !errors.Is(copyErr, errResponseTooLarge) && r.Context().Err() == nil {
		s.log.Debug("image body copy stopped", "error", copyErr)
	}
}

// imageCacheKey derives the disk cache key for a token payload. The key
// covers everything that can change the upstream bytes or the transform:
// URL, header profile, token headers and JM scramble parameters. Tokens with
// JM scramble parameters get their own namespace because the cached bytes are
// the recombined image.
func imageCacheKey(p *imgproxy.Payload) string {
	h := sha256.New()
	h.Write([]byte(p.URL))
	if p.Profile != "" {
		h.Write([]byte{0})
		h.Write([]byte(p.Profile))
	}
	if len(p.Headers) > 0 {
		names := make([]string, 0, len(p.Headers))
		for name := range p.Headers {
			names = append(names, name)
		}
		sort.Strings(names)
		h.Write([]byte{0, 'h'})
		for _, name := range names {
			h.Write([]byte(name))
			h.Write([]byte{0})
			h.Write([]byte(p.Headers[name]))
			h.Write([]byte{0})
		}
	}
	if p.JM != nil {
		h.Write([]byte{0})
		h.Write([]byte(p.JM.EpsID))
		h.Write([]byte{0})
		h.Write([]byte(p.JM.ScrambleID))
		h.Write([]byte{0})
		h.Write([]byte(p.JM.BookID))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// imageCacheControl derives the browser cache lifetime from the remaining
// token validity, capped at one day. Signed image URLs are deterministic for
// their whole window, so the browser can reuse the response across reloads.
func imageCacheControl(p *imgproxy.Payload, now time.Time) string {
	maxAge := p.Exp - now.Unix()
	if maxAge < 0 {
		maxAge = 0
	}
	if maxAge > maxImageCacheAge {
		maxAge = maxImageCacheAge
	}
	return fmt.Sprintf("private, max-age=%d", maxAge)
}

// serveJMImage serves a JM page image. JM scrambles both JPEGs and WebPs by
// reordering vertical blocks; on a cache miss the body is buffered, recombined
// and only the recombined result is cached and returned. JPEG/WebP input is
// re-encoded as JPEG (the gateway has no pure-Go WebP encoder); other payloads
// are passed through unchanged with a warning.
func (s *Server) serveJMImage(w http.ResponseWriter, r *http.Request, resp *http.Response, payload *imgproxy.Payload, key string) {
	mimeType := mediaType(resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(io.LimitReader(resp.Body, s.cfg.RespLimit()+1))
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	if int64(len(body)) > s.cfg.RespLimit() {
		writeError(w, http.StatusBadGateway, "response_too_large", "image exceeds the response limit")
		return
	}

	num := 0
	if epsID, perr := strconv.Atoi(strings.TrimSpace(payload.JM.EpsID)); perr != nil {
		s.log.Warn("image token jm.epsId is not a number; serving as-is", "target", payload.URL, "value", payload.JM.EpsID)
	} else if scrambleID, serr := strconv.Atoi(strings.TrimSpace(payload.JM.ScrambleID)); serr != nil {
		s.log.Warn("image token jm.scrambleId is not a number; serving as-is", "target", payload.URL, "value", payload.JM.ScrambleID)
	} else {
		num = jmimage.SegmentationNum(epsID, scrambleID, payload.JM.BookID)
	}

	ext := ""
	if u, uerr := url.Parse(payload.URL); uerr == nil {
		ext = strings.ToLower(strings.TrimPrefix(path.Ext(u.Path), "."))
	}
	if num > 1 {
		supported := jmimage.IsJPEG(ext, mimeType) || jmimage.IsWebP(ext, mimeType) ||
			jmimage.IsJPEGBytes(body) || jmimage.IsWebPBytes(body)
		if !supported {
			s.log.Warn("jm recombine skipped: payload is not JPEG or WebP",
				"target", payload.URL, "contentType", mimeType, "ext", ext)
		} else {
			out, rerr := jmimage.Recombine(body, num)
			if rerr != nil {
				writeError(w, http.StatusBadGateway, "jm_recombine_failed", rerr.Error())
				return
			}
			body = out
			mimeType = "image/jpeg"
		}
	}

	cacheable := s.cache.Enabled() && resp.StatusCode == http.StatusOK &&
		strings.HasPrefix(mimeType, "image/") && resp.Header.Get("Content-Encoding") == ""

	outHeaders := sanitizeResponseHeaders(resp.Header)
	outHeaders.Del("Content-Length")
	for k, vv := range outHeaders {
		w.Header()[k] = append([]string(nil), vv...)
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("ETag", `"`+key+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if resp.StatusCode == http.StatusOK {
		w.Header().Set("Cache-Control", imageCacheControl(payload, time.Now()))
	}
	if cacheable {
		w.Header().Set("X-Cache", "MISS")
	} else {
		w.Header().Set("X-Cache", "BYPASS")
	}
	addImageSandbox(w, mimeType)
	w.WriteHeader(resp.StatusCode)
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified && r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}

	if !cacheable {
		return
	}
	if writer, werr := s.cache.Begin(key); werr != nil {
		s.log.Debug("image cache begin failed", "error", werr)
	} else {
		_, _ = writer.Write(body)
		if cerr := writer.Commit(mimeType); cerr != nil {
			s.log.Debug("image cache commit failed", "error", cerr)
		}
	}
}

func mediaType(contentType string) string {
	if contentType == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.TrimSpace(strings.Split(contentType, ";")[0])
	}
	return strings.ToLower(mt)
}

func addImageSandbox(w http.ResponseWriter, mimeType string) {
	// Direct navigation to an untrusted SVG/HTML response must not run scripts
	// in the gateway's origin.
	if mimeType == "" || !strings.HasPrefix(mimeType, "image/") || mimeType == "image/svg+xml" {
		w.Header().Set("Content-Security-Policy", "sandbox")
	}
}
