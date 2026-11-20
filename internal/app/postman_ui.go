package app

import (
	"context"
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
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// importReport lists what an import could not bring over.
type importReport struct {
	open         bool
	title, intro string
	lines        []string
	// review is the first imported request, the way into the collection the
	// report just wrote.
	review string
}

// importRequests adds what a file the user picks holds: a Postman
// collection, environment or globals file, a HAR file or an OpenAPI
// document as a new collection.
func (a *app) importRequests() {
	go guarded(func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:   locales.ImportText2(),
			Filters: []mygo.FileFilter{{Name: "API exports", Extensions: []string{"json", "har", "yaml", "yml", "xml", "jmx", "http", "rest"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		path := paths[0]
		a.importProgress(locales.ImportCollectionTask(), func(ctx context.Context, report func(progressReport)) (func(), error) {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			report(progressReport{message: locales.ImportCollectionTask(), percentage: 60})
			return func() { a.importData(data, path) }, nil
		})
	})
}

// importCollectionFolder reads a collection that is a directory: a Postman
// Collection v3 tree or a Bruno .bru collection. The native dialog picks a
// directory because the collection is a tree of files, not one document.
func (a *app) importCollectionFolder() {
	go guarded(func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:       locales.ImportCollectionFolder2(),
			ButtonLabel: "Import",
			Directory:   true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.importProgress(locales.ImportCollectionTask(), func(ctx context.Context, report func(progressReport)) (func(), error) {
			merge, err := a.loadCollectionDir(ctx, paths[0])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", locales.ImportCollectionFolder2(), err)
			}
			report(progressReport{message: locales.ImportCollectionTask(), percentage: 60})
			return merge, nil
		})
	})
}

// loadCollectionDir reads a Postman v3 or Bruno collection directory on a
// worker and returns the merge to run on the UI thread. Bruno wins when both
// markers are present: a Bruno folder can contain a .resources directory of
// its own.
func (a *app) loadCollectionDir(ctx context.Context, path string) (merge func(), err error) {
	defer func() {
		if r := recover(); r != nil {
			// Do not put the panic value in the error: it is not request data
			// we want to record, and the type identifies the bug.
			merge, err = nil, fmt.Errorf("import stopped unexpectedly (%T)", r)
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch collectionDirKindOf(path) {
	case collectionDirV3:
		col, err := postman.ReadCollectionDir(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", locales.ImportPostmanCollection(), err)
		}
		return func() { a.importPostmanCollection(col) }, nil
	case collectionDirBruno:
		result, err := imports.ReadBrunoDir(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", locales.ImportBrunoCollection(), err)
		}
		return func() { a.importThirdParty(result, locales.ImportBrunoCollection()) }, nil
	default:
		return nil, fmt.Errorf("not a Postman v3 collection or a Bruno collection")
	}
}

// importData merges data into the library; the UI thread calls it.
func (a *app) importData(data []byte, path string) {
	if ext := strings.ToLower(filepath.Ext(path)); ext == ".http" || ext == ".rest" {
		a.importHTTPFile(data, path)
		return
	}
	switch postman.Detect(data) {
	case postman.KindCollection:
		col, err := postman.ReadCollection(data)
		if err != nil {
			a.fail(locales.ImportPostmanCollection(), err)
			return
		}
		a.importPostmanCollection(col)
	case postman.KindEnvironment, postman.KindGlobals:
		env, err := postman.ReadEnvironment(data)
		if err != nil {
			a.fail(locales.ImportPostmanEnvironment(), err)
			return
		}
		if a.envsProblem != "" {
			a.warn(locales.ImportPostmanEnvironmentError(a.envsProblem))
			return
		}
		envs, globals := slices.Clone(a.envs), maps.Clone(a.globals)
		title := locales.ImportedVariablesIntoGlobals(len(env.Values))
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
			title = locales.ImportedEnvironmentVariables(name, len(env.Values))
		}
		if err := func() error {
			f := a.localEnvs()
			f.Envs = envs
			f.Globals = globals
			return a.saveEnvFile(f)
		}(); err != nil {
			a.fail(locales.ImportPostmanEnvironment(), err)
			return
		}
		a.envs, a.globals = envs, globals
		a.reportImport(title, env.Warnings)
	default:
		format, detectErr := imports.DetectWithError(data)
		if detectErr != nil {
			a.fail(locales.ImportText2(), detectErr)
			return
		}
		switch format {
		case imports.InsomniaV4, imports.InsomniaV5:
			result, err := imports.ReadInsomnia(data)
			if err != nil {
				a.fail(locales.ImportInsomnia(), err)
				return
			}
			a.importThirdParty(result, locales.ImportInsomnia())
			return
		case imports.HoppscotchCollection:
			result, err := imports.ReadHoppscotch(data)
			if err != nil {
				a.fail(locales.ImportHoppscotchCollection(), err)
				return
			}
			a.importThirdParty(result, locales.ImportHoppscotchCollection())
			return
		case imports.HoppscotchEnvironment:
			envs, err := imports.ReadHoppscotchEnvironment(data)
			if err != nil {
				a.fail(locales.ImportHoppscotchEnvironment(), err)
				return
			}
			a.importThirdParty(imports.Result{Environments: envs}, locales.ImportHoppscotchEnvironment())
			return
		case imports.ThunderClient:
			result, err := imports.ReadThunder(data)
			if err != nil {
				a.fail(locales.ImportText2(), err)
				return
			}
			a.importThirdParty(result, locales.ImportThunderClientCollection())
			return
		case imports.SoapUI:
			result, err := imports.ReadSoapUI(data)
			if err != nil {
				a.fail(locales.ImportText2(), err)
				return
			}
			a.importThirdParty(result, locales.ImportSoapuiProject())
			return
		case imports.JMeter:
			result, err := imports.ReadJMeter(data)
			if err != nil {
				a.fail(locales.ImportText2(), err)
				return
			}
			a.importThirdParty(result, locales.ImportJmeterTestPlan())
			return
		}
		if format == imports.Swagger2 {
			doc, warnings, err := imports.ReadSwagger2(data)
			if err != nil {
				a.fail(locales.ImportSwagger2(), err)
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
				a.fail(locales.ImportOpenapi2(), err)
				return
			}
			a.importOpenAPICollection(doc, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), nil, "")
			return
		}
		a.warn(locales.ImportNotASupportedFile())
	}
}

// importPostmanCollection merges a parsed Postman collection. The v2.1 file
// reader and the v3 directory reader produce the same shape; the UI thread
// calls it. Secret variables arrive as Current values and stay out of the
// library write.
func (a *app) importPostmanCollection(col postman.Collection) {
	incoming := make([]savedRequest, len(col.Requests))
	for i, r := range col.Requests {
		incoming[i] = savedRequest{Request: r}
	}
	folders := libraryFolders(col.Folders)
	replaced := replacedFolderPaths(a.requests, a.folders, folders, []imports.Collection{{Name: col.Name}})
	merged := mergeRequests(a.requests, incoming)
	if err := a.persistRequests(merged); err != nil {
		a.fail(locales.ImportPostmanCollection(), err)
		return
	}
	a.requests = merged
	settingsSaved := true
	if len(folders) > 0 {
		saved := slices.DeleteFunc(slices.Clone(a.folders), func(f request.Folder) bool {
			return slices.ContainsFunc(folders, func(in request.Folder) bool { return in.Path == f.Path })
		})
		if err := a.writeFolders(append(saved, folders...)); err != nil {
			settingsSaved = false
			col.Warnings = append(col.Warnings, "Collection and folder settings not saved: "+err.Error())
		}
	}
	if settingsSaved {
		a.applyImportedFolderCurrent(replaced, importedFolderLocals(col.Folders))
	}
	a.page = pageRequest
	review := ""
	for _, r := range merged {
		if strings.HasPrefix(r.Collection, col.Name) {
			review = r.key()
			break
		}
	}
	a.reportImport(locales.ImportedCollection(col.Name, len(col.Requests)), col.Warnings, review)
}

// reportImport says what came in; when something was dropped it opens a
// dialog that lists it instead of a passing message.
func (a *app) reportImport(title string, warnings []string, review ...string) {
	if len(warnings) == 0 {
		a.done(title)
		return
	}
	r := importReport{open: true, title: title, lines: warnings,
		intro: locales.ImportedRemainder()}
	if len(review) > 0 {
		r.review = review[0]
	}
	a.importReport = r
	a.dialogs.arrive(dialogImportReport)
}

func (a *app) importReportModal(c *ui.Context, t *ui.Theme) {
	r := &a.importReport
	if !r.open {
		return
	}
	uikit.Modal(c, &r.open, locales.ImportReview(), func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, locales.ImportReview())
			uikit.Hint(c, r.title)
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(uikit.SpaceM)).Children(func() {
				uikit.Hint(c, r.intro)
				for _, line := range r.lines {
					// The bullet hangs: a wrapped line starts under the text, not the dot.
					ui.Row(c).Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Start).Children(func() {
						uikit.Body(c, "•")
						uikit.Body(c, line).Selectable().Grow(1).MinWidth(0)
					})
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if r.review != "" {
					if uikit.PrimaryButton(c, locales.ReviewCollection()).Clicked() {
						r.open = false
						a.openSaved(r.review)
					}
					if uikit.SecondaryButton(c, locales.Done()).Clicked() {
						r.open = false
					}
					return
				}
				if uikit.PrimaryButton(c, locales.Ok()).Clicked() {
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
		a.fail(locales.ExportPostmanCollection(), err)
		return
	}
	a.saveExport(locales.ExportPostmanCollection(), strings.ReplaceAll(name, "∕", "-")+".postman_collection.json", data, warnings)
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
		a.fail(locales.ExportPostmanEnvironment(), err)
		return
	}
	file := name + ".postman_environment.json"
	if a.varsGlobals {
		file = "workspace.postman_globals.json"
	}
	a.saveExport(locales.ExportPostmanEnvironment(), file, data, nil)
}

// saveExport reviews a copy before opening the native save dialog.
func (a *app) saveExport(title, file string, data []byte, warnings []string) {
	a.reviewExport(title, file, data, warnings)
}
