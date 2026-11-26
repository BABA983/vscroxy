// Package uikit is vscroxy's design system: the tokens, themes and
// components every screen is built from. It depends only on MyGo; screens
// name roles and variants here instead of colors and sizes.
package uikit

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// Shared metrics. Container geometry stays with each screen; these describe
// controls, not a new layout for the shell or inspector.
//
// Units: heights, insets, widths and radii are points the kit fixes. Spacing
// is the one exception: the Space names are multiples of the theme's grid,
// read through t.Space, so a local theme can tighten a dense table.
const (
	// Sizes follow the text scale: 11/12/13/15/16/20 at the usual
	// font size, with two weights (400 and 500). Monospace is only for wire
	// data: paths, header values, numbers and bodies.
	WeightMedium = 500
	TextMeta     = 11 // counts, timestamps and sizes
	TextData     = 12 // monospace data in rows and fields
	TextSmall    = 12 // secondary interface text and popover descriptions
	TextBody     = 13 // prose, labels and controls
	TextMedium   = 15 // the scale's md step
	TextTitle    = 16 // a dialog, a pane or the request an inspector shows
	TextDisplay  = 20 // a first-run title or a command palette's heading
	// Line heights are multiples of the font size, pinned to the text
	// scale: 11/13.6, 12/16, 13/20, 15/20, 16/24 and 20/28. Prose uses
	// the body's 20/13 leading.
	LineMeta    = 13.6 / 11.0
	LineSmall   = 16.0 / 12.0
	LineBody    = 20.0 / 13.0
	LineMedium  = 20.0 / 15.0
	LineProse   = LineBody
	LineTitle   = 24.0 / 16.0
	LineDisplay = 28.0 / 20.0

	// SpaceGrid is the default theme's grid in points, which every Space name
	// multiplies. A local theme may tighten it, as a dense table does.
	SpaceGrid = 4

	// Spacing names, as multiples of the grid (4 points under the default
	// theme). Screens use these names, never a bare number, so a gap means
	// the same thing in every pane.
	SpaceHair    = 0.5 // 2: a hairline between two marks
	SpaceXS      = 1   // 4: an icon and its label, tabs in a row
	SpaceS       = 1.5 // 6: inside a control, between a title and its meta
	SpaceM       = 2   // 8: controls in one row
	SpaceCompact = 2.5 // 10: a tip's and a toast's inner gap
	SpaceL       = 3   // 12: a panel's padding and the gap inside a group
	SpaceXL      = 4   // 16: between groups
	SpaceXXL     = 5   // 20: a large group's gap
	Space2XL     = 6   // 24: between sections
	Space3XL     = 8   // 32: between the parts of a page

	// Bar heights, as in Proxyman and Xcode: the title bar, a pane header or
	// filter bar, and the one-row strip that holds a sidebar heading or the
	// activity line.
	HeightBar    = 40
	HeightTabs   = 32
	HeightStatus = 24 // the activity strip's one row and a sidebar section heading
	// HeightFooter is the footer bar under the window: its actions, counts and
	// the tools that are on.
	HeightFooter = 28
	// Heights of what a person presses, types in or picks. HeightControl is
	// the default for forms, toolbars and primary actions; HeightControlSmall
	// is for inline actions, pane tabs and the footer. HeightRow is a table
	// row, a list row or a sidebar item.
	HeightControl      = 28
	HeightControlSmall = 22
	HeightSecondary    = 26 // the outlined secondary button
	HeightBadge        = 18 // an h-4.5 status badge
	HeightRow          = 24
	HeightSidebarRow   = 32 // a settings row before a subtitle adds a line
	HeightSearchField  = 32 // the search and scope search field
	// HeightRowAction is an icon action or a ⋯ inside a row, a sidebar
	// heading or a pane header. It is centered, so it leaves RowActionInset
	// above and below in a 24-point row; its corner is RadiusInner, which is
	// the row's RadiusControl less that inset.
	HeightRowAction = 20
	RowActionInset  = (HeightRow - HeightRowAction) / 2
	// HeightChip is a query chip in a search field: the field is HeightRow
	// (24) tall, the chip is centered in it and leaves 3 above and below, and
	// its corners are RadiusInner. The field's sides take SpaceM, as
	// SearchField's do, so the first chip clears the edge.
	HeightChip = 18
	// Insets: the padding a surface gives its content. A component builds its
	// own from these; a screen never pads a control itself.
	ControlPaddingX      = 12 // a default control, left and right
	ControlPaddingXSmall = 8  // a small control, a chip, a row's sides
	PanelPadding         = 12 // a panel's content and a pane header's sides
	TipPaddingX          = 4
	TipPaddingY          = 2
	ToastPaddingX        = 16
	ToastPaddingY        = 8
	ToastWidth           = 360 // a toast is at most this wide
	CardTipWidth         = 320 // a w-80 hover card
	CardTipPaddingX      = 10  // a hover card, left and right
	CardTipPaddingY      = 10  // top; the rows end 8 above the bottom edge
	CardTipPaddingBottom = 8
	// A settings card row's padding: the horizontal inset controls and the
	// card's edge share, and the vertical room that keeps a two-line title
	// and description off the row's top and bottom edges.
	CardPaddingX    = SpaceXL * SpaceGrid // 16
	CardRowPaddingY = SpaceL * SpaceGrid  // 12
	// Corners stay concentric: something nested in a padded container takes
	// the container's radius less the padding, which is RadiusInner. The
	// workspace stops at RadiusPanel; a card and dialog take a step of the
	// looser floating scale. A dropdown menu matches Amp Web: its panel is
	// RadiusControl and its items are RadiusInner.
	RadiusInner   = 4
	RadiusControl = 6
	RadiusPanel   = 8
	RadiusMenu    = 6
	RadiusCard    = 12
	RadiusDialog  = 16
	// HairlineWidth is the one-point edge every hairline, field edge and
	// separator is drawn with.
	HairlineWidth = 1
	IconInline    = 14
	IconLarge     = 16
	// Widths for the fields and pickers a form lays out. A screen picks one
	// of these instead of writing its own number.
	WidthNarrow    = 120
	WidthPicker    = 140
	WidthField     = 160
	WidthFieldWide = 200
	WidthWide      = 240 // a wide field, a popover or a timing card
	WidthNumber    = 88  // a number field: a port, a status, a count
	DialogSmall    = 480
	DialogMedium   = 640
	DialogLarge    = 840
	// The floating scale. The workspace keeps its 24-point rows and 28/22
	// controls; menus, popovers, dialogs, toasts and Settings use these.
	MenuRowHeight     = 28
	PopoverPadding    = 16
	DialogPadding     = 24
	DialogGap         = 16
	SettingsWidth     = 620
	SettingsPageWidth = 760 // a settings page's readable column, left-aligned
	SettingsRowHeight = 48
	// WindowMargin is the space a modal, a toast or a popover keeps from the
	// window's edge, on each side.
	WindowMargin = 16
	// Settings: the label column is as wide as its widest label, between the
	// default and the most it takes; a hint wraps at SettingsHintWidth.
	SettingsLabelWidth      = 112
	SettingsLabelMax        = 180
	SettingsHintWidth       = 400
	TipMaxWidth             = 450 // a tip wraps at this width
	CardKeyWidth            = 52  // a card's key column
	TabRuleHeight           = 2   // the rule under a selected tab
	DotSize                 = 6   // a status or a legend dot
	HeightInlineKbd         = 19  // a shortcut keycap in a menu or command row
	KbdPaddingX             = 4
	SettingsControlHeight   = 36
	CommandPaletteWidth     = 720
	CommandPaletteInputRow  = 48
	CommandPaletteInputSize = 16
	CommandPaletteRow       = 40
	CommandPaletteRowSize   = 14
	CommandPaletteRowInset  = 4
	// FontUI and FontMono are per platform: font_darwin.go, font_other.go.
)

// Shadow is the kit's floating elevation. Its layers map to --shadow-sm/md/lg
// and the shadow-popover ring; a menu takes a deeper Dark variant. The
// workspace has no shadow.
type shadowLayer struct {
	x, y, blur, spread float32
	light, dark        ui.Color
}

type Shadow struct {
	layers []shadowLayer
}

var (
	ShadowSmall = Shadow{layers: []shadowLayer{
		{0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.1), ui.RGBA(0, 0, 0, 0.1)},
		{0, 1, 2, -1, ui.RGBA(0, 0, 0, 0.1), ui.RGBA(0, 0, 0, 0.1)},
	}}
	ShadowKbd = Shadow{layers: []shadowLayer{
		{0, 0, 0, HairlineWidth, ui.RGBA(0, 0, 0, 0.1), ui.RGBA(255, 255, 255, 0.1)},
		{0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.1), ui.RGBA(0, 0, 0, 0.1)},
		{0, 1, 2, -1, ui.RGBA(0, 0, 0, 0.1), ui.RGBA(0, 0, 0, 0.1)},
	}}
	ShadowPopover = Shadow{layers: []shadowLayer{
		{0, 0, 0, HairlineWidth, ui.RGBA(0, 0, 0, 0.08), ui.RGBA(255, 255, 255, 0.1)},
		{0, 4, 6, -1, ui.RGBA(0, 0, 0, 0.1), ui.RGBA(0, 0, 0, 0.1)},
		{0, 2, 4, -2, ui.RGBA(0, 0, 0, 0.1), ui.RGBA(0, 0, 0, 0.1)},
	}}
	ShadowDialog = Shadow{layers: []shadowLayer{
		{0, 10, 15, -3, ui.RGBA(0, 0, 0, 0.1), ui.RGBA(0, 0, 0, 0.1)},
		{0, 4, 6, -4, ui.RGBA(0, 0, 0, 0.1), ui.RGBA(0, 0, 0, 0.1)},
	}}
	ShadowMenu = Shadow{layers: []shadowLayer{
		{0, 0, 0, HairlineWidth, ui.RGBA(0, 0, 0, 0.08), ui.RGBA(255, 255, 255, 0.1)},
		{0, 4, 6, -1, ui.RGBA(0, 0, 0, 0.1), ui.RGBA(0, 0, 0, 0.8)},
		{0, 2, 4, -2, ui.RGBA(0, 0, 0, 0.1), ui.RGBA(0, 0, 0, 0.6)},
	}}
)

// Apply puts the shadow's layers under e. Dark changes the ring to white and
// deepens the menu's lower layers.
func (s Shadow) Apply(e ui.Element, dark bool) ui.Element {
	for _, layer := range s.layers {
		ink := layer.light
		if dark {
			ink = layer.dark
		}
		e = e.Shadow(layer.x, layer.y, layer.blur, layer.spread, ink)
	}
	return e
}

// TextScale follows the desktop's text size, as the theme's own font size does:
// a control that names one of the sizes above asks for it through here, so the
// four sizes keep their proportions at any scale.
var textScale float32 = 1

// SetTextScale makes the sizes follow the desktop's text size: 1 is usual, 1.5
// half again as large. It is one value for the process, which is what a desktop
// has: every window of the app follows the same setting, and a theme cannot
// carry its own scale because a size is asked for where a control is built,
// without the theme at hand.
func SetTextScale(scale float32) {
	if scale <= 0 {
		scale = 1
	}
	textScale = scale
}

// TextScale is the scale the desktop asks for, 1 at the usual size.
func TextScale() float32 { return textScale }

// Scaled is a size from the scale the desktop asks for.
func Scaled(size float32) float32 {
	return float32(math.Round(float64(size * textScale)))
}
