//go:build linux

package system

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	caName      = "vscroxy.crt"
	proxySchema = "org.gnome.system.proxy"
)

// applyProxy points the GNOME desktop proxy settings at addr after journaling.
func (proxySnapshot) applyProxy(addr string) error {
	host, port, ok := splitAddr(addr)
	if !ok {
		return fmt.Errorf("bad proxy address %q", addr)
	}
	for _, args := range [][]string{
		{"set", proxySchema + ".http", "host", host},
		{"set", proxySchema + ".http", "port", port},
		{"set", proxySchema + ".https", "host", host},
		{"set", proxySchema + ".https", "port", port},
		{"set", proxySchema, "ignore-hosts", "['localhost', '127.0.0.1', '::1']"},
		{"set", proxySchema, "mode", "manual"},
	} {
		if err := run("gsettings", args...); err != nil {
			return err
		}
	}
	return nil
}

// ClearProxy turns the desktop proxy off.
func ClearProxy() error {
	return run("gsettings", "set", proxySchema, "mode", "none")
}

// readProxy reads the desktop proxy settings. It reports what the desktop
// applies: a manual mode uses the fixed servers, an automatic mode uses the
// configuration URL, and use-same-proxy makes HTTPS follow HTTP.
func readProxy(out outputFunc) ([]ProxyConfig, error) {
	get := func(section, key string) (string, error) {
		raw, err := out("gsettings", "get", section, key)
		if err != nil {
			return "", err
		}
		return gvariant(string(raw)), nil
	}
	mode, err := get(proxySchema, "mode")
	if err != nil {
		return nil, err
	}
	cfg := ProxyConfig{Scope: "Desktop"}
	switch mode {
	case "manual":
		cfg.Mode = ProxyModeManual
	case "auto":
		cfg.Mode = ProxyModeAutomatic
		if cfg.PAC, err = get(proxySchema, "autoconfig-url"); err != nil {
			return nil, err
		}
	}
	if cfg.Mode != ProxyModeManual {
		return []ProxyConfig{cfg}, nil
	}
	if cfg.HTTP, err = readEndpoint(get, proxySchema+".http"); err != nil {
		return nil, err
	}
	same, err := get(proxySchema, "use-same-proxy")
	if err != nil {
		return nil, err
	}
	if same == "true" {
		cfg.HTTPS = cfg.HTTP
	} else if cfg.HTTPS, err = readEndpoint(get, proxySchema+".https"); err != nil {
		return nil, err
	}
	return []ProxyConfig{cfg}, nil
}

// gvariant reads a value as gsettings prints it: strings are quoted, numbers
// and booleans are bare.
func gvariant(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) < 2 || s[0] != '\'' || s[len(s)-1] != '\'' {
		return s
	}
	body := s[1 : len(s)-1]
	body = strings.ReplaceAll(body, `\'`, `'`)
	return strings.ReplaceAll(body, `\\`, `\`)
}

// InstallCA copies the CA into the system trust store and refreshes it.
func InstallCA(path string) error {
	cert, err := readCA(path)
	if err != nil {
		return err
	}
	var target, update string
	for _, store := range []struct{ command, dir string }{
		{"update-ca-certificates", "/usr/local/share/ca-certificates"},
		{"update-ca-trust", "/etc/pki/ca-trust/source/anchors"},
		{"update-ca-trust", "/etc/ca-certificates/trust-source/anchors"},
	} {
		bin, err := exec.LookPath(store.command)
		info, statErr := os.Stat(store.dir)
		if err == nil && statErr == nil && info.IsDir() {
			target, update = filepath.Join(store.dir, caName), bin
			break
		}
	}
	if update == "" {
		return errors.New("automatic CA installation requires update-ca-certificates or update-ca-trust and its system anchor directory; import the public CA manually for this distribution")
	}
	if _, err := exec.LookPath("pkexec"); err != nil {
		return fmt.Errorf("pkexec not found; copy the public CA to %s and run %s as administrator", target, update)
	}
	install, err := exec.LookPath("install")
	if err != nil {
		return err
	}
	snapshot, err := snapshotCA(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	if err != nil {
		return err
	}
	defer os.Remove(snapshot)
	// One authorization for both steps; paths are arguments, never shell code.
	err = run("pkexec", "/bin/sh", "-c", `"$1" -m 0644 -- "$2" "$3" && "$4"`, "vscroxy-ca", install, snapshot, target, update)
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() == 126 {
		return ErrInstallCancelled
	}
	return err
}

func verifyCA(path string, _ *x509.Certificate) error {
	// A fresh process sees trust-store updates; Go caches Unix system roots.
	// This is a root CA, not a TLS server leaf with digitalSignature usage.
	return run("openssl", "verify", path)
}

// RevealCA opens the folder that holds the CA.
func RevealCA(path string) error {
	return run("xdg-open", filepath.Dir(path))
}
