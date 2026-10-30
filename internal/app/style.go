package app

import "github.com/egoist/mygo/ui"

// Shared metrics. Container geometry stays with each screen; these describe
// controls, not a new layout for the shell or inspector.
const (
	textMeta      = 11
	textBody      = 13
	textTitle     = 15
	heightCompact = 24
	heightControl = 28
	heightLarge   = 32
	heightTabs    = 32
	radiusControl = 4
	radiusPanel   = 6
	radiusDialog  = 10
	iconInline    = 14
	iconLarge     = 16
	// Latin fallbacks precede CJK faces: missing embedded fonts must not turn
	// monospace readers into proportional text or widen ordinary UI labels.
	fontUI   = "Inter, Arial, DejaVu Sans, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC, sans-serif"
	fontMono = "JetBrains Mono, Menlo, Consolas, DejaVu Sans Mono, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC, monospace"
)

// ButtonBase keeps native keyboard, focus-ring and accessibility behavior.
// IsDisabled includes a disabled ancestor, so disabled controls never hover.
func toolbarButton(c *ui.Context, label, tip string, active bool) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Label(label).Tooltip(tip).Height(heightControl).
		Padding(0, t.Space(2)).Gap(t.Space(2)).Radius(radiusControl).
		AlignItems(ui.Center).FontSize(textBody).TextColor(t.TextMuted)
	if !b.IsDisabled() {
		if active {
			b.TextColor(t.Accent)
		}
		switch {
		case b.Pressed():
			b.Background(t.SurfacePressed)
		case b.Hovered():
			b.Background(t.SurfaceHover)
		case active:
			b.Background(t.Accent.Alpha(0.12))
		}
	} else {
		b.TextColor(appColors(t).TextFaint)
	}
	return b
}

func iconButton(c *ui.Context, label, tip string, icon *ui.SVG) ui.Element {
	return toolbarButton(c, label, tip, false).Size(heightCompact, heightCompact).
		Padding(0).Justify(ui.Center).Children(func() {
		ui.Icon(c, icon).Size(iconInline, iconInline)
	})
}

func primaryButton(c *ui.Context, label string) ui.Element {
	return ui.PrimaryButton(c, label).Height(heightControl).Padding(0, c.Theme().Space(3)).
		FontSize(textBody).Radius(radiusControl)
}

func secondaryButton(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Height(heightControl).Padding(0, c.Theme().Space(3)).
		FontSize(textBody).Radius(radiusControl)
}

func statusPill(c *ui.Context, label string, foreground ui.Color) ui.Element {
	return ui.Text(c, label).FontSize(textBody).FontFeatures("tnum").SingleLine().
		TextColor(foreground).Background(foreground.Alpha(0.12)).
		Padding(c.Theme().Space(1), c.Theme().Space(2)).Radius(radiusControl)
}

func methodLabel(c *ui.Context, method string) ui.Element {
	return ui.Text(c, method).Font(fontMono).FontSize(textBody).
		TextColor(methodColor(method, c.Theme())).SingleLine()
}

// Keep Sidebar's native source-list navigation and item identities. Only
// unselected icons use a neutral accent; selected items retain native colors.
func sidebarItem(c *ui.Context, id string, icon *ui.SVG, label string, chosen bool) ui.Element {
	t := c.Theme()
	neutral := *t
	if !chosen {
		neutral.Accent = t.TextMuted
	}
	c.SetTheme(&neutral)
	item := ui.SidebarItem(c, id, icon, label).Height(heightControl).
		FontSize(textBody).Padding(0, t.Space(2)).Radius(radiusControl)
	c.SetTheme(t)
	return item
}

// Read-only key/value rows preserve wrapping and existing value actions.
// The editable request grid and merged pane headers arrive with their layouts.
func kvRow(c *ui.Context, content func()) ui.Element {
	t := c.Theme()
	return ui.Row(c).Gap(t.Space(3)).Padding(t.Space(1), t.Space(3)).
		MinHeight(heightControl).AlignItems(ui.Start).FillWidth().Children(content)
}
