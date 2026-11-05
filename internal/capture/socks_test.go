package capture

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startSOCKSServer starts capture with the SOCKS5 entry enabled on a
// loopback port the OS chooses.
func startSOCKSServer(t *testing.T, cfg SOCKSConfig) *Server {
	t.Helper()
	return startSOCKSServerWith(t, cfg, nil)
}

func startSOCKSServerWith(t *testing.T, cfg SOCKSConfig, change func(*Server)) *Server {
	t.Helper()
	s, err := New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if change != nil {
		change(s)
	}
	cfg.Enabled = true
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	if err := s.SetSOCKS(cfg); err != nil {
		t.Fatalf("configure SOCKS5: %v", err)
	}
	if err := s.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.SOCKSAddr() == "" {
		t.Fatalf("SOCKS5 did not start: %v", s.SOCKSError())
	}
	return s
}

// socksConnect performs the greeting and CONNECT and returns the connection
// and the reply code. A reply below 0 is a failure before the request, such
// as an authentication rejection.
func dialSOCKS5(t *testing.T, proxy string, atyp byte, addr []byte, port int, user, pass string, methods ...byte) (net.Conn, int) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxy, 5*time.Second)
	if err != nil {
		t.Fatalf("dial SOCKS5: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := conn.Write(append([]byte{5, byte(len(methods))}, methods...)); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	var choice [2]byte
	if _, err := io.ReadFull(conn, choice[:]); err != nil {
		t.Fatalf("read greeting reply: %v", err)
	}
	if choice[0] != 5 {
		t.Fatalf("greeting reply version %d", choice[0])
	}
	switch choice[1] {
	case 0x00:
	case 0x02:
		if _, err := conn.Write(append(append([]byte{1, byte(len(user))}, user...), append([]byte{byte(len(pass))}, pass...)...)); err != nil {
			t.Fatalf("write authentication: %v", err)
		}
		var auth [2]byte
		if _, err := io.ReadFull(conn, auth[:]); err != nil {
			t.Fatalf("read authentication reply: %v", err)
		}
		if auth[0] != 1 {
			t.Fatalf("authentication reply version %d", auth[0])
		}
		if auth[1] != 0 {
			return conn, -int(auth[1])
		}
	case 0xff:
		return conn, 0xff
	default:
		t.Fatalf("greeting chose unsupported method %d", choice[1])
	}

	req := []byte{5, 1, 0, atyp}
	req = append(req, addr...)
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write request: %v", err)
	}
	var head [4]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		t.Fatalf("read request reply: %v", err)
	}
	if head[0] != 5 {
		t.Fatalf("request reply version %d", head[0])
	}
	var rest int
	switch head[3] {
	case 1:
		rest = 4 + 2
	case 4:
		rest = 16 + 2
	case 3:
		var l [1]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			t.Fatalf("read bound name length: %v", err)
		}
		rest = int(l[0]) + 2
	default:
		t.Fatalf("request reply address type %d", head[3])
	}
	if _, err := io.ReadFull(conn, make([]byte, rest)); err != nil {
		t.Fatalf("read bound address: %v", err)
	}
	return conn, int(head[1])
}

func socksAddrBytes(t *testing.T, host string, atyp byte) []byte {
	t.Helper()
	switch atyp {
	case 1:
		ip := net.ParseIP(host).To4()
		if ip == nil {
			t.Fatalf("%q is not IPv4", host)
		}
		return ip
	case 4:
		ip := net.ParseIP(host).To16()
		if ip == nil {
			t.Fatalf("%q is not IPv6", host)
		}
		return ip
	case 3:
		return append([]byte{byte(len(host))}, host...)
	default:
		t.Fatalf("unknown address type %d", atyp)
		return nil
	}
}

func hostPort(t *testing.T, raw string) (string, int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse port of %q: %v", raw, err)
	}
	return u.Hostname(), port
}

func TestSOCKS5HandshakeAddressTypesCommandsAndAuth(t *testing.T) {
	s := startSOCKSServer(t, SOCKSConfig{})
	proxy := s.SOCKSAddr()

	for _, tc := range []struct {
		name string
		atyp byte
		addr []byte
		port int
	}{
		{"IPv4", 1, net.ParseIP("127.0.0.1").To4(), 1},
		{"IPv6", 4, net.ParseIP("::1").To16(), 1},
		{"domain", 3, append([]byte{byte(len("localhost"))}, "localhost"...), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, reply := dialSOCKS5(t, proxy, tc.atyp, tc.addr, tc.port, "", "", 0x00, 0x02)
			if reply != 0 {
				t.Fatalf("CONNECT reply %d, want success", reply)
			}
		})
	}

	t.Run("wrong command", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", proxy, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(conn, make([]byte, 2)); err != nil {
			t.Fatal(err)
		}
		// BIND is not offered by a capture proxy; the reply must be the
		// RFC 1928 "command not supported" code, not a dropped connection.
		if _, err := conn.Write([]byte{5, 2, 0, 1, 127, 0, 0, 1, 0, 80}); err != nil {
			t.Fatal(err)
		}
		var head [10]byte
		if _, err := io.ReadFull(conn, head[:]); err != nil {
			t.Fatal(err)
		}
		if head[0] != 5 || head[1] != 0x07 {
			t.Fatalf("reply %v, want version 5 code 7", head[:2])
		}
	})

	t.Run("unsupported address type", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", proxy, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(conn, make([]byte, 2)); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write([]byte{5, 1, 0, 0x05, 1, 2, 3, 4, 0, 80}); err != nil {
			t.Fatal(err)
		}
		var head [10]byte
		if _, err := io.ReadFull(conn, head[:]); err != nil {
			t.Fatal(err)
		}
		if head[1] != 0x08 {
			t.Fatalf("reply code %d, want 8", head[1])
		}
	})

	t.Run("domain with a colon", func(t *testing.T) {
		_, reply := dialSOCKS5(t, proxy, 3, append([]byte{byte(len("bad:name"))}, "bad:name"...), 80, "", "", 0x00)
		if reply != 0x08 {
			t.Fatalf("reply %d, want address type not supported", reply)
		}
	})

	t.Run("no acceptable method", func(t *testing.T) {
		conn, reply := dialSOCKS5(t, proxy, 1, net.IPv4(127, 0, 0, 1), 80, "", "", 0x02)
		if reply != 0xff {
			t.Fatalf("reply %d, want 0xff", reply)
		}
		_ = conn.Close()
	})

	t.Run("authentication success", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "authenticated")
		}))
		defer upstream.Close()
		host, port := hostPort(t, upstream.URL)
		auth := startSOCKSServer(t, SOCKSConfig{Username: "alice", Password: "secret"})
		conn, reply := dialSOCKS5(t, auth.SOCKSAddr(), 1, socksAddrBytes(t, host, 1), port, "alice", "secret", 0x00, 0x02)
		if reply != 0 {
			t.Fatalf("CONNECT reply %d, want success", reply)
		}
		fmt.Fprintf(conn, "GET /auth HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", net.JoinHostPort(host, strconv.Itoa(port)))
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatalf("read authenticated response: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "authenticated" {
			t.Fatalf("authenticated response %d %q", resp.StatusCode, body)
		}
	})

	t.Run("authentication failure", func(t *testing.T) {
		auth := startSOCKSServer(t, SOCKSConfig{Username: "alice", Password: "secret"})
		conn, reply := dialSOCKS5(t, auth.SOCKSAddr(), 1, socksAddrBytes(t, "127.0.0.1", 1), 80, "alice", "wrong", 0x02)
		if reply != -1 {
			t.Fatalf("authentication reply %d, want failure", reply)
		}
		if _, err := conn.Read(make([]byte, 1)); err == nil {
			t.Fatal("a failed authentication left the connection open")
		}
	})

	t.Run("wrong version", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", proxy, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write([]byte{4, 1, 0}); err != nil {
			t.Fatal(err)
		}
		var b [1]byte
		if _, err := conn.Read(b[:]); err == nil {
			t.Fatalf("a SOCKS4 greeting was answered with %v", b[:])
		}
	})
}

func TestSOCKS5PlainHTTPIsCapturedAndRulesApply(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		fmt.Fprintf(w, "upstream %s %s", r.Method, r.URL.RequestURI())
	}))
	defer upstream.Close()
	host, port := hostPort(t, upstream.URL)

	s := startSOCKSServer(t, SOCKSConfig{})
	if err := s.SetRules([]Rule{{Type: RuleBlock, Match: "/blocked"}}); err != nil {
		t.Fatal(err)
	}
	conn, reply := dialSOCKS5(t, s.SOCKSAddr(), 1, socksAddrBytes(t, host, 1), port, "", "", 0x00)
	if reply != 0 {
		t.Fatalf("CONNECT reply %d", reply)
	}
	fmt.Fprintf(conn, "GET /plain?x=1 HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", net.JoinHostPort(host, strconv.Itoa(port)))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "upstream GET /plain?x=1" {
		t.Fatalf("response %d %q", resp.StatusCode, body)
	}
	f := waitFlow(t, s)
	if f.Scheme != "http" || f.Host != net.JoinHostPort(host, strconv.Itoa(port)) || f.Path != "/plain?x=1" || f.Status != 200 || f.ResponseBody != "upstream GET /plain?x=1" {
		t.Fatalf("captured %+v", f)
	}
	if hits != 1 {
		t.Fatalf("upstream hits %d, want 1", hits)
	}

	// The same entry passes rules: a Block rule answers locally and never
	// reaches the upstream.
	conn, reply = dialSOCKS5(t, s.SOCKSAddr(), 1, socksAddrBytes(t, host, 1), port, "", "", 0x00)
	if reply != 0 {
		t.Fatalf("CONNECT reply %d", reply)
	}
	fmt.Fprintf(conn, "GET /blocked HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", net.JoinHostPort(host, strconv.Itoa(port)))
	resp, err = http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read blocked response: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "blocked by vscroxy") {
		t.Fatalf("blocked response %d %q", resp.StatusCode, body)
	}
	if hits != 1 {
		t.Fatalf("blocked request reached the upstream: %d hits", hits)
	}
	if f := waitFlow(t, s); f.Status != http.StatusForbidden || !strings.Contains(strings.Join(f.Actions, ","), "Block") {
		t.Fatalf("blocked flow %+v", f)
	}
}

func TestSOCKS5IPv6AndDomainReachTheUpstream(t *testing.T) {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ok %s", r.URL.Path)
	}))
	upstream.Listener.Close()
	upstream.Listener = ln
	upstream.Start()
	defer upstream.Close()
	host, port := hostPort(t, upstream.URL)

	s := startSOCKSServer(t, SOCKSConfig{})
	s.SetSSL(SSLConfig{All: true})
	for _, tc := range []struct {
		name string
		atyp byte
		addr string
	}{
		{"IPv6", 4, "::1"},
		{"domain", 3, "localhost"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, reply := dialSOCKS5(t, s.SOCKSAddr(), tc.atyp, socksAddrBytes(t, tc.addr, tc.atyp), port, "", "", 0x00)
			if reply != 0 {
				t.Fatalf("CONNECT reply %d", reply)
			}
			fmt.Fprintf(conn, "GET /%s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", tc.name, net.JoinHostPort(host, strconv.Itoa(port)))
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 || string(body) != "ok /"+tc.name {
				t.Fatalf("response %d %q", resp.StatusCode, body)
			}
			f := waitFlow(t, s)
			if f.Host != net.JoinHostPort(tc.addr, strconv.Itoa(port)) || f.Path != "/"+tc.name {
				t.Fatalf("captured %+v", f)
			}
		})
	}
}

func TestSOCKS5HTTPSDecryptAndPassthrough(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "secure %s", r.URL.Path)
	}))
	defer upstream.Close()
	_, port := hostPort(t, upstream.URL)

	t.Run("decrypt", func(t *testing.T) {
		s := startSOCKSServer(t, SOCKSConfig{})
		s.SetSSL(SSLConfig{All: true})
		roots := x509.NewCertPool()
		cert, err := x509.ParseCertificate(s.CACertificate())
		if err != nil {
			t.Fatal(err)
		}
		roots.AddCert(cert)
		conn, reply := dialSOCKS5(t, s.SOCKSAddr(), 3, socksAddrBytes(t, "localhost", 3), port, "", "", 0x00)
		if reply != 0 {
			t.Fatalf("CONNECT reply %d", reply)
		}
		tlsConn := tls.Client(conn, &tls.Config{RootCAs: roots, ServerName: "example.com"})
		if err := tlsConn.Handshake(); err != nil {
			t.Fatalf("TLS handshake through the MITM: %v", err)
		}
		fmt.Fprintf(tlsConn, "GET /secure HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", net.JoinHostPort("localhost", strconv.Itoa(port)))
		resp, err := http.ReadResponse(bufio.NewReader(tlsConn), nil)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "secure /secure" {
			t.Fatalf("response %d %q", resp.StatusCode, body)
		}
		f := waitFlow(t, s)
		if f.Scheme != "https" || f.Host != net.JoinHostPort("localhost", strconv.Itoa(port)) || f.Path != "/secure" || f.ResponseBody != "secure /secure" {
			t.Fatalf("captured %+v", f)
		}
	})

	t.Run("passthrough", func(t *testing.T) {
		s := startSOCKSServer(t, SOCKSConfig{})
		roots := x509.NewCertPool()
		roots.AddCert(upstream.Certificate())
		conn, reply := dialSOCKS5(t, s.SOCKSAddr(), 3, socksAddrBytes(t, "localhost", 3), port, "", "", 0x00)
		if reply != 0 {
			t.Fatalf("CONNECT reply %d", reply)
		}
		tlsConn := tls.Client(conn, &tls.Config{RootCAs: roots, ServerName: "example.com"})
		if err := tlsConn.Handshake(); err != nil {
			t.Fatalf("TLS handshake through the tunnel: %v", err)
		}
		fmt.Fprintf(tlsConn, "GET /secure HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", net.JoinHostPort("localhost", strconv.Itoa(port)))
		resp, err := http.ReadResponse(bufio.NewReader(tlsConn), nil)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "secure /secure" {
			t.Fatalf("response %d %q", resp.StatusCode, body)
		}
		f := waitFlow(t, s)
		if f.Kind != "connect" || f.Method != "CONNECT" || f.Host != net.JoinHostPort("localhost", strconv.Itoa(port)) {
			t.Fatalf("tunnel flow %+v", f)
		}
		select {
		case extra := <-s.Flows():
			t.Fatalf("a bypassed TLS tunnel produced an inner flow: %+v", extra)
		default:
		}
	})
}

func TestSOCKS5PlainHTTPKeepsOneConnectionForSeveralRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "path=%s", r.URL.Path)
	}))
	defer upstream.Close()
	host, port := hostPort(t, upstream.URL)

	s := startSOCKSServer(t, SOCKSConfig{})
	s.SetSSL(SSLConfig{All: true})
	conn, reply := dialSOCKS5(t, s.SOCKSAddr(), 1, socksAddrBytes(t, host, 1), port, "", "", 0x00)
	if reply != 0 {
		t.Fatalf("CONNECT reply %d", reply)
	}
	br := bufio.NewReader(conn)
	for i := 1; i <= 2; i++ {
		path := fmt.Sprintf("/%d", i)
		fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\n\r\n", path, net.JoinHostPort(host, strconv.Itoa(port)))
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("read response %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "path="+path {
			t.Fatalf("response %d: %d %q", i, resp.StatusCode, body)
		}
		f := waitFlow(t, s)
		if f.Path != path || f.Status != 200 {
			t.Fatalf("flow %d: %+v", i, f)
		}
		if i == 2 && !f.Conn.Reused {
			t.Fatalf("second request did not reuse the SOCKS connection: %+v", f.Conn)
		}
	}
}

func TestSOCKS5UsesAppDecryptionRule(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "app rule")
	}))
	defer upstream.Close()
	_, port := hostPort(t, upstream.URL)

	s := startSOCKSServer(t, SOCKSConfig{})
	s.SetSSL(SSLConfig{Apps: []SSLApp{{Name: "display only", Path: exe}}})
	roots := x509.NewCertPool()
	cert, err := x509.ParseCertificate(s.CACertificate())
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(cert)
	conn, reply := dialSOCKS5(t, s.SOCKSAddr(), 3, socksAddrBytes(t, "localhost", 3), port, "", "", 0x00)
	if reply != 0 {
		t.Fatalf("CONNECT reply %d", reply)
	}
	tlsConn := tls.Client(conn, &tls.Config{RootCAs: roots, ServerName: "example.com"})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("TLS handshake: %v", err)
	}
	fmt.Fprintf(tlsConn, "GET /app HTTP/1.1\r\nHost: localhost:%d\r\nConnection: close\r\n\r\n", port)
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "app rule" {
		t.Fatalf("response %d %q", resp.StatusCode, body)
	}
	f := waitFlow(t, s)
	if f.Scheme != "https" || f.ProcessPath != exe || f.Path != "/app" {
		t.Fatalf("captured %+v", f)
	}
}

func TestSOCKS5HostProfileMapsTheDial(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "host=%s", r.Host)
	}))
	defer upstream.Close()
	_, port := hostPort(t, upstream.URL)

	s := startSOCKSServer(t, SOCKSConfig{})
	s.SetSSL(SSLConfig{All: true})
	setHosts(t, s, "127.0.0.1 every.test")
	conn, reply := dialSOCKS5(t, s.SOCKSAddr(), 3, socksAddrBytes(t, "every.test", 3), port, "", "", 0x00)
	if reply != 0 {
		t.Fatalf("CONNECT reply %d", reply)
	}
	fmt.Fprintf(conn, "GET /x HTTP/1.1\r\nHost: every.test:%d\r\nConnection: close\r\n\r\n", port)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if want := "host=every.test:" + strconv.Itoa(port); resp.StatusCode != 200 || string(body) != want {
		t.Fatalf("response %d %q, want %q", resp.StatusCode, body, want)
	}
	f := waitFlow(t, s)
	if f.URL != "http://every.test:"+strconv.Itoa(port)+"/x" {
		t.Fatalf("URL %q", f.URL)
	}
	if want := "every.test → 127.0.0.1"; f.DNSMapped != want || f.HostProfile != "Test:1" {
		t.Fatalf("DNSMapped %q from %q, want %q from Test:1", f.DNSMapped, f.HostProfile, want)
	}
}

func TestSOCKS5NestedConnectIsRefused(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	host, port := hostPort(t, target.URL)

	reached := make(chan struct{}, 1)
	second, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	go func() {
		for {
			conn, err := second.Accept()
			if err != nil {
				return
			}
			select {
			case reached <- struct{}{}:
			default:
			}
			_ = conn.Close()
		}
	}()

	s := startSOCKSServer(t, SOCKSConfig{})
	s.SetSSL(SSLConfig{All: true})
	conn, reply := dialSOCKS5(t, s.SOCKSAddr(), 1, socksAddrBytes(t, host, 1), port, "", "", 0x00)
	if reply != 0 {
		t.Fatalf("CONNECT reply %d", reply)
	}
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", second.Addr(), second.Addr())
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read nested CONNECT response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "pinned") {
		t.Fatalf("nested CONNECT = %d %q, want 400 pinned", resp.StatusCode, body)
	}
	select {
	case <-reached:
		t.Fatal("nested CONNECT reached the second target")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSOCKS5BypassedTargetIsARawTunnel(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "220 banner first\r\n")
	}()
	_, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)

	// No decryption rule: the connection is sniffed for a short window, then
	// tunnelled, so a server-first protocol is not held for the decrypted
	// 10-second peek.
	s := startSOCKSServer(t, SOCKSConfig{})
	conn, reply := dialSOCKS5(t, s.SOCKSAddr(), 1, socksAddrBytes(t, "127.0.0.1", 1), port, "", "", 0x00)
	if reply != 0 {
		t.Fatalf("CONNECT reply %d", reply)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	banner, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("server-first banner: %v", err)
	}
	if banner != "220 banner first\r\n" {
		t.Fatalf("banner %q", banner)
	}
	f := waitFlow(t, s)
	if f.Kind != "connect" || f.Method != "CONNECT" {
		t.Fatalf("bypassed tunnel flow %+v", f)
	}
}

func TestSOCKS5DecryptedPeekHasATimeout(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	_, portText, _ := net.SplitHostPort(target.Addr().String())
	port, _ := strconv.Atoi(portText)

	s := startSOCKSServerWith(t, SOCKSConfig{}, func(s *Server) {
		s.proxy.Opts.SOCKSPeekTimeout = 100 * time.Millisecond
	})
	s.SetSSL(SSLConfig{All: true})
	conn, reply := dialSOCKS5(t, s.SOCKSAddr(), 1, socksAddrBytes(t, "127.0.0.1", 1), port, "", "", 0x00)
	if reply != 0 {
		t.Fatalf("CONNECT reply %d", reply)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	started := time.Now()
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("a decrypted target that sent nothing stayed open")
	}
	if elapsed := time.Since(started); elapsed > 800*time.Millisecond {
		t.Fatalf("peek timeout took %s", elapsed)
	}
}

func TestSetSOCKSRejectsAuthenticationWithoutAPassword(t *testing.T) {
	s, err := New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	err = s.SetSOCKS(SOCKSConfig{Enabled: true, Addr: "127.0.0.1:0", Username: "alice"})
	if err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("SetSOCKS error = %v", err)
	}
}

func TestSOCKS5BypassedClientFirstProtocolIsForwarded(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err == nil && line == "PING\n" {
			_, _ = io.WriteString(conn, "PONG\n")
		}
	}()
	_, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)

	s := startSOCKSServer(t, SOCKSConfig{})
	conn, reply := dialSOCKS5(t, s.SOCKSAddr(), 1, socksAddrBytes(t, "127.0.0.1", 1), port, "", "", 0x00)
	if reply != 0 {
		t.Fatalf("CONNECT reply %d", reply)
	}
	if _, err := io.WriteString(conn, "PING\n"); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if line != "PONG\n" {
		t.Fatalf("reply %q, want PONG", line)
	}
	f := waitFlow(t, s)
	if f.Kind != "connect" || f.Method != "CONNECT" {
		t.Fatalf("bypassed client-first flow %+v", f)
	}
}
