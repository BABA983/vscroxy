package app

import (
	"bytes"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/applog"
	"github.com/baba983/vscroxy/internal/datadir"
)

// TestCustomDataRootOwnsEveryAppFileAndLog is the contract behind --user-data-dir
// and VSCROXY_HOME: no app file falls back to a home directory.
// useTempHome starts a test with a fresh data root: Root is cached for the
// process, as it is in a launched app, so each test that changes HOME must
// reset that cache and restore it after the environment is restored.
func useTempHome(t *testing.T, home string) {
	t.Helper()
	datadir.Reset()
	t.Cleanup(datadir.Reset)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func TestCustomDataRootOwnsEveryAppFileAndLog(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	datadir.Reset()
	t.Cleanup(datadir.Reset)
	t.Setenv(datadir.Env, root)
	useTempHome(t, home)

	a := newApp()
	if a.dataDir != root {
		t.Fatalf("dataDir = %q, want %q", a.dataDir, root)
	}
	files := map[string]string{
		"settings": a.settingsPath, "android proxy": a.androidProxyPath,
		"backups": a.backupDir, "ca": a.caPath, "rules": a.rulesPath,
		"network presets": a.network.path, "breakpoint templates": a.bpTemplatesPath,
		"ssl": a.sslPath, "collections": a.library.root, "ignore": a.ignorePath,
		"ignore apps": a.ignoreAppsPath, "allow": a.allowPath,
		"allow apps": a.allowAppsPath, "host profiles": a.profiles.dir,
		"scripts": a.scripts.dir, "addons": a.addons.dir, "env": a.envsPath,
		"cookies": a.cookiesPath, "MCP consents": a.mcpConsentsPath,
		"MCP key": a.mcpConsentsKeyPath, "vault": a.vault.path,
		"vault key": a.vault.keyPath, "control": a.controlPath,
		"OAuth tokens": a.oauthTokens.path, "views": a.views.path,
		"protobuf": a.protobuf.store, "proto types": a.protoTypesPath,
		"queries": a.queriesPath, "sessions": a.sessions.dir,
	}
	for name, path := range files {
		if path == "" {
			t.Errorf("%s has no path", name)
			continue
		}
		if !underRoot(root, path) {
			t.Errorf("%s is outside the data root: %q", name, path)
		}
	}

	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	logs, err := applog.Open(filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	slog.Info("data root contract marker")
	if err := logs.Close(); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(root, "logs", "vscroxy.log"))
	if err != nil || !bytes.Contains(written, []byte("data root contract marker")) {
		t.Fatalf("log did not land under the data root: %v", err)
	}

	if err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			t.Errorf("app wrote into home: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func underRoot(root, path string) bool {
	return path == root || strings.HasPrefix(path, root+string(os.PathSeparator))
}
