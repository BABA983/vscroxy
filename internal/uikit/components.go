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
// The face is chosen as the button is built, so disable it through an
// ancestor (IsDisabled includes one); Disabled on the result comes too late
// to stop the hover face.
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

// CompactButton is a secondary button for dense rows and bars.
func CompactButton(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Height(HeightCompact).Padding(0, c.Theme().Space(2)).
		FontSize(TextMeta).Radius(RadiusControl)
}

// DangerButton confirms a destructive action; it is never the only way out.
func DangerButton(c *ui.Context, label string) ui.Element {
	t := c.Theme()
	return SecondaryButton(c, label).TextColor(t.Danger).Border(1, t.Danger)
}

// RowButton is a full-width row in a list of things to pick: no face of its
// own until hovered or pressed. Pair it with Selected for the picked row.
func RowButton(c *ui.Context, label string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Label(label).FillWidth().MinHeight(HeightControl).
		Padding(t.Space(1), t.Space(2)).Gap(t.Space(2)).Radius(RadiusControl).AlignItems(ui.Center).
		FontSize(TextBody).TextColor(t.Text)
	switch {
	case b.IsDisabled():
		b.TextColor(Colors(t).TextFaint)
	case b.Pressed():
		b.Background(t.SurfacePressed)
	case b.Hovered():
		b.Background(t.SurfaceHover)
	}
	return b
}

// SwatchButton picks a color; the chosen one carries a check mark, and a hovered
// one an outline, so the color itself stays unaltered.
func SwatchButton(c *ui.Context, label string, color ui.Color, chosen bool) ui.Element {
	b := ui.ButtonBase(c).Label(label).Tooltip(label).Checked(chosen).Size(HeightControl, HeightControl).
		Radius(RadiusControl).Background(color).Center()
	if b.Hovered() {
		b.Border(1, c.Theme().Text)
	}
	return b.Children(func() {
		if chosen {
			ui.Text(c, "✓").FontSize(TextBody).Bold().TextColor(SwatchText)
		}
	})
}

// Input is a single-line text field at control height.
func Input(c *ui.Context, value *string) ui.Element {
	return ui.TextInput(c, value).Height(HeightControl).FontSize(TextBody).Radius(RadiusControl)
}

// Select picks one of a few options at control height.
func Select(c *ui.Context, selected *string, options []string) ui.Element {
	return ui.Select(c, selected, options).Height(HeightControl).FontSize(TextBody).Radius(RadiusControl)
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
// a dialog: popovers and toasts. Modal has the deeper of the two shadows.
func PopoverPanel(c *ui.Context, panel ui.Element) ui.Element {
	t := c.Theme()
	return panel.Radius(RadiusPanel).Background(t.Background).Border(1, t.Border).
		Shadow(0, 8, 28, 0, shadow(t, 0.14))
}

// shadow is a soft, wide shadow as macOS draws under floating panels, with
// the hairline border doing the edge. A dark window needs it darker to show.
func shadow(t *ui.Theme, alpha float32) ui.Color {
	if t.Dark {
		alpha *= 2.5
	}
	return ui.RGBA(0, 0, 0, alpha)
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

// Keep Sidebar's native source-list navigation and item identities, with
// the selection look. A sidebar's selection is where the window is, not a
// keyboard cursor, so its marker keeps the accent while focus is elsewhere.
func SidebarItem(c *ui.Context, id string, icon *ui.SVG, label string, chosen bool) ui.Element {
	t := c.Theme()
	neutral := *t
	// Sidebar draws icons in Accent and the selection in Accent/AccentText,
	// or SurfacePressed without the focus.
	// Sidebar also takes a chosen item's icon from Accent while it lacks the
	// focus, so the fill is set after construction rather than through Accent.
	neutral.Accent, neutral.AccentText = t.TextMuted, t.Text
	c.SetTheme(&neutral)
	item := ui.SidebarItem(c, id, icon, label).Height(HeightControl).
		FontSize(TextBody).Padding(0, t.Space(2)).Radius(RadiusControl)
	c.SetTheme(t)
	if chosen {
		Selected(c, item, true)
	}
	return item
}

func SidebarSection(c *ui.Context, title string, open *bool, content func()) ui.Element {
	t := c.Theme()
	sectionTheme := *t
	sectionTheme.FontSize = TextMeta + 1 // SidebarSection uses FontSize - 1.
	c.SetTheme(&sectionTheme)
	section := ui.SidebarSection(c, strings.ToUpper(title), open, func() {
		c.SetTheme(t)
		content()
	}).Margin(t.Space(2), 0, 0, 0)
	c.SetTheme(t)
	return section
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
			MaxWidth(width-40).MaxHeight(height-40).Shadow(0, 20, 56, 0, shadow(t, 0.2))
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
	return CountedTabs(c, selected, prefix, labels, nil)
}

// CountedTabs is Tabs with a quiet count after each label, as in "Headers 3".
// A count is presentation, not part of the tab's accessible name; an empty
// or zero count is left out.
func CountedTabs(c *ui.Context, selected *int, prefix string, labels, counts []string) ui.Element {
	t := c.Theme()
	group := ui.TabsBase(c, selected, len(labels))
	group.List.Gap(t.Space(0.5)).Children(func() {
		for i, label := range labels {
			name := label
			if prefix != "" {
				name = prefix + ": " + label
			}
			tab := group.Tab(i).Label(name).Height(HeightTabs).Padding(0, t.Space(1.5)).Gap(t.Space(1)).Radius(0).AlignItems(ui.Center).TextColor(t.TextMuted)
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
				if i < len(counts) && counts[i] != "" && counts[i] != "0" {
					Meta(c, counts[i]).FontFeatures("tnum")
				}
			})
		}
	})
	return group.List
}

// MoreMenu is the quiet "⋯" button that opens a menu of further actions;
// label names it for assistive technology and in its tooltip.
func MoreMenu(c *ui.Context, label string, menu func(*ui.Menu)) ui.Element {
	t := c.Theme()
	return ui.PopoverMenuButton(c, "⋯", menu).Label(label).Tooltip(label).Height(HeightCompact).
		Padding(0, t.Space(2)).FontSize(TextBody).TextColor(t.TextMuted).Border(0, t.Border)
}

// Glyph is a quiet inline icon that marks rather than acts: a folder, a pin,
// a note.
func Glyph(c *ui.Context, icon *ui.SVG) ui.Element {
	return ui.Icon(c, icon).Size(IconInline, IconInline).TextColor(c.Theme().TextMuted)
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
			// Concentric with the track: its radius less the track's padding.
			s := parts.Segment(i).Label(label).Padding(0, t.Space(3)).Radius(RadiusControl - t.Space(0.5)).
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

// Selection is a tinted fill with the row's normal text colors. focused is
// whether the list holding the row has the keyboard focus; without it the
// fill turns to the hover color, so the row still shows which one is
// selected without claiming the keyboard.
//
// Rows that pick a place or a thing (sidebars, trees, pick lists) are
// rounded blocks inset from the edges, as in a macOS source list: Selected.
// Rows of data in a MyGo table or list span its width and add a marker line
// at the leading edge, which keeps a selected row apart from a hovered one
// once the fill turns to hover: ListTheme and RowMarker.

// ListTheme is the theme a MyGo table, list or tree paints its selection
// from. Scope it to the list with SetTheme; rows draw RowMarker themselves.
func ListTheme(t *ui.Theme, focused bool) *ui.Theme {
	p := Colors(t)
	list := *t
	list.Accent, list.AccentText = p.AccentTint, t.Text
	if !focused {
		list.Accent = p.Hover
	}
	return &list
}

// marker paints the selection marker on the leading edge of r.
func marker(t *ui.Theme, focused bool) func(p *ui.Painter, r ui.Rect) {
	// The palette's accent, not t.Accent: inside ListTheme that is the fill.
	color := Colors(t).Accent
	if !focused {
		color = t.TextMuted
	}
	return func(p *ui.Painter, r ui.Rect) {
		inset := min(t.Space(1.5), r.H/4)
		p.Fill(ui.Rect{X: r.X, Y: r.Y + inset, W: 2, H: r.H - 2*inset}, color, 1)
	}
}

// RowMarker adds the marker to the row being built by a MyGo list or table,
// whose own selection fill comes from ListTheme.
func RowMarker(c *ui.Context, focused bool) {
	ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Width(4).Draw(marker(c.Theme(), focused))
}

// Selected gives a rounded row the selection look.
func Selected(c *ui.Context, e ui.Element, focused bool) ui.Element {
	t := c.Theme()
	fill := Colors(t).AccentTint
	if !focused {
		fill = Colors(t).Hover
	}
	return e.Background(fill).TextColor(t.Text)
}
