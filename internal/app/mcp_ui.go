package app

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"image"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

const (
	mcpTabConnection = iota
	mcpTabTools
	mcpTabResources
	mcpTabPrompts
	mcpTabHeaders
	mcpTabAuth
)

type mcpDocument struct {
	session      *mcpSession
	tab          int
	responseTab  int
	catalogTab   int
	toolTab      int
	selected     int
	problem      string
	query        string
	reader       *editor.State
	readerKey    string
	argsEditor   *editor.State
	argsFor      string
	schemaReader *editor.State
	schemaFor    string
	split        float32
	list         ui.ListState
	catalogList  ui.ListState
	resultFor    string
	resultBmp    []*ui.Bitmap
	resultErr    []string
	imageJob     *mcpImageJob
}

const (
	mcpMaxImagePixels  = 16 << 20
	mcpMaxResultPixels = 32 << 20
)

type mcpImageResult struct {
	key string
	bmp []*ui.Bitmap
	err []string
}

type mcpImageJob struct {
	key     string
	answers chan mcpImageResult
}

func (d *requestDocument) mcpState() *mcpDocument {
	if d.mcpConfig.Transport == "" {
		d.mcpConfig.Transport = request.MCPTransportStreamableHTTP
	}
	if d.mcpUI == nil {
		d.mcpUI = &mcpDocument{split: 240}
	}
	return d.mcpUI
}

func (a *app) drainMCP() {
	for _, d := range a.documents {
		u := d.mcpUI
		if d.protocol != "MCP" || u == nil {
			continue
		}
		if u.imageJob != nil {
			select {
			case result := <-u.imageJob.answers:
				if u.resultFor == result.key {
					u.resultBmp, u.resultErr = result.bmp, result.err
				}
				u.imageJob = nil
			default:
			}
		}
		if u.session == nil {
			continue
		}
		if u.session.snapshot().finished {
			d.composeWaiting, d.sendCancel, d.sendCancelling = false, nil, false
		}
	}
}

func (a *app) connectMCP(d *requestDocument) {
	u := d.mcpState()
	r := d.value().Request
	in, vars := a.sendContext(r), a.activeVars()
	plan, err := r.PrepareMCP(in, vars)
	if err != nil {
		u.problem = err.Error()
		return
	}
	if plan.Transport != request.MCPTransportStdio && a.srv != nil && a.srv.Running() {
		plan.Proxy, plan.ProxyCA, plan.Jar = a.srv.Addr(), a.srv.CACertificate(), a.requestCookies()
	}
	if plan.Transport == request.MCPTransportStdio {
		shape := mcpConsentKey(d, plan, a.mcpConsentsKey)
		full := mcpConsentFullKey(shape, plan, a.mcpConsentsKey)
		if a.mcpConsents == nil {
			a.mcpConsents = map[string]mcpConsentApproval{}
		}
		if _, ok := a.mcpConsents[full]; ok {
			a.markMCPConsentUsed(shape, full)
			a.startMCP(d, plan)
			return
		}
		var changed []string
		if approval, ok := a.mcpConsents[shape]; ok {
			changed = mcpChangedEnvNames(approval.Env, plan, a.mcpConsentsKey)
		}
		a.mcpConsent = mcpConsentState{open: true, d: d, plan: plan, shape: shape, full: full,
			valuesChanged: len(changed) > 0, changed: changed}
		return
	}
	a.startMCP(d, plan)
}

// startMCP opens the session after any stdio confirmation has been granted.
func (a *app) startMCP(d *requestDocument, plan request.MCPPlan) {
	u := d.mcpState()
	r := d.value().Request
	in, vars := a.sendContext(r), a.activeVars()
	u.session = openMCPSession(plan, r, in, vars, a.invalidate)
	u.problem, u.query, u.readerKey, u.reader, u.selected = "", "", "", nil, 0
	u.argsEditor, u.argsFor, u.schemaReader, u.schemaFor = nil, "", nil, ""
	u.responseTab, u.catalogTab, u.toolTab = 0, 0, 0
	u.resultFor, u.resultBmp, u.resultErr = "", nil, nil
	d.composeWaiting, d.sendCancel, d.sendCancelling = true, u.session.disconnect, false
	d.historyPick, d.comparing = 0, false
}

func (a *app) mcpActions(d *requestDocument, editing bool) []action {
	if d == nil || !editing || d.protocol != "MCP" {
		return nil
	}
	u := d.mcpState()
	s := u.session.snapshot()
	connected := s.connected && !s.finished
	idle := connected && !s.busy && !d.sendCancelling
	return []action{
		do("request.mcp-cancel", "Cancel MCP request", func() { u.session.cancelOp() }).when(connected && s.busy, "No MCP request is running"),
		do("request.mcp-disconnect", "Disconnect MCP", func() { u.session.disconnect() }).when(connected, "Connect an MCP document first"),
		do("request.mcp-refresh", "Refresh MCP catalog", func() { u.session.refreshCatalog() }).when(idle, "Connect an MCP document first"),
		do("request.mcp-call", "Call MCP tool", func() {
			if _, err := u.session.callTool(d.mcpConfig); err != nil {
				u.problem = err.Error()
			}
		}).when(idle && d.mcpConfig.Tool != "", "Choose a tool and connect first"),
		do("request.mcp-read", "Read MCP resource", func() {
			if _, err := u.session.readResource(d.mcpConfig); err != nil {
				u.problem = err.Error()
			}
		}).when(idle && d.mcpConfig.ResourceURI != "", "Enter a resource URI and connect first"),
		do("request.mcp-prompt", "Get MCP prompt", func() {
			if _, err := u.session.getPrompt(d.mcpConfig); err != nil {
				u.problem = err.Error()
			}
		}).when(idle && d.mcpConfig.Prompt != "", "Choose a prompt and connect first"),
		do("request.mcp-fill-args", "Fill MCP arguments from schema", func() { a.mcpFillArguments(d) }).when(d.mcpConfig.Tool != "", "Choose a tool first"),
	}
}

// mcpFillArguments builds a skeleton object from the selected tool's input
// schema, using defaults and enum values where the server gives them.
func (a *app) mcpFillArguments(d *requestDocument) {
	u := d.mcpState()
	s := u.session.snapshot()
	var tool *request.MCPTool
	for i := range s.tools {
		if s.tools[i].Name == d.mcpConfig.Tool {
			tool = &s.tools[i]
			break
		}
	}
	if tool == nil {
		u.problem = "Choose a tool from the catalog first."
		return
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(tool.InputSchema), &schema); err != nil {
		u.problem = "The tool's input schema is not valid JSON."
		return
	}
	data, err := json.Marshal(mcpSchemaSkeleton(schema), jsontext.WithIndent("  "))
	if err != nil {
		u.problem = err.Error()
		return
	}
	d.mcpConfig.ToolArgs = string(data)
	u.problem = ""
}

func mcpSchemaSkeleton(schema map[string]any) map[string]any {
	out := map[string]any{}
	properties, _ := schema["properties"].(map[string]any)
	for name, raw := range properties {
		property, _ := raw.(map[string]any)
		out[name] = mcpSchemaValue(property)
	}
	return out
}

func mcpSchemaValue(property map[string]any) any {
	if value, ok := property["default"]; ok {
		return value
	}
	if values, ok := property["enum"].([]any); ok && len(values) > 0 {
		return values[0]
	}
	switch property["type"] {
	case "string":
		return ""
	case "number", "integer":
		return 0
	case "boolean":
		return false
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	}
	return nil
}

func (a *app) mcpEditor(c *ui.Context, t *ui.Theme, d *requestDocument) {
	u := d.mcpState()
	s := u.session.snapshot()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(3)).Children(func() {
			uikit.Tabs(c, &u.tab, "MCP editor", "Connection", "Tools", "Resources", "Prompts", "Headers", "Auth").Grow(1)
		})
		if u.problem != "" {
			ui.Text(c, u.problem).TextColor(t.Danger).FontSize(uikit.TextMeta).MaxHeight(55).Padding(0, t.Space(3))
		}
		if s.catalogNotice != "" && !s.busy {
			ui.Row(c).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, s.catalogNotice).Grow(1).SingleLine()
				a.actionButton(c, "request.mcp-refresh", "Refresh")
			})
		}
		ui.Column(c).Grow(1).MinHeight(0).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
			switch u.tab {
			case mcpTabConnection:
				a.mcpConnectionEditor(c, t, d, s)
			case mcpTabTools:
				a.mcpToolsEditor(c, t, d, u, s)
			case mcpTabResources:
				a.mcpResourcesEditor(c, t, d, s)
			case mcpTabPrompts:
				a.mcpPromptsEditor(c, t, d, u, s)
			case mcpTabHeaders:
				if d.mcpConfig.Transport == request.MCPTransportStdio {
					uikit.Hint(c, "stdio MCP servers receive credentials through environment variables, not HTTP headers.")
					return
				}
				ui.Column(c).Fill().Disabled(d.composeWaiting).Children(func() { a.headerEditor(c, d, t) })
			case mcpTabAuth:
				if d.mcpConfig.Transport == request.MCPTransportStdio {
					uikit.Hint(c, "stdio MCP servers receive credentials through environment variables, not HTTP auth.")
					return
				}
				ui.Column(c).Fill().Disabled(d.composeWaiting).Children(func() { a.authEditor(c, d, t) })
			}
		})
	})
}

func (a *app) mcpConnectionEditor(c *ui.Context, t *ui.Theme, d *requestDocument, s mcpSnapshot) {
	ui.Scroll(c).Grow(1).Gap(t.Space(2)).Padding(0, t.Space(1)).Disabled(d.composeWaiting).Children(func() {
		ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
			uikit.Select(c, &d.mcpConfig.Transport, []string{request.MCPTransportStreamableHTTP, request.MCPTransportSSE, request.MCPTransportStdio}).
				Label("MCP transport").Width(190)
			timeout := strconv.Itoa(a.mcpEffectiveTimeout(d))
			uikit.Select(c, &timeout, []string{"10", "30", "60", "300", "3600"}).Label("MCP timeout seconds").Width(96).
				Tooltip("Connect and each request use this timeout; cancelling never undoes server-side changes").OnChange(func() {
				d.mcpConfig.TimeoutSeconds, _ = strconv.Atoi(timeout)
			})
		})
		version := cmp.Or(d.mcpConfig.ProtocolVersion, "auto")
		uikit.Select(c, &version, []string{"auto", "2025-06-18", "2025-11-25", "2026-07-28"}).Label("MCP protocol version").Width(180).
			Tooltip("auto negotiates the latest version; a fixed version fails if the server chooses another").OnChange(func() {
			d.mcpConfig.ProtocolVersion = version
		})
		if d.mcpConfig.Transport == request.MCPTransportStdio {
			a.variableInput(c, "mcp-command", &d.mcpConfig.Command, "Command", d.composeCollection).
				Placeholder("/path/to/mcp-server").Font(uikit.FontMono)
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, "Arguments · one per line · {{variables}} supported").Grow(1)
			})
			a.variableTextArea(c, "mcp-args", &d.mcpConfig.Args, "Command arguments", d.composeCollection).Height(64).Font(uikit.FontMono)
			a.variableInput(c, "mcp-dir", &d.mcpConfig.WorkingDir, "Working directory", d.composeCollection).
				Placeholder("Optional · the app's directory by default").Font(uikit.FontMono)
			uikit.Meta(c, "Environment · values may use {{variables}}; masked rows are not sent").Padding(0, t.Space(1))
			a.requestFieldsEditor(c, &d.mcpConfig.Env, fieldsOptions{secret: true, collection: d.composeCollection}, "environment variable")
			uikit.Hint(c, "The command starts only when you press Connect, and stops on Disconnect, closing the document or quitting. Nothing is run at load time. Environment values are saved with the document and never logged.")
		} else {
			uikit.Hint(c, "The address bar is the MCP endpoint. Headers, HTTP auth, variables and the request's certificate settings apply. HTTPS is captured while the proxy runs; without it the connection is direct.")
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				verify := a.inheritance(d.composeCollection).InheritSettings(d.settings).Verifies()
				if ui.Switch(c, &verify).Label("Verify TLS certificate").Changed() {
					d.setVerify(verify)
				}
				uikit.Meta(c, "System roots plus Settings → Certificates; a matching client certificate is presented").Grow(1).SingleLine()
			})
			if !a.inheritance(d.composeCollection).InheritSettings(d.settings).Verifies() {
				ui.Row(c).Padding(t.Space(1), t.Space(2)).Gap(t.Space(2)).Radius(uikit.RadiusControl).
					Background(t.Warning.Alpha(0.12)).AlignItems(ui.Start).Children(func() {
					ui.Icon(c, warningIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.Warning)
					ui.Text(c, "Certificate verification is off: this connection accepts any certificate, including an impostor's. Use it only for a server you control.").
						FontSize(uikit.TextMeta).TextColor(t.Warning).Grow(1)
				})
			}
			if s.connected {
				uikit.Meta(c, "Connected to "+s.connection.Endpoint).Padding(0, t.Space(1)).SingleLine()
			}
		}
		uikit.Hint(c, "MCP documents do not run HTTP scripts, Checks, token bindings, Runner, code generation or History. HTTP/SSE and stdio share the same timeline and catalog.")
	})
}

// mcpEffectiveTimeout is what PrepareMCP will use: the document's own value,
// then the request settings, then 30 seconds.
func (a *app) mcpEffectiveTimeout(d *requestDocument) int {
	if d.mcpConfig.TimeoutSeconds > 0 {
		return d.mcpConfig.TimeoutSeconds
	}
	settings := a.inheritance(d.composeCollection).InheritSettings(d.settings)
	if settings.Timeout() > 0 {
		return int(settings.Timeout() / time.Second)
	}
	return 30
}

func mcpToolByName(tools []request.MCPTool, name string) *request.MCPTool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

func mcpToolNames(tools []request.MCPTool) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Name)
	}
	return out
}

func (a *app) mcpToolsEditor(c *ui.Context, t *ui.Theme, d *requestDocument, u *mcpDocument, s mcpSnapshot) {
	tool := mcpToolByName(s.tools, d.mcpConfig.Tool)
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		field := ui.Combobox(c, &d.mcpConfig.Tool, mcpToolNames(s.tools)).Label("MCP tool").Placeholder("Choose a tool").
			Grow(1).MinWidth(0).Height(uikit.HeightControl).FontSize(uikit.TextBody).Disabled(d.composeWaiting)
		uikit.FieldFace(c, field, field.Focused())
		a.actionButton(c, "request.mcp-call", "Call tool")
	})
	if tool == nil {
		uikit.Hint(c, "Connect and refresh the catalog to choose a tool. The server's input schema can fill the arguments object.")
		return
	}
	description := cmp.Or(tool.Description, tool.Title, "No description")
	uikit.Meta(c, description).MaxLines(2)
	if tool.Annotations != "" && tool.Annotations != "null" {
		uikit.Meta(c, tool.Annotations).SingleLine().Tooltip("Tool annotations")
	}
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		uikit.Segmented(c, &u.toolTab, "Arguments", "Input schema")
		ui.Spacer(c)
		if u.toolTab == 0 {
			a.actionButton(c, "request.mcp-fill-args", "Fill from schema")
		}
	})
	if u.toolTab == 1 {
		key := tool.Name + "\x00" + tool.InputSchema
		if u.schemaFor != key {
			u.schemaFor = key
			text := tool.InputSchema
			if text == "" {
				text = "This tool has no input schema."
			}
			u.schemaReader = &editor.State{Buffer: editor.NewBuffer(text), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: "MCP tool input schema"}
			u.schemaReader.Buffer.SetLanguage(editor.LanguageNamed("json"))
		}
		requestCodeEditor(c, u.schemaReader)
		return
	}
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		uikit.Meta(c, "Arguments JSON object · {{variables}} supported · blank sends {}").Grow(1).SingleLine()
	})
	authoredText(c, &d.mcpConfig.ToolArgs, &u.argsEditor, &u.argsFor, "MCP tool arguments", "json")
	a.wireVariableEditor(u.argsEditor, d.composeCollection)
	a.setVariableDiagnostics(u.argsEditor, d.mcpConfig.ToolArgs, d.composeCollection)
}

func mcpResourceOptions(resources []request.MCPResource, templates []request.MCPResourceTemplate) []string {
	out := make([]string, 0, len(resources)+len(templates))
	for _, resource := range resources {
		out = append(out, resource.URI)
	}
	for _, template := range templates {
		out = append(out, template.URITemplate)
	}
	return out
}

func mcpResourceByURI(resources []request.MCPResource, uri string) *request.MCPResource {
	for i := range resources {
		if resources[i].URI == uri {
			return &resources[i]
		}
	}
	return nil
}

func mcpTemplateByURI(templates []request.MCPResourceTemplate, uri string) *request.MCPResourceTemplate {
	for i := range templates {
		if templates[i].URITemplate == uri {
			return &templates[i]
		}
	}
	return nil
}

func (a *app) mcpResourcesEditor(c *ui.Context, t *ui.Theme, d *requestDocument, s mcpSnapshot) {
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		field := ui.Combobox(c, &d.mcpConfig.ResourceURI, mcpResourceOptions(s.resources, s.templates)).Label("MCP resource URI").
			Placeholder("memo://welcome").Grow(1).MinWidth(0).Height(uikit.HeightControl).FontSize(uikit.TextBody).Font(uikit.FontMono).Disabled(d.composeWaiting)
		uikit.FieldFace(c, field, field.Focused())
		a.actionButton(c, "request.mcp-read", "Read resource")
	})
	if resource := mcpResourceByURI(s.resources, d.mcpConfig.ResourceURI); resource != nil {
		uikit.Meta(c, cmp.Or(resource.Title, resource.Name, resource.URI)+" · "+cmp.Or(resource.MIMEType, "unknown type")).MaxLines(2)
		if resource.Description != "" {
			uikit.Meta(c, resource.Description).MaxLines(2)
		}
	} else if template := mcpTemplateByURI(s.templates, d.mcpConfig.ResourceURI); template != nil {
		uikit.Meta(c, "Template "+template.URITemplate+" · edit its placeholders before reading").MaxLines(2)
		if template.Description != "" {
			uikit.Meta(c, template.Description).MaxLines(2)
		}
	} else {
		uikit.Hint(c, "Choose an advertised resource or template, or type any URI the server accepts. Templates keep their {placeholders}; edit them first.")
	}
	uikit.Hint(c, "resources/read returns text, blob, image or audio content in the Result tab. The URI is expanded with the connection's original variables.")
}

func mcpPromptNames(prompts []request.MCPPrompt) []string {
	out := make([]string, 0, len(prompts))
	for _, prompt := range prompts {
		out = append(out, prompt.Name)
	}
	return out
}

func mcpPromptByName(prompts []request.MCPPrompt, name string) *request.MCPPrompt {
	for i := range prompts {
		if prompts[i].Name == name {
			return &prompts[i]
		}
	}
	return nil
}

func (a *app) mcpPromptsEditor(c *ui.Context, t *ui.Theme, d *requestDocument, u *mcpDocument, s mcpSnapshot) {
	prompt := mcpPromptByName(s.prompts, d.mcpConfig.Prompt)
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		field := ui.Combobox(c, &d.mcpConfig.Prompt, mcpPromptNames(s.prompts)).Label("MCP prompt").Placeholder("Choose a prompt").
			Grow(1).MinWidth(0).Height(uikit.HeightControl).FontSize(uikit.TextBody).Disabled(d.composeWaiting)
		uikit.FieldFace(c, field, field.Focused())
		a.actionButton(c, "request.mcp-prompt", "Get prompt")
	})
	if prompt == nil {
		uikit.Hint(c, "Connect and refresh the catalog to choose a prompt.")
		return
	}
	if prompt.Description != "" {
		uikit.Meta(c, prompt.Description).MaxLines(2)
	}
	if len(prompt.Arguments) > 0 {
		var parts []string
		for _, argument := range prompt.Arguments {
			name := argument.Name
			if argument.Required {
				name += " (required)"
			}
			parts = append(parts, name)
		}
		uikit.Meta(c, "Arguments: "+strings.Join(parts, ", ")).MaxLines(2)
	}
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		uikit.Meta(c, "Arguments JSON object · string values · {{variables}} supported").Grow(1).SingleLine()
	})
	authoredText(c, &d.mcpConfig.PromptArgs, &u.argsEditor, &u.argsFor, "MCP prompt arguments", "json")
	a.wireVariableEditor(u.argsEditor, d.composeCollection)
	a.setVariableDiagnostics(u.argsEditor, d.mcpConfig.PromptArgs, d.composeCollection)
}

func (a *app) mcpResponse(c *ui.Context, t *ui.Theme, d *requestDocument) {
	u := d.mcpState()
	s := u.session.snapshot()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			uikit.Tabs(c, &u.responseTab, "MCP response", "Timeline", "Result", "Catalog", "Session").Grow(1)
			if s.busy {
				a.actionButton(c, "request.mcp-cancel", "Cancel request")
			}
			state := s.state
			if s.connected && s.server.Name != "" {
				state += " · " + cmp.Or(s.server.Title, s.server.Name)
			}
			if s.progress != "" {
				state += " · " + s.progress
			}
			uikit.Meta(c, state).SingleLine()
		})
		if u.session == nil {
			uikit.EmptyState(c, "No MCP session yet", "Choose a transport, then Connect. The session lists the server's tools, resources and prompts and keeps a bounded timeline of requests, responses, notifications and logs.", nil)
			return
		}
		if u.problem != "" {
			ui.Text(c, u.problem).TextColor(t.Danger).FontSize(uikit.TextMeta).MaxHeight(55).Padding(0, t.Space(3))
		}
		if s.problem != "" {
			ui.Text(c, s.problem).TextColor(t.Danger).FontSize(uikit.TextMeta).MaxHeight(55).Padding(0, t.Space(3))
		}
		switch u.responseTab {
		case 0:
			a.mcpTimeline(c, t, u, s)
		case 1:
			a.mcpResult(c, t, u, s)
		case 2:
			a.mcpCatalog(c, t, d, u, s)
		case 3:
			a.mcpSessionPane(c, t, u, s)
		}
	})
}

func (a *app) mcpTimeline(c *ui.Context, t *ui.Theme, u *mcpDocument, s mcpSnapshot) {
	ui.Row(c).Padding(t.Space(1), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		uikit.Input(c, &u.query).Label("Search MCP timeline").Placeholder("Search kind, name or payload").Grow(1).MinWidth(0)
		uikit.Meta(c, fmt.Sprintf("%d retained · %d evicted · 64 KiB preview", len(s.events), s.evicted)).SingleLine()
	})
	query := strings.ToLower(strings.TrimSpace(u.query))
	var events []mcpEvent
	for _, event := range s.events {
		if query == "" || strings.Contains(strings.ToLower(event.kind+" "+event.name+" "+event.text), query) {
			events = append(events, event)
		}
	}
	if len(events) == 0 {
		uikit.EmptyState(c, "No matching MCP events", "Change the search or wait for the server.", nil)
		return
	}
	ui.Split(c, &u.split, func() {
		u.list.Selected = &u.selected
		u.list.Key = func(i int) any { return events[i].seq }
		u.list.Label = func(i int) string {
			return fmt.Sprintf("MCP event %d %s %s", events[i].seq, events[i].kind, events[i].name)
		}
		focused := u.list.Handle.Focused(c)
		c.SetTheme(uikit.ListTheme(t, focused))
		ui.List(c, &u.list, len(events), func(i int) {
			event := events[i]
			if i == u.selected {
				uikit.RowMarker(c, focused)
			}
			ui.Column(c).Padding(t.Space(1), t.Space(2)).Children(func() {
				ui.Text(c, event.kind+" · "+event.name).FontSize(uikit.TextBody).SingleLine()
				uikit.Meta(c, fmt.Sprintf("#%d · %s · %d B", event.seq, event.at.Format("15:04:05.000"), event.size)).SingleLine()
			})
		}).Grow(1)
		c.SetTheme(t)
	}, func() {
		u.selected = max(0, min(u.selected, len(events)-1))
		event := events[u.selected]
		key := fmt.Sprint(event.seq)
		if u.readerKey != key {
			u.readerKey = key
			u.reader = &editor.State{Buffer: editor.NewBuffer(event.text), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: "MCP event payload"}
		}
		ui.Column(c).Fill().Children(func() {
			ui.Row(c).Padding(t.Space(1), t.Space(2)).Gap(t.Space(2)).Children(func() {
				uikit.Meta(c, fmt.Sprintf("#%d %s · %s · %s", event.seq, event.kind, event.name, event.at.Format("15:04:05.000"))).Grow(1).SingleLine()
				uikit.SecondaryButton(c, "Copy shown event").OnClick(func() { c.Services().WriteClipboard(u.reader.Buffer.String()) })
			})
			requestCodeEditor(c, u.reader)
		})
	}).Grow(1)
}

func (a *app) mcpResult(c *ui.Context, t *ui.Theme, u *mcpDocument, s mcpSnapshot) {
	if s.result == nil {
		uikit.EmptyState(c, "No MCP result yet", "Call a tool, read a resource or get a prompt. The Result tab shows text, image and audio blocks, structured content and isError.", nil)
		return
	}
	result := s.result
	key := fmt.Sprint(s.resultSeq)
	a.startMCPImageDecode(u, result, key)
	if s.progress != "" {
		uikit.Meta(c, "Progress · "+s.progress).Padding(0, t.Space(3)).SingleLine()
	}
	ui.Row(c).Padding(t.Space(1), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, result.Kind+" · "+cmp.Or(result.Title, "result")).FontSize(uikit.TextBody).Grow(1).SingleLine()
		if result.IsError {
			ui.Text(c, "isError").FontSize(uikit.TextMeta).TextColor(t.Danger)
		} else {
			uikit.Meta(c, "OK").SingleLine()
		}
		uikit.Meta(c, fmt.Sprintf("%d B · %d blocks", result.Bytes, len(result.Content))).SingleLine()
		uikit.SecondaryButton(c, "Copy result").OnClick(func() { c.Services().WriteClipboard(result.Text) })
	})
	ui.Scroll(c).Grow(1).Padding(0, t.Space(3)).Children(func() {
		ui.Column(c).Selectable().Gap(t.Space(2)).Children(func() {
			if result.Description != "" {
				uikit.Meta(c, "Description").Padding(0, 0, t.Space(1), 0)
				ui.Text(c, request.MCPPreview(result.Description, 64<<10)).FontSize(uikit.TextBody)
			}
			for i, block := range result.Content {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					label := block.Kind
					if block.Role != "" {
						label = block.Role + " · " + label
					}
					if block.MIMEType != "" {
						label += " · " + block.MIMEType
					}
					if block.Size > 0 {
						label += fmt.Sprintf(" · %d B", block.Size)
					}
					if block.URI != "" {
						label += " · " + block.URI
					}
					uikit.Meta(c, label).SingleLine()
				})
				switch {
				case block.Kind == "image" && i < len(u.resultBmp) && u.resultBmp[i] != nil:
					ui.Image(c, u.resultBmp[i]).MaxWidth(200).MaxHeight(120)
				case block.Kind == "image" || block.Kind == "audio" || block.Kind == "blob":
					if i < len(u.resultErr) && u.resultErr[i] != "" {
						uikit.Hint(c, "Cannot decode "+block.Kind+": "+u.resultErr[i])
					} else if len(block.Data) == 0 {
						uikit.Hint(c, "The server's "+block.Kind+" data was omitted because it exceeds the 1 MiB media limit.")
					} else if block.Kind == "image" && u.imageJob != nil {
						uikit.Hint(c, "Decoding "+block.Kind+"…")
					} else {
						uikit.Hint(c, fmt.Sprintf("%s data · %d bytes (shown as base64)", block.Kind, len(block.Data)))
						ui.Text(c, base64Preview(block.Data)).Font(uikit.FontMono).FontSize(uikit.TextMeta)
					}
				case block.Kind == "resource_link":
					ui.Text(c, cmp.Or(block.Name, block.URI)).Font(uikit.FontMono).FontSize(uikit.TextBody)
					if block.Description != "" {
						uikit.Meta(c, block.Description)
					}
				default:
					text := request.MCPPreview(block.Text, 64<<10)
					if text == "" {
						text = "(empty)"
					}
					ui.Text(c, text).Font(uikit.FontMono).FontSize(uikit.TextBody)
				}
			}
			if result.Structured != "" {
				uikit.Meta(c, "structuredContent").Padding(0, 0, t.Space(1), 0)
				ui.Text(c, request.MCPPreview(result.Structured, 64<<10)).Font(uikit.FontMono).FontSize(uikit.TextBody)
			}
			if result.Truncated {
				uikit.Hint(c, fmt.Sprintf("Result truncated to fit %d MiB; %d bytes were returned in total.", request.MCPMaxResultBytes>>20, result.Bytes))
			}
		})
	})
}

// startMCPImageDecode checks dimensions and decodes media off the UI thread.
// The result is cached by result sequence; a result that changes while a job
// runs starts a new job and discards the old one.
func (a *app) startMCPImageDecode(u *mcpDocument, result *request.MCPResult, key string) {
	if u.resultFor == key {
		return
	}
	u.resultFor, u.resultBmp, u.resultErr, u.imageJob = key, nil, nil, nil
	var indexes []int
	for i, block := range result.Content {
		if len(block.Data) == 0 || block.Kind != "image" && !(block.Kind == "resource" && strings.HasPrefix(block.MIMEType, "image/")) {
			continue
		}
		indexes = append(indexes, i)
	}
	if len(indexes) == 0 {
		return
	}
	job := &mcpImageJob{key: key, answers: make(chan mcpImageResult, 1)}
	u.imageJob = job
	go func() {
		decoded := mcpImageResult{key: key, bmp: make([]*ui.Bitmap, len(result.Content)), err: make([]string, len(result.Content))}
		pixels := 0
		for _, i := range indexes {
			block := result.Content[i]
			width, height, err := mcpImageDimensions(block.Data)
			if err != nil {
				decoded.err[i] = err.Error()
				continue
			}
			if width <= 0 || height <= 0 || width > mcpMaxResultPixels/height ||
				width*height > mcpMaxImagePixels || pixels+width*height > mcpMaxResultPixels {
				decoded.err[i] = fmt.Sprintf("image is %d×%d; the limit is %d megapixels per image and %d total", width, height, mcpMaxImagePixels>>20, mcpMaxResultPixels>>20)
				continue
			}
			pixels += width * height
			bmp, err := ui.DecodeBitmap(block.Data)
			if err != nil {
				decoded.err[i] = err.Error()
			} else {
				decoded.bmp[i] = bmp
			}
		}
		job.answers <- decoded
		a.invalidate()
	}()
}

func mcpImageDimensions(data []byte) (int, int, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	return config.Width, config.Height, nil
}

func base64Preview(data []byte) string {
	const limit = 48 << 10
	text := strings.TrimRight(base64.StdEncoding.EncodeToString(data[:min(len(data), limit)]), "=")
	if len(data) > limit {
		return text + "\n… " + fmt.Sprint(len(data)-limit) + " bytes not shown"
	}
	return text
}

func (a *app) mcpCatalog(c *ui.Context, t *ui.Theme, d *requestDocument, u *mcpDocument, s mcpSnapshot) {
	ui.Row(c).Padding(t.Space(1), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		uikit.Segmented(c, &u.catalogTab, "Tools", "Resources", "Prompts")
		ui.Spacer(c)
		uikit.Meta(c, fmt.Sprintf("%d %s · %d %s · %d %s · %d %s",
			len(s.tools), plural(len(s.tools), "tool", "tools"),
			len(s.resources), plural(len(s.resources), "resource", "resources"),
			len(s.templates), plural(len(s.templates), "template", "templates"),
			len(s.prompts), plural(len(s.prompts), "prompt", "prompts"))).SingleLine()
		a.actionButton(c, "request.mcp-refresh", "Refresh")
	})
	switch u.catalogTab {
	case 0:
		if len(s.tools) == 0 {
			uikit.EmptyState(c, "No tools listed", "The server did not advertise tools, or the catalog has not loaded yet.", nil)
			return
		}
		ui.Scroll(c).Grow(1).Padding(0, t.Space(2)).Children(func() {
			for _, tool := range s.tools {
				tool := tool
				row := ui.Row(c).Padding(t.Space(2), t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).OnClick(func() {
					d.mcpConfig.Tool = tool.Name
					u.tab, u.toolTab, u.problem = mcpTabTools, 0, ""
				})
				if row.Hovered() {
					row.Background(t.SurfaceHover)
				}
				row.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						ui.Text(c, cmp.Or(tool.Title, tool.Name)).FontSize(uikit.TextBody)
						if tool.Description != "" {
							uikit.Meta(c, tool.Description).MaxLines(2)
						}
					})
					uikit.Meta(c, tool.Name).SingleLine()
				})
			}
		})
	case 1:
		if len(s.resources) == 0 && len(s.templates) == 0 {
			uikit.EmptyState(c, "No resources listed", "The server did not advertise resources or templates.", nil)
			return
		}
		ui.Scroll(c).Grow(1).Padding(0, t.Space(2)).Children(func() {
			for _, resource := range s.resources {
				resource := resource
				row := ui.Row(c).Padding(t.Space(2), t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).OnClick(func() {
					d.mcpConfig.ResourceURI = resource.URI
					u.tab, u.problem = mcpTabResources, ""
				})
				if row.Hovered() {
					row.Background(t.SurfaceHover)
				}
				row.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						ui.Text(c, cmp.Or(resource.Title, resource.Name, resource.URI)).FontSize(uikit.TextBody)
						uikit.Meta(c, resource.URI+" · "+cmp.Or(resource.MIMEType, "unknown type")).SingleLine()
					})
					uikit.Meta(c, "resource").SingleLine()
				})
			}
			for _, template := range s.templates {
				template := template
				row := ui.Row(c).Padding(t.Space(2), t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).OnClick(func() {
					d.mcpConfig.ResourceURI = template.URITemplate
					u.tab, u.problem = mcpTabResources, ""
				})
				if row.Hovered() {
					row.Background(t.SurfaceHover)
				}
				row.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						ui.Text(c, cmp.Or(template.Title, template.Name, template.URITemplate)).FontSize(uikit.TextBody)
						uikit.Meta(c, template.URITemplate+" · "+cmp.Or(template.MIMEType, "unknown type")).SingleLine()
					})
					uikit.Meta(c, "template").SingleLine()
				})
			}
		})
	case 2:
		if len(s.prompts) == 0 {
			uikit.EmptyState(c, "No prompts listed", "The server did not advertise prompts.", nil)
			return
		}
		ui.Scroll(c).Grow(1).Padding(0, t.Space(2)).Children(func() {
			for _, prompt := range s.prompts {
				prompt := prompt
				row := ui.Row(c).Padding(t.Space(2), t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).OnClick(func() {
					d.mcpConfig.Prompt = prompt.Name
					u.tab, u.problem = mcpTabPrompts, ""
				})
				if row.Hovered() {
					row.Background(t.SurfaceHover)
				}
				row.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						ui.Text(c, cmp.Or(prompt.Title, prompt.Name)).FontSize(uikit.TextBody)
						if prompt.Description != "" {
							uikit.Meta(c, prompt.Description).MaxLines(2)
						}
					})
					uikit.Meta(c, prompt.Name).SingleLine()
				})
			}
		})
	}
}

func (a *app) mcpSessionPane(c *ui.Context, t *ui.Theme, u *mcpDocument, s mcpSnapshot) {
	info := s.connection
	ui.Scroll(c).Grow(1).Padding(0, t.Space(3)).Children(func() {
		ui.Column(c).Selectable().Gap(t.Space(2)).Children(func() {
			server := cmp.Or(s.server.Title, s.server.Name, "MCP server")
			ui.Text(c, server+" "+s.server.Version).FontSize(uikit.TextBody).Bold()
			uikit.Meta(c, "MCP "+cmp.Or(s.server.ProtocolVersion, "unknown")+" · "+info.Transport).SingleLine()
			if info.Endpoint != "" {
				uikit.Meta(c, "Endpoint "+info.Endpoint).SingleLine()
			}
			if info.Command != "" {
				uikit.Meta(c, fmt.Sprintf("Command %s · %d %s · %d environment %s", info.Command, info.ArgsCount,
					plural(info.ArgsCount, "argument", "arguments"), info.EnvCount, plural(info.EnvCount, "variable", "variables"))).MaxLines(2)
			}
			var settings []string
			settings = append(settings, "timeout "+cmp.Or(info.Timeout, "none"))
			if info.Insecure {
				settings = append(settings, "certificate verification off")
			} else {
				settings = append(settings, "certificates verified")
			}
			if info.Proxied {
				settings = append(settings, "through capture proxy")
			}
			uikit.Meta(c, strings.Join(settings, " · ")).MaxLines(2)
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, "Capabilities").Grow(1)
				uikit.SecondaryButton(c, "Copy capabilities").OnClick(func() { c.Services().WriteClipboard(s.server.Capabilities) })
			})
			ui.Text(c, s.server.Capabilities).Font(uikit.FontMono).FontSize(uikit.TextBody)
			if s.server.Instructions != "" {
				uikit.Meta(c, "Server instructions").Padding(t.Space(2), 0, 0, 0)
				ui.Text(c, s.server.Instructions).FontSize(uikit.TextBody)
			}
		})
	})
}
