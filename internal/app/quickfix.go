package app

import (
	"cmp"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/markers"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// quickFixTarget is the marker's range in the authored field it came from.
// Ranges are byte offsets in text; native inputs count runes in ReplaceText,
// so only the edit path converts them. A code editor keeps its own undo
// history, and edits through its buffer stay undoable there.
type quickFixTarget struct {
	doc            *requestDocument
	field          string
	name           string
	text           *string
	state          *editor.State
	seen           *string
	bodyGeneration *uint64
	start, end     int
}

// quickFix is one code action. The label is what both the Problems panel's
// lightbulb and the editor's shortcut menu show.
type quickFix struct {
	label string
	apply func()
}

// quickFixScope is one place a missing variable can be created. Empty values
// are intentional: the name then exists for the producer to resolve, and the
// reader can type its value in the normal scope editor afterwards.
type quickFixScope struct {
	label string
	add   func(string) error
}

func (a *app) quickFixesForMarker(m markers.Marker) []quickFix {
	if m.Owner != problemOwnerVariables || m.Source == "" || m.Range == nil || m.Range.StartLineNumber != m.Range.EndLineNumber {
		return nil
	}
	d := a.documentForProblem(m.Resource)
	if d == nil {
		return nil
	}
	target, ok := a.quickFixTargetFromMarker(d, m)
	if !ok {
		return nil
	}
	return a.quickFixes(target)
}

func (a *app) quickFixTargetFromMarker(d *requestDocument, m markers.Marker) (quickFixTarget, bool) {
	_, text, state, seen, generation, ok := d.quickFixField(m.Code)
	if !ok || text == nil {
		return quickFixTarget{}, false
	}
	start, end, ok := quickFixByteRange(*text, m.Range)
	if !ok {
		return quickFixTarget{}, false
	}
	return quickFixTarget{doc: d, field: m.Code, name: m.Source, text: text,
		state: state, seen: seen, bodyGeneration: generation, start: start, end: end}, true
}

// quickFixTargetAtCaret makes a target from a caret inside one authored
// string. The same name twice in a field edits only the occurrence under the
// caret, just as a marker edits only its own range.
func (a *app) quickFixTargetAtCaret(d *requestDocument, field string, text *string, state *editor.State, seen *string, generation *uint64, caret int) (quickFixTarget, bool) {
	if d == nil || text == nil || caret < 0 || caret > len(*text) {
		return quickFixTarget{}, false
	}
	ref, ok := request.VariableAt(*text, caret)
	if !ok {
		return quickFixTarget{}, false
	}
	return quickFixTarget{doc: d, field: field, name: ref.Name, text: text,
		state: state, seen: seen, bodyGeneration: generation, start: ref.Start, end: ref.End}, true
}

// quickFixByteRange turns a marker's 1-based byte columns into byte offsets
// in the field text. A reference stays on one line, so a multi-line range is
// not one this fix can rewrite.
func quickFixByteRange(text string, r *markers.Range) (int, int, bool) {
	if r == nil || r.StartLineNumber < 1 || r.StartLineNumber != r.EndLineNumber || r.StartColumn < 1 || r.EndColumn < r.StartColumn {
		return 0, 0, false
	}
	line := 1
	lineStart := 0
	for offset := 0; offset < len(text) && line < r.StartLineNumber; offset++ {
		if text[offset] == '\n' {
			line++
			lineStart = offset + 1
		}
	}
	if line != r.StartLineNumber {
		return 0, 0, false
	}
	start, end := lineStart+r.StartColumn-1, lineStart+r.EndColumn-1
	if start < 0 || end > len(text) || start > end {
		return 0, 0, false
	}
	return start, end, true
}

// quickFixField is the authored field named by a marker's code. Editors keep
// their own buffer and undo history; for them the target also carries the
// string the producer reads and the generation that tells the producer to
// rescan a body written outside its editor.
func (d *requestDocument) quickFixField(code string) (field string, text *string, state *editor.State, seen *string, generation *uint64, ok bool) {
	switch code {
	case searchFieldName:
		return code, &d.composeName, nil, nil, nil, true
	case problemFieldURL:
		return code, &d.composeURL, nil, nil, nil, true
	case problemFieldBody:
		return code, &d.composeBody, d.bodyEditor, &d.bodyFor, &d.problemBodyGen, true
	case problemFieldPre:
		return code, &d.composePre, d.preEditor, &d.preFor, nil, true
	case problemFieldPost:
		return code, &d.composePost, d.postEditor, &d.postFor, nil, true
	case problemFieldHeaderRaw:
		return code, &d.composeHeaders, d.bulkEditor, &d.bulkFor, nil, true
	}
	if strings.HasPrefix(code, "header-") {
		part, at, ok := rowFieldCode(code, "header-")
		if !ok || at < 1 || at > len(d.headerRows) {
			return "", nil, nil, nil, nil, false
		}
		if part == "key" {
			return code, &d.headerRows[at-1].Name, nil, nil, nil, true
		}
		return code, &d.headerRows[at-1].Value, nil, nil, nil, true
	}
	if part, at, ok := rowFieldCode(code, problemFieldFormPrefix); ok {
		if at < 1 || at > len(d.formFields) {
			return "", nil, nil, nil, nil, false
		}
		if part == "key" {
			return code, &d.formFields[at-1].Name, nil, nil, nil, true
		}
		return code, &d.formFields[at-1].Value, nil, nil, nil, true
	}
	if part, at, ok := rowFieldCode(code, "path-"); ok {
		if at < 1 || at > len(d.pathRows) {
			return "", nil, nil, nil, nil, false
		}
		if part == "key" {
			return code, &d.pathRows[at-1].Name, nil, nil, nil, true
		}
		return code, &d.pathRows[at-1].Value, nil, nil, nil, true
	}
	switch code {
	case problemFieldDescription:
		return code, &d.description, nil, nil, nil, true
	case "graphql-query":
		return code, &d.graphql.Query, nil, nil, nil, true
	case "graphql-vars":
		return code, &d.graphql.Variables, nil, nil, nil, true
	case "body-file":
		return code, &d.bodyFile, nil, nil, nil, true
	case "grpc-method":
		return code, &d.grpcConfig.Method, nil, nil, nil, true
	}
	if at, ok := strings.CutPrefix(code, "grpc-message-"); ok {
		index, err := strconv.Atoi(at)
		if err == nil && index >= 1 && index <= len(d.grpcConfig.Messages) {
			return code, &d.grpcConfig.Messages[index-1], nil, nil, nil, true
		}
	}
	if path, ok := strings.CutPrefix(code, problemFieldAuthPrefix); ok {
		if value := authFieldPointer(&d.auth, "Auth."+path); value != nil {
			return code, value, nil, nil, nil, true
		}
	}
	return "", nil, nil, nil, nil, false
}

func rowFieldCode(code, prefix string) (part string, row int, ok bool) {
	rest, found := strings.CutPrefix(code, prefix)
	if !found {
		return "", 0, false
	}
	part, number, found := strings.Cut(rest, "-")
	if !found {
		return "", 0, false
	}
	at, err := strconv.Atoi(number)
	if err != nil {
		return "", 0, false
	}
	return part, at, true
}

// authFieldPointer walks the same Auth tree the field editor and the send
// warning walk. It returns the address of one authored string, so a fix
// writes the field the producer reads rather than a copy of it.
func authFieldPointer(auth *request.Auth, path string) *string {
	var find func(reflect.Value, string) *string
	find = func(value reflect.Value, prefix string) *string {
		for value.IsValid() && value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return nil
			}
			value = value.Elem()
		}
		if !value.IsValid() || value.Kind() != reflect.Struct {
			return nil
		}
		for i := range value.NumField() {
			field := value.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			member := value.Field(i)
			name := prefix + "." + field.Name
			for member.Kind() == reflect.Pointer {
				if member.IsNil() {
					break
				}
				member = member.Elem()
			}
			if name == path && member.Kind() == reflect.String && member.CanAddr() {
				return member.Addr().Interface().(*string)
			}
			if member.Kind() == reflect.Struct && member.Type().PkgPath() != "time" {
				if value := find(member, name); value != nil {
					return value
				}
			}
		}
		return nil
	}
	return find(reflect.ValueOf(auth), "Auth")
}

// authAuthPaths lists non-empty auth strings with the field path the marker
// uses as its code. One label can name several schemes' fields (Username),
// so the path, not the label, identifies the exact field.
func authAuthPaths(auth request.Auth) []struct{ code, text string } {
	var out []struct{ code, text string }
	var walk func(reflect.Value, string)
	walk = func(value reflect.Value, prefix string) {
		for value.IsValid() && value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return
			}
			value = value.Elem()
		}
		if !value.IsValid() || value.Kind() != reflect.Struct {
			return
		}
		for i := range value.NumField() {
			field := value.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			if prefix == "Auth" && (field.Name == "Type" || field.Name == "Location") {
				continue
			}
			path := prefix + "." + field.Name
			member := value.Field(i)
			for member.Kind() == reflect.Pointer {
				if member.IsNil() {
					break
				}
				member = member.Elem()
			}
			switch member.Kind() {
			case reflect.String:
				if member.String() != "" {
					out = append(out, struct{ code, text string }{problemFieldAuthPrefix + fieldPath(path), member.String()})
				}
			case reflect.Struct:
				if member.Type().PkgPath() != "time" {
					walk(member, path)
				}
			}
		}
	}
	walk(reflect.ValueOf(auth), "Auth")
	return out
}

// fieldPath drops the Auth root, leaving the marker code's field path.
func fieldPath(path string) string {
	return strings.TrimPrefix(path, "Auth.")
}

// variableFixProposal is one action for an unresolved name, without the
// field it would edit. Hover, the Problems panel and the editor's shortcut
// all offer the same proposals, in the same order.
type variableFixProposal struct {
	label string
	add   func(string) error // set for "create in scope", nil for a rename
	to    string             // set for a rename
}

// quickFixes offers creation in the nearby scopes and renames to names that
// are close to what the author typed. Creation comes first: VS Code asks to
// add the unknown symbol before guessing a different one.
func (a *app) quickFixes(target quickFixTarget) []quickFix {
	if !target.ok() {
		return nil
	}
	collection := ""
	if target.doc != nil {
		collection = target.doc.composeCollection
	}
	set := a.variableHintSet(collection)
	var fixes []quickFix
	for _, proposal := range a.variableFixProposals(set, collection, target.name) {
		proposal := proposal
		switch {
		case proposal.add != nil:
			name, apply := target.name, proposal.add
			fixes = append(fixes, quickFix{
				label: proposal.label,
				apply: func() {
					if err := apply(name); err != nil {
						a.fail(locales.Variables(), err)
						return
					}
					a.invalidate()
				},
			})
		default:
			to, at, end := proposal.to, target.start, target.end
			fixes = append(fixes, quickFix{
				label: proposal.label,
				apply: func() {
					if !target.rename(at, end, to) {
						// The field changed while the action list was open.
						// Dropping the menu is safer than editing new text at
						// an offset from the old field.
						a.problems.fixMenuOpen = false
					}
					a.invalidate()
				},
			})
		}
	}
	return fixes
}

// variableFixProposals lists the fixes for one name against one resolution.
// The hover shows their labels; the code-action paths apply them to the
// exact reference they were opened on.
func (a *app) variableFixProposals(set variableSet, collection, name string) []variableFixProposal {
	if _, defined := set.hint(name); defined {
		return nil
	}
	var out []variableFixProposal
	for _, scope := range a.quickFixScopes(collection) {
		out = append(out, variableFixProposal{
			label: locales.CreateVariableIn(name, scope.label),
			add:   scope.add,
		})
	}
	for _, hint := range similarVariableNames(name, set.list) {
		out = append(out, variableFixProposal{label: locales.ChangeToVariable(hint.Name), to: hint.Name})
	}
	return out
}

func (t quickFixTarget) ok() bool {
	return t.text != nil && t.doc != nil && t.name != "" &&
		t.start >= 0 && t.end >= t.start && t.end <= len(*t.text) &&
		!strings.ContainsAny((*t.text)[t.start:t.end], "\r\n")
}

// stillMatches checks that the captured range still holds the reference the
// action was offered for. The field can have been typed in, undone or rewritten
// while the menu was open; reusing the old offsets would edit the wrong text.
// VariableRefs also accepts spaces inside the braces, as the producer does.
func (t quickFixTarget) stillMatches() bool {
	if !t.ok() || t.state != nil && t.state.Buffer.String() != *t.text {
		return false
	}
	if (*t.text)[t.start:t.end] == "{{"+t.name+"}}" {
		return true
	}
	for _, ref := range request.VariableRefs(*t.text) {
		if ref.Start == t.start && ref.End == t.end && ref.Name == t.name {
			return true
		}
	}
	return false
}

// rename applies the code action through the field's own edit path. A code
// editor edits its buffer, so undo and redo know the step; plain strings are
// written where the producer reads them. Only the marker's byte range moves.
// It reports false when the range no longer holds the same reference.
func (t quickFixTarget) rename(start, end int, name string) bool {
	if !t.stillMatches() || start != t.start || end != t.end {
		return false
	}
	replacement := "{{" + name + "}}"
	if t.state != nil && t.state.Buffer.String() == *t.text && end <= t.state.Buffer.Len() {
		t.state.Buffer.Edit(editor.Edit{Start: start, End: end, Text: replacement})
		*t.text = t.state.Buffer.String()
		if t.seen != nil {
			*t.seen = *t.text
		}
	} else {
		*t.text = (*t.text)[:start] + replacement + (*t.text)[end:]
		if t.seen != nil {
			*t.seen = "" // the next frame builds the reader from the new text
		}
	}
	if t.bodyGeneration != nil {
		*t.bodyGeneration++
	}
	if t.doc != nil {
		switch {
		case t.field == problemFieldHeaderRaw:
			t.doc.headerFor = ""
		case strings.HasPrefix(t.field, "header-"):
			t.doc.composeHeaders = headerRowsText(t.doc.headerRows)
			t.doc.headerFor = t.doc.composeHeaders
		case t.field == problemFieldURL:
			t.doc.querySource = ""
		}
	}
	return true
}

// similarVariableNames ranks existing names by how close they are to the
// variable the author typed. Damerau-Levenshtein counts a swapped pair as one
// edit, as a typo does. Ranking by distance before scope keeps the nearest
// spelling first; ties fall back to the closer scope and then the name.
func similarVariableNames(name string, hints []variableHint) []variableHint {
	query := []rune(strings.ToLower(strings.TrimSpace(name)))
	if len(query) == 0 {
		return nil
	}
	type ranked struct {
		hint     variableHint
		distance int
	}
	var rankedNames []ranked
	for _, hint := range hints {
		if hint.Dynamic || hint.Vault || strings.EqualFold(hint.Name, name) {
			continue
		}
		candidate := []rune(strings.ToLower(hint.Name))
		distance := damerauLevenshtein(query, candidate)
		// A name can be longer than the typed one: tenant is nearer than
		// tenant_id, but tenant_id is still a useful guess. Unrelated names
		// are many edits away and get no suggestion at all.
		if distance > max(2, max(len(query), len(candidate))/2) {
			continue
		}
		rankedNames = append(rankedNames, ranked{hint, distance})
	}
	slices.SortStableFunc(rankedNames, func(left, right ranked) int {
		return cmp.Or(cmp.Compare(left.distance, right.distance),
			variableScopeRank(left.hint)-variableScopeRank(right.hint),
			strings.Compare(left.hint.Name, right.hint.Name))
	})
	out := make([]variableHint, 0, min(len(rankedNames), 3))
	for _, item := range rankedNames[:min(len(rankedNames), 3)] {
		out = append(out, item.hint)
	}
	return out
}

// damerauLevenshtein is the unrestricted edit distance with adjacent
// transpositions, computed on runes. Variable names are short, so the two
// rows are cheap and the extra row keeps `tenat` next to `tenant`.
func damerauLevenshtein(left, right []rune) int {
	if len(left) == 0 {
		return len(right)
	}
	if len(right) == 0 {
		return len(left)
	}
	rows := make([][]int, len(left)+2)
	for i := range rows {
		rows[i] = make([]int, len(right)+2)
		rows[i][0] = i
	}
	for j := range right {
		rows[0][j+1] = j
	}
	maxDistance := len(left) + len(right)
	for i := 1; i <= len(left); i++ {
		rows[i][1] = i
		for j := 1; j <= len(right); j++ {
			cost := 1
			if left[i-1] == right[j-1] {
				cost = 0
			}
			rows[i][j] = min(rows[i-1][j]+1, rows[i][j-1]+1, rows[i-1][j-1]+cost)
			if i > 1 && j > 1 && left[i-1] == right[j-2] && left[i-2] == right[j-1] {
				rows[i][j] = min(rows[i][j], rows[i-2][j-2]+1)
			}
		}
	}
	return min(rows[len(left)][len(right)], maxDistance)
}

// quickFixScopes lists the places a missing name can be created, nearest
// first: the environment in use, the request's folder, its collection, then
// globals. Each scope writes through the same store its editor saves to.
// With no environment in use there is no environment scope: inventing and
// activating "default" would change what every other request resolves against.
func (a *app) quickFixScopes(collection string) []quickFixScope {
	var scopes []quickFixScope
	if a.envsProblem == "" && a.activeEnv != "" {
		name := a.activeEnv
		scopes = append(scopes, quickFixScope{
			label: locales.KindPath(locales.Environment(), name),
			add: func(variable string) error {
				return a.addQuickFixEnvVariable(name, variable)
			},
		})
	}
	paths := variablePathChain(collection)
	slices.Reverse(paths) // the deepest folder comes before its collection
	for _, path := range paths {
		kind := locales.Collection()
		if strings.Contains(path, "/") {
			kind = locales.FolderName()
		}
		path, label := path, locales.KindPath(kind, path)
		scopes = append(scopes, quickFixScope{
			label: label,
			add: func(variable string) error {
				return a.addQuickFixFolderVariable(path, variable)
			},
		})
	}
	if a.envsProblem == "" {
		scopes = append(scopes, quickFixScope{
			label: locales.ScopeGlobal(),
			add: func(variable string) error {
				globals := maps.Clone(a.globals)
				if globals == nil {
					globals = map[string]string{}
				}
				globals[variable] = ""
				f := a.localEnvs()
				f.Globals = globals
				if err := a.saveEnvFile(f); err != nil {
					return err
				}
				a.globals = globals
				return nil
			},
		})
	}
	return scopes
}

func (a *app) addQuickFixEnvVariable(envName, name string) error {
	envs := slices.Clone(a.envs)
	at := slices.IndexFunc(envs, func(e environment) bool { return e.Name == envName })
	if at < 0 {
		envs = append(envs, environment{Name: envName})
		at = len(envs) - 1
	}
	vars := maps.Clone(envs[at].Vars)
	if vars == nil {
		vars = map[string]string{}
	}
	vars[name] = ""
	envs[at].Vars = vars
	f := a.localEnvs()
	f.Envs, f.Active = envs, envName
	if err := a.saveEnvFile(f); err != nil {
		return err
	}
	a.envs, a.activeEnv = envs, envName
	return nil
}

func (a *app) addQuickFixFolderVariable(path, name string) error {
	folders := slices.Clone(a.folders)
	at := slices.IndexFunc(folders, func(f request.Folder) bool { return f.Path == path })
	if at < 0 {
		return fmt.Errorf("%s: %s", locales.Collection(), path)
	}
	variables := slices.Clone(folders[at].Variables)
	if slices.ContainsFunc(variables, func(v request.Variable) bool { return v.Name == name }) {
		return nil
	}
	variables = append(variables, request.Variable{Name: name})
	folders[at].Variables = variables
	return a.writeFolders(folders)
}

// quickFixContextMenu is the editor's Cmd/Ctrl+. action list. It hangs off
// the focused input or code editor, so the menu opens beside the field the
// caret is in. The Problems panel offers the same fixes from its lightbulb.
func (a *app) quickFixContextMenu(c *ui.Context, anchor ui.Element, owner any) {
	target := a.problems.caret
	if !a.problems.fixMenuOpen || target == nil || !anchor.Valid() {
		return
	}
	// Only the field holding the caret owns the open menu. The menu takes
	// the focus away from that field as it opens, so focus alone cannot
	// identify it on the following pass.
	switch field := owner.(type) {
	case *string:
		if target.text != field {
			return
		}
	case *editor.State:
		if target.state != field {
			return
		}
	}
	fixes := a.problems.caretFixes
	if len(fixes) == 0 {
		return
	}
	t := c.Theme()
	menu := ui.ContextMenuBase(c, anchor, &a.problems.fixMenuOpen)
	menu.Popup(func(panel ui.Element) {
		uikit.PopoverPanel(c, panel).Width(360)
		for i := range fixes {
			item := menu.Item().Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceM)).Radius(uikit.RadiusControl)
			if item.Highlighted() {
				item.Background(uikit.Colors(t).Primary).TextColor(uikit.Colors(t).PrimaryForeground)
			}
			item.Children(func() { uikit.Body(c, fixes[i].label).SingleLine() })
			if item.Clicked() {
				fix := fixes[i]
				a.problems.fixMenuOpen = false
				fix.apply()
			}
		}
	})
}

// variableHandle gives an authoring field a stable identity for the
// focus-scoped shortcut, keyed by the string it edits.
func (a *app) variableHandle(value *string) *ui.Handle {
	if a.variableHandles == nil {
		a.variableHandles = make(map[*string]*ui.Handle)
	}
	if a.variableHandles[value] == nil {
		a.variableHandles[value] = new(ui.Handle)
	}
	return a.variableHandles[value]
}

// bindQuickFixShortcut binds Cmd/Ctrl+. inside one field, so Start/Stop
// capture keeps the global key everywhere else.
func (a *app) bindQuickFixShortcut(c *ui.Context, handle *ui.Handle) {
	if handle == nil || c == nil {
		return
	}
	handle.OnShortcut(c, ui.Cmd, ui.KeyPeriod, func() { a.openQuickFixMenuFromCaret() })
}

// trackInputQuickFix remembers the variable under a native input's caret.
// The selection is read on the built element, as the field's completion does.
func (a *app) trackInputQuickFix(d *requestDocument, code string, value *string, in ui.Element) {
	if d == nil || !in.Focused() {
		return
	}
	_, caretRunes := in.TextSelection()
	target, ok := a.quickFixTargetAtCaret(d, code, value, nil, nil, nil, runeToByte(*value, caretRunes))
	if !ok {
		a.clearVariableCaret(value)
		return
	}
	a.setVariableCaret(target)
}

func (a *app) clearVariableCaret(value *string) {
	p := &a.problems
	if p.variableCaret != nil && p.variableCaret.text == value {
		p.variableCaret = nil
		p.caret, p.caretFixes = nil, nil
	}
}

// setVariableCaret feeds both the quick fix and the rename/reference commands
// from the same caret target; a defined variable simply has no quick fix.
func (a *app) setVariableCaret(target quickFixTarget) {
	copied := target
	a.problems.variableCaret = &copied
	a.problems.caret, a.problems.caretFixes = &copied, a.quickFixes(copied)
}

func (a *app) quickFixHandleMenu(c *ui.Context, handle *ui.Handle, state *editor.State) {
	if c != nil && handle != nil {
		a.quickFixContextMenu(c, c.Resolve(*handle), state)
	}
}

// trackEditorQuickFix remembers the reference at a code editor's caret. The
// editor's own cursor is a byte offset, the same unit its buffer edits use.
func (a *app) trackEditorQuickFix(d *requestDocument, code string, state *editor.State) {
	if d == nil || state == nil || !state.Focused() {
		return
	}
	_, text, _, seen, generation, ok := d.quickFixField(code)
	if !ok || state.Buffer.String() != *text {
		return
	}
	target, ok := a.quickFixTargetAtCaret(d, code, text, state, seen, generation, state.Cursor())
	if !ok {
		a.clearVariableCaret(text)
		return
	}
	a.setVariableCaret(target)
}

// quickFixFieldCode maps a field's completion key to the code its marker
// carries, so the shortcut and the Problems panel can share the fix target.
func quickFixFieldCode(key string) string {
	switch {
	case key == "URL":
		return problemFieldURL
	case strings.HasPrefix(key, "header-"):
		return key
	case strings.HasPrefix(key, "field-"):
		return problemFieldFormPrefix + strings.TrimPrefix(key, "field-")
	default:
		return key
	}
}

// openQuickFixMenuFromCaret opens the editor's action list with the fixes
// computed for the frame's focused field.
func (a *app) openQuickFixMenuFromCaret() {
	if a.problems.caret == nil {
		return
	}
	target := *a.problems.caret
	a.openQuickFixMenu(target)
}

// openQuickFixMenu is called by the scoped Cmd/Ctrl+. handler of the field
// holding the caret. Nothing opens when the caret is off a variable or the
// variable already resolves.
func (a *app) openQuickFixMenu(target quickFixTarget) {
	fixes := a.quickFixes(target)
	if len(fixes) == 0 {
		return
	}
	copied := target
	a.problems.caret, a.problems.caretFixes, a.problems.fixMenuOpen = &copied, fixes, true
	a.invalidate()
}
