package app

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// variableCompletion is the one {{ completion open in the window. Only the
// focused field can own it, so one state serves every input.
type variableCompletion struct {
	field     string
	open      bool
	selected  int
	start     int // byte offset of "{{"
	caret     int // byte offset after the typed prefix
	dismissed string
}

// variableInput is uikit.Input with {{}} highlighting, a hover preview and
// completion. key names the field for the one open completion.
func (a *app) variableInput(c *ui.Context, key string, value *string, label, collection string) ui.Element {
	in := uikit.InputControl(c, value).Label(label)
	a.attachVariables(c, in, key, value, label, collection, false)
	return uikit.FieldFace(c, in, in.Focused())
}

// variablePasswordInput is variableInput for a field that hides its text. The
// hover preview masks every value, not only credential-like names.
func (a *app) variablePasswordInput(c *ui.Context, key string, value *string, label, collection string) ui.Element {
	in := uikit.InputControl(c, value).Label(label).Password()
	a.attachVariables(c, in, key, value, label, collection, true)
	return uikit.FieldFace(c, in, in.Focused())
}

// variableTextArea is ui.TextArea with the same variable help.
func (a *app) variableTextArea(c *ui.Context, key string, value *string, label, collection string) ui.Element {
	in := uikit.TextArea(c, value).Label(label)
	a.attachVariables(c, in, key, value, label, collection, false)
	return in
}

// variableSensitiveTextArea is variableTextArea whose hover masks values, for
// private keys and similar credentials.
func (a *app) variableSensitiveTextArea(c *ui.Context, key string, value *string, label, collection string) ui.Element {
	in := uikit.TextArea(c, value).Label(label)
	a.attachVariables(c, in, key, value, label, collection, true)
	return in
}

// variableGridInput is requestGridInput with the same variable help, for the
// Params, Headers and form tables. mask hides every hover value.
func (a *app) variableGridInput(c *ui.Context, key string, value *string, placeholder, label, collection string, mask, mono bool) ui.Element {
	in := uikit.InputControl(c, value).Label(label).Placeholder(placeholder).FillWidth().MinWidth(0).
		FillHeight().Padding(0, c.Theme().Space(uikit.SpaceM)).
		Border(1, ui.Color{})
	if mono {
		in.Font(uikit.FontMono)
	}
	a.attachVariables(c, in, key, value, label, collection, mask)
	return in
}

// attachVariables paints defined references in the accent color and undefined
// ones in danger, then adds the hover preview and completion. It builds the
// hint set once for the field.
func (a *app) attachVariables(c *ui.Context, in ui.Element, key string, value *string, label, collection string, mask bool) {
	handle := a.variableHandle(value)
	in.Bind(handle)
	a.bindQuickFixShortcut(c, handle)
	var set variableSet
	if strings.Contains(*value, "{{") {
		set = a.variableHintSet(collection)
		t := c.Theme()
		marks := variableMarksWith(set, *value)
		for _, mark := range marks {
			color := t.Accent
			if !mark.Defined {
				color = t.Danger
			}
			in.TextRanges(ui.TextRange{Start: mark.Start, End: mark.End, Color: color, Weight: 500})
		}
		a.variableTooltip(c, in, marks, mask, set, collection)
	}
	// Invisible and look-alike characters get warning ranges and a tip at
	// their own offsets, beside the variable styling. Masking still applies:
	// the tip names code points, never the value that contained them.
	a.unicodeInputMarks(c, in, *value, mask)
	a.syncVariableCompletion(in, key, value)
	a.variablePopover(c, in, key, value, set, nil)
	a.trackInputQuickFix(a.requestDocument, quickFixFieldCode(key), value, in)
	a.quickFixContextMenu(c, in, value)
}

// variableTooltip lists the field's references, each with its resolved value
// and scope. Text inputs have no per-run hover in MyGo, so the preview covers
// the field rather than the rune under the pointer; editor hovers are exact.
// mask hides every value, for a password or private-key field. An unresolved
// name also carries the quick fixes offered by the Problems panel.
func (a *app) variableTooltip(c *ui.Context, anchor ui.Element, marks []variableMark, mask bool, set variableSet, collection string) {
	if len(marks) == 0 || a.problems.fixMenuOpen {
		return // a code-action menu must not sit behind a value preview
	}
	t := c.Theme()
	ui.TooltipBase(c, anchor, func(tip ui.Element) {
		uikit.TipSurface(c, tip)
		tip.Width(360).Children(func() {
			for i, mark := range marks {
				if i > 0 {
					ui.Box(c).Height(1).FillWidth().Background(uikit.Colors(t).Border)
				}
				ui.Row(c).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Children(func() {
					uikit.DataMeta(c, "{{"+mark.Ref.Name+"}}").TextColor(t.Text)
					if mark.Defined {
						uikit.Meta(c, mark.Hint.source()).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
					} else {
						uikit.ErrorText(c, locales.VariableUndefined()).Grow(1)
					}
				})
				value := locales.StaysLiteralWhenSent()
				if mark.Defined {
					if mask {
						value = "••••••"
					} else {
						value = mark.Hint.display()
					}
				}
				uikit.DataMeta(c, value).TextColor(t.Text).SingleLine().Ellipsis("…")
				if !mark.Defined {
					proposals := a.variableFixProposals(set, collection, mark.Ref.Name)
					if len(proposals) > 0 {
						uikit.Meta(c, locales.VariableQuickFixesTitle()).TextColor(t.Text).FontWeight(uikit.WeightMedium)
						for _, proposal := range proposals {
							uikit.Meta(c, proposal.label).TextColor(t.TextMuted)
						}
					}
				}
			}
		})
	})
}

// variableCompletionToken names one unterminated {{token in one field, so an
// Escape can dismiss it without closing completions in other fields.
func variableCompletionToken(field string, start int) string {
	return fmt.Sprintf("%s\x00%d", field, start)
}

// syncVariableCompletion reads the caret and opens or closes the completion
// for this field. It also restores the caret after a completion inserted.
func (a *app) syncVariableCompletion(in ui.Element, key string, value *string) {
	if !in.Focused() {
		// Views build more than once per frame; a pass without the focus must
		// not close a completion the focused pass opened.
		return
	}
	caretRune, _ := in.TextSelection()
	caret := runeToByte(*value, caretRune)
	start, _, ok := openVariableToken(*value, caret)
	if !ok {
		if caret == 0 && strings.HasPrefix(*value, "{{") && a.varsComplete.field == key && a.varsComplete.open {
			// A measurement pass can report the start of the text before the
			// editor's selection is set; keep the completion the focused pass
			// opened. Escape and outside presses still close it.
			return
		}
		if a.varsComplete.field == key {
			a.varsComplete.open, a.varsComplete.dismissed = false, ""
		}
		return
	}
	// Escape dismisses the whole {{token session, not just the prefix typed
	// when it was pressed: continuing to type must not bring the list back.
	token := variableCompletionToken(key, start)
	if a.varsComplete.dismissed == token {
		return
	}
	if a.varsComplete.field != key || a.varsComplete.start != start || a.varsComplete.caret != caret {
		a.varsComplete.selected = 0
	}
	a.varsComplete.field, a.varsComplete.start, a.varsComplete.caret = key, start, caret
	a.varsComplete.open = true
}

// variablePopover shows the filtered variables below the field and handles
// Up/Down/Enter/Tab/Escape while the field keeps the keyboard. next receives
// the keys the completion does not own, so a field can also have history.
func (a *app) variablePopover(c *ui.Context, anchor ui.Element, key string, value *string, set variableSet, next func(ui.TextKey) bool) {
	anchor.OnTextKey(next)
	if a.varsComplete.field != key || !a.varsComplete.open {
		return
	}
	start, prefix, ok := openVariableToken(*value, a.varsComplete.caret)
	if !ok || start != a.varsComplete.start {
		return
	}
	items := rankVariableHints(prefix, set.list, variableCompletionLimit)
	if len(items) == 0 {
		return
	}
	if a.varsComplete.selected >= len(items) {
		a.varsComplete.selected = len(items) - 1
	}
	t := c.Theme()
	token := variableCompletionToken(key, start)
	anchor.OnTextKey(func(k ui.TextKey) bool {
		if !a.varsComplete.open || a.varsComplete.field != key {
			return false
		}
		switch k.Key {
		case ui.KeyUp:
			a.varsComplete.selected = (a.varsComplete.selected - 1 + len(items)) % len(items)
			return true
		case ui.KeyDown:
			a.varsComplete.selected = (a.varsComplete.selected + 1) % len(items)
			return true
		case ui.KeyEnter, ui.KeyTab:
			if a.acceptVariableKey(anchor, k.Text(), items[a.varsComplete.selected].Name) {
				return true
			}
		case ui.KeyEscape:
			a.varsComplete.open, a.varsComplete.dismissed = false, token
			return true
		}
		// A key the completion does not use belongs to the field, such as
		// Up/Down walking the request URL history once {{ is closed.
		return next != nil && next(k)
	})
	panel := ui.PopoverBase(c, anchor, &a.varsComplete.open, func(panel ui.Element) {
		uikit.PopoverPanel(c, panel).Width(320).Padding(t.Space(uikit.SpaceXS))
		ui.Scroll(c).MaxHeight(300).Children(func() {
			for i, hint := range items {
				item := uikit.RowButton(c, locales.InsertVariable(hint.Name)).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center)
				fg, muted := t.Text, t.TextMuted
				selected := i == a.varsComplete.selected
				if selected {
					fg, muted = uikit.Colors(t).PrimaryForeground, uikit.Colors(t).PrimaryForeground
					item.Background(uikit.Colors(t).Primary)
				} else if item.Hovered() {
					item.Background(t.SurfaceHover)
				}
				item.Children(func() {
					uikit.DataBody(c, hint.Name).TextColor(fg)
					uikit.Meta(c, hint.source()).TextColor(muted).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				})
				if item.Clicked() {
					a.acceptVariableCompletion(anchor, value, hint.Name)
				}
			}
		})
	})
	if panel.Dismissed() {
		a.varsComplete.open, a.varsComplete.dismissed = false, token
	}
}

// acceptVariableCompletion replaces the open {{prefix with {{name}}, as an
// edit of its own: Undo brings back what the user typed before it, and the caret
// lands after the reference without a frame putting it there.
func (a *app) acceptVariableCompletion(field ui.Element, value *string, name string) {
	if !a.replaceVariableToken(field, *value, name) {
		return
	}
	a.varsComplete.open, a.varsComplete.dismissed = false, ""
}

// acceptVariableKey does the same for a key, whose text is ahead of the bound
// value; the caret is the last one the field reported.
func (a *app) acceptVariableKey(field ui.Element, text, name string) bool {
	if !a.replaceVariableToken(field, text, name) {
		return false
	}
	a.varsComplete.open, a.varsComplete.dismissed = false, ""
	return true
}

// replaceVariableToken replaces the unterminated {{prefix ending at the caret
// with {{name}}, keeping a closing brace pair the text already has.
func (a *app) replaceVariableToken(field ui.Element, text, name string) bool {
	caret, _ := field.TextSelection()
	start, end, ok := variableTokenRunes(text, caret)
	if !ok {
		return false
	}
	field.ReplaceText(start, end, "{{"+name+"}}")
	return true
}

// variableTokenRunes is the rune range of the {{prefix ending at caret, which
// reaches past the caret when the text closes the reference already.
func variableTokenRunes(text string, caret int) (start, end int, ok bool) {
	startByte, _, ok := openVariableToken(text, runeToByte(text, caret))
	if !ok {
		return 0, 0, false
	}
	start = utf8.RuneCountInString(text[:startByte])
	end = caret
	if strings.HasPrefix(text[runeToByte(text, caret):], "}}") {
		end += 2
	}
	return start, end, true
}

// replaceVariableToken replaces the unterminated {{prefix ending at caret with
// {{name}}, keeping an existing closing brace pair. after is the rune offset
// just past the inserted reference.
func replaceVariableToken(text, name string, caret int) (string, int, bool) {
	start, _, ok := openVariableToken(text, caret)
	if !ok {
		return "", 0, false
	}
	insert := "{{" + name
	closed := strings.HasPrefix(text[caret:], "}}")
	if !closed {
		insert += "}}"
	}
	after := utf8.RuneCountInString(text[:start]) + utf8.RuneCountInString(insert)
	if closed {
		after += 2 // the closing braces already in the text follow the caret
	}
	return text[:start] + insert + text[caret:], after, true
}

// openVariableToken finds an unterminated {{prefix that ends at caret.
func openVariableToken(text string, caret int) (start int, prefix string, ok bool) {
	if caret < 0 || caret > len(text) {
		return 0, "", false
	}
	before := text[:caret]
	start = strings.LastIndex(before, "{{")
	if start < 0 {
		return 0, "", false
	}
	prefix = before[start+2:]
	if strings.Contains(prefix, "}}") || strings.ContainsAny(prefix, "{}") || strings.ContainsAny(prefix, "\r\n") {
		return 0, "", false
	}
	return start, prefix, true
}

// runeToByte converts a rune offset into a byte offset.
func runeToByte(text string, runes int) int {
	if runes <= 0 {
		return 0
	}
	count := 0
	for i := range text {
		if count == runes {
			return i
		}
		count++
	}
	return len(text)
}

// wireVariableEditor gives a code editor completions and exact-token hovers.
// In script editors the name inside pm.environment.get("…") is a variable
// too, so both reference forms share the one resolver and the same hover.
func (a *app) wireVariableEditor(state *editor.State, collection string) {
	a.installUnicodeHover(state)
	state.OnCompletion = func(r editor.CompletionRequest) {
		text := state.Buffer.String()
		if start, prefix, ok := openVariableToken(text, r.Offset); ok {
			suffix := ""
			if !strings.HasPrefix(text[r.Offset:], "}}") {
				suffix = "}}"
			}
			state.ShowCompletions(start+2, a.variableCompletionItems(collection, prefix, suffix))
			return
		}
		if ref, ok := scriptVariableAt(text, r.Offset); ok {
			// The literal can still be empty, and a caret in the middle of
			// a name must rank what was typed before it, not the whole name.
			prefix := text[ref.Start:min(r.Offset, ref.End)]
			state.ShowCompletions(ref.Start, a.variableCompletionItems(collection, prefix, ""))
		}
	}
	state.OnHover = func(r editor.HoverRequest) {
		text := state.Buffer.String()
		if ref, ok := request.VariableAt(text, r.Offset); ok {
			state.ShowHover(ref.Start, ref.End, a.variablePreview(collection, ref.Name), nil)
			return
		}
		if ref, ok := scriptVariableAt(text, r.Offset); ok && ref.Start < ref.End {
			state.ShowHover(ref.Start, ref.End, a.variablePreview(collection, ref.Name), nil)
		}
	}
}

// setVariableDiagnostics underlines undefined references and flagged
// characters in a code editor, so both problems show in one diagnostic set.
func (a *app) setVariableDiagnostics(state *editor.State, text, collection string) {
	var diagnostics []editor.Diagnostic
	if strings.Contains(text, "{{") {
		set := a.variableHintSet(collection)
		for _, ref := range request.VariableRefs(text) {
			if _, ok := set.hint(ref.Name); !ok {
				diagnostics = append(diagnostics, editor.Diagnostic{
					Start: ref.Start, End: ref.End, Severity: editor.SeverityError,
					Message: "Undefined variable: " + ref.Name, Source: "variables",
				})
			}
		}
	}
	diagnostics = append(diagnostics, a.cachedUnicodeDiagnostics(state, text)...)
	state.SetDiagnostics(state.Buffer.Version(), diagnostics)
}
