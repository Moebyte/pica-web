package imgproxy

import (
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
