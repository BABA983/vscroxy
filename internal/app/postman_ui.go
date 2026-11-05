package app

import (
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/imports"
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
// document as a new collection.
func (a *app) importRequests() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:   "Import",
			Filters: []mygo.FileFilter{{Name: "Postman, HAR or OpenAPI", Extensions: []string{"json", "har", "yaml", "yml"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		data, err := os.ReadFile(paths[0])
		if err != nil {
			a.fail("Import", err)
			return
		}
		a.win.Update(func() { a.importData(data, paths[0]) })
	}()
}

// importCollectionFolder reads a Bruno .bru collection directory. The native
// dialog picks a directory because the collection is a tree of files, not one
// export document.
func (a *app) importCollectionFolder() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:       "Import Bruno collection",
			ButtonLabel: "Import",
			Directory:   true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		result, err := imports.ReadBrunoDir(paths[0])
		if err != nil {
			a.fail("Import Bruno collection", err)
			return
		}
		a.win.Update(func() { a.importThirdParty(result, "Import Bruno collection") })
	}()
}

// importData merges data into the library; the UI thread calls it.
func (a *app) importData(data []byte, path string) {
	switch postman.Detect(data) {
	case postman.KindCollection:
		col, err := postman.ReadCollection(data)
		if err != nil {
			a.fail("Import Postman collection", err)
			return
		}
		incoming := make([]savedRequest, len(col.Requests))
		for i, r := range col.Requests {
			incoming[i] = savedRequest{Request: r}
		}
		replaced := replacedFolderPaths(a.requests, a.folders, col.Folders, []imports.Collection{{Name: col.Name}})
		merged := mergeRequests(a.requests, incoming)
		if err := a.persistRequests(merged); err != nil {
			a.fail("Import Postman collection", err)
			return
		}
		a.requests = merged
		settingsSaved := true
		if len(col.Folders) > 0 {
			folders := slices.DeleteFunc(slices.Clone(a.folders), func(f request.Folder) bool {
				return slices.ContainsFunc(col.Folders, func(in request.Folder) bool { return in.Path == f.Path })
			})
			if err := a.writeFolders(append(folders, col.Folders...)); err != nil {
				settingsSaved = false
				col.Warnings = append(col.Warnings, "Collection and folder settings not saved: "+err.Error())
			}
		}
		if settingsSaved {
			a.applyImportedFolderCurrent(replaced, nil)
		}
		a.page = pageRequest
		a.reportImport(fmt.Sprintf("Imported “%s” · %d %s", col.Name, len(col.Requests), plural(len(col.Requests), "request", "requests")), col.Warnings)
	case postman.KindEnvironment, postman.KindGlobals:
		env, err := postman.ReadEnvironment(data)
		if err != nil {
			a.fail("Import Postman environment", err)
			return
		}
		if a.envsProblem != "" {
			a.warn("Import Postman environment: " + a.envsProblem)
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
		if err := func() error {
			f := a.localEnvs()
			f.Envs = envs
			f.Globals = globals
			return saveEnvs(a.file(a.envsPath), f)
		}(); err != nil {
			a.fail("Import Postman environment", err)
			return
		}
		a.envs, a.globals = envs, globals
		a.reportImport(title, env.Warnings)
	default:
		format := imports.Detect(data)
		switch format {
		case imports.InsomniaV4, imports.InsomniaV5:
			result, err := imports.ReadInsomnia(data)
			if err != nil {
				a.fail("Import Insomnia", err)
				return
			}
			a.importThirdParty(result, "Import Insomnia")
			return
		case imports.HoppscotchCollection:
			result, err := imports.ReadHoppscotch(data)
			if err != nil {
				a.fail("Import Hoppscotch collection", err)
				return
			}
			a.importThirdParty(result, "Import Hoppscotch collection")
			return
		case imports.HoppscotchEnvironment:
			envs, err := imports.ReadHoppscotchEnvironment(data)
			if err != nil {
				a.fail("Import Hoppscotch environment", err)
				return
			}
			a.importThirdParty(imports.Result{Environments: envs}, "Import Hoppscotch environment")
			return
		}
		if format == imports.Swagger2 {
			doc, warnings, err := imports.ReadSwagger2(data)
			if err != nil {
				a.fail("Import Swagger 2", err)
				return
			}
			a.importOpenAPICollection(doc, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), warnings, "swagger2")
			return
		}
		if isHAR(data) {
			a.importHARCollection(data, path)
			return
		}
		if isOpenAPI(data) {
			doc, err := parseOpenAPI(string(data))
			if err != nil {
				a.fail("Import OpenAPI", err)
				return
			}
			a.importOpenAPICollection(doc, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), nil, "")
			return
		}
		a.warn("Import: not a supported file (Postman, Insomnia, Hoppscotch, Swagger 2, HAR, OpenAPI 3.0/3.1); for Bruno choose Import Bruno collection…")
	}
}

// reportImport says what came in; when something was dropped it opens a
// dialog that lists it instead of a passing message.
func (a *app) reportImport(title string, warnings []string) {
	if len(warnings) == 0 {
		a.done(title)
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
	uikit.Modal(c, &r.open, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, r.title)
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(2)).Children(func() {
				uikit.Hint(c, r.intro)
				for _, line := range r.lines {
					uikit.Body(c, "• "+line).Selectable()
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.PrimaryButton(c, "OK").Clicked() {
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
		a.fail("Export Postman collection", err)
		return
	}
	a.saveExport("Export Postman collection", strings.ReplaceAll(name, "∕", "-")+".postman_collection.json", data, warnings)
}

// exportPostmanVariables writes the variables dialog's Initial values, as
// they are on screen, as a Postman environment or globals file. Current
// values are local and never exported.
func (a *app) exportPostmanVariables() {
	values := map[string]string{}
	for _, row := range a.varsRows {
		if name := strings.TrimSpace(row.Name); name != "" {
			values[name] = row.Initial
		}
	}
	name := "globals"
	if !a.varsGlobals {
		name = cmpOr(strings.TrimSpace(a.varsName), "default")
	}
	data, err := postman.WriteEnvironment(name, values, a.varsGlobals)
	if err != nil {
		a.fail("Export Postman environment", err)
		return
	}
	file := name + ".postman_environment.json"
	if a.varsGlobals {
		file = "workspace.postman_globals.json"
	}
	a.saveExport("Export Postman environment", file, data, nil)
}

// saveExport reviews a copy before opening the native save dialog.
func (a *app) saveExport(title, file string, data []byte, warnings []string) {
	a.reviewExport(title, file, data, warnings)
}
