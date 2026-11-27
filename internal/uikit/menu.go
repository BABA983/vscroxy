package uikit

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// MenuSelect is Select for choices built as a menu: a button naming the
// current one, with the same field face and chevron, that opens the menu.
func MenuSelect(c *ui.Context, current string, menu func(*ui.Menu)) ui.Element {
	var b ui.Element
	var open *bool
	MenuFace(c, func() {
		b = ui.ButtonBase(c)
		open = ui.Local(b, "menu-open", func() bool { return false })
		opts := MenuButtonOptions(c)
		opts.Open = open
		b.Label(current).PopoverMenuButton(menu, opts)
		b.Children(func() {
			ui.Text(c, current).FontSize(Scaled(TextBody)).SingleLine()
			chevron(c)
		})
	})
	return selectTrigger(c, b, *open).Justify(ui.SpaceBetween).TextColor(c.Theme().Text)
}

// ToolbarSelect is MenuSelect without a field's well: the current choice and
// its chevron at toolbar height, a face only under the pointer. It is for a
// choice that sits among a pane's tabs and tools, where a filled field would
// be the one heavy thing on a quiet row.
func ToolbarSelect(c *ui.Context, current string, menu func(*ui.Menu)) ui.Element {
	return toolbarSelect(c, current, nil, menu)
}

// ToolbarSelectIcon is ToolbarSelect with an icon that names the choice's
// kind before its label, as a body view keeps its format's mark.
func ToolbarSelectIcon(c *ui.Context, current string, icon *ui.SVG, menu func(*ui.Menu)) ui.Element {
	return toolbarSelect(c, current, icon, menu)
}

func toolbarSelect(c *ui.Context, current string, icon *ui.SVG, menu func(*ui.Menu)) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	var b ui.Element
	MenuFace(c, func() {
		b = ui.ButtonBase(c)
		b.Label(current).PopoverMenuButton(menu, MenuButtonOptions(c))
		b.Children(func() {
			if icon != nil {
				ui.Icon(c, icon).Size(IconInline, IconInline).TextColor(t.TextMuted)
			}
			ui.Text(c, current).FontSize(Scaled(TextBody)).SingleLine()
			chevron(c)
		})
	})
	b.Height(HeightControl).Padding(0, Scaled(ControlPaddingXSmall)).Gap(t.Space(SpaceXS)).
		Radius(RadiusControl).AlignItems(ui.Center).Shrink(0).TextColor(t.Text)
	if !b.IsDisabled() {
		switch {
		case b.Pressed():
			b.Background(p.MutedPressed)
		case b.Hovered():
			b.Background(p.MutedHover)
		}
	} else {
		b.TextColor(t.TextMuted)
	}
	return b
}

// popoverFace gives a menu's floating panel the kit's chrome: the canvas on
// the menu shadow, edged by a hairline. A menu's panel comes from MyGo, so
// the face is applied here instead of in PopoverPanel.
func popoverFace(c *ui.Context, panel ui.Element) {
	MenuPanel(c, panel.NoShadow())
}

// MenuPanel is the chrome of a drawn menu: a 6-point panel on the
// shadow-popover's three layers. MyGo builds the items; this is the face
// they sit on. The 1-point ring is part of the shadow, not a Border token.
func MenuPanel(c *ui.Context, panel ui.Element) ui.Element {
	p := Colors(c.Theme())
	overlayShown(c)
	e := panel.Border(0, ui.Color{}).Radius(RadiusMenu).Background(p.Background)
	return ShadowMenu.Apply(e, c.Theme().Dark)
}

// menuTheme scopes a menu's items to the menu face: a 7% ink highlight with
// ink text, a 4-point item corner and 12-point type. MyGo reads these from
// the theme while it builds the menu, so the wrapper must be around that
// build, not around the button that opens it.
func menuTheme(c *ui.Context, build func()) {
	t := c.Theme()
	p := Colors(t)
	face := *t
	face.Accent, face.AccentHover, face.AccentPressed = p.Accent, p.Accent, p.Accent
	face.AccentText = t.Text
	face.Radius, face.FontSize = RadiusInner, Scaled(TextSmall)
	c.SetTheme(&face)
	build()
	c.SetTheme(t)
}

// MenuFace builds a menu button with the item face. MyGo reads the
// highlight colors, radius and type from the theme when the button is made,
// not when its menu opens, so the scope has to cover construction.
func MenuFace(c *ui.Context, build func()) { menuTheme(c, build) }

// MenuButtonOptions gives a PopoverMenuButton the kit's menu face. Use it
// for a menu the app opens from its own button; MyGo's ContextMenu is the
// system's menu and takes no panel option.
func MenuButtonOptions(c *ui.Context) ui.PopoverMenuButtonOptions {
	return ui.PopoverMenuButtonOptions{Panel: func(panel ui.Element) { MenuPanel(c, panel) }}
}

// MenuOptions gives a PopoverMenu the kit's menu face, so a right-click menu
// and the app's menu buttons look alike.
func MenuOptions(c *ui.Context) ui.PopoverMenuOptions {
	return ui.PopoverMenuOptions{Panel: func(panel ui.Element) { MenuPanel(c, panel) }}
}

// ContextMenu gives el a right-click menu with the kit's item face. MyGo
// reads a drawn menu's highlight, radius and type from the theme active while
// PopoverMenu is called, so the scope has to cover that call: without it a
// menu built under the base theme highlights in the primary ink instead of
// the menu's 7% wash.
func ContextMenu(c *ui.Context, el ui.Element, build func(*ui.Menu)) ui.Element {
	menuTheme(c, func() { el.PopoverMenu(build, MenuOptions(c)) })
	return el
}

// MoreButton is the toolbar's "⋯": an IconButton with the menu on it and no
// chevron, since the three dots already say one opens. The title bar uses it.
func MoreButton(c *ui.Context, label, tip string, menu func(*ui.Menu)) ui.Element {
	var b ui.Element
	MenuFace(c, func() {
		b = IconButton(c, label, tip, lucide.Ellipsis()).PopoverMenuButton(menu, MenuButtonOptions(c))
	})
	return b
}

// MoreMenu is the "⋯" of a row, a sidebar heading or a pane header, at the
// row-action size: HeightRowAction square with RadiusInner. It draws the same
// Ellipsis icon as MoreButton, has no face until hovered, and keeps its width
// where a row is full.
func MoreMenu(c *ui.Context, label string, menu func(*ui.Menu)) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	ghost := *t
	ghost.Surface, ghost.SurfaceHover, ghost.SurfacePressed = ui.Color{}, p.MutedHover, p.MutedPressed
	ghost.Border = ui.Color{}
	c.SetTheme(&ghost)
	// A button of our own with a menu on it, not the library's menu button:
	// that one draws a chevron after its label, and a ⋯ already says a menu
	// opens. The menu itself is the library's, panel and motion included.
	var b ui.Element
	MenuFace(c, func() {
		b = ui.ButtonBase(c).Label(label).PopoverMenuButton(menu, MenuButtonOptions(c))
	})
	// ButtonBase is a row with no look: the icon has to be a child, or the
	// row's menu is an empty strip with nothing to click.
	b.Children(func() { ui.Icon(c, lucide.Ellipsis()).Size(IconInline, IconInline) })
	c.SetTheme(t)
	return Tip(c, b, label).Size(HeightRowAction, HeightRowAction).Shrink(0).
		Padding(0).Radius(RadiusInner).Justify(ui.Center).AlignItems(ui.Center).TextColor(t.TextMuted)
}

// TabMenu is a tab that opens a menu instead of choosing a view: where a pane
// keeps the views it has beyond the tabs in its row, so it never needs a
// second row of them.
func TabMenu(c *ui.Context, label, name string, menu func(*ui.Menu)) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	ghost := *t
	ghost.Surface, ghost.SurfaceHover, ghost.SurfacePressed = ui.Color{}, p.MutedHover, p.MutedPressed
	ghost.Border = ui.Color{}
	c.SetTheme(&ghost)
	var b ui.Element
	MenuFace(c, func() {
		b = ui.ButtonBase(c).Label(label).PopoverMenuButton(menu, MenuButtonOptions(c))
	})
	// ButtonBase paints nothing of its own: the tab's words are its child.
	b.Children(func() { ui.Text(c, label).FontSize(Scaled(TextBody)).SingleLine() })
	c.SetTheme(t)
	return Tip(c, b.Label(name), name).Height(HeightControlSmall).Padding(0, Scaled(ControlPaddingXSmall)).
		Radius(RadiusControl).AlignItems(ui.Center).Gap(t.Space(SpaceXS)).FontSize(Scaled(TextBody)).TextColor(t.TextMuted)
}
