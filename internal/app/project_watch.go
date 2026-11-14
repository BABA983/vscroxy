package app

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/library"
)

// The project folder's files can change outside the app, for example when
// someone pulls from Git. The watcher checks them every two seconds and reloads
// each part that changed on disk. A part with unsaved edits waits until they are
// saved or discarded, so the edits are never replaced. A save in the meantime
// is refused, because the file changed since it was read.

// startProjectWatch replaces the watch with one for the current project folder.
// It does nothing without a window, as in tests.
func (a *app) startProjectWatch() {
	if a.projectWatchStop != nil {
		close(a.projectWatchStop)
		a.projectWatchStop = nil
	}
	a.projectPending.Store(false)
	if a.win == nil || a.projectDir == "" {
		return
	}
	stop := make(chan struct{})
	a.projectWatchStop = stop
	go a.watchProject(a.projectDir, stop)
}

// watchProject looks at the folder's fingerprint every two seconds and asks the
// UI thread to sync when it changed, or while a change waits on unsaved edits.
func (a *app) watchProject(dir string, stop <-chan struct{}) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	var seen string
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		fp := projectFingerprint(dir)
		if fp == seen && !a.projectPending.Load() {
			continue
		}
		seen = fp
		a.win.Update(func() { a.syncProject(dir) })
	}
}

// projectFingerprint names the project's files by path, size and modification
// time, so a change shows without reading any contents.
func projectFingerprint(dir string) string {
	h := sha256.New()
	_ = filepath.WalkDir(filepath.Join(dir, ".vscroxy"), func(path string, d fs.DirEntry, err error) error {
		if err == nil {
			if info, err := d.Info(); err == nil {
				fmt.Fprintf(h, "%s\x00%d\x00%d\n", path, info.Size(), info.ModTime().UnixNano())
			}
		}
		return nil
	})
	return string(h.Sum(nil))
}

// syncProject runs on the UI thread. It reloads each part that changed on disk,
// and leaves a part alone while it has unsaved edits, which marks the sync as
// pending.
func (a *app) syncProject(dir string) {
	if dir == "" || dir != a.projectDir {
		return
	}
	pending := false
	if a.libraryChangedOnDisk() {
		if a.libraryHasDrafts() {
			pending = true
		} else {
			a.loadRequestAssets()
		}
	}
	if changed, err := a.file(a.projectFile("environments.json")).Changed(); err == nil && changed {
		a.loadEnvsState()
	}
	if changed, err := a.file(a.apisPath).Changed(); err == nil && changed {
		if a.apiDrafts() {
			pending = true
		} else {
			a.loadSavedAPIs()
		}
	}
	if a.addonsChangedOnDisk() {
		if a.addonDrafts() {
			pending = true
		} else {
			a.loadScripts()
		}
	}
	a.projectPending.Store(pending)
}

// libraryChangedOnDisk compares the library's files with the bytes the app last
// read or wrote, which is what a save checks too.
func (a *app) libraryChangedOnDisk() bool {
	if a.library.root == "" {
		return false
	}
	_, _, known, err := library.Read(a.library.root, func([]byte, string) (struct{}, error) { return struct{}{}, nil })
	if err != nil || len(known) != len(a.library.known) {
		return true
	}
	for name, data := range known {
		if old, ok := a.library.known[name]; !ok || !bytes.Equal(old, data) {
			return true
		}
	}
	return false
}

// libraryHasDrafts reports whether a saved request has unsaved edits. New
// requests that were never saved are not on disk, so they cannot conflict.
func (a *app) libraryHasDrafts() bool {
	for _, d := range a.documents {
		if d.savedKey != "" && d.dirty() {
			return true
		}
	}
	return false
}

// apiDrafts reports whether an OpenAPI document has unsaved changes.
func (a *app) apiDrafts() bool {
	for _, d := range a.apis {
		if d.dirty() {
			return true
		}
	}
	return false
}

// addonDrafts reports whether an addon is open with unsaved text.
func (a *app) addonDrafts() bool {
	for _, e := range a.addons.editors {
		if e.Buffer.Modified() {
			return true
		}
	}
	return false
}

// addonsChangedOnDisk reports an addon file added, removed or changed since the
// folder was last read.
func (a *app) addonsChangedOnDisk() bool {
	s := &a.addons
	if s.dir == "" {
		return false
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return true
	}
	var names []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".js"); ok && !e.IsDir() && checkAddonName(name) == nil {
			names = append(names, name)
		}
	}
	listed := slices.Clone(s.names)
	slices.Sort(names)
	slices.Sort(listed)
	if !slices.Equal(names, listed) {
		return true
	}
	for _, name := range s.names {
		if changed, err := a.file(filepath.Join(s.dir, scriptFile(name))).Changed(); err != nil || changed {
			return true
		}
	}
	return false
}
