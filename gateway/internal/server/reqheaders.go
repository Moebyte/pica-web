package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const maxReqHeadersEncoded = 512 << 10

// decodeBase64URL accepts padded and unpadded base64url.
func decodeBase64URL(s string) ([]byte, error) {
	if len(s) > maxReqHeadersEncoded {
		return nil, fmt.Errorf("X-Pica-Req-Headers is too large")
	}
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return nil, fmt.Errorf("X-Pica-Req-Headers is not valid base64url")
}

// decodeRequestHeaders parses base64url(JSON map<string, string|string[]>).
func decodeRequestHeaders(encoded string) (http.Header, error) {
	data, err := decodeBase64URL(strings.TrimSpace(encoded))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("X-Pica-Req-Headers JSON is too large")
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("X-Pica-Req-Headers is not valid JSON: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("X-Pica-Req-Headers must be a JSON object")
	}

	out := make(http.Header, len(raw))
	for name, value := range raw {
		if !validHeaderName(name) {
			return nil, fmt.Errorf("invalid header name %q", name)
		}
		values, err := decodeHeaderValues(value)
		if err != nil {
			return nil, fmt.Errorf("header %q: %w", name, err)
		}
		ck := http.CanonicalHeaderKey(name)
		for _, v := range values {
			if !validHeaderValue(v) {
				return nil, fmt.Errorf("header %q contains an invalid value", name)
			}
			out.Add(ck, v)
		}
	}
	return out, nil
}

func decodeHeaderValues(raw json.RawMessage) ([]string, error) {
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, fmt.Errorf("value must be a string or an array of strings")
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, nil
	}
	return nil, fmt.Errorf("value must be a string or an array of strings")
}

// mergeDecodedRequestHeaders merges an already-validated overlay. Connection
// may name additional hop-by-hop headers; those are honored too.
func mergeDecodedRequestHeaders(dst, src http.Header) {
	hop := hopByHopHeaders(src)
	for name, values := range src {
		if tunnelOverlayHeaderBlocked(name, hop) {
			continue
		}
		dst.Del(name)
		for _, v := range values {
			dst.Add(name, v)
		}
	}
}

func tunnelOverlayHeaderBlocked(name string, hop map[string]bool) bool {
	if hop[name] {
		return true
	}
	switch name {
	case "Host", "Content-Length", "Accept-Encoding":
		return true
	}
	return strings.HasPrefix(name, "X-Pica-") ||
		strings.HasPrefix(name, "X-Target-") ||
		strings.HasPrefix(name, "Proxy-")
}

func validHeaderName(name string) bool {
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

func validHeaderValue(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}
