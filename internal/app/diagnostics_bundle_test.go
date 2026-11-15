package app

import (
	"archive/zip"
	"bytes"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/internal/version"
)

func TestDiagnosticsBundleLeavesOutRequestContentsAndSecrets(t *testing.T) {
	dir := t.TempDir()
	a := workspaceApp()
	a.settingsPath = filepath.Join(dir, "settings.json")
	a.socksUsername, a.socksPassword = "socks-user", "socks-secret"
	a.saved.SOCKSOn, a.saved.SOCKSAuth = true, true
	a.flows[0].url = "https://api.example.com/projects?token=query-secret"
	a.flows[0].path = "/projects?token=query-secret"
	a.flows[0].reqBody = "request-body-secret"
	a.requests[0].URL = "https://api.example.com/projects?token=query-secret"
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "vscroxy.log"), []byte("vscroxy starting\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse("https://api.example.com/projects?token=query-secret")
	if err != nil {
		t.Fatal(err)
	}
	data, err := a.buildDiagnosticsBundle(uikit.Theme(false), target)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		contents[f.Name] = string(body)
	}
	for _, name := range []string{"version.txt", "settings.json", "diagnostics.txt", "logs/vscroxy.log"} {
		if _, ok := contents[name]; !ok {
			t.Fatalf("bundle has no %s: %v", name, keys(contents))
		}
	}
	if !strings.Contains(contents["version.txt"], version.Number) || !strings.Contains(contents["version.txt"], version.Platform()) {
		t.Fatalf("version = %q", contents["version.txt"])
	}
	if !strings.Contains(contents["diagnostics.txt"], "Listener") || !strings.Contains(contents["diagnostics.txt"], "target: https://api.example.com") {
		t.Fatalf("diagnostics = %q", contents["diagnostics.txt"])
	}
	for name, text := range contents {
		for _, secret := range []string{"socks-secret", "socks-user", "query-secret", "request-body-secret", "/projects"} {
			if strings.Contains(text, secret) {
				t.Fatalf("%s carries %q:\n%s", name, secret, text)
			}
		}
	}
	settings := contents["settings.json"]
	for _, forbidden := range []string{"socksPassword", "socksUsername", "upstream", "certificates", "projectDir", "keyLogPath"} {
		if strings.Contains(settings, forbidden) {
			t.Fatalf("settings carries %q:\n%s", forbidden, settings)
		}
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
