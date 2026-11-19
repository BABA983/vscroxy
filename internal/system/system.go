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

// ErrKDEHelpersMissing means neither kwriteconfig6 nor kwriteconfig5 is
// installed, so the KDE desktop's proxy cannot be changed.
var ErrKDEHelpersMissing = errors.New("KDE proxy configuration tools are not installed")

// ErrKDELocked means an immutable KConfig key or group prevents a KDE proxy
// write. Only an administrator can change such a setting.
var ErrKDELocked = errors.New("KDE proxy settings are locked by an administrator")

// KDELockedError names the setting KConfig refused to write. Group is set when
// the whole [Proxy Settings] group is immutable rather than one key.
type KDELockedError struct {
	Key   string
	Group string
	cause error
}

func (e *KDELockedError) Error() string {
	if e.cause != nil {
		return e.cause.Error()
	}
	if e.Group != "" {
		return fmt.Sprintf("%s: group %q, key %q", ErrKDELocked, e.Group, e.Key)
	}
	return fmt.Sprintf("%s: key %q", ErrKDELocked, e.Key)
}

// Unwrap lets the UI recognise the failure after it crosses the guard pipe.
func (e *KDELockedError) Unwrap() error { return ErrKDELocked }

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

// outputFunc runs a platform helper and returns its standard output, so a
// readback can be tested against fake output instead of the machine.
type outputFunc func(name string, args ...string) ([]byte, error)

// commandOutput reads a helper's standard output.
func commandOutput(name string, args ...string) ([]byte, error) {
	bin, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("%s not found", name)
	}
	out, err := exec.Command(bin, args...).Output()
	if err != nil {
		if msg := trim(string(exitOutput(err))); msg != "" {
			return nil, fmt.Errorf("%s: %s: %w", name, msg, err)
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

// exitOutput is what a failed helper wrote to standard error.
func exitOutput(err error) []byte {
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exit.Stderr
	}
	return nil
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
