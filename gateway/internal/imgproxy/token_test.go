package imgproxy

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestImageToken(t *testing.T) {
	s := NewSigner("master")
	now := time.Now()
	token, exp := s.Sign("https://example.com/a.png", "profile-a", time.Hour, now)
	if token == "" || !exp.After(now) {
		t.Fatalf("token=%q exp=%v", token, exp)
	}
	p, err := s.Verify(token, now)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if p.URL != "https://example.com/a.png" || p.Profile != "profile-a" {
		t.Fatalf("payload = %+v", p)
	}

	if _, err := s.Verify(token, exp.Add(time.Second)); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("expired err = %v", err)
	}
	if _, err := s.Verify(token+"x", now); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("tampered err = %v", err)
	}
	if _, err := NewSigner("other").Verify(token, now); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong key err = %v", err)
	}
}

// Tokens must be byte-identical for the same inputs inside the expiry
// window; browsers only reuse their HTTP cache when the URL repeats verbatim.
func TestTokenDeterministicWithinWindow(t *testing.T) {
	s := NewSigner("master")
	day := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	inputs := []struct {
		url     string
		profile string
		headers map[string]string
	}{
		{"https://cdn.example/a.webp", "jm", nil},
		{"https://cdn.example/b.webp", "", map[string]string{"Referer": "https://localhost/"}},
	}
	for _, in := range inputs {
		first, exp1 := s.SignPayload(Payload{URL: in.url, Profile: in.profile, Headers: in.headers},
			24*time.Hour, day.Add(1*time.Minute))
		second, exp2 := s.SignPayload(Payload{URL: in.url, Profile: in.profile, Headers: in.headers},
			24*time.Hour, day.Add(23*time.Hour+59*time.Minute))
		if first != second || !exp1.Equal(exp2) {
			t.Fatalf("same window tokens differ:\n%s\n%s", first, second)
		}
		if want := day.Add(24*time.Hour + 2*time.Hour); !exp1.Equal(want) {
			t.Fatalf("exp = %v, want %v", exp1, want)
		}

		next, expNext := s.SignPayload(Payload{URL: in.url, Profile: in.profile, Headers: in.headers},
			24*time.Hour, day.Add(24*time.Hour+time.Minute))
		if next == first || expNext.Equal(exp1) {
			t.Fatal("token from the next window must differ")
		}
	}
}

func TestTokenDeterministicAcrossFieldOrder(t *testing.T) {
	s := NewSigner("master")
	now := time.Now()
	a, _ := s.SignPayload(Payload{URL: "https://x/a", Headers: map[string]string{
		"referer": "https://localhost/", "user-agent": "UA",
	}}, time.Hour, now)
	b, _ := s.SignPayload(Payload{URL: "https://x/a", Headers: map[string]string{
		"User-Agent": "UA", "Referer": "https://localhost/",
	}}, time.Hour, now)
	if a != b {
		t.Fatal("header casing/order must not change the token")
	}
}

func TestSanitizeTokenHeaders(t *testing.T) {
	in := map[string]string{
		"Host":                "evil.example",
		"Content-Length":      "10",
		"Cookie":              "sid=1",
		"Accept-Encoding":     "br",
		"Connection":          "keep-alive",
		"TE":                  "trailers",
		"Trailer":             "X-Trailer",
		"Transfer-Encoding":   "chunked",
		"Upgrade":             "h2c",
		"Proxy-Authorization": "Basic abc",
		"X-Forwarded-For":     "1.2.3.4",
		"X-Target-URL":        "http://169.254.169.254/",
		"Sec-Fetch-Mode":      "no-cors",
		"User-Agent":          "  Dalvik/2.1.0  ",
		"Referer":             "https://localhost/",
		"Accept":              "image/webp",
		"Accept-Language":     "zh-CN",
		"Origin":              "https://localhost",
		"Bad\x01Name":         "x",
		"Empty":               "   ",
	}
	got := SanitizeHeaders(in)
	want := map[string]string{
		"User-Agent":      "Dalvik/2.1.0",
		"Referer":         "https://localhost/",
		"Accept":          "image/webp",
		"Accept-Language": "zh-CN",
		"Origin":          "https://localhost",
	}
	if len(got) != len(want) {
		t.Fatalf("filtered headers = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("filtered headers = %v, want %v", got, want)
		}
	}

	// A token must never replay blocked headers, even if they were signed by
	// an older implementation.
	s := NewSigner("master")
	now := time.Now()
	p := Payload{URL: "https://x/a", Headers: map[string]string{"Cookie": "sid=1", "Referer": "https://r/"}}
	body := signRaw(t, s, p, now)
	verified, err := s.Verify(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(verified.Headers) != 1 || verified.Headers["Referer"] != "https://r/" {
		t.Fatalf("verified headers = %v", verified.Headers)
	}
}

// signRaw signs a payload without passing it through SignPayload's sanitizer,
// emulating a token produced by an older gateway.
func signRaw(t *testing.T, s *Signer, p Payload, now time.Time) string {
	t.Helper()
	p.Exp = now.Add(time.Hour).Unix()
	payload, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(s.sign([]byte(body)))
}
