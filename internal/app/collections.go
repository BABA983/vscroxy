package app

import (
	"cmp"
	"errors"
	"github.com/baba983/vscroxy/internal/uikit"
	"maps"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/vault"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// folderEditor edits one collection's or folder's settings as a draft:
// Cancel leaves the saved settings, and the requests using them, alone.
type folderEditor struct {
	open            bool
	draft           request.Folder
	variables       []variableRow
	name            string // the last part of draft.Path, as edited
	tab             int    // auth, scripts, variables, description, request defaults
	script          int    // pre-request, post-response
	pre, post       *editor.State
	preFor, postFor string
	problem         string
	// settings are the request defaults this level sets; timeout and
	// maxRedirects are their inputs, seeded from the effective values.
	settings     request.Settings
	timeout      float64
	maxRedirects float64
}

// inheritance is what a request filed under path takes from its collection,
// folders and the globals. Each level's Current values override its Initial
// values locally. The vault joins it only for a send (sendContext).
func (a *app) inheritance(path string) request.Context {
	folders := request.Chain(a.folders, path)
	for i := range folders {
		folders[i].Variables = a.folderVariables(folders[i].Path, folders[i].Variables)
	}
	return request.Context{Folders: folders, Globals: a.globalValues(), GlobalsInitial: maps.Clone(a.globals), Addons: a.addons.sources()}
}

// folderVariables resolves one level's Initial rows and local Current values.
// A Current name without an Initial row still defines a local variable.
func (a *app) folderVariables(path string, initial []request.Variable) []request.Variable {
	vars := slices.Clone(initial)
	current := a.folderCurrent[path]
	seen := map[string]bool{}
	for i := range vars {
		name := strings.TrimSpace(vars[i].Name)
		seen[name] = true
		vars[i].Current = current[name]
	}
	for _, name := range slices.Sorted(maps.Keys(current)) {
		if name == "" || seen[name] {
			continue
		}
		vars = append(vars, request.Variable{Name: name, Current: current[name], Local: true})
	}
	return vars
}

// ancestorInheritance is what a level takes from above it, without its own
// saved settings, so its editor can preview a draft.
func (a *app) ancestorInheritance(path string) request.Context {
	parent, _, found := strings.CutLast(path, "/")
	if !found {
		parent = ""
	}
	return a.inheritance(parent)
}

// openFolder edits the settings of the collection or folder at path.
func (a *app) openFolder(path string) {
	f := &a.folderUI
	*f = folderEditor{open: true, draft: request.Folder{Path: path}, name: path[strings.LastIndex(path, "/")+1:]}
	if i := slices.IndexFunc(a.folders, func(folder request.Folder) bool { return folder.Path == path }); i >= 0 {
		f.draft = a.folders[i]
		f.draft.Variables = slices.Clone(f.draft.Variables)
	}
	f.variables = a.folderVariableRows(path)
	f.draft.Auth.Type = cmp.Or(f.draft.Auth.Type, a.folderAuthTypes(path)[0])
	f.draft.Auth.Location = cmp.Or(f.draft.Auth.Location, "Header")
	f.settings = f.draft.Settings.Clone()
	effective := a.ancestorInheritance(path).InheritSettings(f.settings)
	f.timeout, f.maxRedirects = effective.Timeout().Seconds(), float64(effective.RedirectLimit())
}

// applyWrites saves what scripts changed beyond the environment: globals,
// collection variables and vault secrets. Script writes are Current values:
// global and collection Initial values stay as their shared files have them.
// Each store saves once.
func (a *app) applyWrites(writes []request.Write) {
	if len(writes) == 0 {
		return
	}
	globals, folderCurrent, folderCredentials, secrets := maps.Clone(a.globalsCurrent), maps.Clone(a.folderCurrent), maps.Clone(a.folderCredentials), slices.Clone(a.vault.secrets)
	var touched [3]bool
	for _, w := range writes {
		switch w.Scope {
		case "global":
			if globals == nil {
				globals = map[string]string{}
			}
			if w.Unset {
				delete(globals, w.Name)
			} else {
				globals[w.Name] = w.Value
			}
			touched[0] = true
		case "collection":
			if folderCurrent == nil {
				folderCurrent = map[string]map[string]string{}
			}
			values := folderCurrent[w.Path]
			if values == nil {
				values = map[string]string{}
			} else {
				values = maps.Clone(values)
			}
			if w.Unset {
				delete(values, w.Name)
				if marked := folderCredentials[w.Path]; marked != nil {
					delete(marked, w.Name)
					if len(marked) == 0 {
						delete(folderCredentials, w.Path)
					}
				}
			} else {
				values[w.Name] = w.Value
			}
			if len(values) == 0 {
				delete(folderCurrent, w.Path)
			} else {
				folderCurrent[w.Path] = values
			}
			touched[1] = true
		case "vault":
			at := slices.IndexFunc(secrets, func(s vault.Secret) bool { return s.Name == w.Name })
			switch {
			case w.Unset && at >= 0:
				secrets = slices.Delete(secrets, at, at+1)
			case !w.Unset && at >= 0:
				secrets[at].Value = w.Value
			case !w.Unset:
				secrets = append(secrets, vault.Secret{Name: w.Name, Value: w.Value})
			}
			touched[2] = true
		}
	}
	var problems []string
	if touched[0] || touched[1] {
		if a.envsProblem != "" {
			problems = append(problems, "variables: "+a.envsProblem)
		} else {
			f := a.localEnvs()
			f.GlobalsCurrent, f.FolderCurrent, f.FolderCredentials = globals, folderCurrent, folderCredentials
			if err := a.saveEnvFile(f); err != nil {
				problems = append(problems, "variables: "+err.Error())
			} else {
				a.globalsCurrent, a.folderCurrent, a.folderCredentials = globals, folderCurrent, folderCredentials
			}
		}
	}
	if touched[2] {
		if !a.vault.loaded {
			problems = append(problems, "vault: it is locked")
		} else if err := a.writeVault(secrets); err != nil {
			problems = append(problems, "vault: "+err.Error())
		}
	}
	if len(problems) > 0 {
		a.warn(locales.ScriptChangesNotSaved(strings.Join(problems, "; ")))
	}
}

// folderSettingsActions offers the settings of every level a request filed
// under path lives in: its collection, then each folder down to path.
func (a *app) folderSettingsActions(path string) []action {
	var actions []action
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "" {
			return actions
		}
		label := "Collection “" + part + "” settings…"
		if i > 0 {
			label = "Folder “" + part + "” settings…"
		}
		actions = append(actions, do("collection.settings", label, func() { a.openFolder(strings.Join(parts[:i+1], "/")) }).in("edit"))
	}
	return append(actions, do("collection.export-postman", "Export “"+parts[0]+"” as Postman…", func() { a.exportPostman(parts[0]) }).in("copy"))
}

// settingsLabel names the settings of the level path is: its collection's
// for "Shop", its folder's for "Shop/Orders".
func settingsLabel(path string) string {
	if strings.Contains(path, "/") {
		return "Folder settings…"
	}
	return "Collection settings…"
}

// collectionRoot is the collection a path is in: "Shop" for "Shop/Orders".
func collectionRoot(path string) string {
	root, _, _ := strings.Cut(strings.Trim(path, "/"), "/")
	return root
}

// folderAuthTypes are a folder's choices: a folder inside a collection can
// inherit from above it, a collection has nothing above it.
func (a *app) folderAuthTypes(path string) []string {
	if strings.Contains(path, "/") {
		return append([]string{request.AuthInherit}, authTypes...)
	}
	return authTypes
}

// saveFolder keeps the draft, replacing the folder's saved settings. Its
// Initial values go to the library; its Current values stay in env.json.
func (a *app) saveFolder() {
	f := &a.folderUI
	vars, current, credentials, err := folderVariablesOf(f.variables)
	if err != nil {
		f.problem = err.Error()
		return
	}
	if name := strings.TrimSpace(f.name); name != f.draft.Path[strings.LastIndex(f.draft.Path, "/")+1:] {
		from := f.draft.Path
		if err := a.renameFolder(from, name); err != nil {
			f.problem = "Not renamed: " + err.Error()
			return
		}
		f.draft.Path = from[:strings.LastIndex(from, "/")+1] + name
	}
	draft := f.draft
	draft.Settings = f.settings
	draft.Variables = vars
	if draft.Auth.Type == request.AuthInherit || draft.Auth.Type == "None" && !strings.Contains(draft.Path, "/") {
		draft.Auth = request.Auth{Type: draft.Auth.Type}
	}
	folders := slices.DeleteFunc(slices.Clone(a.folders), func(folder request.Folder) bool { return folder.Path == draft.Path })
	if err := a.writeFolders(append(folders, draft)); err != nil {
		f.problem = "Save failed: " + err.Error()
		return
	}
	next := maps.Clone(a.folderCurrent)
	if next == nil {
		next = map[string]map[string]string{}
	}
	if len(current) == 0 {
		delete(next, draft.Path)
	} else {
		next[draft.Path] = current
	}
	nextCredentials := maps.Clone(a.folderCredentials)
	if nextCredentials == nil {
		nextCredentials = map[string]map[string]bool{}
	}
	if len(credentials) == 0 {
		delete(nextCredentials, draft.Path)
	} else {
		nextCredentials[draft.Path] = credentials
	}
	envs := a.localEnvs()
	envs.FolderCurrent, envs.FolderCredentials = next, nextCredentials
	if err := a.saveEnvFile(envs); err != nil {
		f.problem = "Settings saved, but local Current values were not: " + err.Error()
		return
	}
	a.folderCurrent, a.folderCredentials = next, nextCredentials
	f.open = false
	a.done(locales.SavedSettingsFor(draft.Path))
}

// folderVariablesOf splits edited rows into the Initial rows the library
// carries and the Current values kept locally. A Current name with no Initial
// row stays local until the reader persists it.
func folderVariablesOf(rows []variableRow) ([]request.Variable, map[string]string, map[string]bool, error) {
	var vars []request.Variable
	current, credentials := map[string]string{}, map[string]bool{}
	seen := map[string]bool{}
	for _, r := range rows {
		name := strings.TrimSpace(r.Name)
		if name == "" && !r.HasInitial && !r.HasCurrent && r.Initial == "" && r.Current == "" {
			continue
		}
		if name == "" || strings.ContainsAny(name, "{}") {
			return nil, nil, nil, errors.New("variable names must be nonempty and contain no braces")
		}
		if seen[name] {
			return nil, nil, nil, errors.New("variable names must be unique")
		}
		seen[name] = true
		if r.HasInitial || !r.HasCurrent || r.Initial != "" || r.Disabled {
			vars = append(vars, request.Variable{Name: name, Initial: r.Initial, Disabled: r.Disabled})
		}
		if r.Credential {
			credentials[name] = true
		}
		if r.HasCurrent {
			current[name] = r.Current
		}
	}
	if len(credentials) == 0 {
		credentials = nil
	}
	return vars, current, credentials, nil
}

// writeFolders saves the collection and folder settings, sorted by path,
// and makes them the ones in use.
func (a *app) writeFolders(folders []request.Folder) error {
	slices.SortFunc(folders, func(x, y request.Folder) int { return strings.Compare(x.Path, y.Path) })
	return a.writeLibrary(a.requests, folders)
}

// folderModal edits what a collection or folder gives its requests: the
// auth they inherit, the scripts around theirs and the variables they read.
func (a *app) folderModal(c *ui.Context, t *ui.Theme) {
	f := &a.folderUI
	if !f.open {
		return
	}
	_, height := c.Size()
	kind := "Collection"
	if strings.Contains(f.draft.Path, "/") {
		kind = "Folder"
	}
	uikit.Modal(c, &f.open, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(560, height-80)).Children(func() {
			uikit.DialogTitle(c, kind+" · "+f.draft.Path)
			uikit.Meta(c, locales.EveryRequestSavedUnderIt())
			ui.Form(c, func() {
				ui.Field(c, locales.Name(), func() {
					uikit.Input(c, &f.name).Label(kind + " name")
				})
			})
			uikit.Meta(c, locales.RenamingMovesItsRequestsFolders())
			uikit.Tabs(c, &f.tab, "", locales.Auth(), locales.Scripts(), locales.Variables(), locales.Description(), locales.RequestDefaults())
			ui.Column(c).Grow(1).MinHeight(0).Gap(t.Space(2)).Children(func() {
				switch f.tab {
				case 0:
					ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(2)).Padding(0, t.Space(1)).Children(func() {
						a.authFields(c, &f.draft.Auth, a.folderAuthTypes(f.draft.Path), f.draft.Path)
					})
					uikit.Meta(c, locales.RequestsWhoseAuthIsInherit())
				case 1:
					uikit.Segmented(c, &f.script, locales.PreRequest(), locales.PostResponse())
					uikit.Meta(c, locales.RunsBeforeTheRequestS())
					if f.script == 0 {
						authoredText(c, &f.draft.Pre, &f.pre, &f.preFor, kind+" pre-request script", "JavaScript")
					} else {
						authoredText(c, &f.draft.Post, &f.post, &f.postFor, kind+" post-response script", "JavaScript")
					}
				case 2:
					uikit.Meta(c, locales.EnvironmentValuesOverrideTheseA())
					uikit.Hint(c, locales.InitialValuesAreSharedAnd2())
					a.variableTable(c, &f.variables, variableTableOptions{enable: true})
				case 3:
					ui.TextArea(c, &f.draft.Description).Label(locales.Description()).Placeholder(locales.WhatTheseRequestsAreFor()).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)).Radius(uikit.RadiusControl).Grow(1).MinHeight(0)
				case 4:
					a.folderSettingsEditor(c, f, t)
				}
			})
			if f.problem != "" {
				ui.Text(c, f.problem).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Danger)
			}
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					f.open = false
				}
				if uikit.PrimaryButton(c, locales.Save()).Label(locales.SaveSettings()).Clicked() {
					a.saveFolder()
				}
			})
		})
	})
}
