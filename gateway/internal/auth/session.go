// Package auth issues and verifies HMAC session cookies.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"picagateway/internal/secret"
)

// CookieName is the gateway session cookie name.
const CookieName = "pica_session"

// Tenant is the single-tenant identifier used by phase 1.
const Tenant = "default"

type claims struct {
	Sub string `json:"sub"`
	Exp int64  `json:"exp"`
}

// Manager signs and verifies session tokens.
type Manager struct {
	key []byte
	ttl time.Duration
}

// NewManager derives a session key from the master secret.
func NewManager(masterSecret string, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Manager{key: secret.Derive(masterSecret, "session"), ttl: ttl}
}

// Issue returns a signed token and its expiry.
func (m *Manager) Issue(now time.Time) (string, time.Time) {
	exp := now.Add(m.ttl)
	payload, _ := json.Marshal(claims{Sub: Tenant, Exp: exp.Unix()})
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(m.sign([]byte(body))), exp
}

func (m *Manager) sign(body []byte) []byte {
	mac := hmac.New(sha256.New, m.key)
	mac.Write(body)
	return mac.Sum(nil)
}

// Validate verifies the token signature and expiry.
func (m *Manager) Validate(token string, now time.Time) (time.Time, bool) {
	body, sig, ok := strings.Cut(token, ".")
	if !ok || body == "" || sig == "" {
		return time.Time{}, false
	}
	want := m.sign([]byte(body))
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, want) {
		return time.Time{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return time.Time{}, false
	}
	var c claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, false
	}
	if c.Sub != Tenant || c.Exp <= now.Unix() {
		return time.Time{}, false
	}
	return time.Unix(c.Exp, 0), true
}

// Cookie builds the session Set-Cookie.
func (m *Manager) Cookie(token string, secure bool, now time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(m.ttl.Seconds()),
		Expires:  now.Add(m.ttl),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// ClearCookie builds a cookie that deletes the session.
func (m *Manager) ClearCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// CheckPassword compares passwords in constant time.
func CheckPassword(configured, given string) bool {
	a := sha256.Sum256([]byte(configured))
	b := sha256.Sum256([]byte(given))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
