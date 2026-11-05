package app

import (
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// transferSettings is what a send of d uses, including Checks' rule to inspect
// the original response instead of following it.
func (a *app) transferSettings(d *requestDocument) request.Settings {
	effective := a.inheritance(d.composeCollection).InheritSettings(d.settings)
	if d.checks != (request.Checks{}) {
		follow := false
		effective.FollowRedirects = &follow
	}
	return effective
}

// requestDocsEditor edits the request's Markdown description, which the
// collection's documentation view renders.
func (a *app) requestDocsEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	ui.Column(c).Grow(1).MinHeight(0).Gap(t.Space(2)).Children(func() {
		uikit.Meta(c, "MARKDOWN · shown in the collection’s documentation view")
		ui.TextArea(c, &d.description).Label("Request description").
			Placeholder("What this request does, its parameters and responses").
			Font(uikit.FontMono).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).
			Grow(1).MinHeight(0)
		uikit.Meta(c, "Saved with the request and round-tripped through Postman.")
	})
}

// requestSettingsEditor edits the request's transport choices. Unset fields
// follow the collection and folder defaults, which the controls show.
func (a *app) requestSettingsEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	effective := a.inheritance(d.composeCollection).InheritSettings(d.settings)
	ui.Scroll(c).Grow(1).Gap(t.Space(3)).Children(func() {
		uikit.Meta(c, "TRANSPORT · applies to Send and the Collection Runner")
		if request.IsWebSocket(d.composeURL) {
			uikit.Meta(c, "WebSocket sends use their own TLS and timeout handling; these apply to HTTP(S) sends.")
		}
		ui.Form(c, func() {
			ui.Field(c, "Timeout", func() {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					if ui.NumberInput(c, &d.timeout, 0, 3600, 1).Label("Request timeout in seconds").Changed() {
						seconds := int(d.timeout)
						d.settings.TimeoutSeconds = &seconds
					}
					uikit.Meta(c, "seconds · 0 waits with no request limit")
				})
			})
			ui.Field(c, "Redirects", func() {
				follow := effective.Follows()
				if ui.Switch(c, &follow).Label("Follow redirects").Changed() {
					d.setFollow(follow)
				}
				uikit.Meta(c, "3xx responses are followed unless Checks need the first one.")
			})
			if effective.Follows() {
				ui.Field(c, "Maximum redirects", func() {
					ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
						if ui.NumberInput(c, &d.maxRedirects, 1, 50, 1).Label("Maximum redirects").Changed() {
							value := int(d.maxRedirects)
							d.settings.MaxRedirects = &value
						}
						uikit.Meta(c, "HTTP(S) hops")
					})
				})
			}
			ui.Field(c, "TLS", func() {
				verify := effective.Verifies()
				if ui.Switch(c, &verify).Label("Verify TLS certificate").Changed() {
					d.setVerify(verify)
				}
				uikit.Meta(c, "Uses the system roots and this app’s CA.")
			})
		})
		if !effective.Verifies() {
			ui.Row(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Radius(uikit.RadiusControl).
				Background(t.Warning.Alpha(0.12)).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, warningIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.Warning)
				ui.Text(c, "Certificate verification is off: this request accepts any certificate, including an impostor’s. Use it only for a server you control.").
					FontSize(uikit.TextMeta).TextColor(t.Warning).Grow(1)
			})
		}
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			if d.settings == (request.Settings{}) {
				uikit.Meta(c, "Using the collection and folder defaults.")
			} else {
				uikit.Meta(c, "Some values override the collection and folder defaults.")
				if uikit.SecondaryButton(c, "Use collection defaults").Clicked() {
					d.resetSettings(a)
				}
			}
		})
	})
}

// setFollow pins the request's redirect behavior; a cURL import keeps its
// method and Content-Type rules when it follows again.
func (d *requestDocument) setFollow(follow bool) {
	d.settings.FollowRedirects = &follow
}

// setVerify pins whether the request verifies the server certificate.
func (d *requestDocument) setVerify(verify bool) {
	value := verify
	d.settings.VerifyTLS = &value
}

// resetSettings returns the request to its inherited defaults.
func (d *requestDocument) resetSettings(a *app) {
	d.settings = request.Settings{}
	effective := a.inheritance(d.composeCollection).InheritSettings(d.settings)
	d.timeout, d.maxRedirects = effective.Timeout().Seconds(), float64(effective.RedirectLimit())
}

// folderSettingsEditor edits the transport defaults requests under a folder
// or collection inherit. Unset fields keep inheriting from above.
func (a *app) folderSettingsEditor(c *ui.Context, f *folderEditor, t *ui.Theme) {
	effective := a.ancestorInheritance(f.draft.Path).InheritSettings(f.settings)
	ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(3)).Children(func() {
		uikit.Meta(c, "REQUESTS UNDER IT INHERIT THESE · unset values keep inheriting from above")
		ui.Form(c, func() {
			ui.Field(c, "Timeout", func() {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					if ui.NumberInput(c, &f.timeout, 0, 3600, 1).Label("Folder timeout in seconds").Changed() {
						seconds := int(f.timeout)
						f.settings.TimeoutSeconds = &seconds
					}
					uikit.Meta(c, "seconds · 0 waits with no request limit")
				})
			})
			ui.Field(c, "Redirects", func() {
				follow := effective.Follows()
				if ui.Switch(c, &follow).Label("Follow redirects").Changed() {
					f.setFollow(follow)
				}
			})
			if effective.Follows() {
				ui.Field(c, "Maximum redirects", func() {
					ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
						if ui.NumberInput(c, &f.maxRedirects, 1, 50, 1).Label("Folder maximum redirects").Changed() {
							value := int(f.maxRedirects)
							f.settings.MaxRedirects = &value
						}
						uikit.Meta(c, "HTTP(S) hops")
					})
				})
			}
			ui.Field(c, "TLS", func() {
				verify := effective.Verifies()
				if ui.Switch(c, &verify).Label("Verify TLS certificate").Changed() {
					value := verify
					f.settings.VerifyTLS = &value
				}
			})
		})
		if !effective.Verifies() {
			ui.Row(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Radius(uikit.RadiusControl).
				Background(t.Warning.Alpha(0.12)).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, warningIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.Warning)
				ui.Text(c, "Certificate verification is off for requests under it: they accept any certificate, including an impostor’s. Use it only for a server you control.").
					FontSize(uikit.TextMeta).TextColor(t.Warning).Grow(1)
			})
		}
		if f.settings != (request.Settings{}) {
			ui.Row(c).Children(func() {
				if uikit.SecondaryButton(c, "Clear request defaults").Clicked() {
					f.settings = request.Settings{}
					effective := a.ancestorInheritance(f.draft.Path).InheritSettings(f.settings)
					f.timeout, f.maxRedirects = effective.Timeout().Seconds(), float64(effective.RedirectLimit())
				}
			})
		}
	})
}

// setFollow pins a folder's redirect behavior.
func (f *folderEditor) setFollow(follow bool) {
	f.settings.FollowRedirects = &follow
}
