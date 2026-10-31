package app

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

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
	heightBar     = 44 // title bar and the Capture toolbar
	radiusControl = 4
	radiusPanel   = 6
	radiusDialog  = 10
	iconInline    = 14
	iconLarge     = 16
	dialogSmall   = 480
	dialogMedium  = 640
	dialogLarge   = 840
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

// Dialog content uses three width tiers inside appModal's shared padding.
// Callers choose the height; long content scrolls without moving the footer.
func dialogColumn(c *ui.Context, width float32) ui.Element {
	w, h := c.Size()
	return ui.Column(c).Width(min(width, w-80)).MaxHeight(h - 80).MinHeight(0).Gap(c.Theme().Space(4))
}

func dialogFooter(c *ui.Context) ui.Element {
	t := c.Theme()
	return ui.Row(c).FillWidth().Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Justify(ui.End).
		Padding(t.Space(3), 0, 0, 0).Border(0, t.Border).BorderWidth(1, 0, 0, 0)
}

// popoverPanel is the chrome of anything floating above the window short of
// a dialog: popovers and toasts. appModal has the heavier of the two shadows.
func popoverPanel(c *ui.Context, panel ui.Element) ui.Element {
	t := c.Theme()
	return panel.Radius(radiusPanel).Background(t.Background).Border(1, t.Border).
		Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.22))
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

func sidebarSection(c *ui.Context, title string, open *bool, content func()) {
	t := c.Theme()
	sectionTheme := *t
	sectionTheme.FontSize = textMeta + 1 // SidebarSection uses FontSize - 1.
	c.SetTheme(&sectionTheme)
	ui.SidebarSection(c, strings.ToUpper(title), open, func() {
		c.SetTheme(t)
		content()
	}).Margin(t.Space(2), 0, 0, 0)
	c.SetTheme(t)
}

func emptyState(c *ui.Context, title, hint string, actions func()) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Padding(t.Space(6)).Gap(t.Space(2)).Children(func() {
		ui.Icon(c, trafficIcon).Size(iconLarge, iconLarge).TextColor(t.TextMuted).Margin(0, 0, t.Space(2), 0)
		ui.Text(c, title).FontSize(textTitle).TextColor(t.Text)
		ui.Text(c, hint).FontSize(textBody).TextColor(t.TextMuted)
		if actions != nil {
			actions()
		}
	})
}

// Read-only key/value rows preserve wrapping and existing value actions.
func kvRow(c *ui.Context, content func()) ui.Element {
	t := c.Theme()
	return ui.Row(c).Gap(t.Space(3)).Padding(t.Space(1), t.Space(3)).
		MinHeight(heightControl).AlignItems(ui.Start).FillWidth().Children(content)
}

// Only the look is ours: MyGo owns announcements, timeouts, pause-on-hover,
// the three-toast limit, action dispatch and keyboard dismissal.
func appToasts(c *ui.Context) {
	t := c.Theme()
	width, _ := c.Size()
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(t.Space(4)).AlignItems(ui.End).Gap(t.Space(2))
		for _, toast := range toasts {
			icon, color := infoIcon, t.TextMuted
			switch toast.Type {
			case "success":
				icon, color = successIcon, t.Success
			case "warning":
				icon, color = warningIcon, t.Warning
			case "error":
				icon, color = errorIcon, t.Danger
			}
			parts := ui.ToastBase(c, toast)
			popoverPanel(c, parts.Root).Row().Width(min(360, width-32)).Gap(t.Space(3)).Padding(t.Space(3)).
				AlignItems(ui.Start).
				Transition(ui.ElementTransition{Enter: &ui.Motion{Y: 8}, Exit: &ui.Motion{Y: 4}}).
				Children(func() {
					ui.Icon(c, icon).Size(iconLarge, iconLarge).TextColor(color)
					ui.Column(c).Grow(1).MinWidth(0).Gap(t.Space(2)).Children(func() {
						ui.Text(c, toast.Title).FontSize(textBody).TextColor(t.Text)
						if toast.Description != "" {
							ui.Text(c, toast.Description).FontSize(textMeta).TextColor(t.TextMuted)
						}
						if toast.Action != "" {
							b := parts.ActionButton().Label(toast.Action).Height(heightControl).Padding(0, t.Space(2)).
								Radius(radiusControl).Border(1, t.Border).TextColor(t.Text).AlignItems(ui.Center)
							if b.Pressed() {
								b.Background(t.SurfacePressed)
							} else if b.Hovered() {
								b.Background(t.SurfaceHover)
							}
							b.Children(func() { ui.Text(c, toast.Action).FontSize(textBody) })
						}
					})
					close := parts.CloseButton().Label("Dismiss notification").Tooltip("Dismiss notification").
						Size(heightCompact, heightCompact).Radius(radiusControl).Center().TextColor(t.TextMuted)
					if close.Pressed() {
						close.Background(t.SurfacePressed)
					} else if close.Hovered() {
						close.Background(t.SurfaceHover)
					}
					close.Children(func() { ui.Icon(c, closeIcon).Size(iconInline, iconInline) })
				})
		}
	})
}
