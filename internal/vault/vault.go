// Package vault keeps the secrets requests read as {{vault:name}}, encrypted
// at rest with AES-256-GCM. It holds no key and touches no file: the app
// keeps the key in the system's credential store and the sealed bytes on
// disk, so a copied vault file is useless without the key.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
)

// KeySize is the length of a vault key: AES-256.
const KeySize = 32

// Secret is one named value. Domains, when set, are the only hosts a request
// may send it to: "api.example.com" is that host, "*.example.com" any host
// under example.com.
type Secret struct {
	Name    string   `json:"name"`
	Value   string   `json:"value"`
	Domains []string `json:"domains,omitempty"`
}

// Secrets are a vault's contents, decrypted.
type Secrets []Secret

// ErrNotFound is what Lookup fails with for a name the vault does not hold.
var ErrNotFound = errors.New("no such secret in the vault")

// NewKey makes a random vault key.
func NewKey() []byte {
	key := make([]byte, KeySize)
	rand.Read(key)
	return key
}

// sealed is the file: everything but the format is opaque without the key.
type sealed struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

const format = "vscroxy-vault"

// additional binds the ciphertext to this format and version.
var additional = []byte("vscroxy-vault/1")

// Seal encrypts the secrets with key.
func Seal(key []byte, secrets Secrets) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(secrets)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	rand.Read(nonce)
	return json.Marshal(sealed{Format: format, Version: 1, Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plain, additional)})
}

// Open decrypts what Seal wrote. A wrong key and a damaged file fail alike,
// never with partial secrets.
func Open(key, data []byte) (Secrets, error) {
	var file sealed
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("read vault: %w", err)
	}
	if file.Format != format || file.Version != 1 {
		return nil, errors.New("unsupported vault file format or version")
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(file.Nonce) != aead.NonceSize() {
		return nil, errors.New("damaged vault file")
	}
	plain, err := aead.Open(nil, file.Nonce, file.Ciphertext, additional)
	if err != nil {
		return nil, errors.New("the vault key does not open this vault, or the file is damaged")
	}
	var secrets Secrets
	if err := json.Unmarshal(plain, &secrets); err != nil {
		return nil, fmt.Errorf("read vault: %w", err)
	}
	return secrets, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("a vault key is %d bytes", KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Lookup returns the secret named for a request to host, refusing a host
// outside the secret's domains.
func (s Secrets) Lookup(name, host string) (string, error) {
	for _, secret := range s {
		if secret.Name != name {
			continue
		}
		if !secret.Allows(host) {
			return "", fmt.Errorf("secret %q is not allowed for host %q", name, host)
		}
		return secret.Value, nil
	}
	return "", fmt.Errorf("%w: %q", ErrNotFound, name)
}

// Allows reports whether a request to host may carry the secret.
func (s Secret) Allows(host string) bool {
	if len(s.Domains) == 0 {
		return true
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, domain := range s.Domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if parent, ok := strings.CutPrefix(domain, "*."); ok {
			if strings.HasSuffix(host, "."+parent) {
				return true
			}
		} else if domain != "" && host == domain {
			return true
		}
	}
	return false
}
