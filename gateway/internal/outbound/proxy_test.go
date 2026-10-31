package outbound

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSOCKS5ProxyDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		head := make([]byte, 2)
		if _, err := io.ReadFull(conn, head); err != nil {
			serverErr <- err
			return
		}
		methods := make([]byte, int(head[1]))
		if _, err := io.ReadFull(conn, methods); err != nil {
			serverErr <- err
			return
		}
		if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
			serverErr <- err
			return
		}
		req := make([]byte, 4)
		if _, err := io.ReadFull(conn, req); err != nil {
			serverErr <- err
			return
		}
		if req[0] != 0x05 || req[1] != 0x01 || req[3] != 0x01 {
			serverErr <- fmt.Errorf("unexpected request header %v", req)
			return
		}
		addr := make([]byte, 4)
		if _, err := io.ReadFull(conn, addr); err != nil {
			serverErr <- err
			return
		}
		port := make([]byte, 2)
		if _, err := io.ReadFull(conn, port); err != nil {
			serverErr <- err
			return
		}
		if !net.IP(addr).Equal(net.ParseIP("93.184.216.34")) || binary.BigEndian.Uint16(port) != 443 {
			serverErr <- fmt.Errorf("unexpected target %v:%d", addr, binary.BigEndian.Uint16(port))
			return
		}
		if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
			serverErr <- err
			return
		}
		buf := make([]byte, 5)
		if _, err := io.ReadFull(conn, buf); err != nil {
			serverErr <- err
			return
		}
		_, err = conn.Write(buf)
		serverErr <- err
	}()

	p, err := NewProxy("socks5://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := p.DialContext(ctx, "tcp", "93.184.216.34:443")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 5)
	if _, err := io.ReadFull(conn, echo); err != nil {
		t.Fatal(err)
	}
	if string(echo) != "hello" {
		t.Fatalf("echo = %q", echo)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("proxy server: %v", err)
	}
}

func TestHTTPConnectProxyDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		line, err := br.ReadString('\n')
		if err != nil {
			serverErr <- err
			return
		}
		if !strings.HasPrefix(line, "CONNECT 93.184.216.34:443 ") {
			serverErr <- fmt.Errorf("unexpected CONNECT line %q", line)
			return
		}
		sawAuth := false
		for {
			header, err := br.ReadString('\n')
			if err != nil {
				serverErr <- err
				return
			}
			if header == "\r\n" {
				break
			}
			if strings.HasPrefix(strings.ToLower(header), "proxy-authorization: basic ") {
				sawAuth = true
			}
		}
		if !sawAuth {
			serverErr <- fmt.Errorf("missing proxy authorization")
			return
		}
		if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
			serverErr <- err
			return
		}
		buf := make([]byte, 2)
		if _, err := io.ReadFull(conn, buf); err != nil {
			serverErr <- err
			return
		}
		_, err = conn.Write(buf)
		serverErr <- err
	}()

	p, err := NewProxy("http://user:pass@" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := p.DialContext(ctx, "tcp", "93.184.216.34:443")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 2)
	if _, err := io.ReadFull(conn, echo); err != nil {
		t.Fatal(err)
	}
	if string(echo) != "hi" {
		t.Fatalf("echo = %q", echo)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("proxy server: %v", err)
	}
}
