package uikit

import (
	"github.com/egoist/mygo/ui"
)

// RowButton is a full-width row in a list of things to pick: no face of its
// own until pressed, as rows in a macOS list do not light up under the
// pointer. Pair it with Selected for the picked row.
func RowButton(c *ui.Context, label string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	b := ui.ButtonBase(c).Label(label).FillWidth().MinHeight(HeightControl).
		Padding(t.Space(SpaceXS), Scaled(ControlPaddingXSmall)).Gap(t.Space(SpaceM)).Radius(RadiusControl).AlignItems(ui.Center).
		FontSize(Scaled(TextBody)).TextColor(t.Text)
	switch {
	case b.IsDisabled():
	case b.Pressed():
		b.Background(p.MutedPressed)
	}
	return b
}

// Read-only key/value rows preserve wrapping and existing value actions.
func KVRow(c *ui.Context, content func()) ui.Element {
	t := c.Theme()
	return ui.Row(c).Gap(t.Space(SpaceL)).Padding(t.Space(SpaceXS), t.Space(SpaceL)).
		MinHeight(HeightRow).AlignItems(ui.Start).FillWidth().Children(content)
}
