package app

import (
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func (a *app) checksEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	ui.Scroll(c).Grow(1).Gap(t.Space(3)).Padding(0, t.Space(1)).Children(func() {
		uikit.Meta(c, locales.ResponseChecksAllMustPass())
		ui.Form(c, func() {
			ui.Field(c, locales.ExpectedStatus(), func() {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					uikit.Input(c, &d.checks.Status).Width(90).Label(locales.ExpectedStatus()).Placeholder("200")
					uikit.Meta(c, locales.EmptyNoStatusAssertion())
				})
			})
			ui.Field(c, locales.ResponseHeaders(), func() {
				ui.TextArea(c.Key("check-headers"), &d.checks.Headers).Height(72).Font(uikit.FontMono).FontSize(uikit.TextBody).
					Radius(uikit.RadiusControl).Label(locales.ExpectedResponseHeaders()).Placeholder(locales.ContentTypeApplicationJson())
				uikit.Meta(c, locales.ResponseHeadersExactValueAny())
			})
			ui.Field(c, locales.JsonScalarFields(), func() {
				ui.TextArea(c.Key("check-json"), &d.checks.JSON).Height(96).Font(uikit.FontMono).FontSize(uikit.TextBody).
					Radius(uikit.RadiusControl).Label(locales.JsonResponseChecks()).Placeholder("/data/id = 42\n/items/0/name = \"example\"\n/ok = true")
				uikit.Meta(c, locales.JsonScalarFieldsRfc6901())
			})
		})
		if _, _, _, err := d.checks.Parse(); err != nil {
			ui.Text(c, err.Error()).FontSize(uikit.TextMeta).TextColor(t.Danger)
		}
		if names := d.value().Variables(); len(names) > 0 {
			uikit.Meta(c, "Required variables: "+strings.Join(names, ", ")+" · configure in Variables…")
		}
		uikit.Meta(c, locales.JsonTypesMatterMissingIs())
	})
}
