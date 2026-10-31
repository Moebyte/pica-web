package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

// hopByHopHeaders returns headers that must not be forwarded in either
// direction, including tokens named by the Connection header.
func hopByHopHeaders(h http.Header) map[string]bool {
	out := map[string]bool{
		"Connection":          true,
		"Keep-Alive":          true,
		"Proxy-Authenticate":  true,
		"Proxy-Authorization": true,
		"Te":                  true,
		"Trailer":             true,
		"Transfer-Encoding":   true,
		"Upgrade":             true,
	}
	for _, v := range h.Values("Connection") {
		for _, token := range strings.Split(v, ",") {
			token = strings.TrimSpace(token)
			if token != "" {
				out[http.CanonicalHeaderKey(token)] = true
			}
		}
	}
	return out
}

// documentScopedHeaders are per-document policy headers. Forwarding them onto
// /api/tunnel responses is at best console noise (Chrome warns about features
// like run-ad-auction) and at worst dangerous: an upstream Clear-Site-Data
// would wipe this origin's localStorage/IndexedDB (the app's settings,
// history and favorites). Strip them from tunnel responses.
var documentScopedHeaders = map[string]bool{
	"Permissions-Policy":                  true,
	"Report-To":                           true,
	"NEL":                                 true,
	"Content-Security-Policy":             true,
	"Content-Security-Policy-Report-Only": true,
	"Cross-Origin-Opener-Policy":          true,
	"Cross-Origin-Embedder-Policy":        true,
	"Cross-Origin-Resource-Policy":        true,
	"X-Frame-Options":                     true,
	"X-XSS-Protection":                    true,
	"Clear-Site-Data":                     true,
	"Strict-Transport-Security":           true,
}

// sanitizeResponseHeaders copies h without hop-by-hop, Set-Cookie, Proxy-*,
// document-scoped policy headers or the gateway's own X-Pica-Meta header.
func sanitizeResponseHeaders(h http.Header) http.Header {
	hop := hopByHopHeaders(h)
	out := make(http.Header, len(h))
	for k, vv := range h {
		ck := http.CanonicalHeaderKey(k)
		switch {
		case hop[ck]:
			continue
		case ck == "Set-Cookie":
			continue
		case strings.HasPrefix(ck, "Proxy-"):
			continue
		case documentScopedHeaders[ck]:
			continue
		case strings.EqualFold(ck, "X-Pica-Meta"):
			continue
		}
		out[ck] = append([]string(nil), vv...)
	}
	return out
}

// copyTunnelRequestHeaders forwards end-to-end headers from the tunnel
// request itself. Cookie, User-Agent, Referer and Origin are ignored here:
// browser-forbidden headers must arrive through X-Pica-Req-Headers. Gateway
// control headers, Content-Length, Accept-Encoding (left to the Go transport)
// and the browser's Origin/Referer are also dropped.
func copyTunnelRequestHeaders(dst, src http.Header) {
	hop := hopByHopHeaders(src)
	for k, vv := range src {
		ck := http.CanonicalHeaderKey(k)
		switch {
		case hop[ck]:
			continue
		case ck == "Host", ck == "Cookie", ck == "Content-Length", ck == "User-Agent", ck == "Accept-Encoding":
			continue
		case ck == "Origin", ck == "Referer":
			continue
		case strings.HasPrefix(ck, "X-Target-"), strings.HasPrefix(ck, "X-Pica-"):
			continue
		case strings.HasPrefix(ck, "Proxy-"):
			continue
		}
		dst[ck] = append([]string(nil), vv...)
	}
}

func encodeMeta(m *tunnelMeta) (string, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
