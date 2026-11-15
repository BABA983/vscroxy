package app

import (
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
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
		uikit.Meta(c, locales.MarkdownShownInTheCollection())
		uikit.TextArea(c, &d.description).Label(locales.RequestDescription()).
			Placeholder(locales.WhatThisRequestDoesIts()).
			Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)).Radius(uikit.RadiusControl).
			Grow(1).MinHeight(0)
		uikit.Meta(c, locales.SavedWithTheRequestAnd())
	})
}

// requestSettingsEditor edits the request's transport choices. Unset fields
// follow the collection and folder defaults, which the controls show.
func (a *app) requestSettingsEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	effective := a.inheritance(d.composeCollection).InheritSettings(d.settings)
	ui.Scroll(c).Grow(1).Gap(t.Space(3)).Children(func() {
		uikit.Meta(c, locales.TransportAppliesToSendAnd())
		if request.IsWebSocket(d.composeURL) {
			uikit.Meta(c, locales.WebsocketSendsUseTheirOwn())
		}
		ui.Form(c, func() {
			ui.Field(c, locales.Timeout(), func() {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					if ui.NumberInput(c, &d.timeout, 0, 3600, 1).Label(locales.RequestTimeoutInSeconds()).Changed() {
						seconds := int(d.timeout)
						d.settings.TimeoutSeconds = &seconds
					}
					uikit.Meta(c, locales.Seconds0WaitsWithNo())
				})
			})
			ui.Field(c, locales.Redirects(), func() {
				follow := effective.Follows()
				if uikit.Switch(c, &follow).Label(locales.FollowRedirects()).Changed() {
					d.setFollow(follow)
				}
				uikit.Meta(c, locales.N3xxResponsesAreFollowedUnless())
			})
			if effective.Follows() {
				ui.Field(c, locales.MaximumRedirects(), func() {
					ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
						if ui.NumberInput(c, &d.maxRedirects, 1, 50, 1).Label(locales.MaximumRedirects()).Changed() {
							value := int(d.maxRedirects)
							d.settings.MaxRedirects = &value
						}
						uikit.Meta(c, locales.HttpSHops())
					})
				})
			}
			ui.Field(c, locales.Tls(), func() {
				verify := effective.Verifies()
				if uikit.Switch(c, &verify).Label(locales.VerifyTlsCertificate()).Changed() {
					d.setVerify(verify)
				}
				uikit.Meta(c, locales.UsesTheSystemRootsAnd())
			})
			ui.Field(c, locales.HttpVersion(), func() {
				choice := httpVersionChoice(effective.HTTPVersion)
				if uikit.Select(c, &choice, httpVersionChoices()).Label(locales.HttpVersion()).Width(160).Changed() {
					d.settings.HTTPVersion = httpVersionFromChoice(choice)
				}
				uikit.Meta(c, locales.HttpVersionAutoHint())
			})
			ui.Field(c, locales.EncodeUrlAutomatically(), func() {
				encode := effective.EncodesURL()
				if uikit.Switch(c, &encode).Label(locales.EncodeUrlAutomatically()).Changed() {
					d.setURLEncoding(encode)
				}
				uikit.Meta(c, locales.EncodeUrlAutomaticallyHint())
			})
			ui.Field(c, locales.SendCookiesFromTheJar(), func() {
				use := effective.UsesCookies()
				if uikit.Switch(c, &use).Label(locales.SendCookiesFromTheJar()).Changed() {
					d.setCookies(use)
				}
				uikit.Meta(c, locales.SendCookiesFromTheJarHint())
			})
			ui.Field(c, locales.SocketPath(), func() {
				socket := effective.SocketPath
				if uikit.Input(c, &socket).Label(locales.SocketPath()).Placeholder("/var/run/app.sock").Grow(1).Changed() {
					d.settings.SocketPath = strings.TrimSpace(socket)
				}
				uikit.Meta(c, locales.SocketPathHint())
			})
		})
		if !effective.Verifies() {
			ui.Row(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Radius(uikit.RadiusControl).
				Background(t.Warning.Alpha(0.12)).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, warningIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.Warning)
				ui.Text(c, locales.CertificateVerificationIsOffThis()).
					FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Warning).Grow(1)
			})
		}
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			if d.settings == (request.Settings{}) {
				uikit.Meta(c, locales.UsingTheCollectionAndFolder())
			} else {
				uikit.Meta(c, locales.SomeValuesOverrideTheCollection())
				if uikit.SecondaryButton(c, locales.UseCollectionDefaults()).Clicked() {
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

// setURLEncoding pins whether the request escapes its query rows.
func (d *requestDocument) setURLEncoding(encode bool) {
	disable := !encode
	d.settings.DisableURLEncoding = &disable
}

// setCookies pins whether the request reads and writes the cookie jar.
func (d *requestDocument) setCookies(use bool) {
	disable := !use
	d.settings.DisableCookies = &disable
}

// httpVersionChoices are the HTTP version choices, in display order.
func httpVersionChoices() []string {
	return []string{locales.HttpVersionAuto(), "HTTP/1.1", "HTTP/2"}
}

// httpVersionChoice names the stored version.
func httpVersionChoice(version string) string {
	switch version {
	case "1.1":
		return "HTTP/1.1"
	case "2":
		return "HTTP/2"
	}
	return locales.HttpVersionAuto()
}

// httpVersionFromChoice maps a choice back to the stored version.
func httpVersionFromChoice(choice string) string {
	switch choice {
	case "HTTP/1.1":
		return "1.1"
	case "HTTP/2":
		return "2"
	}
	return ""
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
		uikit.Meta(c, locales.RequestsUnderItInheritThese())
		ui.Form(c, func() {
			ui.Field(c, locales.Timeout(), func() {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					if ui.NumberInput(c, &f.timeout, 0, 3600, 1).Label(locales.FolderTimeoutInSeconds()).Changed() {
						seconds := int(f.timeout)
						f.settings.TimeoutSeconds = &seconds
					}
					uikit.Meta(c, locales.Seconds0WaitsWithNo())
				})
			})
			ui.Field(c, locales.Redirects(), func() {
				follow := effective.Follows()
				if uikit.Switch(c, &follow).Label(locales.FollowRedirects()).Changed() {
					f.setFollow(follow)
				}
			})
			if effective.Follows() {
				ui.Field(c, locales.MaximumRedirects(), func() {
					ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
						if ui.NumberInput(c, &f.maxRedirects, 1, 50, 1).Label(locales.FolderMaximumRedirects()).Changed() {
							value := int(f.maxRedirects)
							f.settings.MaxRedirects = &value
						}
						uikit.Meta(c, locales.HttpSHops())
					})
				})
			}
			ui.Field(c, locales.Tls(), func() {
				verify := effective.Verifies()
				if uikit.Switch(c, &verify).Label(locales.VerifyTlsCertificate()).Changed() {
					value := verify
					f.settings.VerifyTLS = &value
				}
			})
			ui.Field(c, locales.HttpVersion(), func() {
				choice := httpVersionChoice(effective.HTTPVersion)
				if uikit.Select(c, &choice, httpVersionChoices()).Label(locales.HttpVersion()).Width(160).Changed() {
					f.settings.HTTPVersion = httpVersionFromChoice(choice)
				}
				uikit.Meta(c, locales.HttpVersionAutoHint())
			})
			ui.Field(c, locales.EncodeUrlAutomatically(), func() {
				encode := effective.EncodesURL()
				if uikit.Switch(c, &encode).Label(locales.EncodeUrlAutomatically()).Changed() {
					value := !encode
					f.settings.DisableURLEncoding = &value
				}
				uikit.Meta(c, locales.EncodeUrlAutomaticallyHint())
			})
			ui.Field(c, locales.SendCookiesFromTheJar(), func() {
				use := effective.UsesCookies()
				if uikit.Switch(c, &use).Label(locales.SendCookiesFromTheJar()).Changed() {
					value := !use
					f.settings.DisableCookies = &value
				}
				uikit.Meta(c, locales.SendCookiesFromTheJarHint())
			})
		})
		if !effective.Verifies() {
			ui.Row(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Radius(uikit.RadiusControl).
				Background(t.Warning.Alpha(0.12)).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, warningIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.Warning)
				ui.Text(c, locales.CertificateVerificationIsOffFor()).
					FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Warning).Grow(1)
			})
		}
		if f.settings != (request.Settings{}) {
			ui.Row(c).Children(func() {
				if uikit.SecondaryButton(c, locales.ClearRequestDefaults()).Clicked() {
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
