package app

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/fuzzy"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// renameModal is VS Code's rename box followed by its bulk-edit preview. The
// preview is built from a snapshot of the library: unchecking a row is enough
// to leave that byte alone when Apply runs.
func (a *app) renameModal(c *ui.Context, t *ui.Theme) {
	s := &a.variableRename
	if !s.open {
		return
	}
	_, height := c.Size()
	uikit.Modal(c, &s.open, locales.RenameVariable(), func() {
		if !s.preview {
			uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
				uikit.DialogTitle(c, locales.RenameVariable())
				uikit.Meta(c, locales.RenameVariableIn(s.target.Name, s.target.Scope.label()))
				ui.Form(c, func() {
					uikit.FormRow(c, locales.NewVariableName(), "", s.problem, func() {
						uikit.Input(c, &s.input).Label(locales.NewVariableName()).FillWidth()
					})
				})
				uikit.Hint(c, locales.VariableScopeExplanation())
				uikit.DialogFooter(c).Children(func() {
					if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
						s.open = false
					}
					if uikit.PrimaryButton(c, locales.ReviewRename()).Clicked() {
						a.prepareVariableRename()
					}
				})
			})
			return
		}
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(620, height-80)).Children(func() {
			uikit.DialogTitle(c, locales.RenameVariable())
			uikit.Meta(c, locales.RenameVariableIn(s.target.Name, s.target.Scope.label()))
			if s.warning != "" {
				uikit.ErrorText(c, s.warning)
			}
			if s.problem != "" {
				uikit.ErrorText(c, s.problem)
			}
			uikit.Hint(c, locales.VariableScopeExplanation())
			if s.target.Scope.Kind == "Environment" {
				uikit.Hint(c, locales.OtherEnvironmentDefinitions())
			}
			checked := 0
			for i := range s.changes {
				if s.changes[i].Checked {
					checked++
				}
			}
			uikit.Caption(c, locales.RenameChangeCount(checked, len(s.changes)))
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
				a.variableChangeList(c, t, s)
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Back()).Clicked() {
					s.preview, s.problem, s.warning = false, "", ""
				}
				ui.Spacer(c)
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					s.open = false
				}
				if uikit.PrimaryButton(c, locales.ApplyRename()).Disabled(s.warning != "" || checked == 0).Clicked() {
					a.applyVariableRename()
				}
			})
		})
	})
}

func (a *app) variableChangeList(c *ui.Context, t *ui.Theme, s *variableRenameState) {
	resource, field := "", ""
	for i := range s.changes {
		m := &s.changes[i]
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
		row := ui.Row(c.Key(fmt.Sprintf("rename-change-%d", i))).Height(uikit.HeightRow).FillWidth().
			Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Padding(0, t.Space(uikit.SpaceM))
		if row.Hovered() {
			row.Background(t.SurfaceHover)
		}
		row.Children(func() {
			uikit.Checkbox(c, &m.Checked, m.location()).Shrink(0)
			ui.Row(c).Width(48).Shrink(0).Justify(ui.End).Children(func() {
				if m.Line > 0 {
					uikit.Meta(c, strconv.Itoa(m.Line)).FontFeatures("tnum")
				}
			})
			variableMatchLine(c, *m).Grow(1).MinWidth(0)
			uikit.Meta(c, m.ScopeLabel).Width(160).SingleLine().Ellipsis("…").Shrink(0)
		})
	}
}

// referencesModal is the References view: one group per request or scope,
// one group per field within it, with the matching line shown in place.
func (a *app) referencesModal(c *ui.Context, t *ui.Theme) {
	s := &a.variableRefs
	if !s.open {
		return
	}
	_, height := c.Size()
	uikit.Modal(c, &s.open, locales.FindAllReferences(), func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(620, height-80)).Children(func() {
			uikit.DialogTitle(c, locales.FindAllReferences())
			uikit.Meta(c, locales.ReferencesOf(s.target.Name, s.target.Scope.label()))
			uikit.Hint(c, locales.VariableScopeExplanation())
			uikit.Caption(c, locales.ReferenceCount(len(s.matches)))
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
				if len(s.matches) == 0 {
					uikit.Meta(c, locales.NoVariableUses())
					return
				}
				resource, field := "", ""
				for i := range s.matches {
					m := s.matches[i]
					if m.Resource != resource {
						resource, field = m.Resource, ""
						ui.Row(c.Key("ref-resource-" + resource)).Height(uikit.HeightRow).FillWidth().AlignItems(ui.Center).Children(func() {
							uikit.BodyStrong(c, resource).Grow(1).MinWidth(0).SingleLine()
						})
					}
					if m.Field != field {
						field = m.Field
						ui.Row(c.Key(fmt.Sprintf("ref-field-%s-%s", resource, field))).Height(uikit.HeightControlSmall).FillWidth().Children(func() {
							uikit.Meta(c, field).SingleLine()
						})
					}
					a.variableReferenceRow(c, t, i, m)
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Close()).Clicked() {
					s.open = false
				}
			})
		})
	})
}

func (a *app) variableReferenceRow(c *ui.Context, t *ui.Theme, i int, m variableMatch) {
	open := func() { a.openVariableMatch(m) }
	row := ui.Row(c.Key(fmt.Sprintf("variable-reference-%d", i))).Height(uikit.HeightRow).
		FillWidth().Gap(t.Space(uikit.SpaceM)).Padding(0, t.Space(uikit.SpaceM)).
		AlignItems(ui.Center).Role(ui.RoleButton)
	if i == a.variableRefs.current {
		row.Background(t.SurfaceHover)
	} else if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.OnClick(open)
	row.Children(func() {
		ui.Row(c).Width(40).Shrink(0).Justify(ui.End).Children(func() {
			if m.Line > 0 {
				uikit.Meta(c, strconv.Itoa(m.Line)).FontFeatures("tnum")
			}
		})
		ui.Box(c).Grow(1).MinWidth(0).Children(func() { variableMatchLine(c, m) })
		uikit.Meta(c, m.ScopeLabel).Width(160).SingleLine().Ellipsis("…").Shrink(0)
	})
}

// variableMatchLine draws the matching line around the variable, with the
// name highlighted as a Search result highlights its match. Two matches on a
// long script line show different windows instead of the same clipped head.
func variableMatchLine(c *ui.Context, m variableMatch) ui.Element {
	size := uikit.Scaled(uikit.TextData)
	label := locales.OpenVariableReference(m.Field)
	if m.Kind == "definition" || m.LineText == "" {
		return ui.Row(c).Grow(1).MinWidth(0).Children(func() {
			uikit.DataMeta(c, "{{"+m.Text+"}}").Font(uikit.FontMono).FontSize(size).SingleLine()
		})
	}
	lineStart := strings.LastIndex(m.Text[:min(m.Start, len(m.Text))], "\n") + 1
	start := utf8.RuneCountInString(m.Text[lineStart:m.Start])
	end := start + utf8.RuneCountInString(m.Text[m.Start:m.End])
	runes := []rune(m.LineText)
	from := max(0, start-24)
	to := min(len(runes), end+24)
	spans := make([]ui.Span, 0, 4)
	if from > 0 {
		spans = append(spans, ui.Span{Text: "…", Size: size, Color: uikit.Colors(c.Theme()).Faint})
	}
	spans = append(spans, paletteSpans(string(runes[from:to]), []fuzzy.Match{{Start: start - from, End: end - from}},
		paletteHighlightInk(c, true), size)...)
	if to < len(runes) {
		spans = append(spans, ui.Span{Text: "…", Size: size, Color: uikit.Colors(c.Theme()).Faint})
	}
	return ui.RichText(c, spans...).Font(uikit.FontMono).FontSize(size).
		Label(label).Grow(1).MinWidth(0).SingleLine()
}

// revealVariableMatch asks the editor built for a click to select the range.
// It runs at the end of the frame, after a tab switch has built its editors.
func (a *app) revealVariableMatch(c *ui.Context) {
	r := a.variableRefs.reveal
	if r.owner == "" {
		return
	}
	switch r.owner {
	case "saved":
		d := a.requestDocument
		if d == nil || d.savedKey != r.key {
			return
		}
		d.composeTab = variableTabForCode(r.code)
		if r.code == problemFieldPost {
			d.scriptTab = 1
		} else if r.code == problemFieldPre {
			d.scriptTab = 0
		}
		if state := variableEditorFor(d, r.code); state != nil {
			if state.Buffer.String() != "" {
				state.Select(min(r.start, state.Buffer.Len()), min(r.end, state.Buffer.Len()))
				d.unicodeHandle(r.code).Focus(c)
			}
		} else if handle := variableInputHandle(d, r.code); handle != nil && c.Resolve(*handle).Valid() {
			text := variableInputText(d, r.code)
			from := utf8.RuneCountInString(text[:min(r.start, len(text))])
			to := utf8.RuneCountInString(text[:min(r.end, len(text))])
			c.Resolve(*handle).SetTextSelection(from, to).Focus()
		} else {
			return // the field has not been built yet
		}
	case "folder":
		if a.folderUI.draft.Path != r.key {
			return
		}
		if r.code == "row" {
			a.folderUI.tab = 2
		} else {
			a.folderUI.tab = variableFolderTabForCode(r.code)
		}
	case "env-initial", "env-current", "global-initial", "global-current":
		a.varsGlobals = strings.HasPrefix(r.owner, "global")
	}
	a.variableRefs.reveal = variableReveal{}
	a.invalidate()
}

func variableTabForCode(code string) int {
	switch {
	case code == problemFieldBody, code == "graphql-query", code == "graphql-vars",
		code == "body-file", strings.HasPrefix(code, "form-"):
		return composeTabBody
	case code == problemFieldPre, code == problemFieldPost:
		return composeTabScripts
	case code == problemFieldHeaderRaw || strings.HasPrefix(code, "header-"):
		return composeTabHeaders
	case strings.HasPrefix(code, "auth:"):
		return composeTabAuth
	case code == problemFieldDescription:
		return composeTabDocs
	case strings.HasPrefix(code, "path-"), code == problemFieldURL:
		return composeTabParams
	default:
		return composeTabHeaders
	}
}

func variableFolderTabForCode(code string) int {
	switch {
	case code == problemFieldDescription:
		return 3
	case code == problemFieldPre, code == problemFieldPost:
		return 1
	default:
		return 0
	}
}

func variableEditorFor(d *requestDocument, code string) *editor.State {
	switch code {
	case problemFieldBody:
		return d.bodyEditor
	case problemFieldPre:
		return d.preEditor
	case problemFieldPost:
		return d.postEditor
	case problemFieldHeaderRaw:
		return d.bulkEditor
	}
	return nil
}

func variableInputHandle(d *requestDocument, code string) *ui.Handle {
	switch code {
	case problemFieldURL:
		return &d.urlInput
	default:
		return d.unicodeHandle(code)
	}
}

func variableInputText(d *requestDocument, code string) string {
	switch code {
	case problemFieldURL:
		return d.composeURL
	case problemFieldDescription:
		return d.description
	case problemFieldBody:
		return d.composeBody
	case problemFieldPre:
		return d.composePre
	case problemFieldPost:
		return d.composePost
	case problemFieldHeaderRaw:
		return d.composeHeaders
	}
	if _, text, _, _, _, ok := d.quickFixField(code); ok {
		return *text
	}
	return ""
}
