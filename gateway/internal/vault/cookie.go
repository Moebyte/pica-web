package vault

import (
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Cookie is a stored cookie row (value is plaintext in memory only).
type Cookie struct {
	Tenant    string
	Host      string
	Name      string
	Path      string
	Value     string
	Expires   int64 // unix seconds, 0 = session cookie (kept server-side indefinitely)
	Secure    bool
	HTTPOnly  bool
	UpdatedAt int64
}

// View is the redacted representation returned by the API.
type View struct {
	Host     string `json:"host"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Expires  int64  `json:"expires"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"httpOnly"`
	Masked   string `json:"masked"`
}

func parseSetCookieLine(line string) (*http.Cookie, int, bool) {
	resp := http.Response{Header: http.Header{"Set-Cookie": []string{line}}}
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		return nil, 0, false
	}
	c := cookies[0]
	if c.Name == "" || !validCookieName(c.Name) || !validCookieValue(c.Value) {
		return nil, 0, false
	}
	maxAge, hasMaxAge := rawMaxAge(line)
	return c, maxAge, hasMaxAge
}

// rawMaxAge returns the literal Max-Age attribute. net/http maps Max-Age=0 to
// Cookie.MaxAge == 0, which is indistinguishable from an absent attribute.
func rawMaxAge(line string) (int, bool) {
	parts := strings.Split(line, ";")
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		if len(part) < 8 || !strings.EqualFold(part[:7], "max-age") {
			continue
		}
		if part[7] != '=' {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(part[8:]))
		if err != nil {
			continue
		}
		return n, true
	}
	return 0, false
}

// cookieTarget derives the storage host, path and expiration for a Set-Cookie
// received from reqURL. ok is false when the cookie must not be stored.
func cookieTarget(c *http.Cookie, maxAge int, hasMaxAge bool, reqURL *url.URL, now time.Time) (host, path string, expires int64, expired, ok bool) {
	reqHost := normalizeHost(reqURL.Hostname())
	if reqHost == "" {
		return "", "", 0, false, false
	}

	path = c.Path
	if path == "" || !strings.HasPrefix(path, "/") {
		path = defaultPath(reqURL.Path)
	}

	domain := normalizeHost(strings.TrimPrefix(strings.TrimSpace(c.Domain), "."))
	if domain != "" {
		if !domainValid(domain, reqHost) {
			return "", "", 0, false, false
		}
		host = "." + domain
	} else {
		host = reqHost
	}

	switch {
	case hasMaxAge && maxAge <= 0:
		return host, path, 0, true, true
	case hasMaxAge:
		return host, path, now.Add(time.Duration(maxAge) * time.Second).Unix(), false, true
	case !c.Expires.IsZero():
		if !c.Expires.After(now) {
			return host, path, 0, true, true
		}
		return host, path, c.Expires.Unix(), false, true
	default:
		// Session cookie: keep it server-side with no expiry.
		return host, path, 0, false, true
	}
}

func defaultPath(reqPath string) string {
	if reqPath == "" || !strings.HasPrefix(reqPath, "/") {
		return "/"
	}
	if strings.Count(reqPath, "/") <= 1 {
		return "/"
	}
	idx := strings.LastIndex(reqPath, "/")
	if idx <= 0 {
		return "/"
	}
	return reqPath[:idx]
}

// domainValid implements the RFC6265 domain-attribute acceptance check.
func domainValid(domain, reqHost string) bool {
	domain = normalizeHost(domain)
	reqHost = normalizeHost(reqHost)
	if domain == "" || reqHost == "" {
		return false
	}
	if net.ParseIP(domain) != nil || net.ParseIP(reqHost) != nil {
		return false
	}
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	if domain == "localhost" || strings.HasSuffix(domain, ".localhost") ||
		strings.HasSuffix(domain, ".local") || strings.HasSuffix(domain, ".internal") {
		return false
	}
	return reqHost == domain || strings.HasSuffix(reqHost, "."+domain)
}

func domainMatch(cookieHost, reqHost string) bool {
	cookieHost = normalizeHost(cookieHost)
	reqHost = normalizeHost(reqHost)
	if cookieHost == "" || reqHost == "" {
		return false
	}
	if strings.HasPrefix(cookieHost, ".") {
		d := cookieHost[1:]
		return reqHost == d || strings.HasSuffix(reqHost, "."+d)
	}
	return reqHost == cookieHost
}

func pathMatch(cookiePath, reqPath string) bool {
	if cookiePath == "" {
		cookiePath = "/"
	}
	if reqPath == "" {
		reqPath = "/"
	}
	if reqPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(reqPath, cookiePath) {
		return false
	}
	if strings.HasSuffix(cookiePath, "/") {
		return true
	}
	return len(reqPath) > len(cookiePath) && reqPath[len(cookiePath)] == '/'
}

func validCookieName(name string) bool {
	if name == "" {
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

func validCookieValue(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c < 0x20 || c == 0x7f || c == ';' {
			return false
		}
	}
	return true
}

func buildCookieHeader(cookies []Cookie) string {
	sort.Slice(cookies, func(i, j int) bool {
		if len(cookies[i].Path) != len(cookies[j].Path) {
			return len(cookies[i].Path) > len(cookies[j].Path)
		}
		if cookies[i].Name != cookies[j].Name {
			return cookies[i].Name < cookies[j].Name
		}
		return cookies[i].Path < cookies[j].Path
	})
	var b strings.Builder
	for i, c := range cookies {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(c.Name)
		b.WriteByte('=')
		b.WriteString(c.Value)
	}
	return b.String()
}

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

func maskValue(v string) string {
	r := []rune(v)
	if len(r) == 0 {
		return ""
	}
	if len(r) <= 6 {
		return "******"
	}
	return string(r[:2]) + "…" + string(r[len(r)-2:])
}
