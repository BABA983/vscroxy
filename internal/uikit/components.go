package uikit

import (
	"fmt"

	"github.com/baba983/vscroxy/locales"
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
			b.TextColor(t.Text)
		}
		switch {
		case b.Pressed():
			b.Background(t.SurfacePressed)
		case b.Hovered():
			b.Background(t.SurfaceHover)
		case active:
			b.Background(Colors(t).AccentTint)
		}
	} else {
		b.TextColor(Colors(t).TextFaint)
	}
	return b
}

func IconButton(c *ui.Context, label, tip string, icon *ui.SVG) ui.Element {
	return ToolbarButton(c, label, tip, false).Size(HeightControl, HeightControl).
		Padding(0).Justify(ui.Center).Children(func() {
		ui.Icon(c, icon).Size(IconInline, IconInline)
	})
}

func PrimaryButton(c *ui.Context, label string) ui.Element {
	return ui.PrimaryButton(c, label).Height(HeightControl).Padding(0, c.Theme().Space(3)).
		FontSize(TextBody).Radius(RadiusControl)
}

// SecondaryButton has no border: its face is the text color laid thinly
// over whatever is beneath, as macOS draws a push button, so it reads the
// same on the window, a panel or a selected row.
func SecondaryButton(c *ui.Context, label string) ui.Element {
	return faced(c, func() ui.Element { return ui.Button(c, label) }).Height(HeightControl).
		Padding(0, c.Theme().Space(3)).FontSize(TextBody).Radius(RadiusControl)
}

// CompactButton is a secondary button for dense rows and bars.
func CompactButton(c *ui.Context, label string) ui.Element {
	return faced(c, func() ui.Element { return ui.Button(c, label) }).Height(HeightCompact).
		Padding(0, c.Theme().Space(2)).FontSize(TextMeta).Radius(RadiusControl)
}

// DangerButton confirms a destructive action; it is never the only way out.
func DangerButton(c *ui.Context, label string) ui.Element {
	return SecondaryButton(c, label).TextColor(c.Theme().Danger)
}

// faced builds a MyGo control with the neutral face and no border. MyGo
// picks a control's resting, hover and pressed colors from the theme as
// it is built, so the theme is swapped only around the constructor and
// disabled controls keep MyGo's own handling.
func faced(c *ui.Context, build func() ui.Element) ui.Element {
	t := c.Theme()
	face := *t
	face.Surface, face.SurfaceHover, face.SurfacePressed = neutral(t, 1), neutral(t, 1.5), neutral(t, 2.2)
	face.Border = ui.Color{}
	c.SetTheme(&face)
	e := build()
	c.SetTheme(t)
	return e
}

// neutral is the text color at a low opacity: the face of a control, or
// level times that for its hover and pressed states.
func neutral(t *ui.Theme, level float32) ui.Color {
	alpha := float32(0.06)
	if t.Dark {
		alpha = 0.09
	}
	return t.Text.Alpha(alpha * level)
}

// RowButton is a full-width row in a list of things to pick: no face of its
// own until pressed, as rows in a macOS list do not light up under the
// pointer. Pair it with Selected for the picked row.
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
			ui.Text(c, "✓").FontSize(TextBody).FontWeight(600).TextColor(SwatchText)
		}
	})
}

// Input is a single-line text field at control height: a neutral fill
// with a hairline, and a soft accent halo instead of the focus ring while
// it has the focus.
func Input(c *ui.Context, value *string) ui.Element {
	in := InputControl(c, value)
	return FieldFace(c, in, in.Focused())
}

// InputControl is Input's text input without the field face, for a field that
// styles runs of its text before wrapping it with FieldFace. It keeps the
// control's height, padding and font, so the field looks like Input.
func InputControl(c *ui.Context, value *string) ui.Element {
	t := c.Theme()
	return ui.TextInput(c, value).Height(HeightControl).Padding(0, t.Space(2)).FontSize(TextBody).Radius(RadiusControl).FocusRing(false)
}

// FieldFace gives a text field, or a row holding one with its own controls
// (a search box, the URL bar), the field look: a neutral fill with a
// hairline, and an accent edge and halo while focused. A scroll container
// around a field leaves Space(1) at each side for that halo.
func FieldFace(c *ui.Context, field ui.Element, focused bool) ui.Element {
	t := c.Theme()
	field.Background(neutral(t, 0.6)).Border(1, t.Text.Alpha(0.1)).Radius(RadiusControl)
	if focused {
		field.Border(1, t.Accent).Shadow(0, 0, 0, 3, t.Accent.Alpha(0.25))
	}
	return field
}

// Select picks one of a few options at control height, with a button's face.
func Select(c *ui.Context, selected *string, options []string) ui.Element {
	return faced(c, func() ui.Element { return ui.Select(c, selected, options) }).
		Height(HeightControl).Padding(0, c.Theme().Space(2)).FontSize(TextBody).Radius(RadiusControl)
}

// TimingPhase is one part of a response's time, as the Timing tab colors it.
type TimingPhase struct {
	Name   string
	Millis int
	Color  ui.Color
}

// Timing is how a response's time divides into its phases, in order, and
// what the phases cannot name, as a reused connection; no phases for an
// exchange that never reached a server.
type Timing struct {
	Phases []TimingPhase
	Note   string
}

// ResponseHeadline keeps the complete status above its secondary facts: the
// duration, then the rest of detail. Narrow panes elide either line, with the
// full text available on hover; the duration's hover card splits it into its
// phases.
func ResponseHeadline(c *ui.Context, status string, ink ui.Color, duration, detail string, timing Timing) ui.Element {
	t := c.Theme()
	return ui.Column(c).Height(48).Shrink(0).Padding(t.Space(1.5), t.Space(3)).
		Justify(ui.Center).Label(locales.ResponseResult()).
		Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
		ui.Text(c, status).Font(FontMono).FontSize(TextHeadline).FixedLineHeight(22).
			TextColor(ink).SingleLine().Tooltip(status)
		ui.Row(c).MinWidth(0).Children(func() {
			d := Meta(c, duration).Font(FontMono).FontFeatures("tnum").FixedLineHeight(14).Shrink(0)
			if len(timing.Phases) > 0 {
				d.Description(locales.TimingHoverHelp())
				ui.TooltipBase(c, d, func(tip ui.Element) {
					tip.AttachTo(d, ui.AnchorBottomStart, ui.AnchorTopStart).Margin(t.Space(1.5), 0, 0, 0)
					timingCard(c, tip, duration, timing)
				})
			}
			if detail != "" {
				rest := " · " + detail
				Meta(c, rest).Font(FontMono).FontFeatures("tnum").FixedLineHeight(14).
					SingleLine().Shrink(1).MinWidth(0).Tooltip(detail)
			}
		})
	})
}

// timingCard lists the phases of a response's time: a bar of their shares,
// then each with its milliseconds, the total and what the phases leave out.
func timingCard(c *ui.Context, card ui.Element, total string, timing Timing) {
	t := c.Theme()
	sum := 0
	for _, p := range timing.Phases {
		sum += max(p.Millis, 0)
	}
	PopoverPanel(c, card).Width(240).Padding(t.Space(2.5)).Gap(t.Space(1.5)).Children(func() {
		ui.Row(c).FillWidth().Height(6).Radius(3).Background(t.Text.Alpha(0.08)).Children(func() {
			for _, p := range timing.Phases {
				if p.Millis > 0 {
					ui.Box(c).Grow(float32(p.Millis)).FillHeight().Background(p.Color)
				}
			}
			if sum == 0 {
				ui.Spacer(c)
			}
		})
		row := func(dot ui.Color, name, value string, bold bool) {
			ui.Row(c).FillWidth().Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(dot)
				label := ui.Text(c, name).FontSize(TextMeta).TextColor(t.Text).Grow(1)
				v := ui.Text(c, value).Font(FontMono).FontSize(TextMeta).FontFeatures("tnum").TextColor(t.Text)
				if bold {
					label.FontWeight(600)
					v.FontWeight(600)
				}
			})
		}
		for _, p := range timing.Phases {
			row(p.Color, p.Name, fmt.Sprintf("%d ms", p.Millis), false)
		}
		ui.Divider(c)
		row(ui.Color{}, "Total", total, true)
		if timing.Note != "" {
			Meta(c, timing.Note)
		}
	})
}

// Sparkline draws counts oldest first as a smooth line over a soft shade
// that fades to the baseline, scaled to the largest, in a box the size of
// a line of meta text; no counts draws it flat.
func Sparkline(c *ui.Context, counts []int, ink ui.Color) ui.Element {
	peak := 1
	for _, n := range counts {
		peak = max(peak, n)
	}
	return ui.Box(c).Size(56, 14).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		const stroke = 1.25
		bottom, height := r.Y+r.H-stroke/2, r.H-stroke
		if len(counts) < 2 {
			p.Line(r.X, bottom, r.X+r.W, bottom, stroke, ink)
			return
		}
		x := func(i int) float32 { return r.X + r.W*float32(i)/float32(len(counts)-1) }
		y := func(i int) float32 { return bottom - height*float32(counts[i])/float32(peak) }
		// Through the midpoints between samples, with each sample as the
		// control point: a curve that never overshoots below zero.
		var line ui.Path
		line.MoveTo(x(0), y(0))
		for i := 1; i < len(counts)-1; i++ {
			line.QuadTo(x(i), y(i), (x(i)+x(i+1))/2, (y(i)+y(i+1))/2)
		}
		last := len(counts) - 1
		line.LineTo(x(last), y(last))
		shade := line
		shade.LineTo(x(last), r.Y+r.H).LineTo(x(0), r.Y+r.H).Close()
		p.FillPathGradient(&shade, ui.LinearGradient{From: ink.Alpha(0.45), To: ink.Alpha(0.04), Angle: 180})
		p.StrokePath(&line, stroke, ink)
	})
}

// MenuSelect is Select for choices built as a menu: a button naming the
// current one, with the same face, that opens the menu.
func MenuSelect(c *ui.Context, current string, menu func(*ui.Menu)) ui.Element {
	return faced(c, func() ui.Element { return ui.PopoverMenuButton(c, current, menu) }).
		Height(HeightControl).Padding(0, c.Theme().Space(2)).FontSize(TextBody).Radius(RadiusControl)
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

// QueryChip is one condition in the query bar, written as the query syntax
// writes it. A click on its text runs edit, which turns it back into text
// to change; a condition with no syntax passes nil and has no such button.
// The × runs remove. Both run after the chip is built.
func QueryChip(c *ui.Context, text string, edit, remove func()) ui.Element {
	t := c.Theme()
	p := Colors(t)
	inner := float32(RadiusControl - 2)
	return ui.Row(c.Key("query-chip:"+text)).Height(HeightCompact).Shrink(0).Padding(0, 2).Gap(0).
		AlignItems(ui.Center).Radius(RadiusControl).Background(p.AccentTint).Children(func() {
		label := func() {
			ui.Text(c, text).Font(FontMono).FontSize(TextMeta).TextColor(p.Accent).SingleLine()
		}
		if edit == nil {
			ui.Row(c).Padding(0, t.Space(1)).Children(label)
		} else {
			b := ui.ButtonBase(c).Label("Edit condition: "+text).Tooltip("Edit "+text).
				Height(HeightCompact-4).Padding(0, t.Space(1)).Radius(inner).AlignItems(ui.Center)
			if b.Hovered() {
				b.Background(p.Accent.Alpha(0.12))
			}
			b.Children(label)
			if b.Clicked() {
				edit()
			}
		}
		x := ui.ButtonBase(c).Label("Remove condition: "+text).Tooltip("Remove "+text).
			Size(HeightCompact-4, HeightCompact-4).Radius(inner).Justify(ui.Center).AlignItems(ui.Center).TextColor(p.Accent)
		if x.Hovered() {
			x.Background(p.Accent.Alpha(0.12))
		}
		x.Children(func() { ui.Icon(c, closeIcon).Size(10, 10) })
		if x.Clicked() {
			remove()
		}
	})
}

func MethodLabel(c *ui.Context, method string) ui.Element {
	return ui.Text(c, method).Font(FontMono).FontSize(TextBody).
		TextColor(MethodColor(method, c.Theme())).SingleLine()
}

// Keep Sidebar's native source-list navigation and item identities, with
// the selection look. A sidebar's selection is where the window is, not a
// keyboard cursor, so its marker keeps the accent while focus is elsewhere.
//
// active is whether the window is the one in front: in a window behind
// others the chosen item turns gray, as in a macOS source list.
func SidebarItem(c *ui.Context, id string, icon *ui.SVG, label string, chosen, active bool) ui.Element {
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
		Selected(c, item, active)
	}
	return item
}

func SidebarSection(c *ui.Context, title string, open *bool, content func()) ui.Element {
	t := c.Theme()
	sectionTheme := *t
	sectionTheme.FontSize = TextMeta + 1 // SidebarSection uses FontSize - 1.
	c.SetTheme(&sectionTheme)
	section := ui.SidebarSection(c, title, open, func() {
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
		Heading(c, title).TextColor(t.Text)
		Hint(c, hint)
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
					close := parts.CloseButton().Label(locales.DismissNotification()).Tooltip(locales.DismissNotification()).
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
	return ui.Text(c, title).FontSize(TextTitle).FontWeight(600).TextColor(c.Theme().Text)
}

// Tabs is a row of text tabs; the chosen one is a rounded chip in the
// accent tint with the normal text color, as a selected row is. The prefix names each tab for assistive
// technology, so a screen with two tab rows of the same labels can name the
// one it means.
func Tabs(c *ui.Context, selected *int, prefix string, labels ...string) ui.Element {
	return tabs(c, selected, prefix, labels, nil, false)
}

// CountedTabs is Tabs with a quiet count after each label, as in "Headers 3".
// A count is presentation, not part of the tab's accessible name; an empty
// or zero count is left out.
func CountedTabs(c *ui.Context, selected *int, prefix string, labels, counts []string) ui.Element {
	return tabs(c, selected, prefix, labels, counts, false)
}

// SubTabs is a row of tabs inside what another tab row chose: the chosen
// one is only accent text, so the two levels never look alike.
func SubTabs(c *ui.Context, selected *int, prefix string, labels ...string) ui.Element {
	return tabs(c, selected, prefix, labels, nil, true)
}

func tabs(c *ui.Context, selected *int, prefix string, labels, counts []string, sub bool) ui.Element {
	t := c.Theme()
	p := Colors(t)
	group := ui.TabsBase(c, selected, len(labels))
	// As narrow as the underlined tabs were, so a narrow inspector still
	// shows them all; the chip's own padding keeps neighbours apart.
	group.List.Gap(t.Space(0.5)).Children(func() {
		for i, label := range labels {
			name := label
			if prefix != "" {
				name = prefix + ": " + label
			}
			chosen := i == *selected
			tab := group.Tab(i).Label(name).Height(HeightControl).Padding(0, t.Space(1.5)).Gap(t.Space(1)).
				Radius(RadiusControl).AlignItems(ui.Center).TextColor(t.TextMuted)
			switch {
			case chosen && sub:
				tab.TextColor(p.Accent)
			case chosen:
				tab.Background(p.AccentTint).TextColor(t.Text)
			case sub && (tab.Pressed() || tab.Hovered()):
				tab.TextColor(t.Text)
			case tab.Pressed():
				tab.Background(t.SurfacePressed)
			case tab.Hovered():
				tab.Background(t.SurfaceHover)
			}
			tab.Children(func() {
				ui.Text(c, label).FontSize(TextBody)
				if i < len(counts) && counts[i] != "" && counts[i] != "0" {
					Meta(c, counts[i]).FontFeatures("tnum")
				}
			})
		}
	})
	return group.List
}

// TabStrip scrolls a row of tabs sideways in a narrow pane. An edge that
// hides more tabs fades into the background, so a cut-off tab reads as
// more to scroll rather than a clipped label. The caller sizes it.
func TabStrip(c *ui.Context, tabs func()) ui.Element {
	t := c.Theme()
	strip := ui.Box(c).MinWidth(0)
	scroll := ui.Local(strip, "scroll", func() ui.ScrollState { return ui.ScrollState{} })
	return strip.Children(func() {
		ui.ScrollHorizontal(c).TrackScroll(scroll).Fill().Children(tabs)
		fade := func(from bool) ui.Element {
			// The fade only paints: a tab half under it still takes its click.
			edge := ui.Box(c).Absolute().Top(0).Bottom(0).Width(t.Space(6)).PassThrough()
			if from {
				return edge.Left(0).Gradient(t.Background, t.Background.Alpha(0), 90)
			}
			return edge.Right(0).Gradient(t.Background.Alpha(0), t.Background, 90)
		}
		if scroll.X > 0.5 {
			fade(true)
		}
		if scroll.X < scroll.MaxX-0.5 {
			fade(false)
		}
	})
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

// Segmented chooses one of a few mutually exclusive modes, as AppKit
// draws one: a recessed groove and a raised thumb, concentric with it.
// Tabs, not Segmented, switch what a pane shows.
func Segmented(c *ui.Context, selected *int, labels ...string) ui.Element {
	t := c.Theme()
	parts := ui.SegmentedBase(c, selected, len(labels))
	parts.Track.AlignItems(ui.Stretch).Height(HeightControl).Padding(t.Space(0.5)).
		Radius(RadiusControl).Background(neutral(t, 1)).Children(func() {
		for i, label := range labels {
			s := parts.Segment(i).Label(label).Padding(0, t.Space(3)).Radius(RadiusControl - t.Space(0.5)).
				AlignItems(ui.Center).FontSize(TextBody).TextColor(t.TextMuted)
			switch {
			case i == *selected && t.Dark:
				s.Background(neutral(t, 1.6)).TextColor(t.Text)
			case i == *selected:
				s.Background(t.Background).TextColor(t.Text).Shadow(0, 0.5, 1.5, 0, ui.RGBA(0, 0, 0, 0.16))
			case s.Pressed() || s.Hovered():
				s.TextColor(t.Text)
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

// SettingsPage is one category of a settings window, as in macOS's System
// Settings: its title, a sentence on what it holds, and a form of
// SettingsGroups, whose labels line up across the page. It keeps a reading
// width however wide the window is.
func SettingsPage(c *ui.Context, title, about string, groups func()) ui.Element {
	t := c.Theme()
	return ui.Column(c).FillWidth().MaxWidth(640).Gap(t.Space(5)).Children(func() {
		ui.Column(c).Gap(t.Space(1)).Children(func() {
			ui.Text(c, title).FontSize(TextHeadline).FontWeight(600)
			if about != "" {
				Hint(c, about)
			}
		})
		ui.Form(c, groups).Gap(t.Space(4))
	})
}

// SettingsGroup is a card of related fields under its legend.
func SettingsGroup(c *ui.Context, legend string, fields func()) ui.Element {
	t := c.Theme()
	return ui.Fieldset(c, legend, fields).Margin(0).Padding(t.Space(3), t.Space(4), t.Space(4), t.Space(4)).
		Background(neutral(t, 0.4)).Border(1, t.Text.Alpha(0.08)).Radius(RadiusPanel)
}

// SelectLabels is Select for values a screen names in its own language: the
// stored value stays option, while the trigger and the popup show label of
// it. The app uses it where an option is an identifier it compares, such as
// "Raw" or "In", and the screen says "原始" or "入".
func SelectLabels(c *ui.Context, selected *string, options []string, label func(string) string) ui.Element {
	t := c.Theme()
	sel := ui.SelectBase(c, selected)
	sel.Trigger.Justify(ui.SpaceBetween).MinWidth(t.Space(35)).Children(func() {
		ui.Text(c, label(*selected)).SingleLine()
		chevron(c)
	})
	sel.Popup(func(panel ui.Element) {
		PopoverPanel(c, panel.Padding(t.Space(1)).Gap(t.Space(0.5)))
		for _, option := range options {
			item := sel.Item(option).Padding(t.Space(1.5), t.Space(2.5)).Radius(RadiusControl)
			switch {
			case item.Highlighted():
				item.Background(t.Accent).TextColor(t.AccentText)
			case option == *selected:
				item.Background(t.Surface)
			}
			item.Children(func() { ui.Text(c, label(option)).FontSize(TextBody).SingleLine() })
		}
	})
	return faced(c, func() ui.Element { return sel.Trigger }).
		Height(HeightControl).Padding(0, t.Space(2)).FontSize(TextBody).Radius(RadiusControl)
}

// chevron draws the arrow of a control that opens something below it.
func chevron(c *ui.Context) {
	t := c.Theme()
	ui.Box(c).Size(t.Space(2.5), t.Space(2.5)).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		var path ui.Path
		path.MoveTo(r.X+r.W*0.1, r.Y+r.H*0.3).LineTo(r.X+r.W*0.5, r.Y+r.H*0.7).LineTo(r.X+r.W*0.9, r.Y+r.H*0.3)
		p.StrokePath(&path, 1.5, t.TextMuted)
	})
}
