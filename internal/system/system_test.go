package system

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestInstallCARejectsInvalidInputBeforeAuthorization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CA with spaces and 'single' quotes.pem")
	if err := InstallCA(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing CA must be rejected before authorization: %v", err)
	}
	if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallCA(path); err == nil || err.Error() != "no PEM certificate found" {
		t.Fatalf("invalid CA must be rejected before authorization: %v", err)
	}
}

func TestCheckCAReportsIdentityWithoutTrustingAnUnknownRoot(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, expired := range []bool{false, true} {
		cert := &x509.Certificate{
			SerialNumber: big.NewInt(17), Subject: pkix.Name{CommonName: "Uninstalled test CA"},
			IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
			NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		}
		if expired {
			cert.NotAfter = time.Now().Add(-24 * time.Hour)
		}
		der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := CheckCA(path)
		if got == nil || err == nil || !bytes.Equal(got.Raw, der) || got.Subject.CommonName != "Uninstalled test CA" {
			t.Fatalf("untrusted CA must retain its identity without reporting trust: %v, %v", got, err)
		}
		if invalid, ok := errors.AsType[x509.CertificateInvalidError](err); expired && (!ok || invalid.Reason != x509.Expired) {
			t.Fatalf("expired CA must report its validity problem, not just unknown trust: %v", err)
		}
		if expired {
			if invalid, ok := errors.AsType[x509.CertificateInvalidError](InstallCA(path)); !ok || invalid.Reason != x509.Expired {
				t.Fatal("expired CA must not reach the system installer")
			}
		}
		if runtime.GOOS == "linux" && !expired {
			// Use an isolated OpenSSL trust store, never the machine's store.
			// A CA with certSign only is not a TLS server leaf. A fresh check
			// must see both adding and removing its trust in this process.
			anchor := filepath.Join(t.TempDir(), "roots.pem")
			public := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
			if err := os.WriteFile(anchor, public, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SSL_CERT_FILE", anchor)
			t.Setenv("SSL_CERT_DIR", t.TempDir())
			if got, err := CheckCA(path); err != nil || got == nil || !bytes.Equal(got.Raw, der) {
				t.Fatalf("trusted signing CA must verify without leaf key usage: %v", err)
			}
			if err := os.WriteFile(anchor, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := CheckCA(path); err == nil {
				t.Fatal("removing trust must take effect without restarting")
			}
		}
	}
	path := filepath.Join(t.TempDir(), "missing.pem")
	if got, err := CheckCA(path); got != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: %v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := CheckCA(path); got != nil || err == nil {
		t.Fatalf("malformed file must not appear trusted: %v, %v", got, err)
	}
}

func TestSplitAddr(t *testing.T) {
	cases := []struct {
		addr   string
		host   string
		port   string
		wantOk bool
	}{
		{"127.0.0.1:7890", "127.0.0.1", "7890", true},
		{"localhost:8080", "localhost", "8080", true},
		{"127.0.0.1", "", "", false},
		{":7890", "", "", false},
		{"127.0.0.1:", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		host, port, ok := splitAddr(c.addr)
		if host != c.host || port != c.port || ok != c.wantOk {
			t.Errorf("splitAddr(%q) = %q, %q, %v; want %q, %q, %v", c.addr, host, port, ok, c.host, c.port, c.wantOk)
		}
	}
}
