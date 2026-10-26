package lan

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// newCA builds a CA certificate in DER form, as the UI would hand it to Start.
func newCA(t *testing.T) []byte {
	t.Helper()
	return selfSigned(t, true)
}

// newLeaf builds a certificate that is not a CA, which Start must refuse.
func newLeaf(t *testing.T) []byte {
	t.Helper()
	return selfSigned(t, false)
}

func selfSigned(t *testing.T, isCA bool) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "vscroxy test certificate"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}
	if isCA {
		tmpl.KeyUsage |= x509.KeyUsageCertSign
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return der
}

// testGateway starts a gateway that admits loopback clients and forwards to
// upstream, after letting change adjust the configuration.
func testGateway(t *testing.T, upstream string, change func(*Config)) *Gateway {
	t.Helper()
	cfg := Config{
		ListenAddr:      "127.0.0.1:0",
		CertificateAddr: "127.0.0.1:0",
		ProxyAddr:       upstream,
		Allowed:         []string{"127.0.0.1"},
		Certificate:     newCA(t),
	}
	if change != nil {
		change(&cfg)
	}
	g, err := Start(cfg)
	if err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	t.Cleanup(func() { _ = g.Close() })
	return g
}

// upstream is a TCP server that counts the connections it accepted and hands
// each one to handle.
type upstream struct {
	addr string

	mu       sync.Mutex
	accepted int
}

func newUpstream(t *testing.T, handle func(net.Conn)) *upstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	u := &upstream{addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			u.mu.Lock()
			u.accepted++
			u.mu.Unlock()
			if handle == nil {
				_ = conn.Close()
				continue
			}
			go handle(conn)
		}
	}()
	return u
}

// count is how many connections reached the upstream, which is the core in
// these tests.
func (u *upstream) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.accepted
}

// echo sends back what it is given, so a test can tell bytes crossed both ways.
func echo(conn net.Conn) {
	defer conn.Close()
	_, _ = io.Copy(conn, conn)
}

// dialGateway connects to addr with a deadline, so a stuck test fails rather
// than hanging.
func dialGateway(t *testing.T, addr string) *net.TCPConn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	return conn.(*net.TCPConn)
}

// freeAddr reserves an address and releases it, for a test that needs the
// number rather than the listener.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve address: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release address: %v", err)
	}
	return addr
}

func TestStartRejectsInvalidConfig(t *testing.T) {
	ca := newCA(t)
	base := func() Config {
		return Config{
			ListenAddr:      "127.0.0.1:0",
			CertificateAddr: "127.0.0.1:0",
			ProxyAddr:       "127.0.0.1:7890",
			Allowed:         []string{"127.0.0.1"},
			Certificate:     ca,
		}
	}
	pemCA := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca})
	tests := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"no listen address", func(c *Config) { c.ListenAddr = "" }, "ListenAddr is empty"},
		{"host name for listen", func(c *Config) { c.ListenAddr = "localhost:0" }, "not a literal ip:port"},
		{"no port", func(c *Config) { c.ListenAddr = "127.0.0.1" }, "not a literal ip:port"},
		{"wildcard listen", func(c *Config) { c.ListenAddr = "0.0.0.0:0" }, "every interface"},
		{"wildcard listen v6", func(c *Config) { c.ListenAddr = "[::]:0" }, "every interface"},
		{"mapped wildcard listeners", func(c *Config) {
			c.ListenAddr, c.CertificateAddr = "[::ffff:0.0.0.0]:0", "[::ffff:0.0.0.0]:0"
		}, "every interface"},
		{"no certificate address", func(c *Config) { c.CertificateAddr = "" }, "CertificateAddr is empty"},
		{"wildcard certificate address", func(c *Config) { c.CertificateAddr = "[::]:0" }, "every interface"},
		{"mapped wildcard certificate address", func(c *Config) { c.CertificateAddr = "[::ffff:0.0.0.0]:0" }, "every interface"},
		{"listeners on different addresses", func(c *Config) { c.CertificateAddr = "127.0.0.2:0" }, "different addresses"},
		{"no allowed addresses", func(c *Config) { c.Allowed = nil }, "Allowed is empty"},
		{"blank allowed addresses", func(c *Config) { c.Allowed = []string{"", "  "} }, "Allowed is empty"},
		{"malformed allowed entry", func(c *Config) { c.Allowed = []string{"not-an-address"} }, "not an IP address"},
		{"malformed cidr", func(c *Config) { c.Allowed = []string{"10.0.0.0/33"} }, "not a CIDR range"},
		{"cidr allows everyone", func(c *Config) { c.Allowed = []string{"0.0.0.0/0"} }, "allows every address"},
		{"cidr allows everyone v6", func(c *Config) { c.Allowed = []string{"::/0"} }, "allows every address"},
		{"mapped cidr allows every IPv4 address", func(c *Config) { c.Allowed = []string{"::ffff:0.0.0.0/96"} }, "allows every address"},
		{"no proxy address", func(c *Config) { c.ProxyAddr = "" }, "ProxyAddr is empty"},
		{"proxy address without port", func(c *Config) { c.ProxyAddr = "127.0.0.1" }, "ProxyAddr"},
		{"proxy address on every interface", func(c *Config) { c.ProxyAddr = "0.0.0.0:7890" }, "every interface"},
		{"mapped proxy address on every interface", func(c *Config) { c.ProxyAddr = "[::ffff:0.0.0.0]:7890" }, "every interface"},
		{"proxy port zero", func(c *Config) { c.ProxyAddr = "127.0.0.1:0" }, "no usable port"},
		{"no certificate", func(c *Config) { c.Certificate = nil }, "Certificate is empty"},
		{"garbage certificate", func(c *Config) { c.Certificate = []byte("not a certificate") }, "not a DER certificate"},
		{"pem certificate", func(c *Config) { c.Certificate = pemCA }, "not a DER certificate"},
		{"not a ca certificate", func(c *Config) { c.Certificate = newLeaf(t) }, "not a CA certificate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			tt.change(&cfg)
			g, err := Start(cfg)
			if err == nil {
				_ = g.Close()
				t.Fatal("Start accepted an invalid configuration")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestStartRollsBackPartialBind(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold certificate address: %v", err)
	}
	defer held.Close()

	proxyListen := freeAddr(t)
	g, err := Start(Config{
		ListenAddr:      proxyListen,
		CertificateAddr: held.Addr().String(),
		ProxyAddr:       "127.0.0.1:7890",
		Allowed:         []string{"127.0.0.1"},
		Certificate:     newCA(t),
	})
	if err == nil {
		_ = g.Close()
		t.Fatal("Start succeeded with the certificate address taken")
	}
	// The proxy listener was bound before the failure: it must be released
	// again, or the next start would fail on an address nobody uses.
	ln, err := net.Listen("tcp", proxyListen)
	if err != nil {
		t.Fatalf("proxy address still bound after a failed start: %v", err)
	}
	_ = ln.Close()
}

func TestForwardsHalfClose(t *testing.T) {
	got := make(chan string, 1)
	up := newUpstream(t, func(conn net.Conn) {
		defer conn.Close()
		// Only a half-close forwarded by the gateway ends this read.
		body, err := io.ReadAll(conn)
		if err != nil {
			got <- "read: " + err.Error()
			return
		}
		got <- string(body)
		_, _ = io.WriteString(conn, "answer:"+string(body))
	})
	g := testGateway(t, up.addr, nil)

	client := dialGateway(t, g.Addr())
	if _, err := io.WriteString(client, "ping"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatalf("half-close: %v", err)
	}
	answer, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("read answer: %v", err)
	}
	if string(answer) != "answer:ping" {
		t.Fatalf("answer = %q, want %q", answer, "answer:ping")
	}
	select {
	case body := <-got:
		if body != "ping" {
			t.Fatalf("upstream read %q, want %q", body, "ping")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream never saw the end of the request")
	}
}

func TestGateAdmitsConfiguredClient(t *testing.T) {
	up := newUpstream(t, echo)
	g := testGateway(t, up.addr, func(c *Config) {
		c.Allowed = []string{"192.0.2.7", "127.0.0.0/8"}
	})

	client := dialGateway(t, g.Addr())
	if _, err := io.WriteString(client, "hello"); err != nil {
		t.Fatalf("write: %v", err)
	}
	reply := make([]byte, len("hello"))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if string(reply) != "hello" {
		t.Fatalf("reply = %q, want %q", reply, "hello")
	}

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + g.CertificateAddr() + "/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}

	status := g.Status()
	if !strings.HasPrefix(status.LastProxyClient, "127.0.0.1:") {
		t.Fatalf("LastProxyClient = %q, want the loopback client", status.LastProxyClient)
	}
	if !strings.HasPrefix(status.LastCertificateClient, "127.0.0.1:") {
		t.Fatalf("LastCertificateClient = %q, want the loopback client", status.LastCertificateClient)
	}
	if age := time.Since(status.LastProxyAt); age < 0 || age > time.Minute {
		t.Fatalf("LastProxyAt is %s old", age)
	}
	if age := time.Since(status.LastCertificateAt); age < 0 || age > time.Minute {
		t.Fatalf("LastCertificateAt is %s old", age)
	}
}

// TestGateAdmitsMappedIPv4Entries checks that an address written in its
// IPv4-mapped form names the IPv4 client that connects.
func TestGateAdmitsMappedIPv4Entries(t *testing.T) {
	for _, entry := range []string{"::ffff:127.0.0.1", "::ffff:127.0.0.0/104"} {
		t.Run(entry, func(t *testing.T) {
			up := newUpstream(t, echo)
			g := testGateway(t, up.addr, func(c *Config) { c.Allowed = []string{entry} })
			client := dialGateway(t, g.Addr())
			if _, err := io.WriteString(client, "hi"); err != nil {
				t.Fatalf("write: %v", err)
			}
			reply := make([]byte, len("hi"))
			if _, err := io.ReadFull(client, reply); err != nil {
				t.Fatalf("read reply: %v", err)
			}
		})
	}
}

func TestGateDeniesUnlistedClient(t *testing.T) {
	up := newUpstream(t, echo)
	g := testGateway(t, up.addr, func(c *Config) {
		c.Allowed = []string{"192.0.2.7"}
	})

	client := dialGateway(t, g.Addr())
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("the proxy gateway served a client that is not allowed")
	}

	if resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + g.CertificateAddr() + "/health"); err == nil {
		resp.Body.Close()
		t.Fatalf("the certificate endpoint answered a client that is not allowed: %d", resp.StatusCode)
	}

	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if n := up.count(); n != 0 {
		t.Fatalf("the gateway dialled the core %d times for a denied client", n)
	}
	if status := g.Status(); !status.LastProxyAt.IsZero() || !status.LastCertificateAt.IsZero() {
		t.Fatalf("a denied client was recorded: %+v", status)
	}
}

func TestGateIgnoresForwardedHeaders(t *testing.T) {
	up := newUpstream(t, echo)
	g := testGateway(t, up.addr, func(c *Config) {
		c.Allowed = []string{"192.0.2.7"}
	})

	// The source address is loopback, so the claim in these headers must not
	// admit it.
	client := dialGateway(t, g.Addr())
	_, err := fmt.Fprintf(client, "GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\nX-Forwarded-For: 192.0.2.7\r\nX-Real-IP: 192.0.2.7\r\nForwarded: for=192.0.2.7\r\n\r\n")
	if err != nil {
		t.Fatalf("write request: %v", err)
	}
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("a forwarded header admitted a client the gate refuses")
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+g.CertificateAddr()+"/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-Forwarded-For", "192.0.2.7")
	req.Header.Set("X-Real-IP", "192.0.2.7")
	if resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req); err == nil {
		resp.Body.Close()
		t.Fatalf("a forwarded header admitted a client at the certificate endpoint: %d", resp.StatusCode)
	}

	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if n := up.count(); n != 0 {
		t.Fatalf("the gateway dialled the core %d times for a denied client", n)
	}
}

func TestCertificateEndpoint(t *testing.T) {
	der := newCA(t)
	g := testGateway(t, "127.0.0.1:1", func(c *Config) { c.Certificate = der })
	base := "http://" + g.CertificateAddr()
	client := &http.Client{Timeout: 5 * time.Second}

	get := func(t *testing.T, method, path string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, base+path, nil)
		if err != nil {
			t.Fatalf("build %s %s: %v", method, path, err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read %s %s: %v", method, path, err)
		}
		return resp, body
	}

	resp, body := get(t, http.MethodGet, caPath)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", caPath, resp.StatusCode)
	}
	if !bytes.Equal(body, der) {
		t.Fatalf("GET %s served %d bytes that are not the CA", caPath, len(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-x509-ca-cert" {
		t.Fatalf("Content-Type = %q, want application/x-x509-ca-cert", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="vscroxy-ca.cer"` {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if bytes.Contains(body, []byte("PRIVATE KEY")) || bytes.Contains(body, []byte("-----BEGIN")) {
		t.Fatal("the certificate endpoint served something other than DER")
	}

	resp, body = get(t, http.MethodHead, caPath)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD %s status = %d, want 200", caPath, resp.StatusCode)
	}
	if len(body) != 0 {
		t.Fatalf("HEAD %s returned %d body bytes", caPath, len(body))
	}
	if resp.ContentLength != int64(len(der)) {
		t.Fatalf("HEAD %s Content-Length = %d, want %d", caPath, resp.ContentLength, len(der))
	}

	resp, body = get(t, http.MethodGet, "/health")
	if resp.StatusCode != http.StatusOK || string(body) != "ok\n" {
		t.Fatalf("GET /health = %d %q, want 200 %q", resp.StatusCode, body, "ok\n")
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("GET /health Content-Type = %q", ct)
	}
	if resp, body = get(t, http.MethodHead, "/health"); resp.StatusCode != http.StatusOK || len(body) != 0 {
		t.Fatalf("HEAD /health = %d with %d body bytes", resp.StatusCode, len(body))
	}

	resp, body = get(t, http.MethodGet, "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), caPath) {
		t.Fatalf("GET / does not mention %s: %q", caPath, body)
	}
	if bytes.Contains(bytes.ToLower(body), []byte("<html")) {
		t.Fatalf("GET / served HTML: %q", body)
	}

	for _, path := range []string{"/vscroxy-ca.key", "/vscroxy-ca.cer.bak", "/mitmproxy-ca.pem", "/index.html", caPath + "/"} {
		if resp, _ := get(t, http.MethodGet, path); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, resp.StatusCode)
		}
	}

	for _, request := range []struct{ method, path string }{
		{http.MethodPost, caPath},
		{http.MethodPut, caPath},
		{http.MethodPost, "/health"},
		{http.MethodDelete, "/"},
	} {
		resp, _ := get(t, request.method, request.path)
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s status = %d, want 405", request.method, request.path, resp.StatusCode)
		}
		if allow := resp.Header.Get("Allow"); !strings.Contains(allow, http.MethodGet) {
			t.Fatalf("%s %s Allow = %q, want GET", request.method, request.path, allow)
		}
	}
}

func TestCloseRevokesEstablishedStream(t *testing.T) {
	coreConn := make(chan net.Conn, 1)
	up := newUpstream(t, func(conn net.Conn) {
		coreConn <- conn
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	})
	g := testGateway(t, up.addr, nil)

	client := dialGateway(t, g.Addr())
	if _, err := io.WriteString(client, "ping"); err != nil {
		t.Fatalf("write: %v", err)
	}
	reply := make([]byte, len("ping"))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}

	var core net.Conn
	select {
	case core = <-coreConn:
	case <-time.After(5 * time.Second):
		t.Fatal("the core never saw the forwarded connection")
	}
	_ = core.SetReadDeadline(time.Now().Add(5 * time.Second))

	if err := g.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	for _, conn := range []net.Conn{client, core} {
		_, err := conn.Read(make([]byte, 1))
		if err == nil {
			t.Fatal("a forwarded connection survived close")
		}
		if networkErr, ok := errors.AsType[net.Error](err); ok && networkErr.Timeout() {
			t.Fatal("a forwarded connection timed out instead of closing")
		}
	}

	if err := g.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	for _, addr := range []string{g.Addr(), g.CertificateAddr()} {
		if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			conn.Close()
			t.Fatalf("%s still accepts after close", addr)
		}
	}
	// The core keeps its own listener: the gateway closes only what it
	// opened, so a new gateway can forward to the same core.
	if conn, err := net.DialTimeout("tcp", up.addr, time.Second); err != nil {
		t.Fatalf("the core's listener was closed with the gateway: %v", err)
	} else {
		conn.Close()
	}
}

// TestCloseRightAfterStart closes each gateway as soon as Start returns, before
// its serving goroutines may have run, so a listener that is never handed to
// the HTTP server cannot be left bound.
func TestCloseRightAfterStart(t *testing.T) {
	up := newUpstream(t, echo)
	for range 20 {
		g := testGateway(t, up.addr, nil)
		proxyAddr, certAddr := g.Addr(), g.CertificateAddr()
		if err := g.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		for _, addr := range []string{proxyAddr, certAddr} {
			if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
				conn.Close()
				t.Fatalf("%s still listens after close", addr)
			}
		}
	}
}

func TestConcurrentAcceptsAndClose(t *testing.T) {
	up := newUpstream(t, echo)
	for round := range 5 {
		g := testGateway(t, up.addr, nil)
		var clients sync.WaitGroup
		for range 16 {
			clients.Go(func() {
				conn, err := net.DialTimeout("tcp", g.Addr(), time.Second)
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = io.WriteString(conn, "hello")
				_, _ = io.Copy(io.Discard, conn)
			})
		}
		// Close while connections are being accepted and forwarded: no
		// accepted connection may outlive the gateway, and no worker may
		// hang.
		time.Sleep(time.Millisecond)
		if err := g.Close(); err != nil {
			t.Fatalf("round %d: close: %v", round, err)
		}
		done := make(chan struct{})
		go func() {
			clients.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("round %d: clients did not finish after close", round)
		}
	}
}

func TestConfigIsImmutable(t *testing.T) {
	original := newCA(t)
	der := bytes.Clone(original)
	allowed := []string{"127.0.0.1"}
	up := newUpstream(t, echo)

	g, err := Start(Config{
		ListenAddr:      "127.0.0.1:0",
		CertificateAddr: "127.0.0.1:0",
		ProxyAddr:       up.addr,
		Allowed:         allowed,
		Certificate:     der,
	})
	if err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	defer func() { _ = g.Close() }()

	// The caller may reuse what it handed Start, as the UI does when the
	// configuration is edited in place.
	allowed[0] = "192.0.2.7"
	for i := range der {
		der[i] = 0
	}

	client := dialGateway(t, g.Addr())
	if _, err := io.WriteString(client, "still here"); err != nil {
		t.Fatalf("write: %v", err)
	}
	reply := make([]byte, len("still here"))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + g.CertificateAddr() + caPath)
	if err != nil {
		t.Fatalf("get certificate: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read certificate: %v", err)
	}
	if !bytes.Equal(body, original) {
		t.Fatal("the served certificate changed with the caller's slice")
	}
}
