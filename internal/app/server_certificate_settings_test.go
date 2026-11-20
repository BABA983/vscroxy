package app

import (
	"crypto/tls"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/testcert"
)

func serverCertEditor(t *testing.T, rows ...serverCertificateRow) *app {
	t.Helper()
	return &app{
		settingsPath: filepath.Join(t.TempDir(), "settings.json"),
		certificates: certs.Settings{},
		certificateUI: certificateEditor{
			loaded:     true,
			serverRows: rows,
		},
	}
}

func TestSaveServerCertificateRejectsWrongSANOrKey(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	certPEM, keyPEM, _ := ca.Issue(t, "api", []string{"api.example.com"}, nil, false)
	certPath, keyPath := testcert.WritePair(t, dir, "api", certPEM, keyPEM)
	_, _, otherKey := ca.Issue(t, "other-key", []string{"api.example.com"}, nil, false)
	_, _, otherCert := ca.Issue(t, "other-cert", []string{"api.example.com"}, nil, false)
	mixed := tls.Certificate{PrivateKey: otherKey.PrivateKey, Leaf: otherCert.Leaf}
	mixedPath := testcert.WriteFile(t, dir, "mixed.p12", ca.PFX(t, mixed, "pfx-secret"))

	for _, tc := range []struct {
		name, want string
		row        serverCertificateRow
	}{
		{"SAN", "do not cover other.example.com", serverCertificateRow{
			ID: "wrong-san", Host: "other.example.com", Format: certs.FormatPEM,
			CertFile: certPath, KeyFile: keyPath,
		}},
		{"key", "does not match", serverCertificateRow{
			ID: "wrong-key", Host: "api.example.com", Format: certs.FormatPKCS12,
			CertFile: mixedPath, Passphrase: "pfx-secret",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := serverCertEditor(t, tc.row)
			a.saveCertificates()
			if !strings.Contains(a.certProblem, tc.want) {
				t.Fatalf("save problem = %q, want it to mention %q", a.certProblem, tc.want)
			}
			if len(a.certificates.Servers) != 0 {
				t.Fatalf("a rejected server certificate was saved: %+v", a.certificates.Servers)
			}
		})
	}
}

func TestSaveServerCertificateSealsPassphraseOutsideExports(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	_, _, serverCert := ca.Issue(t, "server", []string{"api.example.com"}, nil, false)
	pfxPath := testcert.WriteFile(t, dir, "server.p12", ca.PFX(t, serverCert, "server-secret"))

	a := serverCertEditor(t, serverCertificateRow{
		ID: "server-one", Host: "api.example.com", Format: certs.FormatPKCS12,
		CertFile: pfxPath, Passphrase: "server-secret",
	})
	a.saveCertificates()
	if a.certProblem != "" {
		t.Fatalf("save problem: %s", a.certProblem)
	}
	if len(a.certificates.Servers) != 1 || a.certificates.Servers[0].Host != "api.example.com" {
		t.Fatalf("saved servers: %+v", a.certificates.Servers)
	}
	secrets, err := certs.SecretStoreFor(a.settingsPath).Load()
	if err != nil || secrets["server-one"] != "server-secret" {
		t.Fatalf("sealed server passphrase = %q, %v", secrets["server-one"], err)
	}
	data, err := os.ReadFile(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "server-secret") {
		t.Fatalf("settings.json contains the server passphrase: %s", data)
	}
	bundle, _ := a.exportConfig()
	data, err = json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "server-secret") {
		t.Fatalf("configuration export contains the server passphrase: %s", data)
	}
}
