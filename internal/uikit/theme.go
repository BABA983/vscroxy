package uikit

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// Palette is one theme as semantic roles. Screens name a role, never a color,
// so a palette can be swapped (Paper, Ink, later imported themes) without
// touching them. Sizes, density and shapes are not part of a palette.
type Palette struct {
	Background, Panel, Hover, Pressed, Border  ui.Color
	Text, TextMuted, TextFaint                 ui.Color
	Accent, AccentHover, AccentPressed         ui.Color
	AccentText, AccentTint                     ui.Color
	Info, Warning, Danger                      ui.Color
	MethodGet, MethodPost, MethodPatch, Socket ui.Color
	// Inverse fills tooltips, so they stand out from what they cover.
	Inverse, InverseText ui.Color
}

// Paper is the light theme: ink on warm paper. TextFaint is only for
// disabled controls and placeholders; it carries no information.
var Paper = Palette{
	Background: ui.Hex("#f7f4ec"), Panel: ui.Hex("#efebe0"),
	Hover: ui.Hex("#e9e4d6"), Pressed: ui.Hex("#e0dacb"), Border: ui.Hex("#d3ccba"),
	Text: ui.Hex("#1f2a24"), TextMuted: ui.Hex("#6d6a5f"), TextFaint: ui.Hex("#a29d8f"),
	Accent: ui.Hex("#1e5b47"), AccentHover: ui.Hex("#174a3a"), AccentPressed: ui.Hex("#123d30"),
	AccentText: ui.Hex("#f7f4ec"), AccentTint: ui.Hex("#e3ece4"),
	Info: ui.Hex("#396482"), Warning: ui.Hex("#8d520e"), Danger: ui.Hex("#ab382c"),
	MethodGet: ui.Hex("#2f5d8a"), MethodPost: ui.Hex("#58661e"), MethodPatch: ui.Hex("#7a3e6d"),
	Socket:  ui.Hex("#2b6868"),
	Inverse: ui.Hex("#1f2a24"), InverseText: ui.Hex("#f7f4ec"),
}

// Ink is the dark theme: warm paper-colored ink on a green-black board.
var Ink = Palette{
	Background: ui.Hex("#121614"), Panel: ui.Hex("#181d1a"),
	Hover: ui.Hex("#1f2622"), Pressed: ui.Hex("#26302a"), Border: ui.Hex("#343c37"),
	Text: ui.Hex("#e6e1d3"), TextMuted: ui.Hex("#9a978b"), TextFaint: ui.Hex("#66665e"),
	Accent: ui.Hex("#6fb593"), AccentHover: ui.Hex("#82c4a4"), AccentPressed: ui.Hex("#5ea482"),
	AccentText: ui.Hex("#121614"), AccentTint: ui.Hex("#1f2d26"),
	Info: ui.Hex("#8fb3d1"), Warning: ui.Hex("#e0a458"), Danger: ui.Hex("#e8806f"),
	MethodGet: ui.Hex("#8fb3d1"), MethodPost: ui.Hex("#b5c27a"), MethodPatch: ui.Hex("#c99abd"),
	Socket:  ui.Hex("#7cc4c0"),
	Inverse: ui.Hex("#e6e1d3"), InverseText: ui.Hex("#121614"),
}

// Theme is MyGo's theme for a palette. Surface is the face of MyGo's own
// controls (inputs, buttons, table headers), which sit on the background
// with a border; Panel is for the app's sidebars and bars.
func Theme(dark bool) *ui.Theme {
	registerFonts()
	p, t := Paper, ui.LightTheme()
	if dark {
		p, t = Ink, ui.DarkTheme()
	}
	t.Background, t.Surface = p.Background, p.Background
	t.SurfaceHover, t.SurfacePressed, t.Border = p.Hover, p.Pressed, p.Border
	t.Text, t.TextMuted = p.Text, p.TextMuted
	t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = p.Accent, p.AccentHover, p.AccentPressed, p.AccentText
	// Success shares the accent: 2xx is quiet, so green only ever means
	// "this check passed" or "this is on", never a normal response.
	t.Danger, t.Warning, t.Success = p.Danger, p.Warning, p.Accent
	t.Inverse, t.InverseText = p.Inverse, p.InverseText
	t.Font, t.FontSize, t.Radius, t.Spacing = FontUI, TextBody, RadiusControl, 4
	t.Selection, t.Focus = p.Accent.Alpha(0.3), p.Accent
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
// than error colors.
func EditorTheme(t *ui.Theme) *editor.Theme {
	p := Colors(t)
	theme := editor.DefaultTheme(t)
	theme.Tokens[editor.TokenProperty].Color = p.Socket
	theme.Tokens[editor.TokenString].Color = p.MethodPost
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
