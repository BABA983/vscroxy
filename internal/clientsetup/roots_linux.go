//go:build linux

package clientsetup

import "os"

// platformSystemRoots reads the distribution's CA bundle. The paths cover
// Debian/Ubuntu, Fedora/RHEL, openSUSE, Arch and Alpine.
func platformSystemRoots() ([]byte, error) {
	for _, path := range []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/pki/tls/certs/ca-bundle.crt",
		"/etc/ssl/ca-bundle.pem",
		"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
		"/etc/ssl/cert.pem",
	} {
		data, err := os.ReadFile(path)
		if err == nil && hasCertificate(data) {
			return data, nil
		}
	}
	return nil, ErrNoSystemRoots
}
