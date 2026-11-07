package capture

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/upstream"
)

// recordingHTTPProxy is a real forward proxy: CONNECT for tunnels and
// absolute-form forwarding for plain HTTP. It records what it carried and can
// require Basic authentication.
type recordingHTTPProxy struct {
	addr           string
	auth           string
	connectOnly443 bool
	certPEM        []byte
	ln             net.Listener

	mu           sync.Mutex
	connects     []string
	requests     []string
	authFailures int
}

func startRecordingHTTPProxy(t *testing.T, auth string) *recordingHTTPProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &recordingHTTPProxy{addr: ln.Addr().String(), auth: auth, ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go p.serve()
	return p
}

// startRecordingHTTPSProxy wraps the same real proxy in TLS, for the PAC
// HTTPS directive. The capture engine accepts the throwaway certificate, as a
// debugging proxy does for inspected upstreams.
func startRecordingHTTPSProxy(t *testing.T, auth string) *recordingHTTPProxy {
	t.Helper()
	seed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := seed.TLS.Certificates[0]
	seed.Close()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	p := &recordingHTTPProxy{addr: ln.Addr().String(), auth: auth, ln: ln,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})}
	t.Cleanup(func() { _ = ln.Close() })
	go p.serve()
	return p
}

func (p *recordingHTTPProxy) serve() {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(conn)
	}
}

func (p *recordingHTTPProxy) handle(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if p.auth != "" {
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte(p.auth))
		if req.Header.Get("Proxy-Authorization") != want {
			p.mu.Lock()
			p.authFailures++
			p.mu.Unlock()
			fmt.Fprint(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"test\"\r\nContent-Length: 0\r\n\r\n")
			return
		}
	}
	if req.Method == http.MethodConnect {
		target := req.Host
		if target == "" {
			target = req.URL.Host
		}
		p.mu.Lock()
		p.connects = append(p.connects, target)
		p.mu.Unlock()
		if p.connectOnly443 {
			if _, port, _ := net.SplitHostPort(target); port != "443" {
				fmt.Fprint(conn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
				return
			}
		}
		out, err := net.DialTimeout("tcp", target, 3*time.Second)
		if err != nil {
			fmt.Fprint(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
			return
		}
		defer out.Close()
		fmt.Fprint(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() {
			_, _ = io.Copy(out, br)
			_ = out.(*net.TCPConn).CloseWrite()
		}()
		_, _ = io.Copy(conn, out)
		return
	}
	p.mu.Lock()
	p.requests = append(p.requests, req.URL.String())
	p.mu.Unlock()
	out := req.Clone(req.Context())
	out.RequestURI = ""
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	resp, err := transport.RoundTrip(out)
	if err != nil {
		fmt.Fprint(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer resp.Body.Close()
	_ = resp.Write(conn)
}

func (p *recordingHTTPProxy) counts() (connects int, authFailures int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.connects), p.authFailures
}

func (p *recordingHTTPProxy) saw(target string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, got := range p.connects {
		if got == target {
			return true
		}
	}
	return false
}

// recordingSOCKSProxy is a real RFC 1928 CONNECT proxy with optional RFC 1929
// username/password authentication.
type recordingSOCKSProxy struct {
	addr string
	user string
	pass string
	ln   net.Listener

	mu           sync.Mutex
	connects     []string
	authFailures int
}

func startRecordingSOCKSProxy(t *testing.T, user, pass string) *recordingSOCKSProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &recordingSOCKSProxy{addr: ln.Addr().String(), user: user, pass: pass, ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go p.serve()
	return p
}

func (p *recordingSOCKSProxy) serve() {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(conn)
	}
}

func (p *recordingSOCKSProxy) handle(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil || head[0] != 5 {
		return
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	has := func(m byte) bool {
		for _, got := range methods {
			if got == m {
				return true
			}
		}
		return false
	}
	if p.user != "" {
		if !has(2) {
			_, _ = conn.Write([]byte{5, 0xff})
			return
		}
		_, _ = conn.Write([]byte{5, 2})
		auth := make([]byte, 2)
		if _, err := io.ReadFull(br, auth); err != nil || auth[0] != 1 {
			return
		}
		user := make([]byte, int(auth[1]))
		if _, err := io.ReadFull(br, user); err != nil {
			return
		}
		plen, err := br.ReadByte()
		if err != nil {
			return
		}
		pass := make([]byte, int(plen))
		if _, err := io.ReadFull(br, pass); err != nil {
			return
		}
		if string(user) != p.user || string(pass) != p.pass {
			p.mu.Lock()
			p.authFailures++
			p.mu.Unlock()
			_, _ = conn.Write([]byte{1, 1})
			return
		}
		_, _ = conn.Write([]byte{1, 0})
	} else {
		if !has(0) {
			_, _ = conn.Write([]byte{5, 0xff})
			return
		}
		_, _ = conn.Write([]byte{5, 0})
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil || req[0] != 5 || req[1] != 1 {
		_, _ = conn.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	var host string
	switch req[3] {
	case 1:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(br, buf); err != nil {
			return
		}
		host = net.IP(buf).String()
	case 3:
		n, err := br.ReadByte()
		if err != nil {
			return
		}
		buf := make([]byte, int(n))
		if _, err := io.ReadFull(br, buf); err != nil {
			return
		}
		host = string(buf)
	case 4:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(br, buf); err != nil {
			return
		}
		host = net.IP(buf).String()
	default:
		_, _ = conn.Write([]byte{5, 8, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(br, portBuf); err != nil {
		return
	}
	target := net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(portBuf)))
	p.mu.Lock()
	p.connects = append(p.connects, target)
	p.mu.Unlock()
	out, err := net.DialTimeout("tcp", target, 3*time.Second)
	if err != nil {
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer out.Close()
	_, _ = conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	go func() {
		_, _ = io.Copy(out, br)
		_ = out.(*net.TCPConn).CloseWrite()
	}()
	_, _ = io.Copy(conn, out)
}

func (p *recordingSOCKSProxy) counts() (connects int, authFailures int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.connects), p.authFailures
}

func writePACFile(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proxy.pac")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func proxyAddress(p *recordingHTTPProxy) string  { return "http://" + p.addr }
func socksAddress(p *recordingSOCKSProxy) string { return "socks5://" + p.addr }

func getThrough(t *testing.T, s *Server, rawURL string) (*http.Response, string) {
	t.Helper()
	resp, err := clientFor(t, s, nil).Get(rawURL)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, string(body)
}

func writeProxyCert(t *testing.T, proxy *recordingHTTPProxy) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proxy.pem")
	if err := os.WriteFile(path, proxy.certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func targetURL(t *testing.T, target *httptest.Server) string {
	t.Helper()
	return target.URL + "/resource"
}

func TestHostRulesChooseARealHTTPProxy(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "first") }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "second") }))
	defer second.Close()
	proxyA := startRecordingHTTPProxy(t, "")
	proxyB := startRecordingHTTPProxy(t, "")

	s, _ := startServer(t)
	if err := s.SetUpstreamConfig(upstream.Settings{
		Default: proxyAddress(proxyB),
		Rules:   []upstream.Rule{{Host: strings.TrimPrefix(first.URL, "http://"), Proxy: proxyAddress(proxyA)}},
	}, nil); err != nil {
		t.Fatal(err)
	}

	resp, body := getThrough(t, s, targetURL(t, first))
	if resp.StatusCode != 200 || body != "first" {
		t.Fatalf("first response = %d %q", resp.StatusCode, body)
	}
	if !proxyA.saw(strings.TrimPrefix(first.URL, "http://")) {
		t.Fatal("the host rule's proxy did not carry the request")
	}
	if got, _ := proxyB.counts(); got != 0 {
		t.Fatalf("the default proxy carried %d requests", got)
	}
	flow := waitFlow(t, s)
	if !strings.Contains(flow.Conn.Log, "Upstream") || !strings.Contains(flow.Conn.Log, "rule ") {
		t.Fatalf("connection log does not name the rule: %s", flow.Conn.Log)
	}

	resp, body = getThrough(t, s, targetURL(t, second))
	if resp.StatusCode != 200 || body != "second" {
		t.Fatalf("second response = %d %q", resp.StatusCode, body)
	}
	if got, _ := proxyA.counts(); got != 1 {
		t.Fatalf("the rule's proxy carried %d requests, want 1", got)
	}
	if !proxyB.saw(strings.TrimPrefix(second.URL, "http://")) {
		t.Fatal("the default proxy did not carry the unmatched request")
	}
}

func TestBypassConnectsDirectly(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "direct") }))
	defer target.Close()
	proxy := startRecordingHTTPProxy(t, "")

	s, _ := startServer(t)
	if err := s.SetUpstreamConfig(upstream.Settings{
		Default: proxyAddress(proxy),
		Bypass:  []string{"127.0.0.1"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	resp, body := getThrough(t, s, targetURL(t, target))
	if resp.StatusCode != 200 || body != "direct" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}
	if got, _ := proxy.counts(); got != 0 {
		t.Fatalf("a bypassed request used the proxy %d times", got)
	}
	flow := waitFlow(t, s)
	if !strings.Contains(flow.Conn.Log, "bypass 127.0.0.1") {
		t.Fatalf("connection log does not name the bypass: %s", flow.Conn.Log)
	}
}

func TestPACFallsBackToTheNextProxy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "fallback") }))
	defer target.Close()
	dead := freeAddr(t)
	socks := startRecordingSOCKSProxy(t, "", "")

	s, _ := startServer(t)
	script := fmt.Sprintf(`function FindProxyForURL(url, host) {
		return "PROXY %s; SOCKS5 %s; DIRECT";
	}`, dead, socks.addr)
	if err := s.SetUpstreamConfig(upstream.Settings{PAC: writePACFile(t, script)}, nil); err != nil {
		t.Fatal(err)
	}
	resp, body := getThrough(t, s, targetURL(t, target))
	if resp.StatusCode != 200 || body != "fallback" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}
	if got, _ := socks.counts(); got != 1 {
		t.Fatalf("the fallback SOCKS proxy carried %d connections, want 1", got)
	}
	flow := waitFlow(t, s)
	if !strings.Contains(flow.Conn.Log, "Upstream") || !strings.Contains(flow.Conn.Log, "PAC ·") {
		t.Fatalf("connection log does not name the PAC decision: %s", flow.Conn.Log)
	}
	if !strings.Contains(flow.Conn.Log, "Could not connect to") {
		t.Fatalf("connection log does not show the failed first candidate: %s", flow.Conn.Log)
	}
}

func TestPACDirectWinsOverTheDefault(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "direct") }))
	defer target.Close()
	proxy := startRecordingHTTPProxy(t, "")
	s, _ := startServer(t)
	if err := s.SetUpstreamConfig(upstream.Settings{
		Default: proxyAddress(proxy),
		PAC:     writePACFile(t, `function FindProxyForURL() { return "DIRECT"; }`),
	}, nil); err != nil {
		t.Fatal(err)
	}
	if resp, body := getThrough(t, s, targetURL(t, target)); resp.StatusCode != 200 || body != "direct" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}
	if got, _ := proxy.counts(); got != 0 {
		t.Fatalf("DIRECT used the default proxy %d times", got)
	}
}

func TestHTTPUpstreamAuthentication(t *testing.T) {
	hits := atomic.Int64{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "authenticated")
	}))
	defer target.Close()
	proxy := startRecordingHTTPProxy(t, "alice:s3cret")
	address := proxyAddress(proxy)

	s, _ := startServer(t)
	settings := upstream.Settings{
		Default:     address,
		Credentials: []upstream.Credential{{Proxy: address, Username: "alice"}},
	}
	if err := s.SetUpstreamConfig(settings, map[string]string{address: "s3cret"}); err != nil {
		t.Fatal(err)
	}
	if resp, body := getThrough(t, s, targetURL(t, target)); resp.StatusCode != 200 || body != "authenticated" {
		t.Fatalf("authenticated response = %d %q", resp.StatusCode, body)
	}
	if _, failures := proxy.counts(); failures != 0 {
		t.Fatalf("the proxy rejected %d valid requests", failures)
	}
	if hits.Load() != 1 {
		t.Fatalf("target hits = %d, want 1", hits.Load())
	}

	// A wrong password fails the request. The PAC list also offers DIRECT,
	// which must not be used after an authentication failure.
	if err := s.SetUpstreamConfig(upstream.Settings{
		PAC:         writePACFile(t, fmt.Sprintf(`function FindProxyForURL() { return "PROXY %s; DIRECT"; }`, proxy.addr)),
		Credentials: []upstream.Credential{{Proxy: address, Username: "alice"}},
	}, map[string]string{address: "wrong"}); err != nil {
		t.Fatal(err)
	}
	resp, _ := getThrough(t, s, targetURL(t, target))
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("wrong password response = %d, want 407", resp.StatusCode)
	}
	if hits.Load() != 1 {
		t.Fatalf("authentication failure fell back to DIRECT: target hits = %d", hits.Load())
	}
	if _, failures := proxy.counts(); failures != 1 {
		t.Fatalf("proxy authentication failures = %d, want 1", failures)
	}
}

func TestSOCKS5UpstreamAuthentication(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "socks") }))
	defer target.Close()
	proxy := startRecordingSOCKSProxy(t, "alice", "s3cret")
	address := socksAddress(proxy)

	s, _ := startServer(t)
	settings := upstream.Settings{
		Default:     address,
		Credentials: []upstream.Credential{{Proxy: address, Username: "alice"}},
	}
	if err := s.SetUpstreamConfig(settings, map[string]string{address: "s3cret"}); err != nil {
		t.Fatal(err)
	}
	if resp, body := getThrough(t, s, targetURL(t, target)); resp.StatusCode != 200 || body != "socks" {
		t.Fatalf("authenticated response = %d %q", resp.StatusCode, body)
	}
	_ = waitFlow(t, s) // the successful request's flow

	if err := s.SetUpstreamConfig(settings, map[string]string{address: "wrong"}); err != nil {
		t.Fatal(err)
	}
	resp, _ := getThrough(t, s, targetURL(t, target))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("wrong password response = %d, want 502", resp.StatusCode)
	}
	flow := waitFlow(t, s)
	if !strings.Contains(flow.Conn.Error, "authentication") {
		t.Fatalf("SOCKS authentication error = %q", flow.Conn.Error)
	}
	if _, failures := proxy.counts(); failures != 1 {
		t.Fatalf("SOCKS authentication failures = %d, want 1", failures)
	}
}

func TestPACHTTPSProxyDirective(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "https-proxy") }))
	defer target.Close()
	proxy := startRecordingHTTPSProxy(t, "")
	s, _ := startServer(t)
	s.SetCertificates(certs.Settings{RootCAs: []string{writeProxyCert(t, proxy)}})
	script := fmt.Sprintf(`function FindProxyForURL() { return "HTTPS %s"; }`, proxy.addr)
	if err := s.SetUpstreamConfig(upstream.Settings{PAC: writePACFile(t, script)}, nil); err != nil {
		t.Fatal(err)
	}
	resp, body := getThrough(t, s, targetURL(t, target))
	if resp.StatusCode != 200 || body != "https-proxy" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}
	if !proxy.saw(strings.TrimPrefix(target.URL, "http://")) {
		t.Fatal("the HTTPS proxy did not carry the request")
	}
}

func TestAuthoredReplayUsesUpstreamRules(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "replayed") }))
	defer target.Close()
	proxy := startRecordingHTTPProxy(t, "")
	s, _ := startServer(t)
	if err := s.SetUpstreamConfig(upstream.Settings{
		Default: proxyAddress(proxy),
	}, nil); err != nil {
		t.Fatal(err)
	}
	out, err := s.ReplayWithPolicy(context.Background(), Flow{
		Method: "GET", URL: target.URL + "/replay", RequestHeader: http.Header{},
	}, nil, TransferPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != 200 || out.ResponseBody != "replayed" {
		t.Fatalf("replay = %d %q", out.Status, out.ResponseBody)
	}
	if !proxy.saw(strings.TrimPrefix(target.URL, "http://")) {
		t.Fatal("an authored send bypassed the configured upstream")
	}
}

func TestPACLoadErrorKeepsTheRunningConfiguration(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "old") }))
	defer target.Close()
	proxy := startRecordingHTTPProxy(t, "")
	s, _ := startServer(t)
	if err := s.SetUpstreamConfig(upstream.Settings{Default: "http://" + proxy.addr}, nil); err != nil {
		t.Fatal(err)
	}
	bad := writePACFile(t, `function FindProxyForURL( {`)
	if err := s.SetUpstreamConfig(upstream.Settings{PAC: bad}, nil); err == nil {
		t.Fatal("a PAC syntax error was accepted")
	}
	if got := s.UpstreamConfig().Default; got != "http://"+proxy.addr {
		t.Fatalf("failed PAC load changed the default upstream to %q", got)
	}
	if resp, body := getThrough(t, s, targetURL(t, target)); resp.StatusCode != 200 || body != "old" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}
	if !proxy.saw(strings.TrimPrefix(target.URL, "http://")) {
		t.Fatal("the old upstream stopped carrying traffic")
	}
}

func TestPACInfiniteLoopFailsWithoutConnecting(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "leaked") }))
	defer target.Close()
	hits := atomic.Int64{}
	target.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "leaked")
	})
	s, _ := startServer(t)
	script := writePACFile(t, `function FindProxyForURL() { while (true) {} }`)
	if err := s.SetUpstreamConfig(upstream.Settings{PAC: script}, nil); err != nil {
		t.Fatal(err)
	}
	resp, _ := getThrough(t, s, targetURL(t, target))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("response = %d, want 502", resp.StatusCode)
	}
	flow := waitFlow(t, s)
	if !strings.Contains(flow.Conn.Error, "PAC") || !strings.Contains(flow.Conn.Error, "did not finish") {
		t.Fatalf("PAC timeout error = %q", flow.Conn.Error)
	}
	if hits.Load() != 0 {
		t.Fatalf("a timed-out PAC reached the target %d times", hits.Load())
	}
}

func startBlackhole(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
	})
	return ln.Addr().String()
}

func TestHTTPUpstreamPasswordWithReservedCharacters(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer target.Close()
	proxy := startRecordingHTTPProxy(t, "alice:p@ss/w:rd")
	address := proxyAddress(proxy)
	s, _ := startServer(t)
	if err := s.SetUpstreamConfig(upstream.Settings{
		Default:     address,
		Credentials: []upstream.Credential{{Proxy: address, Username: "alice"}},
	}, map[string]string{address: "p@ss/w:rd"}); err != nil {
		t.Fatal(err)
	}
	if resp, body := getThrough(t, s, targetURL(t, target)); resp.StatusCode != 200 || body != "ok" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}
	if _, failures := proxy.counts(); failures != 0 {
		t.Fatalf("the proxy rejected %d requests with reserved characters in the password", failures)
	}
}

func TestProxyHTTPResponseStopsFallback(t *testing.T) {
	hits := atomic.Int64{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "leaked")
	}))
	defer target.Close()
	proxy := startRecordingHTTPProxy(t, "")
	proxy.connectOnly443 = true // CONNECT to this plain HTTP target gets 403
	s, _ := startServer(t)
	if err := s.SetUpstreamConfig(upstream.Settings{
		PAC: writePACFile(t, fmt.Sprintf(`function FindProxyForURL() { return "PROXY %s; DIRECT"; }`, proxy.addr)),
	}, nil); err != nil {
		t.Fatal(err)
	}
	resp, _ := getThrough(t, s, targetURL(t, target))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("response = %d, want 502", resp.StatusCode)
	}
	if hits.Load() != 0 {
		t.Fatalf("a proxy 403 fell back to DIRECT: target hits = %d", hits.Load())
	}
	if !proxy.saw(strings.TrimPrefix(target.URL, "http://")) {
		t.Fatal("the proxy did not see the CONNECT")
	}
	flow := waitFlow(t, s)
	if !strings.Contains(flow.Conn.Error, "403") {
		t.Fatalf("connection error = %q, want the proxy's 403", flow.Conn.Error)
	}
}

func TestUpstreamCandidateTimeoutFallsBack(t *testing.T) {
	blackhole := startBlackhole(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "direct") }))
	defer target.Close()
	s, _ := startServer(t)
	s.proxy.Opts.UpstreamDialTimeout = 300 * time.Millisecond
	if err := s.SetUpstreamConfig(upstream.Settings{
		PAC: writePACFile(t, fmt.Sprintf(`function FindProxyForURL() { return "PROXY %s; DIRECT"; }`, blackhole)),
	}, nil); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, body := getThrough(t, s, targetURL(t, target))
	if resp.StatusCode != 200 || body != "direct" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("a silent proxy blocked fallback for %s", elapsed)
	}
	flow := waitFlow(t, s)
	if !strings.Contains(flow.Conn.Log, "Could not connect") || !strings.Contains(flow.Conn.Log, "DIRECT") {
		t.Fatalf("connection log does not show the timeout and fallback: %s", flow.Conn.Log)
	}
}

func TestHTTPSUpstreamCertificateIsVerified(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "trusted") }))
	defer target.Close()
	proxy := startRecordingHTTPSProxy(t, "")
	s, _ := startServer(t)
	if err := s.SetUpstreamConfig(upstream.Settings{
		PAC: writePACFile(t, fmt.Sprintf(`function FindProxyForURL() { return "HTTPS %s"; }`, proxy.addr)),
	}, nil); err != nil {
		t.Fatal(err)
	}
	resp, _ := getThrough(t, s, targetURL(t, target))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("untrusted proxy response = %d, want 502", resp.StatusCode)
	}
	flow := waitFlow(t, s)
	if text := strings.ToLower(flow.Conn.Error); !strings.Contains(text, "certificate") && !strings.Contains(text, "x509") {
		t.Fatalf("connection error = %q, want a certificate error", flow.Conn.Error)
	}
	if connects, _ := proxy.counts(); connects != 0 {
		t.Fatalf("the untrusted proxy received %d CONNECTs", connects)
	}

	s.SetCertificates(certs.Settings{RootCAs: []string{writeProxyCert(t, proxy)}})
	if resp, body := getThrough(t, s, targetURL(t, target)); resp.StatusCode != 200 || body != "trusted" {
		t.Fatalf("trusted proxy response = %d %q", resp.StatusCode, body)
	}
	if !proxy.saw(strings.TrimPrefix(target.URL, "http://")) {
		t.Fatal("the trusted HTTPS proxy did not carry the request")
	}
}

func TestMapRemotePlainHTTPThroughHTTPProxy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "mapped") }))
	defer target.Close()
	proxy := startRecordingHTTPProxy(t, "")
	proxy.connectOnly443 = true
	s, _ := startServerWithRules(t, Rule{Type: RuleMapRemote, Match: "mapped.invalid", Target: target.URL})
	if err := s.SetUpstreamConfig(upstream.Settings{Default: proxyAddress(proxy)}, nil); err != nil {
		t.Fatal(err)
	}
	proxyURL, _ := url.Parse("http://" + s.Addr())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}
	resp, err := client.Get("http://mapped.invalid/path?q=1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "mapped" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}
	proxy.mu.Lock()
	requests := strings.Join(proxy.requests, " ")
	proxy.mu.Unlock()
	if !strings.Contains(requests, target.URL+"/path?q=1") {
		t.Fatalf("the proxy did not receive an absolute-URI request: %q", requests)
	}
}

func TestUpstreamConfigLoadFailureFailsClosed(t *testing.T) {
	hits := atomic.Int64{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "leaked")
	}))
	defer target.Close()
	s, _ := startServer(t)
	deadPAC := "http://" + freeAddr(t) + "/proxy.pac"
	if err := s.SetUpstreamConfigFailClosed(upstream.Settings{PAC: deadPAC}, nil); err == nil {
		t.Fatal("an unreachable PAC URL was accepted")
	}
	if s.UpstreamError() == nil {
		t.Fatal("the load error was not stored")
	}
	resp, _ := getThrough(t, s, targetURL(t, target))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("response = %d, want 502", resp.StatusCode)
	}
	if hits.Load() != 0 {
		t.Fatalf("a failed upstream configuration fell back to DIRECT: target hits = %d", hits.Load())
	}
}

func TestUpstreamURLForPAC(t *testing.T) {
	for _, tc := range []struct {
		name    string
		req     *http.Request
		address string
		want    string
	}{
		{"CONNECT", &http.Request{Method: http.MethodConnect, URL: &url.URL{Scheme: "http", Host: "example.com:443"}}, "example.com:443", "https://example.com/"},
		{"CONNECT IPv6", &http.Request{Method: http.MethodConnect, URL: &url.URL{Scheme: "http", Host: "[::1]:443"}}, "[::1]:443", "https://[::1]/"},
		{"CONNECT IPv6 nondefault port", &http.Request{Method: http.MethodConnect, URL: &url.URL{Scheme: "http", Host: "[::1]:8443"}}, "[::1]:8443", "https://[::1]:8443/"},
		{"HTTPS strips path", &http.Request{URL: &url.URL{Scheme: "https", Host: "example.com", Path: "/a", RawQuery: "q=1"}}, "example.com:443", "https://example.com/"},
		{"HTTPS nondefault port", &http.Request{URL: &url.URL{Scheme: "https", Host: "example.com:8443", Path: "/a"}}, "example.com:8443", "https://example.com:8443/"},
		{"HTTP keeps path", &http.Request{URL: &url.URL{Scheme: "http", Host: "example.com", Path: "/a", RawQuery: "q=1"}}, "example.com:80", "http://example.com/a?q=1"},
		{"WebSocket", &http.Request{URL: &url.URL{Scheme: "ws", Host: "example.com", Path: "/socket"}}, "example.com:80", "http://example.com/socket"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := upstreamURL(tc.req, tc.address); got != tc.want {
				t.Fatalf("upstreamURL = %q, want %q", got, tc.want)
			}
		})
	}
}
