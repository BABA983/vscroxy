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

// tipBreakRun is how many characters a tip lets run before it breaks them.
const tipBreakRun = 32

// Sizes only these components use: a pane header's monospace line, the
// timing bar, the activity sparkline, a list's marker box, a switch's track,
// a tall form's label column and the slack a settings label is measured with.
const (
	responseMetaLine   = 14
	timingBarHeight    = 6
	sparklineWidth     = 56
	sparklineHeight    = 14
	rowMarkerBox       = 4
	formTallLabelWidth = 96
	switchHeight       = 20
	settingsLabelSlack = 4
)

// ButtonBase keeps native keyboard, focus-ring and accessibility behavior.
// The face is chosen as the button is built, so disable it through an
// ancestor (IsDisabled includes one); Disabled on the result comes too late
// to stop the hover face. keys draws a shortcut as Kbd caps in its tip.
func ToolbarButton(c *ui.Context, label, tip string, active bool, keys ...string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	b := TipKeys(c, ui.ButtonBase(c).Label(label), tip, keys...).Height(HeightControl).
		Padding(0, Scaled(ControlPaddingX)).Gap(t.Space(SpaceM)).Radius(RadiusControl).
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
	return b
}

// FooterButton is one action in the footer bar: 20 points high, at rest text
// with no face of its own. The pointer brings a face, and being on brings the
// same raised face a chosen segment has, so "on" and "under the pointer"
// never read alike.
func FooterButton(c *ui.Context, label, tip string, on bool) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	b := Tip(c, ui.ButtonBase(c).Label(label), tip).Height(HeightControlSmall).
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
		b.Background(p.Canvas).InsetShadow(0, 0, 0, HairlineWidth, p.Hairline)
	}
	return b
}

// IconButton is an icon-only action at control height: the title bar, a
// toolbar and the footer. Its tip is its only label, so it always has one.
func IconButton(c *ui.Context, label, tip string, icon *ui.SVG, keys ...string) ui.Element {
	return iconAction(c, label, tip, icon, HeightControl, keys...)
}

// RowIconButton is IconButton at the row-action size, for an action inside a
// row, a sidebar heading or a pane header: HeightRowAction square, a 14-point
// icon, and RadiusInner, the corner a row's own radius leaves at its inset.
func RowIconButton(c *ui.Context, label, tip string, icon *ui.SVG) ui.Element {
	return iconAction(c, label, tip, icon, HeightRowAction).Radius(RadiusInner)
}

// iconAction is a ToolbarButton that is a square of size, with the icon as its
// only child.
func iconAction(c *ui.Context, label, tip string, icon *ui.SVG, size float32, keys ...string) ui.Element {
	return ToolbarButton(c, label, tip, false, keys...).Size(size, size).
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
	b := ui.PrimaryButton(c, label).Height(HeightControl).Padding(0, Scaled(ControlPaddingX)).
		FontSize(Scaled(TextBody)).FontWeight(WeightMedium).Radius(RadiusControl)
	c.SetTheme(t)
	return b
}

// PrimaryButtonBusy is PrimaryButton while work runs under it: the button is
// disabled and a spinner turns where its label is. The label keeps its space,
// so the button is the same width busy or idle.
func PrimaryButtonBusy(c *ui.Context, label string, busy bool) ui.Element {
	if !busy {
		return PrimaryButton(c, label)
	}
	return busyButton(c, PrimaryButton(c, ""), label, 500, c.Theme().AccentText)
}

// SecondaryButtonBusy is SecondaryButton while work runs under it: the same
// disabled face and spinner as PrimaryButtonBusy, in the text color.
func SecondaryButtonBusy(c *ui.Context, label string, busy bool) ui.Element {
	if !busy {
		return SecondaryButton(c, label)
	}
	return busyButton(c, SecondaryButton(c, ""), label, 400, c.Theme().Text)
}

// busyButton disables b and turns a spinner where its label is. b is named by
// label, and an invisible copy of the label holds the width it had.
func busyButton(c *ui.Context, b ui.Element, label string, weight int, ink ui.Color) ui.Element {
	b = b.Label(label).Disabled(true)
	b.Children(func() {
		ui.Text(c, label).FontSize(Scaled(TextBody)).FontWeight(weight).Opacity(0)
		ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().Children(func() {
			ui.Spinner(c).Label(label).TextColor(ink)
		})
	})
	return b
}

// SecondaryButton has no border: its face is the text color laid thinly
// over whatever is beneath, so it reads the same on the window, a panel or
// a selected row.
func SecondaryButton(c *ui.Context, label string) ui.Element {
	return faced(c, func() ui.Element { return ui.Button(c, label) }).Height(HeightControl).
		Padding(0, Scaled(ControlPaddingX)).FontSize(Scaled(TextBody)).Radius(RadiusControl)
}

// ChipButton is a control with the neutral face and no text of its own: the
// caller builds what it shows, as an icon and a shortcut. It reads on every
// surface, as every control does.
func ChipButton(c *ui.Context, label, tip string) ui.Element {
	t := c.Theme()
	return faced(c, func() ui.Element { return Tip(c, ui.ButtonBase(c).Label(label), tip) }).
		Height(HeightControlSmall).Padding(0, Scaled(ControlPaddingXSmall)).Gap(t.Space(SpaceS)).Radius(RadiusControl).AlignItems(ui.Center)
}

// CompactButton is a secondary button for dense rows and bars: the same
// face at meta size.
func CompactButton(c *ui.Context, label string) ui.Element {
	return faced(c, func() ui.Element { return ui.Button(c, label) }).Height(HeightControlSmall).
		Padding(0, Scaled(ControlPaddingXSmall)).FontSize(Scaled(TextMeta)).Radius(RadiusControl)
}

// LinkButton is an inline action, such as "Add property": text at the small
// control height with no face until the pointer arrives. An optional icon
// leads the label, so an add action uses the same plus as a table's add row.
func LinkButton(c *ui.Context, label string, icon ...*ui.SVG) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	b := Tip(c, ui.ButtonBase(c).Label(label), label).Height(HeightControlSmall).
		Padding(0, Scaled(ControlPaddingXSmall)).Gap(t.Space(SpaceXS)).Radius(RadiusInner).AlignItems(ui.Center).
		FontSize(Scaled(TextMeta)).TextColor(t.TextMuted)
	b.Children(func() {
		if len(icon) > 0 && icon[0] != nil {
			ui.Icon(c, icon[0]).Size(IconInline, IconInline)
		}
		ui.Text(c, label).FontSize(Scaled(TextMeta))
	})
	if b.Hovered() && !b.IsDisabled() {
		b.Background(p.FillHover).TextColor(t.Text)
	}
	return b
}

// Kbd draws a shortcut as a keycap: a small canvas face on a 1-point
// hairline, four-point corners and an 11-point label. It is for a shortcut
// beside a menu item or a command, never for a key the person should press
// inside a sentence.
func Kbd(c *ui.Context, key string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	return ui.Text(c, key).FontSize(Scaled(TextMeta)).FontWeight(WeightMedium).TextColor(t.TextMuted).
		Height(HeightKbd).MinWidth(HeightKbd).Padding(0, KbdPaddingX).Radius(RadiusInner).Background(p.Canvas).
		InsetShadow(0, 0, 0, HairlineWidth, p.Hairline).Center().SingleLine()
}

// numberInvalid reports text that is not a number in range, so the field
// shows its invalid face while it is typed. A blank field is not invalid: it
// keeps the value it had.
func numberInvalid(text string, lo, hi float64) bool {
	s := strings.TrimSpace(text)
	if s == "" {
		return false
	}
	v, err := strconv.ParseFloat(s, 64)
	return err != nil || v < lo || v > hi
}

// NumberField edits a whole number with its unit beside it, as [60] s. The
// text holds the value across builds so a half-typed number is not replaced
// under the caret. It is the protocol editors' number control.
func NumberField(c *ui.Context, text *string, value *float64, lo, hi float64, unit, label, tip string, changed func()) ui.Element {
	t := c.Theme()
	row := ui.Row(c).Gap(t.Space(SpaceXS)).AlignItems(ui.Center)
	row.Children(func() {
		in := InputState(c, text, numberInvalid(*text, lo, hi)).Label(label).Width(WidthNumber).Font(FontMono)
		if tip != "" {
			in = Tip(c, in, tip)
		}
		valid := true
		if in.Changed() {
			v, err := strconv.ParseFloat(strings.TrimSpace(*text), 64)
			valid = err == nil && v >= lo && v <= hi
			if valid {
				*value = v
				if changed != nil {
					changed()
				}
			}
		}
		// A half-typed or out-of-range number stays as typed, so the person
		// can fix it; only a valid value is normalized back into the field.
		if !in.Focused() && valid {
			*text = strconv.Itoa(int(*value))
		}
		if unit != "" {
			Meta(c, unit)
		}
	})
	return row
}

// DestructiveButton is the one action a destructive confirmation is for. It
// fills with Danger and prints in the palette's canvas text; it belongs in a
// dialog's footer, not in a row or a menu. A destructive action outside a
// dialog asks first, or is a menu item.
func DestructiveButton(c *ui.Context, label string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	face := *t
	face.Accent, face.AccentText = p.Danger, p.AccentText
	face.AccentHover = p.Danger.Mix(p.Text, 0.12)
	face.AccentPressed = p.Danger.Mix(p.Text, 0.22)
	c.SetTheme(&face)
	b := ui.PrimaryButton(c, label).Height(HeightControl).Padding(0, Scaled(ControlPaddingX)).
		FontSize(Scaled(TextBody)).FontWeight(WeightMedium).Radius(RadiusControl)
	c.SetTheme(t)
	return b
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
	b := ui.ButtonBase(c).Label(label).FillWidth().MinHeight(HeightControl).
		Padding(t.Space(SpaceXS), Scaled(ControlPaddingXSmall)).Gap(t.Space(SpaceM)).Radius(RadiusControl).AlignItems(ui.Center).
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
	b := Tip(c, ui.ButtonBase(c).Label(label), label).Checked(chosen).Size(HeightControl, HeightControl).
		Radius(RadiusControl).Background(color).Center()
	if b.Hovered() && !b.IsDisabled() {
		b.Border(HairlineWidth, c.Theme().Text)
	}
	return b.Children(func() {
		if chosen {
			ui.Text(c, "✓").FontSize(Scaled(TextBody)).FontWeight(WeightMedium).TextColor(SwatchText)
		}
	})
}

// Input is a single-line text field at control height: a well set into the
// canvas, with the focus ring's edge and a soft tint halo while it has the
// focus.
func Input(c *ui.Context, value *string) ui.Element {
	return InputState(c, value, false)
}

// InputState is Input with an invalid face: a Danger edge, and a Danger halo
// while the field has the focus. FormRow sets the accessible error; this is
// the visual half, so a caller passes err != "" here.
func InputState(c *ui.Context, value *string, invalid bool) ui.Element {
	in := InputControl(c, value)
	if invalid {
		in.Invalid(true)
	}
	return fieldFace(Colors(c.Theme()), in, false, in.Focused(), invalid)
}

// InputControl is Input's text input without the field face, for a field that
// styles runs of its text before wrapping it with FieldFace. It keeps the
// control's height, padding and font, so the field looks like Input.
func InputControl(c *ui.Context, value *string) ui.Element {
	// MyGo paints a text field's border from the theme it was built with, in
	// Accent when focused, Border or Text when hovered, Danger when invalid.
	// The inset border below must stay clear in every state, so the field is
	// built with those colors clear, and the caller's theme comes back after.
	t := c.Theme()
	quiet := *t
	quiet.Accent, quiet.Border, quiet.Text, quiet.Danger = ui.Color{}, ui.Color{}, ui.Color{}, ui.Color{}
	c.SetTheme(&quiet)
	e := TextInset(ui.TextInput(c, value).Height(HeightControl).FontSize(Scaled(TextBody)).Radius(RadiusControl).FocusRing(false), Scaled(ControlPaddingX))
	c.SetTheme(t)
	return e
}

// TextInset gives a single-line text field inset points at each side. The
// left one is padding. MyGo clips a field's text to its border box, not its
// padding, so the right one is a border: its width sets the room the text
// keeps, and it paints nothing because its color is clear. A field whose
// border MyGo restyles by state, as Input's, is built with clear colors too
// (see InputControl).
func TextInset(e ui.Element, inset float32) ui.Element {
	return e.Padding(0, 0, 0, inset).BorderWidth(0, inset, 0, 0).BorderColor(ui.Color{})
}

// Combobox is MyGo's text field with a suggestion popup, in the kit's field
// face at control height. Its focus edge is Ring, and the one edge it draws is
// the face's, so callers add no face of their own.
func Combobox(c *ui.Context, value *string, options []string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	face := *t
	face.Accent = p.Ring
	c.SetTheme(&face)
	// The field's gap sits between the text and the arrow, so a long value
	// stops ControlPaddingX short of the arrow, as an Input's stops at its inset.
	e := ui.Combobox(c, value, options).Border(0, ui.Color{}).Height(HeightControl).Padding(0, Scaled(ControlPaddingX)).Gap(Scaled(ControlPaddingX)).FontSize(Scaled(TextBody))
	c.SetTheme(t)
	return fieldFace(p, e, false, e.Focused(), false)
}

// TextArea is MyGo's multiline text field with the kit's focus edge: its
// border takes Ring while it has the focus, not the ink accent a primary
// action uses. The rest of its face stays MyGo's.
func TextArea(c *ui.Context, value *string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	face := *t
	face.Accent = p.Ring
	c.SetTheme(&face)
	e := ui.TextArea(c, value).Padding(Scaled(ControlPaddingXSmall), Scaled(ControlPaddingX))
	c.SetTheme(t)
	return e
}

// FieldFace gives a text field, or a row holding one with its own controls
// (a search box, the URL bar), the face a field has on the canvas: a well,
// and a muted edge and tint halo while focused. The edge is a neutral rather
// than the ink accent: a field's focus is a place in a form, not the screen's
// one primary action, so it reads quieter than the button beside it while
// staying well above 3:1 against both the field and the surface around it.
// A scroll container around a field leaves Space(1) at each side for the halo.
func FieldFace(c *ui.Context, field ui.Element, focused bool) ui.Element {
	return fieldFace(Colors(c.Theme()), field, false, focused, false)
}

// ChromeFieldFace is FieldFace for a field on chrome: the canvas face with
// a hairline, so it stays lighter than the bar it sits in.
func ChromeFieldFace(c *ui.Context, field ui.Element, focused bool) ui.Element {
	return fieldFace(Colors(c.Theme()), field, true, focused, false)
}

// SearchField is a one-line field that finds something in a list, with the
// magnifier that says so. onChrome draws it as a field on chrome: the canvas
// with a hairline rather than the well it is on the canvas.
func SearchField(c *ui.Context, value *string, label, placeholder string, onChrome bool) ui.Element {
	t := c.Theme()
	field := ui.Row(c).Height(HeightControl).AlignItems(ui.Center).Gap(t.Space(SpaceXS)).Padding(0, t.Space(SpaceM))
	var in ui.Element
	field.Children(func() {
		ui.Icon(c, searchIcon).Size(IconInline, IconInline).TextColor(t.TextMuted)
		// The face is the row's: the input inside carries no border or fill
		// of its own, so a hairline is drawn once.
		in = ui.TextInput(c, value).Height(HeightControl).Padding(0).FontSize(Scaled(TextBody)).Grow(1).MinWidth(0).
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
func fieldFace(p *Palette, field ui.Element, onChrome, focused, invalid bool) ui.Element {
	face := p.Well
	if onChrome {
		face = p.Canvas
	}
	// The face's radius also clips what sits inside it: a method control's
	// hover or a chip's fill follows the field's corners instead of squaring
	// them off.
	field.Radius(RadiusControl).Background(face).Clip()
	switch {
	case invalid && focused:
		fieldEdge(field, p.Danger)
		field.Shadow(0, 0, 0, FocusHalo, p.Danger.Alpha(0.14))
	case invalid:
		fieldEdge(field, p.Danger)
	case focused:
		fieldEdge(field, p.Ring)
		field.Shadow(0, 0, 0, FocusHalo, p.Ring.Alpha(0.16))
	case onChrome:
		fieldEdge(field, p.Hairline)
	}
	return field
}

// fieldEdge draws a field's edge round its whole box. An inset shadow stops
// at the padding edge, so a field whose text room is a transparent right
// border (TextInset) would leave that border's side bare; a stroke on the
// box does not.
func fieldEdge(field ui.Element, color ui.Color) {
	field.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Stroke(r, color, RadiusControl, HairlineWidth)
	})
}

// Select picks one of a few options at control height, with a button's face.
// It is SelectLabels with each option named as itself.
func Select(c *ui.Context, selected *string, options []string) ui.Element {
	return SelectLabels(c, selected, options, func(option string) string { return option })
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
		d := Tip(c, Meta(c, duration).Font(FontMono).FixedLineHeight(responseMetaLine).Shrink(0), duration)
		if len(timing.Phases) > 0 {
			d.Description(locales.TimingHoverHelp())
			ui.TooltipBase(c, d, func(tip ui.Element) {
				tip.AttachTo(d, ui.AnchorBottomStart, ui.AnchorTopStart).Margin(t.Space(SpaceS), 0, 0, 0)
				timingCard(c, tip, duration, timing)
			})
		}
		if detail != "" {
			Tip(c, Meta(c, " · "+detail).Font(FontMono).FixedLineHeight(responseMetaLine).
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
	PopoverPanel(c, card).Width(WidthWide).Padding(PopoverPadding).Gap(t.Space(SpaceS)).Children(func() {
		ui.Row(c).FillWidth().Height(timingBarHeight).Radius(timingBarHeight / 2).Background(pal.Fill).Children(func() {
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
			ui.Row(c).FillWidth().Gap(t.Space(SpaceM)).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(DotSize, DotSize).Radius(DotSize / 2).Background(dot)
				label := ui.Text(c, name).FontSize(Scaled(TextMeta)).TextColor(t.Text).Grow(1)
				v := ui.Text(c, value).Font(FontMono).FontSize(Scaled(TextMeta)).FontFeatures("tnum").TextColor(t.Text)
				if strong {
					label.FontWeight(WeightMedium)
					v.FontWeight(WeightMedium)
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
	return ui.Box(c).Size(sparklineWidth, sparklineHeight).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
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
// current one, with the same face and chevron, that opens the menu. The
// trigger keeps the hovered face while its menu is open, so the control reads
// as the one that opened the menu.
func MenuSelect(c *ui.Context, current string, menu func(*ui.Menu)) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c)
	open := ui.Local(b, "menu-open", func() bool { return false })
	opts := MenuButtonOptions(c)
	opts.Open = open
	b.Label(current).PopoverMenuButton(menu, opts)
	b.Children(func() {
		ui.Text(c, current).FontSize(Scaled(TextBody)).SingleLine()
		chevron(c)
	})
	return selectTrigger(c, b, *open).Justify(ui.SpaceBetween).TextColor(t.Text)
}

// popoverFace gives a menu's floating panel the kit's chrome: the canvas on
// the menu shadow, edged by a hairline. A menu's panel comes from MyGo, so
// the face is applied here instead of in PopoverPanel.
func popoverFace(c *ui.Context, panel ui.Element) {
	MenuPanel(c, panel.NoShadow())
}

// overlayShown marks a frame in which a drawn menu or popover was built, so
// FrameEnd can ask for the frame that paints the opener's restored focus.
func overlayShown(c *ui.Context) {
	*ui.Local(c.Root(), "overlay-shown", func() bool { return false }) = true
}

// FrameEnd closes the frame that builds the window. MyGo gives the focus
// back to the element that opened a menu or popover after the frame is
// painted, so the frame that loses an overlay asks for one more: that one
// paints the opener's focus ring. Without it the ring waits for an unrelated
// event, and a keyboard user cannot see where the focus went.
func FrameEnd(c *ui.Context) {
	shown := ui.Local(c.Root(), "overlay-shown", func() bool { return false })
	last := ui.Local(c.Root(), "overlay-was-shown", func() bool { return false })
	if *last && !*shown {
		c.Invalidate()
	}
	*last, *shown = *shown, false
}

// Tip gives e a tip of the kit's look: the canvas on the popover shadow, a
// hairline edge, RadiusPanel, 6 by 10 of padding and the small text role at
// the normal ink. It is the one tip the app draws; nothing styles its own.
// A URL or another long run breaks after / ? & = - _ . and every 32
// characters, so a tip never runs off the window.
//
// The timing is MyGo's TooltipBase: the pointer rests for the theme's
// TooltipDelay (500ms in the kit, VS Code's Linux and Windows default), and
// within two thirds of it after a tip goes the next shows at once, so moving
// along a row of controls does not wait again. The keyboard focus shows the
// tip at once, Escape hides it, and the tip goes above e, or below it where
// there is no room. e is described to assistive technology.
func Tip(c *ui.Context, e ui.Element, text string) ui.Element {
	return TipKeys(c, e, text)
}

// TipKeys is Tip with shortcuts drawn as Kbd caps after the text, rather than
// spelled inside the sentence: TipKeys(c, b, "Hide sidebar", "⌘", "B").
func TipKeys(c *ui.Context, e ui.Element, text string, keys ...string) ui.Element {
	if text == "" && len(keys) == 0 {
		return e
	}
	t := c.Theme()
	ui.TooltipBase(c, e, func(tip ui.Element) {
		tipFace(c, tip)
		tip.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(t.Space(SpaceS)).Children(func() {
				if text != "" {
					ui.Text(c, breakTip(text)).FontSize(Scaled(TextSmall)).LineHeight(LineSmall).TextColor(t.Text)
				}
				for _, key := range keys {
					Kbd(c, key)
				}
			})
		})
	})
	e.Description(tipDescription(text, keys))
	return e
}

// tipDescription is what assistive technology reads for a tip: the sentence,
// then the keys it names.
func tipDescription(text string, keys []string) string {
	if len(keys) == 0 {
		return text
	}
	return strings.TrimSpace(text + " (" + strings.Join(keys, " ") + ")")
}

// tipFace is the one surface a tip and a hover card share: the canvas on the
// popover shadow, a hairline edge, and the panel's 8-point corners.
func tipFace(c *ui.Context, tip ui.Element) {
	t, p := c.Theme(), Colors(c.Theme())
	tip.Background(p.Canvas).TextColor(t.Text).Radius(RadiusPanel).MaxWidth(Scaled(TipMaxWidth)).
		Padding(TipPaddingY, TipPaddingX).InsetShadow(0, 0, 0, HairlineWidth, p.Hairline)
	ShadowPopover.Apply(tip, t.Dark)
}

// TipSurface gives a tip built by the caller the kit's one face: the canvas
// on the popover shadow, a hairline edge and RadiusPanel. It is for a tip
// whose content is more than text and key/value rows, as a field's list of
// variables; the timing, placement and dismissal are still TooltipBase's.
func TipSurface(c *ui.Context, tip ui.Element) {
	tipFace(c, tip)
}

// HoverCard is the rich tip of a row: a title, one line of facts under it,
// and named values. It is for what a row cannot show in its width, as the
// full URL, method and status of a captured request.
type HoverCard struct {
	Title string
	Meta  string
	Rows  []HoverCardRow
}

// HoverCardRow is one named value of a HoverCard.
type HoverCardRow struct {
	Key   string
	Value string
}

// CardTip gives e a rich tip: a title, one line of meta and key/value rows,
// on the same surface as Tip. It shows when Tip does and is described to
// assistive technology by its text, since MyGo's tooltip itself is not read.
// The card is wider than a one-line tip and keeps clear of the window's
// edges through TooltipBase's placement.
func CardTip(c *ui.Context, e ui.Element, card HoverCard) ui.Element {
	return cardTip(c, e, card, nil)
}

// CardTipBeside is CardTip opened beside e: the card's top edge is level with
// e's and it starts a step to e's right. A sidebar row uses it, so its card
// lies beside the sidebar rather than over the rows above.
func CardTipBeside(c *ui.Context, e ui.Element, card HoverCard) ui.Element {
	return cardTip(c, e, card, func(tip ui.Element) {
		tip.AttachTo(e, ui.AnchorTopRight, ui.AnchorTopLeft).MarginStart(c.Theme().Space(SpaceS))
	})
}

// cardTip is the body of CardTip and CardTipBeside; place, when set, positions
// the card inside TooltipBase.
func cardTip(c *ui.Context, e ui.Element, card HoverCard, place func(tip ui.Element)) ui.Element {
	if card.Title == "" && card.Meta == "" && len(card.Rows) == 0 {
		return e
	}
	t := c.Theme()
	ui.TooltipBase(c, e, func(tip ui.Element) {
		tipFace(c, tip)
		tip.Padding(CardTipPaddingY, CardTipPaddingX)
		if place != nil {
			place(tip)
		}
		tip.Children(func() {
			ui.Column(c).Gap(t.Space(SpaceM)).Children(func() {
				head := ui.Column(c).Gap(t.Space(SpaceHair))
				head.Children(func() {
					if card.Title != "" {
						ui.Text(c, breakTip(card.Title)).FontSize(Scaled(TextBody)).FontWeight(WeightMedium).
							LineHeight(LineBody).TextColor(t.Text)
					}
					if card.Meta != "" {
						Meta(c, card.Meta).SingleLine()
					}
				})
				if len(card.Rows) > 0 {
					ui.Divider(c)
					for _, row := range card.Rows {
						if row.Key == "" && row.Value == "" {
							continue
						}
						ui.Row(c).FillWidth().Gap(t.Space(SpaceM)).AlignItems(ui.Start).Children(func() {
							Meta(c, row.Key).Width(Scaled(CardKeyWidth)).Shrink(0).Padding(0, 0, HairlineWidth, 0)
							if strings.Contains(row.Value, "://") {
								Mono(c, breakTip(row.Value)).TextColor(t.Text).Grow(1).MinWidth(0)
							} else {
								Small(c, breakTip(row.Value)).TextColor(t.Text).Grow(1).MinWidth(0)
							}
						})
					}
				}
			})
		})
	})
	parts := make([]string, 0, 2+len(card.Rows))
	if card.Title != "" {
		parts = append(parts, card.Title)
	}
	if card.Meta != "" {
		parts = append(parts, card.Meta)
	}
	for _, row := range card.Rows {
		if row.Key != "" || row.Value != "" {
			parts = append(parts, strings.TrimSpace(row.Key+": "+row.Value))
		}
	}
	e.Description(strings.Join(parts, ". "))
	return e
}

// breakTip gives a tip's text places to wrap only where a word cannot fit:
// a URL, or a run longer than the limit. Prose keeps its own spaces and
// punctuation, so a sentence is not cut at every dash.
func breakTip(s string) string {
	if strings.Contains(s, "://") {
		return breakLong(s)
	}
	run := 0
	for _, r := range s {
		switch r {
		case ' ', '\n', '\t':
			run = 0
			continue
		}
		run++
		if run > tipBreakRun {
			return breakLong(s)
		}
	}
	return s
}

// breakLong gives a long string places to wrap. A URL gets a zero-width
// space after the characters that already divide it; only a run with none of
// them is broken after 32 characters.
func breakLong(s string) string {
	const limit = tipBreakRun
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
	// The panel keeps 16 points at each edge, then its own 24-point inset.
	innerW := max(0, w-2*DialogPadding-2*WindowMargin)
	innerH := max(0, h-2*DialogPadding-2*WindowMargin)
	return ui.Column(c).Width(min(width, innerW)).MaxHeight(innerH).MinHeight(0).Gap(DialogGap)
}

func DialogFooter(c *ui.Context) ui.Element {
	t := c.Theme()
	return ui.Row(c).FillWidth().Gap(t.Space(SpaceM)).Wrap().AlignItems(ui.Center).Justify(ui.End).
		Padding(t.Space(SpaceL), 0, 0, 0).Border(0, t.Border).BorderWidth(HairlineWidth, 0, 0, 0)
}

// Panel frames one data area on the canvas: the surface behind it, a
// hairline and RadiusPanel. Use it for a preview, a catalog or a reader that
// would otherwise float on the dialog's canvas, not for every group.
func Panel(c *ui.Context) ui.Element {
	p := Colors(c.Theme())
	return ui.Column(c).Background(p.Chrome).Border(HairlineWidth, p.Hairline).Radius(RadiusPanel)
}

// PopoverPanel is the chrome of anything floating above the window short of
// a menu or a dialog: popovers and timing cards. It is a 12-point canvas
// panel on the popover shadow. A menu takes MenuPanel; a dialog Modal's own.
func PopoverPanel(c *ui.Context, panel ui.Element) ui.Element {
	p := Colors(c.Theme())
	overlayShown(c)
	return ShadowPopover.Apply(panel.Radius(RadiusCard).Background(p.Canvas).InsetShadow(0, 0, 0, HairlineWidth, p.Hairline), c.Theme().Dark)
}

// ToastPanel is the chrome of a toast: the dialog's 16-point corner and the
// popover's shadow, so a toast reads as a lighter, temporary dialog rather
// than a second dialog.
func ToastPanel(c *ui.Context, panel ui.Element) ui.Element {
	p := Colors(c.Theme())
	return ShadowPopover.Apply(panel.Radius(RadiusDialog).Background(p.Canvas).InsetShadow(0, 0, 0, HairlineWidth, p.Hairline), c.Theme().Dark)
}

// MenuPanel is the chrome of a drawn menu: a 10-point canvas panel on the
// menu shadow. MyGo builds the items; this is the face they sit on.
func MenuPanel(c *ui.Context, panel ui.Element) ui.Element {
	p := Colors(c.Theme())
	overlayShown(c)
	return ShadowMenu.Apply(panel.Border(0, ui.Color{}).Radius(RadiusMenu).Background(p.Canvas).
		InsetShadow(0, 0, 0, HairlineWidth, p.Hairline), c.Theme().Dark)
}

// MenuButtonOptions gives a PopoverMenuButton the kit's menu face. Use it
// for a menu the app opens from its own button; MyGo's ContextMenu and
// PopoverMenu have no panel option of their own.
func MenuButtonOptions(c *ui.Context) ui.PopoverMenuButtonOptions {
	return ui.PopoverMenuButtonOptions{Panel: func(panel ui.Element) { MenuPanel(c, panel) }}
}

// StatusPill prints a status in its signal color. Status and method colors
// never fill a background: only the slow-duration pill is tinted.
func StatusPill(c *ui.Context, label string, foreground ui.Color) ui.Element {
	t := c.Theme()
	return ui.Text(c, label).FontSize(Scaled(TextBody)).FontFeatures("tnum").SingleLine().
		TextColor(foreground).Padding(t.Space(SpaceXS), t.Space(SpaceM)).Radius(RadiusControl)
}

// QueryChip is one condition in the query bar, written as the query syntax
// writes it. A filter is not a selection, so the chip is a neutral face. A
// click on its text runs edit, which turns it back into text to change; a
// condition with no syntax passes nil and has no such button. The × runs
// remove. Both run after the chip is built.
func QueryChip(c *ui.Context, text string, edit, remove func()) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	return ui.Row(c.Key("query-chip:"+text)).Height(HeightChip).Shrink(0).Padding(0, t.Space(SpaceHair)).Gap(0).
		AlignItems(ui.Center).Radius(RadiusInner).Background(p.Fill).Children(func() {
		label := func() {
			ui.Text(c, text).Font(FontMono).FontSize(Scaled(TextData)).TextColor(t.Text).SingleLine()
		}
		if edit == nil {
			ui.Row(c).Padding(0, t.Space(SpaceXS)).Children(label)
		} else {
			b := Tip(c, ui.ButtonBase(c).Label(locales.QueryEditLabel(text)), locales.QueryEditTip(text)).
				Height(HeightChip).Padding(0, t.Space(SpaceXS)).Radius(RadiusInner).AlignItems(ui.Center)
			if b.Hovered() {
				b.Background(p.FillHover)
			}
			b.Children(label)
			if b.Clicked() {
				edit()
			}
		}
		x := Tip(c, ui.ButtonBase(c).Label(locales.QueryRemoveLabel(text)), locales.QueryRemoveTip(text)).
			Size(HeightChip, HeightChip).Radius(RadiusInner).Justify(ui.Center).AlignItems(ui.Center).TextColor(t.TextMuted)
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
	return ui.Text(c, method).Font(FontMono).FontSize(Scaled(TextData)).FontWeight(WeightMedium).
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

func EmptyState(c *ui.Context, title, hint string, actions func()) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Padding(t.Space(Space2XL)).Gap(t.Space(SpaceM)).Children(func() {
		ui.Icon(c, emptyIcon).Size(IconLarge, IconLarge).TextColor(t.TextMuted).Margin(0, 0, t.Space(SpaceM), 0)
		Heading(c, title).TextColor(t.Text)
		Hint(c, hint)
		if actions != nil {
			ui.Row(c).Gap(t.Space(SpaceM)).AlignItems(ui.Center).Justify(ui.Center).Children(actions)
		}
	})
}

// Read-only key/value rows preserve wrapping and existing value actions.
func KVRow(c *ui.Context, content func()) ui.Element {
	t := c.Theme()
	return ui.Row(c).Gap(t.Space(SpaceL)).Padding(t.Space(SpaceXS), t.Space(SpaceL)).
		MinHeight(HeightRow).AlignItems(ui.Start).FillWidth().Children(content)
}

// Only the look is ours: MyGo owns announcements, timeouts, pause-on-hover,
// the three-toast limit, action dispatch and keyboard dismissal.
func Toasts(c *ui.Context) {
	t, p := c.Theme(), Colors(c.Theme())
	width, _ := c.Size()
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		// Toasts keep clear of the footer: its buttons and tool chips are
		// at the window's bottom edge and must stay reachable.
		viewport.Padding(t.Space(SpaceXL), t.Space(SpaceXL), t.Space(SpaceXL)+HeightFooter, t.Space(SpaceXL)).AlignItems(ui.End).Gap(t.Space(SpaceM))
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
			ToastPanel(c, parts.Root).Row().Width(min(ToastWidth, width-2*WindowMargin)).Gap(t.Space(SpaceM)).Padding(ToastPaddingY, ToastPaddingX).
				AlignItems(ui.Center).
				Children(func() {
					ui.Icon(c, icon).Size(IconLarge, IconLarge).TextColor(color)
					ui.Column(c).Grow(1).MinWidth(0).Gap(t.Space(SpaceM)).Children(func() {
						ui.Text(c, toast.Title).FontSize(Scaled(TextBody)).TextColor(t.Text)
						if toast.Description != "" {
							ui.Text(c, toast.Description).FontSize(Scaled(TextMeta)).TextColor(t.TextMuted)
						}
					})
					if toast.Action != "" {
						// The action sits on the toast's one line, as a link beside
						// the message rather than a second full-width button. On the
						// canvas panel it is body ink, not the inverse text a
						// tooltip uses; at rest it must read as an action, not as a
						// disabled control.
						b := parts.ActionButton().Label(toast.Action).Height(HeightControlSmall).
							Padding(0, Scaled(ControlPaddingXSmall)).Radius(RadiusInner).AlignItems(ui.Center).
							FontSize(Scaled(TextBody)).FontWeight(WeightMedium).TextColor(t.Text)
						b.Children(func() {
							ui.Text(c, toast.Action).FontSize(Scaled(TextBody)).FontWeight(WeightMedium).TextColor(t.Text).Underline()
						})
						if b.Hovered() {
							b.Background(p.FillHover).TextColor(t.Text)
						}
					}
					close := Tip(c, parts.CloseButton().Label(locales.DismissNotification()), locales.DismissNotification()).
						Size(HeightControlSmall, HeightControlSmall).Radius(RadiusControl).Center().TextColor(t.TextMuted)
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

// ModalFlush is a modal with no inset: its content owns the whole panel.
// It is for a command palette, whose input and rows run to the panel's edge;
// the title still names the dialog for assistive technology.
func ModalFlush(c *ui.Context, open *bool, title string, content func()) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	width, height := c.Size()
	dialog := ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(p.Scrim)
		panel.Label(title).Padding(0).Radius(RadiusCard).Background(p.Canvas).Clip().
			InsetShadow(0, 0, 0, HairlineWidth, p.Hairline).
			MaxWidth(max(0, width-2*WindowMargin)).MaxHeight(max(0, height-2*WindowMargin))
		ShadowPopover.Apply(panel, t.Dark)
		seen := ui.Local(panel, "modal-in", func() bool { return false })
		if !*seen {
			panel.Jump("modal-in", 0)
			*seen = true
		}

		content()
	})
	if dialog.Dismissed() {
		c.Invalidate()
	}
	return dialog
}

// DialogBase retains the library's modal focus and dismissal rules; only
// the shared desktop chrome is ours.
func Modal(c *ui.Context, open *bool, title string, content func()) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	width, height := c.Size()
	dialog := ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(p.Scrim)
		panel.Label(title).Padding(DialogPadding).Gap(DialogGap).Radius(RadiusDialog).Background(p.Canvas).
			InsetShadow(0, 0, 0, HairlineWidth, p.Hairline).
			MaxWidth(max(0, width-2*WindowMargin)).MaxHeight(max(0, height-2*WindowMargin))
		ShadowDialog.Apply(panel, t.Dark)
		content()
	})
	if dialog.Dismissed() {
		// MyGo v0.7.3 restores the opener after painting. Render its focus
		// ring even when no hover or animation would request another frame.
		c.Invalidate()
	}
	return dialog
}

// DialogTitle names a dialog: the heading role in the body text color.
func DialogTitle(c *ui.Context, title string) ui.Element {
	return Heading(c, title).TextColor(c.Theme().Text)
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

// SubTabs is a row of tabs inside what another tab row chose, or at the head
// of a list beside an editor: the words alone carry the choice, and the chosen
// one is underlined in the accent. It has no chip, so a second level never
// looks like the row that holds it.
func SubTabs(c *ui.Context, selected *int, prefix string, labels ...string) ui.Element {
	return tabs(c, selected, prefix, labels, nil, true)
}

func tabs(c *ui.Context, selected *int, prefix string, labels, counts []string, sub bool) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	group := ui.TabsBase(c, selected, len(labels))
	gap := t.Space(SpaceHair)
	if sub {
		// A second level reads as words: the words keep a gap that tells
		// them apart, and the rule below them is the only mark of the
		// chosen one.
		gap = t.Space(SpaceM)
	}
	// As narrow as the underlined tabs were, so a narrow inspector still
	// shows them all; the chip's own padding keeps neighbours apart.
	group.List.Gap(gap).Children(func() {
		for i, label := range labels {
			name := label
			if prefix != "" {
				name = prefix + ": " + label
			}
			chosen := i == *selected
			tab := group.Tab(i).Label(name).Height(HeightControlSmall).Padding(0, Scaled(ControlPaddingXSmall)).Gap(t.Space(SpaceXS)).
				Radius(RadiusControl).AlignItems(ui.Center).TextColor(t.TextMuted)
			switch {
			case chosen && sub:
				// A rule, not a chip: the row is one level down from the
				// tabs that chose it, and the accent says which one.
				tab.TextColor(t.Text).Radius(0)
			case chosen:
				tab.Background(p.Fill).TextColor(t.Text)
			case sub && (tab.Pressed() || tab.Hovered()):
				tab.TextColor(t.Text)
			case tab.Pressed():
				tab.Background(p.FillPressed)
			case tab.Hovered():
				tab.Background(p.FillHover)
			}
			tab.Children(func() {
				ui.Text(c, label).FontSize(Scaled(TextBody))
				if i < len(counts) && counts[i] != "" && counts[i] != "0" {
					Meta(c, counts[i]).FontFeatures("tnum")
				}
				if chosen && sub {
					// The rule spans the word, not the tab's hit area, so
					// it starts where the word does.
					ui.Box(c).Absolute().Left(Scaled(ControlPaddingXSmall)).Right(Scaled(ControlPaddingXSmall)).Bottom(0).
						Height(TabRuleHeight).Background(p.Accent)
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
	edge := ui.Box(c).Absolute().Top(0).Bottom(0).Width(c.Theme().Space(Space2XL)).PassThrough()
	if from {
		return edge.Left(0).Gradient(fill, fill.Alpha(0), 90)
	}
	return edge.Right(0).Gradient(fill.Alpha(0), fill, 90)
}

// MoreButton is the toolbar's "⋯": an IconButton with the menu on it and no
// chevron, since the three dots already say one opens. The title bar uses it.
func MoreButton(c *ui.Context, label, tip string, menu func(*ui.Menu)) ui.Element {
	return IconButton(c, label, tip, lucide.Ellipsis()).PopoverMenuButton(menu, MenuButtonOptions(c))
}

// MoreMenu is the "⋯" of a row, a sidebar heading or a pane header, at the
// row-action size: HeightRowAction square with RadiusInner. It draws the same
// Ellipsis icon as MoreButton, has no face until hovered, and keeps its width
// where a row is full.
func MoreMenu(c *ui.Context, label string, menu func(*ui.Menu)) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	ghost := *t
	ghost.Surface, ghost.SurfaceHover, ghost.SurfacePressed = ui.Color{}, p.FillHover, p.FillPressed
	ghost.Border = ui.Color{}
	c.SetTheme(&ghost)
	// A button of our own with a menu on it, not the library's menu button:
	// that one draws a chevron after its label, and a ⋯ already says a menu
	// opens. The menu itself is the library's, panel and motion included.
	b := ui.ButtonBase(c).Label(label).PopoverMenuButton(menu, MenuButtonOptions(c))
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
	ghost.Surface, ghost.SurfaceHover, ghost.SurfacePressed = ui.Color{}, p.FillHover, p.FillPressed
	ghost.Border = ui.Color{}
	c.SetTheme(&ghost)
	b := ui.ButtonBase(c).Label(label).PopoverMenuButton(menu, MenuButtonOptions(c))
	// ButtonBase paints nothing of its own: the tab's words are its child.
	b.Children(func() { ui.Text(c, label).FontSize(Scaled(TextBody)).SingleLine() })
	c.SetTheme(t)
	return Tip(c, b.Label(name), name).Height(HeightControlSmall).Padding(0, Scaled(ControlPaddingXSmall)).
		Radius(RadiusControl).AlignItems(ui.Center).Gap(t.Space(SpaceXS)).FontSize(Scaled(TextBody)).TextColor(t.TextMuted)
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

// Segmented chooses one of a few mutually exclusive modes, as AppKit draws
// one: a recessed groove and a raised thumb, concentric with it. Tabs, not
// Segmented, switch what a pane shows.
func Segmented(c *ui.Context, selected *int, labels ...string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	parts := ui.SegmentedBase(c, selected, len(labels))
	parts.Track.AlignItems(ui.Stretch).Height(HeightRow).Padding(t.Space(SpaceHair)).
		Radius(RadiusControl).Background(p.Fill).Children(func() {
		for i, label := range labels {
			s := parts.Segment(i).Label(label).Padding(0, t.Space(SpaceM)).Radius(RadiusInner).
				AlignItems(ui.Center).FontSize(Scaled(TextMeta)).TextColor(t.TextMuted)
			switch {
			case i == *selected:
				s.Background(p.Canvas).InsetShadow(0, 0, 0, HairlineWidth, p.Hairline).TextColor(t.Text)
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

// DataList is a list of data: the selection is the app's tint plus a marker,
// never the solid accent a menu uses. It applies ListTheme and draws RowMarker
// for the selected row, so a caller cannot forget either. Build rows with
// Rows, and do not reset the row context's theme inside the callback.
func DataList(c *ui.Context, state *ui.ListState, count int, rows func(ui.ListRow)) ui.Element {
	t := c.Theme()
	focused := state != nil && state.Handle.Focused(c)
	c.SetTheme(ListTheme(t, focused))
	list := ui.List(c, state, count).Rows(func(row ui.ListRow) {
		if row.Selected() {
			RowMarker(row.Context, focused)
		}
		rows(row)
	})
	c.SetTheme(t)
	return list
}

// DataListIndexed is DataList for the original index callback. It draws the
// marker from the state's selected index, which the list keeps current.
func DataListIndexed(c *ui.Context, state *ui.ListState, count int, row func(int)) ui.Element {
	t := c.Theme()
	focused := state != nil && state.Handle.Focused(c)
	c.SetTheme(ListTheme(t, focused))
	list := ui.List(c, state, count, func(i int) {
		if state != nil && state.Selected != nil && *state.Selected == i {
			RowMarker(c, focused)
		}
		row(i)
	})
	c.SetTheme(t)
	return list
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
		inset := min(t.Space(SpaceS), r.H/4)
		p.Fill(ui.Rect{X: r.X, Y: r.Y + inset, W: 2, H: r.H - 2*inset}, color, 1)
	}
}

// RowMarker adds the marker to the row being built by a MyGo list or table,
// whose own selection fill comes from ListTheme.
func RowMarker(c *ui.Context, focused bool) {
	ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Width(rowMarkerBox).Draw(marker(c.Theme(), focused))
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

// FormRow is one field: its label, the control, an optional description and an
// optional error. It uses MyGo's Field, so the label names the control and the
// description and error are read with it; the error also marks the control
// invalid. A text field should use InputState(c, &value, err != "") for the
// visible danger edge, since the face is drawn by this kit rather than MyGo.
func FormRow(c *ui.Context, label, description, errText string, control func()) ui.Element {
	field := ui.Field(c, label, control)
	if description != "" {
		field.Description(description)
	}
	if errText != "" {
		field.Error(errText)
	}
	return field
}

// FormRowTip is FormRow with its explanation as a tip on the label rather
// than a line under the control: for a tall field whose description would
// otherwise sit below it, away from the name it explains. The label still
// names the control for assistive technology.
func FormRowTip(c *ui.Context, label, tip string, control func() ui.Element) ui.Element {
	t := c.Theme()
	return ui.Column(c).FillWidth().Gap(t.Space(SpaceXS)).Children(func() {
		name := Tip(c, ui.Text(c, label).FontSize(Scaled(TextBody)).TextColor(t.Text), tip)
		field := control()
		field.LabelledBy(name)
	})
}

// FormRowTall is FormRow for a control taller than one line, such as a code
// editor: its label sits on the control's top line instead of being centered
// on the whole box. It names the control, and binds the description and error
// directly, since MyGo's Field aligns a form row's first text line and a tall
// editor has none. Use it inside a ui.Form, beside FormRow fields.
func FormRowTall(c *ui.Context, label, description, errText string, control func() ui.Element) ui.Element {
	t := c.Theme()
	row := ui.Row(c).FillWidth().Gap(t.Space(SpaceL)).AlignItems(ui.Start)
	row.Children(func() {
		lab := ui.Text(c, label).Width(Scaled(formTallLabelWidth)).TextAlign(ui.End).TextColor(t.Text)
		col := ui.Column(c).Grow(1).MinWidth(0).Gap(t.Space(SpaceXS))
		col.Children(func() {
			ctrl := control()
			ctrl.LabelledBy(lab).Invalid(errText != "")
			if description != "" {
				ctrl.Description(description)
			}
			if errText != "" {
				// The error is drawn once, and named as the control's error for
				// assistive technology.
				ctrl.ErrorMessage(ui.Text(c, errText).FontSize(Scaled(TextMeta)).TextColor(t.Danger))
			} else if description != "" {
				Meta(c, description)
			}
		})
	})
	return row
}

// SettingsPage is one category of a settings window, as in macOS's System
// Settings: its title, a sentence on what it holds, and a form of
// SettingsGroups, whose labels line up across the page. It keeps a reading
// width however wide the window is.
func SettingsPage(c *ui.Context, title, about string, groups func()) ui.Element {
	t := c.Theme()
	return ui.Column(c).FillWidth().MaxWidth(640).Gap(t.Space(SpaceXXL)).Children(func() {
		ui.Column(c).Gap(t.Space(SpaceXS)).Children(func() {
			Heading(c, title)
			if about != "" {
				Hint(c, about)
			}
		})
		ui.Form(c, groups).Gap(t.Space(SpaceXL))
	})
}

// SettingsCardPage is the looser Settings skeleton: a page 620 points wide,
// a title and one sentence, then groups of SettingsCards. It is for the
// settings, first-run and wizard surfaces; the workspace keeps its own
// density.
func SettingsCardPage(c *ui.Context, title, about string, groups func()) ui.Element {
	t := c.Theme()
	return ui.Column(c).FillWidth().MaxWidth(SettingsWidth).Gap(t.Space(SpaceXL)).Children(func() {
		ui.Column(c).Gap(t.Space(SpaceXS)).Children(func() {
			Display(c, title)
			if about != "" {
				Hint(c, about)
			}
		})
		ui.Form(c, groups).Gap(t.Space(SpaceXL))
	})
}

// SettingsCard groups related rows in one canvas card, its legend above it.
// A row is at least 44 points high, with its title and one line of
// description on the left and its control on the right.
func SettingsCard(c *ui.Context, legend string, rows func()) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	return ui.Column(c).FillWidth().Gap(t.Space(SpaceM)).Children(func() {
		if legend != "" {
			Label(c, legend)
		}
		ui.Column(c).FillWidth().Clip().Radius(RadiusCard).Background(p.Canvas).
			Border(HairlineWidth, p.Hairline).Children(rows)
	})
}

// Switch is the kit's 36-by-20 switch. MyGo's own switch fills its track
// with the accent; in Dark that is almost white, so the white thumb has no
// edge. The kit paints the track from SwitchOn instead, keeping the on state
// distinct from off and from a disabled switch in both themes.
func Switch(c *ui.Context, on *bool) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	sw := ui.SwitchBase(c, on).Size(SettingsControlHeight, switchHeight).Radius(switchHeight / 2)
	target := float32(0)
	if *on {
		target = 1
	}
	pos := target
	off := t.Border.Mix(t.Text, 0.15)
	track := off.Mix(p.SwitchOn, pos)
	sw.Draw(func(painter *ui.Painter, r ui.Rect) {
		in := t.Space(SpaceHair)
		d := r.H - 2*in
		painter.Fill(r, track, r.H/2)
		at := pos
		if c.Direction() == ui.RightToLeft {
			at = 1 - pos
		}
		knob := ui.Rect{X: r.X + in + at*(r.W-r.H), Y: r.Y + in, W: d, H: d}
		painter.Shadow(knob, d/2, 0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.25))
		painter.Fill(knob, ui.RGB(255, 255, 255), d/2)
	})
	return sw
}

// SettingsSwitch is the 36-by-20 switch of the looser Settings skeleton.
func SettingsSwitch(c *ui.Context, on *bool, label string) ui.Element {
	return Switch(c, on).Label(label)
}

// SettingsCardRow is one row of a SettingsCard. The title is body text, the
// description one small muted line, and the control stays at the trailing
// edge, vertically centered. A longer tip can explain the description.
func SettingsCardRow(c *ui.Context, title, description string, control func(), tip ...string) ui.Element {
	t := c.Theme()
	row := ui.Row(c).FillWidth().MinHeight(SettingsRowHeight).Padding(CardRowPaddingY, CardPaddingX).
		Gap(t.Space(SpaceXL)).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
	row.Children(func() {
		head := ui.Column(c).Grow(1).MinWidth(0).Gap(0)
		head.Children(func() {
			Body(c, title)
			if description != "" {
				Small(c, description).TextColor(t.TextMuted)
			}
		})
		if len(tip) > 0 && tip[0] != "" {
			Tip(c, head, tip[0])
		}
		if control != nil {
			control()
		}
	})
	return row
}

// SettingsCardBlock is a row of a SettingsCard for content that does not fit
// beside its title: a multiline field, a list of editors or certificate
// details. The title and one description line sit above the content, and the
// block keeps the card's full width.
func SettingsCardBlock(c *ui.Context, title, description string, content func(), tip ...string) ui.Element {
	t := c.Theme()
	block := ui.Column(c).FillWidth().Padding(t.Space(SpaceL), t.Space(SpaceXL)).Gap(t.Space(SpaceM)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
	block.Children(func() {
		if title != "" || description != "" {
			head := ui.Column(c).Gap(0)
			head.Children(func() {
				if title != "" {
					Body(c, title)
				}
				if description != "" {
					Small(c, description).TextColor(t.TextMuted)
				}
			})
			if len(tip) > 0 && tip[0] != "" {
				Tip(c, head, tip[0])
			}
		}
		if content != nil {
			content()
		}
	})
	return block
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
		return Scaled(SettingsLabelWidth)
	}
	width := float32(0)
	if stored, ok := settingsWidths.Load(c); ok {
		if widths, ok := stored.(map[string]float32); ok {
			width = widths[group]
		}
	}
	if width == 0 {
		width = Scaled(SettingsLabelWidth)
	}
	measured, _ := c.MeasureText(0, ui.Span{Text: label, Size: Scaled(TextBody)})
	want := min(max(measured+Scaled(settingsLabelSlack), Scaled(SettingsLabelWidth)), Scaled(SettingsLabelMax))
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
	return ui.Fieldset(c, legend, fields).Margin(0).Padding(0, 0, t.Space(SpaceXL), 0).
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
		return settingsRowBody(c, label, control, "", align)
	}
	return ui.Column(c).FillWidth().Gap(t.Space(SpaceXS)).Children(func() {
		settingsRowBody(c, label, control, hint, align)
		ui.Row(c).Children(func() {
			ui.Box(c).Width(Scaled(SettingsLabelWidth) + t.Space(SpaceXL)).Shrink(0)
			Tip(c, ui.Text(c, hint).FontSize(Scaled(TextMeta)).TextColor(t.TextMuted).
				MaxWidth(Scaled(SettingsHintWidth)).LineHeight(LineProse), tip)
		})
	})
}

// settingsRowBody lines the label up with the control. The hint is the
// control's description for assistive technology; the hint line is drawn
// under the row by settingsRow. The label is not bound to the control with
// LabelledBy: that would replace the control's own name, which the settings
// screens rely on.
func settingsRowBody(c *ui.Context, label string, control func() ui.Element, hint string, align ui.Align) ui.Element {
	t := c.Theme()
	labelWidth := settingsLabelWidth(c, label)
	row := ui.Row(c).FillWidth().Gap(t.Space(SpaceXL)).AlignItems(align)
	row.Children(func() {
		if label == "" {
			ui.Box(c).Width(labelWidth).Shrink(0)
		} else {
			ui.Text(c, label).FontSize(Scaled(TextBody)).TextColor(t.Text).Width(labelWidth).Shrink(0).MaxLines(2)
		}
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			control().Description(hint)
		})
	})
	return row
}

// SelectLabels is Select for values a screen names in its own language: the
// stored value stays option, while the trigger and the popup show label of
// it. The app uses it where an option is an identifier it compares, such as
// "Raw" or "In", and the screen says "原始" or "入". Every select on a surface
// shares this trigger and popup, so they read alike.
func SelectLabels(c *ui.Context, selected *string, options []string, label func(string) string) ui.Element {
	t := c.Theme()
	sel := ui.SelectBase(c, selected)
	trigger := selectTrigger(c, sel.Trigger, sel.Open()).Justify(ui.SpaceBetween).TextColor(t.Text)
	trigger.Children(func() {
		ui.Text(c, label(*selected)).SingleLine()
		chevron(c)
	})
	sel.Popup(func(panel ui.Element) {
		popoverFace(c, panel.Padding(t.Space(SpaceXS)).Gap(t.Space(SpaceHair)))
		for _, option := range options {
			// The keyboard's highlight is the only fill in the popup; the
			// chosen option is marked with a check, so two rows never look
			// selected while the highlight moves.
			item := sel.Item(option).Padding(t.Space(SpaceS), t.Space(SpaceCompact)).Radius(RadiusControl).AlignItems(ui.Center).Gap(t.Space(SpaceS))
			ink := t.Text
			if item.Highlighted() {
				item.Background(t.Accent).TextColor(t.AccentText)
				ink = t.AccentText
			}
			item.Children(func() {
				Body(c, label(option)).SingleLine().Grow(1).MinWidth(0)
				if option == *selected {
					ui.Icon(c, checkIcon).Size(IconInline, IconInline).TextColor(ink).Shrink(0)
				}
			})
		}
	})
	return trigger
}

// selectTrigger gives a select's trigger the button face at control height:
// the field's text color laid thinly, a step stronger while hovered or while
// its popup is open, and the pressed step. A select's trigger is a button
// with no look of its own, so the face is painted here.
func selectTrigger(c *ui.Context, b ui.Element, open bool) ui.Element {
	p := Colors(c.Theme())
	switch {
	case b.Pressed():
		b.Background(p.FillPressed)
	case b.Hovered() || open:
		b.Background(p.FillHover)
	default:
		b.Background(p.Fill)
	}
	return b.Height(HeightControl).Padding(0, Scaled(ControlPaddingX)).FontSize(Scaled(TextBody)).
		Radius(RadiusControl).AlignItems(ui.Center).MinWidth(WidthPicker)
}

// chevron draws the arrow of a control that opens something below it.
func chevron(c *ui.Context) {
	t := c.Theme()
	ui.Box(c).Size(t.Space(SpaceCompact), t.Space(SpaceCompact)).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		var path ui.Path
		path.MoveTo(r.X+r.W*0.1, r.Y+r.H*0.3).LineTo(r.X+r.W*0.5, r.Y+r.H*0.7).LineTo(r.X+r.W*0.9, r.Y+r.H*0.3)
		p.StrokePath(&path, 1.5, t.TextMuted)
	})
}
