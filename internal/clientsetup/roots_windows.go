//go:build windows

package clientsetup

import (
	"bytes"
	"encoding/pem"
	"unsafe"

	"golang.org/x/sys/windows"
)

// platformSystemRoots exports the machine's Root certificate store. Using the
// CryptoAPI avoids certutil's text output and works without a console.
func platformSystemRoots() ([]byte, error) {
	store, err := windows.CertOpenSystemStore(0, windows.StringToUTF16Ptr("ROOT"))
	if err != nil {
		return nil, err
	}
	defer windows.CertCloseStore(store, 0)
	var bundle bytes.Buffer
	var previous *windows.CertContext
	for {
		context, err := windows.CertEnumCertificatesInStore(store, previous)
		if err != nil || context == nil {
			break
		}
		der := unsafe.Slice(context.EncodedCert, context.Length)
		if err := pem.Encode(&bundle, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
			return nil, err
		}
		previous = context
	}
	if !hasCertificate(bundle.Bytes()) {
		return nil, ErrNoSystemRoots
	}
	return bundle.Bytes(), nil
}
