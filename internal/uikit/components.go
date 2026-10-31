package uikit

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// Icons the components draw themselves, from the app's one Lucide set.
var (
	emptyIcon   = lucide.LayoutList()
	infoIcon    = lucide.Info()
	successIcon = lucide.CircleCheck()
	warningIcon = lucide.TriangleAlert()
	errorIcon   = lucide.CircleAlert()
	closeIcon   = lucide.X()
)

// ButtonBase keeps native keyboard, focus-ring and accessibility behavior.
// IsDisabled includes a disabled ancestor, so disabled controls never hover.
func ToolbarButton(c *ui.Context, label, tip string, active bool) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Label(label).Tooltip(tip).Height(HeightControl).
		Padding(0, t.Space(2)).Gap(t.Space(2)).Radius(RadiusControl).
		AlignItems(ui.Center).FontSize(TextBody).TextColor(t.TextMuted)
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
		b.TextColor(Colors(t).TextFaint)
	}
	return b
}

func IconButton(c *ui.Context, label, tip string, icon *ui.SVG) ui.Element {
	return ToolbarButton(c, label, tip, false).Size(HeightCompact, HeightCompact).
		Padding(0).Justify(ui.Center).Children(func() {
		ui.Icon(c, icon).Size(IconInline, IconInline)
	})
}

func PrimaryButton(c *ui.Context, label string) ui.Element {
	return ui.PrimaryButton(c, label).Height(HeightControl).Padding(0, c.Theme().Space(3)).
		FontSize(TextBody).Radius(RadiusControl)
}

func SecondaryButton(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Height(HeightControl).Padding(0, c.Theme().Space(3)).
		FontSize(TextBody).Radius(RadiusControl)
}

// Dialog content uses three width tiers inside Modal's shared padding.
// Callers choose the height; long content scrolls without moving the footer.
func DialogColumn(c *ui.Context, width float32) ui.Element {
	w, h := c.Size()
	return ui.Column(c).Width(min(width, w-80)).MaxHeight(h - 80).MinHeight(0).Gap(c.Theme().Space(4))
}

func DialogFooter(c *ui.Context) ui.Element {
	t := c.Theme()
	return ui.Row(c).FillWidth().Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Justify(ui.End).
		Padding(t.Space(3), 0, 0, 0).Border(0, t.Border).BorderWidth(1, 0, 0, 0)
}

// PopoverPanel is the chrome of anything floating above the window short of
// a dialog: popovers and toasts. Modal has the heavier of the two shadows.
func PopoverPanel(c *ui.Context, panel ui.Element) ui.Element {
	t := c.Theme()
	return panel.Radius(RadiusPanel).Background(t.Background).Border(1, t.Border).
		Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.22))
}

// StatusPill tints only what calls for attention: a quiet status (2xx, 304,
// no response) is plain muted text with the same padding.
func StatusPill(c *ui.Context, label string, foreground ui.Color) ui.Element {
	t := c.Theme()
	pill := ui.Text(c, label).FontSize(TextBody).FontFeatures("tnum").SingleLine().
		TextColor(foreground).Padding(t.Space(1), t.Space(2)).Radius(RadiusControl)
	if foreground != Colors(t).TextMuted {
		pill.Background(foreground.Alpha(0.12))
	}
	return pill
}

func MethodLabel(c *ui.Context, method string) ui.Element {
	return ui.Text(c, method).Font(FontMono).FontSize(TextBody).
		TextColor(MethodColor(method, c.Theme())).SingleLine()
}

// Keep Sidebar's native source-list navigation and item identities. Only
// unselected icons use a neutral accent; selected items retain native colors.
func SidebarItem(c *ui.Context, id string, icon *ui.SVG, label string, chosen bool) ui.Element {
	t := c.Theme()
	neutral := *t
	if !chosen {
		neutral.Accent = t.TextMuted
	}
	c.SetTheme(&neutral)
	item := ui.SidebarItem(c, id, icon, label).Height(HeightControl).
		FontSize(TextBody).Padding(0, t.Space(2)).Radius(RadiusControl)
	c.SetTheme(t)
	return item
}

func SidebarSection(c *ui.Context, title string, open *bool, content func()) {
	t := c.Theme()
	sectionTheme := *t
	sectionTheme.FontSize = TextMeta + 1 // SidebarSection uses FontSize - 1.
	c.SetTheme(&sectionTheme)
	ui.SidebarSection(c, strings.ToUpper(title), open, func() {
		c.SetTheme(t)
		content()
	}).Margin(t.Space(2), 0, 0, 0)
	c.SetTheme(t)
}

func EmptyState(c *ui.Context, title, hint string, actions func()) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Padding(t.Space(6)).Gap(t.Space(2)).Children(func() {
		ui.Icon(c, emptyIcon).Size(IconLarge, IconLarge).TextColor(t.TextMuted).Margin(0, 0, t.Space(2), 0)
		ui.Text(c, title).FontSize(TextTitle).TextColor(t.Text)
		ui.Text(c, hint).FontSize(TextBody).TextColor(t.TextMuted)
		if actions != nil {
			actions()
		}
	})
}

// Read-only key/value rows preserve wrapping and existing value actions.
func KVRow(c *ui.Context, content func()) ui.Element {
	t := c.Theme()
	return ui.Row(c).Gap(t.Space(3)).Padding(t.Space(1), t.Space(3)).
		MinHeight(HeightControl).AlignItems(ui.Start).FillWidth().Children(content)
}

// Only the look is ours: MyGo owns announcements, timeouts, pause-on-hover,
// the three-toast limit, action dispatch and keyboard dismissal.
func Toasts(c *ui.Context) {
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
			PopoverPanel(c, parts.Root).Row().Width(min(360, width-32)).Gap(t.Space(3)).Padding(t.Space(3)).
				AlignItems(ui.Start).
				Transition(ui.ElementTransition{Enter: &ui.Motion{Y: 8}, Exit: &ui.Motion{Y: 4}}).
				Children(func() {
					ui.Icon(c, icon).Size(IconLarge, IconLarge).TextColor(color)
					ui.Column(c).Grow(1).MinWidth(0).Gap(t.Space(2)).Children(func() {
						ui.Text(c, toast.Title).FontSize(TextBody).TextColor(t.Text)
						if toast.Description != "" {
							ui.Text(c, toast.Description).FontSize(TextMeta).TextColor(t.TextMuted)
						}
						if toast.Action != "" {
							b := parts.ActionButton().Label(toast.Action).Height(HeightControl).Padding(0, t.Space(2)).
								Radius(RadiusControl).Border(1, t.Border).TextColor(t.Text).AlignItems(ui.Center)
							if b.Pressed() {
								b.Background(t.SurfacePressed)
							} else if b.Hovered() {
								b.Background(t.SurfaceHover)
							}
							b.Children(func() { ui.Text(c, toast.Action).FontSize(TextBody) })
						}
					})
					close := parts.CloseButton().Label("Dismiss notification").Tooltip("Dismiss notification").
						Size(HeightCompact, HeightCompact).Radius(RadiusControl).Center().TextColor(t.TextMuted)
					if close.Pressed() {
						close.Background(t.SurfacePressed)
					} else if close.Hovered() {
						close.Background(t.SurfaceHover)
					}
					close.Children(func() { ui.Icon(c, closeIcon).Size(IconInline, IconInline) })
				})
		}
	})
}

// DialogBase retains the library's modal focus and dismissal rules; only
// the shared desktop chrome is ours.
func Modal(c *ui.Context, open *bool, content func()) ui.Element {
	t := c.Theme()
	width, height := c.Size()
	dialog := ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.45))
		panel.Padding(t.Space(5)).Gap(t.Space(3)).Radius(RadiusDialog).Background(t.Background).Border(1, t.Border).
			MaxWidth(width-40).MaxHeight(height-40).Shadow(0, 16, 40, 0, ui.RGBA(0, 0, 0, 0.3))
		content()
	})
	if dialog.Dismissed() {
		// MyGo v0.7.3 restores the opener after painting. Render its focus
		// ring even when no hover or animation would request another frame.
		c.Invalidate()
	}
	return dialog
}

func DialogTitle(c *ui.Context, title string) ui.Element {
	return ui.Text(c, title).FontSize(TextTitle).Bold().TextColor(c.Theme().Text)
}

// Tabs is a row of plain text tabs with an underline under the chosen
// one. The prefix names each tab for assistive technology, so a screen with
// two tab rows of the same labels can name the one it means.
func Tabs(c *ui.Context, selected *int, prefix string, labels ...string) ui.Element {
	t := c.Theme()
	group := ui.TabsBase(c, selected, len(labels))
	group.List.Gap(t.Space(0.5)).Children(func() {
		for i, label := range labels {
			name := label
			if prefix != "" {
				name = prefix + ": " + label
			}
			tab := group.Tab(i).Label(name).Height(HeightTabs).Padding(0, t.Space(2)).Radius(0).AlignItems(ui.Center).TextColor(t.TextMuted)
			if i == *selected {
				tab.TextColor(t.Text).Border(0, t.Accent).BorderWidth(0, 0, 2, 0)
			}
			if tab.Pressed() {
				tab.Background(t.SurfacePressed)
			} else if tab.Hovered() {
				tab.Background(t.SurfaceHover)
			}
			tab.Children(func() {
				text := ui.Text(c, label).FontSize(TextBody)
				if i == *selected {
					text.Bold()
				}
			})
		}
	})
	return group.List
}

// Segmented chooses one of a few mutually exclusive modes. The track sits on
// the panel color so the selected segment reads as raised in both themes;
// MyGo's own Segmented draws both from Surface, the control face.
func Segmented(c *ui.Context, selected *int, labels ...string) ui.Element {
	t := c.Theme()
	p := Colors(t)
	parts := ui.SegmentedBase(c, selected, len(labels))
	parts.Track.AlignItems(ui.Stretch).Height(HeightControl).Padding(t.Space(0.5)).Gap(t.Space(0.5)).
		Radius(RadiusControl).Background(p.Panel).Border(1, t.Border).Children(func() {
		for i, label := range labels {
			s := parts.Segment(i).Label(label).Padding(0, t.Space(3)).Radius(RadiusControl).
				AlignItems(ui.Center).FontSize(TextBody).TextColor(t.TextMuted)
			switch {
			case i == *selected && t.Dark:
				s.Background(p.Pressed).TextColor(t.Text)
			case i == *selected:
				s.Background(t.Background).TextColor(t.Text).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.12))
			case s.Pressed():
				s.Background(p.Pressed)
			case s.Hovered():
				s.Background(p.Hover)
			}
			s.Children(func() { ui.Text(c, label).SingleLine() })
		}
	})
	return parts.Track
}
