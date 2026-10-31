package server

import (
	"net/http"
	"testing"
)

func TestHopByHopStripping(t *testing.T) {
	src := http.Header{}
	src.Set("Connection", "X-Hop, keep-alive")
	src.Set("X-Hop", "secret")
	src.Set("Keep-Alive", "timeout=5")
	src.Set("Proxy-Authorization", "Basic abc")
	src.Set("Proxy-Connection", "keep-alive")
	src.Set("Te", "trailers")
	src.Set("Trailer", "X-Trailer")
	src.Set("Transfer-Encoding", "chunked")
	src.Set("Upgrade", "h2c")
	src.Set("Set-Cookie", "sid=1")
	src.Set("X-Pica-Meta", "upstream")
	src.Set("Content-Type", "application/json")
	src.Set("Location", "/next")
	src.Set("Permissions-Policy", "run-ad-auction=(), join-ad-interest-group=()")
	src.Set("Clear-Site-Data", `"storage"`)
	src.Set("Content-Security-Policy", "default-src 'none'")
	src.Set("Report-To", `{"group":"x"}`)
	src.Set("Strict-Transport-Security", "max-age=1")

	filtered := sanitizeResponseHeaders(src)
	for _, key := range []string{
		"Connection", "X-Hop", "Keep-Alive", "Proxy-Authorization", "Proxy-Connection",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Set-Cookie", "X-Pica-Meta",
		"Permissions-Policy", "Clear-Site-Data", "Content-Security-Policy",
		"Report-To", "Strict-Transport-Security",
	} {
		if _, ok := filtered[key]; ok {
			t.Errorf("sanitizeResponseHeaders kept %s", key)
		}
	}
	if filtered.Get("Content-Type") != "application/json" || filtered.Get("Location") != "/next" {
		t.Fatalf("end-to-end headers lost: %v", filtered)
	}
	if _, ok := src["Set-Cookie"]; !ok {
		t.Fatal("sanitize mutated the source header")
	}
}

func TestTunnelRequestHeaderFiltering(t *testing.T) {
	src := http.Header{}
	src.Set("Connection", "X-Hop")
	src.Set("X-Hop", "secret")
	src.Set("Host", "gateway.local")
	src.Set("Cookie", "pica_session=leak")
	src.Set("User-Agent", "browser-ua")
	src.Set("Accept-Encoding", "br")
	src.Set("Content-Length", "10")
	src.Set("Origin", "https://gateway.local")
	src.Set("Referer", "https://gateway.local/")
	src.Set("X-Target-URL", "https://example.com")
	src.Set("X-Target-Method", "POST")
	src.Set("X-Pica-Follow", "1")
	src.Set("Proxy-Authorization", "Basic abc")
	src.Set("Accept", "application/json")
	src.Set("X-Custom", "kept")

	dst := http.Header{}
	copyTunnelRequestHeaders(dst, src)

	for _, key := range []string{
		"Connection", "X-Hop", "Host", "Cookie", "Content-Length", "Origin", "Referer",
		"User-Agent", "Accept-Encoding",
		"X-Target-Url", "X-Target-Method", "X-Pica-Follow", "Proxy-Authorization",
	} {
		if _, ok := dst[key]; ok {
			t.Errorf("copyTunnelRequestHeaders kept %s", key)
		}
	}
	if dst.Get("Accept") != "application/json" || dst.Get("X-Custom") != "kept" {
		t.Fatalf("expected headers missing: %v", dst)
	}
}
