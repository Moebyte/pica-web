package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"picagateway/internal/imgproxy"
)

const (
	maxImageSignBatchItems = 300
	maxImageSignBatchBody  = 1 << 20
)

// handleImageSignBatch signs many image URLs in one request. The web reader
// primes a whole chapter at once so every page then costs a single GET
// instead of sign+GET — one saved round trip per page over slow links.
//
// Request: {"ttl":86400,"items":[{"url":"...","headers":{},"profile":"jm","jm":{...}}, ...]}
// Response: {"ok":true,"urls":["/api/img/<token>", ...],"expiresAt":"..."}
func (s *Server) handleImageSignBatch(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxImageSignBatchBody+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
		return
	}
	if len(raw) > maxImageSignBatchBody {
		writeError(w, http.StatusBadRequest, "body_too_large",
			fmt.Sprintf("request body exceeds %d bytes", maxImageSignBatchBody))
		return
	}
	var req struct {
		TTL   *int `json:"ttl"`
		Items []struct {
			URL     string             `json:"url"`
			Headers map[string]string  `json:"headers"`
			Profile string             `json:"profile"`
			JM      *imgproxy.JMParams `json:"jm"`
		} `json:"items"`
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		writeError(w, http.StatusBadRequest, "missing_items", "request body must contain items")
		return
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json",
			"request body is not valid JSON: "+err.Error())
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "missing_items", "items must not be empty")
		return
	}
	if len(req.Items) > maxImageSignBatchItems {
		writeError(w, http.StatusBadRequest, "too_many_items",
			fmt.Sprintf("at most %d items are allowed", maxImageSignBatchItems))
		return
	}
	ttl, ok := s.parseImageSignTTL(w, req.TTL)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Timeout())
	defer cancel()

	urls := make([]string, len(req.Items))
	var latestExpiry time.Time
	for i, item := range req.Items {
		rawURL := strings.TrimSpace(item.URL)
		if rawURL == "" {
			writeError(w, http.StatusBadRequest, "missing_url",
				fmt.Sprintf("items[%d].url is required", i))
			return
		}
		profile := strings.TrimSpace(item.Profile)
		if profile != "" {
			if _, exists := s.cfg.HeaderProfiles[profile]; !exists {
				writeError(w, http.StatusBadRequest, "unknown_profile",
					fmt.Sprintf("items[%d].profile: no such header profile", i))
				return
			}
		}
		if _, _, err := s.policy.ValidateURL(ctx, rawURL); err != nil {
			s.writePolicyError(w, err)
			return
		}
		token, exp := s.signer.SignPayload(imgproxy.Payload{
			URL:     rawURL,
			Profile: profile,
			Headers: item.Headers,
			JM:      item.JM,
		}, ttl, time.Now())
		urls[i] = "/api/img/" + token
		if exp.After(latestExpiry) {
			latestExpiry = exp
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"urls":      urls,
		"expiresAt": latestExpiry.UTC().Format(time.RFC3339),
	})
}
