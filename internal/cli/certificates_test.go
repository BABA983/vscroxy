package cli_test

import (
	"bytes"
	"crypto/tls"
	"encoding/json/v2"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/cli"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/testcert"
)

func TestRunUsesDesktopClientCertificates(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	caPath := ca.WritePEM(t, dir)
	_, _, clientCert := ca.Issue(t, "cli-client", nil, nil, true)
	pfxPath := testcert.WriteFile(t, dir, "client.p12", ca.PFX(t, clientCert, "pfx-secret"))

	var hits atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			t.Error("CLI upstream received no client certificate")
			return
		}
		hits.Add(1)
		io.WriteString(w, "ok")
	}))
	_, _, serverCert := ca.Issue(t, "localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, false)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.Pool(),
	}
	server.StartTLS()
	t.Cleanup(server.Close)

	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsPath := filepath.Join(home, ".vscroxy", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	off := false
	settings := map[string]any{"certificates": certs.Settings{
		Clients:          []certs.Client{{ID: "one", Host: "127.0.0.1", Format: certs.FormatPKCS12, CertFile: pfxPath}},
		RootCAs:          []string{caPath},
		TrustSystemRoots: &off,
	}}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := certs.SecretStoreFor(settingsPath).Save(map[string]string{"one": "pfx-secret"}); err != nil {
		t.Fatal(err)
	}

	collection := filepath.Join(dir, "collections", "Shop")
	if err := os.MkdirAll(collection, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSON := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON(filepath.Join(collection, "folder.json"), request.Folder{Path: "Shop"})
	writeJSON(filepath.Join(collection, "Get.request.json"), request.Request{Name: "Get", Collection: "Shop", Method: "GET", URL: server.URL})

	var out, errOut bytes.Buffer
	code := cli.Run(t.Context(), []string{"run", filepath.Join(dir, "collections")}, &out, &errOut)
	if code != 0 || hits.Load() != 1 || !strings.Contains(out.String(), "1 completed · 0 failed") {
		t.Fatalf("exit=%d hits=%d stdout=%s stderr=%s", code, hits.Load(), &out, &errOut)
	}
	if strings.Contains(out.String(), "pfx-secret") || strings.Contains(errOut.String(), "pfx-secret") {
		t.Fatal("CLI output leaked the passphrase")
	}

	// A wrong sealed passphrase is a setup error, reported before any send.
	if err := certs.SecretStoreFor(settingsPath).Save(map[string]string{"one": "wrong-secret"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	code = cli.Run(t.Context(), []string{"run", filepath.Join(dir, "collections")}, &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "Could not load certificate settings") || strings.Contains(errOut.String(), "wrong-secret") {
		t.Fatalf("wrong passphrase: exit=%d stdout=%s stderr=%s", code, &out, &errOut)
	}
}
