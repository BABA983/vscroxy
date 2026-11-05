//go:build darwin

package clientsetup

import (
	"os"
	"os/exec"
)

// platformSystemRoots exports the System Roots keychain plus the System
// keychain, where administrators install enterprise roots, as PEM. The fallback
// paths cover a machine whose security tool cannot read those keychains.
func platformSystemRoots() ([]byte, error) {
	var bundle []byte
	for _, keychain := range []string{
		"/System/Library/Keychains/SystemRootCertificates.keychain",
		"/Library/Keychains/System.keychain",
	} {
		out, err := exec.Command("/usr/bin/security", "find-certificate", "-a", "-p", keychain).Output()
		if err == nil {
			bundle = append(bundle, out...)
		}
	}
	if hasCertificate(bundle) {
		return bundle, nil
	}
	for _, path := range []string{"/etc/ssl/cert.pem", "/private/etc/ssl/cert.pem"} {
		data, readErr := os.ReadFile(path)
		if readErr == nil && hasCertificate(data) {
			return data, nil
		}
	}
	return nil, ErrNoSystemRoots
}
