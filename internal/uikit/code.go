package uikit

import "github.com/egoist/mygo/ui"

// CodeBlock is a copyable snippet on the field surface: a hairline edge,
// control corners and inner padding, with an optional action such as Copy at
// the top-right of the code's own row. The code stays selectable and wraps
// beside the action.
func CodeBlock(c *ui.Context, code string, action func()) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	block := ui.Column(c).FillWidth().Clip().Radius(RadiusControl).Background(p.Input).
		Border(HairlineWidth, p.Border).Padding(t.Space(SpaceL))
	block.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(t.Space(SpaceM)).Children(func() {
			Code(c, code).Selectable().FillWidth().MinWidth(0)
			if action != nil {
				ui.Row(c).Shrink(0).Children(action)
			}
		})
	})
	return block
}
