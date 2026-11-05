package app

import (
	"cmp"
	"maps"
	"slices"
	"strings"
)

// paletteEntry is one row of the command palette: a command or a search
// result. detail is a tooltip and the status line; it never holds a variable
// value or a cookie value.
type paletteEntry struct {
	label   string
	detail  string
	meta    string // the shortcut of a command, or the kind of a result
	enabled bool
	why     string
	action  bool
	run     func()
}

type requestSearchEntry struct {
	key      string
	label    string
	detail   string
	haystack string
}

type pathSearchEntry struct {
	path     string
	label    string
	kind     string
	haystack string
}

// requestSearchIndex caches what the palette searches of the library. It is
// rebuilt when requestGen changes, not on every keystroke or frame.
type requestSearchIndex struct {
	gen      int
	built    bool
	requests []requestSearchEntry
	paths    []pathSearchEntry
}

func (a *app) requestSearch() requestSearchIndex {
	if a.requestIndex.built && a.requestIndex.gen == a.requestGen {
		return a.requestIndex
	}
	idx := requestSearchIndex{gen: a.requestGen, built: true}
	// A collection or folder is a path. Folders carry a description; a
	// collection implied only by its requests still gets a result.
	paths := map[string]string{}
	for _, f := range a.folders {
		paths[f.Path] = f.Description
	}
	for _, r := range a.requests {
		for _, prefix := range pathPrefixes(r.Collection) {
			if _, ok := paths[prefix]; !ok {
				paths[prefix] = ""
			}
		}
	}
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		name := path
		if _, last, ok := strings.CutLast(path, "/"); ok {
			name = last
		}
		kind := "Collection"
		if strings.Contains(path, "/") {
			kind = "Folder"
		}
		idx.paths = append(idx.paths, pathSearchEntry{
			path: path, label: name, kind: kind,
			haystack: strings.ToLower(path + "\x00" + paths[path]),
		})
	}
	for _, r := range a.requests {
		method := cmp.Or(r.Protocol, r.Method)
		detail := strings.TrimSpace(method + " " + r.URL)
		if r.Collection != "" {
			detail += " · " + r.Collection
		}
		// Request descriptions are not part of the model yet; add them here
		// when they are. Values of {{variables}} stay authored, as in the URL.
		idx.requests = append(idx.requests, requestSearchEntry{
			key: r.key(), label: cmp.Or(r.Name, "Untitled request"), detail: detail,
			haystack: strings.ToLower(r.Collection + "\x00" + r.Name + "\x00" + method + "\x00" + r.URL),
		})
	}
	a.requestIndex = idx
	return idx
}

// paletteSearchEntries finds collections, folders, requests, environments and
// variable names. Variable values are never part of a label, detail or
// haystack, so typing a secret cannot reveal that it exists.
func (a *app) paletteSearchEntries(query string) []paletteEntry {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	idx := a.requestSearch()
	var out []paletteEntry
	for _, p := range idx.paths {
		if strings.Contains(p.haystack, query) {
			path := p.path
			out = append(out, paletteEntry{
				label: p.label, detail: p.kind + " · " + path, meta: p.kind, enabled: true,
				run: func() { a.revealCollection(path) },
			})
		}
	}
	for _, r := range idx.requests {
		if !strings.Contains(r.haystack, query) {
			continue
		}
		key := r.key
		out = append(out, paletteEntry{
			label: r.label, detail: r.detail, meta: "Request", enabled: true,
			run: func() { a.openSaved(key) },
		})
	}
	envs := slices.Clone(a.envs)
	slices.SortFunc(envs, func(x, y environment) int { return strings.Compare(x.Name, y.Name) })
	for _, env := range envs {
		name := env.Name
		if strings.Contains(strings.ToLower(name), query) {
			out = append(out, paletteEntry{
				label: name, detail: "Environment · " + name, meta: "Environment", enabled: true,
				run: func() { a.openEnvironment(name) },
			})
		}
		names := map[string]bool{}
		for key := range env.Vars {
			names[key] = true
		}
		for key := range env.Current {
			names[key] = true
		}
		for _, variable := range slices.Sorted(maps.Keys(names)) {
			if !strings.Contains(strings.ToLower(variable), query) {
				continue
			}
			out = append(out, paletteEntry{
				label: variable, detail: "Variable · " + name, meta: "Variable", enabled: true,
				run: func() { a.openEnvironment(name) },
			})
		}
	}
	globals := map[string]bool{}
	for name := range a.globals {
		globals[name] = true
	}
	for name := range a.globalsCurrent {
		globals[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(globals)) {
		if !strings.Contains(strings.ToLower(name), query) {
			continue
		}
		out = append(out, paletteEntry{
			label: name, detail: "Global variable", meta: "Variable", enabled: true,
			run: func() {
				a.editGlobals()
				a.varsOpen = true
			},
		})
	}
	return out
}

// revealCollection shows a collection or folder in the sidebar without
// filtering the tree: its ancestors and itself open, and any sidebar search
// that would hide it is cleared.
func (a *app) revealCollection(path string) {
	a.page = pageRequest
	a.collectionsOpen, a.revealGroup = true, "collections"
	a.workbench.sidebarHidden = false
	a.requestQuery = ""
	for _, prefix := range pathPrefixes(path) {
		*a.collectionShown(prefix) = true
	}
}

func (a *app) openEnvironment(name string) {
	a.editEnvironment(name)
	a.varsOpen = true
}
