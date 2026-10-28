package app

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// importReport lists what an import could not bring over.
type importReport struct {
	open         bool
	title, intro string
	lines        []string
}

// importRequests adds what a file the user picks holds: a Postman
// collection, environment or globals file, a HAR file or an OpenAPI
// document as a new collection, or this app's request list.
func (a *app) importRequests() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:   "Import",
			Filters: []mygo.FileFilter{{Name: "Postman, HAR, OpenAPI or vscroxy requests", Extensions: []string{"json", "har", "yaml", "yml"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		data, err := os.ReadFile(paths[0])
		if err != nil {
			a.setMessage("Import: " + err.Error())
			return
		}
		a.win.Update(func() { a.importData(data, paths[0]) })
	}()
}

// importData merges data into the library; the UI thread calls it.
func (a *app) importData(data []byte, path string) {
	switch postman.Detect(data) {
	case postman.KindCollection:
		col, err := postman.ReadCollection(data)
		if err != nil {
			a.setMessage("Import Postman collection: " + err.Error())
			return
		}
		incoming := make([]savedRequest, len(col.Requests))
		for i, r := range col.Requests {
			incoming[i] = savedRequest{Request: r}
		}
		merged := mergeRequests(a.requests, incoming)
		if err := a.persistRequests(merged); err != nil {
			a.setMessage("Import Postman collection: " + err.Error())
			return
		}
		a.requests = merged
		if len(col.Folders) > 0 {
			folders := slices.DeleteFunc(slices.Clone(a.folders), func(f request.Folder) bool {
				return slices.ContainsFunc(col.Folders, func(in request.Folder) bool { return in.Path == f.Path })
			})
			if err := a.writeFolders(append(folders, col.Folders...)); err != nil {
				col.Warnings = append(col.Warnings, "Collection and folder settings not saved: "+err.Error())
			}
		}
		a.workspace, a.requestsSection = 1, 0
		a.reportImport(fmt.Sprintf("Imported “%s” · %d %s", col.Name, len(col.Requests), plural(len(col.Requests), "request", "requests")), col.Warnings)
	case postman.KindEnvironment, postman.KindGlobals:
		env, err := postman.ReadEnvironment(data)
		if err != nil {
			a.setMessage("Import Postman environment: " + err.Error())
			return
		}
		if a.envsProblem != "" {
			a.setMessage("Import Postman environment: " + a.envsProblem)
			return
		}
		envs, globals := slices.Clone(a.envs), maps.Clone(a.globals)
		title := fmt.Sprintf("Imported %d %s into globals", len(env.Values), plural(len(env.Values), "variable", "variables"))
		if env.Globals {
			if globals == nil {
				globals = map[string]string{}
			}
			maps.Copy(globals, env.Values)
		} else {
			name := strings.TrimSpace(env.Name)
			if name == "" {
				name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			}
			at := slices.IndexFunc(envs, func(e environment) bool { return e.Name == name })
			if at < 0 {
				envs, at = append(envs, environment{Name: name}), len(envs)
			}
			vars := maps.Clone(envs[at].Vars)
			if vars == nil {
				vars = map[string]string{}
			}
			maps.Copy(vars, env.Values)
			envs[at].Vars = vars
			title = fmt.Sprintf("Imported environment “%s” · %d %s", name, len(env.Values), plural(len(env.Values), "variable", "variables"))
		}
		if err := saveEnvs(a.envsPath, envFile{Active: a.activeEnv, Envs: envs, Globals: globals}); err != nil {
			a.setMessage("Import Postman environment: " + err.Error())
			return
		}
		a.envs, a.globals = envs, globals
		a.reportImport(title, env.Warnings)
	default:
		if isHAR(data) {
			a.importHARCollection(data, path)
			return
		}
		if isOpenAPI(data) {
			doc, err := parseOpenAPI(string(data))
			if err != nil {
				a.setMessage("Import OpenAPI: " + err.Error())
				return
			}
			a.importOpenAPICollection(doc, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
			return
		}
		list, err := parseRequests(data)
		if err != nil {
			a.setMessage("Import: not a Postman collection, environment or globals file, a HAR file, an OpenAPI 3.0/3.1 document or a vscroxy request list (" + err.Error() + ")")
			return
		}
		merged := mergeRequests(a.requests, list)
		if err := a.persistRequests(merged); err != nil {
			a.setMessage("Import requests: " + err.Error())
			return
		}
		a.requests = merged
		a.setMessage(fmt.Sprintf("Imported %d requests", len(list)))
	}
}

// reportImport says what came in; when something was dropped it opens a
// dialog that lists it instead of a passing message.
func (a *app) reportImport(title string, warnings []string) {
	if len(warnings) == 0 {
		a.setMessage(title)
		return
	}
	a.importReport = importReport{open: true, title: title, lines: warnings,
		intro: "Everything else was imported. These parts have no equivalent here yet:"}
}

func (a *app) importReportModal(c *ui.Context, t *ui.Theme) {
	r := &a.importReport
	if !r.open {
		return
	}
	appModal(c, &r.open, func() {
		ui.Column(c).Width(520).Gap(10).Children(func() {
			dialogTitle(c, r.title)
			ui.Text(c, r.intro).FontSize(12).TextColor(t.TextMuted)
			ui.Column(c).Gap(4).Children(func() {
				for _, line := range r.lines {
					ui.Text(c, "• "+line).FontSize(12).Selectable()
				}
			})
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.PrimaryButton(c, "OK").Clicked() {
					r.open = false
				}
			})
		})
	})
}

// exportPostman writes the collection named, its folders and their
// settings, as a Postman Collection v2.1 file the user picks.
func (a *app) exportPostman(name string) {
	var list []request.Request
	for _, r := range a.requests {
		list = append(list, r.Request)
	}
	data, warnings, err := postman.WriteCollection(name, list, slices.Clone(a.folders))
	if err != nil {
		a.setMessage("Export Postman collection: " + err.Error())
		return
	}
	a.saveExport("Export Postman collection", strings.ReplaceAll(name, "∕", "-")+".postman_collection.json", data, warnings)
}

// exportPostmanVariables writes the variables dialog's rows, as they are on
// screen, as a Postman environment or globals file.
func (a *app) exportPostmanVariables() {
	values := map[string]string{}
	for _, row := range a.varsRows {
		if name := strings.TrimSpace(row.Name); name != "" {
			values[name] = row.Value
		}
	}
	name := "globals"
	if !a.varsGlobals {
		name = cmpOr(strings.TrimSpace(a.varsName), "default")
	}
	data, err := postman.WriteEnvironment(name, values, a.varsGlobals)
	if err != nil {
		a.setMessage("Export Postman environment: " + err.Error())
		return
	}
	file := name + ".postman_environment.json"
	if a.varsGlobals {
		file = "workspace.postman_globals.json"
	}
	a.saveExport("Export Postman environment", file, data, nil)
}

// saveExport asks where to write data and writes it privately: exports can
// hold credentials typed into auth fields and variables.
func (a *app) saveExport(title, file string, data []byte, warnings []string) {
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Title: title, DefaultPath: file,
			Filters: []mygo.FileFilter{{Name: "Postman JSON", Extensions: []string{"json"}}}})
		if err != nil || path == "" {
			return
		}
		if err := writePrivateFile(path, data); err != nil {
			a.setMessage(title + ": " + err.Error())
			return
		}
		a.win.Update(func() {
			done := "Exported to " + path
			if len(warnings) > 0 {
				a.importReport = importReport{open: true, title: done, lines: warnings,
					intro: "Everything else was exported. Postman has no place for these:"}
				return
			}
			a.setMessage(done)
		})
	}()
}
