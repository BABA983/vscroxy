package capture

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/testcert"
	upstreamcfg "github.com/baba983/vscroxy/internal/upstream"
	"github.com/gorilla/websocket"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// mtlsServer starts a TLS server that requires a client certificate signed by
// ca. The handler sees the verified peer certificate.
func mtlsServer(t *testing.T, ca *testcert.CA, handler http.Handler) *httptest.Server {
	t.Helper()
	_, _, server := ca.Issue(t, "localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, false)
	upstream := httptest.NewUnstartedServer(handler)
	upstream.TLS = &tls.Config{
		Certificates: []tls.Certificate{server},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.Pool(),
	}
	upstream.StartTLS()
	t.Cleanup(upstream.Close)
	return upstream
}

func TestCapturedUpstreamClientCertificate(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	clientCertPEM, clientKeyPEM, _ := ca.Issue(t, "capture-client", nil, nil, true)
	certPath, keyPath := testcert.WritePair(t, dir, "client", clientCertPEM, clientKeyPEM)
	caPath := ca.WritePEM(t, dir)
	settings := certs.Settings{
		Clients: []certs.Client{{ID: "one", Host: "127.0.0.1", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}},
		RootCAs: []string{caPath},
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || r.TLS.PeerCertificates[0].Subject.CommonName != "capture-client" {
			t.Errorf("upstream did not receive the client certificate")
		}
		io.WriteString(w, "mtls ok")
	})
	upstream := mtlsServer(t, ca, handler)

	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	get := func() (*http.Response, error) {
		// A fresh client opens a fresh CONNECT, so a changed certificate
		// configuration is not hidden by a pinned upstream connection.
		return clientFor(t, s, s.caPool).Get(upstream.URL)
	}

	// A configured certificate and root succeed.
	s.SetCertificates(settings)
	resp, err := get()
	if err != nil {
		t.Fatalf("configured mTLS: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "mtls ok" {
		t.Fatalf("configured mTLS: %d %q", resp.StatusCode, body)
	}

	// No certificate fails the upstream handshake. The proxy still finishes
	// the client handshake and answers 502 with the reason.
	rejected := func(what string) {
		t.Helper()
		resp, err := get()
		if err != nil {
			return // the upstream closed the client-certificate handshake
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("%s = %d, want 502 or a failed handshake", what, resp.StatusCode)
		}
	}
	s.SetCertificates(certs.Settings{RootCAs: []string{caPath}})
	rejected("a missing client certificate")

	// A certificate for another host is not presented.
	wrong := settings.Clone()
	wrong.Clients[0].Host = "api.example.com"
	s.SetCertificates(wrong)
	rejected("a certificate for another host")

	// A missing file is a configuration error, never a silent send.
	broken := settings.Clone()
	broken.Clients[0].CertFile = filepath.Join(dir, "missing.pem")
	s.SetCertificates(broken)
	rejected("a missing certificate file")
}

func TestCapturedUpstreamCustomRootsVerify(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	caPath := ca.WritePEM(t, dir)
	upstream := mtlsServer(t, ca, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "verified")
	}))
	clientCertPEM, clientKeyPEM, _ := ca.Issue(t, "verify-client", nil, nil, true)
	certPath, keyPath := testcert.WritePair(t, dir, "verify-client", clientCertPEM, clientKeyPEM)

	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	settings := certs.Settings{
		Clients: []certs.Client{{ID: "one", Host: "127.0.0.1", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}},
	}
	s.SetCertificates(settings)

	// Verify without the custom root fails: the server certificate is private.
	flow := Flow{Method: "GET", URL: upstream.URL}
	if _, err := s.ReplayWithPolicy(t.Context(), flow, nil, TransferPolicy{TLS: TLSVerify}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "certificate") {
		t.Fatalf("a private upstream verified without its root: %v", err)
	}

	// Adding the root makes the same verified send succeed.
	settings.RootCAs = []string{caPath}
	s.SetCertificates(settings)
	got, err := s.ReplayWithPolicy(t.Context(), flow, nil, TransferPolicy{TLS: TLSVerify})
	if err != nil {
		t.Fatalf("verified custom root: %v", err)
	}
	if got.Status != 200 || got.ResponseBody != "verified" {
		t.Fatalf("verified custom root: %d %q", got.Status, got.ResponseBody)
	}

	// A request that turns verification off does not weaken the proxy: the
	// decrypted upstream is verified by default, so the private certificate
	// still fails without its root.
	settings.RootCAs = nil
	s.SetCertificates(settings)
	if _, err := s.ReplayWithPolicy(t.Context(), flow, nil, TransferPolicy{TLS: TLSInsecure}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "certificate") {
		t.Fatalf("a request's verification-off setting bypassed the proxy's default verification: %v", err)
	}

	// The explicit per-domain exception accepts it, while still presenting
	// the client certificate.
	s.SetSSL(SSLConfig{All: true, AllowUntrusted: []string{"127.0.0.1"}})
	if _, err := s.ReplayWithPolicy(t.Context(), flow, nil, TransferPolicy{TLS: TLSInsecure}); err != nil {
		t.Fatalf("allow-untrusted upstream: %v", err)
	}
}

func TestReverseProxyUsesUpstreamClientCertificate(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	clientCertPEM, clientKeyPEM, _ := ca.Issue(t, "reverse-client", nil, nil, true)
	certPath, keyPath := testcert.WritePair(t, dir, "reverse", clientCertPEM, clientKeyPEM)
	upstream := mtlsServer(t, ca, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "reverse mtls")
	}))

	s, _ := startServer(t)
	port := freePort(t)
	if err := s.SetReverseProxies([]ReverseProxy{{ID: "mtls", Port: port, Target: upstream.URL, Enabled: true, Insecure: true}}); err != nil {
		t.Fatal(err)
	}
	addr := reverseAddr(t, s, "mtls")
	if resp, _ := getReverse(t, addr, "/", ""); resp.StatusCode == http.StatusOK {
		t.Fatal("a reverse proxy without a client certificate reached the upstream")
	}

	s.SetCertificates(certs.Settings{Clients: []certs.Client{{ID: "one", Host: "127.0.0.1", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}}})
	resp, body := getReverse(t, addr, "/", "")
	if resp.StatusCode != 200 || body != "reverse mtls" {
		t.Fatalf("reverse mTLS: %d %q", resp.StatusCode, body)
	}
}

func TestCapturedWebSocketPresentsClientCertificate(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	clientCertPEM, clientKeyPEM, _ := ca.Issue(t, "ws-client", nil, nil, true)
	certPath, keyPath := testcert.WritePair(t, dir, "ws-client", clientCertPEM, clientKeyPEM)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := mtlsServer(t, ca, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			t.Error("WebSocket upstream did not receive a client certificate")
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			conn.WriteMessage(kind, data)
		}
	}))
	wsURL := "wss" + strings.TrimPrefix(upstream.URL, "https")

	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	s.SetCertificates(certs.Settings{
		Clients: []certs.Client{{ID: "one", Host: "127.0.0.1", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}},
		RootCAs: []string{ca.WritePEM(t, dir)},
	})
	proxyURL, _ := url.Parse("http://" + s.Addr())
	dial := func() (*websocket.Conn, error) {
		dialer := websocket.Dialer{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: s.caPool}, HandshakeTimeout: 5 * time.Second}
		conn, _, err := dialer.Dial(wsURL, nil)
		return conn, err
	}
	conn, err := dial()
	if err != nil {
		t.Fatalf("configured WebSocket mTLS: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil || string(data) != "hello" {
		t.Fatalf("WebSocket echo: %q %v", data, err)
	}

	// Without the certificate, the upstream handshake fails and the client
	// never gets an upgraded connection.
	s.SetCertificates(certs.Settings{})
	if conn, err := dial(); err == nil {
		conn.Close()
		t.Fatal("a WebSocket without its client certificate was accepted")
	}
}

// connectProxy is an HTTP proxy that only tunnels CONNECT requests, to the
// address routes maps each authority to. Tests use it to give a default-port
// URL (host:443) a real listener on an unprivileged port.
func connectProxy(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		target, ok := routes[r.Host]
		if !ok {
			http.Error(w, "no route for "+r.Host, http.StatusBadGateway)
			return
		}
		upstream, err := net.Dial("tcp", target)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			upstream.Close()
			http.Error(w, "hijacking is unavailable", http.StatusInternalServerError)
			return
		}
		client, _, err := hijacker.Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		defer client.Close()
		defer upstream.Close()
		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		done := make(chan struct{}, 2)
		go func() { io.Copy(upstream, client); done <- struct{}{} }()
		go func() { io.Copy(client, upstream); done <- struct{}{} }()
		<-done
	}))
	t.Cleanup(func() {
		proxy.CloseClientConnections()
		proxy.Close()
	})
	return proxy
}

func TestMapRemoteUsesDefaultPortForClientCertificate(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	clientPEM, clientKey, _ := ca.Issue(t, "mapped-client", nil, nil, true)
	certPath, keyPath := testcert.WritePair(t, dir, "mapped", clientPEM, clientKey)
	upstream := mtlsServer(t, ca, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			t.Error("the rewritten target did not receive a client certificate")
		}
		io.WriteString(w, "mapped")
	}))
	proxy := connectProxy(t, map[string]string{"localhost:443": upstream.Listener.Addr().String()})

	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	if err := s.SetUpstreamConfig(upstreamcfg.Settings{Default: "http://" + proxy.Listener.Addr().String()}, nil); err != nil {
		t.Fatal(err)
	}
	s.SetCertificates(certs.Settings{
		Clients: []certs.Client{{
			ID: "mapped", Host: "localhost:443", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath,
		}},
		RootCAs: []string{ca.WritePEM(t, dir)},
	})
	if err := s.SetRules([]Rule{{Type: RuleMapRemote, Match: "mapped.test", Target: "https://localhost"}}); err != nil {
		t.Fatal(err)
	}
	resp, err := clientFor(t, s, s.caPool).Get("http://mapped.test/x")
	if err != nil {
		t.Fatalf("Map Remote to a default-port HTTPS target: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "mapped" {
		t.Fatalf("Map Remote to a default-port HTTPS target: %d %q", resp.StatusCode, body)
	}
}

func TestWSSUsesDefaultPortForClientCertificate(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	clientPEM, clientKey, _ := ca.Issue(t, "wss-client", nil, nil, true)
	certPath, keyPath := testcert.WritePair(t, dir, "wss", clientPEM, clientKey)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := mtlsServer(t, ca, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			t.Error("the WSS target did not receive a client certificate")
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(kind, data); err != nil {
				return
			}
		}
	}))
	decoy := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(decoy.Close)
	decoyURL, err := url.Parse(decoy.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := connectProxy(t, map[string]string{
		"localhost:" + decoyURL.Port(): decoy.Listener.Addr().String(),
		"localhost:443":                upstream.Listener.Addr().String(),
	})

	s, caPath := startServer(t)
	// The decoy is only the original name a Map Remote rule replaces, but
	// the eager lookup still handshakes it before rules run. Accept its
	// self-signed certificate for that one port; the mapped target below is
	// verified against the test CA.
	s.SetSSL(SSLConfig{All: true, AllowUntrusted: []string{"localhost:" + decoyURL.Port()}})
	if err := s.SetUpstreamConfig(upstreamcfg.Settings{Default: "http://" + proxy.Listener.Addr().String()}, nil); err != nil {
		t.Fatal(err)
	}
	s.SetCertificates(certs.Settings{
		Clients: []certs.Client{{
			ID: "wss", Host: "localhost:443", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath,
		}},
		RootCAs: []string{ca.WritePEM(t, dir)},
	})
	if err := s.SetRules([]Rule{{Type: RuleMapRemote, Match: "localhost:" + decoyURL.Port(), Target: "https://localhost"}}); err != nil {
		t.Fatal(err)
	}
	conn := dialWSS(t, s, caPath, "localhost:"+decoyURL.Port(), "/mapped")
	if err := conn.WriteMessage(websocket.TextMessage, []byte("mapped wss")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil || string(data) != "mapped wss" {
		t.Fatalf("WSS echo: %q %v", data, err)
	}
}

// recordingServer requests a client certificate but does not require one, and
// records whether the client sent one.
func recordingServer(t *testing.T, ca *testcert.CA, seen *atomic.Bool, handler http.Handler) *httptest.Server {
	t.Helper()
	_, _, server := ca.Issue(t, "localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, false)
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			seen.Store(true)
		}
		handler.ServeHTTP(w, r)
	}))
	upstream.TLS = &tls.Config{
		Certificates: []tls.Certificate{server},
		ClientAuth:   tls.RequestClientCert,
		ClientCAs:    ca.Pool(),
	}
	upstream.StartTLS()
	t.Cleanup(upstream.Close)
	return upstream
}

func TestReplayRedirectUsesEachHostsClientCertificate(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	caPath := ca.WritePEM(t, dir)
	clientPEM, clientKey, _ := ca.Issue(t, "a-only-client", nil, nil, true)
	certPath, keyPath := testcert.WritePair(t, dir, "a-only", clientPEM, clientKey)

	t.Run("A to B does not leak A's certificate", func(t *testing.T) {
		var seen atomic.Bool
		b := recordingServer(t, ca, &seen, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "B")
		}))
		bURL := strings.Replace(b.URL, "127.0.0.1", "localhost", 1)
		a := mtlsServer(t, ca, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, bURL, http.StatusFound)
		}))

		s, _ := startServer(t)
		s.SetCertificates(certs.Settings{
			Clients: []certs.Client{{ID: "a", Host: "127.0.0.1", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}},
			RootCAs: []string{caPath},
		})
		got, err := s.ReplayWithPolicy(t.Context(), Flow{Method: "GET", URL: a.URL}, nil, TransferPolicy{TLS: TLSVerify})
		if err != nil {
			t.Fatalf("A to B: %v", err)
		}
		if got.ResponseBody != "B" {
			t.Fatalf("A to B body = %q", got.ResponseBody)
		}
		if seen.Load() {
			t.Fatal("B received A's client certificate")
		}
	})

	t.Run("HTTP to a configured HTTPS host presents its certificate", func(t *testing.T) {
		b := mtlsServer(t, ca, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "B")
		}))
		bURL := strings.Replace(b.URL, "127.0.0.1", "localhost", 1)
		a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, bURL, http.StatusFound)
		}))
		t.Cleanup(a.Close)

		s, _ := startServer(t)
		s.SetCertificates(certs.Settings{
			Clients: []certs.Client{{ID: "b", Host: "localhost", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}},
			RootCAs: []string{caPath},
		})
		got, err := s.ReplayWithPolicy(t.Context(), Flow{Method: "GET", URL: a.URL}, nil, TransferPolicy{TLS: TLSVerify})
		if err != nil {
			t.Fatalf("HTTP to HTTPS: %v", err)
		}
		if got.ResponseBody != "B" {
			t.Fatalf("HTTP to HTTPS body = %q", got.ResponseBody)
		}
	})
}

// An origin cannot impersonate the proxy's marker: the value is a random
// per-proxy token, and a stale incoming header is stripped before forwarding.
func TestReplayIgnoresAnOriginProxyErrorHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(proxy.HeaderProxyError, "1")
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "origin 502")
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	got, err := s.ReplayWithPolicy(t.Context(), Flow{Method: "GET", URL: upstream.URL}, nil, TransferPolicy{})
	if err != nil {
		t.Fatalf("origin 502 became a replay error: %v", err)
	}
	if got.Status != http.StatusBadGateway || got.ResponseBody != "origin 502" {
		t.Fatalf("origin 502 = %d %q", got.Status, got.ResponseBody)
	}
	if got.ResponseHeader.Get(proxy.HeaderProxyError) != "" {
		t.Fatal("the origin's proxy-error header reached the replay response")
	}
}

// A plain decrypted CONNECT fails its upstream certificate check after the
// CONNECT is established. The client handshake still completes with our leaf,
// so curl and browsers receive a 502 that names the host, the verification
// error and the setting that allows it. The per-domain allow list is the
// explicit exception.
func TestPlainDecryptedConnectShowsUpstreamCertificateError(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "verified upstream")
	}))
	defer upstream.Close()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	target := "https://127.0.0.1:" + u.Port() + "/"

	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	get := func() (*http.Response, string) {
		t.Helper()
		client := clientFor(t, s, s.caPool)
		defer client.CloseIdleConnections()
		resp, err := client.Get(target)
		if err != nil {
			t.Fatalf("GET %s: %v", target, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return resp, string(body)
	}

	resp, body := get()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("untrusted upstream = %d %q, want 502", resp.StatusCode, body)
	}
	for _, want := range []string{"127.0.0.1", "certificate", "Settings → HTTPS → Allow untrusted upstream certificates"} {
		if !strings.Contains(body, want) {
			t.Fatalf("502 body = %q, want %q", body, want)
		}
	}
	f := waitFlow(t, s)
	if f.Status != http.StatusBadGateway || !strings.Contains(strings.ToLower(f.Conn.Error), "certificate") {
		t.Fatalf("connection detail = %+v, want the certificate error", f.Conn)
	}
	if !strings.Contains(strings.ToLower(f.Conn.Log), "certificate") {
		t.Fatalf("connection log = %q, want the certificate error", f.Conn.Log)
	}
	if f.ResponseHeader.Get(proxy.HeaderProxyError) != "" {
		t.Fatal("the internal proxy-error header reached the published flow")
	}

	s.SetSSL(SSLConfig{All: true, AllowUntrusted: []string{"127.0.0.1"}})
	if resp, body := get(); resp.StatusCode != http.StatusOK || body != "verified upstream" {
		t.Fatalf("allowed upstream = %d %q", resp.StatusCode, body)
	}
}

// Decrypted upstreams verify by default. A self-signed server fails with a
// 502 whose connection detail names the certificate; the per-domain allow
// list and custom roots are the two explicit ways to trust it.
func TestDecryptedUpstreamVerificationDefault(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "verified upstream")
	}))
	defer upstream.Close()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	ipURL := "https://127.0.0.1:" + u.Port() + "/other"
	nameURL := "https://localhost:" + u.Port() + "/other"

	// A Code Mock rule for a different path makes the CONNECT use the lazy
	// path, so the request reaches the upstream and a verification failure is
	// an HTTP 502 instead of a failed client TLS handshake.
	start := func(t *testing.T, cfg SSLConfig, roots []string) *Server {
		t.Helper()
		s, _ := startServerWithRules(t,
			Rule{Type: RuleCodeMock, Match: "127.0.0.1/only-this", Target: "unused.js"},
			Rule{Type: RuleCodeMock, Match: "localhost/only-this", Target: "unused.js"},
		)
		s.SetSSL(cfg)
		if len(roots) > 0 {
			s.SetCertificates(certs.Settings{RootCAs: roots})
		}
		return s
	}
	get := func(t *testing.T, s *Server, target string) (*http.Response, string) {
		t.Helper()
		client := clientFor(t, s, s.caPool)
		defer client.CloseIdleConnections()
		resp, err := client.Get(target)
		if err != nil {
			t.Fatalf("GET %s: %v", target, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return resp, string(body)
	}
	reject := func(t *testing.T, s *Server, target string) {
		t.Helper()
		resp, body := get(t, s, target)
		if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "vscroxy:") {
			t.Fatalf("untrusted upstream = %d %q, want a 502", resp.StatusCode, body)
		}
		f := waitFlow(t, s)
		if f.Status != http.StatusBadGateway || !strings.Contains(strings.ToLower(f.Conn.Error), "certificate") {
			t.Fatalf("connection detail = %+v, want the certificate error", f.Conn)
		}
		if !strings.Contains(strings.ToLower(f.Conn.Log), "certificate") {
			t.Fatalf("connection log = %q, want the certificate error", f.Conn.Log)
		}
	}

	t.Run("default rejects", func(t *testing.T) {
		reject(t, start(t, SSLConfig{All: true}, nil), ipURL)
	})
	t.Run("allow list accepts", func(t *testing.T) {
		s := start(t, SSLConfig{All: true, AllowUntrusted: []string{"127.0.0.1"}}, nil)
		if resp, body := get(t, s, ipURL); resp.StatusCode != 200 || body != "verified upstream" {
			t.Fatalf("allowed upstream = %d %q", resp.StatusCode, body)
		}
	})
	t.Run("another host does not apply", func(t *testing.T) {
		reject(t, start(t, SSLConfig{All: true, AllowUntrusted: []string{"other.test"}}, nil), ipURL)
	})
	t.Run("question wildcard", func(t *testing.T) {
		s := start(t, SSLConfig{All: true, AllowUntrusted: []string{"local?ost"}}, nil)
		if resp, body := get(t, s, nameURL); resp.StatusCode != 200 || body != "verified upstream" {
			t.Fatalf("wildcard upstream = %d %q", resp.StatusCode, body)
		}
		reject(t, start(t, SSLConfig{All: true, AllowUntrusted: []string{"local?osx"}}, nil), nameURL)
	})
	t.Run("wildcards match only their shape", func(t *testing.T) {
		for _, tc := range []struct {
			pattern, host string
			want          bool
		}{
			{"*.example.test", "api.example.test", true},
			{"*.example.test", "example.test", false},
			{"*.example.test", "a.b.example.test", true},
			{"api?.example.test", "api1.example.test", true},
			{"api?.example.test", "api12.example.test", false},
			{"*.example.test:8443", "api.example.test:8443", true},
			{"*.example.test:8443", "api.example.test:443", false},
			{"localhost:3000", "localhost:3000", true},
			{"localhost:3000", "localhost:8080", false},
		} {
			if got := (SSLConfig{AllowUntrusted: []string{tc.pattern}}).AllowsUntrusted(tc.host); got != tc.want {
				t.Errorf("%q against %q = %v, want %v", tc.pattern, tc.host, got, tc.want)
			}
		}
	})
	t.Run("custom roots accept", func(t *testing.T) {
		rootPath := testcert.WriteFile(t, t.TempDir(), "upstream.pem",
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw}))
		s := start(t, SSLConfig{All: true}, []string{rootPath})
		if resp, body := get(t, s, ipURL); resp.StatusCode != 200 || body != "verified upstream" {
			t.Fatalf("custom-root upstream = %d %q", resp.StatusCode, body)
		}
	})
}

// A custom signing CA replaces the generated one, its leaves carry an IP SAN
// for IP-literal hosts, and removing it restores the generated CA.
func TestCustomSigningCAAndIPSANs(t *testing.T) {
	ca := testcert.NewCA(t)
	keyDER, err := x509.MarshalPKCS8PrivateKey(ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "custom CA")
	}))
	defer upstream.Close()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	target := "https://127.0.0.1:" + u.Port() + "/custom"

	s, _ := startServer(t)
	if err := s.SetSigningCA(ca.PEM, keyPEM); err != nil {
		t.Fatalf("custom signing CA: %v", err)
	}
	if got := s.CACertificate(); !bytes.Equal(got, ca.Cert.Raw) {
		t.Fatal("the custom CA was not published")
	}
	s.SetSSL(SSLConfig{All: true, AllowUntrusted: []string{"127.0.0.1"}})
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	client := clientFor(t, s, roots)
	defer client.CloseIdleConnections()
	resp, err := client.Get(target)
	if err != nil {
		t.Fatalf("a leaf signed by the custom CA: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "custom CA" {
		t.Fatalf("custom CA response = %d %q", resp.StatusCode, body)
	}
	if f := waitFlow(t, s); f.Status != 200 {
		t.Fatalf("custom CA flow = %+v", f)
	}

	// A key that does not match the certificate, and a leaf that is not a CA,
	// are refused without changing the active CA.
	other := testcert.NewCA(t)
	otherDER, _ := x509.MarshalPKCS8PrivateKey(other.Key)
	if err := s.SetSigningCA(ca.PEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: otherDER})); err == nil {
		t.Fatal("a mismatched private key was accepted")
	}
	leaf := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})
	if err := s.SetSigningCA(leaf, keyPEM); err == nil {
		t.Fatal("a leaf certificate was accepted as a signing CA")
	}
	if got := s.CACertificate(); !bytes.Equal(got, ca.Cert.Raw) {
		t.Fatal("a refused CA changed the active one")
	}

	// Removing the custom CA restores the generated one.
	if err := s.SetSigningCA(nil, nil); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(s.CACertificate(), ca.Cert.Raw) {
		t.Fatal("the custom CA was not removed")
	}
	generated := clientFor(t, s, s.caPool)
	defer generated.CloseIdleConnections()
	resp, err = generated.Get(target)
	if err != nil {
		t.Fatalf("a leaf signed by the restored CA: %v", err)
	}
	resp.Body.Close()
}
