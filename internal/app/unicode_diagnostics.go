package app

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/internal/unicodechars"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

const unicodeDiagnosticSource = "unicode"

// unicodeHighlights is the app's flag set for editable and wire text. VS Code's
// confusable decision already follows allowedLocales and suppresses a look-alike
// inside a word made only of other non-ASCII characters; separate invisible
// pass keeps a zero-width or format character visible as a problem even inside
// such a word, where VS Code's shared word rule would otherwise hide it.
func (a *app) unicodeHighlights(text string) []unicodechars.Highlight {
	if text == "" || unicodechars.IsBasicASCII(text) {
		return nil
	}
	options := a.unicodeOptions()
	confusables := unicodechars.ComputeUnicodeHighlights(text, unicodechars.UnicodeHighlighterOptions{
		AmbiguousCharacters: true,
		AllowedLocales:      options.AllowedLocales,
	})
	invisibles := unicodechars.InvisibleCharacters{}
	var highlights []unicodechars.Highlight
	for index, codePoint := range text {
		if codePoint == ' ' || codePoint == '\t' || codePoint == '\n' || !invisibles.IsInvisibleCharacter(codePoint) {
			continue
		}
		highlights = append(highlights, unicodechars.Highlight{
			Start: index, End: index + utf8.RuneLen(codePoint),
			Kind: unicodechars.HighlightInvisible, CodePoint: codePoint,
		})
	}
	for _, highlight := range confusables {
		if !slices.ContainsFunc(highlights, func(flagged unicodechars.Highlight) bool {
			return flagged.Start == highlight.Start
		}) {
			highlights = append(highlights, highlight)
		}
	}
	slices.SortFunc(highlights, func(left, right unicodechars.Highlight) int { return left.Start - right.Start })
	return highlights
}

// unicodeCodePointLabel names the character the way a diagnostic should:
// its code point followed by its Unicode name, when Unicode has one.
func unicodeCodePointLabel(codePoint rune, kind unicodechars.HighlightKind) string {
	label := fmt.Sprintf("U+%04X", codePoint)
	if name := unicodechars.CodePointName(codePoint); name != "" {
		return label + " " + name
	}
	return label + " " + unicodeKindName(kind)
}

// unicodeDiagnostics describes the flagged characters in text. Offsets are
// byte offsets, as editor.Diagnostic and unicodechars.Highlight both use.
func (a *app) unicodeDiagnostics(text string) []editor.Diagnostic {
	highlights := a.unicodeHighlights(text)
	diagnostics := make([]editor.Diagnostic, 0, len(highlights))
	for _, highlight := range highlights {
		diagnostics = append(diagnostics, editor.Diagnostic{
			Start: highlight.Start, End: highlight.End, Severity: editor.SeverityWarning,
			Message: unicodeCodePointLabel(highlight.CodePoint, highlight.Kind), Source: unicodeDiagnosticSource,
		})
	}
	return diagnostics
}

// appendUnicodeDiagnostics adds character warnings to a state whose diagnostics
// another provider, such as the GraphQL validator, already owns.
func (a *app) appendUnicodeDiagnostics(state *editor.State, text string) {
	if state == nil {
		return
	}
	diagnostics := slices.DeleteFunc(state.Diagnostics(), func(d editor.Diagnostic) bool {
		return d.Source == unicodeDiagnosticSource
	})
	diagnostics = append(diagnostics, a.unicodeDiagnostics(text)...)
	state.SetDiagnostics(state.Buffer.Version(), diagnostics)
}

// unicodeInputMarks flags characters in a native text input: ranges underline
// them at their rune offsets, and a tip names them. A masked input gets the
// same code points and positions but never a reveal of the value.
func (a *app) unicodeInputMarks(c *ui.Context, in ui.Element, raw string, masked bool) {
	highlights := a.unicodeHighlights(raw)
	if len(highlights) == 0 || !in.Valid() {
		return
	}
	t := c.Theme()
	for _, highlight := range highlights[:min(len(highlights), 64)] {
		in.TextRanges(ui.TextRange{
			Start: utf8.RuneCountInString(raw[:highlight.Start]),
			End:   utf8.RuneCountInString(raw[:highlight.End]),
			Color: t.Warning, Weight: 600,
		})
	}
	a.unicodeTip(c, in, raw, highlights, masked)
}

// installUnicodeHover offers a fix on a diagnostic's hover. MyGo's editor has
// no code-action provider, but its hover card can carry the same one-click edit:
// remove an invisible character, or replace a confusable with its ASCII primary.
func (a *app) installUnicodeHover(state *editor.State) {
	if state == nil || state.RenderHover != nil {
		return
	}
	state.RenderHover = func(c *ui.Context, panel ui.Element, hover *editor.HoverCard) {
		editor.DefaultHover(c, panel, hover)
		for _, diagnostic := range hover.Diagnostics() {
			if diagnostic.Source != unicodeDiagnosticSource || diagnostic.Start >= diagnostic.End {
				continue
			}
			start, end := diagnostic.Start, diagnostic.End
			if end > state.Buffer.Len() {
				continue
			}
			codePoint, size := utf8.DecodeRuneInString(state.Buffer.Slice(start, end))
			if size != end-start {
				continue
			}
			replacement, label, ok := a.unicodeFix(codePoint)
			if !ok {
				continue
			}
			panel.Children(func() {
				if uikit.SecondaryButton(c, label).Clicked() {
					state.Buffer.Edit(editor.Edit{Start: start, End: end, Text: replacement})
					c.Invalidate()
				}
			})
		}
	}
}

func (a *app) unicodeFix(codePoint rune) (string, string, bool) {
	options := a.unicodeOptions()
	if codePoint != ' ' && codePoint != '\t' && codePoint != '\n' &&
		(unicodechars.InvisibleCharacters{}).IsInvisibleCharacter(codePoint) {
		return "", locales.UnicodeRemoveCharacter(), true
	}
	primary, ok := unicodechars.GetInstance(options.AllowedLocales).GetPrimaryConfusable(codePoint)
	if !ok {
		return "", "", false
	}
	return string(primary), locales.UnicodeReplaceWithAscii(), true
}

// unicodeField is one field of a draft a send warning can open. Name is shown
// in the warning; key names its focus handle, and tab is where the field lives.
type unicodeField struct {
	name string
	key  string
	tab  int
}

func (d *requestDocument) unicodeHandle(key string) *ui.Handle {
	if d.unicodeFocus == nil {
		d.unicodeFocus = make(map[string]*ui.Handle)
	}
	handle := d.unicodeFocus["compose:"+key]
	if handle == nil {
		handle = new(ui.Handle)
		d.unicodeFocus["compose:"+key] = handle
	}
	return handle
}

// openUnicodeField selects the tab and asks the field to take focus. A handle
// bound later than this call waits for its control, as MyGo's handles do.
func (a *app) openUnicodeField(d *requestDocument, field unicodeField) {
	if a.requestDocument != d || d == nil {
		return
	}
	if field.key == "" {
		return
	}
	if field.key == "url" {
		d.composeTab = field.tab
		d.urlInput.Focus()
		return
	}
	d.composeTab = field.tab
	key := field.key
	if d.headerBulk && strings.HasPrefix(key, "header-") {
		// Rows are not on screen in bulk mode; open the block instead.
		key = "header-bulk"
	}
	if handle := d.unicodeFocus["compose:"+key]; handle != nil {
		handle.Focus()
	}
}

// unicodeSendWarning is the one notice a send with flagged characters raises.
type unicodeSendWarning struct {
	field unicodeField
	label string
}

// warnUnicodeBeforeSend checks the expanded values of URL, headers, auth and
// body. It never blocks the send, never prints a value into the notice, and
// warns once even when several characters or fields are flagged.
func (a *app) warnUnicodeBeforeSend(r savedRequest, d *requestDocument, vars map[string]string) {
	warning := a.firstUnicodeSendWarning(r, vars)
	if warning == nil {
		return
	}
	target := *warning
	a.notify(notice{
		level:  noticeWarning,
		text:   locales.UnicodeSendWarning(target.field.name, target.label),
		action: locales.UnicodeOpenField(),
		run:    func() { a.openUnicodeField(d, target.field) },
		valid:  func() bool { return a.requestDocument == d },
	})
}

// unicodeSendField pairs the expanded value with the authored field it came
// from. Only the field's name and the flagged code point reach a notice.
type unicodeSendField struct {
	field unicodeField
	value string
}

// firstUnicodeSendWarning returns the first field with a flagged character,
// in the order the request is built. Body text is checked with the same rule
// as the editors: a word in a real non-ASCII language is fine, an invisible
// code point never is.
func (a *app) firstUnicodeSendWarning(r savedRequest, vars map[string]string) *unicodeSendWarning {
	for _, candidate := range a.unicodeSendFields(r, vars) {
		highlights := a.unicodeHighlights(candidate.value)
		if len(highlights) == 0 {
			continue
		}
		return &unicodeSendWarning{field: candidate.field,
			label: unicodeCodePointLabel(highlights[0].CodePoint, highlights[0].Kind)}
	}
	return nil
}

func (a *app) unicodeSendFields(r savedRequest, vars map[string]string) []unicodeSendField {
	expand := func(text string) string { return request.ExpandVars(text, vars) }
	fields := []unicodeSendField{{
		field: unicodeField{name: locales.Url(), key: "url", tab: composeTabHeaders},
		value: expand(request.FillPath(r.URL, r.PathVariables)),
	}}
	noun := locales.NounHeader()
	for i, row := range parseHeaderRows(r.Headers) {
		fields = append(fields,
			unicodeSendField{field: unicodeField{name: locales.KeyOfRow(noun, i+1),
				key: fmt.Sprintf("header-key-%d", i+1), tab: composeTabHeaders}, value: expand(row.Name)},
			unicodeSendField{field: unicodeField{name: locales.ValueOfRow(noun, i+1),
				key: fmt.Sprintf("header-value-%d", i+1), tab: composeTabHeaders}, value: expand(row.Value)},
		)
	}
	for _, auth := range unicodeAuthStrings(r.Auth) {
		fields = append(fields, unicodeSendField{
			field: unicodeField{name: locales.AuthorizationType() + " · " + auth.label,
				key: auth.key, tab: composeTabAuth},
			value: expand(auth.value),
		})
	}
	fields = append(fields, unicodeSendField{
		field: unicodeField{name: locales.Body(), key: "body", tab: composeTabBody},
		value: r.Body,
	})
	return fields
}
