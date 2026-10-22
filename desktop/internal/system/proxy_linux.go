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
)

const caName = "vscroxy.crt"

// SetProxy points the GNOME desktop proxy settings at addr.
func SetProxy(addr string) error {
	host, port, ok := splitAddr(addr)
	if !ok {
		return fmt.Errorf("bad proxy address %q", addr)
	}
	cmds := [][]string{
		{"set", "org.gnome.system.proxy", "mode", "manual"},
		{"set", "org.gnome.system.proxy.http", "host", host},
		{"set", "org.gnome.system.proxy.http", "port", port},
		{"set", "org.gnome.system.proxy.https", "host", host},
		{"set", "org.gnome.system.proxy.https", "port", port},
		{"set", "org.gnome.system.proxy", "ignore-hosts", "['localhost', '127.0.0.1', '::1']"},
	}
	for _, args := range cmds {
		if err := run("gsettings", args...); err != nil {
			return err
		}
	}
	return nil
}

// ClearProxy turns the desktop proxy off.
func ClearProxy() error {
	return run("gsettings", "set", "org.gnome.system.proxy", "mode", "none")
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
