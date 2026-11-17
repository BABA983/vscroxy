package app

import (
	"fmt"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func (a *app) checksEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	ui.Scroll(c).Grow(1).Gap(t.Space(uikit.SpaceL)).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
		uikit.Meta(c, locales.ResponseChecksAllMustPass())
		ui.Form(c, func() {
			ui.Field(c, locales.ExpectedStatus(), func() {
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
					uikit.Input(c, &d.checks.Status).Width(90).Label(locales.ExpectedStatus()).Placeholder("200")
					uikit.Meta(c, locales.EmptyNoStatusAssertion())
				})
			})
			ui.Field(c, locales.ResponseHeaders(), func() {
				uikit.TextArea(c.Key("check-headers"), &d.checks.Headers).Height(72).Font(uikit.FontMono).
					Radius(uikit.RadiusControl).Label(locales.ExpectedResponseHeaders()).Placeholder("Content-Type: application/json")
				uikit.Meta(c, locales.ResponseHeadersExactValueAny())
			})
			ui.Field(c, locales.JsonScalarFields(), func() {
				uikit.TextArea(c.Key("check-json"), &d.checks.JSON).Height(96).Font(uikit.FontMono).
					Radius(uikit.RadiusControl).Label(locales.JsonResponseChecks()).Placeholder("/data/id = 42\n/items/0/name = \"example\"\n/ok = true")
				uikit.Meta(c, locales.JsonScalarFieldsRfc6901())
			})
		})
		if _, _, _, err := d.checks.Parse(); err != nil {
			uikit.ErrorText(c, err.Error())
		}
		if snapshot := d.checks.Snapshot; snapshot != nil {
			ui.Divider(c)
			uikit.Label(c, locales.ResponseSnapshot())
			preview := snapshot.Body
			if len(preview) > 2000 {
				preview = preview[:2000] + "\n…"
			}
			uikit.Mono(c, preview).Selectable()
			uikit.Hint(c, locales.SnapshotHint())
			if len(snapshot.Vary) > 0 {
				uikit.Label(c, locales.SnapshotVaryCount(len(snapshot.Vary)))
				mode := 0
				if snapshot.VaryMode == request.SnapshotVaryType {
					mode = 1
				}
				if uikit.Segmented(c, &mode, locales.SnapshotIgnoreValues(), locales.SnapshotTypesOnly()).Changed() {
					snapshot.VaryMode = request.SnapshotVaryIgnore
					if mode == 1 {
						snapshot.VaryMode = request.SnapshotVaryType
					}
				}
				for i, path := range snapshot.Vary {
					if i >= 40 {
						uikit.Meta(c, fmt.Sprintf("+%d", len(snapshot.Vary)-40))
						break
					}
					uikit.Mono(c, path).Selectable()
				}
			}
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				if uikit.SecondaryButton(c, locales.LearnSnapshotVary()).Clicked() {
					a.learnSnapshotVary(d)
				}
				if uikit.SecondaryButton(c, locales.RemoveSnapshot()).Clicked() {
					d.checks.Snapshot = nil
				}
			})
		}
		if names := d.value().Variables(); len(names) > 0 {
			uikit.Meta(c, locales.RequiredVariables(strings.Join(names, ", ")))
		}
		uikit.Meta(c, locales.JsonTypesMatterMissingIs())
	})
}
