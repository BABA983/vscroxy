package request

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/testcert"
	"github.com/gorilla/websocket"
)

// certificateUpstream requires a client certificate signed by ca and answers
// every request with "ok".
func certificateUpstream(t *testing.T, ca *testcert.CA) *httptest.Server {
	t.Helper()
	_, _, server := ca.Issue(t, "localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, false)
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			t.Error("upstream received no client certificate")
		}
		io.WriteString(w, "ok")
	}))
	upstream.TLS = &tls.Config{
		Certificates: []tls.Certificate{server},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.Pool(),
	}
	upstream.StartTLS()
	t.Cleanup(upstream.Close)
	return upstream
}

func TestRequestClientCertificatesAndRoots(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	caPath := ca.WritePEM(t, dir)
	clientCertPEM, clientKeyPEM, clientCert := ca.Issue(t, "request-client", nil, nil, true)
	certPath, keyPath := testcert.WritePair(t, dir, "client", clientCertPEM, clientKeyPEM)
	pfxPath := testcert.WriteFile(t, dir, "client.p12", ca.PFX(t, clientCert, "pfx-secret"))
	upstream := certificateUpstream(t, ca)

	pemClient := certs.Client{ID: "one", Host: "127.0.0.1", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}
	pfxClient := certs.Client{ID: "one", Host: "127.0.0.1", Format: certs.FormatPKCS12, CertFile: pfxPath}
	off := false

	for _, decrypt := range []bool{false, true} {
		t.Run(map[bool]string{false: "tunneled", true: "intercepted"}[decrypt], func(t *testing.T) {
			proxy, err := capture.New("127.0.0.1:0", filepath.Join(t.TempDir(), "ca"))
			if err != nil {
				t.Fatal(err)
			}
			if err := proxy.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { proxy.Close() })
			if decrypt {
				proxy.SetSSL(capture.SSLConfig{All: true})
			}

			for _, tc := range []struct {
				name     string
				settings certs.Settings
				verify   bool
				want     bool
				wantErr  string
			}{
				{"PEM with custom root", certs.Settings{Clients: []certs.Client{pemClient}, RootCAs: []string{caPath}}, true, true, ""},
				{"PKCS#12 with custom root", certs.Settings{Clients: []certs.Client{pfxClient}, RootCAs: []string{caPath}, Secrets: map[string]string{"one": "pfx-secret"}}, true, true, ""},
				{"no client certificate", certs.Settings{RootCAs: []string{caPath}}, true, false, ""},
				{"certificate for another host", certs.Settings{Clients: []certs.Client{{ID: "one", Host: "api.example.com", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}}, RootCAs: []string{caPath}}, true, false, ""},
				{"wrong passphrase", certs.Settings{Clients: []certs.Client{pfxClient}, RootCAs: []string{caPath}, Secrets: map[string]string{"one": "wrong-secret"}}, true, false, "client certificate"},
				{"no custom root", certs.Settings{Clients: []certs.Client{pemClient}}, true, false, ""},
				{"verification off", certs.Settings{Clients: []certs.Client{pemClient}}, false, true, ""},
				{"verification off ignores a broken root", certs.Settings{Clients: []certs.Client{pemClient}, RootCAs: []string{filepath.Join(dir, "missing.pem")}}, false, true, ""},
				{"system roots off with custom root", certs.Settings{Clients: []certs.Client{pemClient}, RootCAs: []string{caPath}, TrustSystemRoots: &off}, true, true, ""},
				{"system roots off without custom root", certs.Settings{Clients: []certs.Client{pemClient}, TrustSystemRoots: &off}, true, false, ""},
				{"verification off still needs the certificate", certs.Settings{RootCAs: []string{caPath}}, false, false, ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					proxy.SetCertificates(tc.settings)
					verify := tc.verify
					got := Request{Method: "GET", URL: upstream.URL, Settings: Settings{VerifyTLS: &verify}}.
						Execute(t.Context(), proxy, Context{Certificates: tc.settings}, nil, nil)
					if (got.Err == nil) != tc.want {
						t.Fatalf("success=%t err=%v, want success=%t", got.Err == nil, got.Err, tc.want)
					}
					if tc.wantErr != "" && (got.Err == nil || !strings.Contains(got.Err.Error(), tc.wantErr)) {
						t.Fatalf("error %v does not contain %q", got.Err, tc.wantErr)
					}
					if tc.want && (got.Response == nil || got.Response.Status != 200 || got.Response.ResponseBody != "ok") {
						t.Fatalf("response %+v", got.Response)
					}
					if tc.name == "wrong passphrase" && got.Err != nil && strings.Contains(got.Err.Error(), "wrong-secret") {
						t.Fatalf("error leaked the passphrase: %v", got.Err)
					}
				})
			}
		})
	}
}

func TestRequestCertificateValidationFailsBeforeTheProxy(t *testing.T) {
	dir := t.TempDir()
	settings := certs.Settings{Clients: []certs.Client{{ID: "one", Host: "api.example.com", Format: certs.FormatPEM, CertFile: filepath.Join(dir, "missing.pem"), KeyFile: filepath.Join(dir, "missing.key")}}}
	verify := true
	got := Request{Method: "GET", URL: "https://api.example.com/", Settings: Settings{VerifyTLS: &verify}}.
		Execute(t.Context(), testProxy(t), Context{Certificates: settings}, nil, nil)
	if got.Err == nil || !strings.Contains(got.Err.Error(), "client certificate") {
		t.Fatalf("error %v does not name the client certificate", got.Err)
	}
}

func TestSocketIOClientCertificate(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	caPath := ca.WritePEM(t, dir)
	clientPEM, clientKey, _ := ca.Issue(t, "socketio-client", nil, nil, true)
	certPath, keyPath := testcert.WritePair(t, dir, "socketio-client", clientPEM, clientKey)

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			t.Error("Socket.IO upstream received no client certificate")
			return
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		ws.WriteMessage(websocket.TextMessage, []byte(`0{"sid":"engine-id","pingInterval":5000,"pingTimeout":5000}`))
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
		ws.WriteMessage(websocket.TextMessage, []byte(`40{"sid":"namespace-id"}`))
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	_, _, serverCert := ca.Issue(t, "localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, false)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.Pool(),
	}
	server.StartTLS()
	t.Cleanup(server.Close)

	proxy := testProxy(t)
	settings := certs.Settings{
		Clients: []certs.Client{{ID: "one", Host: "127.0.0.1", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}},
		RootCAs: []string{caPath},
	}
	conn, err := DialSocketIO(t.Context(), proxy.Addr(), &Prepared{URL: server.URL}, SocketIOConfig{}, proxy.CACertificate(), settings, nil)
	if err != nil {
		t.Fatalf("configured Socket.IO mTLS: %v", err)
	}
	conn.Close()

	if conn, err := DialSocketIO(t.Context(), proxy.Addr(), &Prepared{URL: server.URL}, SocketIOConfig{}, proxy.CACertificate(), certs.Settings{RootCAs: []string{caPath}}, nil); err == nil {
		conn.Close()
		t.Fatal("Socket.IO connected without a client certificate")
	}
}

func TestSSEClientCertificate(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	caPath := ca.WritePEM(t, dir)
	clientPEM, clientKey, _ := ca.Issue(t, "sse-client", nil, nil, true)
	certPath, keyPath := testcert.WritePair(t, dir, "sse-client", clientPEM, clientKey)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			t.Error("SSE upstream received no client certificate")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: hello\n\n")
	}))
	_, _, serverCert := ca.Issue(t, "localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, false)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.Pool(),
	}
	server.StartTLS()
	t.Cleanup(server.Close)

	proxy := testProxy(t)
	settings := certs.Settings{
		Clients: []certs.Client{{ID: "one", Host: "127.0.0.1", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}},
		RootCAs: []string{caPath},
	}
	var events []SSEEvent
	err := ReceiveSSE(t.Context(), proxy.Addr(), &Prepared{Method: "GET", URL: server.URL}, proxy.CACertificate(), settings, nil, false, func(u SSEUpdate) {
		if u.Event != nil {
			events = append(events, *u.Event)
		}
	})
	if err != nil || len(events) != 1 || events[0].Data != "hello" {
		t.Fatalf("configured SSE mTLS: events=%+v err=%v", events, err)
	}

	err = ReceiveSSE(t.Context(), proxy.Addr(), &Prepared{Method: "GET", URL: server.URL}, proxy.CACertificate(), certs.Settings{RootCAs: []string{caPath}}, nil, false, func(SSEUpdate) {})
	if err == nil {
		t.Fatal("SSE connected without a client certificate")
	}
}

func tlsServerWithClientAuth(t *testing.T, ca *testcert.CA, auth tls.ClientAuthType, handler http.Handler) *httptest.Server {
	t.Helper()
	_, _, server := ca.Issue(t, "localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, false)
	upstream := httptest.NewUnstartedServer(handler)
	upstream.TLS = &tls.Config{
		Certificates: []tls.Certificate{server},
		ClientAuth:   auth,
		ClientCAs:    ca.Pool(),
	}
	upstream.StartTLS()
	t.Cleanup(upstream.Close)
	return upstream
}

func sseHandler(data string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: "+data+"\n\n")
	})
}

func TestSSERedirectUsesEachHostsClientCertificate(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	caPath := ca.WritePEM(t, dir)
	clientPEM, clientKey, _ := ca.Issue(t, "sse-a-only", nil, nil, true)
	certPath, keyPath := testcert.WritePair(t, dir, "sse-a-only", clientPEM, clientKey)

	collect := func(t *testing.T, proxy *capture.Server, startURL string, settings certs.Settings) []SSEEvent {
		t.Helper()
		var events []SSEEvent
		err := ReceiveSSE(t.Context(), proxy.Addr(), &Prepared{Method: "GET", URL: startURL}, proxy.CACertificate(), settings, nil, false, func(u SSEUpdate) {
			if u.Event != nil {
				events = append(events, *u.Event)
			}
		})
		if err != nil {
			t.Fatalf("SSE: %v", err)
		}
		return events
	}

	t.Run("A to B does not leak A's certificate", func(t *testing.T) {
		var seen atomic.Bool
		b := tlsServerWithClientAuth(t, ca, tls.RequestClientCert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
				seen.Store(true)
			}
			sseHandler("B").ServeHTTP(w, r)
		}))
		bURL := strings.Replace(b.URL, "127.0.0.1", "localhost", 1)
		a := tlsServerWithClientAuth(t, ca, tls.RequireAndVerifyClientCert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, bURL, http.StatusFound)
		}))
		events := collect(t, testProxy(t), a.URL, certs.Settings{
			Clients: []certs.Client{{ID: "a", Host: "127.0.0.1", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}},
			RootCAs: []string{caPath},
		})
		if len(events) != 1 || events[0].Data != "B" {
			t.Fatalf("events = %+v", events)
		}
		if seen.Load() {
			t.Fatal("SSE sent A's client certificate to B")
		}
	})

	t.Run("HTTP to a configured HTTPS host presents its certificate", func(t *testing.T) {
		b := tlsServerWithClientAuth(t, ca, tls.RequireAndVerifyClientCert, sseHandler("B"))
		bURL := strings.Replace(b.URL, "127.0.0.1", "localhost", 1)
		a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, bURL, http.StatusFound)
		}))
		t.Cleanup(a.Close)
		events := collect(t, testProxy(t), a.URL, certs.Settings{
			Clients: []certs.Client{{ID: "b", Host: "localhost", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}},
			RootCAs: []string{caPath},
		})
		if len(events) != 1 || events[0].Data != "B" {
			t.Fatalf("events = %+v", events)
		}
	})
}

func TestPlainHTTPSendIgnoresBrokenCertificateFiles(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "plain")
	}))
	t.Cleanup(upstream.Close)
	dir := t.TempDir()
	settings := certs.Settings{
		Clients: []certs.Client{{ID: "one", Host: "127.0.0.1", Format: certs.FormatPEM, CertFile: filepath.Join(dir, "missing.pem"), KeyFile: filepath.Join(dir, "missing.key")}},
		RootCAs: []string{filepath.Join(dir, "missing-ca.pem")},
	}
	got := Request{Method: "GET", URL: upstream.URL}.Execute(t.Context(), testProxy(t), Context{Certificates: settings}, nil, nil)
	if got.Err != nil || got.Response == nil || got.Response.Status != 200 || got.Response.ResponseBody != "plain" {
		t.Fatalf("plain HTTP with broken certificate files: %+v", got)
	}
}
