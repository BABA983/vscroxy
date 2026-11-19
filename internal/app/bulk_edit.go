package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// bulkPreviewState is the part of a rename and a search replace that the
// bulk-edit dialog owns: the checked changes and their validation. Both
// features build these changes from a snapshot and apply only checked ones.
type bulkPreviewState struct {
	open, preview bool
	changes       []variableMatch
	current       int
	problem       string
	warning       string
}

func (s *bulkPreviewState) checkedCount() int {
	n := 0
	for i := range s.changes {
		if s.changes[i].Checked {
			n++
		}
	}
	return n
}

func (s *bulkPreviewState) checkedChanges() []variableMatch {
	return slices.DeleteFunc(slices.Clone(s.changes), func(m variableMatch) bool { return !m.Checked })
}

// bulkChangeList is the #44 bulk-edit preview: one group per resource, one
// label per field, one checkbox per change. Search replacement renders the
// very same list, with its expansion and any secret mask already in the change.
func (a *app) bulkChangeList(c *ui.Context, t *ui.Theme, changes []variableMatch) {
	resource, field := "", ""
	for i := range changes {
		m := &changes[i]
		if m.Resource != resource {
			resource, field = m.Resource, ""
			ui.Row(c.Key("resource-" + resource)).Height(uikit.HeightRow).FillWidth().AlignItems(ui.Center).Children(func() {
				uikit.BodyStrong(c, resource).Grow(1).MinWidth(0).SingleLine()
			})
		}
		if m.Field != field {
			field = m.Field
			ui.Row(c.Key(fmt.Sprintf("field-%s-%s", resource, field))).Height(uikit.HeightControlSmall).FillWidth().Children(func() {
				uikit.Meta(c, field).SingleLine()
			})
		}
		row := ui.Row(c.Key(fmt.Sprintf("bulk-change-%d", i))).Height(uikit.HeightRow).FillWidth().
			Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Padding(0, t.Space(uikit.SpaceM))
		if row.Hovered() {
			row.Background(t.SurfaceHover)
		}
		row.Children(func() {
			uikit.Checkbox(c, &m.Checked, m.location()).Shrink(0)
			ui.Row(c).Width(48).Shrink(0).Justify(ui.End).Children(func() {
				if m.Line > 0 {
					uikit.Meta(c, fmt.Sprint(m.Line)).FontFeatures("tnum")
				}
			})
			bulkChangeLine(c, *m).Grow(1).MinWidth(0)
			if m.ScopeLabel != "" {
				uikit.Meta(c, m.ScopeLabel).Width(160).SingleLine().Ellipsis("…").Shrink(0)
			}
		})
	}
}

// bulkChangeLine shows one change with the matched bytes struck through and
// the replacement beside them, as a search result shows its inline replace.
func bulkChangeLine(c *ui.Context, m variableMatch) ui.Element {
	return bulkEditLine(c, m.Text, m.Start, m.End, m.Replacement, m.Line, locales.OpenVariableReference(m.Field))
}

// bulkEditLine draws a line with its changed range struck through and the
// replacement added after it. It is used by the preview and by Search rows.
func bulkEditLine(c *ui.Context, text string, start, end int, replacement string, line int, label string) ui.Element {
	size := uikit.Scaled(uikit.TextData)
	if start < 0 || end < start || end > len(text) || replacement == "" {
		return ui.RichText(c, ui.Span{Text: text, Size: size}).
			Font(uikit.FontMono).FontSize(size).Label(label).Grow(1).MinWidth(0).SingleLine()
	}
	lineStart := strings.LastIndex(text[:start], "\n") + 1
	lineEnd := strings.IndexByte(text[end:], '\n')
	if lineEnd < 0 {
		lineEnd = len(text)
	} else {
		lineEnd += end
	}
	from, to := max(lineStart, end-48), min(lineEnd, start+48)
	spans := make([]ui.Span, 0, 6)
	if from > lineStart {
		spans = append(spans, ui.Span{Text: "…", Size: size, Color: uikit.Colors(c.Theme()).Faint})
	}
	spans = append(spans,
		ui.Span{Text: text[from:start], Size: size},
		ui.Span{Text: text[start:end], Size: size, Strikethrough: true,
			Color: uikit.Colors(c.Theme()).Faint},
		ui.Span{Text: replacement, Size: size, Color: uikit.Colors(c.Theme()).DiffAdded},
		ui.Span{Text: text[end:to], Size: size},
	)
	if to < lineEnd {
		spans = append(spans, ui.Span{Text: "…", Size: size, Color: uikit.Colors(c.Theme()).Faint})
	}
	return ui.RichText(c, spans...).Font(uikit.FontMono).FontSize(size).Label(label).Grow(1).MinWidth(0).SingleLine()
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

// planBulkChanges resolves every checked change against the live field and
// drops one whose captured range no longer holds the same bytes. Applying
// from these plans keeps offsets stable even when several matches share a
// field, and an unopened saved request uses the same path as a draft.
func (a *app) planBulkChanges(changes []variableMatch, snippet func(variableMatch) string) ([]*plannedField, []variableMatch, bool) {
	fields, byField := []*plannedField{}, map[string]*plannedField{}
	var values []variableMatch
	ok := true
	for _, m := range changes {
		if m.Kind == "definition" {
			// Rename owns the definition's new name; there is no byte range.
			values = append(values, m)
			continue
		}
		if bulkValueOwner(m.Owner) {
			if current, found := a.bulkValue(m); !found || current != m.Text {
				continue // the value changed since the preview
			}
			values = append(values, m)
			continue
		}
		ref, doc, found := a.variableResolveField(m)
		// An editor buffer newer than the captured string owns that field;
		// do not write at offsets from a stale snapshot.
		if !found || ref.state != nil && ref.state.Buffer.String() != *ref.text {
			continue
		}
		text := ref.current()
		if m.Start < 0 || m.End > len(text) || m.Start > m.End ||
			m.Text[m.Start:m.End] != text[m.Start:m.End] {
			continue
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
		plan.uses = append(plan.uses, plannedUse{match: m, replacement: snippet(m)})
	}
	for _, plan := range fields {
		// Later offsets first so earlier ones keep their positions.
		slices.SortStableFunc(plan.uses, func(x, y plannedUse) int { return y.match.Start - x.match.Start })
		text := plan.oldText
		lastStart := len(text) + 1
		for _, use := range plan.uses {
			m := use.match
			if m.End > len(text) || m.Start >= lastStart || text[m.Start:m.End] != m.Text[m.Start:m.End] {
				continue // overlapping or displaced change: never write at a shifted offset
			}
			text = text[:m.Start] + use.replacement + text[m.End:]
			lastStart = m.Start
		}
		plan.newText = text
	}
	if len(fields)+len(values) == 0 {
		ok = false
	}
	return fields, values, ok
}

// bulkValueOwner separates values held in maps and folder rows from the
// request/folder text fields variableFieldRef resolves.
func bulkValueOwner(owner string) bool {
	switch owner {
	case searchValueEnvInitial, searchValueEnvCurrent, searchValueGlobalInitial,
		searchValueGlobalCurrent, searchValueFolderDef, searchValueFolderCurrent:
		return true
	}
	return false
}

// bulkCommitOptions is what a caller adds around the shared write: how a
// value or a draft's saved copy is changed on the copies, and what to report.
type bulkCommitOptions struct {
	label      string
	done       string
	value      func(state *variableStateCopy, m variableMatch, text string) (library, local bool)
	savedDraft func(state *variableStateCopy, plan *plannedField) bool
}

// commitBulkEdits writes the library once from copies, then adopts memory
// and editor buffers. A refused write leaves requests, folders, environments
// and every draft as they were; the writeLibrary conflict view owns the retry.
func (a *app) commitBulkEdits(fields []*plannedField, values []variableMatch, state variableStateCopy, o bulkCommitOptions) bool {
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
	localChanged := false
	for _, m := range values {
		if o.value == nil {
			continue
		}
		library, local := o.value(&state, m, m.Replacement)
		libraryChanged, localChanged = libraryChanged || library, localChanged || local
	}
	// An open tab may be dirty with edits the reader never saved. Change the
	// saved copy for the file, not the draft: the reader's own changes stay
	// only in the tab, and its baseline becomes the changed saved request.
	baselines := map[*requestDocument]savedRequest{}
	for _, plan := range fields {
		if plan.owner != "document" || plan.doc == nil || plan.doc.savedKey == "" || plan.doc.assetID == "" {
			continue
		}
		if o.savedDraft != nil && !o.savedDraft(&state, plan) {
			continue
		}
		saved := savedRequestForDoc(state.requests, plan.doc)
		if saved == nil {
			continue
		}
		baselines[plan.doc] = documentBaseline(a, *saved, plan.doc.savedKey)
		libraryChanged = true
	}
	if libraryChanged {
		if err := a.writeLibrary(state.requests, state.folders); err != nil {
			a.fail(o.label, err)
			return false // no live state moved
		}
	}
	if localChanged {
		a.envs, a.globals, a.globalsCurrent = state.envs, state.globals, state.globalsCurrent
		a.folderCurrent, a.folderCredentials = state.folderCurrent, state.folderCredentials
	}
	// Only now, after the files are safe, touch the drafts and their buffers.
	for _, plan := range fields {
		if plan.owner != "document" || plan.doc == nil {
			continue
		}
		for _, use := range plan.uses {
			ref, _, found := a.variableResolveField(use.match)
			if !found || !replaceVariableText(ref, use.match, use.replacement) {
				continue
			}
			a.syncVariableField(plan.doc, ref, plan.code)
		}
		if baseline, found := baselines[plan.doc]; found {
			plan.doc.baseline = baseline
		}
	}
	if localChanged {
		if err := a.saveLocalEnvs(); err != nil {
			a.fail(locales.Variables(), err)
		}
	}
	if o.done != "" {
		a.done(o.done)
	}
	a.invalidate()
	return true
}

// bulkPreviewDialog is the shared bulk-edit dialog body. Rename wraps its
// first step in the same modal; search replacement opens straight here.
func (a *app) bulkPreviewDialog(c *ui.Context, t *ui.Theme, s *bulkPreviewState, title, subtitle string, extra func(), footer func(int)) {
	_, height := c.Size()
	// A small preview should not leave a page of empty space below the last
	// change. Keep the old ceiling for a large rename or Replace All.
	wanted := float32(200 + min(len(s.changes), 12)*28)
	uikit.DialogColumn(c, uikit.DialogLarge).Height(min(max(wanted, 200), height-80)).Children(func() {
		uikit.DialogTitle(c, title)
		uikit.Meta(c, subtitle)
		if s.warning != "" {
			uikit.ErrorText(c, s.warning)
		}
		if s.problem != "" {
			uikit.ErrorText(c, s.problem)
		}
		if extra != nil {
			extra()
		}
		checked := s.checkedCount()
		uikit.Caption(c, locales.RenameChangeCount(checked, len(s.changes)))
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
			a.bulkChangeList(c, t, s.changes)
		})
		if footer != nil {
			footer(checked)
		}
	})
}
