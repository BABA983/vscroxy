// Package testcert builds throwaway certificate authorities and TLS
// certificates for real-socket tests. It is test support, not application
// code.
package testcert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// CA is a throwaway certificate authority.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	PEM  []byte
}

var serial int64

func nextSerial() *big.Int {
	serial++
	return big.NewInt(serial)
}

// NewCA makes a self-signed CA valid for a day.
func NewCA(t *testing.T) *CA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          nextSerial(),
		Subject:               pkix.Name{CommonName: "vscroxy test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	return &CA{Cert: cert, Key: key, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// Pool is a root pool containing only this CA.
func (ca *CA) Pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	return pool
}

// WritePEM writes the CA certificate and returns its path.
func (ca *CA) WritePEM(t *testing.T, dir string) string {
	t.Helper()
	return WriteFile(t, dir, "ca.pem", ca.PEM)
}

// Issue makes a certificate signed by the CA. Server certificates include the
// given DNS names and IP addresses; client certificates are marked for client
// authentication.
func (ca *CA) Issue(t *testing.T, commonName string, dns []string, ips []net.IP, client bool) (certPEM, keyPEM []byte, cert tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate %s key: %v", commonName, err)
	}
	usage := x509.ExtKeyUsageServerAuth
	if client {
		usage = x509.ExtKeyUsageClientAuth
	}
	template := &x509.Certificate{
		SerialNumber:          nextSerial(),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{usage},
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		t.Fatalf("create %s certificate: %v", commonName, err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encode %s key: %v", commonName, err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	cert, err = tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("parse %s pair: %v", commonName, err)
	}
	return certPEM, keyPEM, cert
}

// PFX encodes a client certificate and its key as a modern PKCS#12 file.
func (ca *CA) PFX(t *testing.T, cert tls.Certificate, password string) []byte {
	t.Helper()
	if cert.Leaf == nil {
		t.Fatalf("certificate has no parsed leaf")
	}
	data, err := pkcs12.Modern.Encode(cert.PrivateKey, cert.Leaf, nil, password)
	if err != nil {
		t.Fatalf("encode PKCS#12: %v", err)
	}
	return data
}

// WriteFile writes data under dir with a private mode and returns its path.
func WriteFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// WritePair writes a certificate and key and returns their paths.
func WritePair(t *testing.T, dir, name string, certPEM, keyPEM []byte) (certPath, keyPath string) {
	t.Helper()
	return WriteFile(t, dir, name+".pem", certPEM), WriteFile(t, dir, name+".key", keyPEM)
}
