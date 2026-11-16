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
const (
	// Five sizes, two weights (400 and 500). Monospace is only for wire
	// data: paths, header values, numbers and bodies.
	TextMeta    = 11 // counts, timestamps and sizes
	TextData    = 12 // monospace data in rows and fields
	TextSmall   = 12 // secondary interface text and popover descriptions
	TextBody    = 13 // prose, labels and controls
	TextTitle   = 16 // a dialog, a pane or the request an inspector shows
	TextDisplay = 20 // a first-run title or a command palette's heading
	// Line heights are multiples of the font size, pinned to the desktop
	// scale: 11/15, 12/16, 13/20, 16/24 and 20/28. Prose gets a little
	// more air, which also keeps CJK paragraphs from feeling cramped.
	LineMeta    = 15.0 / 11.0
	LineSmall   = 16.0 / 12.0
	LineBody    = 20.0 / 13.0
	LineProse   = 1.6
	LineTitle   = 24.0 / 16.0
	LineDisplay = 28.0 / 20.0
	// Three bar heights, as in Proxyman and Xcode: the title bar, a pane
	// header or filter bar, and the status bar under everything.
	HeightBar    = 40
	HeightTabs   = 32
	HeightStatus = 24 // the activity strip's one row
	// HeightFooter is the bar under everything: the window's own actions,
	// its counts and the tools that are on.
	HeightFooter = 28
	// Two control heights. ControlHeight is the default for forms, toolbars
	// and primary actions; ControlHeightSmall is for inline actions, pane
	// headers and chips. Both follow the desktop text scale.
	ControlHeight      = 28
	ControlHeightSmall = 22
	// Horizontal control padding: Space(3) for a default control, Space(2)
	// for a small one. The kit multiplies them by the text scale where used.
	ControlPaddingX      = 12
	ControlPaddingXSmall = 8
	// HeightControl is a table or sidebar row; HeightLarge is a bar or a
	// field that needs the default control height. HeightFooterControl is
	// the small control inside the footer.
	HeightControl       = 24
	HeightLarge         = 28
	HeightFooterControl = ControlHeightSmall
	// Corners stay concentric: something nested in a padded container takes
	// the container's radius less the padding, which is RadiusInner. The
	// workspace stops at RadiusPanel; a menu, card and dialog each take one
	// step of the looser floating scale.
	RadiusInner   = 4
	RadiusControl = 6
	RadiusPanel   = 8
	RadiusMenu    = 10
	RadiusCard    = 12
	RadiusDialog  = 16
	IconInline    = 14
	IconLarge     = 16
	DialogSmall   = 480
	DialogMedium  = 640
	DialogLarge   = 840
	// The floating scale. The workspace keeps its 24-point rows and 28/22
	// controls; menus, popovers, dialogs, toasts and Settings use these.
	MenuRowHeight           = 28
	PopoverPadding          = 16
	DialogPadding           = 24
	DialogGap               = 16
	SettingsWidth           = 620
	SettingsRowHeight       = 44
	SettingsControlHeight   = 36
	CommandPaletteWidth     = 720
	CommandPaletteInputRow  = 48
	CommandPaletteInputSize = 16
	CommandPaletteRow       = 40
	CommandPaletteRowSize   = 14
	CommandPaletteRowInset  = 4
	// FontUI and FontMono are per platform: font_darwin.go, font_other.go.
)

// Shadow is one step of the kit's floating elevation: a box shadow's y
// offset, blur and opacity. The workspace has none; a menu, popover and
// dialog each take one step. Dark doubles the opacity so the same lift stays
// visible on the darker board.
type Shadow struct {
	Y, Blur, Opacity float32
}

var (
	ShadowMenu    = Shadow{Y: 8, Blur: 24, Opacity: 0.14}
	ShadowPopover = Shadow{Y: 12, Blur: 32, Opacity: 0.16}
	ShadowDialog  = Shadow{Y: 16, Blur: 48, Opacity: 0.18}
)

// Color is the shadow's ink on a light or dark board.
func (s Shadow) Color(dark bool) ui.Color {
	opacity := s.Opacity
	if dark {
		opacity *= 2
	}
	return ui.RGBA(0, 0, 0, opacity)
}

// Apply puts the shadow under e.
func (s Shadow) Apply(e ui.Element, dark bool) ui.Element {
	return e.Shadow(0, s.Y, s.Blur, 0, s.Color(dark))
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
