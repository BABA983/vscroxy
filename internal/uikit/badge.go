package uikit

import (
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// StatusPill prints a status in its signal color. It uses the quiet filter
// pill: clear face, 28 points tall, 4×6 inset, 11-point type and a
// full-pill corner. Status and method colors never fill a background here.
func StatusPill(c *ui.Context, label string, foreground ui.Color) ui.Element {
	t := c.Theme()
	return ui.Text(c, label).FontSize(Scaled(TextSmall)).FontFeatures("tnum").SingleLine().
		TextColor(foreground).Height(HeightControl).
		Padding(t.Space(SpaceXS), t.Space(SpaceS)).Radius(HeightControl / 2)
}

// QueryChip is one condition in the query bar, written as the query syntax
// writes it. A filter is not a selection, so the chip is a neutral face. A
// click on its text runs edit, which turns it back into text to change; a
// condition with no syntax passes nil and has no such button. The × runs
// remove. Both run after the chip is built.
func QueryChip(c *ui.Context, text string, edit, remove func()) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	return ui.Row(c.Key("query-chip:"+text)).Height(HeightChip).Shrink(0).Padding(0, t.Space(SpaceHair)).Gap(0).
		AlignItems(ui.Center).Radius(RadiusInner).Background(p.Muted).Children(func() {
		label := func() {
			ui.Text(c, text).Font(FontMono).FontSize(Scaled(TextData)).TextColor(t.Text).SingleLine()
		}
		if edit == nil {
			ui.Row(c).Padding(0, t.Space(SpaceXS)).Children(label)
		} else {
			b := Tip(c, ui.ButtonBase(c).Label(locales.QueryEditLabel(text)), locales.QueryEditTip(text)).
				Height(HeightChip).Padding(0, t.Space(SpaceXS)).Radius(RadiusInner).AlignItems(ui.Center)
			if b.Hovered() {
				b.Background(p.MutedHover)
			}
			b.Children(label)
			if b.Clicked() {
				edit()
			}
		}
		x := Tip(c, ui.ButtonBase(c).Label(locales.QueryRemoveLabel(text)), locales.QueryRemoveTip(text)).
			Size(HeightChip, HeightChip).Radius(RadiusInner).Justify(ui.Center).AlignItems(ui.Center).TextColor(t.TextMuted)
		if x.Hovered() {
			x.Background(p.MutedHover)
		}
		x.Children(func() { ui.Icon(c, closeIcon).Size(IconInline, IconInline) })
		if x.Clicked() {
			remove()
		}
	})
}
func MethodLabel(c *ui.Context, method string) ui.Element {
	return ui.Text(c, method).Font(FontMono).FontSize(Scaled(TextData)).FontWeight(WeightMedium).
		TextColor(MethodColor(method, c.Theme())).SingleLine()
}
