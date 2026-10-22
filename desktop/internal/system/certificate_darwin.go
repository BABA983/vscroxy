//go:build darwin

package system

import (
	"bytes"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/ebitengine/purego"
)

// These are CoreFoundation and Security C APIs, not Objective-C objects.
// Create/Copy results are owned by the caller and released after use.
type securityAPI struct {
	dataCreate        func(uintptr, *byte, int) uintptr
	release           func(uintptr)
	certificateCreate func(uintptr, uintptr) uintptr
	setTrust          func(uintptr, uint32, uintptr) int32
	copyError         func(int32, uintptr) uintptr
	stringGetCString  func(uintptr, *byte, int, uint32) bool
}

var macSecurity = sync.OnceValues(func() (*securityAPI, error) {
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, err
	}
	sec, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, err
	}
	api := &securityAPI{}
	purego.RegisterLibFunc(&api.dataCreate, cf, "CFDataCreate")
	purego.RegisterLibFunc(&api.release, cf, "CFRelease")
	purego.RegisterLibFunc(&api.stringGetCString, cf, "CFStringGetCString")
	purego.RegisterLibFunc(&api.certificateCreate, sec, "SecCertificateCreateWithData")
	purego.RegisterLibFunc(&api.setTrust, sec, "SecTrustSettingsSetTrustSettings")
	purego.RegisterLibFunc(&api.copyError, sec, "SecCopyErrorMessageString")
	return api, nil
})

// InstallCA elevates only the public certificate import. Trust authorization
// must stay in this non-elevated GUI process: an elevated shell cannot present
// the separate trust dialog. Both stages may block for user authorization and
// must run off the UI thread.
func InstallCA(path string) error {
	cert, err := readCA(path)
	if err != nil {
		return err
	}
	api, err := macSecurity()
	if err != nil {
		return fmt.Errorf("load macOS Security framework: %w", err)
	}
	data := api.dataCreate(0, &cert.Raw[0], len(cert.Raw))
	if data == 0 {
		return errors.New("create certificate data failed")
	}
	defer api.release(data)
	ref := api.certificateCreate(0, data)
	if ref == 0 {
		return errors.New("macOS could not read the CA certificate")
	}
	defer api.release(ref)
	if err := importSystemCA(cert.Raw); err != nil {
		return err
	}
	// kSecTrustSettingsDomainAdmin = 1. An empty settings array (NULL) means
	// trust this self-signed root, matching the former trustRoot operation.
	return api.statusError("authorize CA trust", api.setTrust(ref, 1, 0))
}

func importSystemCA(der []byte) error {
	// A failed trust authorization can leave the certificate already imported.
	// Check exact DER, not its name; even a duplicate add can fail with -61
	// when System.keychain is read-only to the current process.
	if present, err := systemHasCA(der); err != nil || present {
		return err
	}
	path, err := snapshotCA(der)
	if err != nil {
		return err
	}
	defer os.Remove(path)
	// add-certificates only imports; do NOT use add-trusted-cert here. Paths
	// are passed as data and quoted by AppleScript, not interpolated as code.
	const script = `on run argv
	try
		do shell script ("/usr/bin/security add-certificates -k /Library/Keychains/System.keychain " & quoted form of (item 1 of argv)) with administrator privileges with prompt "vscroxy needs to import its public CA certificate into the System keychain."
	on error messageText number errorNumber
		if errorNumber is -128 then return "cancelled"
		error messageText number errorNumber
	end try
	return "imported"
end run`
	out, importErr := exec.Command("/usr/bin/osascript", "-e", script, "--", path).CombinedOutput()
	if strings.TrimSpace(string(out)) == "cancelled" {
		return ErrInstallCancelled
	}
	// Recheck before trusting, including when another installation raced ours
	// and add-certificates reported a duplicate (a nonzero exit status).
	present, checkErr := systemHasCA(der)
	if checkErr == nil && present {
		return nil
	}
	if importErr != nil {
		return fmt.Errorf("import CA to System keychain: %w: %s", importErr, trim(string(out)))
	}
	if checkErr != nil {
		return checkErr
	}
	return errors.New("import finished but the CA was not found in System keychain")
}

func systemHasCA(der []byte) (bool, error) {
	out, err := exec.Command("/usr/bin/security", "find-certificate", "-a", "-p", "/Library/Keychains/System.keychain").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("inspect System keychain: %w: %s", err, trim(string(out)))
	}
	for {
		block, rest := pem.Decode(out)
		if block == nil {
			return false, nil
		}
		if block.Type == "CERTIFICATE" && bytes.Equal(block.Bytes, der) {
			return true, nil
		}
		out = rest
	}
}

func (api *securityAPI) statusError(operation string, status int32) error {
	if status == 0 {
		return nil
	}
	if status == -128 || status == -60006 { // errSecUserCanceled / errAuthorizationCanceled
		return fmt.Errorf("%s: %w", operation, ErrInstallCancelled)
	}
	message := api.copyError(status, 0)
	if message != 0 {
		defer api.release(message)
		var buf [1024]byte
		const utf8 = 0x08000100
		if api.stringGetCString(message, &buf[0], len(buf), utf8) {
			return fmt.Errorf("%s: %s (OSStatus %d)", operation, bytes.TrimRight(buf[:], "\x00"), status)
		}
	}
	return fmt.Errorf("%s: OSStatus %d", operation, status)
}
