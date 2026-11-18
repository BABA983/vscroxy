package uikit

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// Palette is one theme as semantic roles. Screens name a role, never a color,
// so a palette can be swapped (Light, Dark, later imported themes) without
// touching them. Sizes, density and shapes are not part of a palette.
type Palette struct {
	// Surfaces carry structure. Window is the shell behind sidebars, bars and
	// the content card. Background holds data. Input is a field set into the
	// content; a field on the shell is Background with a hairline instead.
	// Border separates two regions of the same surface, never two surfaces.
	Window, Background, Input, Border ui.Color
	// Muted, MutedHover and MutedPressed are the text color laid thinly over
	// whatever is beneath: the face of a control at rest, hovered and
	// pressed, so one button reads the same on every surface.
	Muted, MutedHover, MutedPressed                          ui.Color
	Foreground, MutedForeground, Faint                       ui.Color
	Primary, PrimaryHover, PrimaryPressed, PrimaryForeground ui.Color
	// Accent is a low-alpha wash of Foreground for a chosen row or an active
	// tool: AMP's --accent and --sidebar-accent are the same kind of face,
	// a tint of ink over whatever surface the row sits on.
	Accent ui.Color
	// Ring is the keyboard focus color: a quiet, readable neutral that is
	// distinct from Primary, so a focused control and the screen's one main
	// action never look like the same thing.
	Ring ui.Color
	// SwitchOn is the track of an on switch. In Light it is the ink Primary;
	// in Dark the ink is nearly white, so it would disappear under the white
	// thumb. The Dark value keeps a 3:1 edge against the thumb and still
	// separates clearly from the disabled half-opacity face.
	SwitchOn ui.Color
	// Success is green and nothing else is: a check that passed, a capture
	// that is recording. Primary is ink, so "selected", "primary" and
	// "on" are drawn in the text color and green never has to mean two
	// things at once.
	Success                                    ui.Color
	Info, Warning, Danger                      ui.Color
	MethodGet, MethodPost, MethodPatch, Socket ui.Color
	// Inverse fills tooltips, so they stand out from what they cover.
	Inverse, InverseText ui.Color
	// Scrim dims what a dialog covers: black at 35% in both themes, enough
	// to put the dialog forward without hiding the screen behind it.
	Scrim ui.Color
	// Shadow is the soft lift under a popover or a dialog; the hairline
	// draws its edge. The three Shadow tokens carry the exact elevations.
	Shadow ui.Color
}

// fills derives the three control faces from the palette's text color.
func fills(p Palette, rest, hover, pressed float32) Palette {
	p.Muted, p.MutedHover, p.MutedPressed = p.Foreground.Alpha(rest), p.Foreground.Alpha(hover), p.Foreground.Alpha(pressed)
	p.Accent = p.Foreground.Alpha(0.09)
	return p
}

// Light is the light theme, Amp's Daydream: a green-tinted gray-white, with
// ink for anything selected, primary or on, and green only for what passed or
// is recording. Every surface keeps Daydream's green tint, fields included,
// and the shell is Daydream's own outer shade, a step under the content panel,
// so the panel's edge reads without a heavier line. Faint is only for
// icons and placeholders; it carries no information.
var Light = fills(Palette{
	Window: ui.Hex("#eff3ee"), Background: ui.Hex("#f7fbf6"), Input: ui.Hex("#e8ede5"),
	Border:     ui.Hex("#dde4d9"),
	Foreground: ui.Hex("#0b0d0b"), MutedForeground: ui.Hex("#595959"), Faint: ui.Hex("#8a8f88"),
	Primary: ui.Hex("#141614"), PrimaryHover: ui.Hex("#2a2d2a"), PrimaryPressed: ui.Hex("#3d413d"),
	PrimaryForeground: ui.Hex("#f7fbf6"),
	Ring:              ui.Hex("#636e61"),
	SwitchOn:          ui.Hex("#141614"),
	Success:           ui.Hex("#2c784d"),
	Info:              ui.Hex("#2f6ea5"), Warning: ui.Hex("#86600f"), Danger: ui.Hex("#b9423c"),
	MethodGet: ui.Hex("#2f6ea5"), MethodPost: ui.Hex("#5b6b1c"), MethodPatch: ui.Hex("#7a3e6d"),
	Socket:  ui.Hex("#2b6868"),
	Inverse: ui.Hex("#0b0d0b"), InverseText: ui.Hex("#f7fbf6"),
	Scrim:  ui.RGBA(0, 0, 0, 0.35),
	Shadow: ui.RGBA(11, 13, 11, 0.16),
}, 0.07, 0.11, 0.15)

// Dark is the dark theme: the same warm neutral structure on a black board,
// with the accent lightened to carry on it and green kept for pass and
// recording only. Window is the window behind the panel and Background the panel
// itself, so the content sits a step above the shell as it does in Light.
var Dark = fills(Palette{
	// The shell is the darker of the two in Dark: the cards are raised, so
	// they are the lighter surfaces, as they are in Light. It keeps a little
	// of the panel's green, so the window is one material in two depths.
	Window: ui.Hex("#070a08"), Background: ui.Hex("#101612"), Input: ui.Hex("#171f1a"),
	Border:     ui.Hex("#1b2324"),
	Foreground: ui.Hex("#f6fff5"), MutedForeground: ui.Hex("#9ca49c"), Faint: ui.Hex("#6d766e"),
	Primary: ui.Hex("#eaf3e9"), PrimaryHover: ui.Hex("#ffffff"), PrimaryPressed: ui.Hex("#cfd8ce"),
	PrimaryForeground: ui.Hex("#101612"),
	Ring:              ui.Hex("#9aa89a"),
	SwitchOn:          ui.Hex("#8a998a"),
	Success:           ui.Hex("#54b879"),
	Info:              ui.Hex("#8fb3d1"), Warning: ui.Hex("#d6a038"), Danger: ui.Hex("#d67670"),
	MethodGet: ui.Hex("#8fb3d1"), MethodPost: ui.Hex("#b5c27a"), MethodPatch: ui.Hex("#c99abd"),
	Socket:  ui.Hex("#6fc3d6"),
	Inverse: ui.Hex("#f6fff5"), InverseText: ui.Hex("#101612"),
	Scrim:  ui.RGBA(0, 0, 0, 0.35),
	Shadow: ui.RGBA(0, 0, 0, 0.5),
}, 0.07, 0.11, 0.15)

// Theme is MyGo's theme for a palette. Background is the content surface and
// Window is the shell: MyGo's own table heads, toolbars and panels frame the
// window, while every field draws its own face (Input on Background, Background
// with a hairline on Window) instead of sharing the shell color.
func Theme(dark bool) *ui.Theme {
	registerFonts()
	p, t := Light, ui.LightTheme()
	if dark {
		p, t = Dark, ui.DarkTheme()
	}
	// MyGo has one Surface: it paints the table heads, drag previews and
	// panels it draws itself. The content panel is Background, so Surface is
	// Background too; the shell is painted by the app, which knows which
	// parts of the window are the frame.
	t.Background, t.Surface = p.Background, p.Background
	t.SurfaceHover, t.SurfacePressed, t.Border = p.MutedHover, p.MutedPressed, p.Border
	t.Text, t.TextMuted = p.Foreground, p.MutedForeground
	t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = p.Primary, p.PrimaryHover, p.PrimaryPressed, p.PrimaryForeground
	// Green is the success signal alone: 2xx stays quiet, and a pass or a
	// recording is the only thing that is ever green.
	t.Danger, t.Warning, t.Success = p.Danger, p.Warning, p.Success
	t.Inverse, t.InverseText = p.Inverse, p.InverseText
	// Tips rest for 500ms, as VS Code's workbench hovers do on Linux and
	// Windows; MyGo's warm window, within which the next tip shows at once,
	// is two thirds of it.
	t.TooltipDelay = 500 * time.Millisecond
	t.Font, t.FontSize, t.Radius, t.Spacing = FontUI, Scaled(TextBody), RadiusControl, SpaceGrid
	t.Accent, t.Focus = p.Primary.Alpha(0.24), p.Ring
	return t
}

// Colors is the palette behind a theme from Theme. Copies of the theme
// (SetTheme with a tweaked copy) keep their palette.
func Colors(t *ui.Theme) *Palette {
	if t.Dark {
		return &Dark
	}
	return &Light
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
		return p.MutedForeground
	}
}

// StatusColor keeps normal responses quiet: only redirects, client errors
// and server errors are colored. 0 is no response yet.
func StatusColor(status int, t *ui.Theme) ui.Color {
	p := Colors(t)
	switch {
	case status < 300 || status == 304:
		return p.MutedForeground
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
	theme.Tokens[editor.TokenProperty].Color = p.Foreground
	theme.Tokens[editor.TokenString].Color = p.MethodPatch
	theme.Tokens[editor.TokenNumber].Color = p.Info
	// Punctuation is not a fault: red belongs to errors alone.
	theme.Tokens[editor.TokenOperator].Color = p.MutedForeground
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
