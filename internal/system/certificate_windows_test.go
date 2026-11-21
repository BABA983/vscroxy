//go:build windows

package system

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// TestCheckCASeesTrustChangesWithoutARestart is the regression test for a stale
// "system trust not verified" right after InstallCA: the Windows chain engine
// cached the earlier, untrusted answer per process, so the check disagreed with
// the store for about thirteen seconds. The test uses a private store, never
// this machine's roots.
func TestCheckCASeesTrustChangesWithoutARestart(t *testing.T) {
	path := writeTestCA(t)
	name := fmt.Sprintf("vscroxy-test-%d", time.Now().UnixNano())
	store := openTestStore(t, name)
	old := rootStoreName
	rootStoreName = name
	t.Cleanup(func() {
		rootStoreName = old
		windows.CertCloseStore(store, 0)
		namePtr, err := windows.UTF16PtrFromString(name)
		if err != nil {
			t.Error(err)
			return
		}
		// Deleting a system store returns a null handle and leaves a stale last
		// error behind, so the registry is what proves the store is gone.
		windows.CertOpenStore(windows.CERT_STORE_PROV_SYSTEM, 0, 0,
			windows.CERT_SYSTEM_STORE_CURRENT_USER|windows.CERT_STORE_DELETE_FLAG, uintptr(unsafe.Pointer(namePtr)))
		if key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\SystemCertificates\`+name, registry.READ); err == nil {
			key.Close()
			t.Error("temporary certificate store was not removed")
		}
	})

	if _, err := CheckCA(path); err == nil || !unknownAuthority(err) {
		t.Fatalf("a CA outside the store must read as untrusted: %v", err)
	}
	context := addTestCA(t, store, path)
	if _, err := CheckCA(path); err != nil {
		t.Fatalf("a CA just added to the store must verify without a restart: %v", err)
	}
	if err := windows.CertDeleteCertificateFromStore(context); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckCA(path); err == nil || !unknownAuthority(err) {
		t.Fatalf("a removed CA must read as untrusted without a restart: %v", err)
	}
}

func unknownAuthority(err error) bool {
	_, ok := errors.AsType[x509.UnknownAuthorityError](err)
	return ok
}

func writeTestCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "vscroxy trust test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func openTestStore(t *testing.T, name string) windows.Handle {
	t.Helper()
	store, err := windows.CertOpenSystemStore(0, windows.StringToUTF16Ptr(name))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func addTestCA(t *testing.T, store windows.Handle, path string) *windows.CertContext {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	context, err := windows.CertCreateCertificateContext(
		windows.X509_ASN_ENCODING|windows.PKCS_7_ASN_ENCODING, &block.Bytes[0], uint32(len(block.Bytes)))
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.CertAddCertificateContextToStore(store, context, windows.CERT_STORE_ADD_ALWAYS, nil); err != nil {
		windows.CertFreeCertificateContext(context)
		t.Fatal(err)
	}
	windows.CertFreeCertificateContext(context)
	// DeleteCertificateFromStore takes ownership of a context from the store, so
	// find the copy the store holds.
	stored, err := windows.CertEnumCertificatesInStore(store, nil)
	if err != nil || stored == nil {
		t.Fatal("the store did not keep the CA")
	}
	return stored
}
