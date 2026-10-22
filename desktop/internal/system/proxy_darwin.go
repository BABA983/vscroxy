//go:build darwin

package system

import (
	"crypto/x509"
	"fmt"
	"os/exec"
	"strings"
)

// SetProxy points every enabled network service at addr.
func SetProxy(addr string) error {
	host, port, ok := splitAddr(addr)
	if !ok {
		return fmt.Errorf("bad proxy address %q", addr)
	}
	services, err := services()
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return fmt.Errorf("no enabled network services")
	}
	for _, svc := range services {
		for _, args := range [][]string{
			{"-setwebproxy", svc, host, port},
			{"-setsecurewebproxy", svc, host, port},
			{"-setwebproxystate", svc, "on"},
			{"-setsecurewebproxystate", svc, "on"},
		} {
			if err := run("networksetup", args...); err != nil {
				return err
			}
		}
	}
	return nil
}

// ClearProxy turns the proxy off on every enabled network service.
func ClearProxy() error {
	services, err := services()
	if err != nil {
		return err
	}
	for _, svc := range services {
		for _, args := range [][]string{
			{"-setwebproxystate", svc, "off"},
			{"-setsecurewebproxystate", svc, "off"},
		} {
			if err := run("networksetup", args...); err != nil {
				return err
			}
		}
	}
	return nil
}

func verifyCA(path string, _ *x509.Certificate) error {
	// Check CA trust, not TLS leaf policy (which requires a server leaf's EKU).
	// Query the current keychains without network access or adding
	// the certificate as an explicit trust anchor (which would prove nothing).
	return run("security", "verify-cert", "-L", "-l", "-p", "basic", "-c", path)
}

// RevealCA selects the CA in Finder.
func RevealCA(path string) error {
	return run("open", "-R", path)
}

// services lists the enabled network services, skipping the header and the
// services macOS marks disabled with a leading asterisk.
func services() ([]string, error) {
	bin, err := exec.LookPath("networksetup")
	if err != nil {
		return nil, fmt.Errorf("networksetup not found")
	}
	out, err := exec.Command(bin, "-listallnetworkservices").Output()
	if err != nil {
		return nil, fmt.Errorf("list network services: %w", err)
	}
	var list []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "An asterisk") {
			continue
		}
		list = append(list, line)
	}
	return list, nil
}
