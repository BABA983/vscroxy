package clientsetup

import (
	"bytes"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNoSystemRoots means this machine's system CA bundle could not be found.
// Callers then omit the variables that replace a client's trust store and set
// only NODE_EXTRA_CA_CERTS.
var ErrNoSystemRoots = errors.New("no system CA bundle found")

// systemRoots is a variable so tests can provide a fixed bundle.
var systemRoots = platformSystemRoots

// EnsureTrustBundle writes an app-owned PEM bundle with the system roots and
// the public vscroxy CA. The file is 0600 and replaced atomically. It returns
// ErrNoSystemRoots when the platform has no readable system bundle.
func EnsureTrustBundle(caPath, bundlePath string) error {
	if bundlePath == "" {
		return errors.New("trust bundle path is empty")
	}
	roots, err := systemRoots()
	if err != nil {
		return err
	}
	ca, err := os.ReadFile(caPath)
	if err != nil {
		return fmt.Errorf("read CA: %w", err)
	}
	return WriteTrustBundle(roots, ca, bundlePath)
}

// WriteTrustBundle writes system roots followed by ca to path with mode 0600.
// Both inputs must contain at least one CERTIFICATE block.
func WriteTrustBundle(roots, ca []byte, path string) error {
	if !hasCertificate(roots) {
		return ErrNoSystemRoots
	}
	if !hasCertificate(ca) {
		return errors.New("CA file has no certificate")
	}
	var bundle bytes.Buffer
	bundle.Write(bytes.TrimSpace(roots))
	bundle.WriteByte('\n')
	bundle.Write(bytes.TrimSpace(ca))
	bundle.WriteByte('\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".client-ca-bundle-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	_, err = temporary.Write(bundle.Bytes())
	err = errors.Join(err, temporary.Close())
	if err == nil {
		err = os.Chmod(name, 0o600)
	}
	if err == nil {
		err = replaceFile(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write trust bundle: %w", err)
	}
	return nil
}

func hasCertificate(data []byte) bool {
	for {
		block, rest := pem.Decode(data)
		if block == nil {
			return false
		}
		if block.Type == "CERTIFICATE" {
			return true
		}
		data = rest
	}
}
