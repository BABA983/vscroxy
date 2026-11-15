package app

import (
	"path/filepath"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/store"
)

// file is the guarded file at path. Every reader and writer of one path
// shares it, so a save knows what the app last read there and refuses to
// replace a change made on disk since. Used on the UI thread only.
//
// settings.json and sessions are not guarded: the app saves them as it goes,
// so they are its own; settings are written with store.WriteFileVersioned and
// sessions carry their version in the file itself.
func (a *app) file(path string) *store.File {
	if a.files == nil {
		a.files = map[string]*store.File{}
	}
	f := a.files[path]
	if f == nil {
		// What is on disk now is the baseline: a save from a screen that
		// never read the file (the proxy failed to start) still works.
		f = &store.File{Path: path, Format: formatFor(path), BackupDir: a.backupDir}
		f.Read()
		a.files[path] = f
	}
	return f
}

// formatFor names the format of a guarded file by its name. The names are the
// app's own; a file with a name not listed here has no format version.
func formatFor(path string) store.Formatter {
	switch filepath.Base(path) {
	case "rules.json":
		return store.Rules
	case "ssl.json":
		return store.SSL
	case "vault.json":
		return store.Vault
	case "env.json":
		return store.Env
	case "environments.json":
		return store.ProjectEnv
	case "views.json":
		return store.Views
	case "apis.json":
		return store.APIs
	case "breakpoint-templates.json":
		return store.Breakpoints
	case "network-presets.json":
		return store.NetworkPresets
	case "profiles.json":
		return store.HostsProfiles
	case "ignore.txt", "ignore-apps.txt", "allow.txt", "allow-apps.txt":
		return store.Lists
	}
	return nil
}

// readList reads a list of hosts or apps, one per line.
func (a *app) readList(path string) ([]string, error) {
	data, err := a.file(path).Read()
	return capture.ParseIgnore(data), err
}

// writeList writes a list of hosts or apps where the next start reads it.
func (a *app) writeList(path string, lines []string) error {
	text := strings.Join(lines, "\n")
	if text != "" {
		text += "\n"
	}
	return a.file(path).Write([]byte(text))
}

// writeRules writes the rules file; callers apply the rules after it succeeds.
func (a *app) writeRules(rules []capture.Rule) error {
	data, err := capture.MarshalRules(rules)
	if err != nil {
		return err
	}
	return a.file(a.rulesPath).Write(data)
}

// reloadFromDisk reads every guarded file again, after a save found one
// changed on disk. Unsaved edits in open request tabs are kept.
func (a *app) reloadFromDisk() {
	a.reloadConfig()
	a.loadRequestAssets()
	a.views.load(a)
	a.vault.loaded = false
}

// readSSL reads ssl.json. One that does not parse stays read-only.
func (a *app) readSSL() (capture.SSLConfig, error) {
	f := a.file(a.sslPath)
	data, err := f.Read()
	if err != nil {
		return capture.SSLConfig{}, err
	}
	cfg, err := capture.ParseSSL(data)
	if err != nil {
		f.Fail(err)
	}
	return cfg, err
}

// readRules reads the rules file. One that does not parse stays read-only.
func (a *app) readRules() ([]capture.Rule, error) {
	f := a.file(a.rulesPath)
	data, err := f.Read()
	if err != nil {
		return nil, err
	}
	rules, err := capture.ParseRules(data)
	if err != nil {
		f.Fail(err)
	}
	return rules, err
}
