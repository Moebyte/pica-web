// Package imgproxy implements signed image URLs and the on-disk image cache.
package imgproxy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"picagateway/internal/secret"
)

// ErrInvalidToken is returned for malformed or forged tokens.
var ErrInvalidToken = errors.New("invalid image token")

// ErrExpiredToken is returned when a valid token has expired.
var ErrExpiredToken = errors.New("expired image token")

// JMParams carries the JM (18comic) scramble parameters. When present, the
// image proxy recombines the vertical block scramble of JPEG responses on a
// cache miss (non-JPEG payloads are served unchanged with a warning).
type JMParams struct {
	EpsID      string `json:"epsId"`
	ScrambleID string `json:"scrambleId"`
	BookID     string `json:"bookId"`
}

// Payload is the signed portion of an image token.
type Payload struct {
	URL     string    `json:"url"`
	Profile string    `json:"profile,omitempty"`
	JM      *JMParams `json:"jm,omitempty"`
	Exp     int64     `json:"exp"`
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

// Sign issues a token and its expiry.
func (s *Signer) Sign(rawURL, profile string, ttl time.Duration, now time.Time) (string, time.Time) {
	return s.SignPayload(Payload{URL: rawURL, Profile: profile}, ttl, now)
}

// SignPayload issues a token for a fully specified payload (used for tokens
// carrying extra fields such as the JM scramble parameters).
func (s *Signer) SignPayload(p Payload, ttl time.Duration, now time.Time) (string, time.Time) {
	exp := now.Add(ttl)
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
	return &p, nil
}
