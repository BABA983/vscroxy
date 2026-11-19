package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui/editor"
)

// variableScope is one of the scopes a definition lives in. Path names the
// environment or the collection/folder; the rest use an empty path. It is the
// app's own name for one row of the scope model in internal/request.
type variableScope struct {
	Kind string
	Path string
}

func (s variableScope) key() string { return s.Kind + "\x00" + s.Path }

func (s variableScope) label() string {
	if s.Kind == "" {
		return locales.UndefinedVariable()
	}
	return variableScopeName(scopeString(s))
}

func (s variableScope) valid() bool { return s.Kind != "" }

func scopeString(s variableScope) string {
	switch s.Kind {
	case "Environment", "Collection", "Folder":
		return s.Kind + " · " + s.Path
	default:
		return s.Kind
	}
}

func scopeFromString(scope string) variableScope {
	kind, path, _ := strings.Cut(scope, " · ")
	switch kind {
	case "Environment", "Collection", "Folder":
		return variableScope{Kind: kind, Path: path}
	default:
		return variableScope{Kind: kind}
	}
}

// variableTarget is the name and definition scope an action starts from.
// Collection is where the caret was, so unscoped uses can be resolved with
// the same hint set an editor uses.
type variableTarget struct {
	Name       string
	Scope      variableScope
	Collection string
	Defined    bool
}

func (a *app) variableTargetFromCaret() (variableTarget, bool) {
	t := a.problems.variableCaret
	if t == nil || t.name == "" {
		return variableTarget{}, false
	}
	collection := ""
	if t.doc != nil {
		collection = t.doc.composeCollection
	}
	target := variableTarget{Name: t.name, Collection: collection}
	hint, ok := a.variableHint(collection, t.name)
	if !ok || hint.Dynamic || hint.Vault {
		return target, !hint.Dynamic && !hint.Vault
	}
	target.Scope, target.Defined = scopeFromString(hint.Scope), true
	return target, true
}

// variableFieldRef is the authored text of one field and the editor state that
// may hold a newer copy of it, as in quickFixTarget.
type variableFieldRef struct {
	text       *string
	state      *editor.State
	seen       *string
	generation *uint64
}

func (r variableFieldRef) current() string {
	if r.text == nil {
		return ""
	}
	if r.state != nil && r.state.Buffer.String() == *r.text {
		return r.state.Buffer.String()
	}
	return *r.text
}

// variableField is one scanned field of a saved request, an open draft or a
// collection/folder. owner and key say where a checked change is applied.
type variableField struct {
	owner, key, code     string
	resource, collection string
	field                string
	script               bool
	text                 string
}

// variableMatch is one reference or definition shown in the references view
// and in the rename preview. Byte offsets are in the field's snapshot.
type variableMatch struct {
	Owner, Key, Code     string
	Resource, Collection string
	Field                string
	Text                 string
	Start, End           int
	Line                 int
	LineText             string
	Scope                variableScope
	ScopeLabel           string
	Kind                 string // "reference", "script", "definition" or "search"
	Index                int    // row or map definition this came from
	Checked              bool
	Disabled             bool
	// Replacement is the new bytes for one change. For a text field it is a
	// snippet inserted at Start..End; for a value or definition it is the
	// whole new value, which keeps regexp captures in one place.
	Replacement string
}

// location returns the label and line used by both dialogs.
func (m variableMatch) location() string {
	if m.Line <= 0 {
		return m.Field
	}
	lineStart := strings.LastIndex(m.Text[:min(m.Start, len(m.Text))], "\n") + 1
	return fmt.Sprintf("%s · %d:%d", m.Field, m.Line, m.Start-lineStart+1)
}

// variableFieldSource adapts an open draft or saved request to one field
// walker. get returns a pointer into the live value, so a change applied from
// the preview reaches the same bytes the app would send or save.
type variableFieldSource struct {
	owner, key           string
	resource, collection string
	value                savedRequest
	headerBulk           bool
	get                  func(string) (variableFieldRef, bool)
}

// variableSources lists each saved request once. An open draft replaces its
// saved copy so a rename sees and edits the text on screen.
func (a *app) variableSources() []variableFieldSource {
	var out []variableFieldSource
	openKeys := map[string]bool{}
	seen := map[*requestDocument]bool{}
	addDoc := func(d *requestDocument) {
		if d == nil || seen[d] {
			return
		}
		seen[d] = true
		if d.savedKey != "" {
			openKeys[d.savedKey] = true
		}
		r := d.value()
		out = append(out, variableFieldSource{
			owner: "document", key: d.debugKey, resource: d.title(),
			collection: r.Collection, value: r, headerBulk: d.headerBulk,
			get: func(code string) (variableFieldRef, bool) { return variableDocumentField(d, code) },
		})
	}
	for _, d := range a.documents {
		addDoc(d)
	}
	addDoc(a.libraryDocument)
	for i, r := range a.requests {
		key := r.key()
		if openKeys[key] {
			continue
		}
		index := i
		value := r
		out = append(out, variableFieldSource{
			owner: "saved", key: key, resource: r.Name,
			collection: r.Collection, value: value,
			get: func(code string) (variableFieldRef, bool) {
				return variableSavedField(&a.requests[index], code)
			},
		})
	}
	return out
}

// variableDocumentField resolves a field code to the pointer the draft reads
// on send, including the code editor's buffer where one exists.
func variableDocumentField(d *requestDocument, code string) (variableFieldRef, bool) {
	if field, text, state, seen, generation, ok := d.quickFixField(code); ok {
		return variableFieldRef{text: text, state: state, seen: seen, generation: generation}, field != ""
	}
	return variableFieldRef{}, false
}

func variableSavedField(r *savedRequest, code string) (variableFieldRef, bool) {
	ref := func(text *string) (variableFieldRef, bool) { return variableFieldRef{text: text}, true }
	switch code {
	case searchFieldName:
		return ref(&r.Name)
	case problemFieldURL:
		return ref(&r.URL)
	case problemFieldBody:
		return ref(&r.Body)
	case problemFieldPre:
		return ref(&r.Pre)
	case problemFieldPost:
		return ref(&r.Post)
	case problemFieldHeaderRaw:
		return ref(&r.Headers)
	case problemFieldDescription:
		return ref(&r.Description)
	case "graphql-query":
		return ref(&r.GraphQL.Query)
	case "graphql-vars":
		return ref(&r.GraphQL.Variables)
	case "body-file":
		return ref(&r.BodyFile)
	case "grpc-method":
		return ref(&r.GRPC.Method)
	}
	if strings.HasPrefix(code, "auth:") {
		if text := authFieldPointer(&r.Auth, "Auth."+strings.TrimPrefix(code, "auth:")); text != nil {
			return ref(text)
		}
		return variableFieldRef{}, false
	}
	if part, at, ok := rowFieldCode(code, "header-"); ok {
		if at < 1 || at > len(r.HeaderList) {
			return variableFieldRef{}, false
		}
		switch part {
		case "key":
			return ref(&r.HeaderList[at-1].Name)
		case "value":
			return ref(&r.HeaderList[at-1].Value)
		}
	}
	if part, at, ok := rowFieldCode(code, problemFieldFormPrefix); ok {
		if at < 1 || at > len(r.Form) {
			return variableFieldRef{}, false
		}
		switch part {
		case "key":
			return ref(&r.Form[at-1].Name)
		case "value":
			return ref(&r.Form[at-1].Value)
		}
	}
	if part, at, ok := rowFieldCode(code, "path-"); ok {
		if at < 1 || at > len(r.PathVariables) {
			return variableFieldRef{}, false
		}
		switch part {
		case "key":
			return ref(&r.PathVariables[at-1].Name)
		case "value":
			return ref(&r.PathVariables[at-1].Value)
		}
	}
	if at, ok := strings.CutPrefix(code, "grpc-message-"); ok {
		index, err := strconv.Atoi(at)
		if err == nil && index >= 1 && index <= len(r.GRPC.Messages) {
			return ref(&r.GRPC.Messages[index-1])
		}
	}
	return variableFieldRef{}, false
}

func variableFolderField(f *request.Folder, code string) (variableFieldRef, bool) {
	ref := func(text *string) (variableFieldRef, bool) { return variableFieldRef{text: text}, true }
	switch code {
	case problemFieldDescription:
		return ref(&f.Description)
	case problemFieldPre:
		return ref(&f.Pre)
	case problemFieldPost:
		return ref(&f.Post)
	}
	if strings.HasPrefix(code, "auth:") {
		if text := authFieldPointer(&f.Auth, "Auth."+strings.TrimPrefix(code, "auth:")); text != nil {
			return ref(text)
		}
	}
	return variableFieldRef{}, false
}

// variableRequestFields enumerates the authored fields that carry {{name}}
// references and script variable-name literals. It uses the same fields the
// send and the Variables panel read; the header rows win over the raw block
// when the table is the source of truth.
func (s variableFieldSource) fields() []variableField {
	var out []variableField
	add := func(code, label string, script bool) {
		ref, ok := s.get(code)
		if !ok || ref.text == nil {
			return
		}
		text := ref.current()
		if text == "" {
			return
		}
		out = append(out, variableField{
			owner: s.owner, key: s.key, code: code,
			resource: s.resource, collection: s.collection,
			field: label, script: script, text: text,
		})
	}
	add(problemFieldURL, locales.Url(), false)
	for _, field := range variablePathFields(s.value.PathVariables) {
		add(field.code, field.label, false)
	}
	// The block and the rows are two views of one value, so scanning both
	// would show every use twice.
	if s.headerBulk || len(s.value.HeaderList) == 0 {
		add(problemFieldHeaderRaw, locales.Headers(), false)
	} else {
		for _, field := range variableRowFields("header-", s.value.HeaderList) {
			add(field.code, field.label, false)
		}
	}
	switch s.value.BodyMode {
	case "", "Raw":
		add(problemFieldBody, locales.RequestBody(), false)
	case "GraphQL":
		add("graphql-query", locales.GraphqlQuery(), false)
		add("graphql-vars", locales.GraphqlVariables(), false)
	case "Binary":
		add("body-file", locales.BinaryBody(), false)
	default:
		for _, field := range variableRowFields(problemFieldFormPrefix, s.value.Form) {
			add(field.code, field.label, false)
		}
	}
	for _, field := range variableAuthFields(s.value.Auth) {
		add(field.code, field.label, false)
	}
	add(problemFieldPre, locales.PreRequestScript(), true)
	add(problemFieldPost, locales.PostResponseScript(), true)
	add(problemFieldDescription, locales.Description(), false)
	add("grpc-method", locales.GrpcMethod(), false)
	for i := range s.value.GRPC.Messages {
		add(fmt.Sprintf("grpc-message-%d", i+1), fmt.Sprintf("%s %d", locales.Message(), i+1), false)
	}
	return out
}

type namedField struct{ code, label string }

func variablePathFields(rows []request.Field) []namedField {
	var out []namedField
	for i := range rows {
		out = append(out,
			namedField{fmt.Sprintf("path-key-%d", i+1), fmt.Sprintf("%s %d · %s", locales.PathParameters(), i+1, locales.Name())},
			namedField{fmt.Sprintf("path-value-%d", i+1), fmt.Sprintf("%s %d · %s", locales.PathParameters(), i+1, locales.Value())},
		)
	}
	return out
}

func variableRowFields(prefix string, rows []request.Field) []namedField {
	var out []namedField
	for i := range rows {
		var name string
		switch prefix {
		case "header-":
			name = locales.Header()
		case problemFieldFormPrefix:
			name = locales.FormFields()
		default:
			name = prefix
		}
		out = append(out,
			namedField{fmt.Sprintf("%skey-%d", prefix, i+1), fmt.Sprintf("%s %d · %s", name, i+1, locales.Name())},
			namedField{fmt.Sprintf("%svalue-%d", prefix, i+1), fmt.Sprintf("%s %d · %s", name, i+1, locales.Value())},
		)
	}
	return out
}

func variableAuthFields(auth request.Auth) []namedField {
	var out []namedField
	for _, field := range authAuthPaths(auth) {
		out = append(out, namedField{
			code:  field.code,
			label: locales.Auth() + " · " + strings.ReplaceAll(strings.TrimPrefix(field.code, "auth:"), ".", " · "),
		})
	}
	return out
}

// scriptVariableRefs lists the pm.* variable names of one script through the
// one parser in variable_scripts.go that hovers and completions use. Only the
// name between the quotes is replaced by a rename.
func scriptVariableRefs(text string) []scriptVariableRef {
	var out []scriptVariableRef
	for at := 0; at < len(text); {
		found := strings.IndexAny(text[at:], "'\"`")
		if found < 0 {
			break
		}
		quote := at + found
		if ref, ok := scriptVariableAt(text, quote+1); ok && ref.Start < ref.End {
			out = append(out, ref)
			at = ref.End
			continue
		}
		at = quote + 1
	}
	return out
}

// scriptScope maps a pm.* store to the saved scope its name refers to. Local
// run variables have no definition; they resolve like an inline {{name}}.
func scriptScope(store, collection, environment string, fallback variableScope) (variableScope, bool) {
	switch store {
	case "environment":
		return variableScope{Kind: "Environment", Path: environment}, true
	case "globals":
		return variableScope{Kind: "Global"}, true
	case "collectionVariables":
		if path := firstPath(collection); path != "" {
			return variableScope{Kind: "Collection", Path: path}, true
		}
	case "variables":
		return fallback, fallback.valid()
	}
	return variableScope{}, false
}

func firstPath(collection string) string {
	path, _, _ := strings.Cut(strings.Trim(collection, "/"), "/")
	return path
}

// resolveScope uses the existing hint model, never a second scope chain.
func (a *app) variableScopeFor(collection, name string, sets map[string]variableSet) (variableScope, string) {
	set, ok := sets[collection]
	if !ok {
		set = a.variableHintSet(collection)
		sets[collection] = set
	}
	hint, defined := set.hint(name)
	if !defined {
		return variableScope{}, locales.UndefinedVariable()
	}
	return scopeFromString(hint.Scope), variableScopeName(hint.Scope)
}

// variableMatches collects every use of name with its resolved scope and
// every definition in every scope. Scoping uses request.VariableRefs and the
// app's hint set; script names use the pm.* store's scope.
func (a *app) variableMatches(name string) []variableMatch {
	var out []variableMatch
	sets := map[string]variableSet{}
	env := a.activeEnv
	add := func(field variableField, start, end int, kind string, scope variableScope, label string) {
		line, lineText := variableLine(field.text, start, end)
		out = append(out, variableMatch{
			Owner: field.owner, Key: field.key, Code: field.code,
			Resource: field.resource, Collection: field.collection, Field: field.field,
			Text: field.text, Start: start, End: end, Line: line, LineText: lineText,
			Scope: scope, ScopeLabel: label, Kind: kind,
		})
	}
	for _, source := range a.variableSources() {
		for _, field := range source.fields() {
			for _, ref := range request.VariableRefs(field.text) {
				if ref.Name != name {
					continue
				}
				scope, label := a.variableScopeFor(field.collection, name, sets)
				add(field, ref.Start, ref.End, "reference", scope, label)
			}
			if !field.script {
				continue
			}
			for _, lit := range scriptVariableRefs(field.text) {
				if lit.Name != name {
					continue
				}
				collectionScope, _ := a.variableScopeFor(field.collection, name, sets)
				scope, known := scriptScope(lit.Store, field.collection, env, collectionScope)
				if !known {
					continue
				}
				add(field, lit.Start, lit.End, "script", scope, scope.label())
			}
		}
	}
	for _, f := range a.folders {
		fields := variableFolderFields(f)
		for _, field := range fields {
			for _, ref := range request.VariableRefs(field.text) {
				if ref.Name != name {
					continue
				}
				scope, label := a.variableScopeFor(f.Path, name, sets)
				add(field, ref.Start, ref.End, "reference", scope, label)
			}
			if field.script {
				for _, lit := range scriptVariableRefs(field.text) {
					if lit.Name != name {
						continue
					}
					collectionScope, _ := a.variableScopeFor(f.Path, name, sets)
					scope, known := scriptScope(lit.Store, f.Path, env, collectionScope)
					if !known {
						continue
					}
					add(field, lit.Start, lit.End, "script", scope, scope.label())
				}
			}
		}
	}
	out = append(out, a.variableDefinitions(name)...)
	slices.SortStableFunc(out, func(x, y variableMatch) int {
		if c := strings.Compare(x.Resource, y.Resource); c != 0 {
			return c
		}
		if c := strings.Compare(x.Field, y.Field); c != 0 {
			return c
		}
		if c := x.Line - y.Line; c != 0 {
			return c
		}
		return x.Start - y.Start
	})
	return out
}

func variableFolderFields(f request.Folder) []variableField {
	var out []variableField
	add := func(code, label string, script bool) {
		ref, ok := variableFolderField(&f, code)
		if !ok || ref.text == nil {
			return
		}
		text := ref.current()
		if text != "" {
			out = append(out, variableField{
				owner: "folder", key: f.Path, code: code,
				resource: f.Path, collection: f.Path, field: label, script: script, text: text,
			})
		}
	}
	add(problemFieldDescription, locales.Description(), false)
	add(problemFieldPre, locales.PreRequestScript(), true)
	add(problemFieldPost, locales.PostResponseScript(), true)
	for _, field := range variableAuthFields(f.Auth) {
		add(field.code, field.label, false)
	}
	return out
}

// variableDefinitions lists the row/map definitions of name in every scope,
// independently of which one wins in a request.
func (a *app) variableDefinitions(name string) []variableMatch {
	var out []variableMatch
	def := func(scope variableScope, owner, key, code, field string, index int, mapName string) {
		label := field
		if mapName != "" {
			label = field + " · " + mapName
		}
		out = append(out, variableMatch{
			Owner: owner, Key: key, Code: code,
			Resource: scope.label(), Collection: scope.Path, Field: label,
			Text: name, Scope: scope, ScopeLabel: scope.label(), Kind: "definition", Index: index,
		})
	}
	for i := range a.envs {
		env := &a.envs[i]
		scope := variableScope{Kind: "Environment", Path: env.Name}
		if _, ok := env.Vars[name]; ok {
			def(scope, "env-initial", env.Name, "initial", locales.Definition(), 0, locales.Initial())
		}
		if _, ok := env.Current[name]; ok {
			def(scope, "env-current", env.Name, "current", locales.Definition(), 0, locales.Current())
		}
	}
	if _, ok := a.globals[name]; ok {
		def(variableScope{Kind: "Global"}, "global-initial", "", "initial", locales.Definition(), 0, locales.Initial())
	}
	if _, ok := a.globalsCurrent[name]; ok {
		def(variableScope{Kind: "Global"}, "global-current", "", "current", locales.Definition(), 0, locales.Current())
	}
	for i := range a.folders {
		f := &a.folders[i]
		for j, v := range f.Variables {
			if strings.TrimSpace(v.Name) == name {
				def(folderScope(f.Path), "folder-def", f.Path, "row", locales.Definition(), j, "")
			}
		}
	}
	return out
}

// variablePanelTarget turns a resolved Variables panel row into the scope its
// definition lives in. Rows with no saved definition still offer references.
func variablePanelTarget(row request.ResolvedVariable, environment string) variableTarget {
	target := variableTarget{Name: row.Name}
	switch {
	case row.Source == "Run input", row.Source == "Pre-request script":
		target.Scope = variableScope{Kind: "Environment", Path: environment}
	case row.Source == "Global":
		target.Scope = variableScope{Kind: "Global"}
	case strings.HasPrefix(row.Source, "Collection: "):
		target.Scope = folderScope(strings.TrimPrefix(row.Source, "Collection: "))
	default:
		return target
	}
	target.Defined = target.Scope.valid()
	return target
}

func varsEditorScope(a *app) variableScope {
	if a.varsGlobals {
		return variableScope{Kind: "Global"}
	}
	for _, name := range []string{a.varsChoice, a.varsName, a.activeEnv} {
		if name != "" {
			return variableScope{Kind: "Environment", Path: name}
		}
	}
	return variableScope{Kind: "Environment", Path: "default"}
}

func folderScope(path string) variableScope {
	if path == "" || strings.Contains(path, "/") {
		return variableScope{Kind: "Folder", Path: path}
	}
	return variableScope{Kind: "Collection", Path: path}
}

// renameChanges keeps only the definition and uses that resolve to one scope.
// The scope of each inline reference comes from the existing hint set; a
// nearer variable with the same name therefore stays out of this rename.
func renameChanges(matches []variableMatch, target variableTarget) []variableMatch {
	var out []variableMatch
	for _, m := range matches {
		if target.Scope.Kind == "Environment" && m.Kind == "definition" &&
			(m.Owner == "env-initial" || m.Owner == "env-current") {
			// Request uses resolve only in the active environment, but every
			// other environment holding this name would lose it the moment
			// the reader switched. Offer those definitions as separate,
			// checked changes so the reader can leave one alone.
			m.Checked = true
			out = append(out, m)
			continue
		}
		if m.Scope != target.Scope {
			continue
		}
		m.Checked = true
		out = append(out, m)
	}
	return out
}

func variableLine(text string, start, end int) (int, string) {
	if start < 0 || end > len(text) || start > end {
		return 0, ""
	}
	line := 1 + strings.Count(text[:start], "\n")
	from := strings.LastIndex(text[:start], "\n") + 1
	to := strings.IndexByte(text[end:], '\n')
	if to < 0 {
		to = len(text)
	} else {
		to += end
	}
	return line, text[from:to]
}

func containsSpace(s string) bool {
	return strings.IndexFunc(s, unicode.IsSpace) >= 0
}
