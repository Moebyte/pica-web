// Package vault stores upstream cookies in SQLite, encrypted with AES-GCM, and
// injects them into outbound requests according to RFC6265 matching rules.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"picagateway/internal/secret"
)

// Vault is the cookie store.
type Vault struct {
	db   *sql.DB
	aead cipher.AEAD

	lastPrune atomic.Int64
}

// New opens (or creates) the vault database at dbPath.
func New(dbPath, masterSecret string) (*Vault, error) {
	key := secret.Derive(masterSecret, "cookie-vault")
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("vault cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("vault gcm: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("vault dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open vault db: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0)

	for _, pragma := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("vault pragma: %w", err)
		}
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS cookies (
		tenant TEXT NOT NULL,
		host TEXT NOT NULL,
		name TEXT NOT NULL,
		path TEXT NOT NULL,
		value TEXT NOT NULL,
		expires INTEGER NOT NULL DEFAULT 0,
		secure INTEGER NOT NULL DEFAULT 0,
		http_only INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (tenant, host, name, path)
	)`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create cookies table: %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_cookies_tenant_host ON cookies(tenant, host)"); err != nil {
		db.Close()
		return nil, fmt.Errorf("create cookies index: %w", err)
	}
	// app_store 是给客户端用的加密 KV（当前存账号同步 blob），与 cookies 同库同密钥。
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS app_store (
		tenant TEXT NOT NULL,
		name TEXT NOT NULL,
		value TEXT NOT NULL,
		updated_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (tenant, name)
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create app_store table: %w", err)
	}
	return &Vault{db: db, aead: aead}, nil
}

// Close closes the database.
func (v *Vault) Close() error {
	if v == nil || v.db == nil {
		return nil
	}
	return v.db.Close()
}

func (v *Vault) encrypt(plain string) (string, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := v.aead.Seal(nonce, nonce, []byte(plain), nil)
	return base64.RawStdEncoding.EncodeToString(out), nil
}

func (v *Vault) decrypt(encoded string) (string, error) {
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	ns := v.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	plain, err := v.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// SaveSetCookies parses and stores Set-Cookie lines received from reqURL.
// Already-expired cookies delete the matching row instead of being stored.
func (v *Vault) SaveSetCookies(tenant string, reqURL *url.URL, lines []string, now time.Time) (saved, deleted int, err error) {
	if len(lines) == 0 || reqURL == nil {
		return 0, 0, nil
	}
	tx, err := v.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for _, line := range lines {
		c, maxAge, hasMaxAge := parseSetCookieLine(line)
		if c == nil {
			continue
		}
		host, path, expires, expired, ok := cookieTarget(c, maxAge, hasMaxAge, reqURL, now)
		if !ok {
			continue
		}
		if expired {
			res, execErr := tx.Exec(
				"DELETE FROM cookies WHERE tenant = ? AND host = ? AND name = ? AND path = ?",
				tenant, host, c.Name, path,
			)
			if execErr != nil {
				return saved, deleted, execErr
			}
			if n, _ := res.RowsAffected(); n > 0 {
				deleted++
			}
			continue
		}
		enc, encErr := v.encrypt(c.Value)
		if encErr != nil {
			return saved, deleted, encErr
		}
		_, execErr := tx.Exec(`INSERT INTO cookies (tenant, host, name, path, value, expires, secure, http_only, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (tenant, host, name, path) DO UPDATE SET
				value = excluded.value,
				expires = excluded.expires,
				secure = excluded.secure,
				http_only = excluded.http_only,
				updated_at = excluded.updated_at`,
			tenant, host, c.Name, path, enc, expires, boolInt(c.Secure), boolInt(c.HttpOnly), now.Unix(),
		)
		if execErr != nil {
			return saved, deleted, execErr
		}
		saved++
	}
	if err = tx.Commit(); err != nil {
		return saved, deleted, err
	}
	return saved, deleted, nil
}

// CookieHeader returns the Cookie header value for u, or "".
func (v *Vault) CookieHeader(tenant string, u *url.URL, now time.Time) (string, error) {
	if u == nil || u.Hostname() == "" {
		return "", nil
	}
	v.prune(now)

	rows, err := v.db.Query(
		"SELECT host, name, path, value, expires, secure, http_only FROM cookies WHERE tenant = ?",
		tenant,
	)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	host := normalizeHost(u.Hostname())
	reqPath := u.Path
	if reqPath == "" {
		reqPath = "/"
	}
	secure := strings.EqualFold(u.Scheme, "https")

	var matched []Cookie
	for rows.Next() {
		var c Cookie
		var enc string
		var sec, httpOnly int
		if err := rows.Scan(&c.Host, &c.Name, &c.Path, &enc, &c.Expires, &sec, &httpOnly); err != nil {
			return "", err
		}
		if c.Expires > 0 && c.Expires <= now.Unix() {
			continue
		}
		if !domainMatch(c.Host, host) || !pathMatch(c.Path, reqPath) {
			continue
		}
		if sec != 0 && !secure {
			continue
		}
		plain, err := v.decrypt(enc)
		if err != nil {
			// A single corrupt row must not break all outbound requests.
			continue
		}
		if !validCookieName(c.Name) || !validCookieValue(plain) {
			continue
		}
		c.Value = plain
		c.Secure = sec != 0
		c.HTTPOnly = httpOnly != 0
		matched = append(matched, c)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return buildCookieHeader(matched), nil
}

func (v *Vault) prune(now time.Time) {
	last := v.lastPrune.Load()
	if now.Unix()-last < 60 {
		return
	}
	if !v.lastPrune.CompareAndSwap(last, now.Unix()) {
		return
	}
	_, _ = v.db.Exec("DELETE FROM cookies WHERE expires > 0 AND expires <= ?", now.Unix())
}

// List returns redacted cookies matching host (host-only cookies for host and
// domain cookies that apply to it).
func (v *Vault) List(tenant, host string) ([]View, error) {
	host = normalizeHost(host)
	rows, err := v.db.Query(
		"SELECT host, name, path, value, expires, secure, http_only FROM cookies WHERE tenant = ? ORDER BY host, path, name",
		tenant,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []View{}
	for rows.Next() {
		var c Cookie
		var enc string
		var sec, httpOnly int
		if err := rows.Scan(&c.Host, &c.Name, &c.Path, &enc, &c.Expires, &sec, &httpOnly); err != nil {
			return nil, err
		}
		if host != "" && !domainMatch(c.Host, host) {
			continue
		}
		plain, err := v.decrypt(enc)
		if err != nil {
			plain = ""
		}
		out = append(out, View{
			Host:     c.Host,
			Name:     c.Name,
			Path:     c.Path,
			Expires:  c.Expires,
			Secure:   sec != 0,
			HTTPOnly: httpOnly != 0,
			Masked:   maskValue(plain),
		})
	}
	return out, rows.Err()
}

// DeleteHost removes all cookies that apply to host.
func (v *Vault) DeleteHost(tenant, host string) (int64, error) {
	host = normalizeHost(host)
	if host == "" {
		return 0, nil
	}
	rows, err := v.db.Query("SELECT host, name, path FROM cookies WHERE tenant = ?", tenant)
	if err != nil {
		return 0, err
	}
	type key struct{ host, name, path string }
	var keys []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.host, &k.name, &k.path); err != nil {
			rows.Close()
			return 0, err
		}
		if domainMatch(k.host, host) {
			keys = append(keys, k)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	var deleted int64
	tx, err := v.db.Begin()
	if err != nil {
		return 0, err
	}
	for _, k := range keys {
		res, execErr := tx.Exec(
			"DELETE FROM cookies WHERE tenant = ? AND host = ? AND name = ? AND path = ?",
			tenant, k.host, k.name, k.path,
		)
		if execErr != nil {
			_ = tx.Rollback()
			return deleted, execErr
		}
		if n, _ := res.RowsAffected(); n > 0 {
			deleted++
		}
	}
	if err := tx.Commit(); err != nil {
		return deleted, err
	}
	return deleted, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
