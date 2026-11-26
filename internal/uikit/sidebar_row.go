package uikit

import (
	"cmp"

	"github.com/egoist/mygo/ui"
)

// SidebarDot is the icon slot's dot, for a sidebar row that names a host, an
// app or a thing without an icon of its own: the slot keeps its width, so the
// row's label starts on the line the rows with icons do.
var SidebarDot = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 14 14" width="14" height="14"><circle cx="7" cy="7" r="2.5"/></svg>`))

// SidebarHeading names a region of a sidebar's list: small, quiet and set off
// from the rows above it. With open the heading folds the rows under it away,
// as a source list's section does: a click on it toggles them, and the arrow
// that says which way is drawn under the pointer, or while the region is shut.
// trailing draws what the heading itself offers at the far end, as the switch
// between two lists of the same thing does.
func SidebarHeading(c *ui.Context, title string, open *bool, trailing func(), compact ...bool) ui.Element {
	t := c.Theme()
	top := t.Space(SpaceM)
	if len(compact) > 0 && compact[0] {
		top = 0
	}
	head := ui.Row(c).Height(HeightStatus).Shrink(0).Margin(top, 0, 0, 0).
		Padding(0, t.Space(SpaceL)).Gap(t.Space(SpaceM)).AlignItems(ui.Center).Label(title)
	if open != nil {
		head.OnClick(func() { *open = !*open })
	}
	head.Children(func() {
		Label(c, title).Grow(1).SingleLine()
		if open != nil {
			arrow := ui.Box(c).Size(IconInline, IconInline).Shrink(0)
			if head.Hovered() || !*open {
				icon := disclosure
				if *open {
					icon = chevronIcon
				}
				arrow.Children(func() {
					ui.Icon(c, icon).Size(IconInline, IconInline).TextColor(t.TextMuted)
				})
			}
		}
		if trailing != nil {
			trailing()
		}
	})
	return head
}

// SidebarButton is a sidebar row that acts rather than chooses: it takes the
// rows' height, inset, slot and label, with no face until the pointer is on
// it. on marks the thing it stands for as on, in the accent. Add to it with
// Children for what follows the label, as a chevron or a close button.
func SidebarButton(c *ui.Context, label, tip string, icon *ui.SVG, on bool) ui.Element {
	t := c.Theme()
	ink := t.TextMuted
	if on {
		ink = t.Accent
	}
	return ToolbarButton(c, label, tip, false).TextColor(t.Text).
		Margin(0, t.Space(SpaceS)).Padding(0, t.Space(SpaceS)).Gap(t.Space(SpaceM)).Children(func() {
		ui.Icon(c, cmp.Or(icon, SidebarDot)).Size(IconLarge, IconLarge).TextColor(ink)
		Body(c, label).SingleLine().Grow(1)
	})
}
