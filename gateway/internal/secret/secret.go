// Package secret loads and derives the gateway master secret.
package secret

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Load returns the configured secret, or reads/creates a persistent random
// secret at <dataDir>/.secret with mode 0600.
func Load(configured, dataDir string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return configured, nil
	}

	path := filepath.Join(dataDir, ".secret")
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		s := strings.TrimSpace(string(b))
		if s == "" {
			return "", fmt.Errorf("secret file %s is empty", path)
		}
		if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
			return "", fmt.Errorf("chmod secret file: %w", chmodErr)
		}
		return s, nil
	case errors.Is(err, os.ErrNotExist):
		// Create below.
	default:
		return "", fmt.Errorf("read secret file: %w", err)
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	s := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(s+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write secret file: %w", err)
	}
	return s, nil
}

// Derive produces a 32-byte purpose-specific key from the master secret.
// The label keeps vault, session and image-token keys independent.
func Derive(master, label string) []byte {
	mac := hmac.New(sha256.New, []byte(master))
	mac.Write([]byte(label))
	return mac.Sum(nil)
}
