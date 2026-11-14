package uikit

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// Icons the components draw themselves, from the app's one Lucide set.
var (
	emptyIcon   = lucide.LayoutList()
	searchIcon  = lucide.Search()
	chevronIcon = lucide.ChevronDown()
	disclosure  = lucide.ChevronRight()
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
	t, p := c.Theme(), Colors(c.Theme())
	b := Tip(c, ui.ButtonBase(c).Label(label), tip).Height(ControlHeight).
		Padding(0, Scaled(ControlPaddingX)).Gap(t.Space(2)).Radius(RadiusControl).
		AlignItems(ui.Center).FontSize(Scaled(TextBody)).TextColor(t.TextMuted)
	if !b.IsDisabled() {
		if active {
			b.TextColor(t.Text)
		}
		switch {
		case b.Pressed():
			b.Background(p.FillPressed)
		case b.Hovered():
			b.Background(p.FillHover)
		case active:
			b.Background(p.AccentTint)
		}
	}
	// A disabled control keeps its own text color: MyGo already draws disabled
	// elements at half opacity, and painting the faint color as well dimmed
	// them twice.
	return hoverMotion(b, b.Hovered() || b.Pressed())
}

// FooterButton is one action in the footer bar: 20 points high, at rest text
// with no face of its own. The pointer brings a face, and being on brings the
// same raised face a chosen segment has, so "on" and "under the pointer"
// never read alike.
func FooterButton(c *ui.Context, label, tip string, on bool) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	b := Tip(c, ui.ButtonBase(c).Label(label), tip).Height(ControlHeightSmall).
		Padding(0, Scaled(ControlPaddingXSmall)).Radius(RadiusInner).AlignItems(ui.Center).FontSize(Scaled(TextMeta)).
		TextColor(t.Text)
	b.Children(func() { ui.Text(c, label).FontSize(Scaled(TextMeta)) })
	switch {
	case b.Pressed():
		b.Background(p.FillPressed)
	case b.Hovered():
		b.Background(p.FillHover)
	case on:
		// The selected segment's face: raised canvas with its hairline.
		b.Background(p.Canvas).InsetShadow(0, 0, 0, 1, p.Hairline)
	}
	return hoverMotion(b, b.Hovered() || b.Pressed())
}

func IconButton(c *ui.Context, label, tip string, icon *ui.SVG) ui.Element {
	return ToolbarButton(c, label, tip, false).Size(ControlHeight, ControlHeight).
		Padding(0).Justify(ui.Center).Children(func() {
		ui.Icon(c, icon).Size(IconInline, IconInline)
	})
}

// ButtonState is one of the faces a button can wear. It lets a gallery or a
// documentation plate draw a face without moving the pointer or holding the
// button down.
type ButtonState int

const (
	ButtonRest ButtonState = iota
	ButtonHover
	ButtonPressed
)

// PrimaryButton is the one action a screen or dialog is for. Green is the
// accent and nothing else is filled with it.
func PrimaryButton(c *ui.Context, label string) ui.Element {
	return PrimaryButtonState(c, label, ButtonRest)
}

// PrimaryButtonState draws the primary button with one of its faces. The
// constructor still owns the geometry and label; only the theme's accent
// colors change, so a plate shows exactly what the pointer would.
func PrimaryButtonState(c *ui.Context, label string, state ButtonState) ui.Element {
	t := c.Theme()
	face := *t
	switch state {
	case ButtonHover:
		face.Accent, face.AccentHover, face.AccentPressed = t.AccentHover, t.AccentHover, t.AccentHover
	case ButtonPressed:
		face.Accent, face.AccentHover, face.AccentPressed = t.AccentPressed, t.AccentPressed, t.AccentPressed
	}
	c.SetTheme(&face)
	b := ui.PrimaryButton(c, label).Height(ControlHeight).Padding(0, Scaled(ControlPaddingX)).
		FontSize(Scaled(TextBody)).FontWeight(500).Radius(RadiusControl)
	c.SetTheme(t)
	return b
}

// SecondaryButton has no border: its face is the text color laid thinly
// over whatever is beneath, so it reads the same on the window, a panel or
// a selected row.
func SecondaryButton(c *ui.Context, label string) ui.Element {
	return faced(c, func() ui.Element { return ui.Button(c, label) }).Height(ControlHeight).
		Padding(0, Scaled(ControlPaddingX)).FontSize(Scaled(TextBody)).Radius(RadiusControl)
}

// ChipButton is a control with the neutral face and no text of its own: the
// caller builds what it shows, as an icon and a shortcut. It reads on every
// surface, as every control does.
func ChipButton(c *ui.Context, label, tip string) ui.Element {
	t := c.Theme()
	return faced(c, func() ui.Element { return Tip(c, ui.ButtonBase(c).Label(label), tip) }).
		Height(ControlHeightSmall).Padding(0, Scaled(ControlPaddingXSmall)).Gap(t.Space(1.5)).Radius(RadiusControl).AlignItems(ui.Center)
}

// CompactButton is a secondary button for dense rows and bars: the same
// face at meta size.
func CompactButton(c *ui.Context, label string) ui.Element {
	return faced(c, func() ui.Element { return ui.Button(c, label) }).Height(ControlHeightSmall).
		Padding(0, Scaled(ControlPaddingXSmall)).FontSize(Scaled(TextMeta)).Radius(RadiusControl)
}

// LinkButton is an inline action, such as "+ Add property": text at the
// small control height with no face until the pointer arrives.
func LinkButton(c *ui.Context, label string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	b := Tip(c, ui.ButtonBase(c).Label(label), label).Height(ControlHeightSmall).
		Padding(0, Scaled(ControlPaddingXSmall)).Radius(RadiusInner).AlignItems(ui.Center).
		FontSize(Scaled(TextMeta)).TextColor(t.TextMuted)
	b.Children(func() { ui.Text(c, label).FontSize(Scaled(TextMeta)) })
	if b.Hovered() {
		b.Background(p.FillHover).TextColor(t.Text)
	}
	return b
}

// NumberField edits a whole number with its unit beside it, as [60] s. The
// text holds the value across builds so a half-typed number is not replaced
// under the caret. It is the protocol editors' number control.
func NumberField(c *ui.Context, text *string, value *float64, lo, hi float64, unit, label, tip string, changed func()) ui.Element {
	t := c.Theme()
	row := ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center)
	row.Children(func() {
		in := Input(c, text).Label(label).Width(88).Font(FontMono)
		if tip != "" {
			in = Tip(c, in, tip)
		}
		if in.Changed() {
			if v, err := strconv.ParseFloat(strings.TrimSpace(*text), 64); err == nil && v >= lo && v <= hi {
				*value = v
				if changed != nil {
					changed()
				}
			}
		}
		if !in.Focused() {
			*text = strconv.Itoa(int(*value))
		}
		if unit != "" {
			Meta(c, unit)
		}
	})
	return row
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
	t, p := c.Theme(), Colors(c.Theme())
	face := *t
	face.Surface, face.SurfaceHover, face.SurfacePressed = p.Fill, p.FillHover, p.FillPressed
	face.Border = ui.Color{}
	c.SetTheme(&face)
	e := build()
	c.SetTheme(t)
	return e
}

// RowButton is a full-width row in a list of things to pick: no face of its
// own until pressed, as rows in a macOS list do not light up under the
// pointer. Pair it with Selected for the picked row.
func RowButton(c *ui.Context, label string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	b := ui.ButtonBase(c).Label(label).FillWidth().MinHeight(ControlHeight).
		Padding(Scaled(4), Scaled(ControlPaddingXSmall)).Gap(t.Space(2)).Radius(RadiusControl).AlignItems(ui.Center).
		FontSize(Scaled(TextBody)).TextColor(t.Text)
	switch {
	case b.IsDisabled():
	case b.Pressed():
		b.Background(p.FillPressed)
	}
	return b
}

// SwatchButton picks a color; the chosen one carries a check mark, and a hovered
// one an outline, so the color itself stays unaltered.
func SwatchButton(c *ui.Context, label string, color ui.Color, chosen bool) ui.Element {
	b := Tip(c, ui.ButtonBase(c).Label(label), label).Checked(chosen).Size(ControlHeight, ControlHeight).
		Radius(RadiusControl).Background(color).Center()
	if b.Hovered() {
		b.Border(1, c.Theme().Text)
	}
	return b.Children(func() {
		if chosen {
			ui.Text(c, "✓").FontSize(Scaled(TextBody)).FontWeight(500).TextColor(SwatchText)
		}
	})
}

// Input is a single-line text field at control height: a well set into the
// canvas, with an accent edge and a soft tint halo while it has the focus.
func Input(c *ui.Context, value *string) ui.Element {
	in := InputControl(c, value)
	return FieldFace(c, in, in.Focused())
}

// InputControl is Input's text input without the field face, for a field that
// styles runs of its text before wrapping it with FieldFace. It keeps the
// control's height, padding and font, so the field looks like Input.
func InputControl(c *ui.Context, value *string) ui.Element {
	return ui.TextInput(c, value).Height(ControlHeight).Padding(0, Scaled(ControlPaddingX)).FontSize(Scaled(TextBody)).Radius(RadiusControl).FocusRing(false)
}

// FieldFace gives a text field, or a row holding one with its own controls
// (a search box, the URL bar), the face a field has on the canvas: a well,
// and a muted edge and tint halo while focused. The edge is a neutral rather
// than the ink accent: a field's focus is a place in a form, not the screen's
// one primary action, so it reads quieter than the button beside it while
// staying well above 3:1 against both the field and the surface around it.
// A scroll container around a field leaves Space(1) at each side for the halo.
func FieldFace(c *ui.Context, field ui.Element, focused bool) ui.Element {
	return fieldFace(Colors(c.Theme()), field, false, focused)
}

// ChromeFieldFace is FieldFace for a field on chrome: the canvas face with
// a hairline, so it stays lighter than the bar it sits in.
func ChromeFieldFace(c *ui.Context, field ui.Element, focused bool) ui.Element {
	return fieldFace(Colors(c.Theme()), field, true, focused)
}

// SearchField is a one-line field that finds something in a list, with the
// magnifier that says so. onChrome draws it as a field on chrome: the canvas
// with a hairline rather than the well it is on the canvas.
func SearchField(c *ui.Context, value *string, label, placeholder string, onChrome bool) ui.Element {
	t := c.Theme()
	field := ui.Row(c).Height(ControlHeight).AlignItems(ui.Center).Gap(t.Space(1)).Padding(0, t.Space(2))
	var in ui.Element
	field.Children(func() {
		ui.Icon(c, searchIcon).Size(IconInline, IconInline).TextColor(t.TextMuted)
		// The face is the row's: the input inside carries no border or fill
		// of its own, so a hairline is drawn once.
		in = ui.TextInput(c, value).Height(ControlHeight).Padding(0).FontSize(Scaled(TextBody)).Grow(1).MinWidth(0).
			FocusRing(false).Background(ui.Color{}).Border(0, ui.Color{}).
			Label(label).Placeholder(placeholder)
	})
	if onChrome {
		ChromeFieldFace(c, field, in.Focused())
	} else {
		FieldFace(c, field, in.Focused())
	}
	return field
}

// fieldFace draws a field's face inside its own edges, so its box stays the
// control height whether or not it has the focus.
func fieldFace(p *Palette, field ui.Element, onChrome, focused bool) ui.Element {
	face := p.Well
	if onChrome {
		face = p.Canvas
	}
	// The face's radius also clips what sits inside it: a method control's
	// hover or a chip's fill follows the field's corners instead of squaring
	// them off.
	field.Radius(RadiusControl).Background(face).Clip()
	switch {
	case focused:
		field.InsetShadow(0, 0, 0, 1, p.TextMuted).Shadow(0, 0, 0, 3, p.AccentTint)
	case onChrome:
		field.InsetShadow(0, 0, 0, 1, p.Hairline)
	}
	return field
}

// Select picks one of a few options at control height, with a button's face.
func Select(c *ui.Context, selected *string, options []string) ui.Element {
	return faced(c, func() ui.Element { return ui.Select(c, selected, options) }).
		Height(ControlHeight).Padding(0, Scaled(ControlPaddingX)).FontSize(Scaled(TextBody)).Radius(RadiusControl)
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

// ResponseMeta is what a response reports in its pane header, at the end of
// the row: how long it took, then its size and type. Narrow panes elide it,
// with the full text on hover; the duration's hover card splits it into its
// phases.
func ResponseMeta(c *ui.Context, duration, detail string, timing Timing) ui.Element {
	t := c.Theme()
	return ui.Row(c).MinWidth(0).Shrink(1).AlignItems(ui.Center).Label(locales.ResponseResult()).Children(func() {
		d := Tip(c, Meta(c, duration).Font(FontMono).FixedLineHeight(14).Shrink(0), duration)
		if len(timing.Phases) > 0 {
			d.Description(locales.TimingHoverHelp())
			ui.TooltipBase(c, d, func(tip ui.Element) {
				tip.AttachTo(d, ui.AnchorBottomStart, ui.AnchorTopStart).Margin(t.Space(1.5), 0, 0, 0)
				timingCard(c, tip, duration, timing)
			})
		}
		if detail != "" {
			Tip(c, Meta(c, " · "+detail).Font(FontMono).FixedLineHeight(14).
				SingleLine().Shrink(1).MinWidth(0), detail)
		}
	})
}

// timingCard lists the phases of a response's time: a bar of their shares,
// then each with its milliseconds, the total and what the phases leave out.
func timingCard(c *ui.Context, card ui.Element, total string, timing Timing) {
	t, pal := c.Theme(), Colors(c.Theme())
	sum := 0
	for _, p := range timing.Phases {
		sum += max(p.Millis, 0)
	}
	PopoverPanel(c, card).Width(240).Padding(t.Space(2.5)).Gap(t.Space(1.5)).Children(func() {
		ui.Row(c).FillWidth().Height(6).Radius(3).Background(pal.Fill).Children(func() {
			for _, p := range timing.Phases {
				if p.Millis > 0 {
					ui.Box(c).Grow(float32(p.Millis)).FillHeight().Background(p.Color)
				}
			}
			if sum == 0 {
				ui.Spacer(c)
			}
		})
		row := func(dot ui.Color, name, value string, strong bool) {
			ui.Row(c).FillWidth().Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(dot)
				label := ui.Text(c, name).FontSize(Scaled(TextMeta)).TextColor(t.Text).Grow(1)
				v := ui.Text(c, value).Font(FontMono).FontSize(Scaled(TextMeta)).FontFeatures("tnum").TextColor(t.Text)
				if strong {
					label.FontWeight(500)
					v.FontWeight(500)
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
// current one, with the same face, that opens the menu. The menu wears the
// kit's floating panel, as every popover does, and arrives the same way.
func MenuSelect(c *ui.Context, current string, menu func(*ui.Menu)) ui.Element {
	panel := func(panel ui.Element) {
		popoverFace(c, panel)
		panel.Transition(popoverMotion())
	}
	return faced(c, func() ui.Element {
		return ui.PopoverMenuButton(c, current, menu, ui.PopoverMenuButtonOptions{Panel: panel})
	}).Height(HeightControl).Padding(0, c.Theme().Space(2)).FontSize(Scaled(TextBody)).Radius(RadiusControl)
}

// popoverFace gives a floating panel the kit's chrome: the canvas on the
// shadow, edged by a hairline. PopoverPanel builds it for the panels the app
// draws; a menu's panel comes from MyGo and takes the same face here.
func popoverFace(c *ui.Context, panel ui.Element) {
	p := Colors(c.Theme())
	panel.Background(p.Canvas).Border(1, p.Hairline).Radius(RadiusPanel).
		NoShadow().Shadow(0, 8, 28, 0, p.Shadow)
}

// Tip gives e a tip of the kit's look, as a popover has: the canvas on the
// shadow, a hairline edge, the panel radius, and the popover's rise. It
// describes e to assistive technology as MyGo's Tooltip does.
func Tip(c *ui.Context, e ui.Element, text string) ui.Element {
	if text == "" {
		return e
	}
	t, p := c.Theme(), Colors(c.Theme())
	ui.TooltipBase(c, e, func(tip ui.Element) {
		tip.Background(p.Inverse).TextColor(p.InverseText).Radius(RadiusControl).
			MaxWidth(Scaled(360)).Padding(t.Space(1.5), t.Space(2.5)).Transition(popoverMotion())
		tip.Children(func() {
			body := text
			font := FontUI
			if strings.Contains(text, "://") || strings.Contains(text, " ") && strings.Count(text, " ") < 2 && len(text) > 60 {
				font = FontMono
				body = breakLong(text)
			}
			ui.Text(c, body).Font(font).FontSize(Scaled(TextMeta)).TextColor(p.InverseText).
				MaxLines(8).LineHeight(LineProse)
		})
	})
	e.Description(text)
	return e
}

// breakLong gives a long string places to wrap. A URL gets a zero-width
// space after the characters that already divide it; only a run with none of
// them is broken after 32 characters.
func breakLong(s string) string {
	const limit = 32
	var b strings.Builder
	run := 0
	for _, r := range s {
		b.WriteRune(r)
		if r == '\u200b' {
			run = 0
			continue
		}
		run++
		if strings.ContainsRune("/?&=-_.", r) || run >= limit {
			b.WriteRune('\u200b')
			run = 0
		}
	}
	return strings.TrimSuffix(b.String(), "\u200b")
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
// a dialog: popovers and toasts. It is a canvas panel on the shadow, so it
// lifts off whatever it covers. Modal has the deeper of the two shadows.
func PopoverPanel(c *ui.Context, panel ui.Element) ui.Element {
	p := Colors(c.Theme())
	return panel.Radius(RadiusPanel).Background(p.Canvas).InsetShadow(0, 0, 0, 1, p.Hairline).
		Shadow(0, 8, 28, 0, p.Shadow).Transition(popoverMotion())
}

// StatusPill prints a status in its signal color. Status and method colors
// never fill a background: only the slow-duration pill is tinted.
func StatusPill(c *ui.Context, label string, foreground ui.Color) ui.Element {
	t := c.Theme()
	return ui.Text(c, label).FontSize(Scaled(TextBody)).FontFeatures("tnum").SingleLine().
		TextColor(foreground).Padding(t.Space(1), t.Space(2)).Radius(RadiusControl)
}

// QueryChip is one condition in the query bar, written as the query syntax
// writes it. A filter is not a selection, so the chip is a neutral face. A
// click on its text runs edit, which turns it back into text to change; a
// condition with no syntax passes nil and has no such button. The × runs
// remove. Both run after the chip is built.
func QueryChip(c *ui.Context, text string, edit, remove func()) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	return ui.Row(c.Key("query-chip:"+text)).Height(ControlHeightSmall).Shrink(0).Padding(0, 2).Gap(0).
		AlignItems(ui.Center).Radius(RadiusControl).Background(p.Fill).Children(func() {
		label := func() {
			ui.Text(c, text).Font(FontMono).FontSize(Scaled(TextData)).TextColor(t.Text).SingleLine()
		}
		if edit == nil {
			ui.Row(c).Padding(0, t.Space(1)).Children(label)
		} else {
			b := Tip(c, ui.ButtonBase(c).Label("Edit condition: "+text), "Edit "+text).
				Height(ControlHeightSmall-4).Padding(0, t.Space(1)).Radius(RadiusInner).AlignItems(ui.Center)
			if b.Hovered() {
				b.Background(p.FillHover)
			}
			b.Children(label)
			if b.Clicked() {
				edit()
			}
		}
		x := Tip(c, ui.ButtonBase(c).Label("Remove condition: "+text), "Remove "+text).
			Size(ControlHeightSmall-4, ControlHeightSmall-4).Radius(RadiusInner).Justify(ui.Center).AlignItems(ui.Center).TextColor(t.TextMuted)
		if x.Hovered() {
			x.Background(p.FillHover)
		}
		x.Children(func() { ui.Icon(c, closeIcon).Size(IconInline, IconInline) })
		if x.Clicked() {
			remove()
		}
	})
}

func MethodLabel(c *ui.Context, method string) ui.Element {
	return ui.Text(c, method).Font(FontMono).FontSize(Scaled(TextData)).FontWeight(500).
		TextColor(MethodColor(method, c.Theme())).SingleLine()
}

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
func SidebarHeading(c *ui.Context, title string, open *bool, trailing func()) ui.Element {
	t := c.Theme()
	head := ui.Row(c).Height(HeightStatus).Margin(t.Space(2), 0, 0, 0).
		Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Label(title)
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
		Margin(0, t.Space(1.5)).Padding(0, t.Space(1.5)).Gap(t.Space(2)).Children(func() {
		ui.Icon(c, cmp.Or(icon, SidebarDot)).Size(IconLarge, IconLarge).TextColor(ink)
		Body(c, label).SingleLine().Grow(1)
	})
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
	t, p := c.Theme(), Colors(c.Theme())
	width, _ := c.Size()
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		// Toasts keep clear of the footer: its buttons and tool chips are
		// at the window's bottom edge and must stay reachable.
		viewport.Padding(t.Space(4), t.Space(4), t.Space(4)+HeightFooter, t.Space(4)).AlignItems(ui.End).Gap(t.Space(2))
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
				Transition(ui.ElementTransition{Duration: DurationHover, Enter: &ui.Motion{Y: 8}, Exit: &ui.Motion{Y: 4}}).
				Children(func() {
					ui.Icon(c, icon).Size(IconLarge, IconLarge).TextColor(color)
					ui.Column(c).Grow(1).MinWidth(0).Gap(t.Space(2)).Children(func() {
						ui.Text(c, toast.Title).FontSize(Scaled(TextBody)).TextColor(t.Text)
						if toast.Description != "" {
							ui.Text(c, toast.Description).FontSize(Scaled(TextMeta)).TextColor(t.TextMuted)
						}
						if toast.Action != "" {
							b := parts.ActionButton().Label(toast.Action).Height(ControlHeightSmall).Padding(0, Scaled(ControlPaddingXSmall)).
								Radius(RadiusControl).Background(p.Fill).TextColor(t.Text).AlignItems(ui.Center)
							if b.Pressed() {
								b.Background(p.FillPressed)
							} else if b.Hovered() {
								b.Background(p.FillHover)
							}
							b.Children(func() { ui.Text(c, toast.Action).FontSize(Scaled(TextBody)) })
						}
					})
					close := Tip(c, parts.CloseButton().Label(locales.DismissNotification()), locales.DismissNotification()).
						Size(ControlHeightSmall, ControlHeightSmall).Radius(RadiusControl).Center().TextColor(t.TextMuted)
					if close.Pressed() {
						close.Background(p.FillPressed)
					} else if close.Hovered() {
						close.Background(p.FillHover)
					}
					close.Children(func() { ui.Icon(c, closeIcon).Size(IconInline, IconInline) })
				})
		}
	})
}

// DialogBase retains the library's modal focus and dismissal rules; only
// the shared desktop chrome is ours.
func Modal(c *ui.Context, open *bool, content func()) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	width, height := c.Size()
	dialog := ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.45))
		panel.Padding(t.Space(5)).Gap(t.Space(3)).Radius(RadiusPanel).Background(p.Canvas).
			InsetShadow(0, 0, 0, 1, p.Hairline).
			MaxWidth(width-40).MaxHeight(height-40).Shadow(0, 20, 56, 0, p.Shadow)
		// A dialog is built the frame it opens, so a transition's entrance
		// has no earlier frame to come from: the panel grows from 97% as a
		// value that starts at zero the first time this dialog is built.
		seen := ui.Local(panel, "dialog-in", func() bool { return false })
		if !*seen {
			panel.Jump("dialog-in", 0)
			*seen = true
		}
		// Only the fade: a scale moves the panel's painted pixels away from
		// where the layout puts its controls, and a press in the first
		// frames would land beside them.
		panel.Opacity(panel.AnimateTo("dialog-in", 1, ui.Animation{Duration: DurationStandard, Ease: ui.EaseOut, NonMotion: true}))
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
	return ui.Text(c, title).FontSize(Scaled(TextTitle)).FontWeight(500).TextColor(c.Theme().Text)
}

// Tabs is a row of text tabs; the chosen one is a rounded chip in the
// neutral fill with the normal text color. A tab is a filter, not a
// selection of data, so it is never green. The prefix names each tab for
// assistive technology, so a screen with two tab rows of the same labels can
// name the one it means.
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
	t, p := c.Theme(), Colors(c.Theme())
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
			tab := group.Tab(i).Label(name).Height(ControlHeightSmall).Padding(0, Scaled(ControlPaddingXSmall)).Gap(t.Space(1)).
				Radius(RadiusControl).AlignItems(ui.Center).TextColor(t.TextMuted)
			switch {
			case chosen && sub:
				tab.TextColor(p.Accent)
			case chosen:
				tab.Background(p.Fill).TextColor(t.Text)
			case sub && (tab.Pressed() || tab.Hovered()):
				tab.TextColor(t.Text)
			case tab.Pressed():
				tab.Background(p.FillPressed)
			case tab.Hovered():
				tab.Background(p.FillHover)
			}
			hoverMotion(tab, tab.Hovered() || tab.Pressed())
			tab.Children(func() {
				ui.Text(c, label).FontSize(Scaled(TextBody))
				if i < len(counts) && counts[i] != "" && counts[i] != "0" {
					Meta(c, counts[i]).FontFeatures("tnum")
				}
			})
		}
	})
	return group.List
}

// TabStrip scrolls a row of tabs sideways in a narrow pane. An edge that
// hides more tabs fades into the surface behind them, so a cut-off tab reads
// as more to scroll rather than a clipped label. The caller sizes it.
func TabStrip(c *ui.Context, tabs func()) ui.Element {
	t := c.Theme()
	strip := ui.Box(c).MinWidth(0)
	scroll := ui.Local(strip, "scroll", func() ui.ScrollState { return ui.ScrollState{} })
	return strip.Children(func() {
		ui.ScrollHorizontal(c).TrackScroll(scroll).Fill().Children(tabs)
		if scroll.X > 0.5 {
			EdgeFade(c, true, t.Background)
		}
		if scroll.X < scroll.MaxX-0.5 {
			EdgeFade(c, false, t.Background)
		}
	})
}

// EdgeFade fades a row clipped by a scroll into the surface behind it: from
// fades its leading edge, otherwise its trailing one. It only paints, so what
// is half under it still takes its click.
func EdgeFade(c *ui.Context, from bool, fill ui.Color) ui.Element {
	edge := ui.Box(c).Absolute().Top(0).Bottom(0).Width(c.Theme().Space(6)).PassThrough()
	if from {
		return edge.Left(0).Gradient(fill, fill.Alpha(0), 90)
	}
	return edge.Right(0).Gradient(fill.Alpha(0), fill, 90)
}

// MoreButton is the "⋯" that opens a menu: an icon button with the menu on it
// and no chevron, since the three dots already say one opens.
func MoreButton(c *ui.Context, label, tip string, menu func(*ui.Menu)) ui.Element {
	b := IconButton(c, label, tip, lucide.Ellipsis()).PopoverMenuButton(menu, ui.PopoverMenuButtonOptions{Panel: func(panel ui.Element) { PopoverPanel(c, panel) }})
	return b
}

// MoreMenu is the quiet "⋯" button that opens a menu of further actions;
// label names it for assistive technology and in its tooltip. It has no
// face until hovered, as a toolbar button has none.
func MoreMenu(c *ui.Context, label string, menu func(*ui.Menu)) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	ghost := *t
	ghost.Surface, ghost.SurfaceHover, ghost.SurfacePressed = ui.Color{}, p.FillHover, p.FillPressed
	ghost.Border = ui.Color{}
	c.SetTheme(&ghost)
	// A button of our own with a menu on it, not the library's menu button:
	// that one draws a chevron after its label, and a ⋯ already says a menu
	// opens. The menu itself is the library's, panel and motion included.
	b := ui.ButtonBase(c).Label("⋯").PopoverMenuButton(menu, ui.PopoverMenuButtonOptions{Panel: func(panel ui.Element) { PopoverPanel(c, panel) }})
	// ButtonBase is a row with no look: the glyph has to be a child, or the
	// pane's menu is an empty strip with nothing to click.
	b.Children(func() { ui.Text(c, "⋯").FontSize(Scaled(TextBody)).SingleLine() })
	c.SetTheme(t)
	// A pane header's row is already full of names and tabs: the menu must
	// keep its width and let them shrink, or it is laid out at nothing and
	// the pane has no way into its own commands.
	return Tip(c, b.Label(label), label).Height(ControlHeightSmall).Shrink(0).
		Padding(0, Scaled(ControlPaddingXSmall)).FontSize(Scaled(TextBody)).TextColor(t.TextMuted)
}

// TabMenu is a tab that opens a menu instead of choosing a view: where a pane
// keeps the views it has beyond the tabs in its row, so it never needs a
// second row of them.
func TabMenu(c *ui.Context, label, name string, menu func(*ui.Menu)) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	ghost := *t
	ghost.Surface, ghost.SurfaceHover, ghost.SurfacePressed = ui.Color{}, p.FillHover, p.FillPressed
	ghost.Border = ui.Color{}
	c.SetTheme(&ghost)
	b := ui.ButtonBase(c).Label(label).PopoverMenuButton(menu, ui.PopoverMenuButtonOptions{Panel: func(panel ui.Element) { PopoverPanel(c, panel) }})
	// ButtonBase paints nothing of its own: the tab's words are its child.
	b.Children(func() { ui.Text(c, label).FontSize(Scaled(TextBody)).SingleLine() })
	c.SetTheme(t)
	return Tip(c, b.Label(name), name).Height(ControlHeightSmall).Padding(0, Scaled(ControlPaddingXSmall)).
		Radius(RadiusControl).AlignItems(ui.Center).Gap(t.Space(1)).FontSize(Scaled(TextBody)).TextColor(t.TextMuted)
}

// PaneHeader is the one row above a pane: what it shows, then what it reports
// or offers at the far end. The caller builds the title and tabs, so a pane can
// name itself with a status light and focus on a click; its tabs grow, which
// is what keeps what follows them at the edge.
func PaneHeader(c *ui.Context, content, trailing func()) ui.Element {
	t := c.Theme()
	// A pane header is a row, not a band: the regions around it are told
	// apart by the split and by space, never by a rule of its own.
	return ui.Row(c).Height(HeightTabs).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
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
	b := Tip(c, ui.ButtonBase(c).Label(name), name).Height(ControlHeightSmall).Padding(0).
		Gap(t.Space(1.5)).Radius(RadiusControl).AlignItems(ui.Center).TextColor(ink)
	b.Children(func() {
		if dot.A > 0 {
			ui.Box(c).Size(6, 6).Radius(3).Background(dot).Shrink(0)
		}
		ui.Text(c, title).FontSize(Scaled(TextBody)).FontWeight(500).SingleLine()
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

// Segmented chooses one of a few mutually exclusive modes, as AppKit draws
// one: a recessed groove and a raised thumb, concentric with it. Tabs, not
// Segmented, switch what a pane shows.
func Segmented(c *ui.Context, selected *int, labels ...string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	parts := ui.SegmentedBase(c, selected, len(labels))
	parts.Track.AlignItems(ui.Stretch).Height(HeightControl).Padding(t.Space(0.5)).
		Radius(RadiusControl).Background(p.Fill).Children(func() {
		for i, label := range labels {
			s := parts.Segment(i).Label(label).Padding(0, t.Space(2)).Radius(RadiusInner).
				AlignItems(ui.Center).FontSize(Scaled(TextMeta)).TextColor(t.TextMuted)
			switch {
			case i == *selected:
				s.Background(p.Canvas).InsetShadow(0, 0, 0, 1, p.Hairline).TextColor(t.Text)
			case s.Pressed() || s.Hovered():
				s.TextColor(t.Text)
			}
			// The segment under the pointer takes its face at once; the one
			// it leaves fades, as a tab does.
			hoverMotion(s, s.Hovered() || s.Pressed())
			s.Children(func() { ui.Text(c, label).SingleLine() })
		}
	})
	return parts.Track
}

// Selection is a tinted fill with the row's normal text colors. focused is
// whether the list holding the row has the keyboard focus; without it the
// fill turns to the neutral control face, so the row keeps its shape and
// still shows which one is selected without claiming the keyboard.
//
// Rows that pick a place or a thing (sidebars, trees, pick lists) are
// rounded blocks inset from the edges, as in a macOS source list: Selected.
// Rows of data in a MyGo table or list span its width and add a marker line
// at the leading edge, which keeps a selected row apart from a hovered one
// once the fill turns neutral: ListTheme and RowMarker.

// ListTheme is the theme a MyGo table, list or tree paints its selection
// from. Scope it to the list with SetTheme; rows draw RowMarker themselves.
func ListTheme(t *ui.Theme, focused bool) *ui.Theme {
	p := Colors(t)
	list := *t
	list.Accent, list.AccentText = p.AccentTint, t.Text
	if !focused {
		list.Accent = p.Fill
	}
	return &list
}

// marker paints the selection marker on the leading edge of r.
func marker(t *ui.Theme, focused bool) func(p *ui.Painter, r ui.Rect) {
	// The palette's accent, not t.Accent: inside ListTheme that is the fill.
	pal := Colors(t)
	color := pal.Accent
	if !focused {
		color = pal.TextFaint
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
	t, p := c.Theme(), Colors(c.Theme())
	fill := p.AccentTint
	if !focused {
		fill = p.Fill
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
			ui.Text(c, title).FontSize(Scaled(TextTitle)).FontWeight(500)
			if about != "" {
				Hint(c, about)
			}
		})
		ui.Form(c, groups).Gap(t.Space(4))
	})
}

// settingsGroups and settingsWidths remember the current group and the widest
// label seen in it. The first frame uses the old 112-point column; measuring a
// wider label asks for one more frame, and the next frame uses the new width.
var settingsGroups sync.Map // *ui.Context -> string
var settingsWidths sync.Map // *ui.Context -> map[string]float32

func settingsGroupName(c *ui.Context) string {
	name, _ := settingsGroups.Load(c)
	group, _ := name.(string)
	return group
}

func settingsLabelWidth(c *ui.Context, label string) float32 {
	group := settingsGroupName(c)
	if group == "" {
		return Scaled(112)
	}
	width := float32(0)
	if stored, ok := settingsWidths.Load(c); ok {
		if widths, ok := stored.(map[string]float32); ok {
			width = widths[group]
		}
	}
	if width == 0 {
		width = Scaled(112)
	}
	measured, _ := c.MeasureText(0, ui.Span{Text: label, Size: Scaled(TextBody)})
	want := min(max(measured+Scaled(4), Scaled(112)), Scaled(180))
	if want > width {
		settingsWidths.Store(c, map[string]float32{group: want})
		c.Invalidate()
	}
	return width
}

// SettingsGroup is a group of related fields under its legend: a heading and
// a rule, not a card, so the page reads as one canvas rather than a stack of
// boxes.
func SettingsGroup(c *ui.Context, legend string, fields func()) ui.Element {
	t := c.Theme()
	previous := settingsGroupName(c)
	settingsGroups.Store(c, legend)
	defer func() {
		if previous == "" {
			settingsGroups.Delete(c)
		} else {
			settingsGroups.Store(c, previous)
		}
	}()
	return ui.Fieldset(c, legend, fields).Margin(0).Padding(0, 0, t.Space(4), 0).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
}

// first returns the first value, or fallback when there is none.
func first(values []string, fallback string) string {
	if len(values) > 0 && values[0] != "" {
		return values[0]
	}
	return fallback
}

// SettingsRow is one setting as a row: its label in a fixed left column, its
// control beside it, and a hint under the control when it needs one. The
// label column is Scaled so it follows the text size, the label is centered
// on a single-line control, and the hint stays a meta line within the
// control's width instead of running across the page.
func SettingsRow(c *ui.Context, label string, control func() ui.Element, hint string, tip ...string) ui.Element {
	return settingsRow(c, label, control, hint, first(tip, hint), false)
}

// SettingsRowMultiline is SettingsRow for a control taller than one line,
// such as a text area: the label lines up with the control's first line.
func SettingsRowMultiline(c *ui.Context, label string, control func() ui.Element, hint string) ui.Element {
	return settingsRow(c, label, control, hint, hint, true)
}

func settingsRow(c *ui.Context, label string, control func() ui.Element, hint, tip string, multiline bool) ui.Element {
	t := c.Theme()
	align := ui.Center
	if multiline {
		align = ui.Start
	}
	if hint == "" {
		return settingsRowBody(c, label, control, align)
	}
	return ui.Column(c).FillWidth().Gap(t.Space(1)).Children(func() {
		settingsRowBody(c, label, control, align)
		ui.Row(c).Children(func() {
			ui.Box(c).Width(Scaled(112) + t.Space(4)).Shrink(0)
			Tip(c, ui.Text(c, hint).FontSize(Scaled(TextMeta)).TextColor(t.TextMuted).
				MaxWidth(Scaled(400)).LineHeight(LineProse), tip)
		})
	})
}

func settingsRowBody(c *ui.Context, label string, control func() ui.Element, align ui.Align) ui.Element {
	t := c.Theme()
	labelWidth := settingsLabelWidth(c, label)
	row := ui.Row(c).FillWidth().Gap(t.Space(4)).AlignItems(align)
	row.Children(func() {
		if label == "" {
			ui.Box(c).Width(labelWidth).Shrink(0)
		} else {
			ui.Text(c, label).FontSize(Scaled(TextBody)).TextColor(t.Text).Width(labelWidth).Shrink(0).MaxLines(2)
		}
		ui.Column(c).Grow(1).MinWidth(0).Children(func() { control() })
	})
	return row
}

// SelectLabels is Select for values a screen names in its own language: the
// stored value stays option, while the trigger and the popup show label of
// it. The app uses it where an option is an identifier it compares, such as
// "Raw" or "In", and the screen says "原始" or "入".
func SelectLabels(c *ui.Context, selected *string, options []string, label func(string) string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
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
				item.Background(p.Fill)
			}
			item.Children(func() { ui.Text(c, label(option)).FontSize(Scaled(TextBody)).SingleLine() })
		}
	})
	return faced(c, func() ui.Element { return sel.Trigger }).
		Height(ControlHeight).Padding(0, Scaled(ControlPaddingX)).FontSize(Scaled(TextBody)).Radius(RadiusControl)
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
