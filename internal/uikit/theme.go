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
	Muted, MutedHover, MutedPressed    ui.Color
	Foreground, MutedForeground, Faint ui.Color
	// Primary fills the one action a screen is for. Hover and pressed share
	// one face: Light lifts the ink, Dark lowers it.
	// A disabled primary is the same face at MyGo's half opacity, so none of
	// the four states moves the button's text, border or geometry.
	Primary, PrimaryHover, PrimaryPressed, PrimaryForeground ui.Color
	// Accent is Foreground at 7%: the accent, sidebar accent and highlighted
	// menu item are the same wash of ink over any surface.
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
	// Success is green only for status: a check that passed, a capture that
	// is recording. Syntax and diff use their own colors; syntax classes and
	// added lines being green does not make green a status signal there.
	Success                                    ui.Color
	Info, Warning, Danger                      ui.Color
	MethodGet, MethodPost, MethodPatch, Socket ui.Color
	// Editor is the GitHub Light/Dark token palette @pierre/diffs emits as
	// --diffs-token-light/dark. It is a different family from the syntax
	// colors that style prose in Markdown.
	EditorForeground, EditorKeyword, EditorEntity, EditorTag, EditorVariable, EditorConstant, EditorString, EditorRegexp, EditorComment, EditorInvalid ui.Color
	// Diff is the --diff-* override palette for added, removed and meta lines.
	DiffAdded, DiffRemoved, DiffMeta ui.Color
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
	p.Accent = p.Foreground.Alpha(0.07)
	return p
}

// Light is the light theme: a green-tinted gray-white, with
// ink for anything selected, primary or on, and green only for what passed or
// is recording. Every surface keeps Daydream's green tint, fields included,
// and the shell is Daydream's own outer shade, a step under the content panel,
// so the panel's edge reads without a heavier line. Faint is only for
// icons and placeholders; it carries no information.
var Light = fills(Palette{
	Window: ui.Hex("#eff3ee"), Background: ui.Hex("#f7fbf6"), Input: ui.Hex("#e8ede5"),
	Border:     ui.Hex("#dde4d9"),
	Foreground: ui.Hex("#0b0d0b"), MutedForeground: ui.Hex("#595959"), Faint: ui.Hex("#8a8f88"),
	Primary: ui.Hex("#171917"), PrimaryHover: ui.Hex("#2f312f"), PrimaryPressed: ui.Hex("#2f312f"),
	PrimaryForeground: ui.Hex("#fafaf8"),
	Ring:              ui.Hex("#636e61"),
	SwitchOn:          ui.Hex("#171917"),
	Success:           ui.Hex("#2c784d"),
	Info:              ui.Hex("#2f6ea5"), Warning: ui.Hex("#86600f"), Danger: ui.Hex("#b9423c"),
	MethodGet: ui.Hex("#2f6ea5"), MethodPost: ui.Hex("#5b6b1c"), MethodPatch: ui.Hex("#7a3e6d"),
	Socket:           ui.Hex("#2b6868"),
	EditorForeground: ui.Hex("#24292e"), EditorKeyword: ui.Hex("#d73a49"), EditorEntity: ui.Hex("#6f42c1"),
	EditorTag: ui.Hex("#22863a"), EditorVariable: ui.Hex("#e36209"), EditorConstant: ui.Hex("#005cc5"),
	EditorString: ui.Hex("#032f62"), EditorRegexp: ui.Hex("#032f62"), EditorComment: ui.Hex("#6a737d"),
	EditorInvalid: ui.Hex("#b31d28"),
	DiffAdded:     ui.Hex("#2f8052"), DiffRemoved: ui.Hex("#c44f4a"), DiffMeta: ui.Hex("#a07013"),
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
	Primary: ui.Hex("#eaf3e9"), PrimaryHover: ui.Hex("#d3dbd2"), PrimaryPressed: ui.Hex("#d3dbd2"),
	PrimaryForeground: ui.Hex("#0b0d0b"),
	Ring:              ui.Hex("#9aa89a"),
	SwitchOn:          ui.Hex("#8a998a"),
	Success:           ui.Hex("#54b879"),
	Info:              ui.Hex("#8fb3d1"), Warning: ui.Hex("#d6a038"), Danger: ui.Hex("#d67670"),
	MethodGet: ui.Hex("#8fb3d1"), MethodPost: ui.Hex("#b5c27a"), MethodPatch: ui.Hex("#c99abd"),
	Socket:           ui.Hex("#6fc3d6"),
	EditorForeground: ui.Hex("#e1e4e8"), EditorKeyword: ui.Hex("#f97583"), EditorEntity: ui.Hex("#b392f0"),
	EditorTag: ui.Hex("#85e89d"), EditorVariable: ui.Hex("#ffab70"), EditorConstant: ui.Hex("#79b8ff"),
	EditorString: ui.Hex("#9ecbff"), EditorRegexp: ui.Hex("#dbedff"), EditorComment: ui.Hex("#6a737d"),
	EditorInvalid: ui.Hex("#fdaeb7"),
	DiffAdded:     ui.Hex("#4ba86e"), DiffRemoved: ui.Hex("#d67670"), DiffMeta: ui.Hex("#d6a038"),
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
	// A tip appears about 120ms after the pointer settles; MyGo's warm
	// window, within which the next tip shows at once, follows that delay.
	t.TooltipDelay = 120 * time.Millisecond
	t.Font, t.FontSize, t.Radius, t.Spacing = FontUI, Scaled(TextBody), RadiusControl, SpaceGrid
	// Selection is the low-alpha wash for rows; Accent stays the ink that
	// fills the primary button. Writing the wash into Accent here would make
	// a resting primary button faint while its hover and press were solid.
	// The keyboard ring is one point with no offset; MyGo defaults to two
	// points one point out.
	t.Selection, t.Focus = p.Accent, p.Ring
	t.FocusRingWidth, t.FocusRingOffset = 1, 0
	// Scroll bars follow VS Code's editor: a square 14-point thumb flush with
	// its track's ends, and a thumb that darkens or lightens from the
	// palette's secondary ink to the primary ink while dragged. Every scroll
	// container takes this from the theme, and so does the code editor.
	t.ScrollbarStyle = ui.ScrollbarStyle{
		TrackWidth: 14, ThumbWidth: 14, Square: true, Flush: true, MinLength: 20,
		Thumb:  p.MutedForeground.Alpha(0.4),
		Hover:  p.MutedForeground.Alpha(0.7),
		Active: p.Foreground.Alpha(0.4),
	}
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

// ScrollbarInset is the room an overlay scroll bar takes at the end of a
// scrolling pane: the width of its track and hit area, as the theme resolves
// it. A form pane pads its end by this so a control or footnote at the row's
// end never sits under the bar.
func ScrollbarInset(t *ui.Theme) float32 {
	return ui.ScrollbarStyle{}.Resolve(t).TrackWidth
}

// OverlayScrollbar is the thin, rounded bar of a table that scrolls under the
// pointer's eye: a six-point thumb with three points of air, its track as wide
// as the thumb's hit area, and no square block at the pane's edge. It keeps
// the palette's ink and appears while the pointer is over the rows or they
// move, as MyGo's scroll containers do by default. A scrolling table uses it
// instead of the editor's square 14-point bar.
func OverlayScrollbar(t *ui.Theme) ui.ScrollbarStyle {
	p := Colors(t)
	return ui.ScrollbarStyle{
		TrackWidth: 12, ThumbWidth: 6,
		Thumb:  p.MutedForeground.Alpha(0.4),
		Hover:  p.MutedForeground.Alpha(0.7),
		Active: p.Foreground.Alpha(0.4),
	}
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

// EditorThemeFor is EditorTheme with the caret's own marks, the active line
// and the box around the matching bracket, only while the editor has the
// keyboard. A code editor the app draws directly uses it; requestCodeEditor
// does too. A zero color means "the default" to the editor's theme resolver,
// so the hidden marks are transparent white, whose channels are not zero.
func EditorThemeFor(t *ui.Theme, focused bool) *editor.Theme {
	theme := EditorTheme(t)
	if !focused {
		hidden := ui.RGBA(255, 255, 255, 0)
		theme.ActiveLine = hidden
		theme.ActiveLineNumber = theme.LineNumber
		theme.MatchingBracket = hidden
	}
	return theme
}

// EditorTheme maps the GitHub token palette that @pierre/diffs emits as
// --diffs-token-light/dark onto MyGo's editor kinds. Syntax colors style prose
// and are a different family. An unchanged diff line has no diff fill: its
// code keeps these token colors; only added and removed lines get a diff fill
// from the --diff-* overrides. MyGo derives that fill from Added/Deleted.
func EditorTheme(t *ui.Theme) *editor.Theme {
	p := Colors(t)
	theme := editor.DefaultTheme(t)
	theme.Text = p.EditorForeground
	set := func(kind editor.TokenKind, color ui.Color) {
		theme.Tokens[kind] = editor.TokenStyle{Color: color}
	}
	set(editor.TokenKeyword, p.EditorKeyword)
	set(editor.TokenOperator, p.EditorKeyword)
	set(editor.TokenType, p.EditorEntity)
	set(editor.TokenFunction, p.EditorEntity)
	set(editor.TokenMeta, p.EditorEntity)
	set(editor.TokenVariable, p.EditorVariable)
	set(editor.TokenProperty, p.EditorConstant)
	set(editor.TokenAttribute, p.EditorConstant)
	set(editor.TokenConstant, p.EditorConstant)
	set(editor.TokenNumber, p.EditorConstant)
	set(editor.TokenString, p.EditorString)
	set(editor.TokenEscape, p.EditorConstant)
	set(editor.TokenRegexp, p.EditorRegexp)
	set(editor.TokenTag, p.EditorTag)
	set(editor.TokenComment, p.EditorComment)
	set(editor.TokenHeading, p.EditorConstant)
	set(editor.TokenLink, p.EditorString)
	set(editor.TokenCode, p.EditorConstant)
	set(editor.TokenInvalid, p.EditorInvalid)
	set(editor.TokenInserted, p.DiffAdded)
	set(editor.TokenDeleted, p.DiffRemoved)
	theme.Added, theme.Deleted, theme.Modified = p.DiffAdded, p.DiffRemoved, p.DiffMeta
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
