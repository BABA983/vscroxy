package app

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/vault"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// folderEditor edits one collection's or folder's settings as a draft:
// Cancel leaves the saved settings, and the requests using them, alone.
type folderEditor struct {
	open            bool
	draft           request.Folder
	name            string // the last part of draft.Path, as edited
	tab             int    // auth, scripts, variables, description
	script          int    // pre-request, post-response
	pre, post       *editor.State
	preFor, postFor string
	problem         string
}

// inheritance is what a request filed under path takes from its collection,
// folders and the globals. The vault joins it only for a send (sendContext).
func (a *app) inheritance(path string) request.Context {
	return request.Context{Folders: request.Chain(a.folders, path), Globals: maps.Clone(a.globals)}
}

// openFolder edits the settings of the collection or folder at path.
func (a *app) openFolder(path string) {
	f := &a.folderUI
	*f = folderEditor{open: true, draft: request.Folder{Path: path}, name: path[strings.LastIndex(path, "/")+1:]}
	if i := slices.IndexFunc(a.folders, func(folder request.Folder) bool { return folder.Path == path }); i >= 0 {
		f.draft = a.folders[i]
		f.draft.Variables = slices.Clone(f.draft.Variables)
	}
	f.draft.Auth.Type = cmp.Or(f.draft.Auth.Type, a.folderAuthTypes(path)[0])
	f.draft.Auth.Location = cmp.Or(f.draft.Auth.Location, "Header")
}

// applyWrites saves what scripts changed beyond the environment: globals,
// collection variables and vault secrets. Each store saves once.
func (a *app) applyWrites(writes []request.Write) {
	if len(writes) == 0 {
		return
	}
	globals, folders, secrets := maps.Clone(a.globals), slices.Clone(a.folders), slices.Clone(a.vault.secrets)
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
			at := slices.IndexFunc(folders, func(f request.Folder) bool { return f.Path == w.Path })
			if at < 0 {
				folders, at = append(folders, request.Folder{Path: w.Path}), len(folders)
			}
			folders[at].Variables = w.Fields(folders[at].Variables)
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
	if touched[0] {
		if a.envsProblem != "" {
			problems = append(problems, "globals: "+a.envsProblem)
		} else if err := saveEnvs(a.envsPath, envFile{Active: a.activeEnv, Envs: a.envs, Globals: globals}); err != nil {
			problems = append(problems, "globals: "+err.Error())
		} else {
			a.globals = globals
		}
	}
	if touched[1] {
		if err := a.writeFolders(folders); err != nil {
			problems = append(problems, "collection variables: "+err.Error())
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
		a.setMessage("Script changes not saved: " + strings.Join(problems, "; "))
	}
}

// folderSettingsItems offers the settings of every level a request filed
// under path lives in: its collection, then each folder down to path.
func (a *app) folderSettingsItems(m *ui.Menu, path string) {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "" {
			return
		}
		label := "Collection “" + part + "” settings…"
		if i > 0 {
			label = "Folder “" + part + "” settings…"
		}
		if m.Item(label).Chosen() {
			a.openFolder(strings.Join(parts[:i+1], "/"))
		}
	}
	m.Separator()
	if m.Item("Export “" + parts[0] + "” as Postman…").Chosen() {
		a.exportPostman(parts[0])
	}
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

// saveFolder keeps the draft, replacing the folder's saved settings.
func (a *app) saveFolder() {
	f := &a.folderUI
	seen := map[string]bool{}
	for _, v := range f.draft.Variables {
		name := strings.TrimSpace(v.Name)
		if name == "" && v.Value == "" {
			continue
		}
		if name == "" || seen[name] || strings.ContainsAny(name, "{}") {
			f.problem = "Variable names must be nonempty, unique and contain no braces."
			return
		}
		seen[name] = true
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
	draft.Variables = slices.DeleteFunc(slices.Clone(draft.Variables), func(v request.Field) bool {
		return strings.TrimSpace(v.Name) == "" && v.Value == ""
	})
	if draft.Auth.Type == request.AuthInherit || draft.Auth.Type == "None" && !strings.Contains(draft.Path, "/") {
		draft.Auth = request.Auth{Type: draft.Auth.Type}
	}
	folders := slices.DeleteFunc(slices.Clone(a.folders), func(folder request.Folder) bool { return folder.Path == draft.Path })
	if err := a.writeFolders(append(folders, draft)); err != nil {
		f.problem = "Save failed: " + err.Error()
		return
	}
	f.open = false
	a.setMessage("Saved " + draft.Path + " settings")
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
	width, height := c.Size()
	kind := "Collection"
	if strings.Contains(f.draft.Path, "/") {
		kind = "Folder"
	}
	appModal(c, &f.open, func() {
		ui.Column(c).Width(min(760, width-64)).Height(min(560, height-80)).Gap(10).Children(func() {
			dialogTitle(c, kind+" · "+f.draft.Path)
			ui.Text(c, "Every request saved under it inherits these settings; a folder's settings override its collection's.").FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Name").FontSize(12).Width(48)
				ui.TextInput(c, &f.name).Label(kind + " name").Grow(1)
			})
			ui.Text(c, "Renaming moves its requests, folders and settings together.").FontSize(11).TextColor(t.TextMuted)
			ui.Tabs(c, &f.tab, "Auth", "Scripts", "Variables", "Description")
			ui.Column(c).Grow(1).Gap(8).Children(func() {
				switch f.tab {
				case 0:
					authFields(c, &f.draft.Auth, a.folderAuthTypes(f.draft.Path))
					ui.Text(c, "Requests whose Auth is Inherit send this.").FontSize(11).TextColor(t.TextMuted)
				case 1:
					ui.Segmented(c, &f.script, "Pre-request", "Post-response")
					ui.Text(c, "Runs before the request's own script, after any parent collection's.").FontSize(11).TextColor(t.TextMuted)
					if f.script == 0 {
						scriptText(c, &f.draft.Pre, &f.pre, &f.preFor, kind+" pre-request script")
					} else {
						scriptText(c, &f.draft.Post, &f.post, &f.postFor, kind+" post-response script")
					}
				case 2:
					ui.Text(c, "Environment values override these; a folder's variables override its collection's.").FontSize(11).TextColor(t.TextMuted)
					requestFieldsEditor(c, &f.draft.Variables, fieldsOptions{secret: true}, "variable")
				case 3:
					ui.TextArea(c, &f.draft.Description).Label("Description").Placeholder("What these requests are for").Grow(1)
				}
			})
			if f.problem != "" {
				ui.Text(c, f.problem).FontSize(11).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					f.open = false
				}
				if ui.PrimaryButton(c, "Save").Label("Save settings").Clicked() {
					a.saveFolder()
				}
			})
		})
	})
}
