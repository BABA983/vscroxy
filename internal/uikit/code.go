package uikit

import (
	"time"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// copyFlashFor is how long a copy button shows its check before returning to
// the copy icon.
const copyFlashFor = 1500 * time.Millisecond

// CopyFlash is a copy button's confirmation state: the moment the last copy
// ran, so the button can show a check for a moment. It belongs to the screen,
// as MyGo's other widget state does.
type CopyFlash struct {
	at time.Time
}

// Copied records a copy now and asks for the frame that takes the check away.
func (f *CopyFlash) Copied(c *ui.Context) {
	f.at = c.Now()
	c.After(copyFlashFor)
	c.Invalidate()
}

// Showing reports whether the check still shows.
func (f *CopyFlash) Showing(c *ui.Context) bool {
	return !f.at.IsZero() && c.Now().Sub(f.at) < copyFlashFor
}

// CodeBlock is a copyable snippet on the field surface: a hairline edge,
// control corners and inner padding, with an optional Copy at the top-right
// of the code's own row. The code stays selectable and wraps beside the
// action. flash, when given, shows a check for a moment after a click.
func CodeBlock(c *ui.Context, code string, action func(), flash *CopyFlash) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	block := ui.Column(c).FillWidth().Clip().Radius(RadiusControl).Background(p.Input).
		Border(HairlineWidth, p.Border).Padding(t.Space(SpaceL))
	block.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(t.Space(SpaceM)).Children(func() {
			Code(c, code).Selectable().FillWidth().MinWidth(0)
			if action != nil {
				copy := ToolbarButton(c, locales.Copy(), locales.Copy(), false).
					Size(HeightControl, HeightControl).Padding(0).Justify(ui.Center)
				copy.Children(func() {
					if flash != nil && flash.Showing(c) {
						ui.Icon(c, lucide.Check()).Size(IconInline, IconInline).TextColor(p.Success)
						return
					}
					ui.Icon(c, lucide.Copy()).Size(IconInline, IconInline)
				})
				if copy.Clicked() {
					action()
					if flash != nil {
						flash.Copied(c)
					}
				}
			}
		})
	})
	return block
}
