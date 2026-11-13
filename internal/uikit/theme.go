package uikit

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// Palette is one theme as semantic roles. Screens name a role, never a color,
// so a palette can be swapped (Paper, Ink, later imported themes) without
// touching them. Sizes, density and shapes are not part of a palette.
type Palette struct {
	// Surfaces carry structure. Chrome frames the window: sidebars, bars and
	// table heads. Canvas holds data. Well is a field set into the canvas; a
	// field on chrome is Canvas with a hairline instead. Hairline separates
	// two regions of the same surface, never two surfaces.
	Chrome, Canvas, Well, Hairline ui.Color
	// Sidebar is the card beside the content: the same canvas family, a step
	// away from it, so two cards on the shell read as one workspace.
	Sidebar ui.Color
	// Fill, FillHover and FillPressed are the text color laid thinly over
	// whatever is beneath: the face of a control at rest, hovered and
	// pressed, so one button reads the same on every surface.
	Fill, FillHover, FillPressed                               ui.Color
	Text, TextMuted, TextFaint                                 ui.Color
	Accent, AccentHover, AccentPressed, AccentText, AccentTint ui.Color
	// Success is green and nothing else is: a check that passed, a capture
	// that is recording. The accent is ink, so "selected", "primary" and
	// "on" are drawn in the text color and green never has to mean two
	// things at once.
	Success                                    ui.Color
	Info, Warning, Danger                      ui.Color
	MethodGet, MethodPost, MethodPatch, Socket ui.Color
	// Inverse fills tooltips, so they stand out from what they cover.
	Inverse, InverseText ui.Color
	// Shadow is the soft lift under a popover or a dialog; the hairline
	// draws its edge.
	Shadow ui.Color
}

// fills derives the three control faces from the palette's text color.
func fills(p Palette, rest, hover, pressed float32) Palette {
	p.Fill, p.FillHover, p.FillPressed = p.Text.Alpha(rest), p.Text.Alpha(hover), p.Text.Alpha(pressed)
	return p
}

// Paper is the light theme: warm neutral gray-white, with ink for anything
// selected, primary or on, and green only for what passed or is recording.
// TextFaint is only for icons and placeholders; it carries no information.
var Paper = fills(Palette{
	Chrome: ui.Hex("#edf3ea"), Canvas: ui.Hex("#f9fdf6"), Well: ui.Hex("#ebebeb"),
	Hairline: ui.Hex("#ebefe8"), Sidebar: ui.Hex("#f3f8f0"),
	Text:     ui.Hex("#0b0d0b"), TextMuted: ui.Hex("#595959"), TextFaint: ui.Hex("#8a8f88"),
	Accent: ui.Hex("#141614"), AccentHover: ui.Hex("#2a2d2a"), AccentPressed: ui.Hex("#3d413d"),
	AccentText: ui.Hex("#f9fdf6"), AccentTint: ui.Hex("#d6dcd6"),
	Success: ui.Hex("#2f8052"),
	Info:    ui.Hex("#2f6ea5"), Warning: ui.Hex("#86600f"), Danger: ui.Hex("#c44f4a"),
	MethodGet: ui.Hex("#2f6ea5"), MethodPost: ui.Hex("#5b6b1c"), MethodPatch: ui.Hex("#7a3e6d"),
	Socket:  ui.Hex("#2b6868"),
	Inverse: ui.Hex("#0b0d0b"), InverseText: ui.Hex("#f9fdf6"),
	Shadow: ui.RGBA(11, 13, 11, 0.16),
}, 0.07, 0.11, 0.15)

// Ink is the dark theme: the same warm neutral structure on a black board,
// with the accent lightened to carry on it and green kept for pass and
// recording only. Chrome is the window behind the panel and Canvas the panel
// itself, so the content sits a step above the shell as it does in Paper.
var Ink = fills(Palette{
	// The shell is the darker of the two in Ink: the cards are raised, so
	// they are the lighter surfaces, as they are in Paper.
	Chrome: ui.Hex("#070807"), Canvas: ui.Hex("#0c1516"), Well: ui.Hex("#192121"),
	Hairline: ui.Hex("#1b2324"), Sidebar: ui.Hex("#0c1313"),
	Text:     ui.Hex("#f6fff5"), TextMuted: ui.Hex("#9ca49c"), TextFaint: ui.Hex("#6d766e"),
	Accent: ui.Hex("#eaf3e9"), AccentHover: ui.Hex("#ffffff"), AccentPressed: ui.Hex("#cfd8ce"),
	AccentText: ui.Hex("#0c1516"), AccentTint: ui.Hex("#2f372f"),
	Success: ui.Hex("#54b879"),
	Info:    ui.Hex("#8fb3d1"), Warning: ui.Hex("#d6a038"), Danger: ui.Hex("#d67670"),
	MethodGet: ui.Hex("#8fb3d1"), MethodPost: ui.Hex("#b5c27a"), MethodPatch: ui.Hex("#c99abd"),
	Socket:  ui.Hex("#6fc3d6"),
	Inverse: ui.Hex("#f6fff5"), InverseText: ui.Hex("#0c1516"),
	Shadow: ui.RGBA(0, 0, 0, 0.5),
}, 0.07, 0.11, 0.15)

// Theme is MyGo's theme for a palette. Background is the canvas and Surface
// is chrome: MyGo's own table heads, toolbars and panels frame the window,
// while every field draws its own face (well on the canvas, canvas with a
// hairline on chrome) instead of sharing the window's color.
func Theme(dark bool) *ui.Theme {
	registerFonts()
	p, t := Paper, ui.LightTheme()
	if dark {
		p, t = Ink, ui.DarkTheme()
	}
	// MyGo has one Surface: it paints the table heads, drag previews and
	// panels it draws itself. The content panel is Canvas, so Surface is
	// Canvas too; the shell's chrome is painted by the app, which knows
	// which parts of the window are the frame.
	t.Background, t.Surface = p.Canvas, p.Canvas
	t.SurfaceHover, t.SurfacePressed, t.Border = p.FillHover, p.FillPressed, p.Hairline
	t.Text, t.TextMuted = p.Text, p.TextMuted
	t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = p.Accent, p.AccentHover, p.AccentPressed, p.AccentText
	// Green is the success signal alone: 2xx stays quiet, and a pass or a
	// recording is the only thing that is ever green.
	t.Danger, t.Warning, t.Success = p.Danger, p.Warning, p.Success
	t.Inverse, t.InverseText = p.Inverse, p.InverseText
	t.Font, t.FontSize, t.Radius, t.Spacing = FontUI, TextBody, RadiusControl, 4
	t.Selection, t.Focus = p.Accent.Alpha(0.24), p.Accent
	return t
}

// Colors is the palette behind a theme from Theme. Copies of the theme
// (SetTheme with a tweaked copy) keep their palette.
func Colors(t *ui.Theme) *Palette {
	if t.Dark {
		return &Ink
	}
	return &Paper
}

// MethodColor is the ink an HTTP method is printed in.
func MethodColor(method string, t *ui.Theme) ui.Color {
	p := Colors(t)
	switch method {
	case "GET":
		return p.MethodGet
	case "POST":
		return p.MethodPost
	case "PUT":
		return p.Warning
	case "DELETE":
		return p.Danger
	case "PATCH":
		return p.MethodPatch
	case "WS":
		return p.Socket
	default:
		return p.TextMuted
	}
}

// StatusColor keeps normal responses quiet: only redirects, client errors
// and server errors are colored. 0 is no response yet.
func StatusColor(status int, t *ui.Theme) ui.Color {
	p := Colors(t)
	switch {
	case status < 300 || status == 304:
		return p.TextMuted
	case status < 400:
		return p.Info
	case status < 500:
		return p.Warning
	default:
		return p.Danger
	}
}

// EditorTheme colors bodies and authored code in the palette's inks rather
// than error colors: a key in the text color, a string in the method patch's
// plum, a number or a boolean in the info blue. Green is the success signal
// alone, so the token kinds the library paints green (regular expressions,
// tags, inserted text) take one of those three instead.
func EditorTheme(t *ui.Theme) *editor.Theme {
	p := Colors(t)
	theme := editor.DefaultTheme(t)
	theme.Tokens[editor.TokenProperty].Color = p.Text
	theme.Tokens[editor.TokenString].Color = p.MethodPatch
	theme.Tokens[editor.TokenNumber].Color = p.Info
	// Punctuation is not a fault: red belongs to errors alone.
	theme.Tokens[editor.TokenOperator].Color = p.TextMuted
	theme.Tokens[editor.TokenConstant].Color = p.Info
	theme.Tokens[editor.TokenRegexp].Color = p.MethodPatch
	theme.Tokens[editor.TokenTag].Color = p.Info
	theme.Tokens[editor.TokenInserted].Color = p.Info
	return theme
}

// Swatches are labels a person picks, not semantic roles: they keep one
// saturated value in both themes and always carry SwatchText.
type Swatch struct {
	Name  string
	Color ui.Color
}

var SwatchText = ui.Hex("#ffffff")

// MarkColors are the colors a request can be marked with, in menu order.
var MarkColors = []Swatch{
	{"Red", ui.Hex("#dc2626")},
	{"Orange", ui.Hex("#ea580c")},
	{"Yellow", ui.Hex("#ca8a04")},
	{"Green", ui.Hex("#16a34a")},
	{"Blue", ui.Hex("#2563eb")},
	{"Purple", ui.Hex("#7c3aed")},
}

// BadgeColors color a client's initial when the system has no icon for it.
var BadgeColors = []ui.Color{
	ui.Hex("#2563eb"), ui.Hex("#7c3aed"), ui.Hex("#db2777"), ui.Hex("#ea580c"),
	ui.Hex("#0891b2"), ui.Hex("#16a34a"), ui.Hex("#ca8a04"),
}
