package uikit

import (
	"strings"

	"github.com/egoist/mygo/ui"
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
			b.Background(p.MutedPressed)
		case b.Hovered():
			b.Background(p.MutedHover)
		case active:
			b.Background(p.Accent)
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
		b.Background(p.MutedPressed)
	case b.Hovered():
		b.Background(p.MutedHover)
	case on:
		// The selected segment's face: raised canvas with its hairline.
		b.Background(p.Background).InsetShadow(0, 0, 0, HairlineWidth, p.Border)
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

// PrimaryButton is the one action a screen or dialog is for. Its face is ink,
// and green is left to Success so a primary action never signals a pass.
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

// SecondaryButton is an outlined ghost: clear at rest with a hairline, a 5%
// ink wash under the pointer, and 8 points of side room. It reads the same on
// the window, a panel or a selected row.
func SecondaryButton(c *ui.Context, label string) ui.Element {
	return secondary(c, label, HeightSecondary, ControlPaddingXSmall, TextSmall)
}

// secondary is the face behind SecondaryButton and CompactButton. The button
// stays clear until hovered, so neither the resting wash nor the pressed face
// replaces the hairline.
func secondary(c *ui.Context, label string, height, inset, size float32) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	b := ui.Button(c, label).Height(height).Padding(0, Scaled(inset)).FontSize(Scaled(size)).
		Radius(RadiusInner).Border(HairlineWidth, p.Border).TextColor(t.Text)
	if !b.IsDisabled() && (b.Hovered() || b.Pressed()) {
		b.Background(p.Foreground.Alpha(0.05))
	}
	return b
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
// clear/hairline face at the smaller control height and meta type size.
func CompactButton(c *ui.Context, label string) ui.Element {
	return secondary(c, label, HeightControlSmall, ControlPaddingXSmall, TextMeta)
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
		b.Background(p.MutedHover).TextColor(t.Text)
	}
	return b
}

// Kbd draws an inline shortcut keycap: transparent face, a 10% ring,
// shadow-sm, an 11-point label and ss06/ss07 symbol features. Menu and command
// rows use it; it also keeps a small gap and even width for the final key.
func Kbd(c *ui.Context, key string) ui.Element {
	t := c.Theme()
	prefixLen := 0
	for _, r := range key {
		if !strings.ContainsRune("⌘⇧⌥⌃", r) {
			break
		}
		prefixLen += len(string(r))
	}
	prefix, last := key[:prefixLen], key[prefixLen:]
	if last == "" {
		prefix, last = "", key
	}
	// TextAlign centers the glyph inside MinWidth; Center would align the
	// whole text element inside the keycap and leave the last key too far left.
	ink := func(text string, minWidth float32) ui.Element {
		return ui.Text(c, text).FontSize(Scaled(TextMeta)).TextColor(t.TextMuted).
			FontFeatures("ss06", "ss07").SingleLine().MinWidth(minWidth).TextAlign(ui.Center)
	}
	e := ui.Row(c).Gap(t.Space(SpaceHair)).AlignItems(ui.Center).
		Height(HeightInlineKbd).Padding(t.Space(SpaceXS)).Radius(RadiusInner).
		Margin(-t.Space(SpaceHair), 0).Center()
	e.Children(func() {
		if prefix != "" {
			ink(prefix, 0)
		}
		ink(last, Scaled(TextMeta)*0.75)
	})
	return ShadowKbd.Apply(e, t.Dark)
}

// DestructiveButton is the one action a destructive confirmation is for. It
// fills with Danger and prints in the palette's canvas text; it belongs in a
// dialog's footer, not in a row or a menu. A destructive action outside a
// dialog asks first, or is a menu item.
func DestructiveButton(c *ui.Context, label string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	face := *t
	face.Accent, face.AccentText = p.Danger, p.PrimaryForeground
	face.AccentHover = p.Danger.Mix(p.Foreground, 0.12)
	face.AccentPressed = p.Danger.Mix(p.Foreground, 0.22)
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
	face.Surface, face.SurfaceHover, face.SurfacePressed = p.Muted, p.MutedHover, p.MutedPressed
	face.Border = ui.Color{}
	c.SetTheme(&face)
	e := build()
	c.SetTheme(t)
	return e
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
