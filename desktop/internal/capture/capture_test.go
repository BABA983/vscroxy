package capture

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// freeAddr reserves an address the proxy can listen on.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve address: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("release address: %v", err)
	}
	return addr
}

func startServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(freeAddr(t), dir)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if err := s.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, filepath.Join(dir, "mitmproxy-ca-cert.pem")
}

func clientFor(t *testing.T, s *Server, roots *x509.CertPool) *http.Client {
	t.Helper()
	u, err := url.Parse("http://" + s.Addr())
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	tr := &http.Transport{Proxy: http.ProxyURL(u)}
	if roots != nil {
		tr.TLSClientConfig = &tls.Config{RootCAs: roots}
	}
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

func waitFrame(t *testing.T, s *Server) FrameEvent {
	t.Helper()
	select {
	case fe := <-s.Frames():
		return fe
	case <-time.After(5 * time.Second):
		t.Fatal("no frame captured")
		return FrameEvent{}
	}
}

func waitFlow(t *testing.T, s *Server) Flow {
	t.Helper()
	select {
	case f := <-s.Flows():
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("no flow captured")
		return Flow{}
	}
}

func TestStartRejectsAnOccupiedExplicitAddress(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	s, err := New(busy.Addr().String(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Start(); err == nil {
		t.Fatal("Start reported success on another application's listener")
	}
	if s.Running() {
		t.Fatal("failed startup reported a running proxy")
	}
}

func TestDefaultProxyFallsBackToAnOwnedPort(t *testing.T) {
	// Keep the competing listener bound for the entire test. If a real app
	// already owns 7890, leave it alone and still exercise the fallback.
	busy, err := net.Listen("tcp", "127.0.0.1:7890")
	if err == nil {
		other := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "existing application")
		})}
		go other.Serve(busy)
		t.Cleanup(func() { _ = other.Close() })
	} else {
		t.Logf("preferred port already unavailable: %v", err)
	}
	s, err := New("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.Running() {
		t.Fatal("proxy reported running before Start")
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	addr := s.Addr()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || host != "127.0.0.1" || n < 1024 || n == 7890 || !s.Running() {
		t.Fatalf("fallback listener = %q, running = %v", addr, s.Running())
	}
	t.Logf("occupied 127.0.0.1:7890 → bound %s", addr)
	if err := s.Start(); err == nil || s.Addr() != addr {
		t.Fatal("a second Start must not acquire another port")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "through our proxy")
	}))
	defer upstream.Close()
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/fallback")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(body) != "through our proxy" {
		t.Fatalf("forwarded body = %q, error = %v", body, err)
	}
	f := waitFlow(t, s)
	if f.URL != upstream.URL+"/fallback" || f.ResponseBody != "through our proxy" {
		t.Fatalf("wrong captured exchange: %+v", f)
	}
	replayed, err := s.Replay(t.Context(), f)
	if err != nil || replayed.ResponseBody != "through our proxy" {
		t.Fatalf("replay on actual port: body = %q, error = %v", replayed.ResponseBody, err)
	}
	if busy != nil {
		client := &http.Client{Transport: &http.Transport{}, Timeout: 5 * time.Second}
		resp, err := client.Get("http://" + busy.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != "existing application" {
			t.Fatalf("competing application was affected: %q, %v", body, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.Running() {
		t.Fatal("closed proxy still reported running")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("Close did not release %s: %v", addr, err)
	}
	defer ln.Close()
	if err := s.Start(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Start after Close = %v", err)
	}
}

func TestCloseImmediatelyAfterStartReleasesThePort(t *testing.T) {
	s, err := New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	addr := s.Addr()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if s.Running() {
		t.Fatal("closed proxy reported running")
	}
}

func TestReplayTrustsTheProxyCA(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "replayed")
	}))
	defer upstream.Close()

	s, caPath := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read proxy CA: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("proxy CA is not PEM")
	}
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	u.Host = "localhost:" + u.Port()
	resp, err := clientFor(t, s, roots).Get(u.String() + "/replay")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()
	f := waitFlow(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.Replay(ctx, f); err != nil {
		t.Fatalf("replay of an HTTPS flow: %v", err)
	}
	if again := waitFlow(t, s); again.ResponseBody != "replayed" {
		t.Fatalf("replayed body = %q", again.ResponseBody)
	}
}

func TestIgnoredHostsAreNotShown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "noise")
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	s.SetIgnore([]string{"127.0.0.1"})
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/noise")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(body) != "noise" {
		t.Fatalf("body = %q, %v; want the response untouched", body, err)
	}
	select {
	case f := <-s.Flows():
		t.Fatalf("ignored flow was published: %+v", f)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestFailedRequestShowsAnError(t *testing.T) {
	s, _ := startServer(t)
	dead := freeAddr(t) // reserved and released, so nothing listens there
	resp, err := clientFor(t, s, nil).Get("http://" + dead + "/gone")
	if err == nil {
		_ = resp.Body.Close()
	}

	f := waitFlow(t, s)
	if f.Status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", f.Status)
	}
	if !strings.Contains(f.ResponseBody, "vscroxy:") {
		t.Fatalf("body = %q, want the reason", f.ResponseBody)
	}
	if f.Conn.ErrorStage != "TCP" || f.Conn.Error == "" || !strings.Contains(f.Conn.Log, dead) {
		t.Fatalf("connection failure was not diagnosed: %+v", f.Conn)
	}
}

func TestConnectionLogDistinguishesTLSFailuresAndReuse(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "secure result")
	}))
	defer upstream.Close()
	s, caPath := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	// The client deliberately does NOT trust the proxy's CA.
	if resp, err := clientFor(t, s, nil).Get(upstream.URL); err == nil {
		resp.Body.Close()
		t.Fatal("untrusted intercepted certificate was accepted")
	}
	failed := waitFlow(t, s)
	if failed.Method != "CONNECT" || failed.Status != 200 || failed.Conn.ErrorStage != "TLS client" || failed.Conn.Error == "" {
		t.Fatalf("TLS rejection should remain distinct from CONNECT 200: %+v", failed)
	}
	if !strings.Contains(failed.Conn.Log, "No HTTP response from the target") {
		t.Fatalf("misleading connection log: %s", failed.Conn.Log)
	}
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("invalid CA")
	}
	client := clientFor(t, s, roots)
	defer client.CloseIdleConnections()
	for i := range 2 {
		resp, err := client.Get(upstream.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != "secure result" {
			t.Fatalf("body %q: %v", body, err)
		}
		f := waitFlow(t, s)
		if f.Conn.Error != "" || f.Conn.Reused != (i == 1) {
			t.Fatalf("connection: %+v", f.Conn)
		}
		for _, want := range []string{"[TCP]", "[TLS upstream]", "[TLS client]", "SAN:", "127.0.0.1", "ALPN:", "verification: skipped"} {
			if !strings.Contains(f.Conn.Log, want) {
				t.Fatalf("missing %q in %s", want, f.Conn.Log)
			}
		}
	}
	// A TLS URL aimed at a plaintext HTTP server fails on the OTHER TLS side.
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer plain.Close()
	if resp, err := client.Get(strings.Replace(plain.URL, "http:", "https:", 1)); err == nil {
		resp.Body.Close()
		t.Fatal("plaintext server accepted as TLS")
	}
	f := waitFlow(t, s)
	if f.Conn.ErrorStage != "TLS upstream" || f.Conn.Error == "" {
		t.Fatalf("upstream failure: %+v", f.Conn)
	}
}

func TestConnectionLogDNSFailure(t *testing.T) {
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, &net.DNSError{Name: "failure.test", Err: "fixture name not found", IsNotFound: true}
	}}
	t.Cleanup(func() { net.DefaultResolver = previous })
	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get("http://failure.test/diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	f := waitFlow(t, s)
	if f.Status != 502 || f.Conn.ErrorStage != "DNS" || !strings.Contains(f.Conn.Log, "failure.test") {
		t.Fatalf("DNS failure misclassified: %+v", f.Conn)
	}
}

func TestDNSMappingAppliesToEveryApp(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "host=%s", r.Host)
	}))
	defer upstream.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	if err != nil {
		t.Fatalf("parse upstream address: %v", err)
	}

	s, _ := startServer(t)
	if err := s.SetDNS([]string{"every.test = 127.0.0.1"}); err != nil {
		t.Fatalf("set dns: %v", err)
	}
	target := "http://every.test:" + port + "/x"
	resp, err := clientFor(t, s, nil).Get(target)
	if err != nil {
		t.Fatalf("request through the mapping: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// The dial goes to the address the rule gives; the request keeps the name
	// in its URL and Host header, which is what makes a virtual host work.
	if want := "host=every.test:" + port; string(body) != want {
		t.Fatalf("body = %q, want %q", body, want)
	}

	var mapped Flow
	for mapped.URL == "" {
		f := waitFlow(t, s)
		if f.URL == target {
			mapped = f
		}
	}
	if want := "every.test → 127.0.0.1"; mapped.DNSMapped != want {
		t.Fatalf("DNSMapped = %q, want %q", mapped.DNSMapped, want)
	}
	if mapped.Conn.Proto == "" {
		t.Fatal("Conn.Proto is empty, want the HTTP version the client spoke")
	}
	if want := "every.test:" + port; mapped.Conn.Server != want {
		t.Fatalf("Conn.Server = %q, want %q", mapped.Conn.Server, want)
	}
	if mapped.Conn.Client == "" || mapped.Conn.Proxy == "" {
		t.Fatalf("client %q, proxy %q; want both addresses", mapped.Conn.Client, mapped.Conn.Proxy)
	}
	if want := "127.0.0.1"; mapped.Conn.Resolved != want {
		t.Fatalf("Conn.Resolved = %q, want %q", mapped.Conn.Resolved, want)
	}
}

func TestDNSMappingsResolveTheNameElsewhere(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "host=%s", r.Host)
	}))
	defer upstream.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	if err != nil {
		t.Fatalf("parse upstream address: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable path: %v", err)
	}
	app := filepath.Base(exe)

	s, _ := startServer(t)
	target := "http://mapped.test:" + port + "/x"
	// Nothing resolves this name before the mapping: the proxy answers the
	// gateway error it gives a request whose server it cannot reach.
	before, err := clientFor(t, s, nil).Get(target)
	if err != nil {
		t.Fatalf("request before the mapping: %v", err)
	}
	_ = before.Body.Close()
	if before.StatusCode != http.StatusBadGateway {
		t.Fatalf("status before the mapping = %d, want 502", before.StatusCode)
	}

	// A rule for another app does not apply to this one.
	if err := s.SetDNS([]string{"mapped.test = 127.0.0.1 @other-app"}); err != nil {
		t.Fatalf("set dns: %v", err)
	}
	other, err := clientFor(t, s, nil).Get(target)
	if err != nil {
		t.Fatalf("request for another app: %v", err)
	}
	_ = other.Body.Close()
	if other.StatusCode != http.StatusBadGateway {
		t.Fatalf("status for another app = %d, want 502", other.StatusCode)
	}

	// The rule for this app points the dial at the address, and the request
	// keeps the name it asked for.
	if err := s.SetDNS([]string{"mapped.test = 127.0.0.1 @" + app}); err != nil {
		t.Fatalf("set dns: %v", err)
	}
	resp, err := clientFor(t, s, nil).Get(target)
	if err != nil {
		t.Fatalf("request through the mapping: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if want := "host=mapped.test:" + port; string(body) != want {
		t.Fatalf("body = %q, want %q", body, want)
	}

	var mapped Flow
	for mapped.URL == "" {
		f := waitFlow(t, s)
		// The requests the mapping did not apply to were published too, as
		// the gateway error the proxy gives an unreachable server.
		if f.URL == target && f.Status == http.StatusOK {
			mapped = f
		}
	}
	if want := "mapped.test → 127.0.0.1"; mapped.DNSMapped != want {
		t.Fatalf("DNSMapped = %q, want %q", mapped.DNSMapped, want)
	}
	if hit, ok := s.LastDNSMatch(); !ok || hit.Name != "mapped.test" || hit.IP.String() != "127.0.0.1" {
		t.Fatalf("last match = %+v, %v", hit, ok)
	}
}

func TestIgnoredAppsAreNotShown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "noise")
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable path: %v", err)
	}
	s.SetIgnoreApps([]string{filepath.Base(exe)})
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/noise")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()
	select {
	case f := <-s.Flows():
		t.Fatalf("ignored app's flow was published: %+v", f)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestTimingSplitsWaitAndDownload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/slow")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()

	f := waitFlow(t, s)
	if f.Wait < 20 {
		t.Fatalf("wait = %d ms, want at least 20", f.Wait)
	}
	if f.Wait+f.Download != f.Millis {
		t.Fatalf("wait %d + download %d != total %d", f.Wait, f.Download, f.Millis)
	}
}

func TestTunnelsUntilADomainIsDecrypted(t *testing.T) {
	const body = `{"source":"decrypted HTTPS","items":[3,7]}`
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	defer upstream.Close()

	s, caPath := startServer(t)
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read proxy CA: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("proxy CA is not PEM")
	}
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	u.Host = "localhost:" + u.Port()

	// Nothing is decrypted yet: the client sees the upstream's certificate,
	// which it does not trust, and the tunnel shows as a CONNECT flow.
	if _, err := clientFor(t, s, roots).Get(u.String() + "/before"); err == nil {
		t.Fatal("request succeeded, want a TLS error before decryption")
	}
	tunnel := waitFlow(t, s)
	if tunnel.Method != "CONNECT" || tunnel.Kind != "connect" {
		t.Fatalf("tunnel = %+v, want a CONNECT flow", tunnel)
	}

	// Decrypted now: the proxy's CA is what the client sees.
	// Older UI versions stored the CONNECT authority, including its port.
	s.SetSSL(SSLConfig{Hosts: []string{u.Host}})
	resp, err := clientFor(t, s, roots).Get(u.String() + "/after")
	if err != nil {
		t.Fatalf("request after decryption: %v", err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(data) != body {
		t.Fatalf("HTTPS response = %q, %v", data, err)
	}
	if f := waitFlow(t, s); f.Status != 200 || f.Kind != "http" || f.Path != "/after" || f.ResponseBody != body {
		t.Fatalf("flow = %+v, want the decrypted path and complete JSON response", f)
	}
	// IP clients send no SNI. Trust must still verify the certificate against
	// the CONNECT target, without disabling TLS verification in the client.
	u, _ = url.Parse(upstream.URL)
	s.SetSSL(SSLConfig{Hosts: []string{u.Host}})
	resp, err = clientFor(t, s, roots).Get(u.String() + "/ip-without-sni")
	if err != nil {
		t.Fatalf("verified IP request without SNI: %v", err)
	}
	data, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(data) != body {
		t.Fatalf("IP response = %q, %v", data, err)
	}
	if f := waitFlow(t, s); f.Path != "/ip-without-sni" || f.ResponseBody != body {
		t.Fatalf("IP capture = %+v", f)
	}
}

func TestSSLDomainRulesIgnorePorts(t *testing.T) {
	for _, tc := range []struct {
		rule, host string
		want       bool
	}{
		{"Example.COM.:8443", "api.example.com:9443", true},
		{"example.com:8443", "notexample.com:8443", false},
		{"127.0.0.1:18443", "127.0.0.1", true},
		{"[2001:db8::1]:8443", "[2001:db8::1]:9443", true},
		{"2001:db8::1", "[2001:db8::1]", true},
		{"[2001:db8::1]:8443", "2001:db8::2", false},
		{"[ab].example.com", "a.example.com:8443", true},
	} {
		if got := (SSLConfig{Hosts: []string{tc.rule}}).Decrypts(tc.host); got != tc.want {
			t.Errorf("rule %q for %q = %v, want %v", tc.rule, tc.host, got, tc.want)
		}
	}
}

func TestHostMatches(t *testing.T) {
	patterns := []string{"example.com", "*.internal.test", "# comment", ".corp.test"}
	cases := map[string]bool{
		"example.com":       true,
		"api.example.com":   true,
		"EXAMPLE.COM":       true,
		"api.internal.test": true,
		"internal.test":     false,
		"corp.test":         true,
		"deep.corp.test":    true,
		"example.org":       false,
		"notexample.com":    false,
	}
	for host, want := range cases {
		if got := hostMatches(host, patterns); got != want {
			t.Errorf("hostMatches(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestSpillsLargeBodiesToDisk(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 200<<10)
	sizes := make(chan int, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		sizes <- len(body)
		_, _ = w.Write(big)
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Post(upstream.URL+"/big", "text/plain", bytes.NewReader(big))
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()
	if got := <-sizes; got != len(big) {
		t.Fatalf("upstream got %d bytes, want %d", got, len(big))
	}

	f := waitFlow(t, s)
	if f.RequestBodySize != len(big) || f.ResponseBodySize != len(big) {
		t.Fatalf("sizes = %d, %d; want %d", f.RequestBodySize, f.ResponseBodySize, len(big))
	}
	if len(f.RequestBody) != maxBody || len(f.ResponseBody) != maxBody {
		t.Fatalf("inline bodies = %d, %d; want the %d-byte head", len(f.RequestBody), len(f.ResponseBody), maxBody)
	}
	for _, path := range []string{f.RequestBodyFile, f.ResponseBodyFile} {
		if path == "" {
			t.Fatalf("body was not spilled: %+v", f)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read spilled body: %v", err)
		}
		if len(data) != len(big) {
			t.Fatalf("spilled body = %d bytes, want %d", len(data), len(big))
		}
	}

	// A replay must send the whole body, not the head the flow kept.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.Replay(ctx, f); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got := <-sizes; got != len(big) {
		t.Fatalf("replayed body = %d bytes, want %d", got, len(big))
	}

	dir := filepath.Dir(f.ResponseBodyFile)
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("spill directory survived close: %v", err)
	}
}

func TestFlowNamesClientProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process attribution reads /proc, which only exists on Linux")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/who")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()

	f := waitFlow(t, s)
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable path: %v", err)
	}
	if want := filepath.Base(exe); f.Process != want {
		t.Fatalf("process = %q, want %q", f.Process, want)
	}
}

func TestCapturesHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "yes")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/hello?x=1")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q", body)
	}

	f := waitFlow(t, s)
	if f.Method != "GET" || f.Status != 200 || f.Path != "/hello?x=1" {
		t.Fatalf("flow = %+v", f)
	}
	if !strings.Contains(f.ResponseHeaders, "X-Upstream: yes") {
		t.Fatalf("response headers missing upstream header: %q", f.ResponseHeaders)
	}
	if !strings.Contains(f.ResponseBody, `"ok":true`) {
		t.Fatalf("response body = %q", f.ResponseBody)
	}
	if f.Millis < 0 {
		t.Fatalf("negative duration: %d", f.Millis)
	}
}

func TestCapturesHTTPS(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"secure":true}`)
	}))
	defer upstream.Close()

	s, caPath := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read proxy CA: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("proxy CA is not PEM")
	}

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	u.Host = "localhost:" + u.Port()
	resp, err := clientFor(t, s, roots).Get(u.String() + "/secure")
	if err != nil {
		t.Fatalf("https through proxy: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `{"secure":true}` {
		t.Fatalf("body = %q", body)
	}

	f := waitFlow(t, s)
	if f.Status != 200 || !strings.HasPrefix(f.Host, "localhost:") {
		t.Fatalf("flow = %+v", f)
	}
	if !strings.Contains(f.ResponseBody, `"secure":true`) {
		t.Fatalf("decrypted body = %q", f.ResponseBody)
	}
}

func startServerWithRules(t *testing.T, rules ...Rule) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(freeAddr(t), dir)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	s.SetRules(rules)
	if err := s.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, filepath.Join(dir, "mitmproxy-ca-cert.pem")
}

func TestMapRemote(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "first")
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "second")
	}))
	defer second.Close()

	s, _ := startServerWithRules(t, Rule{Type: RuleMapRemote, Match: first.URL, Target: second.URL})
	resp, err := clientFor(t, s, nil).Get(first.URL + "/path?q=1")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "second" {
		t.Fatalf("body = %q, want the mapped upstream", body)
	}
	f := waitFlow(t, s)
	if !strings.HasPrefix(f.URL, first.URL) {
		t.Fatalf("flow URL = %q, want the URL the client asked for", f.URL)
	}
}

func TestMapLocal(t *testing.T) {
	called := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		fmt.Fprint(w, "upstream")
	}))
	defer upstream.Close()

	file := filepath.Join(t.TempDir(), "mock.json")
	if err := os.WriteFile(file, []byte(`{"mocked":true}`), 0o600); err != nil {
		t.Fatalf("write mock: %v", err)
	}
	s, _ := startServerWithRules(t, Rule{Type: RuleMapLocal, Match: "/mocked", Target: file})
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/mocked")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != `{"mocked":true}` {
		t.Fatalf("body = %q", body)
	}
	if called != 0 {
		t.Fatalf("upstream was called %d times, want 0", called)
	}
	f := waitFlow(t, s)
	if f.Status != 200 {
		t.Fatalf("flow status = %d", f.Status)
	}
}

func TestBlock(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "upstream")
	}))
	defer upstream.Close()

	s, _ := startServerWithRules(t, Rule{Type: RuleBlock, Match: "/blocked"})
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/blocked")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestBlockStillAppliesToGRPC(t *testing.T) {
	s, conn := startGRPCThroughProxy(t)
	s.SetRules([]Rule{{Type: RuleBlock, Match: grpcMethod("Echo")}})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := conn.Invoke(ctx, grpcMethod("Echo"), wrapperspb.String("blocked"), new(wrapperspb.StringValue)); err == nil {
		t.Fatal("a blocked gRPC call reached the upstream")
	}
	f := waitFlowFor(t, s, "/Echo")
	if f.Status != http.StatusForbidden || f.ResponseBody != "blocked by vscroxy\n" || f.ResponseHeader.Get("X-Server-Header") != "" {
		t.Fatalf("gRPC block response: status %d, body %q, headers %v", f.Status, f.ResponseBody, f.ResponseHeader)
	}
}

func TestReplay(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprintf(w, `{"call":%d}`, calls)
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/replay")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()
	first := waitFlow(t, s)

	reply, err := s.Replay(t.Context(), first)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if reply.Status != 200 || reply.ResponseBody != `{"call":2}` || reply.Bytes != 10 {
		t.Fatalf("send returned the wrong response: %+v", reply)
	}
	second := waitFlow(t, s)
	if second.URL != first.URL || second.Method != first.Method || second.ReplayOf != first.ID || first.ReplayOf != 0 {
		t.Fatalf("replayed %s %s, want %s %s", second.Method, second.URL, first.Method, first.URL)
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls)
	}
}

func TestConcurrentReplayLineageSurvivesHTTPSRedirects(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/finish", http.StatusFound)
			return
		}
		fmt.Fprint(w, r.Header.Get("X-Test-Case"))
	}))
	defer upstream.Close()
	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	var wg sync.WaitGroup
	for _, source := range []int{17, 41} {
		wg.Go(func() {
			r, err := s.Replay(t.Context(), Flow{ID: source, Method: "GET", URL: upstream.URL + "/start", RequestHeader: http.Header{"X-Test-Case": {strconv.Itoa(source)}}})
			if err != nil || r.ResponseBody != strconv.Itoa(source) {
				t.Errorf("source %d got %+v: %v", source, r, err)
			}
		})
	}
	wg.Go(func() {
		client := clientFor(t, s, s.roots)
		defer client.CloseIdleConnections()
		r, err := client.Get(upstream.URL + "/finish")
		if err != nil {
			t.Error(err)
			return
		}
		defer r.Body.Close()
		io.Copy(io.Discard, r.Body)
	})
	wg.Wait()
	seen := map[int]int{}
	for range 5 {
		f := waitFlow(t, s)
		want, _ := strconv.Atoi(f.RequestHeader.Get("X-Test-Case"))
		if f.ReplayOf != want {
			t.Fatalf("concurrent replay/redirect mixed its source: %+v", f)
		}
		seen[f.ReplayOf]++
	}
	if seen[17] != 2 || seen[41] != 2 || seen[0] != 1 {
		t.Fatalf("redirect lineage = %v", seen)
	}
}

func TestBreakpointEditsRequest(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "first")
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "second|%s|%s|%s|%s|%s|%s", r.Method, r.Host, r.Header.Get("Authorization"), r.Header.Get("Cookie"), strings.Join(r.Header.Values("X-Multi"), ","), body)
	}))
	defer second.Close()

	s, _ := startServerWithRules(t, Rule{Type: RuleBreak, Match: first.URL})
	s.SetBreak(true)
	client := clientFor(t, s, nil)

	type result struct {
		body string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := client.Get(first.URL + "/bp")
		if err != nil {
			done <- result{err: err}
			return
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		done <- result{body: string(body), err: err}
	}()

	var bp Breakpoint
	select {
	case bp = <-s.Breakpoints():
	case <-time.After(5 * time.Second):
		t.Fatal("no breakpoint")
	}
	if bp.Method != "GET" || !strings.HasPrefix(bp.URL, first.URL) {
		t.Fatalf("breakpoint = %+v", bp)
	}
	if !s.Resume(bp.ID, Decision{Method: "POST", URL: second.URL + "/bp", Body: "edited payload", Header: http.Header{"Host": {"virtual.test"}, "Authorization": {"Bearer changed"}, "Cookie": {"session=new"}, "X-Multi": {"one", "two"}}}) {
		t.Fatal("resume found no waiter")
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("request: %v", r.err)
	}
	if r.body != "second|POST|virtual.test|Bearer changed|session=new|one,two|edited payload" {
		t.Fatalf("body = %q, want the edited URL's upstream", r.body)
	}
	f := waitFlow(t, s)
	if f.Conn.Error != "" || !strings.Contains(f.Conn.Log, "separate upstream transport") || !strings.Contains(f.Conn.Log, strings.TrimPrefix(second.URL, "http://")) {
		t.Fatalf("rewritten URL's real transport missing: %+v", f.Conn)
	}
}

func TestBreakpointAborts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "upstream")
	}))
	defer upstream.Close()

	s, _ := startServerWithRules(t, Rule{Type: RuleBreak, Match: upstream.URL})
	s.SetBreak(true)
	client := clientFor(t, s, nil)

	status := make(chan int, 1)
	go func() {
		resp, err := client.Get(upstream.URL + "/abort")
		if err != nil {
			status <- 0
			return
		}
		_ = resp.Body.Close()
		status <- resp.StatusCode
	}()

	select {
	case bp := <-s.Breakpoints():
		s.Resume(bp.ID, Decision{Abort: true})
	case <-time.After(5 * time.Second):
		t.Fatal("no breakpoint")
	}
	if got := <-status; got != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", got)
	}
}

func TestBreakpointDurationExcludesPause(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	s, _ := startServerWithRules(t, Rule{Type: RuleBreak, Match: upstream.URL})
	s.SetBreak(true)
	client := clientFor(t, s, nil)

	go func() {
		resp, err := client.Get(upstream.URL + "/slow-human")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case bp := <-s.Breakpoints():
		time.Sleep(500 * time.Millisecond)
		s.Resume(bp.ID, Decision{Method: "GET", URL: bp.URL})
	case <-time.After(5 * time.Second):
		t.Fatal("no breakpoint")
	}

	f := waitFlow(t, s)
	if f.Millis >= 400 {
		t.Fatalf("flow took %d ms: the pause is counted as network time", f.Millis)
	}
}

func startServerWithScript(t *testing.T, source string) *Server {
	t.Helper()
	s, _ := startServer(t)
	path := filepath.Join(t.TempDir(), "script.js")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	sc, err := LoadScript(path)
	if err != nil {
		t.Fatalf("load script: %v", err)
	}
	s.SetScript(sc)
	return s
}

func TestScriptRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-From-Script") != "yes" {
			t.Errorf("script header missing: %v", r.Header)
		}
		if r.URL.Path != "/new" {
			t.Errorf("path = %q, want /new", r.URL.Path)
		}
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	s := startServerWithScript(t, `
function onRequest(req) {
  req.headers["X-From-Script"] = "yes"
  req.url = req.url.replace("/old", "/new")
}`)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/old")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	f := waitFlow(t, s)
	if !strings.HasSuffix(f.URL, "/new") {
		t.Fatalf("flow URL = %q, want the scripted URL", f.URL)
	}
}

func TestScriptResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "original")
	}))
	defer upstream.Close()

	s := startServerWithScript(t, `
function onResponse(res) {
  res.status = 201
  res.body = JSON.stringify({scripted: true})
  res.headers["X-Script"] = "1"
}`)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/boom")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 201 || string(body) != `{"scripted":true}` || resp.Header.Get("X-Script") != "1" {
		t.Fatalf("client got %d %q %q", resp.StatusCode, body, resp.Header.Get("X-Script"))
	}

	f := waitFlow(t, s)
	if f.Status != 201 || !strings.Contains(f.ResponseBody, "scripted") {
		t.Fatalf("flow = %d %q", f.Status, f.ResponseBody)
	}
}

func TestScriptTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	s := startServerWithScript(t, `
function onRequest(req) { while (true) {} }`)
	start := time.Now()
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/loop")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("script held the request for %v", elapsed)
	}
	if s.ScriptErrors() == 0 {
		t.Fatal("the interrupted script was not counted")
	}
}

func TestResponseBreakpointEdits(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "old=1")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "original")
	}))
	defer upstream.Close()

	s, _ := startServerWithRules(t, Rule{Type: RuleBreakResponse, Match: upstream.URL})
	s.SetBreak(true)
	client := clientFor(t, s, nil)

	type result struct {
		status int
		body   string
		err    error
		header http.Header
	}
	done := make(chan result, 1)
	go func() {
		resp, err := client.Get(upstream.URL + "/edit")
		if err != nil {
			done <- result{err: err}
			return
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		done <- result{status: resp.StatusCode, body: string(body), err: err, header: resp.Header}
	}()

	var bp Breakpoint
	select {
	case bp = <-s.Breakpoints():
	case <-time.After(5 * time.Second):
		t.Fatal("no response breakpoint")
	}
	if bp.Phase != PhaseResponse || bp.Status != http.StatusInternalServerError || bp.Body != "original" {
		t.Fatalf("breakpoint = %+v", bp)
	}
	if bp.Header.Get("Set-Cookie") != "old=1" {
		t.Fatalf("missing response headers: %+v", bp)
	}
	if !s.Resume(bp.ID, Decision{Status: 201, Body: `{"edited":true}`, Header: http.Header{"Set-Cookie": {"session=new; HttpOnly", "locale=zh"}, "Content-Type": {"application/json"}}}) {
		t.Fatal("resume found no waiter")
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("request: %v", r.err)
	}
	if r.status != 201 || r.body != `{"edited":true}` {
		t.Fatalf("client got %d %q", r.status, r.body)
	}
	if got := strings.Join(r.header.Values("Set-Cookie"), "|"); got != "session=new; HttpOnly|locale=zh" {
		t.Fatalf("Set-Cookie = %q", got)
	}

	f := waitFlow(t, s)
	if f.Status != 201 || !strings.Contains(f.ResponseBody, "edited") {
		t.Fatalf("flow = %d %q", f.Status, f.ResponseBody)
	}
}

func TestResponseBreakpointAborts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "original")
	}))
	defer upstream.Close()

	s, _ := startServerWithRules(t, Rule{Type: RuleBreakResponse, Match: upstream.URL})
	s.SetBreak(true)
	client := clientFor(t, s, nil)

	status := make(chan int, 1)
	go func() {
		resp, err := client.Get(upstream.URL + "/abort")
		if err != nil {
			status <- 0
			return
		}
		_ = resp.Body.Close()
		status <- resp.StatusCode
	}()

	select {
	case bp := <-s.Breakpoints():
		s.Resume(bp.ID, Decision{Abort: true})
	case <-time.After(5 * time.Second):
		t.Fatal("no response breakpoint")
	}
	if got := <-status; got != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", got)
	}
}

func TestBreakpointOriginalPreservesWireMessage(t *testing.T) {
	var wire bytes.Buffer
	gz := gzip.NewWriter(&wire)
	gz.Write([]byte("payload\x00\xff"))
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		phase    RuleType
		original bool
	}{{RuleBreak, true}, {RuleBreakResponse, true}, {RuleBreak, false}, {RuleBreakResponse, false}} {
		t.Run(fmt.Sprintf("%s/original=%t", tc.phase, tc.original), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if !bytes.Equal(body, wire.Bytes()) || r.Header.Get("Content-Encoding") != "gzip" || r.Header.Get("Authorization") != "Bearer original" || r.Header.Get("X-Leak") != "" {
					t.Errorf("request was changed: %q, %v", body, r.Header)
				}
				if tc.phase == RuleBreak && !tc.original && r.Header.Get("X-Edited") != "yes" {
					t.Error("request header-only edit did not reach upstream")
				}
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Add("Set-Cookie", "a=1")
				w.Header().Add("Set-Cookie", "b=2")
				w.WriteHeader(201)
				w.Write(wire.Bytes())
			}))
			defer upstream.Close()
			s, _ := startServerWithRules(t, Rule{Type: tc.phase, Match: upstream.URL})
			s.SetBreak(true)
			client := clientFor(t, s, nil)
			client.Transport.(*http.Transport).DisableCompression = true
			done := make(chan error, 1)
			go func() {
				req, _ := http.NewRequestWithContext(t.Context(), "POST", upstream.URL, bytes.NewReader(wire.Bytes()))
				req.Header.Set("Content-Encoding", "gzip")
				req.Header.Set("Authorization", "Bearer original")
				resp, err := client.Do(req)
				if err != nil {
					done <- err
					return
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err == nil && (resp.StatusCode != 201 || !bytes.Equal(body, wire.Bytes()) || resp.Header.Get("Content-Encoding") != "gzip" || len(resp.Header.Values("Set-Cookie")) != 2) {
					err = fmt.Errorf("response was changed: %d %q %v", resp.StatusCode, body, resp.Header)
				}
				if tc.phase == RuleBreakResponse && !tc.original && resp.Header.Get("X-Edited") != "yes" {
					err = fmt.Errorf("response header-only edit was lost")
				}
				done <- err
			}()
			select {
			case bp := <-s.Breakpoints():
				if !bp.BodyReadOnly {
					t.Fatal("binary body offered as editable text")
				}
				header := bp.Header.Clone()
				bp.Header.Set("X-Leak", "must not mutate the live exchange")
				d := Decision{Original: true, Method: "DELETE", URL: "http://wrong.invalid/", Status: 503, Header: http.Header{}, Body: "discard me"}
				if !tc.original {
					header.Set("X-Edited", "yes")
					d = Decision{Header: header, KeepBody: true}
				}
				s.Resume(bp.ID, d)
			case <-t.Context().Done():
				t.Fatal("no breakpoint")
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBreakpointAbortStatuses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "upstream") }))
	defer upstream.Close()
	for _, phase := range []RuleType{RuleBreak, RuleBreakResponse} {
		for _, status := range []int{429, 503} {
			t.Run(fmt.Sprintf("%s/%d", phase, status), func(t *testing.T) {
				s, _ := startServerWithRules(t, Rule{Type: phase, Match: upstream.URL})
				s.SetBreak(true)
				client := clientFor(t, s, nil)
				done := make(chan int, 1)
				go func() {
					resp, err := client.Get(upstream.URL)
					if err != nil {
						done <- 0
						return
					}
					resp.Body.Close()
					done <- resp.StatusCode
				}()
				select {
				case bp := <-s.Breakpoints():
					s.Resume(bp.ID, Decision{Abort: true, AbortStatus: status})
				case <-t.Context().Done():
					t.Fatal("no breakpoint")
				}
				if got := <-done; got != status {
					t.Fatalf("got %d, want %d", got, status)
				}
			})
		}
	}
}

func TestWebSocketFrames(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.WriteMessage(kind, append([]byte("echo:"), msg...))
		}
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	proxyURL, err := url.Parse("http://" + s.Addr())
	if err != nil {
		t.Fatalf("parse proxy: %v", err)
	}
	dialer := websocket.Dialer{Proxy: http.ProxyURL(proxyURL)}
	conn, _, err := dialer.Dial("ws://"+u.Host+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(msg) != "echo:hello" {
		t.Fatalf("message = %q", msg)
	}
	_ = conn.Close()

	f := waitFlow(t, s)
	if f.Kind != "ws" || f.Status != http.StatusSwitchingProtocols {
		t.Fatalf("flow = kind %q status %d", f.Kind, f.Status)
	}
	out := waitFrame(t, s)
	if out.FlowID != f.ID || out.Frame.Direction != "out" || out.Frame.Data != "hello" {
		t.Fatalf("first frame = %+v", out)
	}
	in := waitFrame(t, s)
	if in.Frame.Direction != "in" || !strings.Contains(in.Frame.Data, "echo:hello") {
		t.Fatalf("second frame = %+v", in)
	}
}

func TestSSEEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "id: 7\nevent: tick\ndata: one\n\n")
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "data: two\n\n")
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/events")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "one") || !strings.Contains(string(body), "two") {
		t.Fatalf("body = %q", body)
	}

	f := waitFlow(t, s)
	if f.Kind != "sse" {
		t.Fatalf("flow = kind %q", f.Kind)
	}
	first := waitFrame(t, s)
	if first.FlowID != f.ID || first.Frame.Data != "one" || first.Frame.Kind != "event" {
		t.Fatalf("first event = %+v, want an SSE event", first)
	}
	if first.Frame.Seq != 1 || first.Frame.Direction != "in" {
		t.Errorf("first event = %+v, want the first the server sent", first.Frame)
	}
	// The engine parses the event's own name and id, and the frame keeps them:
	// the kind says what the message is, not what the server called it.
	if first.Frame.EventName != "tick" || first.Frame.EventID != "7" {
		t.Errorf("first event = name %q id %q, want tick and 7", first.Frame.EventName, first.Frame.EventID)
	}
	if first.Frame.Size != 3 || first.Frame.Kept != 3 || first.Frame.File != "" {
		t.Errorf("first event = size %d kept %d file %q, want the three bytes inline", first.Frame.Size, first.Frame.Kept, first.Frame.File)
	}
	second := waitFrame(t, s)
	if second.Frame.Data != "two" {
		t.Fatalf("second event = %+v", second)
	}
	if second.Frame.EventName != "message" {
		t.Errorf("second event = name %q, want the engine's default name", second.Frame.EventName)
	}
	if second.Frame.Seq != 2 {
		t.Errorf("second event = seq %d, want 2", second.Frame.Seq)
	}
}

// wsEchoServer answers every message with the same bytes, so a test can send
// one and see both frames the proxy captured.
func wsEchoServer(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var seen string
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.RequestURI()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(kind, msg); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, &seen
}

// dialWS opens a WebSocket through the proxy, as a client library does: a
// CONNECT tunnel the proxy decrypts when the host is decrypted, then the
// handshake inside it.
func dialWS(t *testing.T, s *Server, target string) *websocket.Conn {
	t.Helper()
	proxyURL, err := url.Parse("http://" + s.Addr())
	if err != nil {
		t.Fatalf("parse proxy: %v", err)
	}
	conn, _, err := (&websocket.Dialer{Proxy: http.ProxyURL(proxyURL)}).Dial(target, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", target, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// dialWSS opens a wss:// WebSocket through the proxy the way a browser does:
// CONNECT, TLS the proxy decrypts with its own certificate, then the handshake
// on that connection. Each step waits for the one before it, so no hello races
// the response to the CONNECT.
func dialWSS(t *testing.T, s *Server, caPath, target, path string) *websocket.Conn {
	t.Helper()
	host, port, ok := strings.Cut(target, ":")
	if !ok {
		t.Fatalf("target %q has no port", target)
	}
	raw, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	resp, err := http.ReadResponse(bufio.NewReader(raw), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("connect = %s", resp.Status)
	}
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read proxy CA: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("proxy CA is not PEM")
	}
	conn := tls.Client(raw, &tls.Config{RootCAs: roots, ServerName: host})
	if err := conn.HandshakeContext(t.Context()); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	dialer := websocket.Dialer{
		// The TLS handshake is done: gorilla speaks the WebSocket handshake
		// on this connection as it is.
		NetDialTLSContext: func(context.Context, string, string) (net.Conn, error) { return conn, nil },
	}
	ws, _, err := dialer.Dial("wss://"+net.JoinHostPort(host, port)+path, nil)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

// TestWebSocketKeepsLongMessagesWhole covers a message past the inline
// preview: the frame says how long it was, keeps its head in memory and the
// whole of it in the spill file, so the UI can show and save all of it.
func TestWebSocketKeepsLongMessagesWhole(t *testing.T) {
	upstream, _ := wsEchoServer(t)
	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	conn := dialWS(t, s, "ws"+strings.TrimPrefix(upstream.URL, "http")+"/stream")

	long := strings.Repeat("0123456789abcdef", 2560) // 40 KiB
	if err := conn.WriteMessage(websocket.TextMessage, []byte(long)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, msg, err := conn.ReadMessage(); err != nil || string(msg) != long {
		t.Fatalf("echo = %d bytes, %v; want the whole message", len(msg), err)
	}
	flow := waitFlow(t, s)
	if flow.Kind != "ws" {
		t.Fatalf("flow = kind %q", flow.Kind)
	}
	sent := waitFrame(t, s)
	if sent.FlowID != flow.ID || sent.Frame.Direction != "out" || sent.Frame.Kind != "text" {
		t.Fatalf("frame = %+v", sent)
	}
	frame := sent.Frame
	if frame.Seq != 1 {
		t.Errorf("seq = %d, want the first message numbered 1", frame.Seq)
	}
	if frame.Size != len(long) || frame.Kept != len(long) {
		t.Errorf("size = %d, kept = %d, want the whole %d", frame.Size, frame.Kept, len(long))
	}
	if len(frame.Data) != maxFrameBytes || !strings.HasPrefix(long, frame.Data) {
		t.Errorf("data = %d bytes, want the head of the message inline", len(frame.Data))
	}
	if frame.File == "" {
		t.Fatal("the message was not spilled")
	}
	spilled, err := os.ReadFile(frame.File)
	if err != nil {
		t.Fatalf("read spill: %v", err)
	}
	if string(spilled) != long {
		t.Errorf("spill = %d bytes, want the whole %d", len(spilled), len(long))
	}
	echo := waitFrame(t, s)
	if echo.Frame.Seq != 2 || echo.Frame.Direction != "in" || echo.Frame.Data != long[:maxFrameBytes] {
		t.Errorf("echo = %+v, want the message back", echo.Frame)
	}
}

// The patched engine exposes the real upgrade request, including escaped path
// and query, rather than the CONNECT envelope.
func TestWebSocketKeepsThePathAndQuery(t *testing.T) {
	upstream, seen := wsEchoServer(t)
	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	target := "ws" + strings.TrimPrefix(upstream.URL, "http")
	conn := dialWS(t, s, target+"/deep/path%2Fseg?q=%2F")
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read: %v", err)
	}
	flow := waitFlow(t, s)
	if want := target + "/deep/path%2Fseg?q=%2F"; flow.URL != want {
		t.Errorf("url = %q, want %q", flow.URL, want)
	}
	if flow.Scheme != "ws" || flow.Path != "/deep/path%2Fseg?q=%2F" {
		t.Errorf("flow = scheme %q path %q, want the actual upgrade URL", flow.Scheme, flow.Path)
	}
	if *seen != "/deep/path%2Fseg?q=%2F" {
		t.Errorf("upstream saw %q, want the path the client asked for", *seen)
	}
	if frame := waitFrame(t, s); frame.Frame.Data != "hi" {
		t.Errorf("frame = %+v, want the message", frame.Frame)
	}
}

// TestWSSKeepsThePathAndQuery covers a decrypted wss:// URL with a path and
// query that do not look like the host: the row shows the request the client
// made, escapes and all, and the frames of that connection are captured.
func TestWSSKeepsThePathAndQuery(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var seen string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.RequestURI()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(kind, msg); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()

	s, caPath := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	const path = "/deep/path%2Fseg?q=%2F&z=a+b"
	conn := dialWSS(t, s, caPath, "localhost:"+u.Port(), path)
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read: %v", err)
	}
	if seen != path {
		t.Errorf("upstream saw %q, want %q", seen, path)
	}
	flow := waitFlow(t, s)
	if want := "wss://localhost:" + u.Port() + path; flow.URL != want {
		t.Errorf("url = %q, want %q", flow.URL, want)
	}
	if flow.Scheme != "wss" || flow.Path != path || flow.Kind != "ws" {
		t.Errorf("flow = scheme %q path %q kind %q", flow.Scheme, flow.Path, flow.Kind)
	}
	if frame := waitFrame(t, s); frame.Frame.Data != "hi" {
		t.Errorf("frame = %+v, want the message", frame.Frame)
	}
}

// TestFrameQueueLossIsVisible covers a stream the UI cannot keep up with: the
// frames a full queue could not take leave gaps in the sequence numbers the UI
// shows, and the server counts them, so a lost message never passes for one
// that never happened.
func TestFrameQueueLossIsVisible(t *testing.T) {
	upstream, _ := wsEchoServer(t)
	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	conn := dialWS(t, s, "ws"+strings.TrimPrefix(upstream.URL, "http")+"/burst")
	roundTrip := func(msg string) {
		t.Helper()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			t.Fatalf("write %q: %v", msg, err)
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatalf("read %q: %v", msg, err)
		}
	}
	roundTrip("first")
	waitFlow(t, s)

	// Nothing reads the frames while the burst runs, so the queue fills and
	// what follows is dropped rather than kept without limit.
	for i := range queue + 200 {
		roundTrip(fmt.Sprintf("message %d", i))
	}
	if dropped := s.FrameDropped(); dropped == 0 {
		t.Fatal("the queue dropped frames without counting them")
	}
	var seqs []int
	for {
		select {
		case ev := <-s.Frames():
			seqs = append(seqs, ev.Frame.Seq)
			continue
		case <-time.After(200 * time.Millisecond):
		}
		break
	}
	if len(seqs) == 0 {
		t.Fatal("no frame was captured")
	}
	if len(seqs) >= 2*(queue+201) {
		t.Fatalf("frames = %d, want the queue to have dropped some", len(seqs))
	}

	// A message after the burst is numbered past what was dropped, so the gap
	// says how many the UI never saw.
	roundTrip("after the burst")
	last := waitFrame(t, s)
	if last.Frame.Data != "after the burst" {
		t.Fatalf("frame = %q, want the message after the burst", last.Frame.Data)
	}
	if last.Frame.Seq <= seqs[len(seqs)-1]+1 {
		t.Errorf("seq = %d after %d, want the numbering to count the dropped ones", last.Frame.Seq, seqs[len(seqs)-1])
	}
	if gaps := last.Frame.Seq - seqs[len(seqs)-1] - 1; gaps < int(s.FrameDropped()) {
		t.Errorf("gap = %d, counted drops = %d, want the gap to show them", gaps, s.FrameDropped())
	}
}

func TestUpstreamProxy(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello")
	}))
	defer upstream.Close()

	inner, _ := startServer(t)
	outer, caPath := startServer(t)
	outer.SetSSL(SSLConfig{All: true})
	if err := outer.SetUpstream("http://" + inner.Addr()); err != nil {
		t.Fatalf("set upstream: %v", err)
	}
	if got := outer.Upstream(); got != "http://"+inner.Addr() {
		t.Fatalf("upstream = %q", got)
	}

	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read CA: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("CA is not PEM")
	}
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	u.Host = "localhost:" + u.Port()
	resp, err := clientFor(t, outer, roots).Get(u.String() + "/chain")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "hello" {
		t.Fatalf("body = %q", body)
	}
	// The inner proxy leaves HTTPS alone, so what it sees is the tunnel the
	// outer proxy opened through it.
	if f := waitFlow(t, inner); f.Kind != "connect" || f.Host == "" {
		t.Fatalf("inner flow = %+v, want the tunnel", f)
	}

	if err := outer.SetUpstream("not a proxy"); err == nil {
		t.Fatal("an address without a scheme was accepted")
	}
	if err := outer.SetUpstream(""); err != nil || outer.Upstream() != "" {
		t.Fatalf("clearing upstream: %v, %q", err, outer.Upstream())
	}
}

func TestSavedRulesAndCompleteCapturedMock(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		fmt.Fprint(w, "real upstream")
	}))
	defer upstream.Close()
	dir := t.TempDir()
	body := bytes.Repeat([]byte{'a', 0, 'z', 255}, 20003)
	spill := filepath.Join(dir, "capture.bin")
	if err := os.WriteFile(spill, body, 0o600); err != nil {
		t.Fatal(err)
	}
	rule, err := NewMockRule(filepath.Join(dir, "mocks"), Flow{
		Method: "GET", URL: upstream.URL + "/asset", Status: 201,
		ResponseBody: string(body[:64]), ResponseBodyFile: spill, ResponseBodySize: len(body),
		ResponseHeader: http.Header{"Content-Type": {"application/custom"}, "Content-Encoding": {"gzip"}, "Content-Length": {"7"}, "Connection": {"X-Hop"}, "X-Hop": {"remove"}, "Set-Cookie": {"a=7", "b=19"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(spill); err != nil {
		t.Fatal(err)
	}
	rules := []Rule{{Type: RuleBlock, Match: "/asset", Disabled: true}, rule, {Type: RuleBlock, Match: "/asset"}}
	path := filepath.Join(dir, "rules.json")
	if err := SaveRules(path, rules); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := startServerWithRules(t, loaded...)
	// The running proxy owns its configuration, not the editor's maps.
	loaded[1].Headers.Set("Content-Type", "wrong")
	snapshot := s.Rules()
	snapshot[1].Headers.Set("Content-Type", "also wrong")
	client := clientFor(t, s, nil)
	check := func(method string, status int, want []byte) {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), method, upstream.URL+"/asset", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != status || !bytes.Equal(got, want) {
			t.Fatalf("%s: status %d body length %d, want %d/%d: %v", method, resp.StatusCode, len(got), status, len(want), err)
		}
		if status == 201 && (resp.Header.Get("Content-Type") != "application/custom" || resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("X-Hop") != "" || len(resp.Cookies()) != 2) {
			t.Fatalf("mock headers = %v", resp.Header)
		}
	}
	check("GET", 201, body)
	check("POST", 403, []byte("blocked by vscroxy\n")) // method-scoped mock must not handle POST
	rules[0].Disabled = false
	s.SetRules(rules)
	check("GET", 403, []byte("blocked by vscroxy\n")) // first enabled mapping/block wins
	rules[0], rules[1] = rules[1], rules[0]
	s.SetRules(rules)
	check("GET", 201, body) // reordering changes the winner
	before, _ := os.ReadFile(path)
	if err := SaveRules(path, []Rule{{Type: RuleMapRemote, Match: "x", Target: "file:///etc/passwd"}}); err == nil {
		t.Fatal("invalid target accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed save changed the existing rules")
	}
}

func TestMockRefusesPartialOrStreamingResponse(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []Flow{
		{Method: "GET", URL: "http://example.test", Status: 200, ResponseBody: "head", ResponseBodySize: 65537},
		{Method: "GET", URL: "http://example.test", Status: 200, ResponseBodyFile: filepath.Join(dir, "gone")},
		{Method: "GET", URL: "http://example.test", Status: 200, Kind: "sse"},
		{Method: "GET", URL: "http://example.test", Status: 200, ResponseBody: strings.Repeat("x", maxRuleBody+1)},
	} {
		if _, err := NewMockRule(filepath.Join(dir, "mocks"), f); err == nil {
			t.Fatalf("incomplete/streaming response accepted: size %d kind %q", f.ResponseBodySize, f.Kind)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "mocks")); !os.IsNotExist(err) {
		t.Fatal("rejected mocks left response files behind")
	}
}
