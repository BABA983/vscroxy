package app

import (
	"cmp"
	"encoding/json/v2"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/lifecycle"
	"github.com/baba983/vscroxy/internal/markers"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

const (
	problemOwnerAssertions = "vscroxy.request.assertions"
	problemOwnerScripts    = "vscroxy.request.scripts"
	problemOwnerVariables  = "vscroxy.request.variables"
	problemOwnerImports    = "vscroxy.imports"

	problemFieldURL         = "url"
	problemFieldBody        = "body"
	problemFieldPre         = "pre-request-script"
	problemFieldPost        = "post-response-script"
	problemFieldChecks      = "checks"
	problemFieldDescription = "description"
	problemFieldHeaderRaw   = "header-bulk"
	problemFieldImport      = "import"

	// Form rows and auth strings name the exact authored field a marker
	// came from, so a code action can rewrite that field's range.
	problemFieldFormPrefix = "form-"
	problemFieldAuthPrefix = "auth:"

	problemResourcePrefix = "vscroxy:request/"
	problemResourceImport = "vscroxy:import"
)

var problemSeverities = []markers.Severity{markers.Error, markers.Warning, markers.Info, markers.Hint}

// problemInputKey is what a variable marker read depends on in one document.
// The body is represented by its editor version or the generation bumped
// where code writes composeBody, so an unchanged frame never copies or scans
// a large body just to find {{variables}} again.
type problemInputKey struct {
	url, headers, pre, post string
	collection, method      string
	auth, graphQL, forms    string
	protocol                string
	pathVariables           string
	bodyFile                string
	bodyVersion             uint64
	bodyLength              int
	checkStatus             string
	checkHeaders            string
	checkJSON               string
	checkSnapshot           *request.Snapshot
}

// variablesResolver is the seam the contract test counts. The default calls
// the same request.Request.ResolveVariables the Variables panel uses.
type variablesResolver func(savedRequest, request.Context, map[string]string) []request.ResolvedVariable

// problemsState is the panel's view state. Signatures keep a producer from
// firing an unchanged marker set every frame: MyGo rebuilds several times for
// one edit, and Debug variables are read on each pass.
type problemsState struct {
	query     string
	filter    ui.Handle
	focusNext bool // the filter field takes the keyboard on the next build
	show      [4]bool
	collapsed map[string]bool
	selection int
	// signatures holds the last marker set written for resource|owner.
	signatures map[string]string
	// docKeys and envKey are the last inputs read by the variable producer.
	docKeys map[string]problemInputKey
	envKey  string
	// resolveVariables is replaceable by the contract test to count reads.
	resolveVariables variablesResolver
	// importSignature is the import review's title and lines while open.
	importSignature string
	// pendingFocus waits for an editor the click just brought on screen.
	pendingFocus string
	pendingLine  int
	pendingCol   int
	// caret target and its fixes belong to the focused authoring field.
	// They drive the editor's Cmd/Ctrl+. menu and the palette command.
	caret       *quickFixTarget
	caretFixes  []quickFix
	fixMenuOpen bool
	// variableCaret names the {{variable}} under the caret even when it is
	// defined and has no quick fix. It feeds Rename and Find All References.
	variableCaret *quickFixTarget
	subscription  lifecycle.Disposable
}

func (a *app) initProblems() {
	if a.markers != nil {
		return
	}
	a.markers = markers.New()
	a.problems.show = [4]bool{true, true, true, true}
	a.problems.selection = -1 // no marker is current until one is chosen or navigated to
	a.problems.signatures = make(map[string]string)
	a.problems.docKeys = make(map[string]problemInputKey)
	a.problems.resolveVariables = func(r savedRequest, in request.Context, vars map[string]string) []request.ResolvedVariable {
		return r.Request.ResolveVariables(in, vars, nil)
	}
	a.problems.subscription = a.markers.OnMarkerChanged(func([]string) { a.invalidate() })
}

// problemService makes the service available to apps built as test literals
// as well as to a fully initialized window.
func (a *app) problemService() *markers.Service {
	if a.markers == nil {
		a.initProblems()
	}
	return a.markers
}

func problemResource(d *requestDocument) string { return problemResourcePrefix + d.debugKey }

func (a *app) documentForProblem(resource string) *requestDocument {
	if !strings.HasPrefix(resource, problemResourcePrefix) {
		return nil
	}
	key := strings.TrimPrefix(resource, problemResourcePrefix)
	for _, d := range a.problemDocuments() {
		if d.debugKey == key {
			return d
		}
	}
	return nil
}

// problemDocuments are the documents whose markers are still live: open
// tabs, the inspected capture's draft and a capture draft kept for later.
func (a *app) problemDocuments() []*requestDocument {
	var out []*requestDocument
	seen := make(map[*requestDocument]bool)
	add := func(d *requestDocument) {
		if d != nil && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	for _, d := range a.documents {
		add(d)
	}
	add(a.live)
	for _, d := range a.flowDrafts {
		add(d)
	}
	return out
}

// problemResourceLabel names a resource as the panel shows it. A captured
// draft keeps the request's own name rather than its generated UUID.
func (a *app) problemResourceLabel(resource string) string {
	if resource == problemResourceImport {
		return locales.ImportReview()
	}
	if d := a.documentForProblem(resource); d != nil {
		return d.title()
	}
	return strings.TrimPrefix(resource, problemResourcePrefix)
}

// writeProblems replaces one owner's markers for a resource unless it would
// write the same set again.
func (a *app) writeProblems(owner, resource string, list []markers.Marker) {
	key := resource + "|" + owner
	signature := markers.Signature(list)
	if a.problems.signatures[key] == signature {
		return
	}
	a.problems.signatures[key] = signature
	a.problemService().ChangeOne(owner, resource, list)
}

// clearDocumentProblems releases all markers owned by a closed or deleted
// request, whatever producer last wrote them.
func (a *app) clearDocumentProblems(d *requestDocument) {
	if d == nil {
		return
	}
	resource := problemResource(d)
	for _, owner := range []string{problemOwnerAssertions, problemOwnerScripts, problemOwnerVariables} {
		a.problems.signatures[resource+"|"+owner] = ""
	}
	a.problemService().Remove(problemOwnerAssertions, []string{resource})
	a.problemService().Remove(problemOwnerScripts, []string{resource})
	a.problemService().Remove(problemOwnerVariables, []string{resource})
}

func (a *app) clearSendProblemMarkers(d *requestDocument) {
	resource := problemResource(d)
	for _, owner := range []string{problemOwnerAssertions, problemOwnerScripts} {
		a.writeProblems(owner, resource, nil)
	}
}

// applySendProblems turns the last send's failed tests and script errors into
// markers. The assertion itself stays a warning to the runner, not a network
// failure; it is an error in the Problems panel as it is in VS Code's view.
func (a *app) applySendProblems(result requestResult) {
	d := result.document
	if d == nil {
		return
	}
	resource := problemResource(d)
	scriptErrors, assertions := []markers.Marker{}, []markers.Marker{}
	for _, test := range result.tests {
		if test.Pass || test.Skipped {
			continue
		}
		assertions = append(assertions, markers.Marker{
			Severity: markers.Error, Source: test.Name,
			Message: locales.ProblemCheckFailed(test.Name), Code: problemFieldChecks,
		})
	}
	if result.err != nil && (result.outcome == request.StagePreRequestScript || result.outcome == request.StagePostResponseScript) {
		code := problemFieldPost
		if result.outcome == request.StagePreRequestScript {
			code = problemFieldPre
		}
		scriptErrors = append(scriptErrors, markers.Marker{
			Severity: markers.Error, Source: result.outcome, Code: code,
			Message: locales.ProblemScriptFailed(ntlmErrorText(result.err)),
		})
	}
	a.writeProblems(problemOwnerAssertions, resource, assertions)
	a.writeProblems(problemOwnerScripts, resource, scriptErrors)
}

// problemSignature is a compact, comparable form of a small struct. Auth and
// the protocol configs are what request.Request.Variables reads beyond plain
// text fields; JSON gives every nested field a stable name without repeating
// that switch here.
func problemSignature(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%T:%v", value, value)
	}
	return string(data)
}

func problemFieldsSignature(fields []request.Field) string {
	var b strings.Builder
	for _, field := range fields {
		b.WriteString(strconv.FormatBool(field.Disabled))
		b.WriteByte(0)
		b.WriteString(strconv.FormatBool(field.File))
		b.WriteByte(0)
		b.WriteString(field.Name)
		b.WriteByte(0)
		b.WriteString(field.Value)
		b.WriteByte(0)
	}
	return b.String()
}

// problemInputKey reads only small authoring fields. It covers every input
// d.value() passes to request.Request.Variables: authoring text, auth, form
// and GraphQL fields, protocol configs, collection and method. The large body
// is represented by its editor version or the generation bumped where code
// replaces composeBody, so an unchanged frame never copies or scans it.
func (d *requestDocument) problemInputKey() problemInputKey {
	key := problemInputKey{
		url: d.composeURL, headers: d.composeHeaders, pre: d.composePre, post: d.composePost,
		collection: d.composeCollection, method: d.composeMethod,
		auth:    problemSignature(d.auth),
		graphQL: d.graphql.Query + "\x00" + d.graphql.Variables + "\x00" + d.graphql.OperationName,
		forms:   problemFieldsSignature(d.formFields),
		protocol: d.protocol + "\x00" + problemSignature(d.socketIOConfig) +
			"\x00" + problemSignature(d.mqttConfig) + "\x00" + problemSignature(d.mcpConfig),
		pathVariables: problemFieldsSignature(d.pathRows),
		bodyFile:      d.bodyFile,
		bodyVersion:   d.problemBodyGen, bodyLength: len(d.composeBody),
		checkStatus: d.checks.Status, checkHeaders: d.checks.Headers,
		checkJSON: d.checks.JSON, checkSnapshot: d.checks.Snapshot,
	}
	if d.bodyEditor != nil && d.bodyFor == d.composeBody {
		key.bodyVersion = d.bodyEditor.Buffer.Version()
	}
	return key
}

// problemVariablesKey is the app-wide half of the cache key: the environment
// in use, every scope's values and the folder inheritance a request draws
// from. These maps are small; unlike a request body, they are cheap to read
// once per frame.
func (a *app) problemVariablesKey() string {
	var b strings.Builder
	b.WriteString(a.activeEnv)
	b.WriteByte(0)
	appendMap := func(values map[string]string) {
		for _, name := range slices.Sorted(maps.Keys(values)) {
			b.WriteString(name)
			b.WriteByte('=')
			b.WriteString(values[name])
			b.WriteByte(0)
		}
		b.WriteByte(';')
	}
	for i := range a.envs {
		b.WriteString(a.envs[i].Name)
		b.WriteByte(0)
		appendMap(a.envs[i].Vars)
		appendMap(a.envs[i].Current)
	}
	appendMap(a.globals)
	appendMap(a.globalsCurrent)
	for _, path := range slices.Sorted(maps.Keys(a.folderCurrent)) {
		b.WriteString(path)
		b.WriteByte(0)
		appendMap(a.folderCurrent[path])
	}
	folders := slices.Clone(a.folders)
	slices.SortFunc(folders, func(left, right request.Folder) int { return cmp.Compare(left.Path, right.Path) })
	for _, folder := range folders {
		b.WriteString(folder.Path)
		b.WriteByte(0)
		b.WriteString(problemSignature(folder.Auth)) // inherited auth can define variables too
		for _, variable := range folder.Variables {
			b.WriteString(variable.Name)
			b.WriteByte('=')
			b.WriteString(variable.Initial)
			b.WriteByte('=')
			if variable.Disabled {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
			b.WriteByte(0)
		}
		b.WriteByte(';')
	}
	b.WriteString(strconv.Itoa(a.requestGen))
	return b.String()
}

// syncProblems recomputes the variable markers only when one of their inputs
// changed, and clears markers whose document or import review is gone.
func (a *app) syncProblems() {
	service := a.problemService()
	a.syncImportProblems()
	documents := a.problemDocuments()
	variablesKey := a.problemVariablesKey()
	variablesChanged := variablesKey != a.problems.envKey
	if a.problems.envKey == "" {
		variablesChanged = true // the first pass establishes the baseline
	}
	live := make(map[string]bool, len(documents))
	for _, d := range documents {
		resource := problemResource(d)
		live[resource] = true
		key := d.problemInputKey()
		previous, seen := a.problems.docKeys[resource]
		if seen && key == previous && !variablesChanged {
			continue
		}
		// An edit to a send's source fields clears its last-send markers;
		// those fields are all part of key, so unchanged frames skip this too.
		if d.sentSet && d.sentSourceChanged() {
			a.clearSendProblemMarkers(d)
		}
		r := d.value()
		rows := a.problems.resolveVariables(r, a.inheritance(r.Collection), a.activeVars())
		var list []markers.Marker
		for _, row := range rows {
			if !row.Missing {
				continue
			}
			code, codeRange := problemVariableLocation(d, row.Name)
			list = append(list, markers.Marker{
				Severity: markers.Warning, Source: row.Name, Code: code,
				Message: locales.ProblemUnresolvedVariable(row.Name), Range: codeRange,
			})
		}
		a.writeProblems(problemOwnerVariables, resource, list)
		a.problems.docKeys[resource] = key
	}
	a.problems.envKey = variablesKey
	for _, resource := range service.Resources() {
		if strings.HasPrefix(resource, problemResourcePrefix) && !live[resource] {
			service.Remove(problemOwnerAssertions, []string{resource})
			service.Remove(problemOwnerScripts, []string{resource})
			service.Remove(problemOwnerVariables, []string{resource})
			for _, owner := range []string{problemOwnerAssertions, problemOwnerScripts, problemOwnerVariables} {
				delete(a.problems.signatures, resource+"|"+owner)
			}
			delete(a.problems.docKeys, resource)
		}
	}
}

// sentSourceChanged reports an edit to the fields the last send reported on.
// A completed send records exactly what it carried in d.sent.
func (d *requestDocument) sentSourceChanged() bool {
	return d.composeURL != d.sent.URL || d.composeHeaders != d.sent.Headers ||
		d.composeBody != d.sent.Body || d.composePre != d.sent.Pre ||
		d.composePost != d.sent.Post || !reflect.DeepEqual(d.checks, d.sent.Checks)
}

// syncImportProblems follows the import review modal, which is the real
// producer of internal/imports' warnings. Closing or replacing it clears the
// markers; "Review collection" can leave the report closed, which is also a
// dismissal of its warnings.
func (a *app) syncImportProblems() {
	var list []markers.Marker
	if a.importReport.open {
		for _, line := range a.importReport.lines {
			list = append(list, markers.Marker{
				Severity: markers.Warning, Source: locales.ImportReview(),
				Message: line, Code: problemFieldImport,
			})
		}
	}
	signature := fmt.Sprint(a.importReport.open, "|", strings.Join(a.importReport.lines, "\n"))
	if signature == a.problems.importSignature {
		return
	}
	a.problems.importSignature = signature
	a.problemService().ChangeOne(problemOwnerImports, problemResourceImport, list)
}

// problemVariableLocation reuses the editor's variable references to place a
// marker on the first field that holds the unresolved name. The code names
// that exact authored field so a code action can edit only its range.
func problemVariableLocation(d *requestDocument, name string) (string, *markers.Range) {
	fields := []struct{ code, text string }{
		{problemFieldURL, d.composeURL},
		{problemFieldBody, d.composeBody},
		{problemFieldPre, d.composePre},
		{problemFieldPost, d.composePost},
	}
	for _, field := range fields {
		for _, ref := range request.VariableRefs(field.text) {
			if ref.Name == name {
				return field.code, problemTextRange(field.text, ref)
			}
		}
	}
	if !d.headerBulk && len(d.headerRows) == 0 && d.composeHeaders != "" {
		// A draft that has not built its Headers tab yet still needs a
		// row code the fix can address.
		d.headerRows, d.headerFor = parseHeaderRows(d.composeHeaders), d.composeHeaders
	}
	if d.headerBulk || headerRowsText(d.headerRows) != d.composeHeaders {
		// The block is the source of truth in bulk mode; the rows have not
		// caught up to a script or a paste yet.
		_, codeRange := findInText(d.composeHeaders, name)
		return problemFieldHeaderRaw, codeRange
	}
	for i, row := range d.headerRows {
		for _, part := range []struct{ name, text string }{{"key", row.Name}, {"value", row.Value}} {
			_, codeRange := findInText(part.text, name)
			if codeRange != nil {
				return fmt.Sprintf("header-%s-%d", part.name, i+1), codeRange
			}
		}
	}
	for _, auth := range authAuthPaths(d.auth) {
		for _, ref := range request.VariableRefs(auth.text) {
			if ref.Name == name {
				return auth.code, problemTextRange(auth.text, ref)
			}
		}
	}
	for i, row := range d.formFields {
		for _, part := range []struct{ name, text string }{{"key", row.Name}, {"value", row.Value}} {
			_, codeRange := findInText(part.text, name)
			if codeRange != nil {
				return fmt.Sprintf("%s%s-%d", problemFieldFormPrefix, part.name, i+1), codeRange
			}
		}
	}
	return problemFieldURL, nil
}

func findInText(text, name string) (string, *markers.Range) {
	for _, ref := range request.VariableRefs(text) {
		if ref.Name == name {
			return "", problemTextRange(text, ref)
		}
	}
	return "", nil
}

func problemTextRange(text string, ref request.VariableRef) *markers.Range {
	line := 1 + strings.Count(text[:ref.Start], "\n")
	lineStart := strings.LastIndex(text[:ref.Start], "\n") + 1
	return &markers.Range{
		StartLineNumber: line, StartColumn: ref.Start - lineStart + 1,
		EndLineNumber: line, EndColumn: ref.End - lineStart + 1,
	}
}

// visibleProblems is the panel's sorted read: one resource group at a time,
// most severe marker first within it.
func (a *app) visibleProblems() []markers.Marker {
	var mask markers.Severity
	for i, severity := range problemSeverities {
		if a.problems.show[i] {
			mask |= severity
		}
	}
	list := a.problemService().Read(markers.Filter{Severities: mask})
	query := strings.ToLower(strings.TrimSpace(a.problems.query))
	if query != "" {
		list = slices.DeleteFunc(list, func(m markers.Marker) bool {
			return !strings.Contains(strings.ToLower(m.Message), query) &&
				!strings.Contains(strings.ToLower(a.problemResourceLabel(m.Resource)), query)
		})
	}
	slices.SortStableFunc(list, func(left, right markers.Marker) int {
		leftLabel, rightLabel := a.problemResourceLabel(left.Resource), a.problemResourceLabel(right.Resource)
		if c := cmp.Compare(leftLabel, rightLabel); c != 0 {
			return c
		}
		if c := markers.CompareSeverity(left, right); c != 0 {
			return c
		}
		if c := cmp.Compare(left.Code, right.Code); c != 0 {
			return c
		}
		if c := cmp.Compare(left.Message, right.Message); c != 0 {
			return c
		}
		return cmp.Compare(left.Resource, right.Resource)
	})
	return list
}

func (a *app) openProblems() {
	a.bottom.open, a.bottom.tab = true, bottomProblems
}

func (a *app) focusProblems() {
	a.openProblems()
	// The filter field may not be built yet when the panel was on Console:
	// the frame that builds it gives it the keyboard.
	a.problems.focusNext = true
}

// navigateProblems is the Problems panel's form of VS Code's
// editor.action.marker.next: walk the visible markers, wrapping at both ends,
// and reveal the one landed on.
func (a *app) navigateProblems(step int) {
	list := a.visibleProblems()
	if len(list) == 0 {
		return
	}
	a.openProblems()
	n := len(list)
	a.problems.selection = (a.problems.selection + step + n) % n
	a.openProblem(list[a.problems.selection])
}

// openProblem reveals a marker's request and puts the caret in the field or
// range it names. Import warnings reopen the review whose lines produced them.
func (a *app) openProblem(m markers.Marker) {
	if m.Resource == problemResourceImport {
		a.importReport.open = true
		if a.importReport.review != "" {
			a.openSaved(a.importReport.review)
		}
		return
	}
	d := a.documentForProblem(m.Resource)
	if d == nil {
		return
	}
	a.activateDocument(d)
	switch m.Code {
	case problemFieldChecks:
		d.responseTab = resViewTests
	case problemFieldPre, problemFieldPost:
		d.composeTab = composeTabScripts
		d.scriptTab = 0
		if m.Code == problemFieldPost {
			d.scriptTab = 1
		}
	case problemFieldBody:
		d.composeTab = composeTabBody
	case problemFieldHeaderRaw:
		d.composeTab = composeTabHeaders
	default:
		if strings.HasPrefix(m.Code, "header-") {
			d.composeTab = composeTabHeaders
		}
	}
	if m.Code != problemFieldURL && m.Code != problemFieldChecks {
		d.unicodeHandle(m.Code).Focus()
	}
	if m.Range != nil {
		a.problems.pendingFocus, a.problems.pendingLine, a.problems.pendingCol = m.Code, m.Range.StartLineNumber, m.Range.StartColumn
		a.applyProblemLocation()
	}
}

// applyProblemLocation asks the editor named by a click to reveal the range.
// An editor built only after the tab switch gets its position on a later pass.
func (a *app) applyProblemLocation() {
	p := &a.problems
	if p.pendingFocus == "" {
		return
	}
	d := a.requestDocument
	var state *editor.State
	switch p.pendingFocus {
	case problemFieldBody:
		state = d.bodyEditor
	case problemFieldPre:
		state = d.preEditor
	case problemFieldPost:
		state = d.postEditor
	case problemFieldHeaderRaw:
		state = d.bulkEditor
	default:
		p.pendingFocus = ""
		return
	}
	if state == nil {
		return // retry when the tab has built its editor
	}
	state.GoTo(editor.Pos{Line: max(0, p.pendingLine-1), Col: max(0, p.pendingCol-1)})
	p.pendingFocus = ""
}

func severityLabel(severity markers.Severity) string {
	switch severity {
	case markers.Error:
		return locales.ProblemError()
	case markers.Warning:
		return locales.ProblemWarning()
	case markers.Info:
		return locales.ProblemInfo()
	default:
		return locales.ProblemHint()
	}
}

func severityGlyph(severity markers.Severity, t *ui.Theme) (*ui.SVG, ui.Color) {
	palette := uikit.Colors(t)
	switch severity {
	case markers.Error:
		return errorIcon, palette.Danger
	case markers.Warning:
		return warningIcon, palette.Warning
	case markers.Info:
		return infoIcon, palette.Info
	default:
		return hintIcon, palette.MutedForeground
	}
}

// problemsView is the Problems tab of the bottom panel: its rows grouped by
// request, each group folding from its heading. The search field and the
// severity filter sit in the panel's tab row, so the list owns the panel's
// whole height.
func (a *app) problemsView(c *ui.Context, t *ui.Theme) {
	ui.Scroll(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
		list := a.visibleProblems()
		if len(list) == 0 {
			message, hint := locales.NoProblems(), locales.NoProblemsHint()
			if a.problems.query != "" || slices.Contains(a.problems.show[:], false) {
				message, hint = locales.NoMatchingProblems(), locales.NoProblemsHint()
			}
			ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Children(func() {
				uikit.Body(c, message)
				uikit.Hint(c, hint)
			})
			return
		}
		previous := ""
		collapsed := false
		for i, m := range list {
			label := a.problemResourceLabel(m.Resource)
			if label != previous {
				if previous != "" {
					ui.Divider(c)
				}
				previous = label
				// A group is its heading, its method and its count. The
				// heading keeps the count of every marker under it, even
				// while the group is folded.
				count := 0
				for _, other := range list[i:] {
					if a.problemResourceLabel(other.Resource) != label {
						break
					}
					count++
				}
				open := !a.problems.collapsed[label]
				row := ui.Row(c.Key("problem-group-"+strconv.Itoa(i))).FillWidth().
					Height(uikit.HeightRow).Padding(0, t.Space(uikit.SpaceL)).
					Gap(t.Space(uikit.SpaceS)).Radius(uikit.RadiusControl).AlignItems(ui.Center).
					Label(locales.ProblemGroup(label))
				if row.Hovered() {
					row.Background(t.SurfaceHover)
				}
				row.Children(func() {
					branchArrow(c, &open)
					if d := a.documentForProblem(m.Resource); d != nil {
						if method := cmp.Or(d.protocol, d.composeMethod); method != "" {
							uikit.MethodLabel(c, method).FontSize(uikit.Scaled(uikit.TextMeta)).Shrink(0)
						}
					}
					uikit.Body(c, label).Grow(1).MinWidth(0).SingleLine()
					searchCountBadge(c, count)
				})
				if row.Clicked() {
					open = !open
				}
				if open == a.problems.collapsed[label] {
					setCollapsed(&a.problems.collapsed, label, !open)
				}
				collapsed = !open
			}
			if collapsed {
				continue
			}
			row := ui.Row(c.Key("problem-"+strconv.Itoa(i))).FillWidth().
				Height(uikit.HeightRow).PaddingStart(t.Space(uikit.SpaceL)+uikit.HeightRowAction+t.Space(uikit.SpaceXL)).
				PaddingEnd(t.Space(uikit.SpaceL)).
				Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Center)
			if i == a.problems.selection {
				row.Background(t.SurfaceHover)
			}
			row.Children(func() {
				icon, ink := severityGlyph(m.Severity, t)
				ui.Icon(c, icon).Size(uikit.IconInline, uikit.IconInline).TextColor(ink).Shrink(0).Label(severityLabel(m.Severity))
				uikit.Body(c, m.Message).Grow(1).MinWidth(0).SingleLine()
				if fixes := a.quickFixesForMarker(m); len(fixes) > 0 {
					rowIndex := i
					uikit.IconButton(c, locales.QuickFix(), locales.QuickFix(), hintIcon).
						PopoverMenuButton(func(menu *ui.Menu) {
							for j := range fixes {
								fix := fixes[j]
								if menu.Item(fix.label).Chosen() {
									a.problems.selection = rowIndex
									fix.apply()
								}
							}
						}, uikit.MenuButtonOptions(c)).Shrink(0)
				}
				if m.Range != nil {
					uikit.DataMeta(c, strconv.Itoa(m.Range.StartLineNumber)+":"+strconv.Itoa(m.Range.StartColumn)).Shrink(0)
				}
			})
			if row.Clicked() {
				a.problems.selection = i
				a.openProblem(m)
			}
		}
	})
}

// problemCount is every marker the Problems tab holds, including the
// severities the filter hides: the tab's count says what the panel found.
func (a *app) problemCount() int {
	return len(a.problemService().Read(markers.Filter{}))
}

// problemsPanelActions are the Problems tab's own controls, at the panel tab
// row's end: the filter field, the severity menu and its dot while a
// severity is hidden. Closing the panel is the row's shared action.
func (a *app) problemsPanelActions(c *ui.Context, t *ui.Theme) {
	field, input := uikit.SearchFieldInput(c, &a.problems.query, locales.FilterProblems(), locales.FilterProblems(), false)
	field.Width(uikit.WidthFieldWide).Shrink(1).MinWidth(uikit.WidthNarrow)
	input.Bind(&a.problems.filter)
	if a.problems.focusNext {
		input.Focus()
		a.problems.focusNext = false
	}
	input.OnShortcut(0, ui.KeyEscape, func() { a.problems.query = "" })
	if a.problems.query != "" {
		field.Children(func() {
			if uikit.Tip(c, uikit.RowIconButton(c, locales.Clear(), locales.Clear(), closeIcon), locales.Clear()).Clicked() {
				a.problems.query = ""
				a.problems.filter.Focus(c)
			}
		})
	}
	var filter ui.Element
	uikit.MenuFace(c, func() {
		filter = uikit.IconButton(c, locales.FilterProblemsBySeverity(), locales.FilterProblemsBySeverity(), filterIcon)
	})
	filter.PopoverMenuButton(func(m *ui.Menu) {
		for i, severity := range problemSeverities {
			if m.Item(severityLabel(severity)).Checked(a.problems.show[i]).Chosen() {
				a.problems.show[i] = !a.problems.show[i]
			}
		}
	}, uikit.MenuButtonOptions(c))
	if slices.Contains(a.problems.show[:], false) {
		filter.DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X + r.W - uikit.DotSize - 3, Y: r.Y + 4, W: uikit.DotSize, H: uikit.DotSize},
				t.Warning, uikit.DotSize/2)
		})
	}
}
