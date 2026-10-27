package app

import (
	"cmp"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// folderEditor edits one collection's or folder's settings as a draft:
// Cancel leaves the saved settings, and the requests using them, alone.
type folderEditor struct {
	open            bool
	draft           request.Folder
	tab             int // auth, scripts, variables, description
	script          int // pre-request, post-response
	pre, post       *editor.State
	preFor, postFor string
	problem         string
}

// loadFolders reads the collection and folder settings; a missing file has
// none. A damaged file is reported and never overwritten.
func (a *app) loadFolders() {
	if a.foldersPath == "" {
		return
	}
	data, err := os.ReadFile(a.foldersPath)
	if errors.Is(err, fs.ErrNotExist) {
		a.folders, a.foldersProblem = nil, ""
		return
	}
	var folders []request.Folder
	if err == nil {
		err = json.Unmarshal(data, &folders)
	}
	if err != nil {
		a.foldersProblem = fmt.Sprintf("Collections (%s): %v", a.foldersPath, err)
		return
	}
	a.folders, a.foldersProblem = folders, ""
}

// inheritance is what a request filed under path takes from its collection,
// folders and the globals. The vault joins it only for a send (sendContext).
func (a *app) inheritance(path string) request.Context {
	return request.Context{Folders: request.Chain(a.folders, path), Globals: maps.Clone(a.globals)}
}

// openFolder edits the settings of the collection or folder at path.
func (a *app) openFolder(path string) {
	f := &a.folderUI
	*f = folderEditor{open: true, draft: request.Folder{Path: path}}
	if i := slices.IndexFunc(a.folders, func(folder request.Folder) bool { return folder.Path == path }); i >= 0 {
		f.draft = a.folders[i]
		f.draft.Variables = slices.Clone(f.draft.Variables)
	}
	f.draft.Auth.Type = cmp.Or(f.draft.Auth.Type, a.folderAuthTypes(path)[0])
	f.draft.Auth.Location = cmp.Or(f.draft.Auth.Location, "Header")
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
	if a.foldersProblem != "" {
		f.problem = "Not saved: " + a.foldersProblem
		return
	}
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
	draft := f.draft
	draft.Variables = slices.DeleteFunc(slices.Clone(draft.Variables), func(v request.Field) bool {
		return strings.TrimSpace(v.Name) == "" && v.Value == ""
	})
	if draft.Auth.Type == request.AuthInherit || draft.Auth.Type == "None" && !strings.Contains(draft.Path, "/") {
		draft.Auth = request.Auth{Type: draft.Auth.Type}
	}
	folders := slices.DeleteFunc(slices.Clone(a.folders), func(folder request.Folder) bool { return folder.Path == draft.Path })
	folders = append(folders, draft)
	slices.SortFunc(folders, func(x, y request.Folder) int { return strings.Compare(x.Path, y.Path) })
	data, err := json.Marshal(folders)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(a.foldersPath), 0o755)
	}
	if err == nil {
		err = writePrivateFile(a.foldersPath, append(data, '\n'))
	}
	if err != nil {
		f.problem = "Save failed: " + err.Error()
		return
	}
	a.folders, f.open = folders, false
	a.setMessage("Saved " + draft.Path + " settings")
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
