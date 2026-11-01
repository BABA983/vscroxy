package app

import (
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/store"
)

// file is the guarded file at path. Every reader and writer of one path
// shares it, so a save knows what the app last read there and refuses to
// replace a change made on disk since. Used on the UI thread only.
//
// settings.json, sessions and the experiment log are not guarded: the app
// saves them as it goes, so they are its own, written with store.WriteFile.
func (a *app) file(path string) *store.File {
	if a.files == nil {
		a.files = map[string]*store.File{}
	}
	f := a.files[path]
	if f == nil {
		// What is on disk now is the baseline: a save from a screen that
		// never read the file (the proxy failed to start) still works.
		f = &store.File{Path: path}
		f.Read()
		a.files[path] = f
	}
	return f
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
