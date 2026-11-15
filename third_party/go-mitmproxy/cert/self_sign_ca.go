package cert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang/groupcache/lru"
	"github.com/golang/groupcache/singleflight"
	log "github.com/sirupsen/logrus"
)

// reference
// https://docs.mitmproxy.org/stable/concepts-certificates/
// https://github.com/mitmproxy/mitmproxy/blob/master/mitmproxy/certs.py

var errCaNotFound = errors.New("ca not found")

// The files and subject names of the app's own signing CA. They are exported
// so the desktop app can name the public certificate it asks clients to trust
// without repeating the strings.
const (
	// RootCAFile holds the CA certificate and its private key, mode 0600.
	RootCAFile = "vscroxy-ca.pem"
	// RootCACertFile is the public CA certificate in PEM form.
	RootCACertFile = "vscroxy-ca-cert.pem"
	// RootCACerFile is the public CA certificate in DER form.
	RootCACerFile = "vscroxy-ca-cert.cer"
	// RootCAName is the CA certificate's common name.
	RootCAName = "vscroxy CA"
	// RootCAOrg is the organization of the CA and its leaves.
	RootCAOrg = "vscroxy"
	// DefaultStoreDir is the CA directory below the user's home directory.
	// An empty CaRootPath uses it instead of the engine's old ~/.mitmproxy.
	DefaultStoreDir = ".vscroxy/ca"
)

type SelfSignCA struct {
	rsa.PrivateKey
	RootCert  x509.Certificate
	StorePath string

	cache *lru.Cache
	group *singleflight.Group

	cacheMu sync.Mutex
}

func createCert() (*rsa.PrivateKey, *x509.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano() / 100000),
		Subject: pkix.Name{
			CommonName:   RootCAName,
			Organization: []string{RootCAOrg},
		},
		NotBefore:             time.Now().Add(-time.Hour * 48),
		NotAfter:              time.Now().Add(time.Hour * 24 * 365 * 3),
		BasicConstraintsValid: true,
		IsCA:                  true,
		SignatureAlgorithm:    x509.SHA256WithRSA,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		// This CA signs intercepted server certificates and nothing else:
		// no code signing, email protection or timestamping.
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return nil, nil, err
	}

	return key, cert, nil
}

// NewSelfSignCAMemory Create new ca only live in memory, will change when process restart
func NewSelfSignCAMemory() (CA, error) {
	key, cert, err := createCert()
	if err != nil {
		return nil, err
	}
	return &SelfSignCA{
		PrivateKey: *key,
		RootCert:   *cert,
		StorePath:  "",
		cache:      lru.New(100),
		group:      new(singleflight.Group),
	}, nil
}

// NewSelfSignCA Load ca from store path or create new ca then store
func NewSelfSignCA(path string) (CA, error) {
	storePath, err := getStorePath(path)
	if err != nil {
		return nil, err
	}

	ca := &SelfSignCA{
		StorePath: storePath,
		cache:     lru.New(100),
		group:     new(singleflight.Group),
	}

	if err := ca.load(); err != nil {
		if err != errCaNotFound {
			return nil, err
		}
	} else {
		log.Debug("load root ca")
		return ca, nil
	}

	if err := ca.create(); err != nil {
		return nil, err
	}
	log.Debug("create root ca")
	return ca, nil
}

func getStorePath(path string) (string, error) {
	if path == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(homeDir, filepath.FromSlash(DefaultStoreDir))
	}

	if !filepath.IsAbs(path) {
		dir, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(dir, path)
	}

	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			// The directory holds the signing key: only this user may enter it.
			err = os.MkdirAll(path, 0o700)
			if err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	} else {
		if !stat.Mode().IsDir() {
			return "", fmt.Errorf("路径 %v 不是文件夹，请移除此文件重试", path)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return "", err
		}
	}

	return path, nil
}

// The certificate and the private key in PEM format.
func (ca *SelfSignCA) caFile() string {
	return filepath.Join(ca.StorePath, RootCAFile)
}

// The certificate in PEM format.
func (ca *SelfSignCA) caCertFile() string {
	return filepath.Join(ca.StorePath, RootCACertFile)
}

func (ca *SelfSignCA) caCertCerFile() string {
	return filepath.Join(ca.StorePath, RootCACerFile)
}

func (ca *SelfSignCA) load() error {
	caFile := ca.caFile()
	stat, err := os.Stat(caFile)
	if err != nil {
		if os.IsNotExist(err) {
			return errCaNotFound
		}
		return err
	}

	if !stat.Mode().IsRegular() {
		return fmt.Errorf("%v 不是文件", caFile)
	}

	data, err := os.ReadFile(caFile)
	if err != nil {
		return err
	}

	keyDERBlock, data := pem.Decode(data)
	if keyDERBlock == nil {
		return fmt.Errorf("%v 中不存在 PRIVATE KEY", caFile)
	}
	certDERBlock, _ := pem.Decode(data)
	if certDERBlock == nil {
		return fmt.Errorf("%v 中不存在 CERTIFICATE", caFile)
	}

	var privateKey *rsa.PrivateKey
	key, err := x509.ParsePKCS8PrivateKey(keyDERBlock.Bytes)
	if err != nil {
		// fix #14
		if strings.Contains(err.Error(), "use ParsePKCS1PrivateKey instead") {
			privateKey, err = x509.ParsePKCS1PrivateKey(keyDERBlock.Bytes)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		if v, ok := key.(*rsa.PrivateKey); ok {
			privateKey = v
		} else {
			return errors.New("found unknown rsa private key type in PKCS#8 wrapping")
		}
	}
	ca.PrivateKey = *privateKey

	x509Cert, err := x509.ParseCertificate(certDERBlock.Bytes)
	if err != nil {
		return err
	}
	ca.RootCert = *x509Cert

	// A key written by an older build, or copied from a laxer umask, must not
	// stay group- or world-readable.
	if err := os.Chmod(caFile, 0o600); err != nil {
		return err
	}

	return nil
}

func (ca *SelfSignCA) create() error {
	key, cert, err := createCert()
	if err != nil {
		return err
	}

	ca.PrivateKey = *key
	ca.RootCert = *cert

	if err := ca.save(); err != nil {
		return err
	}
	return ca.saveCert()
}

func (ca *SelfSignCA) saveTo(out io.Writer) error {
	keyBytes, err := x509.MarshalPKCS8PrivateKey(&ca.PrivateKey)
	if err != nil {
		return err
	}
	err = pem.Encode(out, &pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
	if err != nil {
		return err
	}

	return pem.Encode(out, &pem.Block{Type: "CERTIFICATE", Bytes: ca.RootCert.Raw})
}

func (ca *SelfSignCA) saveCertTo(out io.Writer) error {
	return pem.Encode(out, &pem.Block{Type: "CERTIFICATE", Bytes: ca.RootCert.Raw})
}

// writeFileAtomic writes a CA file through a temporary file in the same
// directory and renames it into place, so a reader never sees a partial key.
// The temporary file starts at 0600; a public certificate is widened to 0644
// afterwards.
func writeFileAtomic(path string, mode os.FileMode, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".vscroxy-ca-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if err := write(file); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (ca *SelfSignCA) save() error {
	return writeFileAtomic(ca.caFile(), 0o600, ca.saveTo)
}

func (ca *SelfSignCA) saveCert() error {
	if err := writeFileAtomic(ca.caCertFile(), 0o644, ca.saveCertTo); err != nil {
		return err
	}
	return writeFileAtomic(ca.caCertCerFile(), 0o644, ca.saveCertTo)
}

func (ca *SelfSignCA) GetRootCA() *x509.Certificate {
	return &ca.RootCert
}

func (ca *SelfSignCA) GetCert(commonName string) (*tls.Certificate, error) {
	ca.cacheMu.Lock()
	if val, ok := ca.cache.Get(commonName); ok {
		ca.cacheMu.Unlock()
		log.Debugf("ca GetCert: %v", commonName)
		return val.(*tls.Certificate), nil
	}
	ca.cacheMu.Unlock()

	val, err := ca.group.Do(commonName, func() (interface{}, error) {
		cert, err := ca.DummyCert(commonName)
		if err == nil {
			ca.cacheMu.Lock()
			ca.cache.Add(commonName, cert)
			ca.cacheMu.Unlock()
		}
		return cert, err
	})

	if err != nil {
		return nil, err
	}

	return val.(*tls.Certificate), nil
}

// TODO: 是否应该支持多个 SubjectAltName
func (ca *SelfSignCA) DummyCert(commonName string) (*tls.Certificate, error) {
	log.Debugf("ca DummyCert: %v", commonName)
	// A CONNECT target or SNI may carry a port or IPv6 brackets; a
	// certificate names the bare host.
	name := commonName
	if host, _, err := net.SplitHostPort(name); err == nil {
		name = host
	}
	name = strings.Trim(name, "[]")
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano() / 100000),
		Subject: pkix.Name{
			CommonName:   name,
			Organization: []string{RootCAOrg},
		},
		NotBefore:          time.Now().Add(-time.Hour * 48),
		NotAfter:           time.Now().Add(time.Hour * 24 * 365),
		SignatureAlgorithm: x509.SHA256WithRSA,
		ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	if ip := net.ParseIP(name); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{name}
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, template, &ca.RootCert, &ca.PrivateKey.PublicKey, &ca.PrivateKey)
	if err != nil {
		return nil, err
	}

	cert := &tls.Certificate{
		Certificate: [][]byte{certBytes},
		PrivateKey:  &ca.PrivateKey,
	}

	return cert, nil
}
