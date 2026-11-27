package uikit

import (
	"github.com/egoist/mygo/ui"
)

// FindToggle is one option of a find field. On is the option's state; Label
// and Tip name it for assistive technology and the pointer. Key is the letter
// the bar's Alt shortcut uses, and Shortcut names that letter in the tip.
type FindToggle struct {
	On       *bool
	Label    string
	Tip      string
	Icon     *ui.SVG
	Key      ui.Key
	Shortcut string
}

// FindToggles draws a find bar's options as compact icon toggles at the
// trailing edge of its field, as VS Code does. They keep the keyboard in the
// query, so typing continues after a click. An option that is on takes a
// faint accent wash and an accent edge; the icon alone marks its meaning.
func FindToggles(c *ui.Context, toggles ...FindToggle) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	row := ui.Row(c).Shrink(0).Gap(t.Space(SpaceHair)).AlignItems(ui.Center)
	row.Children(func() {
		for _, option := range toggles {
			if option.On == nil {
				continue
			}
			toggle := ui.ToggleBase(c, option.On).Label(option.Label).KeepFocus().
				KeyShortcuts(ui.Alt, option.Key).Size(HeightControlSmall, HeightControlSmall).
				Padding(0).Shrink(0).Justify(ui.Center).AlignItems(ui.Center).Radius(RadiusInner)
			if *option.On {
				toggle.Background(p.Accent).Border(HairlineWidth, p.Foreground.Alpha(0.28))
			}
			ink := t.TextMuted
			if *option.On {
				ink = t.Text
			}
			toggle.Children(func() {
				ui.Icon(c, option.Icon).Size(IconInline, IconInline).TextColor(ink)
			})
			TipKeys(c, toggle, option.Tip, "Alt", option.Shortcut)
		}
	})
	return row
}

// FindField is a find bar's query field with its option toggles inside the
// field's trailing edge. The field carries the focus edge; the returned input
// is what the bar binds and focuses.
func FindField(c *ui.Context, value *string, label, placeholder string, toggles ...FindToggle) (ui.Element, ui.Element) {
	t, p := c.Theme(), Colors(c.Theme())
	row := ui.Row(c).Height(HeightControl).AlignItems(ui.Center).Gap(t.Space(SpaceXS)).
		Padding(0, t.Space(SpaceXS)).Radius(RadiusControl)
	var input ui.Element
	row.Children(func() {
		input = ui.TextInputBase(c, value).Height(HeightControl).FontSize(Scaled(TextSmall)).
			Padding(0, t.Space(SpaceM)).Grow(1).MinWidth(0).FocusRing(false).
			Label(label).Placeholder(placeholder)
		if len(toggles) > 0 {
			FindToggles(c, toggles...)
		}
	})
	fieldFaceRadius(p, row, input.Focused(), false, RadiusControl)
	return row, input
}
