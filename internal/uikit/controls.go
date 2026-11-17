package uikit

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// Checkbox and Radio are the kit's choice controls. Their boxes wear a
// control's face, as a button does: the text color laid thinly at rest,
// hovered and pressed, and the ink accent once chosen, with the mark in the
// accent text. Nothing is outlined, and the label is body text beside them.

var checkIcon = lucide.Check()

// Checkbox toggles *checked with a label; a click on the box or the label
// checks it, as does Space while it has the focus.
func Checkbox(c *ui.Context, checked *bool, label string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	row := ui.CheckboxBase(c, checked).Label(label).Gap(t.Space(SpaceM)).AlignItems(ui.Center)
	row.Children(func() {
		box := ui.Box(c).Size(IconLarge, IconLarge).Radius(RadiusInner).Shrink(0).Center()
		switch {
		case *checked && row.Hovered():
			box.Background(p.AccentHover)
		case *checked:
			box.Background(p.Accent)
		case row.Pressed():
			box.Background(p.FillPressed)
		case row.Hovered():
			box.Background(p.FillHover)
		default:
			box.Background(p.Fill)
		}
		if !*checked {
			// An empty box needs an edge to be seen: its face alone is too faint
			// to read as a box on a surface.
			box.Border(HairlineWidth, p.Ring)
		}
		box.Children(func() {
			if *checked {
				ui.Icon(c, checkIcon).Size(IconInline, IconInline).TextColor(p.AccentText)
			}
		})
		if label != "" {
			Body(c, label)
		}
	})
	return row
}

// Radio selects value into *selected with a label; a click selects it, as
// does Space while it has the focus. Build the choices in a RadioGroup.
func Radio[T comparable](c *ui.Context, selected *T, value T, label string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	on := *selected == value
	row := ui.RadioBase(c, selected, value).Label(label).Gap(t.Space(SpaceM)).AlignItems(ui.Center)
	row.Children(func() {
		dot := ui.Box(c).Size(IconLarge, IconLarge).Radius(IconLarge / 2).Shrink(0).Center()
		switch {
		case on && row.Hovered():
			dot.Background(p.AccentHover)
		case on:
			dot.Background(p.Accent)
		case row.Pressed():
			dot.Background(p.FillPressed)
		case row.Hovered():
			dot.Background(p.FillHover)
		default:
			dot.Background(p.Fill)
		}
		if !on {
			dot.Border(HairlineWidth, p.Ring)
		}
		dot.Children(func() {
			if on {
				ui.Box(c).Size(DotSize, DotSize).Radius(DotSize / 2).Background(p.AccentText)
			}
		})
		if label != "" {
			Body(c, label)
		}
	})
	return row
}

// RadioGroup holds radio buttons as one stop of Tab, laid out in a column with
// the kit's gap between them. Make it a row with Row for a short choice.
func RadioGroup(c *ui.Context, fn func()) ui.Element {
	return ui.RadioGroup(c, fn).Gap(c.Theme().Space(SpaceS))
}
