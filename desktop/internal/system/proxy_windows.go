//go:build windows

package system

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
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

// InstallCA trusts the public CA for the current user, without running the
// capture app as administrator. Other accounts/services need their own setup.
func InstallCA(path string) error {
	cert, err := readCA(path)
	if err != nil {
		return err
	}
	snapshot, err := snapshotCA(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	if err != nil {
		return err
	}
	defer os.Remove(snapshot)
	err = run("certutil", "-user", "-addstore", "-f", "Root", snapshot)
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && (uint32(exit.ExitCode()) == 1223 || uint32(exit.ExitCode()) == 0x800704c7) {
		return ErrInstallCancelled // ERROR_CANCELLED / HRESULT_FROM_WIN32
	}
	return err
}

func verifyCA(_ string, cert *x509.Certificate) error {
	// With nil Roots, x509 uses the current Windows system verifier.
	_, err := cert.Verify(x509.VerifyOptions{KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	return err
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
