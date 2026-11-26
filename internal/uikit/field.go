package uikit

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

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

// Input is a single-line text field at control height: a near-white
// Background face with a 15% ink edge at rest and one Ring point outside it
// while focused.
func Input(c *ui.Context, value *string) ui.Element {
	return InputState(c, value, false)
}

// InputState is Input with an invalid face: a Danger edge, and one Danger
// point outside it while focused. FormRow sets the accessible error; this is
// the visual half, so a caller passes err != "" here.
func InputState(c *ui.Context, value *string, invalid bool) ui.Element {
	in := InputControl(c, value)
	if invalid {
		in.Invalid(true)
	}
	return fieldFace(Colors(c.Theme()), in, in.Focused(), invalid)
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

// Combobox is MyGo's text field with a suggestion popup, in the select face:
// the panel color in an 8-point field, with the same 15% ink edge and
// one Ring point on focus. Its focus edge is Ring, and the one edge it draws
// is the face's, so callers add no face of their own.
func Combobox(c *ui.Context, value *string, options []string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	face := *t
	face.Accent = p.Ring
	c.SetTheme(&face)
	// The field's gap sits between the text and the arrow, so a long value
	// stops ControlPaddingX short of the arrow, as an Input's stops at its inset.
	e := ui.Combobox(c, value, options).Border(0, ui.Color{}).Height(HeightControl).Padding(0, Scaled(ControlPaddingX)).Gap(Scaled(ControlPaddingX)).FontSize(Scaled(TextBody))
	c.SetTheme(t)
	return fieldFaceRadius(p, e, e.Focused(), false, RadiusPanel)
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
// a 15% ink edge at rest and one Ring point outside it while focused. Hover
// leaves the face alone. The focus edge is a neutral rather than the ink
// accent: a field's focus is a place in a form, not the screen's one primary
// action, so it reads quieter than the button beside it while staying above
// 3:1 against both the field and the surface around it. A scroll container
// around a field leaves Space(1) at each side for that point.
func FieldFace(c *ui.Context, field ui.Element, focused bool) ui.Element {
	return fieldFace(Colors(c.Theme()), field, focused, false)
}

// ChromeFieldFace is FieldFace for a field on chrome: the canvas face with
// a hairline, so it stays lighter than the bar it sits in.
func ChromeFieldFace(c *ui.Context, field ui.Element, focused bool) ui.Element {
	return fieldFace(Colors(c.Theme()), field, focused, false)
}

// SearchField is a one-line field that finds something in a list, with the
// magnifier that says so. onChrome draws it as a field on chrome: the canvas
// with a hairline rather than the well it is on the canvas.
func SearchField(c *ui.Context, value *string, label, placeholder string, onChrome bool) ui.Element {
	field, _ := SearchFieldInput(c, value, label, placeholder, onChrome)
	return field
}

// SearchFieldInput is SearchField with its text input, for a field a screen
// focuses from elsewhere.
func SearchFieldInput(c *ui.Context, value *string, label, placeholder string, onChrome bool) (ui.Element, ui.Element) {
	t := c.Theme()
	field := ui.Row(c).Height(HeightSearchField).AlignItems(ui.Center).Gap(t.Space(SpaceXS)).Padding(0, t.Space(SpaceM))
	var in ui.Element
	field.Children(func() {
		ui.Icon(c, searchIcon).Size(IconInline, IconInline).TextColor(t.TextMuted)
		// The face is the row's: the input inside carries no border or fill
		// of its own, so a hairline is drawn once.
		in = ui.TextInput(c, value).Height(HeightSearchField).Padding(0).FontSize(Scaled(TextSmall)).Grow(1).MinWidth(0).
			FocusRing(false).Background(ui.Color{}).Border(0, ui.Color{}).
			Label(label).Placeholder(placeholder)
	})
	var radius float32 = RadiusPanel
	if onChrome {
		radius = RadiusControl
	}
	// Search is a near-white surface with a 15% edge, not the Input well used
	// by ordinary text fields, on either chrome or canvas.
	fieldFaceRadius(Colors(t), field, in.Focused(), false, radius)
	return field, in
}

// fieldFace draws a field's face inside its own edges, so its box stays the
// control height whether or not it has the focus. A faced field's Draw belongs
// to the kit: MyGo's Draw is a single slot, so the edge replaces any Draw a
// caller set, and a Draw set after it would drop the edge.
func fieldFace(p *Palette, field ui.Element, focused, invalid bool) ui.Element {
	return fieldFaceRadius(p, field, focused, invalid, RadiusControl)
}

// fieldFaceRadius is fieldFace at a caller's corner radius: a text input is a
// 6-point field, its selects and search fields are 8-point. Every face is the
// near-white background, with the 15% edge carrying the shape.
func fieldFaceRadius(p *Palette, field ui.Element, focused, invalid bool, radius float32) ui.Element {
	face := p.Background
	// The face's radius also clips what sits inside it: a method control's
	// hover or a chip's fill follows the field's corners instead of squaring
	// them off.
	field.Radius(radius).Background(face).Clip()
	// A 15% ink edge stays at rest; hover does not change it. Focus adds one
	// ring point outside that edge, without a halo.
	edge := p.Foreground.Alpha(0.15)
	ring := p.Ring
	if invalid {
		edge, ring = p.Danger, p.Danger
	}
	fieldEdge(field, edge, radius)
	if focused {
		field.Shadow(0, 0, 0, HairlineWidth, ring)
	}
	return field
}

// fieldEdge draws a field's edge round its whole box. An inset shadow stops
// at the padding edge, so a field whose text room is a transparent right
// border (TextInset) would leave that border's side bare; a stroke on the
// box does not.
func fieldEdge(field ui.Element, color ui.Color, radius float32) {
	field.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Stroke(r, color, radius, HairlineWidth)
	})
}

// Select picks one of a few options at control height, with a button's face.
// It is SelectLabels with each option named as itself.
func Select(c *ui.Context, selected *string, options []string) ui.Element {
	return SelectLabels(c, selected, options, func(option string) string { return option })
}

// SelectLabels is Select for values a screen names in its own language: the
// stored value stays option, while the trigger and the popup show label of
// it. The app uses it where an option is an identifier it compares, such as
// "Raw" or "In", and the screen says "原始" or "入". Every select on a surface
// shares this trigger and popup, so they read alike.
func SelectLabels(c *ui.Context, selected *string, options []string, label func(string) string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	sel := ui.SelectBase(c, selected)
	trigger := selectTrigger(c, sel.Trigger, sel.Open()).Justify(ui.SpaceBetween).TextColor(t.Text)
	trigger.Children(func() {
		ui.Text(c, label(*selected)).SingleLine()
		chevron(c)
	})
	sel.Popup(func(panel ui.Element) {
		menuTheme(c, func() {
			// MyGo's popover helper attaches a Select panel flush to its
			// trigger. Select and Menu poppers keep sideOffset 4.
			panel.Margin(t.Space(SpaceXS), 0, 0, 0)
			popoverFace(c, panel.Padding(t.Space(SpaceXS)).Gap(t.Space(SpaceHair)))
			for _, option := range options {
				// Menu item: one 7% wash while highlighted, the
				// chosen option marked with a check in the same ink.
				item := sel.Item(option).Height(MenuRowHeight).Padding(0, t.Space(SpaceM)).
					Radius(RadiusInner).AlignItems(ui.Center).Gap(t.Space(SpaceS))
				ink := t.Text
				if item.Highlighted() {
					item.Background(p.Accent).TextColor(t.Text)
				}
				item.Children(func() {
					ui.Text(c, label(option)).FontSize(Scaled(TextSmall)).SingleLine().Grow(1).MinWidth(0)
					if option == *selected {
						ui.Icon(c, checkIcon).Size(IconInline, IconInline).TextColor(ink).Shrink(0)
					}
				})
			}
		})
	})
	return trigger
}

// selectTrigger gives a select's trigger the select face: the panel
// color in an 8-point field, with a 15% ink edge that does not react to the
// pointer and one Ring point outside it while focused. A select's trigger is
// a button with no look of its own, so the face is painted here.
func selectTrigger(c *ui.Context, b ui.Element, _ bool) ui.Element {
	p := Colors(c.Theme())
	b = fieldFaceRadius(p, b, b.Focused(), false, RadiusPanel)
	return b.Height(HeightControl).Padding(0, Scaled(ControlPaddingX)).FontSize(Scaled(TextBody)).
		AlignItems(ui.Center).MinWidth(WidthPicker)
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
