package app

import (
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

func (a *app) checksEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	ui.Scroll(c).Grow(1).Gap(t.Space(3)).Padding(0, t.Space(1)).Children(func() {
		uikit.Meta(c, "RESPONSE CHECKS · all must pass · used by Send and Collection Runner")
		ui.Form(c, func() {
			ui.Field(c, "Expected status", func() {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					uikit.Input(c, &d.checks.Status).Width(90).Label("Expected status").Placeholder("200")
					uikit.Meta(c, "Empty = no status assertion")
				})
			})
			ui.Field(c, "Response headers", func() {
				ui.TextArea(c.Key("check-headers"), &d.checks.Headers).Height(72).Font(uikit.FontMono).FontSize(uikit.TextBody).
					Radius(uikit.RadiusControl).Label("Expected response headers").Placeholder("Content-Type: application/json")
				uikit.Meta(c, "Response headers · exact value, any repeated value may match")
			})
			ui.Field(c, "JSON scalar fields", func() {
				ui.TextArea(c.Key("check-json"), &d.checks.JSON).Height(96).Font(uikit.FontMono).FontSize(uikit.TextBody).
					Radius(uikit.RadiusControl).Label("JSON response checks").Placeholder("/data/id = 42\n/items/0/name = \"example\"\n/ok = true")
				uikit.Meta(c, "JSON scalar fields · RFC 6901 pointer = JSON value · one per line")
			})
		})
		if _, _, _, err := d.checks.Parse(); err != nil {
			ui.Text(c, err.Error()).FontSize(uikit.TextMeta).TextColor(t.Danger)
		}
		if names := d.value().Variables(); len(names) > 0 {
			uikit.Meta(c, "Required variables: "+strings.Join(names, ", ")+" · configure in Variables…")
		}
		uikit.Meta(c, "JSON types matter; missing is not null. Checks never run scripts. Rerunning sends real traffic and can change server data.")
	})
}
