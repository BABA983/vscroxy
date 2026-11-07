package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/testcert"
)

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
