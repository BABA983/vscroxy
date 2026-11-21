package uikit

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// Checkbox and Radio are the kit's choice controls. Their boxes have a clear
// face with a Primary edge when off and a Primary fill with a PrimaryForeground
// mark when on. Hover and press leave the box alone; MyGo
// dims a disabled row. The label stays body text beside the box.

var checkIcon = lucide.Check()

// Checkbox toggles *checked with a label; a click on the box or the label
// checks it, as does Space while it has the focus.
func Checkbox(c *ui.Context, checked *bool, label string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	row := ui.CheckboxBase(c, checked).Label(label).Gap(t.Space(SpaceM)).AlignItems(ui.Center)
	row.Children(func() {
		box := ui.Box(c).Size(IconLarge, IconLarge).Radius(RadiusInner).Shrink(0).Center()
		if *checked {
			box.Background(p.Primary)
		}
		box.Border(HairlineWidth, p.Primary)
		box.Children(func() {
			if *checked {
				ui.Icon(c, checkIcon).Size(IconInline, IconInline).TextColor(p.PrimaryForeground)
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
		if on {
			dot.Background(p.Primary)
		}
		dot.Border(HairlineWidth, p.Primary)
		dot.Children(func() {
			if on {
				ui.Box(c).Size(DotSize, DotSize).Radius(DotSize / 2).Background(p.PrimaryForeground)
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
