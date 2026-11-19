package app

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// variableRenameState is the two-step VS Code rename: ask for the name, then
// review every change before a byte of the library is written.
type variableRenameState struct {
	open    bool
	preview bool
	target  variableTarget
	input   string
	problem string
	warning string
	changes []variableMatch
	current int
}

// variableReferencesState is the References view of one variable name.
type variableReferencesState struct {
	open    bool
	target  variableTarget
	matches []variableMatch
	current int
	list    ui.ListState
	reveal  variableReveal
}

// variableReveal asks a field built on a later frame to show the match. It is
// one pending location, as the Problems panel's pending focus is.
type variableReveal struct {
	owner, key, code string
	name             string
	start, end       int
	line             int
}

func (a *app) openVariableRename(target variableTarget) {
	a.variableRefs.open = false
	s := &a.variableRename
	s.open, s.preview = true, false
	s.target = target
	s.input, s.problem, s.warning = target.Name, "", ""
	s.changes, s.current = nil, 0
	a.invalidate()
}

func (a *app) openVariableReferences(target variableTarget) {
	a.variableRename.open = false
	s := &a.variableRefs
	s.open, s.target = true, target
	s.matches, s.current = a.variableMatches(target.Name), 0
	s.reveal = variableReveal{}
	s.list.Selected = &s.current
	s.list.Key = func(i int) any { return s.matches[i].key() }
	a.invalidate()
}

func (m variableMatch) key() string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", m.Owner, m.Key, m.Code, m.Line, m.Start)
}

// prepareVariableRename validates the new name and builds the preview. The
// preview keeps one checkbox per change; nothing is written until Apply.
func (a *app) prepareVariableRename() {
	s := &a.variableRename
	s.problem, s.warning = "", ""
	name := strings.TrimSpace(s.input)
	switch {
	case name == "":
		s.problem = locales.VariableNameCannotBeEmpty()
	case strings.ContainsAny(name, "{}") || containsSpace(name):
		s.problem = locales.VariableNameRules()
	}
	if s.problem != "" {
		return
	}
	changes := renameChanges(a.variableMatches(s.target.Name), s.target)
	if len(changes) == 0 {
		s.problem = locales.NoVariableUsesInScope()
		return
	}
	if name == s.target.Name {
		s.problem = locales.VariableNameUnchanged()
		return
	}
	s.changes, s.current, s.preview = changes, 0, true
	if a.variableCollision(s.target, name) {
		s.warning = locales.VariableNameAlreadyInScope(name, s.target.Scope.label())
	}
	a.invalidate()
}

// variableCollision warns about another definition of the new name in the
// chosen scope; renaming onto one would merge two different variables.
func (a *app) variableCollision(target variableTarget, name string) bool {
	if name == target.Name {
		return false
	}
	env := func(vars map[string]string) bool { _, ok := vars[name]; return ok }
	switch target.Scope.Kind {
	case "Environment":
		for i := range a.envs {
			if a.envs[i].Name == target.Scope.Path {
				return env(a.envs[i].Vars) || env(a.envs[i].Current)
			}
		}
	case "Global":
		return env(a.globals) || env(a.globalsCurrent)
	case "Collection", "Folder":
		for i := range a.folders {
			if a.folders[i].Path != target.Scope.Path {
				continue
			}
			for _, v := range a.folders[i].Variables {
				if strings.TrimSpace(v.Name) == name {
					return true
				}
			}
		}
	}
	return false
}

// variableStateCopy holds the library and scope state a rename would change.
// Nothing is adopted until writeLibrary succeeds, so a refusal leaves memory,
// disk and open drafts agreeing with each other.
type variableStateCopy struct {
	requests          []savedRequest
	folders           []request.Folder
	envs              []environment
	globals           map[string]string
	globalsCurrent    map[string]string
	folderCurrent     map[string]map[string]string
	folderCredentials map[string]map[string]bool
}

// copyVariableState deep-copies the library assets through their own JSON
// format. Maps inside environments are copied directly.
func copyVariableState(a *app) (variableStateCopy, error) {
	requests, err := cloneLibraryValue(a.requests)
	if err != nil {
		return variableStateCopy{}, err
	}
	folders, err := cloneLibraryValue(a.folders)
	if err != nil {
		return variableStateCopy{}, err
	}
	s := variableStateCopy{
		requests: requests, folders: folders,
		envs:    slices.Clone(a.envs),
		globals: maps.Clone(a.globals), globalsCurrent: maps.Clone(a.globalsCurrent),
		folderCurrent: map[string]map[string]string{}, folderCredentials: map[string]map[string]bool{},
	}
	for i := range s.envs {
		s.envs[i].Vars = maps.Clone(s.envs[i].Vars)
		s.envs[i].Current = maps.Clone(s.envs[i].Current)
		s.envs[i].Credentials = maps.Clone(s.envs[i].Credentials)
	}
	for path, values := range a.folderCurrent {
		s.folderCurrent[path] = maps.Clone(values)
	}
	for path, marks := range a.folderCredentials {
		s.folderCredentials[path] = maps.Clone(marks)
	}
	return s, nil
}

func cloneLibraryValue[T any](value T) (T, error) {
	var out T
	data, err := json.Marshal(value)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(data, &out)
	return out, err
}

type plannedUse struct {
	match       variableMatch
	replacement string
}

type plannedField struct {
	owner, key, code string
	collection       string
	oldText, newText string
	uses             []plannedUse
	doc              *requestDocument
}

func planKey(m variableMatch) string {
	return m.Owner + "\x00" + m.Key + "\x00" + m.Code
}

// applyVariableRename plans every checked change first, writes the library
// from copies, and only then updates live definitions, documents and editor
// buffers. A refused write leaves the whole app as it was and hands the draft
// bytes to the ordinary save-conflict view.
func (a *app) applyVariableRename() bool {
	s := &a.variableRename
	if s.warning != "" {
		a.warn(s.warning)
		return false
	}
	to := strings.TrimSpace(s.input)
	checked := slices.DeleteFunc(slices.Clone(s.changes), func(m variableMatch) bool { return !m.Checked })
	if len(checked) == 0 {
		a.warn(locales.NoVariableChangesSelected())
		return false
	}
	state, err := copyVariableState(a)
	if err != nil {
		a.fail(locales.RenameVariable(), err)
		return false
	}

	fields, byField := []*plannedField{}, map[string]*plannedField{}
	definitions := []variableMatch{}
	for _, m := range checked {
		if m.Kind == "definition" {
			definitions = append(definitions, m)
			continue
		}
		ref, doc, ok := a.variableResolveField(m)
		if !ok || ref.state != nil && ref.state.Buffer.String() != *ref.text {
			continue // the editor owns a newer buffer than the preview saw
		}
		text := ref.current()
		if m.Start < 0 || m.End > len(text) || m.Start > m.End ||
			m.Text[m.Start:m.End] != text[m.Start:m.End] {
			continue // the field changed since the preview
		}
		replacement := to
		if m.Kind == "reference" {
			replacement = "{{" + to + "}}"
		}
		key := planKey(m)
		plan := byField[key]
		if plan == nil {
			plan = &plannedField{owner: m.Owner, key: m.Key, code: m.Code, collection: m.Collection,
				oldText: text, newText: text, doc: doc}
			byField[key], fields = plan, append(fields, plan)
		}
		if text != plan.oldText {
			continue
		}
		plan.uses = append(plan.uses, plannedUse{match: m, replacement: replacement})
	}
	for _, plan := range fields {
		// Later offsets first so earlier ones keep their positions.
		slices.SortStableFunc(plan.uses, func(x, y plannedUse) int { return y.match.Start - x.match.Start })
		text := plan.oldText
		for _, use := range plan.uses {
			m := use.match
			if m.End > len(text) || text[m.Start:m.End] != m.Text[m.Start:m.End] {
				continue // overlapping change: never edit a range that moved
			}
			text = text[:m.Start] + use.replacement + text[m.End:]
		}
		plan.newText = text
	}

	libraryChanged := false
	for _, plan := range fields {
		switch plan.owner {
		case "saved":
			if setCopiedRequestField(&state, plan) {
				libraryChanged = true
			}
		case "folder":
			if setCopiedFolderField(&state, plan) {
				libraryChanged = true
			}
		}
	}
	definitionsChanged := false
	for _, m := range definitions {
		if state.renameDefinition(m, to) {
			definitionsChanged = true
			if m.Owner == "folder-def" {
				libraryChanged = true
			}
		}
	}
	// An open tab may be dirty with edits the reader never saved. Rename the
	// saved copy for the file, not the draft: the reader's own changes stay
	// only in the tab, and its baseline becomes the renamed saved request.
	baselines := map[*requestDocument]savedRequest{}
	sets := map[string]variableSet{}
	target := a.variableRename.target
	for _, plan := range fields {
		d := plan.doc
		if plan.owner != "document" || d == nil || d.savedKey == "" || d.assetID == "" {
			continue
		}
		saved := savedRequestForDoc(state.requests, d)
		if saved == nil {
			continue
		}
		code := savedFieldCodeForDocument(plan.code, saved)
		ref, ok := variableSavedField(saved, code)
		if !ok || ref.text == nil {
			continue
		}
		renamed, ok := a.renameInSavedText(*ref.text, plan, saved.Collection, target, sets)
		if !ok || !setSavedField(saved, code, renamed) {
			continue
		}
		baselines[d] = documentBaseline(a, *saved, d.savedKey)
		libraryChanged = true
	}

	if libraryChanged {
		// writeLibrary guards the files and keeps the conflict's draft bytes.
		if err := a.writeLibrary(state.requests, state.folders); err != nil {
			a.fail(locales.RenameVariable(), err)
			return false // no live state moved
		}
		a.requests, a.folders = state.requests, state.folders
	}
	if definitionsChanged {
		a.envs, a.globals, a.globalsCurrent = state.envs, state.globals, state.globalsCurrent
		a.folderCurrent, a.folderCredentials = state.folderCurrent, state.folderCredentials
	}

	// Only now, after the files are safe, touch the drafts and their buffers.
	for _, plan := range fields {
		if plan.owner != "document" || plan.doc == nil {
			continue
		}
		for _, use := range plan.uses {
			ref, _, ok := a.variableResolveField(use.match)
			if !ok || !replaceVariableText(ref, use.match, use.replacement) {
				continue
			}
			a.syncVariableField(plan.doc, ref, plan.code)
		}
		if baseline, ok := baselines[plan.doc]; ok {
			plan.doc.baseline = baseline
		}
	}
	if definitionsChanged {
		if err := a.saveLocalEnvs(); err != nil {
			a.fail(locales.Variables(), err)
		}
	}
	s.open, s.preview = false, false
	a.done(locales.RenamedVariable(s.target.Name, to))
	a.invalidate()
	return true
}

// savedFieldCodeForDocument maps an open draft's header-row code onto the
// header block a saved request carries when it has no stored rows.
func savedFieldCodeForDocument(code string, saved *savedRequest) string {
	if strings.HasPrefix(code, "header-") && code != problemFieldHeaderRaw && len(saved.HeaderList) == 0 {
		return problemFieldHeaderRaw
	}
	return code
}

// documentBaseline is the saved request in the form a document compares
// against, including the fields the editor derives when it opens a file.
func documentBaseline(a *app, saved savedRequest, key string) savedRequest {
	var d requestDocument
	d.load(a, saved, key)
	return d.value()
}

func savedRequestForDoc(requests []savedRequest, d *requestDocument) *savedRequest {
	for i := range requests {
		if requests[i].key() == d.savedKey && requests[i].ID == d.assetID {
			return &requests[i]
		}
	}
	return nil
}

// parsedVarUse is one reference in a field, found by the same parsers the
// preview uses. Offsets are in that field's text.
type parsedVarUse struct {
	Start, End int
}

// parsedUsesForScope lists the parsed refs of one name that resolve to the
// renamed scope. Occurrences in comments, longer names and shadowed scopes
// never count, so a draft's prose cannot shift which saved ref is renamed.
func (a *app) parsedUsesForScope(text, kind, collection string, target variableTarget, sets map[string]variableSet) []parsedVarUse {
	var out []parsedVarUse
	switch kind {
	case "reference":
		scope, _ := a.variableScopeFor(collection, target.Name, sets)
		if scope != target.Scope {
			return out
		}
		for _, ref := range request.VariableRefs(text) {
			if ref.Name == target.Name {
				out = append(out, parsedVarUse{Start: ref.Start, End: ref.End})
			}
		}
	case "script":
		fallback, _ := a.variableScopeFor(collection, target.Name, sets)
		for _, ref := range scriptVariableRefs(text) {
			if ref.Name != target.Name {
				continue
			}
			scope, ok := scriptScope(ref.Store, collection, a.activeEnv, fallback)
			if ok && scope == target.Scope {
				out = append(out, parsedVarUse{Start: ref.Start, End: ref.End})
			}
		}
	}
	return out
}

// renameInSavedText transfers checked uses from a draft to the saved field.
// Each draft use is matched to the saved ref at the same ordinal among parsed
// refs of its name and kind. If the two fields disagree on that count, the
// ordinals cannot be trusted and every saved ref resolving to the scope is
// renamed, leaving the saved copy consistent on its own.
func (a *app) renameInSavedText(savedText string, plan *plannedField, collection string, target variableTarget, sets map[string]variableSet) (string, bool) {
	type savedEdit struct {
		start, end  int
		replacement string
	}
	var edits []savedEdit
	to := strings.TrimSpace(a.variableRename.input)
	for _, kind := range []string{"reference", "script"} {
		draftRefs := a.parsedUsesForScope(plan.oldText, kind, plan.collection, target, sets)
		savedRefs := a.parsedUsesForScope(savedText, kind, collection, target, sets)
		if len(draftRefs) != len(savedRefs) {
			replacement := to
			if kind == "reference" {
				replacement = "{{" + to + "}}"
			}
			for _, ref := range savedRefs {
				edits = append(edits, savedEdit{ref.Start, ref.End, replacement})
			}
			continue
		}
		for i, ref := range draftRefs {
			use := plannedUse{}
			found := false
			for _, candidate := range plan.uses {
				if candidate.match.Kind == kind && candidate.match.Start == ref.Start {
					use, found = candidate, true
					break
				}
			}
			if found && i < len(savedRefs) {
				edits = append(edits, savedEdit{savedRefs[i].Start, savedRefs[i].End, use.replacement})
			}
		}
	}
	slices.SortStableFunc(edits, func(x, y savedEdit) int { return y.start - x.start })
	text := savedText
	changed := false
	for _, edit := range edits {
		if edit.start < 0 || edit.end > len(text) || edit.start > edit.end {
			continue
		}
		text = text[:edit.start] + edit.replacement + text[edit.end:]
		changed = true
	}
	return text, changed
}

func setSavedField(r *savedRequest, code, text string) bool {
	ref, ok := variableSavedField(r, code)
	if !ok || ref.text == nil {
		return false
	}
	*ref.text = text
	if strings.HasPrefix(code, "header-") && code != problemFieldHeaderRaw && len(r.HeaderList) > 0 {
		// The rows are the source of truth; the block the send reads is
		// regenerated from them, as requestDocument.value() does.
		r.Headers = headerRowsText(r.HeaderList)
	}
	return true
}

func setCopiedRequestField(s *variableStateCopy, plan *plannedField) bool {
	for i := range s.requests {
		if s.requests[i].key() == plan.key {
			return setSavedField(&s.requests[i], plan.code, plan.newText)
		}
	}
	return false
}

func setCopiedFolderField(s *variableStateCopy, plan *plannedField) bool {
	for i := range s.folders {
		if s.folders[i].Path == plan.key {
			ref, ok := variableFolderField(&s.folders[i], plan.code)
			if !ok || ref.text == nil {
				return false
			}
			*ref.text = plan.newText
			return true
		}
	}
	return false
}

// renameDefinition renames a row or map key in the copied scope state. Local
// Current overrides follow the row so their values stay with the new name.
func (s *variableStateCopy) renameDefinition(m variableMatch, to string) bool {
	switch m.Owner {
	case "env-initial", "env-current":
		for i := range s.envs {
			if s.envs[i].Name != m.Key {
				continue
			}
			renamed := renameMapKey(s.envs[i].Current, m.Text, to)
			if m.Owner == "env-initial" && renameMapKey(s.envs[i].Vars, m.Text, to) {
				renamed = true
			}
			if !renamed {
				return false
			}
			if marks := s.envs[i].Credentials; marks[m.Text] {
				delete(marks, m.Text)
				marks[to] = true
			}
			return true
		}
	case "global-initial":
		return renameMapKey(s.globals, m.Text, to) || renameMapKey(s.globalsCurrent, m.Text, to)
	case "global-current":
		return renameMapKey(s.globalsCurrent, m.Text, to)
	case "folder-def":
		for i := range s.folders {
			if s.folders[i].Path != m.Key || m.Index < 0 || m.Index >= len(s.folders[i].Variables) {
				continue
			}
			v := &s.folders[i].Variables[m.Index]
			if strings.TrimSpace(v.Name) != m.Text {
				return false
			}
			v.Name = to
			s.renameFolderCurrent(m.Key, m.Text, to)
			return true
		}
	}
	return false
}

func renameMapKey(values map[string]string, from, to string) bool {
	value, ok := values[from]
	if !ok {
		return false
	}
	delete(values, from)
	values[to] = value
	return true
}

func (s *variableStateCopy) renameFolderCurrent(path, from, to string) {
	if values := s.folderCurrent[path]; values != nil {
		if v, ok := values[from]; ok {
			delete(values, from)
			values[to] = v
		}
	}
	if marks := s.folderCredentials[path]; marks[from] {
		delete(marks, from)
		marks[to] = true
	}
}

// variableResolveField finds the current pointer for a change captured in the
// preview. A saved key is looked up again because a save may have replaced the
// slice since the preview opened.
func (a *app) variableResolveField(m variableMatch) (variableFieldRef, *requestDocument, bool) {
	switch m.Owner {
	case "document":
		for _, d := range a.documents {
			if d.debugKey == m.Key {
				ref, ok := variableDocumentField(d, m.Code)
				return ref, d, ok
			}
		}
		if a.libraryDocument != nil && a.libraryDocument.debugKey == m.Key {
			ref, ok := variableDocumentField(a.libraryDocument, m.Code)
			return ref, a.libraryDocument, ok
		}
	case "saved":
		for i := range a.requests {
			if a.requests[i].key() == m.Key {
				ref, ok := variableSavedField(&a.requests[i], m.Code)
				return ref, nil, ok
			}
		}
	case "folder":
		for i := range a.folders {
			if a.folders[i].Path == m.Key {
				ref, ok := variableFolderField(&a.folders[i], m.Code)
				return ref, nil, ok
			}
		}
	}
	return variableFieldRef{}, nil, false
}

// replaceVariableText edits only the range captured in the preview and only
// while it still holds the same reference. An editor's buffer keeps its undo
// history; a plain string is written where the send reads it.
func replaceVariableText(ref variableFieldRef, m variableMatch, replacement string) bool {
	// An editor buffer newer than the authored string owns that field; do
	// not write at offsets from a stale snapshot.
	if ref.state != nil && ref.state.Buffer.String() != *ref.text {
		return false
	}
	text := ref.current()
	if m.Start < 0 || m.End > len(text) || m.Start > m.End || text[m.Start:m.End] != m.Text[m.Start:m.End] {
		return false
	}
	if ref.state != nil && ref.state.Buffer.String() == text {
		ref.state.Buffer.Edit(editor.Edit{Start: m.Start, End: m.End, Text: replacement})
		*ref.text = ref.state.Buffer.String()
		if ref.seen != nil {
			*ref.seen = *ref.text
		}
	} else {
		*ref.text = text[:m.Start] + replacement + text[m.End:]
		if ref.seen != nil {
			*ref.seen = ""
		}
	}
	if ref.generation != nil {
		*ref.generation++
	}
	return true
}

// syncVariableField keeps derived document text and the Problems producer's
// caches consistent after a rename reaches an authored field.
func (a *app) syncVariableField(d *requestDocument, ref variableFieldRef, code string) {
	if ref.state != nil && ref.seen != nil {
		*ref.seen = ref.state.Buffer.String()
	}
	if d == nil {
		return
	}
	switch {
	case code == problemFieldHeaderRaw:
		d.headerFor = ""
	case strings.HasPrefix(code, "header-"):
		d.composeHeaders = headerRowsText(d.headerRows)
		d.headerFor = d.composeHeaders
	case code == problemFieldURL:
		d.querySource = ""
	case code == problemFieldBody:
		d.problemBodyGen++
	}
}

// openVariableMatch closes the list and asks the target editor to reveal the
// range on the next frame, once the tab and its editor have been built.
func (a *app) openVariableMatch(m variableMatch) {
	a.variableRefs.open = false
	a.variableRefs.reveal = variableReveal{
		owner: m.Owner, key: m.Key, code: m.Code,
		name: m.Text, start: m.Start, end: m.End, line: m.Line,
	}
	switch m.Owner {
	case "saved":
		a.openSaved(m.Key)
	case "folder":
		a.openFolder(m.Key)
	case "env-initial", "env-current":
		a.openEnvironment(m.Key)
	case "global-initial", "global-current":
		a.editGlobals()
		a.varsOpen = true
	}
	a.invalidate()
}
