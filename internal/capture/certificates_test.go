package capture

import (
	"crypto/tls"
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
	"github.com/gorilla/websocket"
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

	// No certificate fails the handshake; the proxy reports a failed CONNECT.
	s.SetCertificates(certs.Settings{RootCAs: []string{caPath}})
	if resp, err := get(); err == nil {
		resp.Body.Close()
		t.Fatal("a missing client certificate was accepted")
	}

	// A certificate for another host is not presented.
	wrong := settings.Clone()
	wrong.Clients[0].Host = "api.example.com"
	s.SetCertificates(wrong)
	if resp, err := get(); err == nil {
		resp.Body.Close()
		t.Fatal("a certificate for another host was presented")
	}

	// A missing file is a configuration error, never a silent send.
	broken := settings.Clone()
	broken.Clients[0].CertFile = filepath.Join(dir, "missing.pem")
	s.SetCertificates(broken)
	if resp, err := get(); err == nil {
		resp.Body.Close()
		t.Fatal("a missing certificate file was accepted")
	}
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
	if _, err := s.ReplayWithPolicy(t.Context(), flow, nil, TransferPolicy{TLS: TLSVerify}); err == nil {
		t.Fatal("a private upstream verified without its root")
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

	// Turning verification off accepts the private certificate without the
	// custom root, while still presenting the client certificate.
	settings.RootCAs = nil
	s.SetCertificates(settings)
	if _, err := s.ReplayWithPolicy(t.Context(), flow, nil, TransferPolicy{TLS: TLSInsecure}); err != nil {
		t.Fatalf("verification off: %v", err)
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
	s.SetCertificates(certs.Settings{Clients: []certs.Client{{ID: "one", Host: "127.0.0.1", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}}})
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
	if err := s.SetUpstream("http://" + proxy.Listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	s.SetCertificates(certs.Settings{Clients: []certs.Client{{
		ID: "mapped", Host: "localhost:443", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath,
	}}})
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
	s.SetSSL(SSLConfig{All: true})
	if err := s.SetUpstream("http://" + proxy.Listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	s.SetCertificates(certs.Settings{Clients: []certs.Client{{
		ID: "wss", Host: "localhost:443", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath,
	}}})
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
