package app

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// searchReplaceState is VS Code's Search view Replace field and its bulk edit.
// The preview is the same bulkPreviewState the variable rename uses; this
// state adds only the replacement template and its input handle.
type searchReplaceState struct {
	bulkPreviewState
	input       string
	handle      ui.Handle
	preserve    bool
	confirmOpen bool
}

// searchChanges builds one checked change per replaceable result. Regexp
// captures are expanded here, once, so the preview and the write use exactly
// the same bytes.
func (a *app) searchChanges(indices ...int) []variableMatch {
	s := &a.searchAll
	if len(indices) == 0 {
		for i := range s.results {
			if s.results[i].replaceable() {
				indices = append(indices, i)
			}
		}
	}
	out := make([]variableMatch, 0, len(indices))
	for _, at := range indices {
		if at < 0 || at >= len(s.results) {
			continue
		}
		r := s.results[at]
		if !r.replaceable() {
			continue // captured traffic and other read-only results
		}
		from, to := r.start, r.end
		replacement := s.query.expand(r.text, from, to, s.replace.input)
		if s.replace.preserve {
			replacement = preserveCase(r.match(), replacement)
		}
		m := variableMatch{
			Owner: r.target.owner, Key: r.target.key, Code: r.target.code,
			Collection: r.target.collection, Resource: r.resource, Field: r.field,
			Text: r.fieldText, Start: r.target.start, End: r.target.end,
			Line: r.line, LineText: r.text, Kind: "search", Checked: true,
			Replacement: replacement, Index: r.target.index,
		}
		if r.target.valueKind != "" {
			m.Owner = r.target.valueKind
			value := r.fieldText
			if m.Start < 0 || m.End > len(value) || m.Start > m.End {
				continue
			}
			m.Replacement = value[:m.Start] + replacement + value[m.End:]
		}
		out = append(out, m)
	}
	return out
}

// replaceableSearchCount counts results that can be written: flows, history
// and name-only results without a field target are deliberately left out.
func (a *app) replaceableSearchCount() int {
	n := 0
	for _, r := range a.searchAll.results {
		if r.replaceable() {
			n++
		}
	}
	return n
}

// openSearchReplace asks before writing every replaceable match. The
// confirmation names the count, the resources and the replacement, as a
// bulk edit that leaves the window does.
func (a *app) openSearchReplace() {
	if a.replaceableSearchCount() == 0 {
		a.warn(locales.NoSearchMatchesToReplace())
		return
	}
	a.searchAll.replace.open = true
	a.searchAll.replace.confirmOpen = true
	a.invalidate()
}

// openSearchPreview prepares the shared bulk-edit dialog with every change
// checked. It is the preview the bulk editor already used; Replace All now
// confirms in the sidebar first and writes directly.
func (a *app) openSearchPreview() {
	s := &a.searchAll
	changes := a.searchChanges()
	if len(changes) == 0 {
		a.warn(locales.NoSearchMatchesToReplace())
		return
	}
	s.replace.preview, s.replace.open = true, true
	s.replace.problem, s.replace.warning = "", ""
	s.replace.changes, s.replace.current = changes, 0
	a.invalidate()
}

// searchReplaceableResources counts the resources the replaceable matches
// belong to, for the confirmation.
func searchReplaceableResources(results []searchResult) int {
	seen := map[searchSource]map[string]bool{}
	for _, result := range results {
		if !result.replaceable() {
			continue
		}
		bySource := seen[result.source]
		if bySource == nil {
			bySource = map[string]bool{}
			seen[result.source] = bySource
		}
		bySource[result.resource] = true
	}
	n := 0
	for _, resources := range seen {
		n += len(resources)
	}
	return n
}

// preserveCase copies the case of a match onto the replacement, as VS Code's
// Preserve Case does: an all-uppercase or all-lowercase match makes the whole
// replacement follow it, and a title-cased one takes a capital first letter.
// A mixed-case match leaves the replacement alone.
func preserveCase(match, replacement string) string {
	letters := 0
	upper, lower := 0, 0
	firstUpper := false
	for _, r := range match {
		if !unicode.IsLetter(r) {
			continue
		}
		if letters == 0 {
			firstUpper = unicode.IsUpper(r)
		}
		letters++
		if unicode.IsUpper(r) {
			upper++
		} else if unicode.IsLower(r) {
			lower++
		}
	}
	switch {
	case letters == 0:
		return replacement
	case upper == letters:
		return strings.ToUpper(replacement)
	case lower == letters:
		return strings.ToLower(replacement)
	case firstUpper && upper == 1 && lower == letters-1:
		runes := []rune(replacement)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
		}
		return string(runes)
	}
	return replacement
}

// applySearchReplace writes the checked changes of the preview, or just one
// row's change, through the same plan/copy/write/adopt path as a rename.
func (a *app) applySearchReplace(indices ...int) bool {
	s := &a.searchAll
	var changes []variableMatch
	if s.replace.open && s.replace.preview && len(indices) == 0 {
		changes = slices.Clone(s.replace.changes) // honour the preview's checkboxes
	} else {
		changes = a.searchChanges(indices...)
	}
	changes = slices.DeleteFunc(changes, func(m variableMatch) bool { return !m.Checked })
	if len(changes) == 0 {
		a.warn(locales.NoSearchChangesSelected())
		return false
	}
	fields, values, ok := a.planBulkChanges(changes, func(m variableMatch) string { return m.Replacement })
	if !ok {
		a.warn(locales.NoSearchChangesSelected())
		return false
	}
	state, err := copyVariableState(a)
	if err != nil {
		a.fail(locales.ReplaceAll(), err)
		return false
	}
	accepted := a.commitBulkEdits(fields, values, state, bulkCommitOptions{
		label: locales.ReplaceAll(),
		done:  locales.ReplacedMatches(appliedCount(fields, values)),
		value: applySearchValue,
		savedDraft: func(state *variableStateCopy, plan *plannedField) bool {
			return searchReplaceSavedCopy(state, plan)
		},
	})
	if accepted {
		s.replace.open, s.replace.preview, s.replace.confirmOpen = false, false, false
		s.replace.changes = nil
	}
	return accepted
}

// appliedCount is the number of changes that survived the stale-range checks.
// A stale match is skipped, never written at a shifted offset.
func appliedCount(fields []*plannedField, values []variableMatch) int {
	total := 0
	for _, plan := range fields {
		total += len(plan.uses)
	}
	return total + len(values)
}

// bulkValue reads the live value behind a map cell or collection row, so a
// change captured before an outside edit is detected and skipped.
func (a *app) bulkValue(m variableMatch) (string, bool) {
	switch m.Owner {
	case searchValueEnvInitial:
		for i := range a.envs {
			if a.envs[i].Name == m.Key {
				value, ok := a.envs[i].Vars[m.Code]
				return value, ok
			}
		}
	case searchValueEnvCurrent:
		for i := range a.envs {
			if a.envs[i].Name == m.Key {
				value, ok := a.envs[i].Current[m.Code]
				return value, ok
			}
		}
	case searchValueGlobalInitial:
		value, ok := a.globals[m.Code]
		return value, ok
	case searchValueGlobalCurrent:
		value, ok := a.globalsCurrent[m.Code]
		return value, ok
	case searchValueFolderDef:
		for _, f := range a.folders {
			if f.Path != m.Key {
				continue
			}
			if m.Index >= 0 && m.Index < len(f.Variables) {
				return f.Variables[m.Index].Name, true
			}
			for _, v := range f.Variables {
				if strings.TrimSpace(v.Name) == m.Text {
					return v.Name, true
				}
			}
		}
	case searchValueFolderCurrent:
		if values := a.folderCurrent[m.Key]; values != nil {
			value, ok := values[m.Code]
			return value, ok
		}
	}
	return "", false
}

// applySearchValue changes one copied value. folder Current keys follow a
// collection variable's renamed row, as they do in the variable editor.
func applySearchValue(state *variableStateCopy, m variableMatch, text string) (bool, bool) {
	switch m.Owner {
	case searchValueEnvInitial:
		for i := range state.envs {
			if state.envs[i].Name == m.Key {
				if _, ok := state.envs[i].Vars[m.Code]; !ok {
					return false, false
				}
				state.envs[i].Vars[m.Code] = text
				return false, true
			}
		}
	case searchValueEnvCurrent:
		for i := range state.envs {
			if state.envs[i].Name == m.Key {
				if _, ok := state.envs[i].Current[m.Code]; !ok {
					return false, false
				}
				state.envs[i].Current[m.Code] = text
				return false, true
			}
		}
	case searchValueGlobalInitial:
		if _, ok := state.globals[m.Code]; !ok {
			return false, false
		}
		state.globals[m.Code] = text
		return false, true
	case searchValueGlobalCurrent:
		if _, ok := state.globalsCurrent[m.Code]; !ok {
			return false, false
		}
		state.globalsCurrent[m.Code] = text
		return false, true
	case searchValueFolderDef:
		for i := range state.folders {
			if state.folders[i].Path != m.Key || m.Index < 0 || m.Index >= len(state.folders[i].Variables) {
				continue
			}
			v := &state.folders[i].Variables[m.Index]
			if strings.TrimSpace(v.Name) != m.Text {
				return false, false
			}
			before := v.Name
			v.Name = text
			state.renameFolderCurrent(m.Key, before, text)
			return true, true
		}
	case searchValueFolderCurrent:
		if values := state.folderCurrent[m.Key]; values != nil {
			if _, ok := values[m.Code]; !ok {
				return false, false
			}
			values[m.Code] = text
			return false, true
		}
	}
	return false, false
}

// searchReplaceSavedCopy writes a draft field's replacement into the saved
// request only when that field is unchanged on disk. A dirty field has no
// safe byte mapping for free text, so it stays in the editor's draft.
func searchReplaceSavedCopy(state *variableStateCopy, plan *plannedField) bool {
	saved := savedRequestForDoc(state.requests, plan.doc)
	if saved == nil {
		return false
	}
	code := savedFieldCodeForDocument(plan.code, saved)
	ref, ok := variableSavedField(saved, code)
	if !ok || ref.text == nil {
		return false
	}
	// Free-text search has no parsed reference to anchor a byte range in a
	// different saved text. As VS Code does for an unsaved editor, a dirty
	// field stays in the draft: only the editor buffer is changed, and Save
	// decides later what reaches the file. When the field is identical on
	// disk, the planned replacement applies to both.
	if plan.oldText != *ref.text || plan.oldText != ref.current() || plan.newText == plan.oldText {
		return false
	}
	return setSavedField(saved, code, plan.newText)
}

// searchPreviewModal is the shared bulk-edit dialog with Replace All in its
// footer. It is the same list and checkbox widget #44 uses for a rename.
func (a *app) searchPreviewModal(c *ui.Context, t *ui.Theme) {
	s := &a.searchAll.replace
	if !s.open || !s.preview {
		return // an open Replace field is not the bulk-edit preview
	}
	uikit.Modal(c, &s.open, locales.ReplaceAll(), func() {
		a.bulkPreviewDialog(c, t, &s.bulkPreviewState, locales.ReplaceAll(),
			locales.SearchReplaceCount(a.replaceableSearchCount()),
			nil,
			func(checked int) {
				uikit.DialogFooter(c).Children(func() {
					if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
						s.open = false
					}
					ui.Spacer(c)
					if uikit.PrimaryButton(c, locales.ReplaceAll()).Disabled(checked == 0).Clicked() {
						a.applySearchReplace()
					}
				})
			})
	})
}

// searchReplaceConfirm asks before Replace All writes. The same command
// button, the footer's icon and the palette all land here.
func (a *app) searchReplaceConfirm(c *ui.Context) {
	s := &a.searchAll.replace
	if !s.confirmOpen {
		return
	}
	result := uikit.ConfirmDialog(c, &s.confirmOpen, uikit.ConfirmOptions{
		Title:    locales.ReplaceAll(),
		Message:  locales.ReplaceSearchConfirm(fmt.Sprint(a.replaceableSearchCount()), fmt.Sprint(searchReplaceableResources(a.searchAll.results)), s.input),
		Severity: uikit.SeverityWarning,
		Primary:  locales.Replace(),
		Cancel:   locales.Cancel(),
	})
	if result == uikit.ConfirmPrimary {
		if !a.applySearchReplace() {
			s.confirmOpen = false
		}
	}
}

// replaceOneSearchMatch is a row's Replace command: one match, the same path
// and the same plan as Replace All.
func (a *app) replaceOneSearchMatch(at int) bool {
	s := &a.searchAll
	if at < 0 || at >= len(s.results) || !s.results[at].replaceable() {
		return false
	}
	return a.applySearchReplace(at)
}

// scrubSearchReplace drops queued changes when a new query replaces the
// result list they were built from. The Replace field itself stays open: the
// reader keeps it open while refining the query, as in VS Code.
func (a *app) scrubSearchReplace() {
	s := &a.searchAll.replace
	s.preview, s.confirmOpen, s.changes = false, false, nil
}

// dismissSearchMatch hides one result until the query or scope changes.
func (a *app) dismissSearchMatch(at int) {
	s := &a.searchAll
	if at < 0 || at >= len(s.results) {
		return
	}
	s.results = slices.Delete(s.results, at, at+1)
	if s.current >= len(s.results) {
		s.current = max(0, len(s.results)-1)
	}
}

// dismissSearchResource hides every result of one resource until the query
// or scope changes.
func (a *app) dismissSearchResource(source searchSource, resource string) {
	s := &a.searchAll
	s.results = slices.DeleteFunc(s.results, func(r searchResult) bool {
		return r.source == source && r.resource == resource
	})
	if s.current >= len(s.results) {
		s.current = max(0, len(s.results)-1)
	}
}
