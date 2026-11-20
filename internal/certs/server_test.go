package certs

import (
	"crypto/tls"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/testcert"
)

func TestServerCertificateMatchingAndValidation(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	exactPEM, exactKey, _ := ca.Issue(t, "exact", []string{"api.example.com"}, nil, false)
	exactPath, exactKeyPath := testcert.WritePair(t, dir, "exact", exactPEM, exactKey)
	wildPEM, wildKey, _ := ca.Issue(t, "wild", []string{"*.example.com"}, nil, false)
	wildPath, wildKeyPath := testcert.WritePair(t, dir, "wild", wildPEM, wildKey)

	settings := Settings{Servers: []Server{
		{ID: "exact", Host: "api.example.com", Format: FormatPEM, CertFile: exactPath, KeyFile: exactKeyPath},
		{ID: "wild", Host: "*.example.com", Format: FormatPEM, CertFile: wildPath, KeyFile: wildKeyPath},
	}}
	if err := settings.Validate(); err != nil {
		t.Fatalf("valid server certificates: %v", err)
	}
	for _, tc := range []struct{ address, want string }{
		{"api.example.com:443", "exact"},
		{"nested.api.example.com:8443", "wild"},
	} {
		got, err := settings.ServerCertificate(tc.address)
		if err != nil || got == nil || got.Leaf == nil || got.Leaf.Subject.CommonName != tc.want {
			t.Fatalf("ServerCertificate(%q) = %+v, %v; want %q", tc.address, got, err, tc.want)
		}
	}
	if got, err := settings.ServerCertificate("elsewhere.test:443"); err != nil || got != nil {
		t.Fatalf("an unlisted host got a certificate: %+v, %v", got, err)
	}

	wrongSAN := settings.Clone()
	wrongSAN.Servers[0].Host = "other.example.com"
	if err := wrongSAN.Validate(); err == nil || !strings.Contains(err.Error(), "do not cover other.example.com") {
		t.Fatalf("a certificate for the wrong SAN was accepted: %v", err)
	}
	if _, err := wrongSAN.ServerCertificate("other.example.com:443"); err == nil {
		t.Fatal("a certificate for the wrong SAN was presented")
	}

	missing := settings.Clone()
	missing.Servers[0].CertFile = filepath.Join(dir, "gone.pem")
	if _, err := missing.ServerCertificate("api.example.com:443"); err == nil || !strings.Contains(err.Error(), "gone.pem") {
		t.Fatalf("a vanished certificate did not fail with its path: %v", err)
	}
}

func TestServerCertificateRejectsMismatchedKeyAndHidesPassphrase(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	_, _, keyOwner := ca.Issue(t, "key-owner", []string{"api.example.com"}, nil, false)
	_, _, certOwner := ca.Issue(t, "cert-owner", []string{"api.example.com"}, nil, false)
	mixed := tls.Certificate{PrivateKey: keyOwner.PrivateKey, Leaf: certOwner.Leaf}
	pfxPath := testcert.WriteFile(t, dir, "mixed.p12", ca.PFX(t, mixed, "pfx-secret"))

	settings := Settings{
		Servers: []Server{{ID: "server-pfx", Host: "api.example.com", Format: FormatPKCS12, CertFile: pfxPath}},
		Secrets: map[string]string{"server-pfx": "pfx-secret"},
	}
	if err := settings.Validate(); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a mismatched PKCS#12 key was accepted: %v", err)
	}
	wrong := settings.Clone()
	wrong.Secrets["server-pfx"] = "wrong-secret"
	if _, err := wrong.ServerCertificate("api.example.com:443"); err == nil || strings.Contains(err.Error(), "wrong-secret") {
		t.Fatalf("wrong passphrase error = %v", err)
	}
}
