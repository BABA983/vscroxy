package uikit

import (
	"runtime"

	"github.com/baba983/vscroxy/internal/fuzzy"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// QuickPickItem is one row of a QuickPick. Label is what the row shows; Search
// is extra text fuzzy matching reads without showing, such as a request's URL;
// Detail is the muted fact at the row's end, such as its collection.
type QuickPickItem struct {
	ID       string
	Label    string
	Search   string
	Detail   string
	Method   string
	Icon     *ui.SVG
	Dirty    bool
	Closable bool

	// matches are the label's fuzzy runs, filled by the picker.
	matches []fuzzy.Match
}

// QuickPickGroup is one titled run of rows. Groups keep the caller's order,
// as the open tabs keep their recency order.
type QuickPickGroup struct {
	Title string
	Items []QuickPickItem
}

// QuickPickState is a picker's query and highlighted row. Keep it in the app
// so the query survives the frames while the popover is open.
type QuickPickState struct {
	Query    string
	Selected int
}

// QuickPickOptions draws a quick pick under anchor: a focused search field,
// grouped rows with fuzzy highlights, and the keys that move, open and close
// them. The caller supplies the rows; matching, drawing and keys live here so
// every searchable list behaves alike.
type QuickPickOptions struct {
	Open        *bool
	State       *QuickPickState
	Groups      func() []QuickPickGroup
	Placeholder string
	Empty       string
	Width       float32
	OnActivate  func(id string)
	OnClose     func(id string)
}

// QuickPick draws the popover. anchor.Clicked toggles it; the command that
// opens a picker from the palette sets Open itself.
func QuickPick(c *ui.Context, anchor ui.Element, o QuickPickOptions) {
	s := o.State
	if s == nil {
		s = &QuickPickState{}
	}
	t, p := c.Theme(), Colors(c.Theme())
	width := o.Width
	if width == 0 {
		width = 320
	}
	if anchor.Clicked() {
		*o.Open = !*o.Open
		s.Query, s.Selected = "", 0
	}
	groups := quickPickMatches(o.Groups(), s.Query, &s.Selected)
	ui.PopoverBase(c, anchor, o.Open, func(panel ui.Element) {
		MenuPanel(c, panel).Width(width).Padding(t.Space(SpaceS)).Gap(t.Space(SpaceXS))
		field, input := quickPickField(c, &s.Query, o.Placeholder)
		field.FillWidth()
		if input.Changed() {
			s.Selected = 0
		}
		// The picker owns these keys while it is open; the field keeps the
		// focus, so typing continues after a move.
		c.OnShortcut(0, ui.KeyDown, func() { quickPickStep(groups, &s.Selected, 1) })
		c.OnShortcut(0, ui.KeyUp, func() { quickPickStep(groups, &s.Selected, -1) })
		input.OnTextKey(func(k ui.TextKey) bool {
			if k.Mods != ui.Cmd || k.Key != ui.KeyBackspace {
				return false
			}
			if item, ok := quickPickSelected(groups, s.Selected); ok && item.Closable && o.OnClose != nil {
				o.OnClose(item.ID)
			}
			return true
		})
		var selected ui.Element
		if quickPickCount(groups) == 0 {
			ui.Row(c).Padding(t.Space(SpaceS), t.Space(SpaceM)).Children(func() {
				Hint(c, o.Empty)
			})
		} else {
			ui.Scroll(c).MaxHeight(340).Grow(1).Children(func() {
				index := 0
				for _, group := range groups {
					if group.Title != "" {
						ui.Row(c).Height(22).Padding(0, t.Space(SpaceS)).AlignItems(ui.Center).Children(func() {
							Meta(c, group.Title).SingleLine()
						})
					}
					for _, item := range group.Items {
						at := index
						row := quickPickRow(c, item, at == s.Selected)
						if at == s.Selected {
							selected = row
						}
						if item.Closable && o.OnClose != nil && (row.Hovered() || at == s.Selected) {
							row.Children(func() {
								close := RowIconButton(c, locales.CloseTabNamed(item.Label), locales.Close(), closeIcon)
								close.Size(16, 16).Shrink(0)
								if close.Clicked() {
									o.OnClose(item.ID)
								}
							})
						}
						if row.Clicked() {
							s.Selected = at
							if o.OnActivate != nil {
								*o.Open = false
								o.OnActivate(item.ID)
							}
						}
						index++
					}
				}
			})
		}
		if input.Submitted() {
			if item, ok := quickPickSelected(groups, s.Selected); ok && o.OnActivate != nil {
				*o.Open = false
				o.OnActivate(item.ID)
			}
		}
		if selected.Valid() {
			selected.ScrollIntoView()
		}
		ui.Box(c).FillWidth().Height(HairlineWidth).Background(p.Border)
		ui.Row(c).Padding(t.Space(SpaceXS), t.Space(SpaceS)).Gap(t.Space(SpaceS)).AlignItems(ui.Center).Children(func() {
			Kbd(c, "↑↓")
			Small(c, locales.SelectHint())
			Kbd(c, "↵")
			Small(c, locales.Open())
			Kbd(c, "esc")
			Small(c, locales.Close())
			closeKeys := "⌘⌫"
			if runtime.GOOS != "darwin" {
				closeKeys = "Ctrl+⌫"
			}
			Kbd(c, closeKeys)
			Small(c, locales.CloseTab())
		})
	})
}

// quickPickField is the picker's search field: the field face without a
// separate box inside the popover's own.
func quickPickField(c *ui.Context, value *string, placeholder string) (ui.Element, ui.Element) {
	t := c.Theme()
	row := ui.Row(c).Height(HeightControl).AlignItems(ui.Center).Gap(t.Space(SpaceXS)).
		Padding(0, t.Space(SpaceXS)).Radius(RadiusControl)
	var input ui.Element
	row.Children(func() {
		ui.Icon(c, lucide.Search()).Size(IconInline, IconInline).TextColor(t.TextMuted).Shrink(0)
		input = ui.TextInputBase(c, value).Height(HeightControl).FontSize(Scaled(TextSmall)).
			Padding(0, t.Space(SpaceS)).Grow(1).MinWidth(0).FocusRing(false).
			Label(locales.SearchTabs()).Placeholder(placeholder)
		input.AutoFocus()
	})
	return FieldFace(c, row, input.Focused()), input
}

// quickPickRow draws one item: its method or icon, highlighted label, dirty
// mark and muted detail, over the selected wash.
func quickPickRow(c *ui.Context, item QuickPickItem, selected bool) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	row := ui.Row(c).Height(HeightRow).Padding(0, t.Space(SpaceS)).Gap(t.Space(SpaceS)).
		Radius(RadiusControl).AlignItems(ui.Center)
	if selected {
		row.Background(p.Muted)
	} else if row.Hovered() {
		row.Background(p.MutedHover)
	}
	row.Children(func() {
		if item.Method != "" {
			MethodLabel(c, item.Method).FontSize(Scaled(TextMeta)).Shrink(0)
		} else if item.Icon != nil {
			ui.Icon(c, item.Icon).Size(IconInline, IconInline).TextColor(t.TextMuted).Shrink(0)
		}
		label := quickPickLabel(c, item)
		label.Grow(1).MinWidth(0).SingleLine()
		if item.Dirty {
			ui.Box(c).Size(6, 6).Radius(3).Background(t.Text).Shrink(0)
		}
		if item.Detail != "" {
			Meta(c, item.Detail).SingleLine().Shrink(0).MaxWidth(132)
		}
	})
	return row
}

// quickPickLabel draws the label with the fuzzy runs in the primary ink.
func quickPickLabel(c *ui.Context, item QuickPickItem) ui.Element {
	if len(item.matches) == 0 {
		return Body(c, item.Label)
	}
	spans := quickPickSpans(item.Label, item.matches, Colors(c.Theme()).Primary, Scaled(TextBody))
	return ui.RichText(c, spans...).Label(item.Label)
}

// quickPickSpans splits plain into spans that always concatenate back to plain.
func quickPickSpans(plain string, matches []fuzzy.Match, ink ui.Color, size float32) []ui.Span {
	runes := []rune(plain)
	spans := make([]ui.Span, 0, len(matches)*2+1)
	at := 0
	for _, match := range matches {
		if match.Start < at || match.End <= match.Start || match.End > len(runes) {
			continue
		}
		if match.Start > at {
			spans = append(spans, ui.Span{Text: string(runes[at:match.Start]), Size: size})
		}
		spans = append(spans, ui.Span{
			Text: string(runes[match.Start:match.End]), Size: size,
			Color: ink, Weight: 600,
		})
		at = match.End
	}
	if at < len(runes) {
		spans = append(spans, ui.Span{Text: string(runes[at:]), Size: size})
	}
	return spans
}

// quickPickMatches filters and highlights the groups. Selected stays within
// the rows shown.
func quickPickMatches(groups []QuickPickGroup, query string, selected *int) []QuickPickGroup {
	prepared := fuzzy.PrepareQuery(query)
	cache := fuzzy.NewFuzzyScorerCache()
	accessor := quickPickAccessor{}
	out := make([]QuickPickGroup, 0, len(groups))
	for _, group := range groups {
		matched := QuickPickGroup{Title: group.Title}
		for _, item := range group.Items {
			if query == "" {
				matched.Items = append(matched.Items, item)
				continue
			}
			score := fuzzy.ScoreItemFuzzy(item, prepared, true, accessor, cache)
			if score == nil || score.Score == 0 {
				continue
			}
			item.matches = score.LabelMatch
			matched.Items = append(matched.Items, item)
		}
		if len(matched.Items) > 0 {
			out = append(out, matched)
		}
	}
	*selected = clampSelection(*selected, quickPickCount(out))
	return out
}

func quickPickCount(groups []QuickPickGroup) int {
	n := 0
	for _, group := range groups {
		n += len(group.Items)
	}
	return n
}

func clampSelection(selected, count int) int {
	if count == 0 {
		return 0
	}
	return max(0, min(count-1, selected))
}

func quickPickStep(groups []QuickPickGroup, selected *int, dir int) {
	*selected = clampSelection(*selected+dir, quickPickCount(groups))
}

func quickPickSelected(groups []QuickPickGroup, selected int) (QuickPickItem, bool) {
	at := 0
	for _, group := range groups {
		for _, item := range group.Items {
			if at == selected {
				return item, true
			}
			at++
		}
	}
	return QuickPickItem{}, false
}

type quickPickAccessor struct{}

func (quickPickAccessor) GetItemLabel(item QuickPickItem) string { return item.Label }
func (quickPickAccessor) GetItemDescription(item QuickPickItem) (string, bool) {
	if item.Search == "" {
		return "", false
	}
	return item.Search, true
}
func (quickPickAccessor) GetItemPath(QuickPickItem) (string, bool) { return "", false }
