// Package uikit is vscroxy's design system: the tokens, themes and
// components every screen is built from. It depends only on MyGo; screens
// name roles and variants here instead of colors and sizes.
package uikit

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// Shared metrics. Container geometry stays with each screen; these describe
// controls, not a new layout for the shell or inspector.
const (
	// Four sizes, two weights (400 and 500). Monospace is only for wire
	// data: paths, header values, numbers and bodies.
	TextMeta  = 11 // counts, timestamps and sizes
	TextData  = 12 // monospace data in rows and fields
	TextBody  = 13 // prose, labels and controls
	TextTitle = 15 // a dialog, a pane or the request an inspector shows
	// Line heights are multiples of the font size. Meta stays compact so a
	// table or bar keeps its rhythm; prose gets more air, which also keeps
	// CJK paragraphs from feeling cramped. Body itself keeps the font's
	// compact leading because it is also the label of controls and cells.
	LineMeta  = 1.45
	LineProse = 1.5
	LineTitle = 1.3
	// Three bar heights, as in Proxyman and Xcode: the title bar, a pane
	// header or filter bar, and the status bar under everything.
	HeightBar    = 40
	HeightTabs   = 32
	HeightStatus = 24 // the activity strip's one row
	// HeightFooter is the bar under everything: the window's own actions,
	// its counts and the tools that are on.
	HeightFooter = 28
	// Two control heights: everything is 24, the URL bar and Send 28. Rows
	// are 24, the control height.
	HeightControl = 24
	HeightLarge   = 28
	// HeightFooterControl is a button inside the 28-point footer, where a
	// control keeps a few points of air above and below it.
	HeightFooterControl = 20
	// Corners stay concentric: something nested in a padded container takes
	// the container's radius less the padding, which is RadiusInner.
	RadiusInner   = 4
	RadiusControl = 6
	RadiusPanel   = 8
	IconInline    = 14
	IconLarge     = 16
	DialogSmall   = 480
	DialogMedium  = 640
	DialogLarge   = 840
	// FontUI and FontMono are per platform: font_darwin.go, font_other.go.
)

// The kit's durations. Screens name these, never milliseconds: something
// leaving is quick, a face under the pointer takes the hover duration, a
// chosen face or a number changing takes the standard one, and a panel
// opening or closing takes the slow one.
const (
	DurationFast     = 80 * time.Millisecond
	DurationHover    = 120 * time.Millisecond
	DurationStandard = 160 * time.Millisecond
	DurationSlow     = 200 * time.Millisecond
)

// MotionSpring is the kit's one spring: no bounce, arriving in about
// DurationSlow. A panel opening or a pane moving uses it, so a size change
// settles instead of stopping dead.
var MotionSpring = ui.Animation{Spring: ui.SpringOptions{Duration: DurationSlow, Bounce: 0}}

// RegionMotion collapses a section away and springs it back, as a sidebar's
// regions do when their heading is clicked.
var RegionMotion = ui.ElementTransition{
	Animation: &MotionSpring,
	Enter:     &ui.Motion{Collapse: true},
	Exit:      &ui.Motion{Collapse: true},
}

// PaneMotion moves a pane to its new size with the same spring, so a pane
// that gains or loses room settles rather than jumping.
var PaneMotion = ui.ElementTransition{Size: true, Animation: &MotionSpring}

// hoverMotion fades a control's face back to rest over DurationHover. The
// face arrives with the pointer at once; only the way back is a fade, so a
// pointer crossing a row of buttons does not lag behind it. The transition
// stays on the element every frame: MyGo mixes colors only where the frame
// before had a transition too, so a face that dropped it would jump back to
// rest in the frame the pointer left.
func hoverMotion(b ui.Element, lit bool) ui.Element {
	d := DurationHover
	if lit {
		d = -1 // No time at all: the face under the pointer is its own.
	}
	return b.Transition(ui.ElementTransition{Colors: true, Duration: d})
}

// popoverMotion is how a menu, a popover or a panel arrives and leaves: it
// fades in rising 4 points, and leaves the same way a little quicker.
func popoverMotion() ui.ElementTransition {
	return ui.ElementTransition{
		Duration:     DurationHover,
		ExitDuration: DurationFast,
		Enter:        &ui.Motion{Y: 4},
		Exit:         &ui.Motion{Y: 4},
	}
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
