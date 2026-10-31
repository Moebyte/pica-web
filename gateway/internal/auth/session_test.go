package auth

import (
	"testing"
	"time"
)

func TestSessionRoundTrip(t *testing.T) {
	m := NewManager("master", 24*time.Hour)
	now := time.Now()
	token, exp := m.Issue(now)
	if token == "" || !exp.After(now) {
		t.Fatalf("token=%q exp=%v", token, exp)
	}
	gotExp, ok := m.Validate(token, now)
	if !ok || !gotExp.Equal(exp.Truncate(time.Second)) {
		t.Fatalf("Validate = %v %v", gotExp, ok)
	}
	if _, ok := m.Validate(token, exp.Add(time.Second)); ok {
		t.Fatal("expired token accepted")
	}
	if _, ok := m.Validate(token+"x", now); ok {
		t.Fatal("tampered token accepted")
	}
	if _, ok := NewManager("other", time.Hour).Validate(token, now); ok {
		t.Fatal("token accepted with a different secret")
	}
}

func TestCheckPassword(t *testing.T) {
	if !CheckPassword("abc", "abc") {
		t.Fatal("correct password rejected")
	}
	if CheckPassword("abc", "abd") || CheckPassword("abc", "") {
		t.Fatal("wrong password accepted")
	}
}
