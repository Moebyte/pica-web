// Package imgproxy implements signed image URLs and the on-disk image cache.
package imgproxy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"picagateway/internal/secret"
)

// ErrInvalidToken is returned for malformed or forged tokens.
var ErrInvalidToken = errors.New("invalid image token")

// ErrExpiredToken is returned when a valid token has expired.
var ErrExpiredToken = errors.New("expired image token")

// JMParams carries the JM (18comic) scramble parameters. When present, the
// image proxy recombines the vertical block scramble of JPEG and WebP
// responses on a cache miss (other payloads are served unchanged).
type JMParams struct {
	EpsID      string `json:"epsId"`
	ScrambleID string `json:"scrambleId"`
	BookID     string `json:"bookId"`
}

// Payload is the signed portion of an image token.
//
// Headers are the upstream request headers the token authorizes (for example
// Referer, User-Agent or Accept). They are part of the signature so an image
// can be fetched by the browser with a plain GET while the gateway replays
// the source-specific headers. Hop-by-hop, browser-managed and gateway
// control headers are filtered out both when signing and when verifying.
type Payload struct {
	URL     string            `json:"url"`
	Profile string            `json:"profile,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	JM      *JMParams         `json:"jm,omitempty"`
	Exp     int64             `json:"exp"`
}

// Signer signs and verifies image tokens of the form
// base64url(payload) "." base64url(HMAC-SHA256).
type Signer struct {
	key []byte
}

// NewSigner derives an image-token key from the master secret.
func NewSigner(masterSecret string) *Signer {
	return &Signer{key: secret.Derive(masterSecret, "img-token")}
}

// DefaultTokenTTL is used when a caller does not pass a TTL.
const DefaultTokenTTL = 24 * time.Hour

// MaxTokenTTL bounds the lifetime a caller may request.
const MaxTokenTTL = 7 * 24 * time.Hour

// tokenGrace keeps a token valid for a while after its window ends so a page
// that was rendered just before the boundary still loads its images.
const tokenGrace = 2 * time.Hour

// AlignedExpiry returns a token expiry that depends only on the issuance
// window and the requested TTL, never on the exact second of signing. Two
// requests for the same image therefore produce byte-identical tokens (and
// therefore identical URLs) for as long as the window lasts, which is what
// lets browsers serve the image from their HTTP cache.
//
// Expiries are aligned to fixed UTC multiples of ttl (for the default 24h TTL:
// the next UTC midnight) plus a 2h grace period. The common 1h TTL yields a
// token valid for 2h-3h; the default 24h TTL yields 2h-26h.
func AlignedExpiry(now time.Time, ttl time.Duration) time.Time {
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	if ttl > MaxTokenTTL {
		ttl = MaxTokenTTL
	}
	// Truncate works on the absolute time (zero time is UTC), so the result
	// is stable across server time zones.
	return now.Truncate(ttl).Add(ttl + tokenGrace)
}

// Sign issues a token and its expiry.
func (s *Signer) Sign(rawURL, profile string, ttl time.Duration, now time.Time) (string, time.Time) {
	return s.SignPayload(Payload{URL: rawURL, Profile: profile}, ttl, now)
}

// SignPayload issues a token for a fully specified payload (used for tokens
// carrying extra fields such as request headers or the JM scramble
// parameters). Headers are sanitized; blocked entries are dropped.
func (s *Signer) SignPayload(p Payload, ttl time.Duration, now time.Time) (string, time.Time) {
	p.Headers = SanitizeHeaders(p.Headers)
	exp := AlignedExpiry(now, ttl)
	p.Exp = exp.Unix()
	payload, _ := json.Marshal(p)
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(s.sign([]byte(body))), exp
}

func (s *Signer) sign(body []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(body)
	return mac.Sum(nil)
}

// Verify checks the signature and expiry and returns the payload.
func (s *Signer) Verify(token string, now time.Time) (*Payload, error) {
	body, sig, ok := strings.Cut(token, ".")
	if !ok || body == "" || sig == "" || len(token) > 16<<10 {
		return nil, ErrInvalidToken
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.sign([]byte(body))) {
		return nil, ErrInvalidToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, ErrInvalidToken
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, ErrInvalidToken
	}
	if p.URL == "" {
		return nil, ErrInvalidToken
	}
	if p.Exp <= now.Unix() {
		return nil, ErrExpiredToken
	}
	// Defense in depth: never replay a header that could not have been signed
	// through this package (for example an older token format).
	p.Headers = SanitizeHeaders(p.Headers)
	return &p, nil
}

// blockedTokenHeaders are request headers a signed image URL must never
// replay. Cookie is injected by the gateway cookie vault, Host/Content-Length
// and the hop-by-hop set are connection scoped, and X-*/Proxy-* can override
// proxy or upstream behavior.
var blockedTokenHeaders = map[string]bool{
	"Host":              true,
	"Content-Length":    true,
	"Cookie":            true,
	"Cookie2":           true,
	"Set-Cookie":        true,
	"Accept-Encoding":   true, // let the Go transport negotiate compression
	"Connection":        true,
	"Keep-Alive":        true,
	"Te":                true,
	"Trailer":           true,
	"Transfer-Encoding": true,
	"Upgrade":           true,
}

// SanitizeHeaders canonicalizes names, trims values and drops headers that a
// token must not carry. The returned map has no empty entries; nil input
// yields nil so payloads without headers keep a stable JSON representation.
func SanitizeHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for name, value := range in {
		name = http.CanonicalHeaderKey(strings.TrimSpace(name))
		if name == "" || blockedTokenHeaders[name] || !validTokenHeader(name) {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" || !validTokenHeaderValue(value) {
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validTokenHeader rejects header names that need special handling: they are
// either browser-managed (Sec-*), gateway control headers (X-*) or proxy
// headers (Proxy-*).
func validTokenHeader(name string) bool {
	switch {
	case strings.HasPrefix(name, "X-"):
		return false
	case strings.HasPrefix(name, "Proxy-"):
		return false
	case strings.HasPrefix(name, "Sec-"):
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)):
		default:
			return false
		}
	}
	return true
}

func validTokenHeaderValue(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}
