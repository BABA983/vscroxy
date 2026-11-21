package app

import (
	"cmp"
	"reflect"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/locales"
)

// urlPasteEdit is the cURL command pasted into a request's URL field. The
// command fills the draft, and the notice's action puts the old draft back
// and inserts the command as text at the caret it was pasted over.
type urlPasteEdit struct {
	d        *requestDocument
	text     string
	before   savedRequest
	baseline savedRequest
	start    int
	end      int
	parsed   savedRequest
	warnings []string
	applied  bool
	// appliedValue and appliedBaseline are what the paste produced. Any later
	// edit, save or second paste makes the one-shot action stale.
	appliedValue    savedRequest
	appliedBaseline savedRequest
}

// looksLikeCurl is the paste provider's entry condition, before any parser
// runs. The parser still decides whether the text is a supported command.
func looksLikeCurl(text string) bool {
	trimmed := strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "curl ") || strings.HasPrefix(lower, "curl.exe ") ||
		strings.HasPrefix(lower, "invoke-webrequest") || strings.HasPrefix(lower, "invoke-restmethod") ||
		// Chrome's Copy as PowerShell puts its session setup before the call.
		strings.HasPrefix(lower, "$session") && strings.Contains(lower, "invoke-webrequest")
}

// pasteURLImport runs from the URL input's OnPaste hook, between frames. It
// keeps the edit for the next build, where the input can take the new draft
// without writing its old value back over it.
func (a *app) pasteURLImport(d *requestDocument, text string) bool {
	if !looksLikeCurl(text) {
		return false
	}
	parsed, warnings, err := parseCurlImport(text)
	if err != nil || parsed.URL == "" {
		// A command the parser cannot read pastes as plain text. Its error is
		// not shown or logged: it can quote the command and its credentials.
		return false
	}
	a.urlPaste = &urlPasteEdit{
		d: d, text: text, before: d.value(), baseline: d.baseline,
		start: a.urlPasteSelection[0], end: a.urlPasteSelection[1],
		parsed: parsed, warnings: warnings,
	}
	return true
}

// applyURLPaste fills the request with everything the command described,
// keeping the tab's name, collection and unsaved baseline.
func (a *app) applyURLPaste(d *requestDocument, edit *urlPasteEdit) {
	next := edit.before
	r := edit.parsed.Request
	next.Method = cmp.Or(r.Method, "GET")
	next.URL = r.URL
	next.PathVariables = slices.Clone(r.PathVariables)
	next.Headers = r.Headers
	next.HeaderList = slices.Clone(r.HeaderList)
	next.Body = r.Body
	next.BodyMode = r.BodyMode
	next.BodyFile = r.BodyFile
	next.Form = slices.Clone(r.Form)
	next.Auth = r.Auth
	next.Settings = r.Settings
	d.load(a, next, d.savedKey)
	d.baseline = edit.baseline
	d.urlInput.Focus()
	edit.applied = true
	edit.appliedValue, edit.appliedBaseline = d.value(), d.baseline
	a.notify(notice{
		level: noticeInfo, text: locales.CurlPasteFilledRequest(),
		detail: strings.Join(edit.warnings, "; "),
		action: locales.CurlPasteAsTextAction(),
		valid:  func() bool { return a.urlPasteActionValid(edit) },
		run: func() {
			if !a.urlPasteActionValid(edit) {
				a.pruneURLPaste()
				return
			}
			a.urlPasteUndo, a.urlPaste = edit, nil
			a.invalidate()
		},
	})
}

// restoreURLPaste is the "Paste as text" action: the command goes into the
// URL field where it was pasted, and no part of the old draft is kept from
// the import.
func (a *app) restoreURLPaste(d *requestDocument, edit *urlPasteEdit) {
	before := edit.before
	runes := []rune(before.URL)
	start, end := min(max(edit.start, 0), len(runes)), min(max(edit.end, 0), len(runes))
	if end < start {
		start, end = end, start
	}
	raw := strings.ReplaceAll(strings.ReplaceAll(edit.text, "\r\n", "\n"), "\n", " ")
	before.URL = string(runes[:start]) + raw + string(runes[end:])
	d.load(a, before, d.savedKey)
	d.baseline = edit.baseline
	d.urlInput.Focus()
}

// urlPasteDraftIntact says the work the paste produced is still the work on
// screen, unmoved by later edits, saves or tab closes.
func (a *app) urlPasteDraftIntact(edit *urlPasteEdit) bool {
	if edit == nil || !edit.applied || edit.d == nil {
		return false
	}
	d := edit.d
	open := slices.Contains(a.documents, d) || d == a.live || d == a.requestDocument
	if !open && a.flowDrafts != nil {
		for _, draft := range a.flowDrafts {
			if draft == d {
				open = true
				break
			}
		}
	}
	return open && reflect.DeepEqual(d.value(), edit.appliedValue) &&
		reflect.DeepEqual(d.baseline, edit.appliedBaseline)
}

func (a *app) urlPasteActionValid(edit *urlPasteEdit) bool {
	return a.urlPaste == edit && a.urlPasteDraftIntact(edit)
}

// pruneURLPaste drops an edit whose target changed out from under its action,
// so the notice center stops offering it even before the action is clicked.
func (a *app) pruneURLPaste() {
	if edit := a.urlPaste; edit != nil && edit.applied && !a.urlPasteDraftIntact(edit) {
		a.urlPaste = nil
	}
	if undo := a.urlPasteUndo; undo != nil && !a.urlPasteDraftIntact(undo) {
		a.urlPasteUndo = nil
	}
}

// takeURLPaste applies the edit once, on the build of the input that received
// it. Setting the draft from the hook itself would lose to the input's own
// value; handling both directions here keeps the native field in charge.
func (a *app) takeURLPaste(d *requestDocument) {
	if undo := a.urlPasteUndo; undo != nil && undo.d == d {
		if a.urlPasteDraftIntact(undo) {
			a.restoreURLPaste(d, undo)
		}
		a.urlPasteUndo = nil
	}
	edit := a.urlPaste
	if edit == nil || edit.d != d {
		return
	}
	if !edit.applied {
		a.applyURLPaste(d, edit)
	}
	a.urlPasteSelection = [2]int{}
}
