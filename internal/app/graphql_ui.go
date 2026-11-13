package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/graphql"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// graphqlDocument is a GraphQL body's schema state. The schema text itself is
// saved with the request; parsed values are rebuilt only when it changes.
type graphqlDocument struct {
	loading chan graphqlSchemaResult
	cancel  context.CancelFunc
	problem string

	parsed      *graphql.Schema
	parsedFor   string
	parsedError string

	ops    []graphql.Operation
	opsFor string
	opsErr error

	checkFor, checkSchema string
	check                 []editor.Diagnostic
}

type graphqlSchemaResult struct {
	schema string
	source string
	err    error
}

func (d *requestDocument) graphqlState() *graphqlDocument {
	if d.graphqlUI == nil {
		d.graphqlUI = &graphqlDocument{}
	}
	return d.graphqlUI
}

func (d *requestDocument) stopGraphQL() {
	if d.graphqlUI == nil {
		return
	}
	if d.graphqlUI.cancel != nil {
		d.graphqlUI.cancel()
	}
	d.graphqlUI.loading = nil
}

// graphqlSchema parses the saved SDL once per change. A hand-edited invalid
// schema leaves completion off and is reported on the body row.
func (d *requestDocument) graphqlSchema() *graphql.Schema {
	g := d.graphqlState()
	if g.parsedFor == d.graphql.Schema {
		return g.parsed
	}
	g.parsedFor = d.graphql.Schema
	g.parsed, g.parsedError = nil, ""
	if strings.TrimSpace(d.graphql.Schema) != "" {
		schema, err := graphql.ParseSchema([]byte(d.graphql.Schema))
		g.parsed = schema
		if err != nil {
			g.parsedError = err.Error()
		}
	}
	return g.parsed
}

// graphqlOperations lists the document's operations for the selector. A
// half-typed document has an error but keeps the last selection.
func (d *requestDocument) graphqlOperations() ([]graphql.Operation, error) {
	g := d.graphqlState()
	if g.opsFor == d.graphql.Query {
		return g.ops, g.opsErr
	}
	g.opsFor = d.graphql.Query
	g.ops, g.opsErr = nil, nil
	if strings.TrimSpace(d.graphql.Query) != "" {
		g.ops, g.opsErr = graphql.Operations(d.graphql.Query)
		if g.opsErr != nil && strings.Contains(d.graphql.Query, "{{") {
			// The selector describes the document before variables expand;
			// a template in a selection is not a syntax error there.
			g.ops, g.opsErr = graphql.Operations(graphql.MaskTemplates(d.graphql.Query))
		}
	}
	return g.ops, g.opsErr
}

// graphqlDocuments are the tabs and capture drafts that may have a schema
// fetch in flight. flowDrafts holds capture edits, which are not tabs.
func (a *app) graphqlDocuments() []*requestDocument {
	docs := slices.Clone(a.documents)
	seen := map[*requestDocument]bool{}
	for _, d := range docs {
		seen[d] = true
	}
	add := func(d *requestDocument) {
		if d != nil && !seen[d] {
			seen[d] = true
			docs = append(docs, d)
		}
	}
	add(a.live)
	add(a.libraryDocument)
	for _, d := range a.flowDrafts {
		add(d)
	}
	return docs
}

func (a *app) drainGraphQL() {
	for _, d := range a.graphqlDocuments() {
		g := d.graphqlUI
		if g == nil || g.loading == nil {
			continue
		}
		select {
		case result := <-g.loading:
			g.loading, g.cancel = nil, nil
			if result.err != nil {
				g.problem = result.err.Error()
				continue
			}
			if result.source != d.composeURL {
				g.problem = "Schema response ignored: the endpoint URL changed while fetching."
				continue
			}
			d.graphql.Schema, d.graphql.SchemaSource = result.schema, result.source
			g.problem = ""
		default:
		}
	}
}

// fetchGraphQLSchema asks the endpoint for its schema through the same
// execution path as Send: variables, auth, request settings, cookies and the
// proxy apply. Scripts and checks do not run, so a schema read cannot change
// the body, a variable scope or a run.
func (a *app) fetchGraphQLSchema(d *requestDocument) {
	g := d.graphqlState()
	if g.loading != nil || d.composeWaiting {
		return
	}
	if a.srv == nil || !a.srv.Running() {
		a.warn(locales.NoProxyRunningStartCapture2())
		return
	}
	if strings.TrimSpace(d.composeURL) == "" {
		g.problem = "Enter the GraphQL endpoint URL first."
		return
	}
	r := d.draft()
	r.Method = http.MethodPost
	r.BodyMode = "GraphQL"
	r.GraphQL = request.GraphQLBody{Query: graphql.IntrospectionQuery, Variables: "{}", OperationName: "IntrospectionQuery"}
	r.Body, r.BodyFile, r.Form = "", "", nil
	r.Pre, r.Post, r.Checks, r.TokenSources, r.Examples = "", "", request.Checks{}, nil, nil
	// The introspection query is always a JSON envelope, whatever body type
	// the authored request uses.
	headers := request.ParseHeaderBlock(r.Headers)
	headers.Set("Content-Type", "application/json")
	r.Headers = request.HeaderBlock(headers)
	in := a.sendContext(r.Request)
	in.Folders = slices.Clone(in.Folders)
	for i := range in.Folders {
		in.Folders[i].Pre, in.Folders[i].Post = "", ""
	}
	vars, srv, jar := a.activeVars(), a.srv, a.requestCookies()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	g.cancel, g.problem = cancel, ""
	answers := make(chan graphqlSchemaResult, 1)
	g.loading = answers
	source := d.composeURL
	go func() {
		defer cancel()
		got := r.Execute(ctx, srv, in, vars, jar)
		result := graphqlSchemaResult{source: source}
		switch {
		case got.Err != nil:
			result.err = fmt.Errorf("schema request failed in %s: %w", cmp.Or(got.Stage, "send"), got.Err)
		case got.Response == nil:
			result.err = errors.New("schema request returned no response")
		case got.Response.Status < 200 || got.Response.Status >= 300:
			status := fmt.Sprintf("HTTP %d %s", got.Response.Status, http.StatusText(got.Response.Status))
			if problem := graphql.IntrospectionProblem([]byte(got.Response.ResponseBody)); problem != "" {
				if strings.Contains(strings.ToLower(problem), "introspection") {
					problem = "introspection is disabled: " + problem
				}
				result.err = fmt.Errorf("schema request failed with %s: %s", status, problem)
			} else {
				result.err = fmt.Errorf("schema request failed with %s", status)
			}
		default:
			schema, err := graphql.ParseIntrospection([]byte(got.Response.ResponseBody))
			if err != nil {
				result.err = err
			} else {
				result.schema = schema.SDL()
			}
		}
		answers <- result
	}()
}

// chooseGraphQLSchema loads an SDL document or an introspection JSON file.
func (a *app) chooseGraphQLSchema(d *requestDocument) {
	if a.win == nil {
		return
	}
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: locales.LoadGraphqlSchema()})
		if err != nil || len(paths) == 0 {
			return
		}
		path := paths[0]
		schema, err := loadGraphQLSchemaFile(path)
		win.Update(func() {
			g := d.graphqlState()
			if err != nil {
				g.problem = err.Error()
				return
			}
			d.graphql.Schema, d.graphql.SchemaSource = schema.SDL(), path
			g.problem = ""
		})
	}()
}

// loadGraphQLSchemaFile reads SDL or introspection JSON. The raw file may be
// up to the introspection limit; ParseSchema still enforces the smaller SDL
// limit on what is kept.
func loadGraphQLSchemaFile(path string) (*graphql.Schema, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("the schema must be a regular file")
	}
	if info.Size() > graphql.MaxIntrospectionBytes {
		return nil, fmt.Errorf("schema file exceeds %d KiB", graphql.MaxIntrospectionBytes>>10)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return graphql.ParseSchema(data)
}

// graphqlSchemaRow is the compact toolbar under the Query/Variables switch:
// schema state, fetch/load actions, the operation selector and, on Variables,
// the skeleton action.
func (a *app) graphqlSchemaRow(c *ui.Context, d *requestDocument) {
	g := d.graphqlState()
	t := c.Theme()
	ops, opsErr := d.graphqlOperations()
	matchesSaved := slices.ContainsFunc(ops, func(op graphql.Operation) bool { return op.Name == d.graphql.OperationName })
	ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
		schema := d.graphqlSchema()
		statusText, statusTip, statusDanger := "No schema", "Fetch introspection or load an SDL or introspection JSON file to complete and check fields.", false
		switch {
		case g.loading != nil:
			statusText, statusTip = "Schema · fetching…", ""
		case schema != nil:
			statusText = fmt.Sprintf("Schema · %d types", schema.TypeCount())
			if d.graphql.SchemaSource != "" {
				statusTip = "Loaded from " + d.graphql.SchemaSource
			} else {
				statusTip = ""
			}
		case g.parsedError != "":
			statusText, statusTip, statusDanger = "Schema is invalid", g.parsedError, true
		}
		status := uikit.Meta(c, statusText).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		if statusTip != "" {
			uikit.Tip(c, status, statusTip)
		}
		if statusDanger {
			status.TextColor(t.Danger)
		}
		fetchLabel := "Fetch schema"
		if schema != nil && d.graphql.SchemaSource == d.composeURL {
			fetchLabel = "Refresh schema"
		}
		fetch := uikit.SecondaryButton(c, fetchLabel).Disabled(g.loading != nil || d.composeWaiting)
		if fetch.Clicked() {
			a.fetchGraphQLSchema(d)
		}
		if uikit.SecondaryButton(c, locales.LoadSchema()).Disabled(g.loading != nil).Clicked() {
			a.chooseGraphQLSchema(d)
		}
		if opsErr == nil {
			switch {
			case len(ops) > 1:
				uikit.Meta(c, locales.Operation())
				names := make([]string, len(ops))
				for i, op := range ops {
					names[i] = op.Name
				}
				choice := d.graphql.OperationName
				if choice == "" || !slices.Contains(names, choice) {
					choice = "Select operation"
				}
				selectElement := uikit.Select(c, &choice, names).Label(locales.GraphqlOperation()).Width(170).FontSize(uikit.TextMeta)
				if selectElement.Changed() && choice != "Select operation" {
					d.graphql.OperationName = choice
				}
			case len(ops) == 1 && d.graphql.OperationName != "" && !matchesSaved:
				uikit.Tip(c, uikit.Meta(c, locales.SelectOperation()), locales.TheSavedOperationNoLonger())
			case len(ops) == 1 && ops[0].Name != "":
				uikit.Tip(c, uikit.Meta(c, "Operation · "+ops[0].Name), locales.TheDocumentSOnlyOperation())
			case len(ops) == 1:
				uikit.Meta(c, locales.AnonymousOperation())
			}
		}
		if d.graphqlTab == 1 {
			validChoice := len(ops) == 1 || matchesSaved
			generate := uikit.SecondaryButton(c, locales.GenerateVariables()).Disabled(
				g.loading != nil || schema == nil || strings.TrimSpace(d.graphql.Variables) != "" || !validChoice)
			uikit.Tip(c, generate, locales.FillAnEmptyVariablesEditor())
			if generate.Clicked() {
				a.generateGraphQLVariables(d, schema, ops)
			}
		}
	})
	if g.problem != "" {
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			uikit.Tip(c, ui.Text(c, "Schema: "+oneLine(g.problem)).FontSize(uikit.TextMeta).TextColor(t.Danger).SingleLine().MinWidth(0).Ellipsis("…"), g.problem)
		})
	}
}

func (a *app) generateGraphQLVariables(d *requestDocument, schema *graphql.Schema, ops []graphql.Operation) {
	if schema == nil || strings.TrimSpace(d.graphql.Variables) != "" {
		return
	}
	name := d.graphql.OperationName
	if len(ops) == 1 {
		name = ops[0].Name
	}
	query := d.graphql.Query
	if strings.Contains(query, "{{") {
		query = graphql.MaskTemplates(query)
	}
	skeleton, err := schema.Variables(query, name)
	if err != nil {
		d.graphqlState().problem = err.Error()
		return
	}
	d.graphql.Variables, d.varsFor = skeleton, ""
	d.graphqlState().problem = ""
}

// wireGraphQLQueryEditor gives the query editor both kinds of help: {{ }}
// variables win while a reference is open, then the loaded schema supplies
// fields, arguments, types, enum values, fragments and exact-token hovers.
func (a *app) wireGraphQLQueryEditor(d *requestDocument) {
	state := d.queryEditor
	state.OnCompletion = func(r editor.CompletionRequest) {
		text := state.Buffer.String()
		if start, prefix, ok := openVariableToken(text, r.Offset); ok {
			suffix := ""
			if !strings.HasPrefix(text[r.Offset:], "}}") {
				suffix = "}}"
			}
			state.ShowCompletions(start+2, a.variableCompletionItems(d.composeCollection, prefix, suffix))
			return
		}
		schema := d.graphqlSchema()
		if schema == nil {
			return
		}
		result := schema.Complete(text, r.Offset)
		if len(result.Items) == 0 {
			state.ShowCompletions(result.Start, nil)
			return
		}
		state.ShowCompletions(result.Start, graphqlCompletionItems(result.Items))
	}
	state.OnHover = func(r editor.HoverRequest) {
		text := state.Buffer.String()
		if ref, ok := request.VariableAt(text, r.Offset); ok {
			state.ShowHover(ref.Start, ref.End, a.variablePreview(d.composeCollection, ref.Name), nil)
			return
		}
		schema := d.graphqlSchema()
		if schema == nil {
			return
		}
		if hover, ok := schema.Hover(text, r.Offset); ok {
			state.ShowHover(hover.Start, hover.End, hover.Text, nil)
		}
	}
}

func graphqlCompletionItems(items []graphql.Completion) []editor.Completion {
	out := make([]editor.Completion, 0, len(items))
	for _, item := range items {
		kind := editor.CompletionField
		switch item.Kind {
		case graphql.CompletionArgument:
			kind = editor.CompletionField
		case graphql.CompletionType:
			kind = editor.CompletionType
		case graphql.CompletionEnum:
			kind = editor.CompletionConstant
		case graphql.CompletionVariable:
			kind = editor.CompletionVariable
		case graphql.CompletionDirective:
			kind = editor.CompletionKeyword
		case graphql.CompletionFragment:
			kind = editor.CompletionModule
		}
		insert := item.Insert
		if insert == "" {
			insert = item.Label
		}
		out = append(out, editor.Completion{
			Label: item.Label, Detail: item.Detail, Documentation: item.Documentation,
			Insert: insert, Kind: kind,
		})
	}
	return out
}

// setGraphQLDiagnostics combines undefined {{variables}} with the schema's
// executable-document validation. Schema results are cached by query and
// schema text so a frame that changed neither does not validate again.
func (a *app) setGraphQLDiagnostics(d *requestDocument) {
	state := d.queryEditor
	if state == nil {
		return
	}
	text := d.graphql.Query
	var diagnostics []editor.Diagnostic
	if strings.Contains(text, "{{") {
		set := a.variableHintSet(d.composeCollection)
		for _, ref := range request.VariableRefs(text) {
			if _, ok := set.hint(ref.Name); !ok {
				diagnostics = append(diagnostics, editor.Diagnostic{
					Start: ref.Start, End: ref.End, Severity: editor.SeverityError,
					Message: "Undefined variable: " + ref.Name, Source: "variables",
				})
			}
		}
	}
	g := d.graphqlState()
	if g.checkFor != text || g.checkSchema != d.graphql.Schema {
		g.checkFor, g.checkSchema, g.check = text, d.graphql.Schema, nil
		if schema := d.graphqlSchema(); schema != nil && !strings.Contains(text, "{{") {
			for _, diagnostic := range schema.Diagnostics(text) {
				g.check = append(g.check, editor.Diagnostic{
					Start: diagnostic.Start, End: diagnostic.End, Severity: editor.SeverityError,
					Message: diagnostic.Message, Source: "graphql",
				})
			}
		}
	}
	diagnostics = append(diagnostics, g.check...)
	state.SetDiagnostics(state.Buffer.Version(), diagnostics)
}
