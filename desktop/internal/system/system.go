// Package system changes the machine's proxy settings and trust store, which
// a debugging proxy needs before a client will use it.
package system

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrInstallCancelled means the user dismissed a system authorization prompt.
var ErrInstallCancelled = errors.New("certificate installation cancelled")

// CheckCA reads the public CA and checks the current machine's trust store.
// A parsed certificate is returned even when trust verification fails, so its
// identity and validity can still be shown. This says nothing about a remote
// device or an application with its own trust store or certificate pinning.
func CheckCA(path string) (*x509.Certificate, error) {
	cert, err := readCA(path)
	if err != nil {
		return cert, err
	}
	return cert, verifyCA(path, cert)
}

func readCA(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no PEM certificate found")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	if !cert.IsCA {
		return nil, errors.New("certificate is not a CA")
	}
	if now := time.Now(); now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return cert, x509.CertificateInvalidError{Cert: cert, Reason: x509.Expired}
	}
	if !bytes.Equal(cert.RawIssuer, cert.RawSubject) || cert.CheckSignatureFrom(cert) != nil {
		return cert, errors.New("CA must be a self-signed root certificate")
	}
	return cert, nil
}

// snapshotCA writes only validated public certificate bytes, never the original
// file (which could contain a private key or change during authorization).
// The caller removes the file after the system helper finishes.
func snapshotCA(data []byte) (string, error) {
	file, err := os.CreateTemp("", "vscroxy-ca-*.cer")
	if err != nil {
		return "", err
	}
	_, err = file.Write(data)
	if err = errors.Join(err, file.Close()); err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

// splitAddr splits "host:port" as the dialogs give it.
func splitAddr(addr string) (host, port string, ok bool) {
	host, port, ok = strings.Cut(addr, ":")
	if !ok || host == "" || port == "" {
		return "", "", false
	}
	return host, port, true
}

// run executes a platform helper and turns its output into an error message.
func run(name string, args ...string) error {
	bin, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s not found", name)
	}
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		msg := trim(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%s: %w", msg, err)
	}
	return nil
}

func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
