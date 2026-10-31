package secret

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesAndReusesSecretFile(t *testing.T) {
	dir := t.TempDir()
	first, err := Load("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(first) < 32 {
		t.Fatalf("generated secret too short: %q", first)
	}
	info, err := os.Stat(filepath.Join(dir, ".secret"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	second, err := Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("secret was not persisted")
	}
}

func TestConfiguredSecretWins(t *testing.T) {
	s, err := Load("configured", t.TempDir())
	if err != nil || s != "configured" {
		t.Fatalf("Load = %q, %v", s, err)
	}
}

func TestDeriveIsSeparated(t *testing.T) {
	a := Derive("master", "vault")
	b := Derive("master", "session")
	c := Derive("master", "vault")
	if len(a) != 32 {
		t.Fatalf("key length = %d", len(a))
	}
	if string(a) == string(b) {
		t.Fatal("different labels produced the same key")
	}
	if string(a) != string(c) {
		t.Fatal("derivation is not deterministic")
	}
}
