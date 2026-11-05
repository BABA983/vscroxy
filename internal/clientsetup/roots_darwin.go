//go:build darwin

package clientsetup

import (
	"os"
	"os/exec"
)

// platformSystemRoots exports the System Roots keychain as PEM. The fallback
// paths cover a machine whose security tool cannot read that keychain.
func platformSystemRoots() ([]byte, error) {
	out, err := exec.Command("/usr/bin/security", "find-certificate", "-a", "-p",
		"/System/Library/Keychains/SystemRootCertificates.keychain").Output()
	if err == nil && hasCertificate(out) {
		return out, nil
	}
	for _, path := range []string{"/etc/ssl/cert.pem", "/private/etc/ssl/cert.pem"} {
		data, readErr := os.ReadFile(path)
		if readErr == nil && hasCertificate(data) {
			return data, nil
		}
	}
	return nil, ErrNoSystemRoots
}
