// Package certs is the client-certificate and extra-root configuration shared
// by authored requests and the capture engine. Certificate and key files are
// referenced by local path and read on every use; passphrases are held in
// memory only and sealed separately by SecretStore.
package certs

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/youmark/pkcs8"
	"software.sslmate.com/src/go-pkcs12"
)

const (
	FormatPEM    = "pem"
	FormatPKCS12 = "pkcs12"
)

// maxFileSize bounds a certificate, key, PFX or CA file read into memory.
const maxFileSize = 8 << 20

// Client is one host's client certificate. Host is a host name with an
// optional port and may start with "*.". Format is FormatPEM (CertFile and
// KeyFile) or FormatPKCS12 (CertFile is a .p12/.pfx). ID keys the sealed
// passphrase; it is not a credential.
type Client struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	Format   string `json:"format,omitempty"`
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile,omitempty"`
}

// Settings is the certificate configuration. Secrets maps a Client ID to its
// passphrase and is never serialized: settings files and configuration
// exports contain only paths and hosts. SecretsProblem explains why the
// sealed passphrases could not be opened, without exposing a value.
type Settings struct {
	Clients          []Client          `json:"clients,omitempty"`
	RootCAs          []string          `json:"rootCAs,omitempty"`
	TrustSystemRoots *bool             `json:"trustSystemRoots,omitempty"`
	Secrets          map[string]string `json:"-"`
	SecretsProblem   string            `json:"-"`
}

// Clone returns a copy that can be edited without changing s.
func (s Settings) Clone() Settings {
	out := s
	out.Clients = slices.Clone(s.Clients)
	out.RootCAs = slices.Clone(s.RootCAs)
	if s.TrustSystemRoots != nil {
		value := *s.TrustSystemRoots
		out.TrustSystemRoots = &value
	}
	if s.Secrets != nil {
		out.Secrets = make(map[string]string, len(s.Secrets))
		for key, value := range s.Secrets {
			out.Secrets[key] = value
		}
	}
	return out
}

// Equal reports whether two configurations would write the same settings.
// Passphrases are deliberately not part of the comparison.
func (s Settings) Equal(other Settings) bool {
	if !slices.Equal(s.Clients, other.Clients) || !slices.Equal(s.RootCAs, other.RootCAs) {
		return false
	}
	switch {
	case s.TrustSystemRoots == nil && other.TrustSystemRoots == nil:
		return true
	case s.TrustSystemRoots == nil || other.TrustSystemRoots == nil:
		return false
	default:
		return *s.TrustSystemRoots == *other.TrustSystemRoots
	}
}

// Configured reports whether the settings change TLS behavior at all. The zero
// value trusts the system roots and adds no client certificate.
func (s Settings) Configured() bool {
	return len(s.Clients) > 0 || len(s.RootCAs) > 0 || s.TrustSystemRoots != nil
}

// TrustsSystem reports whether system roots are trusted. The default is yes.
func (s Settings) TrustsSystem() bool {
	return s.TrustSystemRoots == nil || *s.TrustSystemRoots
}

// Normalize trims fields, gives every client a stable ID, and fills in the
// format when it is omitted. It returns a copy.
func (s Settings) Normalize() Settings {
	out := s.Clone()
	out.RootCAs = slices.DeleteFunc(out.RootCAs, func(path string) bool {
		return strings.TrimSpace(path) == ""
	})
	for i := range out.RootCAs {
		out.RootCAs[i] = strings.TrimSpace(out.RootCAs[i])
	}
	for i := range out.Clients {
		c := &out.Clients[i]
		c.Host = NormalizeHost(c.Host)
		c.CertFile = strings.TrimSpace(c.CertFile)
		c.KeyFile = strings.TrimSpace(c.KeyFile)
		c.Format = strings.ToLower(strings.TrimSpace(c.Format))
		if c.Format == "" {
			if c.KeyFile == "" {
				c.Format = FormatPKCS12
			} else {
				c.Format = FormatPEM
			}
		}
		if c.ID == "" {
			c.ID = uuid.NewString()
		}
	}
	return out
}

// Validate checks every configured host and loads every file, including a
// PKCS#12 or encrypted PEM with its sealed passphrase. A bad entry is an
// error; a send never silently continues without the certificate.
func (s Settings) Validate() error {
	seen := make(map[string]bool, len(s.Clients))
	for i, c := range s.Clients {
		if strings.TrimSpace(c.Host) == "" {
			return fmt.Errorf("client certificate %d: host is empty", i+1)
		}
		host, _ := splitHostPort(NormalizeHost(c.Host))
		if host == "" {
			return fmt.Errorf("client certificate %d: %q is not a host", i+1, c.Host)
		}
		key := NormalizeHost(c.Host)
		if seen[key] {
			return fmt.Errorf("client certificate %d: %s is configured twice", i+1, c.Host)
		}
		seen[key] = true
		format := c.Format
		if format == "" {
			if c.KeyFile == "" {
				format = FormatPKCS12
			} else {
				format = FormatPEM
			}
		}
		switch format {
		case FormatPEM:
			if c.CertFile == "" || c.KeyFile == "" {
				return fmt.Errorf("client certificate for %s: PEM needs both a certificate and a key file", c.Host)
			}
		case FormatPKCS12:
			if c.CertFile == "" {
				return fmt.Errorf("client certificate for %s: choose a PKCS#12 file", c.Host)
			}
			if c.KeyFile != "" {
				return fmt.Errorf("client certificate for %s: PKCS#12 already contains its key; clear the key file", c.Host)
			}
		default:
			return fmt.Errorf("client certificate for %s: unknown format %q", c.Host, format)
		}
		if _, err := s.loadClient(c); err != nil {
			return err
		}
	}
	for _, path := range s.RootCAs {
		data, err := readCertificateFile(path, "custom CA")
		if err != nil {
			return err
		}
		if !x509.NewCertPool().AppendCertsFromPEM(data) {
			return fmt.Errorf("custom CA %s: no PEM certificate", path)
		}
	}
	return nil
}

// ClientCertificate returns the certificate matching address, or nil when no
// entry applies. Files are read now, so editing a certificate takes effect on
// the next connection.
func (s Settings) ClientCertificate(address string) (*tls.Certificate, error) {
	best, bestScore := -1, -1
	for i, c := range s.Clients {
		if value, ok := score(c.Host, address); ok && value > bestScore {
			best, bestScore = i, value
		}
	}
	if best < 0 {
		return nil, nil
	}
	cert, err := s.loadClient(s.Clients[best])
	if err != nil {
		if s.SecretsProblem != "" {
			return nil, fmt.Errorf("%w (%s)", err, s.SecretsProblem)
		}
		return nil, err
	}
	return &cert, nil
}

// RootPool returns the roots a verified connection trusts: the system roots
// unless they are turned off, every configured PEM root, and proxyCA (DER)
// when one is supplied. An empty pool with system roots off trusts nothing.
func (s Settings) RootPool(proxyCA []byte) (*x509.CertPool, error) {
	var pool *x509.CertPool
	if s.TrustsSystem() {
		pool, _ = x509.SystemCertPool()
	}
	if pool == nil {
		pool = x509.NewCertPool()
	}
	for _, path := range s.RootCAs {
		data, err := readCertificateFile(path, "custom CA")
		if err != nil {
			return nil, err
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("custom CA %s: no PEM certificate", path)
		}
	}
	if len(proxyCA) > 0 {
		cert, err := x509.ParseCertificate(proxyCA)
		if err != nil {
			return nil, fmt.Errorf("proxy CA: %w", err)
		}
		pool.AddCert(cert)
	}
	return pool, nil
}

// TLSConfig builds a client TLS configuration for address. insecure turns
// verification off and ignores roots, but a matching client certificate is
// still loaded and presented: mTLS is independent of verifying the server.
// proxyCA is the running capture proxy's public CA, if any.
func (s Settings) TLSConfig(address string, proxyCA []byte, insecure bool) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if insecure {
		config.InsecureSkipVerify = true
	} else {
		roots, err := s.RootPool(proxyCA)
		if err != nil {
			return nil, err
		}
		config.RootCAs = roots
	}
	cert, err := s.ClientCertificate(address)
	if err != nil {
		return nil, err
	}
	if cert != nil {
		config.Certificates = []tls.Certificate{*cert}
	}
	return config, nil
}

// ValidateURL loads what a send to rawURL would use, so a broken certificate
// or root is reported before the request reaches the proxy.
func (s Settings) ValidateURL(rawURL string, proxyCA []byte, insecure bool) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if u.Scheme != "https" && u.Scheme != "wss" {
		return nil
	}
	_, err = s.TLSConfig(Address(u), proxyCA, insecure)
	return err
}

// loadClient reads one client certificate now.
func (s Settings) loadClient(c Client) (tls.Certificate, error) {
	format := c.Format
	if format == "" {
		if c.KeyFile == "" {
			format = FormatPKCS12
		} else {
			format = FormatPEM
		}
	}
	passphrase := ""
	if c.ID != "" {
		passphrase = s.Secrets[c.ID]
	}
	switch format {
	case FormatPEM:
		certPEM, err := readCertificateFile(c.CertFile, "certificate")
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("client certificate for %s: %w", c.Host, err)
		}
		keyPEM, err := readCertificateFile(c.KeyFile, "key")
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("client certificate for %s: %w", c.Host, err)
		}
		cert, err := parsePEMPair(certPEM, keyPEM, passphrase)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("client certificate for %s: %w", c.Host, err)
		}
		return cert, nil
	case FormatPKCS12:
		data, err := readCertificateFile(c.CertFile, "PKCS#12")
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("client certificate for %s: %w", c.Host, err)
		}
		cert, err := parsePKCS12(data, passphrase)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("client certificate for %s: %w", c.Host, err)
		}
		return cert, nil
	default:
		return tls.Certificate{}, fmt.Errorf("client certificate for %s: unknown format %q", c.Host, format)
	}
}

// parsePEMPair parses a PEM certificate chain and key, decrypting an encrypted
// key with passphrase. It never echoes the passphrase in an error.
func parsePEMPair(certPEM, keyPEM []byte, passphrase string) (tls.Certificate, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return tls.Certificate{}, errors.New("the key file holds no PEM block")
	}
	switch {
	case x509.IsEncryptedPEMBlock(block):
		if passphrase == "" {
			return tls.Certificate{}, errors.New("the private key is encrypted; enter its passphrase in Settings → Certificates")
		}
		der, err := x509.DecryptPEMBlock(block, []byte(passphrase))
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("decrypt the private key: %w", err)
		}
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: der})
	case block.Type == "ENCRYPTED PRIVATE KEY":
		if passphrase == "" {
			return tls.Certificate{}, errors.New("the private key is encrypted; enter its passphrase in Settings → Certificates")
		}
		key, err := pkcs8.ParsePKCS8PrivateKey(block.Bytes, []byte(passphrase))
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("decrypt the private key: %w", err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("encode the private key: %w", err)
		}
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse the PEM certificate and key: %w", err)
	}
	return cert, nil
}

// parsePKCS12 parses a PFX, including its CA chain. The password is never
// included in an error.
func parsePKCS12(data []byte, passphrase string) (tls.Certificate, error) {
	key, leaf, chain, err := pkcs12.DecodeChain(data, passphrase)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("open the PKCS#12 file: %w; check its passphrase", err)
	}
	if leaf == nil {
		return tls.Certificate{}, errors.New("the PKCS#12 file holds no certificate")
	}
	cert := tls.Certificate{PrivateKey: key, Leaf: leaf, Certificate: [][]byte{leaf.Raw}}
	for _, ca := range chain {
		cert.Certificate = append(cert.Certificate, ca.Raw)
	}
	return cert, nil
}

// readCertificateFile reads a local certificate-related file with a size
// bound, naming what it is in the error.
func readCertificateFile(path, kind string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read %s %s: %w", kind, filepath.Clean(path), err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %s is not a regular file", kind, filepath.Clean(path))
	}
	if info.Size() > maxFileSize {
		return nil, fmt.Errorf("%s %s is larger than 8 MiB", kind, filepath.Clean(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s %s: %w", kind, filepath.Clean(path), err)
	}
	return data, nil
}
