//go:build windows

package system

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type proxySnapshot struct {
	Flags  uint32
	Server string
}

func readProxySnapshot() (proxySnapshot, error) {
	options := []inetOption{{option: inetFlagsUI}, {option: inetServer}}
	if err := queryProxyOptions(options); err != nil {
		return proxySnapshot{}, err
	}
	if options[1].value != 0 {
		defer globalFree.Call(uintptr(options[1].value))
	}
	return proxySnapshot{
		Flags:  uint32(options[0].value),
		Server: windows.UTF16PtrToString(*(**uint16)(unsafe.Pointer(&options[1].value))),
	}, nil
}

func snapshotProxy(target string) (proxySnapshot, error) {
	state, err := readProxySnapshot()
	if err != nil {
		return state, err
	}
	http, https := parseProxyServer(state.Server)
	if state.Flags&proxyManual != 0 && (http.sameAs(target) || https.sameAs(target)) {
		return state, errors.New("system proxy already points at this address without a recovery record; disable the stale proxy first")
	}
	return state, nil
}

func restoreProxy(before proxySnapshot, target string) error {
	current, err := readProxySnapshot()
	if err != nil {
		return err
	}
	server, flags := restoredWindowsProxy(before, current, target)
	if server == current.Server && flags == current.Flags {
		return nil
	}
	text, err := windows.UTF16PtrFromString(server)
	if err != nil {
		return err
	}
	options := []inetOption{{option: inetFlags, value: uint64(flags)}, {option: inetServer}}
	*(**uint16)(unsafe.Pointer(&options[1].value)) = text
	list := inetOptionList{count: uint32(len(options)), options: &options[0]}
	list.size = uint32(unsafe.Sizeof(list))
	ok, _, err := internetSetOption.Call(0, inetPerConnection, uintptr(unsafe.Pointer(&list)), uintptr(list.size))
	runtime.KeepAlive(text)
	if ok == 0 {
		return fmt.Errorf("restore WinINet proxy: %w", err)
	}
	for _, option := range []uintptr{39, 37} {
		if ok, _, err := internetSetOption.Call(0, option, 0, 0); ok == 0 {
			return fmt.Errorf("notify WinINet proxy restoration: %w", err)
		}
	}
	actual, err := readProxySnapshot()
	if err != nil {
		return err
	}
	if actual.Server != server || actual.Flags != flags {
		return errors.New("proxy restoration was not accepted by Windows")
	}
	return nil
}

// Only the fixed-server string and manual/direct flags were changed. PAC,
// discovery and bypass remain untouched, including changes made since enable.
func restoredWindowsProxy(before, current proxySnapshot, target string) (string, uint32) {
	flags := current.Flags
	if current.Server == target {
		if flags&(proxyManual|proxyDirect) == proxyManual|proxyDirect {
			flags = flags&^(proxyManual|proxyDirect) | before.Flags&(proxyManual|proxyDirect)
		}
		return before.Server, flags
	}
	parts, old := windowsProxyParts(current.Server), windowsProxyParts(before.Server)
	if before.Flags&proxyManual == 0 {
		clear(old) // do not activate an old, previously unused protocol server
	}
	changed := false
	for protocol, address := range parts {
		if parseEndpointAddr(address).sameAs(target) {
			if address := old[protocol]; address != "" {
				parts[protocol] = address
			} else {
				delete(parts, protocol)
			}
			changed = true
		}
	}
	if !changed {
		return current.Server, flags
	}
	if len(parts) == 0 {
		flags = flags&^proxyManual | proxyDirect
	}
	values := make([]string, 0, len(parts))
	for protocol, address := range parts {
		values = append(values, protocol+"="+address)
	}
	slices.Sort(values)
	return strings.Join(values, ";"), flags
}

func windowsProxyParts(server string) map[string]string {
	parts := make(map[string]string)
	if !strings.Contains(server, "=") {
		if strings.TrimSpace(server) != "" {
			for _, protocol := range []string{"http", "https", "ftp"} {
				parts[protocol] = server
			}
		}
		return parts
	}
	for part := range strings.SplitSeq(server, ";") {
		if protocol, addr, ok := strings.Cut(part, "="); ok {
			parts[strings.ToLower(strings.TrimSpace(protocol))] = strings.TrimSpace(addr)
		}
	}
	return parts
}
