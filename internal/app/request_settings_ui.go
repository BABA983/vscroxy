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
	ui.Column(c).Grow(1).MinHeight(0).Gap(t.Space(uikit.SpaceM)).Children(func() {
		uikit.Meta(c, locales.MarkdownShownInTheCollection())
		uikit.TextArea(c, &d.description).Label(locales.RequestDescription()).
			Placeholder(locales.WhatThisRequestDoesIts()).
			Font(uikit.FontMono).Radius(uikit.RadiusControl).
			Grow(1).MinHeight(0)
		uikit.Meta(c, locales.SavedWithTheRequestAnd())
	})
}

// requestSettingsEditor edits the request's transport choices. Unset fields
// follow the collection and folder defaults, which each row names under its
// control.
func (a *app) requestSettingsEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	inheritance := a.inheritance(d.composeCollection)
	effective := inheritance.InheritSettings(d.settings)
	defaultFor := func(set func(request.Settings) bool) string {
		if set(d.settings) {
			return ""
		}
		for i := len(inheritance.Folders) - 1; i >= 0; i-- {
			if set(inheritance.Folders[i].Settings) {
				if i == 0 {
					return locales.DefaultCollection()
				}
				return locales.DefaultFolder()
			}
		}
		return locales.DefaultSettings()
	}
	switchRow := func(label string, value bool, set func(bool)) {
		ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			if uikit.Switch(c, &value).Label(label).Changed() {
				set(value)
			}
			word := locales.SwitchOff()
			if value {
				word = locales.SwitchOn()
			}
			uikit.Meta(c, word)
		})
	}
	ui.Scroll(c).Grow(1).FillWidth().Gap(t.Space(uikit.SpaceL)).Children(func() {
		uikit.Label(c, locales.Transport())
		if request.IsWebSocket(d.composeURL) {
			uikit.Hint(c, locales.WebsocketSendsUseTheirOwn())
		}
		ui.Column(c).FillWidth().Gap(t.Space(uikit.SpaceL)).Children(func() {
			uikit.PropertyRow(c, locales.HttpVersion(), locales.HttpVersionAutoHint(), func() {
				choice := httpVersionChoice(effective.HTTPVersion)
				if uikit.Select(c, &choice, httpVersionChoices()).Label(locales.HttpVersion()).Width(uikit.WidthField).Changed() {
					d.settings.HTTPVersion = httpVersionFromChoice(choice)
				}
			}, defaultFor(func(s request.Settings) bool { return s.HTTPVersion != "" }))
			uikit.PropertyRow(c, locales.Tls(), locales.UsesTheSystemRootsAnd(), func() {
				switchRow(locales.VerifyTlsCertificate(), effective.Verifies(), d.setVerify)
			}, defaultFor(func(s request.Settings) bool { return s.VerifyTLS != nil }))
			uikit.PropertyRow(c, locales.Redirects(), locales.N3xxResponsesAreFollowedUnless(), func() {
				switchRow(locales.FollowRedirects(), effective.Follows(), d.setFollow)
			}, defaultFor(func(s request.Settings) bool { return s.FollowRedirects != nil }))
			if effective.Follows() {
				uikit.PropertyRow(c, locales.MaximumRedirects(), locales.HttpSHops(), func() {
					// Bare: MyGo's NumberInput steps with its arrows and buttons, which uikit.NumberField does not draw.
					if ui.NumberInput(c, &d.maxRedirects, 1, 50, 1).Label(locales.MaximumRedirects()).Changed() {
						value := int(d.maxRedirects)
						d.settings.MaxRedirects = &value
					}
				}, defaultFor(func(s request.Settings) bool { return s.MaxRedirects != nil }))
			}
			uikit.PropertyRow(c, locales.EncodeUrlAutomatically(), locales.EncodeUrlAutomaticallyHint(), func() {
				switchRow(locales.EncodeUrlAutomatically(), effective.EncodesURL(), d.setURLEncoding)
			}, defaultFor(func(s request.Settings) bool { return s.DisableURLEncoding != nil }))
			uikit.PropertyRow(c, locales.SendCookiesFromTheJar(), locales.SendCookiesFromTheJarHint(), func() {
				switchRow(locales.SendCookiesFromTheJar(), effective.UsesCookies(), d.setCookies)
			}, defaultFor(func(s request.Settings) bool { return s.DisableCookies != nil }))
			uikit.PropertyRow(c, locales.Timeout(), locales.Seconds0WaitsWithNo(), func() {
				// Bare: MyGo's NumberInput steps with its arrows and buttons, which uikit.NumberField does not draw.
				if ui.NumberInput(c, &d.timeout, 0, 3600, 1).Label(locales.RequestTimeoutInSeconds()).Changed() {
					seconds := int(d.timeout)
					d.settings.TimeoutSeconds = &seconds
				}
			}, defaultFor(func(s request.Settings) bool { return s.TimeoutSeconds != nil }))
			uikit.PropertyRow(c, locales.SocketPath(), locales.SocketPathHint(), func() {
				socket := effective.SocketPath
				if uikit.Input(c, &socket).Label(locales.SocketPath()).Placeholder("/var/run/app.sock").Width(uikit.WidthFieldWide).Changed() {
					d.settings.SocketPath = strings.TrimSpace(socket)
				}
			}, defaultFor(func(s request.Settings) bool { return s.SocketPath != "" }))
		})
		if !effective.Verifies() {
			ui.Row(c).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Radius(uikit.RadiusControl).
				Background(t.Warning.Alpha(0.12)).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, warningIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.Warning)
				uikit.Caption(c, locales.CertificateVerificationIsOffThis()).TextColor(t.Warning).Grow(1)
			})
		}
		if d.settings != (request.Settings{}) {
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, locales.SomeValuesOverrideTheCollection())
				if uikit.SecondaryButton(c, locales.UseCollectionDefaults()).Clicked() {
					d.resetSettings(a)
				}
			})
		}
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
	ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(uikit.SpaceL)).Children(func() {
		uikit.Meta(c, locales.RequestsUnderItInheritThese())
		ui.Form(c, func() {
			ui.Field(c, locales.Timeout(), func() {
				ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
						// Bare: MyGo's NumberInput steps with its arrows and buttons, which uikit.NumberField does not draw.
						if ui.NumberInput(c, &f.timeout, 0, 3600, 1).Label(locales.FolderTimeoutInSeconds()).Changed() {
							seconds := int(f.timeout)
							f.settings.TimeoutSeconds = &seconds
						}
					})
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
					ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
						// Bare: MyGo's NumberInput steps with its arrows and buttons, which uikit.NumberField does not draw.
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
				if uikit.Select(c, &choice, httpVersionChoices()).Label(locales.HttpVersion()).Width(uikit.WidthField).Changed() {
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
			ui.Row(c).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Radius(uikit.RadiusControl).
				Background(t.Warning.Alpha(0.12)).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, warningIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.Warning)
				uikit.Caption(c, locales.CertificateVerificationIsOffFor()).TextColor(t.Warning).Grow(1)
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
