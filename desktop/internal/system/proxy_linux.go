//go:build linux

package system

import (
	"fmt"
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
	target := filepath.Join("/usr/local/share/ca-certificates", caName)
	if _, err := exec.LookPath("pkexec"); err != nil {
		return fmt.Errorf("pkexec not found; copy %s to %s and run update-ca-certificates", path, target)
	}
	if err := run("pkexec", "cp", path, target); err != nil {
		return err
	}
	return run("pkexec", "update-ca-certificates")
}

// RevealCA opens the folder that holds the CA.
func RevealCA(path string) error {
	return run("xdg-open", filepath.Dir(path))
}
