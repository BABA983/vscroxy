//go:build windows

package system

import (
	"fmt"
	"os/exec"
)

const inetSettings = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// SetProxy enables the per-user WinINet proxy for addr.
func SetProxy(addr string) error {
	if _, _, ok := splitAddr(addr); !ok {
		return fmt.Errorf("bad proxy address %q", addr)
	}
	if err := run("reg", "add", inetSettings, "/v", "ProxyServer", "/t", "REG_SZ", "/d", addr, "/f"); err != nil {
		return err
	}
	return run("reg", "add", inetSettings, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
}

// ClearProxy disables the per-user WinINet proxy.
func ClearProxy() error {
	return run("reg", "add", inetSettings, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f")
}

// InstallCA adds the CA to the machine root store. This needs an elevated
// process, so the error explains what to do when it fails.
func InstallCA(path string) error {
	if err := run("certutil", "-addstore", "-f", "Root", path); err != nil {
		return fmt.Errorf("%w (run vscroxy as administrator to trust the certificate)", err)
	}
	return nil
}

// RevealCA selects the CA in Explorer.
func RevealCA(path string) error {
	bin, err := exec.LookPath("explorer")
	if err != nil {
		return fmt.Errorf("explorer not found")
	}
	// Explorer returns a non-zero exit code even on success.
	_ = exec.Command(bin, "/select,", path).Run()
	return nil
}
