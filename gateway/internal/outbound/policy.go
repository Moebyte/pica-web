// Package outbound implements SSRF-safe outbound HTTP: URL policy, validated
// dialing, optional SOCKS5/HTTP CONNECT proxies and the shared transport.
package outbound

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	blockedIPv4 = mustCIDRs(
		"0.0.0.0/8",
		"192.168.1.10/8",
		"100.64.0.0/10",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"198.18.0.0/15",
		"224.0.0.0/4",
		"240.0.0.0/4",
	)
	blockedIPv6 = mustCIDRs(
		"::/128",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
		"ff00::/8",
	)
)

func mustCIDRs(specs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(specs))
	for _, s := range specs {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			panic(fmt.Sprintf("outbound: bad CIDR %q: %v", s, err))
		}
		nets = append(nets, n)
	}
	return nets
}

// Error is a policy failure carrying an HTTP status for the handler.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

func blocked(code, format string, args ...any) *Error {
	return &Error{Status: 403, Code: code, Message: fmt.Sprintf(format, args...)}
}

func badRequest(code, format string, args ...any) *Error {
	return &Error{Status: 400, Code: code, Message: fmt.Sprintf(format, args...)}
}

func badGateway(code, format string, args ...any) *Error {
	return &Error{Status: 502, Code: code, Message: fmt.Sprintf(format, args...)}
}

// LookupFunc resolves a hostname. It is injectable for tests.
type LookupFunc func(ctx context.Context, host string) ([]net.IP, error)

// DialFunc dials an already-validated IP:port. It is injectable for tests.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// Policy validates targets and dials only the IP addresses it validated.
type Policy struct {
	AllowHosts    []string
	AllowAll      bool
	HostOverrides map[string]string
	Proxy         *Proxy

	// AllowLoopback is a test-only escape hatch for httptest servers. It is
	// deliberately not wired to any configuration field.
	AllowLoopback bool

	// LookupIP overrides DNS resolution (tests).
	LookupIP LookupFunc
	// Dial overrides the final TCP dial (tests).
	Dial DialFunc
}

// New builds a policy.
func New(allowHosts []string, allowAll bool, overrides map[string]string, proxy *Proxy) *Policy {
	normalized := make([]string, 0, len(allowHosts))
	for _, h := range allowHosts {
		h = normalizeHost(h)
		if h != "" {
			normalized = append(normalized, h)
		}
	}
	normalizedOverrides := make(map[string]string, len(overrides))
	for host, ip := range overrides {
		normalizedOverrides[normalizeHost(host)] = ip
	}
	return &Policy{
		AllowHosts:    normalized,
		AllowAll:      allowAll,
		HostOverrides: normalizedOverrides,
		Proxy:         proxy,
	}
}

// ValidateURL checks scheme, userinfo, host literals, host allowlist and every
// resolved IP. The returned URL has a normalized hostname.
func (p *Policy) ValidateURL(ctx context.Context, raw string) (*url.URL, []net.IP, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil, badRequest("missing_url", "target URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, nil, badRequest("invalid_url", "cannot parse target URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, nil, blocked("scheme_not_allowed", "only http and https are allowed")
	}
	if u.User != nil {
		return nil, nil, blocked("userinfo_not_allowed", "target URL must not contain userinfo")
	}
	host := normalizeHost(u.Hostname())
	if host == "" {
		return nil, nil, badRequest("missing_host", "target URL has no host")
	}

	// Normalize the URL host (drop trailing dot / uppercase) while keeping the
	// original port. TLS SNI will use this host.
	if u.Hostname() != host {
		if port := u.Port(); port != "" {
			u.Host = net.JoinHostPort(host, port)
		} else {
			u.Host = host
		}
	}

	if ip := net.ParseIP(host); ip != nil {
		if !p.loopbackAllowed(ip) {
			return nil, nil, blocked("ip_literal_not_allowed", "IP literal targets are not allowed")
		}
		return u, []net.IP{ip}, nil
	}

	if err := p.checkHostName(host); err != nil {
		return nil, nil, err
	}
	if !p.AllowAll && !HostAllowed(p.AllowHosts, host) {
		return nil, nil, blocked("host_not_allowed", "host %q is not in allowHosts", host)
	}

	ips, err := p.resolve(ctx, host)
	if err != nil {
		return nil, nil, err
	}
	return u, ips, nil
}

func (p *Policy) checkHostName(host string) error {
	if host == "metadata" || host == "metadata.goog" || host == "metadata.google.internal" ||
		strings.HasSuffix(host, ".metadata.google.internal") {
		return blocked("metadata_host_blocked", "metadata hostnames are not allowed")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		if p.AllowLoopback {
			return nil
		}
		return blocked("private_host_blocked", "localhost targets are not allowed")
	}
	if host == "local" || strings.HasSuffix(host, ".local") ||
		host == "internal" || strings.HasSuffix(host, ".internal") {
		return blocked("private_host_blocked", "internal hostnames are not allowed")
	}
	return nil
}

func (p *Policy) resolve(ctx context.Context, host string) ([]net.IP, error) {
	host = normalizeHost(host)
	if ipStr, ok := p.HostOverrides[host]; ok {
		ip := net.ParseIP(strings.TrimSpace(ipStr))
		if ip == nil {
			return nil, badGateway("bad_host_override", "hostOverrides[%s] is not a valid IP", host)
		}
		ips := []net.IP{ip}
		if err := p.checkIPs(ips); err != nil {
			return nil, err
		}
		return ips, nil
	}

	lookup := p.LookupIP
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]net.IP, error) {
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			ips := make([]net.IP, 0, len(addrs))
			for _, a := range addrs {
				ips = append(ips, a.IP)
			}
			return ips, nil
		}
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, badGateway("dns_error", "cannot resolve %s: %v", host, err)
	}
	if len(ips) == 0 {
		return nil, badGateway("dns_error", "cannot resolve %s: no addresses", host)
	}
	if err := p.checkIPs(ips); err != nil {
		return nil, err
	}
	return ips, nil
}

func (p *Policy) checkIPs(ips []net.IP) error {
	for _, ip := range ips {
		if ip == nil {
			return blocked("invalid_address", "resolver returned an invalid address")
		}
		if BlockedIP(ip) && !p.loopbackAllowed(ip) {
			return blocked("private_ip_blocked", "resolved address %s is not allowed", ip)
		}
	}
	return nil
}

func (p *Policy) loopbackAllowed(ip net.IP) bool {
	return p.AllowLoopback && ip.IsLoopback()
}

// DialContext implements the http.Transport DialContext. It validates the
// host again, then dials only the exact IPs that were validated. This prevents
// DNS rebinding between validation and connection.
func (p *Policy) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid dial address %q: %w", addr, err)
	}
	host = normalizeHost(host)

	if ip := net.ParseIP(host); ip != nil {
		if !p.loopbackAllowed(ip) {
			return nil, blocked("ip_literal_not_allowed", "IP literal targets are not allowed")
		}
		return p.dialOne(ctx, network, net.JoinHostPort(ip.String(), port))
	}

	if err := p.checkHostName(host); err != nil {
		return nil, err
	}
	if !p.AllowAll && !HostAllowed(p.AllowHosts, host) {
		return nil, blocked("host_not_allowed", "host %q is not in allowHosts", host)
	}

	ips, err := p.resolve(ctx, host)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, ip := range ips {
		if network == "tcp4" && ip.To4() == nil {
			continue
		}
		if network == "tcp6" && ip.To4() != nil {
			continue
		}
		conn, err := p.dialOne(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		return nil, fmt.Errorf("no usable address for %s (network %s)", host, network)
	}
	return nil, fmt.Errorf("dial %s: %w", host, lastErr)
}

func (p *Policy) dialOne(ctx context.Context, network, addr string) (net.Conn, error) {
	if p.Proxy != nil {
		return p.Proxy.DialContext(ctx, network, addr)
	}
	dial := p.Dial
	if dial == nil {
		d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		dial = d.DialContext
	}
	return dial(ctx, network, addr)
}

// HostAllowed reports whether host matches one of the patterns. Patterns are
// exact hostnames or "*.example.com" (subdomains only, not the apex).
func HostAllowed(patterns []string, host string) bool {
	host = normalizeHost(host)
	for _, pat := range patterns {
		pat = normalizeHost(pat)
		if pat == "" {
			continue
		}
		if strings.HasPrefix(pat, "*.") {
			suffix := pat[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
			continue
		}
		if host == pat {
			return true
		}
	}
	return false
}

// BlockedIP reports whether ip falls into one of the prohibited ranges.
func BlockedIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		for _, n := range blockedIPv4 {
			if n.Contains(ip4) {
				return true
			}
		}
		return false
	}
	ip16 := ip.To16()
	if ip16 == nil {
		return true
	}
	// IPv4-mapped addresses are handled by To4 above, but mask just in case.
	for _, n := range blockedIPv6 {
		if n.Contains(ip16) {
			return true
		}
	}
	return false
}

func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimSuffix(h, ".")
	return h
}

// NewTransport builds the shared outbound transport. Dialing always goes
// through the policy, TLS still verifies certificates against the URL host
// (SNI is derived by net/http from the request URL, not from the dial address).
func NewTransport(p *Policy, timeout time.Duration) *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           p.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: time.Second,
	}
}

// ErrTooManyRedirects is returned by the server's redirect policy.
var ErrTooManyRedirects = errors.New("too many redirects")
