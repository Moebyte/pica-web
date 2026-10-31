package vault

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func newTestVault(t *testing.T) *Vault {
	t.Helper()
	v, err := New(t.TempDir()+"/vault.db", "unit-test-secret")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return v
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}

func TestCookieTargetParsing(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reqURL := mustURL(t, "https://www.example.com/a/b/c?q=1")

	tests := []struct {
		name        string
		line        string
		wantHost    string
		wantPath    string
		wantExpires int64
		wantExpired bool
		wantOK      bool
	}{
		{
			name:        "domain maxage",
			line:        "sid=abc; Path=/; Domain=.example.com; Max-Age=3600; Secure; HttpOnly",
			wantHost:    ".example.com",
			wantPath:    "/",
			wantExpires: now.Add(time.Hour).Unix(),
			wantOK:      true,
		},
		{
			name:        "host only default path",
			line:        "tok=xyz",
			wantHost:    "www.example.com",
			wantPath:    "/a/b",
			wantExpires: 0,
			wantOK:      true,
		},
		{
			name:        "max age zero deletes",
			line:        "sid=; Max-Age=0",
			wantHost:    "www.example.com",
			wantPath:    "/a/b",
			wantExpired: true,
			wantOK:      true,
		},
		{
			name:        "expires in past deletes",
			line:        "sid=old; Expires=Wed, 21 Oct 2015 07:28:00 GMT",
			wantHost:    "www.example.com",
			wantPath:    "/a/b",
			wantExpired: true,
			wantOK:      true,
		},
		{
			name:   "foreign domain rejected",
			line:   "sid=bad; Domain=evil.example",
			wantOK: false,
		},
		{
			name:   "tld-only domain rejected",
			line:   "sid=bad; Domain=com",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, maxAge, hasMaxAge := parseSetCookieLine(tt.line)
			if c == nil {
				t.Fatalf("cookie did not parse")
			}
			host, path, expires, expired, ok := cookieTarget(c, maxAge, hasMaxAge, reqURL, now)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if host != tt.wantHost || path != tt.wantPath || expires != tt.wantExpires || expired != tt.wantExpired {
				t.Fatalf("got host=%q path=%q expires=%d expired=%v, want host=%q path=%q expires=%d expired=%v",
					host, path, expires, expired, tt.wantHost, tt.wantPath, tt.wantExpires, tt.wantExpired)
			}
		})
	}
}

func TestDomainAndPathMatch(t *testing.T) {
	domainTests := []struct {
		cookieHost, reqHost string
		want                bool
	}{
		{".example.com", "example.com", true},
		{".example.com", "sub.example.com", true},
		{".example.com", "deep.sub.example.com", true},
		{".example.com", "badexample.com", false},
		{"example.com", "example.com", true},
		{"example.com", "sub.example.com", false},
		{"www.example.com", "www.example.com", true},
		{"www.example.com", "example.com", false},
	}
	for _, tt := range domainTests {
		if got := domainMatch(tt.cookieHost, tt.reqHost); got != tt.want {
			t.Errorf("domainMatch(%q, %q) = %v, want %v", tt.cookieHost, tt.reqHost, got, tt.want)
		}
	}

	pathTests := []struct {
		cookiePath, reqPath string
		want                bool
	}{
		{"/", "/", true},
		{"/", "/anything", true},
		{"/a", "/a", true},
		{"/a", "/a/b", true},
		{"/a/", "/a/b", true},
		{"/a", "/ab", false},
		{"/a/b", "/a", false},
	}
	for _, tt := range pathTests {
		if got := pathMatch(tt.cookiePath, tt.reqPath); got != tt.want {
			t.Errorf("pathMatch(%q, %q) = %v, want %v", tt.cookiePath, tt.reqPath, got, tt.want)
		}
	}
}

func TestSaveAndInjectCookies(t *testing.T) {
	v := newTestVault(t)
	now := time.Now()
	reqURL := mustURL(t, "https://www.example.com/login")
	lines := []string{
		"sid=secretvalue; Path=/; Domain=.example.com; Max-Age=3600; HttpOnly",
		"tok=xyz; Path=/api",
		"sec=1; Path=/; Secure",
	}
	saved, deleted, err := v.SaveSetCookies("default", reqURL, lines, now)
	if err != nil {
		t.Fatalf("SaveSetCookies: %v", err)
	}
	if saved != 3 || deleted != 0 {
		t.Fatalf("saved=%d deleted=%d, want 3/0", saved, deleted)
	}

	header, err := v.CookieHeader("default", mustURL(t, "https://www.example.com/api/x"), now)
	if err != nil {
		t.Fatalf("CookieHeader: %v", err)
	}
	for _, want := range []string{"tok=xyz", "sec=1", "sid=secretvalue"} {
		if !strings.Contains(header, want) {
			t.Errorf("header %q missing %q", header, want)
		}
	}
	if !strings.HasPrefix(header, "tok=xyz") {
		t.Errorf("longer path should sort first, got %q", header)
	}

	// Host-only www cookie and Secure cookie must not be sent over http.
	header, err = v.CookieHeader("default", mustURL(t, "http://sub.example.com/api/x"), now)
	if err != nil {
		t.Fatalf("CookieHeader: %v", err)
	}
	if strings.Contains(header, "tok=") || strings.Contains(header, "sec=") {
		t.Errorf("unexpected cookies for subdomain/http: %q", header)
	}
	if !strings.Contains(header, "sid=secretvalue") {
		t.Errorf("domain cookie missing: %q", header)
	}
}

func TestExpiryAndDeletion(t *testing.T) {
	v := newTestVault(t)
	now := time.Now()
	reqURL := mustURL(t, "https://example.com/")

	if _, _, err := v.SaveSetCookies("default", reqURL, []string{"a=1; Max-Age=1"}, now); err != nil {
		t.Fatal(err)
	}
	header, err := v.CookieHeader("default", reqURL, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if header != "" {
		t.Fatalf("expired cookie was sent: %q", header)
	}

	if _, _, err := v.SaveSetCookies("default", reqURL, []string{"b=2; Max-Age=3600"}, now); err != nil {
		t.Fatal(err)
	}
	if _, deleted, err := v.SaveSetCookies("default", reqURL, []string{"b=gone; Max-Age=0"}, now); err != nil || deleted != 1 {
		t.Fatalf("delete: deleted=%d err=%v", deleted, err)
	}
	header, err = v.CookieHeader("default", reqURL, now)
	if err != nil {
		t.Fatal(err)
	}
	if header != "" {
		t.Fatalf("deleted cookie still sent: %q", header)
	}
}

func TestCookiesEncryptedAtRest(t *testing.T) {
	v := newTestVault(t)
	now := time.Now()
	reqURL := mustURL(t, "https://example.com/")
	if _, _, err := v.SaveSetCookies("default", reqURL, []string{"secret=plaintext-visible?"}, now); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := v.db.QueryRow("SELECT value FROM cookies WHERE name = 'secret'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "plaintext") {
		t.Fatalf("cookie value stored in plaintext: %q", stored)
	}
	plain, err := v.decrypt(stored)
	if err != nil || plain != "plaintext-visible?" {
		t.Fatalf("decrypt = %q, %v", plain, err)
	}
}

func TestDeleteHost(t *testing.T) {
	v := newTestVault(t)
	now := time.Now()
	if _, _, err := v.SaveSetCookies("default", mustURL(t, "https://www.example.com/"), []string{
		"a=1; Domain=example.com",
		"b=2",
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := v.SaveSetCookies("default", mustURL(t, "https://other.test/"), []string{"c=3"}, now); err != nil {
		t.Fatal(err)
	}
	n, err := v.DeleteHost("default", "www.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("deleted %d, want 2", n)
	}
	views, err := v.List("default", "www.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 0 {
		t.Fatalf("cookies left after delete: %+v", views)
	}
}
