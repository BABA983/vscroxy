package certs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/vault"
)

// SecretStore keeps certificate passphrases sealed with a private key file.
// The key file is 0600 and separate from settings.json, so neither a settings
// file nor a configuration export contains a passphrase. The sealed file uses
// the same authenticated format as the vault.
type SecretStore struct {
	Path    string
	KeyPath string
}

// SecretStoreFor returns the sealed passphrase files that belong beside a
// settings file.
func SecretStoreFor(settingsPath string) SecretStore {
	dir := filepath.Dir(settingsPath)
	return SecretStore{Path: filepath.Join(dir, "certificates-secrets.json"), KeyPath: filepath.Join(dir, "certificates.key")}
}

// Load returns the passphrases by certificate ID. Missing files are empty;
// a sealed file whose key is gone is an error, never a silent reset.
func (s SecretStore) Load() (map[string]string, error) {
	if s.Path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read certificate passphrases %s: %w", filepath.Clean(s.Path), err)
	}
	key, err := os.ReadFile(s.KeyPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("certificate passphrases %s are sealed, but the key file %s is missing", filepath.Clean(s.Path), filepath.Clean(s.KeyPath))
	}
	if err != nil {
		return nil, fmt.Errorf("read the certificate passphrase key %s: %w", filepath.Clean(s.KeyPath), err)
	}
	secrets, err := vault.Open(key, data)
	if err != nil {
		return nil, fmt.Errorf("open certificate passphrases %s: %w", filepath.Clean(s.Path), err)
	}
	out := make(map[string]string, len(secrets))
	for _, secret := range secrets {
		if secret.Name != "" {
			out[secret.Name] = secret.Value
		}
	}
	return out, nil
}

// Save seals passphrases by certificate ID. No entries removes the sealed
// file; the key stays for the next save.
func (s SecretStore) Save(secrets map[string]string) error {
	if s.Path == "" {
		return nil
	}
	if len(secrets) == 0 {
		if err := os.Remove(s.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove certificate passphrases %s: %w", filepath.Clean(s.Path), err)
		}
		return nil
	}
	key, err := os.ReadFile(s.KeyPath)
	if errors.Is(err, fs.ErrNotExist) {
		key = vault.NewKey()
		if err := os.MkdirAll(filepath.Dir(s.KeyPath), 0o700); err != nil {
			return fmt.Errorf("create the certificate passphrase directory: %w", err)
		}
		if err := store.WriteFile(s.KeyPath, key); err != nil {
			return fmt.Errorf("write the certificate passphrase key: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("read the certificate passphrase key %s: %w", filepath.Clean(s.KeyPath), err)
	}
	names := make([]string, 0, len(secrets))
	for name := range secrets {
		names = append(names, name)
	}
	slices.Sort(names)
	list := make(vault.Secrets, 0, len(names))
	for _, name := range names {
		list = append(list, vault.Secret{Name: name, Value: secrets[name]})
	}
	data, err := vault.Seal(key, list)
	if err != nil {
		return fmt.Errorf("seal certificate passphrases: %w", err)
	}
	if err := store.WriteFile(s.Path, data); err != nil {
		return fmt.Errorf("write certificate passphrases %s: %w", filepath.Clean(s.Path), err)
	}
	return nil
}
