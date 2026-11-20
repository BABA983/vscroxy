package capture

import (
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/testcert"
)

// serverCertRoots trusts both the app's MITM CA and the CA that signed a
// configured chain, so a TLS client can tell the two certificates apart by
// subject instead of by a verification failure.
func serverCertRoots(t *testing.T, caPEM []byte, caPath string) *x509.CertPool {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("configured certificate CA is not PEM")
	}
	data, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	if !roots.AppendCertsFromPEM(data) {
		t.Fatal("proxy CA is not PEM")
	}
	return roots
}

// TestConfiguredServerCertificateReachesTLSClient sends three requests through
// one intercepting proxy: an exact host, a wildcard host and an unlisted host.
// The first two must present the configured chain; the last keeps the leaf the
// proxy mints for its own CA.
func TestConfiguredServerCertificateReachesTLSClient(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "upstream")
	}))
	defer upstream.Close()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	port := u.Port()

	s, caPath := startServer(t)
	setHosts(t, s, "127.0.0.1 api.example.test sub.example.test unlisted.test")
	s.SetSSL(SSLConfig{All: true, AllowUntrusted: []string{"api.example.test", "sub.example.test", "unlisted.test"}})

	exactPEM, exactKey, _ := ca.Issue(t, "configured-exact", []string{"api.example.test"}, nil, false)
	exactPath, exactKeyPath := testcert.WritePair(t, dir, "exact", exactPEM, exactKey)
	wildPEM, wildKey, _ := ca.Issue(t, "configured-wild", []string{"*.example.test"}, nil, false)
	wildPath, wildKeyPath := testcert.WritePair(t, dir, "wild", wildPEM, wildKey)
	s.SetCertificates(certs.Settings{Servers: []certs.Server{
		{ID: "exact", Host: "api.example.test:" + port, Format: certs.FormatPEM, CertFile: exactPath, KeyFile: exactKeyPath},
		{ID: "wild", Host: "*.example.test:" + port, Format: certs.FormatPEM, CertFile: wildPath, KeyFile: wildKeyPath},
	}})

	client := clientFor(t, s, serverCertRoots(t, ca.PEM, caPath))
	defer client.CloseIdleConnections()
	for _, tc := range []struct{ host, want string }{
		{"api.example.test", "configured-exact"},
		{"sub.example.test", "configured-wild"},
		{"unlisted.test", "unlisted.test"},
	} {
		resp, err := client.Get("https://" + tc.host + ":" + port + "/show")
		if err != nil {
			t.Fatalf("%s through the proxy: %v", tc.host, err)
		}
		if len(resp.TLS.PeerCertificates) == 0 {
			t.Fatalf("%s: the client saw no server certificate", tc.host)
		}
		got := resp.TLS.PeerCertificates[0].Subject.CommonName
		_, _ = ioCopy(resp)
		_ = resp.Body.Close()
		if got != tc.want {
			t.Errorf("%s: client saw certificate %q, want %q", tc.host, got, tc.want)
		}
	}
}

// ioCopy drains a response so the connection can be reused by the next
// request; the body itself is not part of this test.
func ioCopy(resp *http.Response) (int64, error) {
	return io.Copy(io.Discard, resp.Body)
}

// TestUnreadableServerCertificateFailsTheConnection deletes a file after it
// was configured, as an operator might rotate it away. The handshake must
// fail with the path and reason in the connection detail, never with a
// silently generated leaf.
func TestUnreadableServerCertificateFailsTheConnection(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "upstream")
	}))
	defer upstream.Close()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	port := u.Port()

	s, caPath := startServer(t)
	setHosts(t, s, "127.0.0.1 api.example.test")
	s.SetSSL(SSLConfig{All: true, AllowUntrusted: []string{"api.example.test"}})
	certPEM, keyPEM, _ := ca.Issue(t, "configured-exact", []string{"api.example.test"}, nil, false)
	certPath, keyPath := testcert.WritePair(t, dir, "exact", certPEM, keyPEM)
	s.SetCertificates(certs.Settings{Servers: []certs.Server{
		{ID: "exact", Host: "api.example.test:" + port, Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath},
	}})
	if err := os.Remove(certPath); err != nil {
		t.Fatal(err)
	}

	client := clientFor(t, s, serverCertRoots(t, ca.PEM, caPath))
	defer client.CloseIdleConnections()
	if resp, err := client.Get("https://api.example.test:" + port + "/show"); err == nil {
		_, _ = ioCopy(resp)
		_ = resp.Body.Close()
		t.Fatal("the client completed a handshake without the configured certificate file")
	}
	f := waitFlow(t, s)
	for _, want := range []string{"server certificate for api.example.test", filepath.Base(certPath), "read"} {
		if !strings.Contains(f.Conn.Error, want) {
			t.Errorf("connection error %q does not explain %q", f.Conn.Error, want)
		}
	}
	if !strings.Contains(f.Conn.Log, "Configured server certificate failed to load") {
		t.Errorf("connection detail does not name the failed certificate: %s", f.Conn.Log)
	}
	if f.Conn.ErrorStage != "TLS client" {
		t.Errorf("error stage = %q, want TLS client", f.Conn.ErrorStage)
	}
}
