package app

import (
	"cmp"
	"crypto/rand"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
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
		uikit.EmptyState(c, "From traffic to an API contract", "Save captured requests in a collection, then generate an OpenAPI draft.", func() {
			uikit.Hint(c, "Or create or import a standalone OpenAPI 3.0 / 3.1 specification.")
			if uikit.SecondaryButton(c, "Create API").Clicked() {
				a.newAPI()
			}
		})
		return
	}
	current := d.code.Buffer.String() == d.checked
	valid := current && d.parsed != nil
	ui.Column(c).Key(d.spec.ID).Fill().Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, d.title()).FontSize(uikit.TextBody).Bold().Grow(1).MinWidth(0).SingleLine()
			if uikit.SecondaryButton(c, "Validate").Clicked() {
				d.validate()
			}
			if uikit.SecondaryButton(c, "Export…").Label("Export OpenAPI").Disabled(!valid).Clicked() {
				a.exportOpenAPI(d)
			}
			if uikit.SecondaryButton(c, "Create collection").Label("Create collection from OpenAPI").Disabled(!valid).Clicked() {
				a.importOpenAPICollection(d.parsed, d.title())
			}
			if uikit.PrimaryButton(c, "Save API").Clicked() {
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
		ui.Text(c, status).FontSize(uikit.TextMeta).FontFeatures("tnum").TextColor(color).Padding(0, t.Space(3), t.Space(2), t.Space(3))
		if d.spec.Fingerprint != "" {
			changed := d.spec.Fingerprint != collectionFingerprint(collectionRequests(a.requests, d.spec.Collection))
			ui.Row(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).
				Border(0, t.Border).BorderWidth(1, 0, 1, 0).Children(func() {
				message := "Source: " + cmp.Or(d.spec.Collection, "Saved") + " · independent draft"
				if changed {
					message = "Source collection changed — review before updating this contract."
				}
				uikit.Meta(c, message).Grow(1)
				if uikit.SecondaryButton(c, "Generate new draft").Clicked() {
					a.generateAPI(d.spec.Collection)
				}
			})
		}
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(1)).AlignItems(ui.Center).
			Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
			uikit.Tabs(c, &d.tab, "API", "Endpoints", "OpenAPI", "Review notes")
		})
		switch d.tab {
		case 0:
			if valid {
				a.apiEndpoints(c, t, d)
			} else {
				uikit.EmptyState(c, "Endpoints unavailable", cmp.Or(d.problem, "Validate the edited source to refresh endpoints."), func() {
					if uikit.SecondaryButton(c, "Edit OpenAPI").Clicked() {
						d.tab = 1
					}
				})
			}
		case 1:
			if current && d.problem != "" {
				ui.Scroll(c).Height(90).Padding(t.Space(2), t.Space(3)).Children(func() {
					ui.Text(c, d.problem).FontSize(uikit.TextMeta).TextColor(t.Danger)
				})
				ui.Divider(c)
			}
			ui.Column(c).Grow(1).MinHeight(0).Padding(t.Space(3)).Children(func() {
				requestCodeEditor(c, d.code)
			})
		case 2:
			ui.Scroll(c).Grow(1).Padding(t.Space(4)).Gap(t.Space(4)).Children(func() {
				uikit.Heading(c, "Review before publishing")
				uikit.Hint(c, "Validation checks OpenAPI structure, not whether your service implements the contract. No automatic or bidirectional synchronization is performed.")
				for _, note := range d.spec.Notes {
					uikit.Body(c, "• "+note)
				}
				uikit.Hint(c, "Only standalone JSON/YAML is supported. Internal #/ references are resolved; external files and URLs are never fetched. Original source is preserved on save/export.")
			})
		}
	})
}

func (a *app) apiEndpoints(c *ui.Context, t *ui.Theme, d *apiDocument) {
	endpoints := apiOperations(d.parsed)
	if len(endpoints) == 0 {
		uikit.EmptyState(c, "No HTTP endpoints yet", "Define paths in OpenAPI, or generate a draft from saved requests.", nil)
		return
	}
	if d.endpoint == "" {
		d.endpoint = endpoints[0].method + " " + endpoints[0].path
	}
	ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
		ui.Sidebar(c, &d.endpoint, func() {
			for _, endpoint := range endpoints {
				label := endpoint.method + " " + endpoint.path
				uikit.SidebarItem(c, label, nil, endpoint.path, d.endpoint == label).Label("Endpoint: " + label).Children(func() {
					method := ui.Text(c, endpoint.method).Font(uikit.FontMono).FontSize(uikit.TextMeta).SingleLine().Width(48)
					if d.endpoint != label {
						method.TextColor(uikit.MethodColor(endpoint.method, t))
					}
				})
			}
		}).Width(210).FillHeight().Padding(t.Space(2)).Gap(0).Background(uikit.Colors(t).Panel)
		ui.Divider(c)
		selected := 0
		for i, endpoint := range endpoints {
			if endpoint.method+" "+endpoint.path == d.endpoint {
				selected = i
			}
		}
		endpoint := endpoints[selected]
		d.endpoint = endpoint.method + " " + endpoint.path
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Padding(t.Space(3)).Gap(t.Space(3)).Children(func() {
			ui.Row(c).Height(uikit.HeightTabs).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				uikit.MethodLabel(c, endpoint.method)
				uikit.Heading(c, endpoint.path).Font(uikit.FontMono).SingleLine().Grow(1).MinWidth(0)
			})
			if summary := cmp.Or(endpoint.op.Summary, endpoint.op.OperationID); summary != "" && summary != endpoint.method+" "+endpoint.path {
				uikit.Body(c, summary)
			}
			if uikit.SecondaryButton(c, "Open in Requests").Width(160).Clicked() {
				a.addDocument(requestFromOperation(d.parsed, endpoint), "")
				a.sourceAPI = d.spec.ID
				a.composeResult = "Draft from OpenAPI. Review server, variables, authentication and body before sending."
			}
			uikit.Meta(c, "Uses the first effective server and required parameters. Body examples are copied when provided; schemas are not turned into invented payloads.")
			ui.Divider(c)
			ui.Row(c).Height(uikit.HeightTabs).AlignItems(ui.Center).Children(func() {
				uikit.Label(c, "OPERATION · schemas and references")
			})
			if d.details == nil || d.detailsFor != d.endpoint {
				data, _ := json.Marshal(endpoint.op, jsontext.WithIndent("  "))
				buf := editor.NewBuffer(string(data))
				buf.SetLanguage(editor.LanguageNamed("json"))
				d.details = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, Label: "Operation schema"}
				d.detailsFor = d.endpoint
			}
			requestCodeEditor(c, d.details)
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
	uikit.Modal(c, &a.apiGenerateOpen, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, "Generate OpenAPI draft")
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(4)).Children(func() {
				uikit.Hint(c, "Choose a saved collection. Unsaved request tabs are not included.")
				ui.PopoverMenuButton(c, cmp.Or(a.apiCollection, "Saved (ungrouped)"), func(m *ui.Menu) {
					for _, name := range slices.Sorted(maps.Keys(groupRequests(a.requests))) {
						if m.Item(cmp.Or(name, "Saved (ungrouped)")).Checked(name == a.apiCollection).Chosen() {
							a.apiCollection = name
						}
					}
				}).
					Label("Source collection").FillWidth().Height(uikit.HeightControl).FontSize(uikit.TextBody).Radius(uikit.RadiusControl)
				uikit.Hint(c, "Header/query/body values and scripts are omitted. Paths remain literal. Authentication, types and response schemas need review. This creates a new specification; existing ones are never overwritten.")
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					a.apiGenerateOpen = false
				}
				if uikit.PrimaryButton(c, "Generate draft").Clicked() {
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
