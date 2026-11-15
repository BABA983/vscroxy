package app

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/store"
)

func TestSavedFilesCarryTheirFormatVersion(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	backups := filepath.Join(dir, "backups")
	if err := writeSettings(settingsPath, settings{ProxyPort: 8899}, backups); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"version":1`)) {
		t.Fatalf("settings = %s", data)
	}
	// The second save keeps the file it replaces.
	if err := writeSettings(settingsPath, settings{ProxyPort: 8900}, backups); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(backups)
	if err != nil || len(entries) != 1 {
		t.Fatalf("backups = %v, %v", entries, err)
	}

	rulesPath := filepath.Join(dir, "rules.json")
	if err := capture.SaveRules(rulesPath, nil); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	var rulesFile struct {
		Version int   `json:"version"`
		Rules   []any `json:"rules"`
	}
	if err := json.Unmarshal(data, &rulesFile); err != nil || rulesFile.Version != store.Version || rulesFile.Rules == nil {
		t.Fatalf("rules = %s, %v", data, err)
	}
	if rules, err := capture.LoadRules(rulesPath); err != nil || len(rules) != 0 {
		t.Fatalf("load = %v, %v", rules, err)
	}

	listPath := filepath.Join(dir, "ignore.txt")
	a := &app{ignorePath: listPath}
	if err := a.writeList(listPath, []string{"telemetry.test"}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(listPath)
	if string(data) != "# vscroxy format 1\ntelemetry.test\n" {
		t.Fatalf("list = %q", data)
	}
	if hosts, _ := a.readList(listPath); len(hosts) != 1 || hosts[0] != "telemetry.test" {
		t.Fatalf("read list = %v", hosts)
	}
}

func TestANewerFileIsRefusedAndLeftUntouched(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	newer := []byte(`{"version":2,"proxyPort":9000}`)
	if err := os.WriteFile(settingsPath, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSettings(settingsPath); !errors.Is(err, store.ErrNewer) {
		t.Fatalf("load = %v, want ErrNewer", err)
	}
	if err := writeSettings(settingsPath, settings{ProxyPort: 8900}, filepath.Join(dir, "backups")); !errors.Is(err, store.ErrNewer) {
		t.Fatalf("save = %v, want ErrNewer", err)
	}
	if got, _ := os.ReadFile(settingsPath); !bytes.Equal(got, newer) {
		t.Fatalf("newer settings were replaced: %s", got)
	}

	// A rules file from a newer version is refused by the engine too.
	rulesPath := filepath.Join(dir, "rules.json")
	if err := os.WriteFile(rulesPath, []byte(`{"version":2,"rules":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := capture.LoadRules(rulesPath); err == nil || !strings.Contains(err.Error(), "newer version") {
		t.Fatalf("rules load = %v", err)
	}
	if err := capture.SaveRules(rulesPath, nil); !errors.Is(err, store.ErrNewer) {
		t.Fatalf("rules save = %v, want ErrNewer", err)
	}
	if got, _ := os.ReadFile(rulesPath); !bytes.Contains(got, []byte(`"version":2`)) {
		t.Fatalf("newer rules were replaced: %s", got)
	}
}

func TestFirstLaunchNeedsTheSettingsFileNotTheDirectory(t *testing.T) {
	dir := t.TempDir()
	// The log is opened before the app reads settings, so the directory
	// exists on a first launch.
	if err := os.MkdirAll(filepath.Join(dir, ".vscroxy", "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".vscroxy", "settings.json")
	if !firstLaunch(path) {
		t.Fatal("a first launch with only the log directory was not detected")
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if firstLaunch(path) {
		t.Fatal("a later launch was treated as the first")
	}
}
