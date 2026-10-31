package outbound

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Proxy is a configured outbound SOCKS5 or HTTP CONNECT proxy.
type Proxy struct {
	scheme string
	addr   string
	user   string
	pass   string
}

// NewProxy parses socks5://user:pass@host:port or http://user:pass@host:port.
// An empty string means direct connections.
func NewProxy(raw string) (*Proxy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse outboundProxy: %w", err)
	}
	var scheme string
	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h":
		scheme = "socks5"
	case "http":
		scheme = "http"
	default:
		return nil, fmt.Errorf("outboundProxy: unsupported scheme %q (use socks5:// or http://)", u.Scheme)
	}
	host := u.Hostname()
	port := u.Port()
	if host == "" || port == "" {
		return nil, errors.New("outboundProxy must be host:port")
	}
	if _, err := strconv.Atoi(port); err != nil {
		return nil, fmt.Errorf("outboundProxy: invalid port %q", port)
	}
	p := &Proxy{scheme: scheme, addr: net.JoinHostPort(host, port)}
	if u.User != nil {
		p.user = u.User.Username()
		p.pass, _ = u.User.Password()
		if len(p.user) > 255 || len(p.pass) > 255 {
			return nil, errors.New("outboundProxy: credentials are too long")
		}
	}
	return p, nil
}

// DialContext connects to target (which the policy has already validated, so
// it is normally an IP:port) through the proxy.
func (p *Proxy) DialContext(ctx context.Context, network, target string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", p.addr)
	if err != nil {
		return nil, fmt.Errorf("dial proxy %s: %w", p.addr, err)
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(15 * time.Second)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, err
	}

	if p.scheme == "socks5" {
		err = p.socks5(conn, target)
	} else {
		var proxied net.Conn
		proxied, err = p.httpConnect(conn, target)
		if err == nil {
			conn = proxied
		}
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (p *Proxy) socks5(conn net.Conn, target string) error {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return fmt.Errorf("socks5 target: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return fmt.Errorf("socks5 target port %q", portStr)
	}

	methods := []byte{0x00}
	if p.user != "" {
		methods = []byte{0x00, 0x02}
	}
	if _, err := conn.Write([]byte{0x05, byte(len(methods))}); err != nil {
		return fmt.Errorf("socks5 greeting: %w", err)
	}
	if _, err := conn.Write(methods); err != nil {
		return fmt.Errorf("socks5 greeting: %w", err)
	}
	var reply [2]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return fmt.Errorf("socks5 greeting reply: %w", err)
	}
	if reply[0] != 0x05 {
		return fmt.Errorf("socks5: unexpected version %d", reply[0])
	}
	switch reply[1] {
	case 0x00:
	case 0x02:
		if p.user == "" {
			return errors.New("socks5: proxy requires authentication")
		}
		if err := p.socks5Auth(conn); err != nil {
			return err
		}
	case 0xff:
		return errors.New("socks5: no acceptable authentication method")
	default:
		return fmt.Errorf("socks5: unsupported auth method %d", reply[1])
	}

	req := []byte{0x05, 0x01, 0x00} // CONNECT
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(req, 0x01)
			req = append(req, ip4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return errors.New("socks5: target hostname too long")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("socks5 connect: %w", err)
	}

	var head [4]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return fmt.Errorf("socks5 connect reply: %w", err)
	}
	if head[0] != 0x05 {
		return fmt.Errorf("socks5: unexpected reply version %d", head[0])
	}
	if head[1] != 0x00 {
		return fmt.Errorf("socks5: connect failed with code %d", head[1])
	}
	var skip int
	switch head[3] {
	case 0x01:
		skip = 4
	case 0x04:
		skip = 16
	case 0x03:
		var l [1]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			return fmt.Errorf("socks5 reply address: %w", err)
		}
		skip = int(l[0])
	default:
		return fmt.Errorf("socks5: unsupported reply address type %d", head[3])
	}
	if _, err := io.CopyN(io.Discard, conn, int64(skip+2)); err != nil {
		return fmt.Errorf("socks5 reply address: %w", err)
	}
	return nil
}

func (p *Proxy) socks5Auth(conn net.Conn) error {
	req := []byte{0x01, byte(len(p.user))}
	req = append(req, p.user...)
	req = append(req, byte(len(p.pass)))
	req = append(req, p.pass...)
	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("socks5 auth: %w", err)
	}
	var reply [2]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return fmt.Errorf("socks5 auth reply: %w", err)
	}
	if reply[1] != 0x00 {
		return errors.New("socks5: authentication rejected")
	}
	return nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.r.Read(b) }

func (p *Proxy) httpConnect(conn net.Conn, target string) (net.Conn, error) {
	var req strings.Builder
	fmt.Fprintf(&req, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	if p.user != "" {
		token := base64.StdEncoding.EncodeToString([]byte(p.user + ":" + p.pass))
		fmt.Fprintf(&req, "Proxy-Authorization: Basic %s\r\n", token)
	}
	req.WriteString("Proxy-Connection: Keep-Alive\r\n\r\n")
	if _, err := io.WriteString(conn, req.String()); err != nil {
		return nil, fmt.Errorf("http proxy connect: %w", err)
	}

	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("http proxy response: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 2 {
		return nil, fmt.Errorf("http proxy: malformed status line %q", strings.TrimSpace(line))
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil {
		return nil, fmt.Errorf("http proxy: malformed status code %q", fields[1])
	}
	total := len(line)
	for {
		line, err = br.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("http proxy response headers: %w", err)
		}
		total += len(line)
		if total > 16<<10 {
			return nil, errors.New("http proxy: response headers too large")
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	if code < 200 || code > 299 {
		return nil, fmt.Errorf("http proxy: CONNECT returned %d", code)
	}
	return &bufferedConn{Conn: conn, r: br}, nil
}
