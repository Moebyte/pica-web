package outbound

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func publicLookup(host string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("93.184.216.34")}, nil
}

func TestHostAllowed(t *testing.T) {
	patterns := []string{"example.com", "*.ok.example"}
	tests := []struct {
		host string
		want bool
	}{
		{"example.com", true},
		{"EXAMPLE.COM", true},
		{"sub.example.com", false},
		{"ok.example", false},
		{"a.ok.example", true},
		{"deep.a.ok.example", true},
		{"badok.example", false},
		{"other.com", false},
	}
	for _, tt := range tests {
		if got := HostAllowed(patterns, tt.host); got != tt.want {
			t.Errorf("HostAllowed(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestBlockedIPRanges(t *testing.T) {
	blocked := []string{
		"0.1.2.3", "192.168.1.10", "100.64.0.1", "127.0.0.1", "169.254.169.254",
		"172.16.0.1", "192.168.1.1", "198.18.0.1", "224.0.0.1", "240.0.0.1",
		"::", "::1", "fc00::1", "fe80::1", "ff02::1", "::ffff:127.0.0.1",
	}
	for _, s := range blocked {
		if ip := net.ParseIP(s); ip == nil || !BlockedIP(ip) {
			t.Errorf("BlockedIP(%s) = false, want true", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111"}
	for _, s := range allowed {
		if ip := net.ParseIP(s); ip == nil || BlockedIP(ip) {
			t.Errorf("BlockedIP(%s) = true, want false", s)
		}
	}
}

func TestValidateURLRejections(t *testing.T) {
	p := New(nil, true, nil, nil)
	p.LookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
		switch host {
		case "private.example.com":
			return []net.IP{net.ParseIP("192.168.1.10")}, nil
		case "v6.example.com":
			return []net.IP{net.ParseIP("::1")}, nil
		case "mapped.example.com":
			return []net.IP{net.ParseIP("::ffff:192.168.0.1")}, nil
		default:
			return publicLookup(host)
		}
	}

	tests := []struct {
		url      string
		wantCode string
	}{
		{"ftp://example.com/x", "scheme_not_allowed"},
		{"http://user:pass@example.com/x", "userinfo_not_allowed"},
		{"http://1.2.3.4/x", "ip_literal_not_allowed"},
		{"http://[::ffff:127.0.0.1]/x", "ip_literal_not_allowed"},
		{"http://localhost/x", "private_host_blocked"},
		{"http://foo.local/x", "private_host_blocked"},
		{"http://svc.internal/x", "private_host_blocked"},
		{"http://metadata.google.internal/x", "metadata_host_blocked"},
		{"http://private.example.com/x", "private_ip_blocked"},
		{"http://v6.example.com/x", "private_ip_blocked"},
		{"http://mapped.example.com/x", "private_ip_blocked"},
	}
	for _, tt := range tests {
		_, _, err := p.ValidateURL(context.Background(), tt.url)
		var pErr *Error
		if !errors.As(err, &pErr) {
			t.Errorf("%s: err = %v, want policy error", tt.url, err)
			continue
		}
		if pErr.Code != tt.wantCode {
			t.Errorf("%s: code = %q, want %q", tt.url, pErr.Code, tt.wantCode)
		}
	}
}

func TestValidateURLAllowList(t *testing.T) {
	p := New([]string{"example.com", "*.cdn.example"}, false, nil, nil)
	p.LookupIP = func(ctx context.Context, host string) ([]net.IP, error) { return publicLookup(host) }

	if _, _, err := p.ValidateURL(context.Background(), "https://example.com/a"); err != nil {
		t.Fatalf("exact host should be allowed: %v", err)
	}
	if _, _, err := p.ValidateURL(context.Background(), "https://sub.example.com/a"); err == nil {
		t.Fatal("subdomain of exact pattern should be rejected")
	}
	if _, _, err := p.ValidateURL(context.Background(), "https://img.cdn.example/a"); err != nil {
		t.Fatalf("wildcard subdomain should be allowed: %v", err)
	}
	if _, _, err := p.ValidateURL(context.Background(), "https://cdn.example/a"); err == nil {
		t.Fatal("wildcard apex should be rejected")
	}
	if _, _, err := p.ValidateURL(context.Background(), "https://other.com/a"); err == nil {
		t.Fatal("unlisted host should be rejected")
	}
}

func TestDialBindsValidatedIP(t *testing.T) {
	var dialed string
	p := New(nil, true, nil, nil)
	p.LookupIP = func(ctx context.Context, host string) ([]net.IP, error) { return publicLookup(host) }
	p.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = addr
		return nil, errors.New("stop after capture")
	}
	if _, err := p.DialContext(context.Background(), "tcp", "example.com:443"); err == nil {
		t.Fatal("expected dial error from test hook")
	}
	if dialed != "93.184.216.34:443" {
		t.Fatalf("dialed %q, want validated IP 93.184.216.34:443", dialed)
	}
}

func TestDNSRebindingRejectedAtDial(t *testing.T) {
	current := net.ParseIP("93.184.216.34")
	p := New(nil, true, nil, nil)
	p.LookupIP = func(ctx context.Context, host string) ([]net.IP, error) { return []net.IP{current}, nil }

	// Validation happens with the public answer...
	if _, _, err := p.ValidateURL(context.Background(), "https://rebind.example.com/x"); err != nil {
		t.Fatalf("initial validation: %v", err)
	}
	// ...then DNS flips to a private address before dialing. DialContext must
	// re-resolve and reject rather than reusing the stale answer.
	current = net.ParseIP("192.168.1.10")
	called := false
	p.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		called = true
		return nil, errors.New("should not dial")
	}
	_, err := p.DialContext(context.Background(), "tcp", "rebind.example.com:443")
	if err == nil || !strings.Contains(err.Error(), "private_ip_blocked") {
		t.Fatalf("expected private_ip_blocked, got %v", err)
	}
	if called {
		t.Fatal("dialed a rebinding host")
	}
}

func TestHostOverride(t *testing.T) {
	p := New(nil, true, map[string]string{"example.com": "93.184.216.34"}, nil)
	u, ips, err := p.ValidateURL(context.Background(), "https://example.com/x")
	if err != nil {
		t.Fatalf("ValidateURL: %v", err)
	}
	if u.Host != "example.com" || len(ips) != 1 || !ips[0].Equal(net.ParseIP("93.184.216.34")) {
		t.Fatalf("unexpected override result: %v %v", u, ips)
	}

	bad := New(nil, true, map[string]string{"example.com": "192.168.1.10"}, nil)
	if _, _, err := bad.ValidateURL(context.Background(), "https://example.com/x"); err == nil {
		t.Fatal("private host override should be rejected")
	}
}

func TestProxyParsing(t *testing.T) {
	for _, raw := range []string{"", "socks5://127.0.0.1:1080", "socks5://u:p@host:1080", "http://host:3128"} {
		if _, err := NewProxy(raw); err != nil {
			t.Errorf("NewProxy(%q): %v", raw, err)
		}
	}
	for _, raw := range []string{"ftp://host:1", "socks5://host", "http://:3128", "http://host:notaport"} {
		if _, err := NewProxy(raw); err == nil {
			t.Errorf("NewProxy(%q) should fail", raw)
		}
	}
}
