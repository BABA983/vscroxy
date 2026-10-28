package app

import (
	"cmp"
	"crypto/rand"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

var apiIcon = lucide.FileCode()

type apiDocument struct {
	spec       apiSpec
	saved      *apiSpec
	code       *editor.State
	checked    string
	parsed     *openapi3.T
	name       string
	problem    string
	tab        int
	endpoint   string
	details    *editor.State
	detailsFor string
}

func (d *apiDocument) title() string {
	return cmp.Or(d.name, "Untitled API")
}

func (d *apiDocument) dirty() bool { return d.saved == nil || d.code.Buffer.Modified() }

func (d *apiDocument) validate() bool {
	d.checked = d.code.Buffer.String()
	d.parsed = nil
	d.problem = ""
	d.details = nil
	if jsontext.Value(d.checked).IsValid() {
		d.code.Buffer.SetLanguage(editor.LanguageNamed("json"))
	} else {
		d.code.Buffer.SetLanguage(editor.LanguageNamed("yaml"))
	}
	doc, err := parseOpenAPI(d.checked)
	if err != nil {
		d.problem = err.Error()
		return false
	}
	d.parsed = doc
	d.name = doc.Info.Title
	return true
}

func (a *app) activeAPI() *apiDocument {
	for _, d := range a.apis {
		if d.spec.ID == a.apiChoice {
			return d
		}
	}
	return nil
}

func (a *app) addAPI(spec apiSpec, saved bool) {
	if spec.ID == "" {
		spec.ID = rand.Text()
	}
	d := &apiDocument{spec: spec, code: &editor.State{Buffer: editor.NewBuffer(spec.Source), Label: "OpenAPI source", NoScrollPastEnd: true}}
	if saved {
		d.saved = new(spec)
		d.code.Buffer.MarkSaved()
	}
	if !d.validate() {
		d.tab = 1
	}
	a.apis = append(a.apis, d)
	a.apiChoice = spec.ID
}

func (a *app) newAPI() {
	a.addAPI(apiSpec{Source: "{\n  \"openapi\": \"3.0.3\",\n  \"info\": {\"title\": \"Untitled API\", \"version\": \"1.0.0\"},\n  \"paths\": {}\n}\n"}, false)
	a.activeAPI().tab = 1
}

func (a *app) saveAPI(d *apiDocument) {
	if !d.validate() {
		a.setMessage("OpenAPI validation failed; nothing was saved")
		return
	}
	next := d.spec
	next.Source = d.code.Buffer.String()
	var list []apiSpec
	for _, other := range a.apis {
		if other == d {
			list = append(list, next)
		} else if other.saved != nil {
			list = append(list, *other.saved)
		}
	}
	if a.apisPath == "" {
		a.setMessage("APIs: no local storage path available")
		return
	}
	if err := saveAPIs(a.apisPath, list); err != nil {
		a.setMessage("Save API: " + err.Error())
		return
	}
	d.spec = next
	d.saved = new(next)
	d.code.Buffer.MarkSaved()
	a.setMessage("Saved API " + d.title())
}

func (a *app) apiView(c *ui.Context, t *ui.Theme) {
	d := a.activeAPI()
	if d == nil {
		ui.Column(c).Fill().Center().Gap(10).Children(func() {
			ui.Icon(c, apiIcon).Size(28, 28).TextColor(t.TextMuted)
			ui.Text(c, "From traffic to an API contract").FontSize(15).Bold()
			ui.Text(c, "Save captured requests in a collection, then generate an OpenAPI draft.").FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, "Or create or import a standalone OpenAPI 3.0 / 3.1 specification.").FontSize(12).TextColor(t.TextMuted)
			if ui.PrimaryButton(c, "Create API").Clicked() {
				a.newAPI()
			}
		})
		return
	}
	current := d.code.Buffer.String() == d.checked
	valid := current && d.parsed != nil
	ui.Column(c).Key(d.spec.ID).Fill().Children(func() {
		ui.Row(c).Height(44).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, d.title()).FontSize(13).Bold().Grow(1).SingleLine()
			if ui.Button(c, "Validate").Height(26).FontSize(11).Clicked() {
				d.validate()
			}
			if ui.Button(c, "Export…").Height(26).FontSize(11).Label("Export OpenAPI").Disabled(!valid).Clicked() {
				a.exportOpenAPI(d)
			}
			if ui.Button(c, "Create collection").Height(26).FontSize(11).Label("Create collection from OpenAPI").Disabled(!valid).Clicked() {
				a.importOpenAPICollection(d.parsed, d.title())
			}
			if ui.PrimaryButton(c, "Save API").Height(26).FontSize(11).Clicked() {
				a.saveAPI(d)
			}
		})
		status, color := "Unvalidated changes — validate before using endpoints or exporting.", t.TextMuted
		if current && d.problem != "" {
			status, color = "Validation failed — fix the source and validate again.", t.Danger
		} else if valid {
			state := "Saved locally"
			if d.dirty() {
				state = "Unsaved draft"
			}
			status = fmt.Sprintf("%s  ·  OpenAPI %s  ·  Endpoints: %d", state, d.parsed.OpenAPI, len(apiOperations(d.parsed)))
		}
		ui.Text(c, status).FontSize(11).TextColor(color).Padding(0, 12, 8, 12)
		if d.spec.Fingerprint != "" {
			changed := d.spec.Fingerprint != collectionFingerprint(collectionRequests(a.requests, d.spec.Collection))
			ui.Row(c).Padding(6, 12).Gap(10).AlignItems(ui.Center).Background(t.Surface).Children(func() {
				message := "Source: " + cmp.Or(d.spec.Collection, "Saved") + " · independent draft"
				if changed {
					message = "Source collection changed — review before updating this contract."
				}
				ui.Text(c, message).FontSize(11).TextColor(t.TextMuted).Grow(1)
				if ui.Button(c, "Generate new draft").FontSize(11).Clicked() {
					a.generateAPI(d.spec.Collection)
				}
			})
		}
		ui.Row(c).Height(34).Padding(0, 6).AlignItems(ui.Center).
			Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
			flatTabs(c, &d.tab, "API", "Endpoints", "OpenAPI", "Review notes")
		})
		switch d.tab {
		case 0:
			if valid {
				a.apiEndpoints(c, t, d)
			} else {
				ui.Scroll(c).Grow(1).Padding(18).Gap(12).Children(func() {
					ui.Text(c, cmp.Or(d.problem, "Validate the edited source to refresh endpoints.")).TextColor(t.TextMuted).FontSize(12)
					if ui.Button(c, "Edit OpenAPI").Clicked() {
						d.tab = 1
					}
				})
			}
		case 1:
			if current && d.problem != "" {
				ui.Scroll(c).Height(90).Padding(10, 18).Children(func() {
					ui.Text(c, d.problem).FontSize(11).TextColor(t.Danger)
				})
				ui.Divider(c)
			}
			editor.View(c, d.code).Grow(1)
		case 2:
			ui.Scroll(c).Grow(1).Padding(20).Gap(16).Children(func() {
				ui.Text(c, "Review before publishing").FontSize(16).Bold()
				ui.Text(c, "Validation checks OpenAPI structure, not whether your service implements the contract. No automatic or bidirectional synchronization is performed.").FontSize(12).TextColor(t.TextMuted)
				for _, note := range d.spec.Notes {
					ui.Text(c, "• "+note).FontSize(12)
				}
				ui.Text(c, "Only standalone JSON/YAML is supported. Internal #/ references are resolved; external files and URLs are never fetched. Original source is preserved on save/export.").FontSize(12).TextColor(t.TextMuted)
			})
		}
	})
}

func (a *app) apiEndpoints(c *ui.Context, t *ui.Theme, d *apiDocument) {
	endpoints := apiOperations(d.parsed)
	if len(endpoints) == 0 {
		ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
			ui.Text(c, "No HTTP endpoints yet").FontSize(15).Bold()
			ui.Text(c, "Define paths in OpenAPI, or generate a draft from saved requests.").FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	if d.endpoint == "" {
		d.endpoint = endpoints[0].method + " " + endpoints[0].path
	}
	ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
		ui.Sidebar(c, &d.endpoint, func() {
			for _, endpoint := range endpoints {
				label := endpoint.method + " " + endpoint.path
				ui.SidebarItem(c, label, nil, endpoint.path).Label("Endpoint: " + label).Children(func() {
					ui.Text(c, endpoint.method).FontSize(9).Bold().TextColor(methodColor(endpoint.method)).Width(36)
				})
			}
		}).Width(210).FillHeight().Background(t.Surface)
		ui.Divider(c)
		selected := 0
		for i, endpoint := range endpoints {
			if endpoint.method+" "+endpoint.path == d.endpoint {
				selected = i
			}
		}
		endpoint := endpoints[selected]
		d.endpoint = endpoint.method + " " + endpoint.path
		ui.Column(c).Grow(1).FillHeight().Padding(18).Gap(12).Children(func() {
			ui.Text(c, endpoint.method+" "+endpoint.path).FontSize(16).Bold().SingleLine().TextColor(methodColor(endpoint.method))
			if summary := cmp.Or(endpoint.op.Summary, endpoint.op.OperationID); summary != "" && summary != endpoint.method+" "+endpoint.path {
				ui.Text(c, summary).FontSize(13)
			}
			if ui.PrimaryButton(c, "Open in Requests").Width(160).Clicked() {
				a.addDocument(requestFromOperation(d.parsed, endpoint), "")
				a.sourceAPI = d.spec.ID
				a.composeResult = "Draft from OpenAPI. Review server, variables, authentication and body before sending."
			}
			ui.Text(c, "Uses the first effective server and required parameters. Body examples are copied when provided; schemas are not turned into invented payloads.").FontSize(11).TextColor(t.TextMuted)
			ui.Divider(c)
			ui.Text(c, "OPERATION · schemas and references").FontSize(10).Bold().TextColor(t.TextMuted)
			if d.details == nil || d.detailsFor != d.endpoint {
				data, _ := json.Marshal(endpoint.op, jsontext.WithIndent("  "))
				buf := editor.NewBuffer(string(data))
				buf.SetLanguage(editor.LanguageNamed("json"))
				d.details = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, Label: "Operation schema"}
				d.detailsFor = d.endpoint
			}
			editor.View(c, d.details).Grow(1)
		})
	})
}

func (a *app) openAPIGeneration() {
	names := slices.Sorted(maps.Keys(groupRequests(a.requests)))
	if len(names) > 0 {
		a.apiCollection = names[0]
		a.apiGenerateOpen = true
	}
}

func (a *app) apiGenerationModal(c *ui.Context, t *ui.Theme) {
	if !a.apiGenerateOpen {
		return
	}
	appModal(c, &a.apiGenerateOpen, func() {
		ui.Column(c).Width(520).Gap(16).Children(func() {
			dialogTitle(c, "Generate OpenAPI draft")
			ui.Text(c, "Choose a saved collection. Unsaved request tabs are not included.").FontSize(12).TextColor(t.TextMuted)
			ui.MenuButton(c, cmp.Or(a.apiCollection, "Saved (ungrouped)"), func(m *ui.Menu) {
				for _, name := range slices.Sorted(maps.Keys(groupRequests(a.requests))) {
					if m.Item(cmp.Or(name, "Saved (ungrouped)")).Checked(name == a.apiCollection).Chosen() {
						a.apiCollection = name
					}
				}
			}).Label("Source collection").FillWidth()
			ui.Text(c, "Header/query/body values and scripts are omitted. Paths remain literal. Authentication, types and response schemas need review. This creates a new specification; existing ones are never overwritten.").FontSize(12).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.apiGenerateOpen = false
				}
				if ui.PrimaryButton(c, "Generate draft").Clicked() {
					a.generateAPI(a.apiCollection)
					a.apiGenerateOpen = false
				}
			})
		})
	})
}

func (a *app) generateAPI(collection string) {
	spec, err := generateOpenAPI(a.requests, collection)
	if err != nil {
		a.setMessage("Generate API: " + err.Error())
		return
	}
	a.addAPI(spec, false)
	a.workspace, a.requestsSection = 1, 1
	a.setMessage("Generated independent OpenAPI draft; review notes before publishing")
}

func (a *app) importOpenAPI() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Import OpenAPI", Filters: []mygo.FileFilter{{Name: "OpenAPI", Extensions: []string{"json", "yaml", "yml"}}}})
		if err != nil {
			a.setMessage("Import OpenAPI: " + err.Error())
			return
		}
		if len(paths) == 0 {
			return
		}
		data, err := os.ReadFile(paths[0])
		if err != nil {
			a.setMessage("Import OpenAPI: " + err.Error())
			return
		}
		if len(data) > 16<<20 {
			a.setMessage("OpenAPI documents are limited to 16 MiB")
			return
		}
		a.win.Update(func() {
			a.addAPI(apiSpec{Source: string(data)}, false)
			a.workspace, a.requestsSection = 1, 1
		})
	}()
}

func (a *app) exportOpenAPI(d *apiDocument) {
	source := d.code.Buffer.String()
	if source != d.checked || d.parsed == nil {
		return
	}
	ext := "yaml"
	if jsontext.Value(source).IsValid() {
		ext = "json"
	}
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Title: "Export OpenAPI", DefaultPath: "openapi." + ext, Filters: []mygo.FileFilter{{Name: "OpenAPI", Extensions: []string{ext}}}})
		if err != nil {
			a.setMessage("Export OpenAPI: " + err.Error())
			return
		}
		if path == "" {
			return
		}
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			a.setMessage("Export OpenAPI: " + err.Error())
			return
		}
		a.setMessage("Exported OpenAPI to " + path)
	}()
}
