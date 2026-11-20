// Package certs is the client-certificate, server-certificate and extra-root
// configuration shared by authored requests and the capture engine.
// Certificate and key files are referenced by local path and read on every
// use; passphrases are held in memory only and sealed separately by
// SecretStore.
package certs

import (
	"bytes"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
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

	// SigningKeySecret is the reserved SecretStore name of the imported MITM
	// signing CA's private key. Client IDs are UUIDs, so it cannot collide.
	SigningKeySecret = "mitm-signing-ca"
	// CustomCACertFile is the public certificate of an imported signing CA,
	// kept beside the app-generated CA.
	CustomCACertFile = "custom-ca-cert.pem"
)

// SigningCA is an imported CA that signs intercepted server certificates.
// CertFile is its public certificate; the private key is sealed in
// SecretStore under SigningKeySecret and never written to settings.
type SigningCA struct {
	CertFile string `json:"certFile"`
}

// ResolveFormat returns the configured format, or infers PEM when a key file
// is present and PKCS#12 otherwise. Unknown explicit formats are returned
// unchanged so validation can name them.
func ResolveFormat(format, keyFile string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format != "" {
		return format
	}
	if strings.TrimSpace(keyFile) == "" {
		return FormatPKCS12
	}
	return FormatPEM
}

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

// Server is one host's server certificate. It is presented to the client
// during an intercepted handshake instead of a leaf minted by the MITM CA.
// Host, format and files follow the same rules as Client; ID keys the sealed
// passphrase and is not a credential.
type Server struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	Format   string `json:"format,omitempty"`
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile,omitempty"`
}

// Settings is the certificate configuration. Secrets maps a Client or Server
// ID to its passphrase and is never serialized: settings files and
// configuration exports contain only paths and hosts. SecretsProblem explains
// why the sealed passphrases could not be opened, without exposing a value.
type Settings struct {
	Clients          []Client          `json:"clients,omitempty"`
	Servers          []Server          `json:"servers,omitempty"`
	RootCAs          []string          `json:"rootCAs,omitempty"`
	TrustSystemRoots *bool             `json:"trustSystemRoots,omitempty"`
	SigningCA        *SigningCA        `json:"signingCA,omitempty"`
	Secrets          map[string]string `json:"-"`
	SecretsProblem   string            `json:"-"`
}

// Clone returns a copy that can be edited without changing s.
func (s Settings) Clone() Settings {
	out := s
	out.Clients = slices.Clone(s.Clients)
	out.Servers = slices.Clone(s.Servers)
	out.RootCAs = slices.Clone(s.RootCAs)
	if s.TrustSystemRoots != nil {
		value := *s.TrustSystemRoots
		out.TrustSystemRoots = &value
	}
	if s.SigningCA != nil {
		value := *s.SigningCA
		out.SigningCA = &value
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
	if !slices.Equal(s.Clients, other.Clients) || !slices.Equal(s.Servers, other.Servers) || !slices.Equal(s.RootCAs, other.RootCAs) {
		return false
	}
	switch {
	case s.SigningCA == nil && other.SigningCA == nil:
	case s.SigningCA == nil || other.SigningCA == nil:
		return false
	case s.SigningCA.CertFile != other.SigningCA.CertFile:
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
	return len(s.Clients) > 0 || len(s.Servers) > 0 || len(s.RootCAs) > 0 || s.TrustSystemRoots != nil || s.SigningCA != nil
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
	if out.SigningCA != nil {
		out.SigningCA.CertFile = strings.TrimSpace(out.SigningCA.CertFile)
		if out.SigningCA.CertFile == "" {
			out.SigningCA = nil
		}
	}
	for i := range out.Clients {
		c := &out.Clients[i]
		c.Host = normalizeOrTrim(c.Host)
		c.CertFile = strings.TrimSpace(c.CertFile)
		c.KeyFile = strings.TrimSpace(c.KeyFile)
		c.Format = ResolveFormat(c.Format, c.KeyFile)
		if c.ID == "" {
			c.ID = uuid.NewString()
		}
	}
	for i := range out.Servers {
		c := &out.Servers[i]
		c.Host = normalizeOrTrim(c.Host)
		c.CertFile = strings.TrimSpace(c.CertFile)
		c.KeyFile = strings.TrimSpace(c.KeyFile)
		c.Format = ResolveFormat(c.Format, c.KeyFile)
		if c.ID == "" {
			c.ID = uuid.NewString()
		}
	}
	return out
}

func normalizeOrTrim(host string) string {
	if normalized, err := normalizeHost(host); err == nil {
		return normalized
	}
	return strings.TrimSpace(strings.ToLower(host))
}

// Validate checks every configured host and loads every file, including a
// PKCS#12 or encrypted PEM with its sealed passphrase. A bad entry is an
// error; a send never silently continues without the certificate.
func (s Settings) Validate() error {
	seen := make(map[string]bool, len(s.Clients))
	for i, c := range s.Clients {
		if err := ValidateHost(c.Host); err != nil {
			return fmt.Errorf("client certificate %d: %w", i+1, err)
		}
		key := NormalizeHost(c.Host)
		if seen[key] {
			return fmt.Errorf("client certificate %d: %s is configured twice", i+1, c.Host)
		}
		seen[key] = true
		format := ResolveFormat(c.Format, c.KeyFile)
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
	seenServers := make(map[string]bool, len(s.Servers))
	for i, c := range s.Servers {
		if err := ValidateHost(c.Host); err != nil {
			return fmt.Errorf("server certificate %d: %w", i+1, err)
		}
		key := NormalizeHost(c.Host)
		if seenServers[key] {
			return fmt.Errorf("server certificate %d: %s is configured twice", i+1, c.Host)
		}
		seenServers[key] = true
		format := ResolveFormat(c.Format, c.KeyFile)
		switch format {
		case FormatPEM:
			if c.CertFile == "" || c.KeyFile == "" {
				return fmt.Errorf("server certificate for %s: PEM needs both a certificate and a key file", c.Host)
			}
		case FormatPKCS12:
			if c.CertFile == "" {
				return fmt.Errorf("server certificate for %s: choose a PKCS#12 file", c.Host)
			}
			if c.KeyFile != "" {
				return fmt.Errorf("server certificate for %s: PKCS#12 already contains its key; clear the key file", c.Host)
			}
		default:
			return fmt.Errorf("server certificate for %s: unknown format %q", c.Host, format)
		}
		if _, err := s.loadServer(c); err != nil {
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
	if s.SigningCA != nil {
		if s.SigningCA.CertFile == "" {
			return errors.New("signing CA: choose a certificate file")
		}
		data, err := readCertificateFile(s.SigningCA.CertFile, "signing CA")
		if err != nil {
			return err
		}
		block, _ := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" {
			return fmt.Errorf("signing CA %s: no PEM certificate", s.SigningCA.CertFile)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("signing CA %s: %w", s.SigningCA.CertFile, err)
		}
		if !cert.BasicConstraintsValid || !cert.IsCA {
			return fmt.Errorf("signing CA %s: certificate is not a CA", s.SigningCA.CertFile)
		}
	}
	return nil
}

// CertificateSubject returns the common name of the first PEM certificate in
// path, or "" when it cannot be read.
func CertificateSubject(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return ""
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	return cert.Subject.CommonName
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

// ServerCertificate returns the certificate to present to a client for
// address, or nil when no entry applies. The file is read now, so a changed or
// unreadable certificate fails this connection rather than falling back to a
// generated leaf.
func (s Settings) ServerCertificate(address string) (*tls.Certificate, error) {
	best, bestScore := -1, -1
	for i, c := range s.Servers {
		if value, ok := score(c.Host, address); ok && value > bestScore {
			best, bestScore = i, value
		}
	}
	if best < 0 {
		return nil, nil
	}
	cert, err := s.loadServer(s.Servers[best])
	if err != nil {
		if s.SecretsProblem != "" {
			return nil, fmt.Errorf("%w (%s)", err, s.SecretsProblem)
		}
		return nil, err
	}
	return &cert, nil
}

func (s Settings) loadServer(c Server) (tls.Certificate, error) {
	format := ResolveFormat(c.Format, c.KeyFile)
	passphrase := s.Secrets[c.ID]
	var cert tls.Certificate
	var err error
	switch format {
	case FormatPEM:
		certPEM, readErr := readCertificateFile(c.CertFile, "server certificate")
		if readErr != nil {
			return tls.Certificate{}, fmt.Errorf("server certificate for %s: %w", c.Host, readErr)
		}
		keyPEM, readErr := readCertificateFile(c.KeyFile, "server key")
		if readErr != nil {
			return tls.Certificate{}, fmt.Errorf("server certificate for %s: %w", c.Host, readErr)
		}
		cert, err = parsePEMPair(certPEM, keyPEM, passphrase)
	case FormatPKCS12:
		data, readErr := readCertificateFile(c.CertFile, "PKCS#12 server certificate")
		if readErr != nil {
			return tls.Certificate{}, fmt.Errorf("server certificate for %s: %w", c.Host, readErr)
		}
		cert, err = parsePKCS12(data, passphrase)
	default:
		return tls.Certificate{}, fmt.Errorf("server certificate for %s: unknown format %q", c.Host, format)
	}
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("server certificate for %s: %w", c.Host, err)
	}
	leaf := cert.Leaf
	if leaf == nil && len(cert.Certificate) > 0 {
		leaf, err = x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("server certificate for %s: parse the leaf: %w", c.Host, err)
		}
	}
	if leaf == nil {
		return tls.Certificate{}, fmt.Errorf("server certificate for %s: no certificate", c.Host)
	}
	if err := coversServerHost(leaf, c.Host); err != nil {
		return tls.Certificate{}, fmt.Errorf("server certificate for %s: %w", c.Host, err)
	}
	if err := keyMatchesLeaf(cert.PrivateKey, leaf); err != nil {
		return tls.Certificate{}, fmt.Errorf("server certificate for %s: %w", c.Host, err)
	}
	cert.Leaf = leaf
	return cert, nil
}

// coversServerHost checks the SAN, never the subject: a certificate for one
// name cannot be reused for a different host. A wildcard host needs the same
// wildcard SAN, and a certificate cannot cover every host at once.
func coversServerHost(leaf *x509.Certificate, pattern string) error {
	host := NormalizeHost(pattern)
	if host == "*" {
		return errors.New("a single certificate cannot cover every host; name a host or a *.parent domain")
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if wildcard, ok := strings.CutPrefix(host, "*."); ok {
		for _, name := range leaf.DNSNames {
			if strings.EqualFold(strings.TrimSuffix(name, "."), "*."+wildcard) {
				return nil
			}
		}
		return fmt.Errorf("SANs %v do not cover %s", leaf.DNSNames, pattern)
	}
	if err := leaf.VerifyHostname(host); err != nil {
		return fmt.Errorf("SANs %v do not cover %s: %w", leaf.DNSNames, pattern, err)
	}
	return nil
}

// keyMatchesLeaf checks PKCS#12's key against its leaf: DecodeChain does not
// do this itself, and presenting an unrelated key would fail the client's
// handshake much later. PEM pairs are also checked by tls.X509KeyPair.
func keyMatchesLeaf(key any, leaf *x509.Certificate) error {
	signer, ok := key.(crypto.Signer)
	if !ok {
		return errors.New("the private key cannot sign a TLS handshake")
	}
	public, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return fmt.Errorf("encode the private key's public part: %w", err)
	}
	want, err := x509.MarshalPKIXPublicKey(leaf.PublicKey)
	if err != nil {
		return fmt.Errorf("encode the certificate's public key: %w", err)
	}
	if !bytes.Equal(public, want) {
		return errors.New("the private key does not match the certificate")
	}
	return nil
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

// Transport returns an http.RoundTripper that builds a TLS configuration for
// each request's own host. A redirect therefore presents the target host's
// client certificate, never the first request's, and an HTTP request reads no
// certificate or root files at all. base is cloned per request and must not
// carry a TLSClientConfig of its own.
func (s Settings) Transport(base *http.Transport, proxyCA []byte, insecure bool) http.RoundTripper {
	return &settingsTransport{settings: s, base: base, proxyCA: proxyCA, insecure: insecure}
}

type settingsTransport struct {
	settings Settings
	base     *http.Transport
	proxyCA  []byte
	insecure bool
}

func (t *settingsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	transport := t.base.Clone()
	if req.URL != nil && strings.EqualFold(req.URL.Scheme, "https") {
		config, err := t.settings.TLSConfig(Address(req.URL), t.proxyCA, t.insecure)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = config
	} else {
		transport.TLSClientConfig = nil
	}
	defer transport.CloseIdleConnections()
	return transport.RoundTrip(req)
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
	format := ResolveFormat(c.Format, c.KeyFile)
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
