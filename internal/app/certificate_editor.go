package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/locales"
	"github.com/google/uuid"
)

// certificateEditor is the Settings → Certificates draft. Passphrases typed
// here stay in memory; only a successful Save seals them beside the settings.
type certificateEditor struct {
	loaded, dirty bool
	rows          []certificateRow
	roots         []certificateRoot
	open          map[string]bool
	trustSystem   bool
	// signingCert and signingKey are the imported CA files this session.
	// signingSaved means a custom signing CA is active; signingClear asks Save
	// to restore the app-generated CA.
	signingCert, signingKey    string
	signingSaved, signingClear bool
	signingSubject             string
	problem                    string
}

type certificateRow struct {
	ID, Host, Format, CertFile, KeyFile string
	// Passphrase is what the user typed this session. Saved says a sealed
	// passphrase exists; Clear asks Save to remove it.
	Passphrase string
	Saved      bool
	Clear      bool
}

type certificateRoot struct {
	Path string
}

// load fills the draft from the active settings unless the user has unsaved
// edits. Saving or importing settings resets loaded.
func (e *certificateEditor) load(a *app) {
	if e.loaded {
		return
	}
	e.rows, e.roots = nil, nil
	for _, client := range a.certificates.Clients {
		id := client.ID
		if id == "" {
			id = uuid.NewString()
		}
		format := certs.ResolveFormat(client.Format, client.KeyFile)
		e.rows = append(e.rows, certificateRow{
			ID: id, Host: client.Host, Format: format,
			CertFile: client.CertFile, KeyFile: client.KeyFile,
			Saved: a.certificates.Secrets[id] != "",
		})
	}
	for _, path := range a.certificates.RootCAs {
		e.roots = append(e.roots, certificateRoot{Path: path})
	}
	e.signingCert, e.signingKey = "", ""
	e.signingSaved, e.signingClear, e.signingSubject = false, false, ""
	if a.certificates.SigningCA != nil {
		e.signingCert = a.certificates.SigningCA.CertFile
		e.signingSaved = true
		e.signingSubject = certs.CertificateSubject(e.signingCert)
	}
	e.trustSystem = a.certificates.TrustsSystem()
	e.loaded = true
}

// build validates the draft and returns the non-secret settings plus the
// sealed passphrases to keep. A validation failure keeps the draft visible.
func (e *certificateEditor) build(a *app) (certs.Settings, map[string]string, error) {
	var settings certs.Settings
	secrets := map[string]string{}
	for i, row := range e.rows {
		if err := certs.ValidateHost(row.Host); err != nil {
			return settings, nil, fmt.Errorf("client certificate %d: %w", i+1, err)
		}
		host := certs.NormalizeHost(row.Host)
		format := certs.ResolveFormat(row.Format, row.KeyFile)
		certFile := strings.TrimSpace(row.CertFile)
		if certFile == "" {
			return settings, nil, fmt.Errorf("client certificate for %s: choose a certificate file", host)
		}
		keyFile := strings.TrimSpace(row.KeyFile)
		switch format {
		case certs.FormatPEM:
			if keyFile == "" {
				return settings, nil, fmt.Errorf("client certificate for %s: choose a key file", host)
			}
		case certs.FormatPKCS12:
			keyFile = ""
		default:
			return settings, nil, fmt.Errorf("client certificate for %s: choose PEM or PKCS#12", host)
		}
		id := strings.TrimSpace(row.ID)
		if id == "" {
			id = uuid.NewString()
		}
		settings.Clients = append(settings.Clients, certs.Client{
			ID: id, Host: host, Format: format, CertFile: certFile, KeyFile: keyFile,
		})
		switch {
		case row.Clear:
		case row.Passphrase != "":
			secrets[id] = row.Passphrase
		default:
			if saved, ok := a.certificates.Secrets[id]; ok && saved != "" {
				secrets[id] = saved
			}
		}
	}
	for _, root := range e.roots {
		if path := strings.TrimSpace(root.Path); path != "" {
			settings.RootCAs = append(settings.RootCAs, path)
		}
	}
	switch {
	case e.signingClear:
		delete(secrets, certs.SigningKeySecret)
	case e.signingCert != "" || e.signingKey != "":
		certFile, keyFile := strings.TrimSpace(e.signingCert), strings.TrimSpace(e.signingKey)
		if certFile == "" || keyFile == "" {
			return settings, nil, errors.New("signing CA: choose both the certificate and the private key")
		}
		certPEM, err := readPEMFile(certFile)
		if err != nil {
			return settings, nil, fmt.Errorf("signing CA: %w", err)
		}
		keyPEM, err := readPEMFile(keyFile)
		if err != nil {
			return settings, nil, fmt.Errorf("signing CA: %w", err)
		}
		if err := capture.ValidateSigningCA(certPEM, keyPEM); err != nil {
			return settings, nil, fmt.Errorf("signing CA: %w", err)
		}
		dir := a.caDirectory()
		if dir == "" {
			return settings, nil, errors.New("signing CA: the CA directory is unavailable")
		}
		customPath := filepath.Join(dir, certs.CustomCACertFile)
		if err := store.WriteFile(customPath, certPEM); err != nil {
			return settings, nil, fmt.Errorf("signing CA: %w", err)
		}
		settings.SigningCA = &certs.SigningCA{CertFile: customPath}
		secrets[certs.SigningKeySecret] = string(keyPEM)
	case e.signingSaved && a.certificates.SigningCA != nil:
		settings.SigningCA = &certs.SigningCA{CertFile: a.certificates.SigningCA.CertFile}
		if saved := a.certificates.Secrets[certs.SigningKeySecret]; saved != "" {
			secrets[certs.SigningKeySecret] = saved
		}
	}
	trust := e.trustSystem
	settings.TrustSystemRoots = &trust
	settings = settings.Normalize()
	settings.Secrets = secrets
	if err := settings.Validate(); err != nil {
		return settings, nil, err
	}
	return settings, secrets, nil
}

// saveCertificates seals the passphrases, saves the settings and hands the
// snapshot to the running proxy.
func (a *app) saveCertificates() {
	e := &a.certificateUI
	settings, secrets, err := e.build(a)
	if err != nil {
		e.problem, a.certProblem = err.Error(), err.Error()
		return
	}
	settings.Secrets = secrets
	if a.settingsPath != "" {
		if err := certs.SecretStoreFor(a.settingsPath).Save(secrets); err != nil {
			e.problem = err.Error()
			a.fail(locales.Certificates(), err)
			return
		}
		a.certificates = settings
		a.lastSave = time.Time{}
		if err := a.persistSettings(); err != nil {
			e.problem = err.Error()
			return
		}
	} else {
		a.certificates = settings
		a.saved.Certificates = settings
	}
	e.problem, a.certProblem = "", ""
	e.loaded, e.dirty = false, false
	if a.srv != nil {
		a.srv.SetCertificates(a.certificates)
	}
	if err := a.applySigningCA(); err != nil {
		e.problem, a.certProblem = err.Error(), err.Error()
		a.fail(locales.Certificates(), err)
		return
	}
	a.checkCertificate(false)
	a.done(locales.CertificatesSaved(len(settings.Clients), len(settings.RootCAs)))
}

// readPEMFile reads a certificate or key file, bounded like the certificate
// settings reader.
func readPEMFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > 8<<20 {
		return nil, fmt.Errorf("%s is larger than 8 MiB", path)
	}
	return data, nil
}

// caDirectory is the app's CA directory, even before capture starts.
func (a *app) caDirectory() string {
	if a.caPath != "" {
		return filepath.Dir(a.caPath)
	}
	if home, err := os.UserHomeDir(); err == nil {
		return capture.CADir(home)
	}
	return ""
}

// applySigningCA hands the imported CA to the proxy, or restores the generated
// one, and points a.caPath at the public certificate clients must trust.
func (a *app) applySigningCA() error {
	if a.certificates.SigningCA == nil {
		if a.srv != nil {
			if err := a.srv.SetSigningCA(nil, nil); err != nil {
				return err
			}
		}
		if a.caPath != "" {
			a.caPath = filepath.Join(filepath.Dir(a.caPath), capture.CACertFile)
		}
		return nil
	}
	certPEM, err := readPEMFile(a.certificates.SigningCA.CertFile)
	if err != nil {
		return err
	}
	keyPEM := a.certificates.Secrets[certs.SigningKeySecret]
	if keyPEM == "" {
		return errors.New("signing CA: the sealed private key is missing; import the certificate and key again")
	}
	if err := capture.ValidateSigningCA(certPEM, []byte(keyPEM)); err != nil {
		return err
	}
	if a.srv != nil {
		if err := a.srv.SetSigningCA(certPEM, []byte(keyPEM)); err != nil {
			return err
		}
	}
	a.caPath = a.certificates.SigningCA.CertFile
	return nil
}
