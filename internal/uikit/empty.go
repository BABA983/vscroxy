package uikit

import (
	"github.com/egoist/mygo/ui"
)

func EmptyState(c *ui.Context, title, hint string, actions func()) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Padding(t.Space(Space2XL)).Gap(t.Space(SpaceM)).Children(func() {
		ui.Icon(c, emptyIcon).Size(IconLarge, IconLarge).TextColor(t.TextMuted).Margin(0, 0, t.Space(SpaceM), 0)
		Heading(c, title).TextColor(t.Text)
		Hint(c, hint)
		if actions != nil {
			ui.Row(c).Gap(t.Space(SpaceM)).AlignItems(ui.Center).Justify(ui.Center).Children(actions)
		}
	})
}
