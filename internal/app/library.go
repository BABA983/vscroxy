package app

import (
	"bytes"
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/library"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/locales"
)

// The request library is a directory tree, one directory per collection
// and folder, so a collection can live in Git and a rename moves everything
// in it:
//
//	collections/
//	  folder.json                  order of the requests outside a collection
//	  Shop/
//	    folder.json                the collection's settings and request order
//	    List products.request.json
//	    Orders/
//	      folder.json
//	      Get order.request.json
//
// A directory's name is the collection or folder name, escaped only where a
// file system forbids a character. A request file's name follows the
// request's name, which the file itself holds, with its ID.
const (
	requestFileSuffix = ".request.json"
	folderFileName    = "folder.json"
)

// libraryState is where the library lives and what this app last read from
// or wrote to each of its files. A file whose contents differ from that has
// been changed by someone else, so the app never overwrites or deletes it.
type libraryState struct {
	root  string
	known map[string][]byte
}

// folderFile is a directory's folder.json: the settings of the collection
// or folder it is, and the order of the requests directly in it.
type folderFile struct {
	request.Folder
	Order []string `json:"order,omitempty"`
}

// cleanPath normalizes a collection path: segments trimmed, empty ones
// dropped. "" is outside any collection.
func cleanPath(p string) string {
	var parts []string
	for part := range strings.SplitSeq(p, "/") {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/")
}

// escapeName makes a collection, folder or request name a file name on
// every system, reversibly: characters a system forbids, %, a leading dot,
// trailing dots and spaces, and Windows device names become %XX.
func escapeName(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		escape := c < 0x20 || c == 0x7f || strings.IndexByte(`%/\:*?"<>|`, c) >= 0 ||
			i == 0 && c == '.' || strings.Trim(name[i:], ". ") == "" && (c == '.' || c == ' ')
		if escape {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	s := b.String()
	stem, _, _ := strings.Cut(strings.ToUpper(s), ".")
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		s = fmt.Sprintf("%%%02X", s[0]) + s[1:]
	}
	return s
}

// unescapeName reverses escapeName; a % that starts no escape stays as it is.
func unescapeName(s string) string {
	return library.UnescapeName(s)
}

// dirOf is the library directory, slash-separated, of a collection path.
func dirOf(collection string) string {
	if collection == "" {
		return ""
	}
	parts := strings.Split(collection, "/")
	for i, part := range parts {
		parts[i] = escapeName(part)
	}
	return strings.Join(parts, "/")
}

func marshalFile(v any) ([]byte, error) {
	data, err := json.Marshal(v, jsontext.WithIndent("  "))
	return append(data, '\n'), err
}

// libraryFiles lays requests and folders out as the files of the tree,
// keyed by slash-separated path relative to the root.
func libraryFiles(requests []savedRequest, folders []request.Folder) (map[string][]byte, error) {
	files := map[string][]byte{}
	settings := map[string]folderFile{}
	dirs := map[string]string{} // case-folded directory → collection path
	claim := func(collection string) error {
		for _, p := range pathPrefixes(collection) {
			key := strings.ToLower(dirOf(p))
			if other, ok := dirs[key]; ok && other != p {
				return fmt.Errorf("“%s” and “%s” differ only in letter case; rename one of them", other, p)
			}
			dirs[key] = p
		}
		return nil
	}
	for _, f := range folders {
		f.Path = cleanPath(f.Path)
		if f.Path == "" {
			continue
		}
		if err := claim(f.Path); err != nil {
			return nil, err
		}
		settings[f.Path] = folderFile{Folder: f}
	}
	taken := map[string]bool{} // case-folded request file paths
	for _, r := range requests {
		collection := cleanPath(r.Collection)
		if err := claim(collection); err != nil {
			return nil, err
		}
		dir := dirOf(collection)
		stem := escapeName(cmp.Or(r.Name, "Untitled request"))
		for n := 2; taken[strings.ToLower(path.Join(dir, stem))]; n++ {
			stem = escapeName(fmt.Sprintf("%s (%d)", cmp.Or(r.Name, "Untitled request"), n))
		}
		taken[strings.ToLower(path.Join(dir, stem))] = true
		r.Collection = "" // where the file is says that
		data, err := marshalFile(r)
		if err != nil {
			return nil, err
		}
		files[path.Join(dir, stem+requestFileSuffix)] = data
		f := settings[collection]
		f.Order = append(f.Order, stem)
		settings[collection] = f
	}
	for collection, f := range settings {
		f.Path = ""
		data, err := marshalFile(f)
		if err != nil {
			return nil, err
		}
		files[path.Join(dirOf(collection), folderFileName)] = data
	}
	return files, nil
}

// pathPrefixes are "A", "A/B" and "A/B/C" for "A/B/C".
func pathPrefixes(collection string) []string {
	var out []string
	for i, c := range collection {
		if c == '/' {
			out = append(out, collection[:i])
		}
	}
	if collection != "" {
		out = append(out, collection)
	}
	return out
}

// loadLibrary reads the tree at root. Files other than requests and
// folder.json, and names starting with a dot, are not the library's and
// are ignored. A missing root is an empty library.
func loadLibrary(root string) (requests []savedRequest, folders []request.Folder, known map[string][]byte, err error) {
	requests, folders, known, err = library.Read(root, func(data []byte, collection string) (savedRequest, error) {
		var r savedRequest
		err := json.Unmarshal(data, &r)
		r.Collection = collection
		return r, err
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if err := assignIDs(requests); err != nil {
		return nil, nil, nil, err
	}
	return requests, folders, known, nil
}

// writeLibrary saves requests and folders as the tree and makes them the
// ones in use. Only files whose contents change are written, and files the
// library no longer has are deleted; either is refused, before anything is
// touched, when someone else changed that file since this app last read it.
func (a *app) writeLibrary(requests []savedRequest, folders []request.Folder) error {
	if a.requestsProblem != "" {
		return fmt.Errorf("%s; files left untouched; repair or move them aside and reload local assets", a.requestsProblem)
	}
	requests, folders = slices.Clone(requests), slices.Clone(folders)
	for i := range requests {
		requests[i].Collection = cleanPath(requests[i].Collection)
	}
	for i := range folders {
		folders[i].Path = cleanPath(folders[i].Path)
	}
	l := &a.library
	if l.root != "" {
		files, err := libraryFiles(requests, folders)
		if err != nil {
			return err
		}
		if err := l.apply(files); err != nil {
			return err
		}
	}
	a.requests, a.folders = requests, folders
	a.requestGen++
	return nil
}

// apply makes the tree hold exactly files, as far as this app owns it.
func (l *libraryState) apply(files map[string][]byte) error {
	if l.known == nil {
		l.known = map[string][]byte{}
	}
	var writes, deletes []string
	for name, data := range files {
		if old, ok := l.known[name]; !ok || !bytes.Equal(old, data) {
			writes = append(writes, name)
		}
	}
	for name := range l.known {
		if _, ok := files[name]; !ok {
			deletes = append(deletes, name)
		}
	}
	slices.Sort(writes)
	slices.Sort(deletes)
	for _, name := range append(slices.Clone(writes), deletes...) {
		disk, err := os.ReadFile(filepath.Join(l.root, filepath.FromSlash(name)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return err
		case !bytes.Equal(disk, l.known[name]):
			return fmt.Errorf("%s was changed outside vscroxy; choose “Reload collections from disk” before saving", filepath.Join(l.root, filepath.FromSlash(name)))
		}
	}
	for _, name := range writes {
		target := filepath.Join(l.root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := store.WriteFile(target, files[name]); err != nil {
			return err
		}
		l.known[name] = files[name]
	}
	for _, name := range deletes {
		target := filepath.Join(l.root, filepath.FromSlash(name))
		if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		delete(l.known, name)
		// Remove the directories this left empty, never the root.
		for dir := filepath.Dir(target); dir != l.root && strings.HasPrefix(dir, l.root); dir = filepath.Dir(dir) {
			if os.Remove(dir) != nil {
				break
			}
		}
	}
	return nil
}

// setProjectDir keeps the request library in dir/.vscroxy/collections, or in
// the app's own collections when dir is empty. A missing folder is refused, so
// a typo cannot start a library somewhere unintended.
func (a *app) setProjectDir(dir string) {
	dir = strings.TrimSpace(dir)
	home, err := os.UserHomeDir()
	if err != nil {
		a.fail(locales.ProjectFolder(), err)
		return
	}
	if rest, ok := strings.CutPrefix(dir, "~/"); ok {
		dir = filepath.Join(home, rest)
	}
	root := filepath.Join(home, ".vscroxy", "collections")
	if dir != "" {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			a.fail(locales.ProjectFolder(), fmt.Errorf("%s is not a folder", dir))
			return
		}
		root = filepath.Join(dir, ".vscroxy", "collections")
	}
	a.projectDir, a.projectInput = dir, dir
	a.library.root = root
	a.loadRequestAssets()
	a.persistSettings()
	a.done(locales.ReloadedRequests(len(a.requests), root))
}

// loadRequestLibrary reads the library into the app.
func (a *app) loadRequestLibrary() {
	l := &a.library
	a.requestsProblem = ""
	requests, folders, known, err := loadLibrary(l.root)
	if err != nil {
		a.requests, a.folders, l.known = nil, nil, nil
		a.requestsProblem = fmt.Sprintf("Requests (%s): %v", l.root, err)
		a.requestGen++
		return
	}
	a.requests, a.folders, l.known = requests, folders, known
	a.requestGen++
}

// refreshDocuments shows what was just read in the tabs of saved requests
// that have no unsaved changes, so saving one cannot put back what changed
// on disk. A tab with changes keeps them; saving it replaces the file.
func (a *app) refreshDocuments() {
	kept := 0
	for _, d := range a.documents {
		if d.savedKey == "" || d.composeWaiting {
			continue
		}
		at := slices.IndexFunc(a.requests, func(r savedRequest) bool { return r.ID == d.assetID })
		if at < 0 {
			continue
		}
		var disk requestDocument // the editor's form of what was read
		disk.load(a, a.requests[at], a.requests[at].key())
		if reflect.DeepEqual(disk.baseline, d.baseline) {
			continue
		}
		if d.dirty() {
			kept++
			continue
		}
		d.load(a, a.requests[at], a.requests[at].key())
	}
	if kept > 0 {
		a.warn(locales.OpenTabsWarning(kept))
	}
}

// renameFolder renames the collection or folder at from, the last part of
// its path, to name: its settings, its requests and everything below it
// move together, and open tabs follow.
func (a *app) renameFolder(from, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "/") {
		return errors.New("the name must be nonempty and contain no “/”")
	}
	to := name
	if at := strings.LastIndex(from, "/"); at >= 0 {
		to = from[:at+1] + name
	}
	if to == from {
		return nil
	}
	under := func(p, root string) bool { return p == root || strings.HasPrefix(p, root+"/") }
	moved := func(p string) string { return to + strings.TrimPrefix(p, from) }
	for _, r := range a.requests {
		if under(r.Collection, to) {
			return fmt.Errorf("“%s” already exists", to)
		}
	}
	for _, f := range a.folders {
		if under(f.Path, to) {
			return fmt.Errorf("“%s” already exists", to)
		}
	}
	requests, folders := slices.Clone(a.requests), slices.Clone(a.folders)
	for i := range requests {
		if under(requests[i].Collection, from) {
			requests[i].Collection = moved(requests[i].Collection)
		}
	}
	for i := range folders {
		if under(folders[i].Path, from) {
			folders[i].Path = moved(folders[i].Path)
		}
	}
	if err := a.writeLibrary(requests, folders); err != nil {
		return err
	}
	a.moveFolderCurrent(from, to)
	for _, d := range a.documents {
		if under(d.composeCollection, from) {
			d.composeCollection = moved(d.composeCollection)
		}
		if d.savedKey != "" && under(d.baseline.Collection, from) {
			d.baseline.Collection = moved(d.baseline.Collection)
		}
	}
	for _, p := range slices.Collect(maps.Keys(a.collectionOpen)) {
		if under(p, from) {
			a.collectionOpen[moved(p)] = a.collectionOpen[p]
			delete(a.collectionOpen, p)
		}
	}
	if under(a.runner.collection, from) {
		a.runner.collection = moved(a.runner.collection)
		a.runner.load(a.requests)
	}
	return nil
}

// moveFolderCurrent follows a rename, so a collection's or folder's local
// Current values stay with it instead of a stale path.
func (a *app) moveFolderCurrent(from, to string) {
	if len(a.folderCurrent) == 0 && len(a.folderCredentials) == 0 {
		return
	}
	under := func(p, root string) bool { return p == root || strings.HasPrefix(p, root+"/") }
	next := maps.Clone(a.folderCurrent)
	if next == nil {
		next = map[string]map[string]string{}
	}
	credentials := maps.Clone(a.folderCredentials)
	if credentials == nil {
		credentials = map[string]map[string]bool{}
	}
	for p, values := range a.folderCurrent {
		if under(p, from) {
			delete(next, p)
			next[to+strings.TrimPrefix(p, from)] = values
		}
	}
	for p, values := range a.folderCredentials {
		if under(p, from) {
			delete(credentials, p)
			credentials[to+strings.TrimPrefix(p, from)] = values
		}
	}
	if err := a.saveFolderCurrent(next, credentials); err != nil {
		a.fail(locales.Variables(), err)
	}
}

// dropFolderCurrent removes the local Current values of a deleted collection
// or folder, and of everything under it.
func (a *app) dropFolderCurrent(path string) {
	if len(a.folderCurrent) == 0 && len(a.folderCredentials) == 0 {
		return
	}
	next := maps.Clone(a.folderCurrent)
	if next == nil {
		next = map[string]map[string]string{}
	}
	credentials := maps.Clone(a.folderCredentials)
	if credentials == nil {
		credentials = map[string]map[string]bool{}
	}
	for p := range a.folderCurrent {
		if within(p, path) {
			delete(next, p)
		}
	}
	for p := range a.folderCredentials {
		if within(p, path) {
			delete(credentials, p)
		}
	}
	if err := a.saveFolderCurrent(next, credentials); err != nil {
		a.fail(locales.Variables(), err)
	}
}

// saveFolderCurrent keeps the library's Current values in env.json, which is
// the only place they are written.
func (a *app) saveFolderCurrent(next map[string]map[string]string, credentials map[string]map[string]bool) error {
	f := a.localEnvs()
	f.FolderCurrent, f.FolderCredentials = next, credentials
	if err := a.saveEnvFile(f); err != nil {
		return err
	}
	a.folderCurrent, a.folderCredentials = next, credentials
	return nil
}
