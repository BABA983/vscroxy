package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/testcert"
)

func TestStartupReportsInvalidCertificateHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".vscroxy", "settings.json")
	if err := writeSettings(path, settings{Certificates: certs.Settings{Clients: []certs.Client{{
		ID: "one", Host: "api.example.com/v1", Format: certs.FormatPEM, CertFile: "/tmp/cert.pem", KeyFile: "/tmp/key.pem",
	}}}}); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	defer a.views.stopSearch()
	if a.srv != nil {
		defer a.srv.Close()
	}
	if !strings.Contains(a.certProblem, "is not a host") {
		t.Fatalf("startup certificate problem = %q", a.certProblem)
	}
}

func TestImportedSettingsKeepTheSecretsLoadProblem(t *testing.T) {
	a := libraryApp(t)
	a.certProblem = "certificate passphrases are unreadable"
	a.certificates.Secrets = map[string]string{"keep": "passphrase"}
	a.applySettings(settings{Certificates: certs.Settings{Clients: []certs.Client{{
		ID: "one", Host: "api.example.com", Format: certs.FormatPEM, CertFile: "/tmp/cert.pem", KeyFile: "/tmp/key.pem",
	}}}})
	if a.certificates.SecretsProblem != a.certProblem {
		t.Fatalf("secrets problem = %q, want %q", a.certificates.SecretsProblem, a.certProblem)
	}
	if a.certificates.Secrets["keep"] != "passphrase" {
		t.Fatal("imported settings discarded this computer's sealed passphrases")
	}
}

func TestSaveCertificatesSealsPassphrasesBesideSettings(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	caPath := ca.WritePEM(t, dir)
	_, _, client := ca.Issue(t, "settings-client", nil, nil, true)
	pfxPath := testcert.WriteFile(t, dir, "client.p12", ca.PFX(t, client, "save-secret"))

	a := &app{
		settingsPath: filepath.Join(dir, ".vscroxy", "settings.json"),
		certificates: certs.Settings{},
		certificateUI: certificateEditor{
			loaded: true,
			rows: []certificateRow{{
				ID: "one", Host: "api.example.com:8443", Format: certs.FormatPKCS12,
				CertFile: pfxPath, Passphrase: "save-secret",
			}},
			roots:       []certificateRoot{{Path: caPath}},
			trustSystem: false,
		},
	}
	a.saveCertificates()
	if a.certProblem != "" {
		t.Fatalf("save problem: %s", a.certProblem)
	}
	if len(a.certificates.Clients) != 1 || a.certificates.Clients[0].Host != "api.example.com:8443" || a.certificates.Secrets["one"] != "save-secret" {
		t.Fatalf("saved settings: %+v", a.certificates)
	}
	data, err := os.ReadFile(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "save-secret") {
		t.Fatalf("settings.json contains the passphrase: %s", data)
	}
	secrets, err := certs.SecretStoreFor(a.settingsPath).Load()
	if err != nil || secrets["one"] != "save-secret" {
		t.Fatalf("sealed passphrases: %v %v", secrets, err)
	}
	if a.certificateUI.dirty || a.certificateUI.loaded {
		t.Fatal("the saved draft was not reset")
	}
}
