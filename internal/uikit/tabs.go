package uikit

import (
	"github.com/egoist/mygo/ui"
)

// Tabs is a row of text tabs; the chosen one is a soft ink wash under its
// word, as Postman's request tabs are: a small inner radius, a compact chip
// height and ink text, with a lighter wash under the pointer. It is never an
// underline, a raised chip or green: a tab is a filter, not a selection of
// data. The prefix names each tab for assistive technology, so a screen with
// two tab rows of the same labels can name the one it means.
func Tabs(c *ui.Context, selected *int, prefix string, labels ...string) ui.Element {
	return tabs(c, selected, prefix, labels, nil, false)
}

// CountedTabs is Tabs with a quiet count after each label, as in "Headers 3".
// A count is presentation, not part of the tab's accessible name; an empty
// or zero count is left out.
func CountedTabs(c *ui.Context, selected *int, prefix string, labels, counts []string) ui.Element {
	return tabs(c, selected, prefix, labels, counts, false)
}

// SubTabs is a row of tabs inside what another tab row chose, or at the head
// of a list beside an editor: the words alone carry the choice, and the chosen
// one is underlined in the primary ink. It has no chip, so a second level never
// looks like the row that holds it.
func SubTabs(c *ui.Context, selected *int, prefix string, labels ...string) ui.Element {
	return tabs(c, selected, prefix, labels, nil, true)
}
func tabs(c *ui.Context, selected *int, prefix string, labels, counts []string, sub bool) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	group := ui.TabsBase(c, selected, len(labels))
	gap := t.Space(SpaceHair)
	if sub {
		// A second level reads as words: the words keep a gap that tells
		// them apart, and the rule below them is the only mark of the
		// chosen one.
		gap = t.Space(SpaceM)
	}
	// One row of words; the padding belongs to each tab, so neighbours stay
	// apart without a visible rule between them.
	group.List.Gap(gap).Children(func() {
		for i, label := range labels {
			name := label
			if prefix != "" {
				name = prefix + ": " + label
			}
			chosen := i == *selected
			height := float32(HeightRow)
			inset := t.Space(SpaceS)
			radius := float32(RadiusInner)
			if sub {
				// A second level is words with an underline, so it keeps the
				// tighter hit area that fits beside an editor.
				height, inset, radius = HeightControlSmall, Scaled(ControlPaddingXSmall), 0
			}
			tab := group.Tab(i).Label(name).Height(height).Padding(0, inset).Gap(t.Space(SpaceXS)).
				Radius(radius).AlignItems(ui.Center).TextColor(t.TextMuted)
			if sub {
				// The chosen word is ink over its rule; hover only darkens the
				// word, so a second level never shows a box.
				if chosen || tab.Pressed() || tab.Hovered() {
					tab.TextColor(t.Text)
				}
			} else {
				// The chosen tab is a 5% ink wash; hover is lighter, and a press
				// deepens it, so the row never shows a stronger box than the
				// choice it is holding.
				switch {
				case chosen:
					tab.Background(p.Foreground.Alpha(0.05)).TextColor(t.Text)
				case tab.Pressed():
					tab.Background(p.Muted).TextColor(t.Text)
				case tab.Hovered():
					tab.Background(p.Foreground.Alpha(0.03)).TextColor(t.Text)
				}
			}
			tab.Children(func() {
				ui.Text(c, label).FontSize(Scaled(TextSmall))
				if i < len(counts) && counts[i] != "" && counts[i] != "0" {
					Meta(c, counts[i]).FontFeatures("tnum")
				}
				if chosen && sub {
					// The rule spans the word, not the tab's hit area, so
					// it starts where the word does.
					ui.Box(c).Absolute().Left(inset).Right(inset).Bottom(0).
						Height(TabRuleHeight).Background(p.Primary)
				}
			})
		}
	})
	return group.List
}

// TabStrip scrolls a row of tabs sideways in a narrow pane. An edge that
// hides more tabs fades into the surface behind them, so a cut-off tab reads
// as more to scroll rather than a clipped label. The caller sizes it.
func TabStrip(c *ui.Context, tabs func()) ui.Element {
	return tabStripOn(c, c.Theme().Background, tabs)
}

// tabStripOn is TabStrip on a surface other than the canvas: its edges fade
// into fill.
func tabStripOn(c *ui.Context, fill ui.Color, tabs func()) ui.Element {
	strip := ui.Box(c).MinWidth(0)
	scroll := ui.Local(strip, "scroll", func() ui.ScrollState { return ui.ScrollState{} })
	return strip.Children(func() {
		scroller := ui.ScrollHorizontal(c).TrackScroll(scroll).Fill().Scrollbars(false)
		scroller.HandleInput(func(ev ui.InputEvent) bool {
			// A mouse wheel's ordinary gesture is vertical, while this row
			// only goes sideways: turn a bare vertical notch into horizontal
			// travel toward the end. Shift+wheel keeps MyGo's own mapping.
			if ev.Kind != ui.InputScroll || ev.Mods != 0 || ev.DX != 0 || ev.DY == 0 {
				return false
			}
			next := max(0, min(scroll.X+ev.DY, scroll.MaxX))
			if next == scroll.X {
				return false
			}
			scroll.X = next
			c.Invalidate()
			return true
		}).Children(tabs)
		if scroll.X > 0.5 {
			EdgeFade(c, true, fill)
		}
		if scroll.X < scroll.MaxX-0.5 {
			EdgeFade(c, false, fill)
		}
	})
}
