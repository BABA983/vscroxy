package request

import (
	"iter"
	"maps"
	"slices"
	"strings"
)

// AuthInherit is the Auth type that takes the nearest folder's auth, or the
// collection's, as Postman's "Inherit auth from parent" does.
const AuthInherit = "Inherit"

// Folder is what a collection, or a folder inside one, gives the requests
// under it: auth they can inherit, scripts that run around theirs and
// variables they read. Path is the collection label requests carry: "Shop"
// for a collection and "Shop/Orders" for a folder in it.
type Folder struct {
	Path        string  `json:"path,omitempty"`
	Description string  `json:"description,omitempty"`
	Auth        Auth    `json:"auth,omitzero"`
	Pre         string  `json:"pre,omitempty"`
	Post        string  `json:"post,omitempty"`
	Variables   []Field `json:"variables,omitempty"`
}

// Context is what a request inherits from where it lives and from the
// workspace. The zero Context inherits nothing.
type Context struct {
	// Folders are the folders the request lives under, the collection first.
	Folders []Folder
	// Globals are the workspace's variables, the lowest scope.
	Globals map[string]string
	// Vault reads {{vault:name}} for a request to host; nil has no vault.
	Vault func(name, host string) (string, error)
	// ScriptVault lets scripts read and change the vault with pm.vault.
	ScriptVault bool
	// Tokens keeps OAuth 2.0 access tokens; nil keeps them for one send.
	Tokens *Tokens

	// The run the request is part of: the active environment's name, the
	// Runner iteration (from 0) of how many, and the pm.variables values
	// earlier requests of the run set.
	Environment           string
	Iteration, Iterations int
	Locals                map[string]string
	// Data is the Runner data file's row for this iteration, over the
	// environment and under pm.variables, as in Postman.
	Data map[string]string
}

// scriptRun is the scope state the scripts of r's send start from.
func (c Context) scriptRun(r Request, vars map[string]string) *scriptRun {
	run := &scriptRun{
		vars: maps.Clone(vars), locals: maps.Clone(c.Locals), globals: maps.Clone(c.Globals), data: c.Data,
		collection: map[string]string{}, folders: map[string]string{}, phase: "setup",
		info: scriptInfo{Iteration: c.Iteration, Iterations: c.Iterations, RequestName: r.Name, RequestID: r.ID, Environment: c.Environment},
	}
	if run.vars == nil {
		run.vars = map[string]string{}
	}
	if run.locals == nil {
		run.locals = map[string]string{}
	}
	for prefix := range pathPrefixes(r.Collection) {
		run.root = prefix
		break
	}
	for part := range strings.SplitSeq(strings.Trim(r.Collection, "/"), "/") {
		if part != "" {
			run.info.Location = append(run.info.Location, part)
		}
	}
	run.info.Location = append(run.info.Location, r.Name)
	for _, f := range c.Folders {
		scope := run.folders
		if f.Path == run.root {
			scope = run.collection
		}
		for _, v := range f.Variables {
			if name := strings.TrimSpace(v.Name); name != "" && !v.Disabled {
				scope[name] = v.Value
			}
		}
	}
	if c.ScriptVault {
		run.vault = c.Vault
	}
	return run
}

// Apply returns c as the next request of a run sees it after a script made
// writes: changed globals, and changed variables of the collection a
// request filed under collection lives in. Vault writes are the app's.
func (c Context) Apply(writes []Write, collection string) Context {
	var root string
	for prefix := range pathPrefixes(collection) {
		root = prefix
		break
	}
	globals, folders := false, false
	for _, w := range writes {
		switch {
		case w.Scope == "global":
			if !globals {
				c.Globals, globals = maps.Clone(c.Globals), true
				if c.Globals == nil {
					c.Globals = map[string]string{}
				}
			}
			if w.Unset {
				delete(c.Globals, w.Name)
			} else {
				c.Globals[w.Name] = w.Value
			}
		case w.Scope == "collection" && w.Path == root:
			if !folders {
				c.Folders, folders = slices.Clone(c.Folders), true
			}
			if len(c.Folders) == 0 || c.Folders[0].Path != root {
				c.Folders = slices.Insert(c.Folders, 0, Folder{Path: root})
			}
			c.Folders[0].Variables = w.Fields(c.Folders[0].Variables)
		}
	}
	return c
}

// Chain returns the folders a request filed under path lives in, the
// collection first: "Shop", then "Shop/Orders" for "Shop/Orders".
func Chain(folders []Folder, path string) []Folder {
	var chain []Folder
	for prefix := range pathPrefixes(path) {
		for _, f := range folders {
			if f.Path == prefix {
				chain = append(chain, f)
				break
			}
		}
	}
	return chain
}

// pathPrefixes yields "A", "A/B", "A/B/C" for "A/B/C".
func pathPrefixes(path string) iter.Seq[string] {
	return func(yield func(string) bool) {
		path = strings.Trim(path, "/")
		if path == "" {
			return
		}
		for i := 0; ; {
			next := strings.IndexByte(path[i:], '/')
			if next < 0 {
				yield(path)
				return
			}
			if !yield(path[:i+next]) {
				return
			}
			i += next + 1
		}
	}
}

// InheritAuth replaces Inherit with the auth of the nearest folder that
// sets one, None when no folder does. from names that folder.
func (c Context) InheritAuth(auth Auth) (resolved Auth, from string) {
	if auth.Type != AuthInherit {
		return auth, ""
	}
	for i := len(c.Folders) - 1; i >= 0; i-- {
		if t := c.Folders[i].Auth.Type; t != "" && t != AuthInherit {
			return c.Folders[i].Auth, c.Folders[i].Path
		}
	}
	return Auth{Type: "None"}, ""
}

// inherited are the values below the environment: globals, then each folder
// from the collection down, so the nearest folder wins. sources says where
// each value came from.
func (c Context) inherited() (values, sources map[string]string) {
	values, sources = maps.Clone(c.Globals), map[string]string{}
	if values == nil {
		values = map[string]string{}
	}
	for name := range values {
		sources[name] = "Global"
	}
	for _, f := range c.Folders {
		for _, v := range f.Variables {
			if name := strings.TrimSpace(v.Name); name != "" && !v.Disabled {
				values[name], sources[name] = v.Value, "Collection: "+f.Path
			}
		}
	}
	return values, sources
}

// Values are the variables a request filed in c sees with the environment's
// vars: vars over the nearest folder's, over the collection's, over globals.
// Dynamic and vault values are generated or read only by a send.
func (c Context) Values(vars map[string]string) map[string]string {
	values, _ := c.inherited()
	maps.Copy(values, vars)
	return values
}

// vaultName is the secret a {{vault:name}} reference reads.
func vaultName(variable string) (string, bool) {
	name, ok := strings.CutPrefix(variable, "vault:")
	return strings.TrimSpace(name), ok
}

// sourceOf names the scope a variable of this request resolves from, or ""
// when nothing defines it.
func sourceOf(name string, vars, data, inherited, sources map[string]string, written map[string]bool) string {
	if _, ok := vaultName(name); ok {
		return "Vault"
	}
	if _, ok := vars[name]; ok {
		if written[name] {
			return "Pre-request script"
		}
		if _, ok := data[name]; ok {
			return "Data file"
		}
		return "Run input"
	}
	if _, ok := inherited[name]; ok {
		return sources[name]
	}
	if dynamic[name] != nil {
		return "Dynamic"
	}
	return ""
}

// ResolveVariables lists the variables r uses, with each one's value and
// scope as a send would see them before its pre-request scripts.
func (r Request) ResolveVariables(in Context, vars map[string]string, written map[string]bool) []ResolvedVariable {
	r.Auth, _ = in.InheritAuth(r.Auth)
	inherited, sources := in.inherited()
	var rows []ResolvedVariable
	for _, name := range r.Variables() {
		row := ResolvedVariable{Name: name, Source: sourceOf(name, vars, in.Data, inherited, sources, written)}
		switch row.Source {
		case "":
			row.Source, row.Missing = "Missing", true
		case "Vault", "Dynamic":
		default:
			if value, ok := vars[name]; ok {
				row.Value = value
			} else {
				row.Value = inherited[name]
			}
		}
		rows = append(rows, row)
	}
	return rows
}
