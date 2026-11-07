package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/google/uuid"
)

// certificateEditor is the Settings → Certificates draft. Passphrases typed
// here stay in memory; only a successful Save seals them beside the settings.
type certificateEditor struct {
	loaded, dirty bool
	rows          []certificateRow
	roots         []certificateRoot
	trustSystem   bool
	problem       string
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
		format := client.Format
		if format == "" {
			if client.KeyFile == "" {
				format = certs.FormatPKCS12
			} else {
				format = certs.FormatPEM
			}
		}
		e.rows = append(e.rows, certificateRow{
			ID: id, Host: client.Host, Format: format,
			CertFile: client.CertFile, KeyFile: client.KeyFile,
			Saved: a.certificates.Secrets[id] != "",
		})
	}
	for _, path := range a.certificates.RootCAs {
		e.roots = append(e.roots, certificateRoot{Path: path})
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
		host := certs.NormalizeHost(row.Host)
		if host == "" {
			return settings, nil, fmt.Errorf("client certificate %d: enter a host", i+1)
		}
		format := strings.ToLower(strings.TrimSpace(row.Format))
		if format == "" {
			if strings.TrimSpace(row.KeyFile) == "" {
				format = certs.FormatPKCS12
			} else {
				format = certs.FormatPEM
			}
		}
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
			a.fail("Certificates", err)
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
	a.done(fmt.Sprintf("Certificates saved · %d client %s, %d root %s",
		len(settings.Clients), plural(len(settings.Clients), "certificate", "certificates"),
		len(settings.RootCAs), plural(len(settings.RootCAs), "CA", "CAs")))
}
