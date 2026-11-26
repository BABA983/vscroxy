package uikit

import (
	"github.com/egoist/mygo/ui"
)

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
