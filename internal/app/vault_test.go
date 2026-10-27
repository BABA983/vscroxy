package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo"
)

// fakeCredentials stands in for the system credential store; nil keys means
// the system has none.
type fakeCredentials struct{ keys map[string][]byte }

func (f *fakeCredentials) Get(key string) ([]byte, error) {
	if f.keys == nil {
		return nil, errors.ErrUnsupported
	}
	if v, ok := f.keys[key]; ok {
		return v, nil
	}
	return nil, mygo.ErrSecretNotFound
}

func (f *fakeCredentials) Set(key string, value []byte) error {
	if f.keys == nil {
		return errors.ErrUnsupported
	}
	f.keys[key] = value
	return nil
}

func vaultApp(t *testing.T, store *fakeCredentials) *app {
	t.Helper()
	old := credentials
	credentials = store
	t.Cleanup(func() { credentials = old })
	dir := t.TempDir()
	a := &app{}
	a.vault.path, a.vault.keyPath = filepath.Join(dir, "vault.json"), filepath.Join(dir, "vault.key")
	return a
}

func TestVaultKeepsItsKeyInTheCredentialStore(t *testing.T) {
	store := &fakeCredentials{keys: map[string][]byte{}}
	a := vaultApp(t, store)
	a.openVault()
	a.vault.rows = []vaultRow{{name: "token", value: "s3cret", domains: "api.example.com, *.Example.org"}}
	a.saveVault()
	if a.vault.editProblem != "" || a.vault.keyInFile || len(store.keys[vaultKeyName]) != 32 {
		t.Fatalf("problem %q, key in file %t, stored key %d bytes", a.vault.editProblem, a.vault.keyInFile, len(store.keys[vaultKeyName]))
	}
	if _, err := os.Stat(a.vault.keyPath); err == nil {
		t.Fatal("a key file was written although a credential store exists")
	}
	data, _ := os.ReadFile(a.vault.path)
	if bytes.Contains(data, []byte("s3cret")) {
		t.Fatal("the vault file holds a secret in plaintext")
	}
	// A later launch opens it with the stored key, and a send reads it.
	b := vaultApp(t, store)
	b.vault.path, b.vault.keyPath = a.vault.path, a.vault.keyPath
	in := b.sendContext(request.Request{URL: "https://x.example.org/", Headers: "Authorization: {{vault:token}}"})
	if in.Vault == nil {
		t.Fatal("no vault for a request that reads one")
	}
	if v, err := in.Vault("token", "x.example.org"); err != nil || v != "s3cret" {
		t.Fatalf("lookup = %q, %v", v, err)
	}
	// Losing the key never replaces the vault with an empty one.
	c := vaultApp(t, &fakeCredentials{keys: map[string][]byte{}})
	c.vault.path, c.vault.keyPath = a.vault.path, a.vault.keyPath
	c.openVault()
	after, _ := os.ReadFile(a.vault.path)
	if c.vault.problem == "" || c.vault.loaded || !bytes.Equal(after, data) {
		t.Fatalf("a vault without its key opened or changed: %q", c.vault.problem)
	}
}

func TestVaultFallsBackToAPrivateKeyFile(t *testing.T) {
	a := vaultApp(t, &fakeCredentials{})
	a.openVault()
	a.vault.rows = []vaultRow{{name: "token", value: "v"}}
	a.saveVault()
	info, err := os.Stat(a.vault.keyPath)
	if err != nil || !a.vault.keyInFile {
		t.Fatalf("no key file: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("key file mode %v is readable by others", info.Mode().Perm())
	}
	b := vaultApp(t, &fakeCredentials{})
	b.vault.path, b.vault.keyPath = a.vault.path, a.vault.keyPath
	if err := b.ensureVault(); err != nil || len(b.vault.secrets) != 1 {
		t.Fatalf("reopen: %v, %+v", err, b.vault.secrets)
	}
}
