//go:build windows

package system

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	wininet             = windows.NewLazySystemDLL("wininet.dll")
	internetQueryOption = wininet.NewProc("InternetQueryOptionW")
	internetSetOption   = wininet.NewProc("InternetSetOptionW")
	globalFree          = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")
)

const (
	inetPerConnection = 75
	inetFlags         = 1
	inetServer        = 2
	inetPAC           = 4
	inetFlagsUI       = 10
	proxyDirect       = 1
	proxyManual       = 2
	proxyAutoURL      = 4
	proxyAutoDetect   = 8
)

// inetOption and inetOptionList match WinINet's INTERNET_PER_CONN_OPTIONW
// and INTERNET_PER_CONN_OPTION_LISTW. The union holds a DWORD, pointer or
// FILETIME (8 bytes); uint64 gives the required size/alignment on 32/64-bit Go.
type inetOption struct {
	option uint32
	value  uint64
}

type inetOptionList struct {
	size       uint32
	connection *uint16 // nil means the default LAN connection
	count      uint32
	failed     uint32
	options    *inetOption
}

func queryProxyOptions(options []inetOption) error {
	list := inetOptionList{count: uint32(len(options)), options: &options[0]}
	list.size = uint32(unsafe.Sizeof(list))
	size := list.size
	ok, _, err := internetQueryOption.Call(0, inetPerConnection, uintptr(unsafe.Pointer(&list)), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return fmt.Errorf("read WinINet proxy: %w", err)
	}
	return nil
}

// applyProxy enables the per-user WinINet proxy after journaling.
func (proxySnapshot) applyProxy(addr string) error {
	if _, _, ok := splitAddr(addr); !ok {
		return fmt.Errorf("bad proxy address %q", addr)
	}
	return writeProxy(addr)
}

// ClearProxy disables the per-user WinINet proxy.
func ClearProxy() error {
	return writeProxy("")
}

// writeProxy preserves PAC/discovery and bypass settings, changes only the
// fixed proxy, then notifies applications. Readback reports automatic conflicts.
func writeProxy(addr string) error {
	options := []inetOption{{option: inetFlagsUI}}
	if err := queryProxyOptions(options); err != nil {
		return err
	}
	options[0].option = inetFlags
	options[0].value = options[0].value&^proxyManual | proxyDirect
	var server *uint16
	if addr != "" {
		var err error
		server, err = windows.UTF16PtrFromString(addr)
		if err != nil {
			return err
		}
		options[0].value |= proxyManual
		options = append(options, inetOption{option: inetServer})
		*(**uint16)(unsafe.Pointer(&options[1].value)) = server
	}
	list := inetOptionList{count: uint32(len(options)), options: &options[0]}
	list.size = uint32(unsafe.Sizeof(list))
	ok, _, err := internetSetOption.Call(0, inetPerConnection, uintptr(unsafe.Pointer(&list)), uintptr(list.size))
	runtime.KeepAlive(server)
	if ok == 0 {
		return fmt.Errorf("write WinINet proxy: %w", err)
	}
	for _, option := range []uintptr{39, 37} { // SETTINGS_CHANGED, REFRESH
		if ok, _, err := internetSetOption.Call(0, option, 0, 0); ok == 0 {
			return fmt.Errorf("notify WinINet proxy change: %w", err)
		}
	}
	return nil
}

// readProxy uses WinINet's connection flags: AutoDetect is not reliably stored
// as a scalar registry value, and a retained PAC URL need not be enabled.
func readProxy(_ outputFunc) ([]ProxyConfig, error) {
	options := []inetOption{{option: inetFlagsUI}, {option: inetServer}, {option: inetPAC}}
	if err := queryProxyOptions(options); err != nil {
		return nil, err
	}
	for _, option := range options[1:] {
		if option.value != 0 {
			defer globalFree.Call(uintptr(option.value))
		}
	}
	server := windows.UTF16PtrToString(*(**uint16)(unsafe.Pointer(&options[1].value)))
	pac := windows.UTF16PtrToString(*(**uint16)(unsafe.Pointer(&options[2].value)))
	return []ProxyConfig{windowsProxyConfig(uint32(options[0].value), server, pac)}, nil
}

func windowsProxyConfig(flags uint32, server, pac string) ProxyConfig {
	cfg := ProxyConfig{Scope: "Current user"}
	if flags&proxyManual != 0 {
		cfg.HTTP, cfg.HTTPS = parseProxyServer(server)
	}
	switch {
	case flags&(proxyAutoURL|proxyAutoDetect) != 0:
		cfg.Mode = ProxyModeAutomatic
		if flags&proxyAutoURL != 0 {
			cfg.PAC = pac
		}
	case flags&proxyManual != 0:
		cfg.Mode = ProxyModeManual
	}
	return cfg
}

// parseProxyServer splits WinINet's ProxyServer value: one host:port for every
// protocol, or protocol=host:port pairs joined by semicolons.
func parseProxyServer(value string) (http, https ProxyEndpoint) {
	value = strings.TrimSpace(value)
	if value == "" {
		return ProxyEndpoint{}, ProxyEndpoint{}
	}
	if !strings.Contains(value, "=") {
		e := parseEndpointAddr(value)
		return e, e
	}
	for part := range strings.SplitSeq(value, ";") {
		name, addr, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "http":
			http = parseEndpointAddr(addr)
		case "https":
			https = parseEndpointAddr(addr)
		}
	}
	return http, https
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
