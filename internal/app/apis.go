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
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/openapi"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/locales"
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
	lint       []openapi.Issue
	name       string
	problem    string
	tab        int
	endpoint   string
	detailTab  int
	details    *editor.State
	detailsFor string
	sync       apiSyncState
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
	doc, lint, err := parseOpenAPIAt(d.spec.Path, d.checked)
	if err != nil {
		d.problem = err.Error()
		return false
	}
	d.parsed, d.lint = doc, lint
	d.name = doc.Info.Title
	return true
}

// loadSavedAPIs shows the specs saved at apisPath. Drafts with unsaved changes
// stay as they are, and a saved spec under the same ID is not shown twice.
func (a *app) loadSavedAPIs() {
	if a.apisPath == "" {
		return
	}
	specs, err := loadAPIs(a.file(a.apisPath))
	if err != nil {
		a.fail(locales.Apis(), err)
		return
	}
	var kept []*apiDocument
	for _, d := range a.apis {
		if d.dirty() {
			kept = append(kept, d)
		}
	}
	choice := a.apiChoice
	a.apis = kept
	for _, spec := range specs {
		if !slices.ContainsFunc(kept, func(d *apiDocument) bool { return d.spec.ID == spec.ID }) {
			a.addAPI(spec, true)
		}
	}
	if slices.ContainsFunc(a.apis, func(d *apiDocument) bool { return d.spec.ID == choice }) {
		a.apiChoice = choice
	}
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
	d := &apiDocument{spec: spec, code: &editor.State{Buffer: editor.NewBuffer(spec.Source), Label: locales.OpenapiSource(), NoScrollPastEnd: true}}
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
		a.warn(locales.OpenapiValidationFailedNothingWas())
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
		a.warn(locales.ApisNoLocalStoragePath())
		return
	}
	if err := saveAPIs(a.file(a.apisPath), list); err != nil {
		a.fail(locales.SaveApi(), err)
		return
	}
	d.spec = next
	d.saved = new(next)
	d.code.Buffer.MarkSaved()
	a.done(locales.ApiSaved(d.title()))
}

func (a *app) apiView(c *ui.Context, t *ui.Theme) {
	d := a.activeAPI()
	if d == nil {
		uikit.EmptyState(c, locales.FromTrafficToAnApi(), locales.SaveCapturedRequestsInA(), func() {
			uikit.Hint(c, locales.OrCreateOrImportA())
			if uikit.SecondaryButton(c, locales.CreateApi()).Clicked() {
				a.newAPI()
			}
		})
		return
	}
	current := d.code.Buffer.String() == d.checked
	valid := current && d.parsed != nil
	ui.Column(c.Key(d.spec.ID)).Fill().Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			uikit.Body(c, d.title()).FontWeight(500).Grow(1).MinWidth(0).SingleLine()
			if uikit.SecondaryButton(c, locales.Validate()).Clicked() {
				d.validate()
			}
			if d.spec.Collection != "" {
				if uikit.SecondaryButton(c, locales.Sync()).Clicked() {
					a.openAPISync(d)
				}
			}
			if uikit.SecondaryButton(c, locales.Export()).Label(locales.ExportOpenapi()).Disabled(!valid).Clicked() {
				a.exportOpenAPI(d)
			}
			if uikit.SecondaryButton(c, locales.CreateCollection2()).Label(locales.CreateCollectionFromOpenapi()).Disabled(!valid).Clicked() {
				a.importOpenAPICollection(d.parsed, d.title(), nil, "")
			}
			// On the endpoint reader the one primary action is opening the
			// operation as a request. Saving belongs to the source tabs.
			if d.tab == 0 {
				if uikit.SecondaryButton(c, locales.SaveApi()).Clicked() {
					a.saveAPI(d)
				}
			} else if uikit.PrimaryButton(c, locales.SaveApi()).Clicked() {
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
			if len(d.lint) > 0 {
				status += fmt.Sprintf("  ·  Lint: %d", len(d.lint))
			}
		}
		uikit.Caption(c, status).FontFeatures("tnum").TextColor(color).Padding(0, t.Space(uikit.SpaceL), t.Space(uikit.SpaceM), t.Space(uikit.SpaceL))
		if d.spec.Fingerprint != "" {
			changed := d.spec.Fingerprint != collectionFingerprint(collectionRequests(a.requests, d.spec.Collection))
			ui.Row(c).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).
				Border(0, t.Border).BorderWidth(1, 0, 1, 0).Children(func() {
				message := locales.ApiDraftSource(cmp.Or(d.spec.Collection, locales.Saved()))
				if changed {
					message = locales.ApiSourceChanged()
				}
				uikit.Meta(c, message).Grow(1)
				if uikit.SecondaryButton(c, locales.GenerateNewDraft()).Clicked() {
					a.generateAPI(d.spec.Collection)
				}
			})
		}
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(uikit.SpaceXS)).AlignItems(ui.Center).
			Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
			uikit.Tabs(c, &d.tab, "API", locales.Endpoints(), locales.Openapi(), locales.ReviewNotes())
		})
		switch d.tab {
		case 0:
			if valid {
				a.apiEndpoints(c, t, d)
			} else {
				uikit.EmptyState(c, locales.EndpointsUnavailable(), cmp.Or(d.problem, "Validate the edited source to refresh endpoints."), func() {
					if uikit.SecondaryButton(c, locales.EditOpenapi()).Clicked() {
						d.tab = 1
					}
				})
			}
		case 1:
			if current && d.problem != "" {
				ui.Scroll(c).Height(90).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Children(func() {
					uikit.ErrorText(c, d.problem)
				})
				ui.Divider(c)
			}
			ui.Column(c).Grow(1).MinHeight(0).Padding(t.Space(uikit.SpaceL)).Children(func() {
				requestCodeEditor(c, d.code)
			})
		case 2:
			ui.Scroll(c).Grow(1).Padding(t.Space(uikit.SpaceXL)).Gap(t.Space(uikit.SpaceXL)).Children(func() {
				uikit.Heading(c, locales.ReviewBeforePublishing())
				uikit.Hint(c, locales.ValidationChecksOpenapiStructureNot())
				for _, note := range d.spec.Notes {
					uikit.Body(c, "• "+note)
				}
				uikit.Hint(c, locales.OnlyStandaloneJsonYamlIs())
			})
		}
		a.apiSyncModal(c, t, d)
	})
}

func (a *app) apiEndpoints(c *ui.Context, t *ui.Theme, d *apiDocument) {
	endpoints := apiOperations(d.parsed)
	if len(endpoints) == 0 {
		uikit.EmptyState(c, locales.NoHttpEndpointsYet(), locales.DefinePathsInOpenapiOr(), nil)
		return
	}
	if d.endpoint == "" {
		d.endpoint = endpoints[0].method + " " + endpoints[0].path
	}
	ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c).Width(210).FillHeight().Padding(t.Space(uikit.SpaceM)).Background(uikit.Colors(t).Chrome).Children(func() {
			uikit.SidebarList(c, "api-endpoints", &d.endpoint, func(id string) { d.endpoint = id }, func() {
				for _, endpoint := range endpoints {
					label := endpoint.method + " " + endpoint.path
					uikit.SidebarItem(c, label, nil, endpoint.path, d.endpoint == label, !a.windowBehind).Label(locales.EndpointLabelNamed(label)).Children(func() {
						method := uikit.DataMeta(c, endpoint.method).SingleLine().Width(48)
						if d.endpoint != label {
							method.TextColor(uikit.MethodColor(endpoint.method, t))
						}
					})
				}
			})
		})
		ui.Divider(c)
		selected := 0
		for i, endpoint := range endpoints {
			if endpoint.method+" "+endpoint.path == d.endpoint {
				selected = i
			}
		}
		endpoint := endpoints[selected]
		d.endpoint = endpoint.method + " " + endpoint.path
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Padding(t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceL)).Children(func() {
			ui.Row(c).Height(uikit.HeightTabs).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				uikit.MethodLabel(c, endpoint.method)
				uikit.Heading(c, endpoint.path).Font(uikit.FontMono).SingleLine().Grow(1).MinWidth(0)
				open := uikit.Tip(c, uikit.PrimaryButton(c, locales.OpenInRequests()).Width(uikit.WidthField), locales.UsesTheFirstEffectiveServer())
				if open.Clicked() {
					a.openAPIRequest(d, endpoint)
				}
			})
			if summary := cmp.Or(endpoint.op.Summary, endpoint.op.OperationID); summary != "" && summary != endpoint.method+" "+endpoint.path {
				uikit.Body(c, summary)
			}
			uikit.SubTabs(c, &d.detailTab, "API endpoint", locales.Overview(), locales.Raw())
			if d.detailTab == 1 {
				a.apiRawOperation(c, d, endpoint)
				return
			}
			ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(uikit.SpaceXL)).Children(func() {
				ui.Column(c).Gap(t.Space(uikit.SpaceS)).Children(func() {
					apiParameterSections(c, t, endpoint)
				})
				ui.Column(c).Gap(t.Space(uikit.SpaceS)).Children(func() {
					apiRequestBodySection(c, t, endpoint.op.RequestBody)
				})
				ui.Column(c).Gap(t.Space(uikit.SpaceS)).Children(func() {
					apiResponsesSection(c, t, endpoint.op.Responses)
				})
			})
		})
	})
}

// openAPIRequest creates the editable draft for an operation. The draft is
// deliberately unsent: the reader still reviews the server, variables,
// authentication and body before sending.
func (a *app) openAPIRequest(d *apiDocument, endpoint apiOperation) {
	a.addDocument(requestFromOperation(d.parsed, endpoint), "")
	a.sourceAPI = d.spec.ID
	a.composeResult = locales.DraftFromOpenapiReviewServer()
}

// apiRawOperation keeps the operation's JSON behind the reader's Raw tab.
func (a *app) apiRawOperation(c *ui.Context, d *apiDocument, endpoint apiOperation) {
	if d.details == nil || d.detailsFor != d.endpoint {
		data, _ := json.Marshal(endpoint.op, jsontext.WithIndent("  "))
		buf := editor.NewBuffer(string(data))
		buf.SetLanguage(editor.LanguageNamed("json"))
		d.details = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, Label: locales.OperationSchema()}
		d.detailsFor = d.endpoint
	}
	requestCodeEditor(c, d.details)
}

// apiParameterSections groups an operation's parameters the way the OpenAPI
// document does: path, query, header and cookie. Operation-level parameters
// replace path-level parameters with the same name and location.
func apiParameterSections(c *ui.Context, t *ui.Theme, endpoint apiOperation) {
	params := map[string]*openapi3.Parameter{}
	for _, refs := range []openapi3.Parameters{endpoint.item.Parameters, endpoint.op.Parameters} {
		for _, ref := range refs {
			if ref != nil && ref.Value != nil {
				p := ref.Value
				params[p.In+"\x00"+p.Name] = p
			}
		}
	}
	groups := map[string][]*openapi3.Parameter{}
	for _, key := range slices.Sorted(maps.Keys(params)) {
		p := params[key]
		groups[p.In] = append(groups[p.In], p)
	}
	uikit.Heading(c, locales.Parameters())
	if len(groups) == 0 {
		uikit.Hint(c, locales.NoParameters())
		return
	}
	apiFieldHeader(c, t)
	for _, in := range []string{"path", "query", "header", "cookie"} {
		group := groups[in]
		if len(group) == 0 {
			continue
		}
		uikit.Label(c, apiParameterGroup(in))
		for _, p := range group {
			apiDataRow(c, t, p.Name, apiTypeName(p.Schema), p.Description, p.Required, 0)
		}
	}
}

func apiParameterGroup(in string) string {
	switch in {
	case "path":
		return locales.PathParameters()
	case "query":
		return locales.QueryParameters()
	case "header":
		return locales.HeaderParameters()
	case "cookie":
		return locales.CookieParameters()
	default:
		if in == "" {
			return locales.Parameters()
		}
		return locales.ParameterSection(strings.ToUpper(in[:1]) + in[1:])
	}
}

// apiRequestBodySection shows each media type and the fields its schema
// declares. It does not synthesize a payload when the contract only names a
// schema; Open in Requests is where a draft is made.
func apiRequestBodySection(c *ui.Context, t *ui.Theme, body *openapi3.RequestBodyRef) {
	ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
		uikit.Heading(c, locales.RequestBody())
		if body != nil && body.Value != nil && body.Value.Required {
			uikit.Label(c, locales.Required())
		}
	})
	if body == nil || body.Value == nil || len(body.Value.Content) == 0 {
		uikit.Hint(c, locales.NoRequestBody())
		return
	}
	if body.Value.Description != "" {
		uikit.Hint(c, body.Value.Description)
	}
	for _, media := range slices.Sorted(maps.Keys(body.Value.Content)) {
		if mt := body.Value.Content[media]; mt != nil {
			apiSchemaLine(c, media, mt.Schema)
			apiSchemaTree(c, t, mt.Schema, 0, map[string]bool{})
		}
	}
}

// apiResponsesSection lists responses by status code, with each media type's
// schema name below it. Response headers, links and full schemas stay in the
// Raw tab; the reader is about what the operation accepts and returns.
func apiResponsesSection(c *ui.Context, t *ui.Theme, responses *openapi3.Responses) {
	uikit.Heading(c, locales.Responses())
	if responses == nil || responses.Len() == 0 {
		uikit.Hint(c, locales.NoResponses())
		return
	}
	for _, code := range slices.Sorted(maps.Keys(responses.Map())) {
		ref := responses.Value(code)
		if ref == nil || ref.Value == nil {
			continue
		}
		description := ""
		if ref.Value.Description != nil {
			description = *ref.Value.Description
		}
		ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Start).Children(func() {
			uikit.Mono(c, code).Width(72).TextColor(uikit.StatusColor(apiStatus(code), t))
			uikit.Hint(c, description).Grow(1)
		})
		for _, media := range slices.Sorted(maps.Keys(ref.Value.Content)) {
			if mt := ref.Value.Content[media]; mt != nil {
				// The status code and its schema name answer the reader's
				// question; the request body above already shows the shared
				// Project fields, and Raw has every response schema.
				apiSchemaLine(c, media, mt.Schema)
			}
		}
	}
}

// apiSchemaLine names the media type and its schema together, so a line reads
// "application/json · Project" rather than a bare schema name under a media
// type the reader has to connect back to it.
func apiSchemaLine(c *ui.Context, media string, ref *openapi3.SchemaRef) {
	value := media
	if typ := apiTypeName(ref); typ != "—" {
		value += " · " + typ
	}
	ui.Row(c).Gap(c.Theme().Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
		uikit.Label(c, locales.Schema2())
		uikit.Mono(c, value).Grow(1).MinWidth(0)
	})
}

func apiStatus(code string) int {
	if n, err := strconv.Atoi(code); err == nil {
		return n
	}
	return 0
}

func apiFieldHeader(c *ui.Context, t *ui.Theme) {
	ui.Row(c).Height(uikit.HeightRow).Padding(0, t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
		uikit.Label(c, locales.Name()).Width(uikit.WidthField)
		uikit.Label(c, locales.TypeText()).Width(uikit.WidthNarrow)
		uikit.Label(c, locales.Required()).Width(80)
		uikit.Label(c, locales.Description()).Grow(1)
	})
}

// apiDataRow is one read-only field line. Descriptions wrap rather than
// truncate, because a schema's prose is part of what the reader came for.
func apiDataRow(c *ui.Context, t *ui.Theme, name, typ, description string, required bool, indent float32) {
	ui.Row(c).MinHeight(uikit.HeightRow).Padding(0, t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Start).Children(func() {
		if indent > 0 {
			ui.Box(c).Width(indent).Shrink(0)
		}
		uikit.Mono(c, name).Width(uikit.WidthField).SingleLine()
		uikit.Mono(c, typ).Width(uikit.WidthNarrow).SingleLine()
		ui.Box(c).Width(80).Shrink(0).Children(func() {
			if required {
				uikit.Label(c, locales.Required())
			} else {
				uikit.Meta(c, locales.Optional())
			}
		})
		if description != "" {
			uikit.Hint(c, description).Grow(1)
		} else {
			ui.Box(c).Grow(1)
		}
	})
}

// apiSchemaTree walks object properties, array items and composed schemas.
// seen stops a recursive $ref; depth stops a schema that is deep without
// being recursive. Both keep a malformed contract from freezing the reader.
func apiSchemaTree(c *ui.Context, t *ui.Theme, ref *openapi3.SchemaRef, depth int, seen map[string]bool) {
	if ref == nil || ref.Value == nil || depth > 6 {
		return
	}
	schema := ref.Value
	if ref.Ref != "" {
		if seen[ref.Ref] {
			return
		}
		seen[ref.Ref] = true
		defer delete(seen, ref.Ref)
	}
	indent := float32(depth) * t.Space(uikit.SpaceL)
	if len(schema.Properties) > 0 {
		required := map[string]bool{}
		for _, name := range schema.Required {
			required[name] = true
		}
		for _, name := range slices.Sorted(maps.Keys(schema.Properties)) {
			property := schema.Properties[name]
			if property == nil || property.Value == nil {
				continue
			}
			apiDataRow(c, t, name, apiTypeName(property), property.Value.Description, required[name], indent)
			apiSchemaTree(c, t, property, depth+1, seen)
		}
		return
	}
	if schema.Type != nil && schema.Type.Is("array") && schema.Items != nil && schema.Items.Value != nil {
		apiDataRow(c, t, "items", apiTypeName(schema.Items), schema.Items.Value.Description, false, indent)
		apiSchemaTree(c, t, schema.Items, depth+1, seen)
		return
	}
	for _, part := range []struct {
		label string
		refs  openapi3.SchemaRefs
	}{{"oneOf", schema.OneOf}, {"anyOf", schema.AnyOf}, {"allOf", schema.AllOf}} {
		for i, branch := range part.refs {
			if branch == nil || branch.Value == nil {
				continue
			}
			apiDataRow(c, t, fmt.Sprintf("%s %d", part.label, i+1), apiTypeName(branch), branch.Value.Description, false, indent)
			apiSchemaTree(c, t, branch, depth+1, seen)
		}
	}
	if schema.AdditionalProperties.Schema != nil && schema.AdditionalProperties.Schema.Value != nil {
		apiDataRow(c, t, "additionalProperties", apiTypeName(schema.AdditionalProperties.Schema), schema.AdditionalProperties.Schema.Value.Description, false, indent)
		apiSchemaTree(c, t, schema.AdditionalProperties.Schema, depth+1, seen)
	}
}

func apiTypeName(ref *openapi3.SchemaRef) string {
	if ref == nil {
		return "—"
	}
	if ref.Ref != "" {
		return ref.Ref[strings.LastIndex(ref.Ref, "/")+1:]
	}
	schema := ref.Value
	if schema == nil {
		return "—"
	}
	if schema.Type != nil && schema.Type.Is("array") {
		return "array<" + apiTypeName(schema.Items) + ">"
	}
	switch {
	case len(schema.OneOf) > 0:
		return "oneOf"
	case len(schema.AnyOf) > 0:
		return "anyOf"
	case len(schema.AllOf) > 0:
		return "allOf"
	case schema.Type != nil && len(schema.Type.Slice()) > 0:
		return strings.Join(schema.Type.Slice(), " | ")
	case len(schema.Properties) > 0:
		return "object"
	default:
		return "—"
	}
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
	uikit.Modal(c, &a.apiGenerateOpen, locales.GenerateOpenapiDraft(), func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, locales.GenerateOpenapiDraft())
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(uikit.SpaceXL)).Children(func() {
				uikit.Hint(c, locales.ChooseASavedCollectionUnsaved())
				uikit.MenuSelect(c, cmp.Or(a.apiCollection, "Saved (ungrouped)"), func(m *ui.Menu) {
					for _, name := range slices.Sorted(maps.Keys(groupRequests(a.requests))) {
						if m.Item(cmp.Or(name, "Saved (ungrouped)")).Checked(name == a.apiCollection).Chosen() {
							a.apiCollection = name
						}
					}
				}).
					Label(locales.SourceCollection()).FillWidth()
				uikit.Hint(c, locales.HeaderQueryBodyValuesAnd())
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					a.apiGenerateOpen = false
				}
				if uikit.PrimaryButton(c, locales.GenerateDraft()).Clicked() {
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
		a.fail(locales.GenerateApi(), err)
		return
	}
	a.addAPI(spec, false)
	a.page = pageAPI
	a.done(locales.GeneratedIndependentOpenapiDraftReview())
}

func (a *app) importOpenAPI() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: locales.ImportOpenapi2(), Filters: []mygo.FileFilter{{Name: "OpenAPI", Extensions: []string{"json", "yaml", "yml"}}}})
		if err != nil {
			a.fail(locales.ImportOpenapi2(), err)
			return
		}
		if len(paths) == 0 {
			return
		}
		data, err := os.ReadFile(paths[0])
		if err != nil {
			a.fail(locales.ImportOpenapi2(), err)
			return
		}
		if len(data) > 16<<20 {
			a.warn(locales.OpenapiDocumentsAreLimitedTo())
			return
		}
		a.win.Update(func() {
			a.addAPI(apiSpec{Source: string(data), Path: paths[0]}, false)
			a.page = pageAPI
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
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Title: locales.ExportOpenapi(), DefaultPath: "openapi." + ext, Filters: []mygo.FileFilter{{Name: "OpenAPI", Extensions: []string{ext}}}})
		if err != nil {
			a.fail(locales.ExportOpenapi(), err)
			return
		}
		if path == "" {
			return
		}
		if err := store.WriteFile(path, []byte(source)); err != nil {
			a.fail(locales.ExportOpenapi(), err)
			return
		}
		a.done(locales.ExportedOpenapiTo(path))
	}()
}
