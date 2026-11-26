package uikit

import (
	"github.com/egoist/mygo/ui"
)

// Panel frames one data area on the canvas: the surface behind it, a
// hairline and RadiusPanel. Use it for a preview, a catalog or a reader that
// would otherwise float on the dialog's canvas, not for every group.
func Panel(c *ui.Context) ui.Element {
	p := Colors(c.Theme())
	return ui.Column(c).Background(p.Window).Border(HairlineWidth, p.Border).Radius(RadiusPanel)
}

// PopoverPanel is the chrome of anything floating above the window short of
// a menu or a dialog: popovers and timing cards. It is a 12-point canvas
// panel on the popover shadow. A menu takes MenuPanel; a dialog Modal's own.
func PopoverPanel(c *ui.Context, panel ui.Element) ui.Element {
	p := Colors(c.Theme())
	overlayShown(c)
	return ShadowPopover.Apply(panel.Radius(RadiusCard).Background(p.Background).InsetShadow(0, 0, 0, HairlineWidth, p.Border), c.Theme().Dark)
}

// ToastPanel is the chrome of a toast: the dialog's 16-point corner and the
// popover's shadow, so a toast reads as a lighter, temporary dialog rather
// than a second dialog.
func ToastPanel(c *ui.Context, panel ui.Element) ui.Element {
	p := Colors(c.Theme())
	return ShadowPopover.Apply(panel.Radius(RadiusDialog).Background(p.Background).InsetShadow(0, 0, 0, HairlineWidth, p.Border), c.Theme().Dark)
}

// EdgeFade fades a row clipped by a scroll into the surface behind it: from
// fades its leading edge, otherwise its trailing one. It only paints, so what
// is half under it still takes its click.
func EdgeFade(c *ui.Context, from bool, fill ui.Color) ui.Element {
	edge := ui.Box(c).Absolute().Top(0).Bottom(0).Width(c.Theme().Space(Space2XL)).PassThrough()
	if from {
		return edge.Left(0).Gradient(fill, fill.Alpha(0), 90)
	}
	return edge.Right(0).Gradient(fill.Alpha(0), fill, 90)
}

// PaneHeader is the one row above a pane: what it shows, then what it reports
// or offers at the far end. The caller builds the title and tabs, so a pane can
// name itself with a status light and focus on a click; its tabs grow, which
// is what keeps what follows them at the edge.
func PaneHeader(c *ui.Context, content, trailing func()) ui.Element {
	t := c.Theme()
	// A pane header is a row, not a band: the regions around it are told
	// apart by the split and by space, never by a rule of its own.
	return ui.Row(c).Height(HeightTabs).Padding(0, PanelPadding).Gap(t.Space(SpaceM)).AlignItems(ui.Center).Children(func() {
		content()
		if trailing != nil {
			trailing()
		}
	})
}

// PaneTitle names a pane. dot lights it with a status color; ink colors the
// name when it carries a signal, and it stays in the body color otherwise. A
// click focuses the pane for the controls only one side owns at a time.
func PaneTitle(c *ui.Context, title, name string, dot, ink ui.Color, onClick func()) ui.Element {
	t := c.Theme()
	if ink.A == 0 {
		ink = t.Text
	}
	b := Tip(c, ui.ButtonBase(c).Label(name), name).Height(HeightControlSmall).Padding(0).
		Gap(t.Space(SpaceS)).Radius(RadiusControl).AlignItems(ui.Center).TextColor(ink)
	b.Children(func() {
		if dot.A > 0 {
			ui.Box(c).Size(DotSize, DotSize).Radius(DotSize / 2).Background(dot).Shrink(0)
		}
		ui.Text(c, title).FontSize(Scaled(TextBody)).FontWeight(WeightMedium).SingleLine()
	})
	if b.Clicked() && onClick != nil {
		onClick()
	}
	return b
}

// Glyph is a quiet inline icon that marks rather than acts: a folder, a pin,
// a note.
func Glyph(c *ui.Context, icon *ui.SVG) ui.Element {
	return ui.Icon(c, icon).Size(IconInline, IconInline).TextColor(c.Theme().TextMuted)
}
