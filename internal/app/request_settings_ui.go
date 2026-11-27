package app

import (
	"slices"
	"strconv"
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
// control; a request's own value offers Reset for that one field.
func (a *app) requestSettingsEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	inheritance := a.inheritance(d.composeCollection)
	effective := inheritance.InheritSettings(d.settings)
	type settingDefault struct {
		scope      string
		overridden bool
		reset      func()
	}
	defaultFor := func(set func(request.Settings) bool, clear func()) settingDefault {
		out := settingDefault{scope: locales.DefaultSettings(), reset: clear}
		for i := len(inheritance.Folders) - 1; i >= 0; i-- {
			if set(inheritance.Folders[i].Settings) {
				out.scope = locales.DefaultFolder()
				if i == 0 {
					out.scope = locales.DefaultCollection()
				}
				break
			}
		}
		out.overridden = set(d.settings)
		return out
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
	settingRow := func(title, description string, control func(), inherited settingDefault) {
		uikit.SettingRow(c, uikit.SettingRowOptions{
			Title: title, Description: description, Control: control,
			Default: inherited.scope, Overridden: inherited.overridden, OnReset: inherited.reset,
		})
	}
	refreshNumbers := func() {
		next := inheritance.InheritSettings(d.settings)
		d.timeout, d.maxRedirects = next.Timeout().Seconds(), float64(next.RedirectLimit())
		d.timeoutText, d.maxRedirectsText = strconv.Itoa(int(d.timeout)), strconv.Itoa(int(d.maxRedirects))
	}
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Gap(t.Space(uikit.SpaceL)).
		Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).PaddingEnd(uikit.ScrollbarInset(t) + t.Space(uikit.SpaceM)).Children(func() {
		if request.IsWebSocket(d.composeURL) {
			uikit.Hint(c, locales.WebsocketSendsUseTheirOwn())
		}
		settingRow(locales.HttpVersion(), locales.HttpVersionAutoHint(), func() {
			choice := httpVersionChoice(effective.HTTPVersion)
			if uikit.Select(c, &choice, httpVersionChoices()).Label(locales.HttpVersion()).Width(uikit.WidthField).Changed() {
				d.settings.HTTPVersion = httpVersionFromChoice(choice)
			}
		}, defaultFor(func(s request.Settings) bool { return s.HTTPVersion != "" }, func() { d.settings.HTTPVersion = "" }))
		settingRow(locales.TlsVerification(), locales.UsesTheSystemRootsAnd(), func() {
			switchRow(locales.VerifyTlsCertificate(), effective.Verifies(), d.setVerify)
		}, defaultFor(func(s request.Settings) bool { return s.VerifyTLS != nil }, func() { d.settings.VerifyTLS = nil }))
		settingRow(locales.Redirects(), locales.N3xxResponsesAreFollowedUnless(), func() {
			switchRow(locales.FollowRedirects(), effective.Follows(), d.setFollow)
		}, defaultFor(func(s request.Settings) bool { return s.FollowRedirects != nil }, func() { d.settings.FollowRedirects = nil }))
		settingRow(locales.FollowOriginalMethod(), locales.FollowOriginalMethodHint(), func() {
			switchRow(locales.FollowOriginalMethod(), effective.FollowsOriginalMethod(), d.setFollowOriginalMethod)
		}, defaultFor(func(s request.Settings) bool { return s.FollowOriginalMethod != nil }, func() { d.settings.FollowOriginalMethod = nil }))
		settingRow(locales.KeepAuthorizationOnRedirect(), locales.KeepAuthorizationOnRedirectHint(), func() {
			switchRow(locales.KeepAuthorizationOnRedirect(), effective.KeepsAuthorization(), d.setKeepAuthorization)
		}, defaultFor(func(s request.Settings) bool { return s.KeepAuthorization != nil }, func() { d.settings.KeepAuthorization = nil }))
		settingRow(locales.RemoveRefererOnRedirect(), locales.RemoveRefererOnRedirectHint(), func() {
			switchRow(locales.RemoveRefererOnRedirect(), effective.RemovesReferer(), d.setRemoveReferer)
		}, defaultFor(func(s request.Settings) bool { return s.RemoveReferer != nil }, func() { d.settings.RemoveReferer = nil }))
		strict := effective.StrictHTTP != nil && *effective.StrictHTTP
		settingRow(locales.EnableStrictHttpParser(), locales.EnableStrictHttpParserHint(), func() {
			switchRow(locales.EnableStrictHttpParser(), strict, d.setStrictHTTP)
		}, defaultFor(func(s request.Settings) bool { return s.StrictHTTP != nil }, func() { d.settings.StrictHTTP = nil }))
		settingRow(locales.EncodeUrlAutomatically(), locales.EncodeUrlAutomaticallyHint(), func() {
			switchRow(locales.EncodeUrlAutomatically(), effective.EncodesURL(), d.setURLEncoding)
		}, defaultFor(func(s request.Settings) bool { return s.DisableURLEncoding != nil }, func() { d.settings.DisableURLEncoding = nil }))
		settingRow(locales.SendCookiesFromTheJar(), locales.SendCookiesFromTheJarHint(), func() {
			switchRow(locales.SendCookiesFromTheJar(), effective.UsesCookies(), d.setCookies)
		}, defaultFor(func(s request.Settings) bool { return s.DisableCookies != nil }, func() { d.settings.DisableCookies = nil }))
		if effective.Follows() {
			settingRow(locales.MaximumRedirects(), locales.HttpSHops(), func() {
				numberText(&d.maxRedirectsText, d.maxRedirects)
				uikit.NumberField(c, &d.maxRedirectsText, &d.maxRedirects, 1, 50, 1, locales.Hops(), locales.MaximumRedirects(), "", func() {
					value := int(d.maxRedirects)
					d.settings.MaxRedirects = &value
				})
			}, defaultFor(func(s request.Settings) bool { return s.MaxRedirects != nil }, func() {
				d.settings.MaxRedirects = nil
				refreshNumbers()
			}))
		}
		settingRow(locales.TlsProtocols(), locales.TlsProtocolsHint(), func() {
			uikit.MenuSelect(c, tlsProtocolsLabel(effective), tlsProtocolsMenu(&d.settings, effective)).Label(locales.ChooseTlsProtocols())
		}, defaultFor(func(s request.Settings) bool { return s.DisabledTLSVersions != nil }, func() { d.settings.DisabledTLSVersions = nil }))
		settingRow(locales.CipherSuites(), locales.CipherSuitesHint(), func() {
			uikit.MenuSelect(c, cipherSuitesLabel(effective), cipherSuitesMenu(&d.settings, effective)).Label(locales.ChooseCipherSuites())
		}, defaultFor(func(s request.Settings) bool { return s.CipherSuites != nil }, func() { d.settings.CipherSuites = nil }))
		settingRow(locales.Timeout(), locales.Seconds0WaitsWithNo(), func() {
			numberText(&d.timeoutText, d.timeout)
			uikit.NumberField(c, &d.timeoutText, &d.timeout, 0, 3600, 1, "s", locales.RequestTimeoutInSeconds(), "", func() {
				seconds := int(d.timeout)
				d.settings.TimeoutSeconds = &seconds
			})
		}, defaultFor(func(s request.Settings) bool { return s.TimeoutSeconds != nil }, func() {
			d.settings.TimeoutSeconds = nil
			refreshNumbers()
		}))
		settingRow(locales.SocketPath(), locales.SocketPathHint(), func() {
			socket := effective.SocketPath
			if uikit.Input(c, &socket).Label(locales.SocketPath()).Placeholder("/var/run/app.sock").Width(uikit.WidthField).Changed() {
				d.settings.SocketPath = strings.TrimSpace(socket)
			}
		}, defaultFor(func(s request.Settings) bool { return s.SocketPath != "" }, func() { d.settings.SocketPath = "" }))
		if !effective.Verifies() {
			ui.Row(c).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Radius(uikit.RadiusControl).
				Background(t.Warning.Alpha(0.12)).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, warningIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.Warning)
				uikit.Caption(c, locales.CertificateVerificationIsOffThis()).TextColor(t.Warning).Grow(1)
			})
		}
		if !d.settings.IsZero() {
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				if uikit.LinkButton(c, locales.UseCollectionDefaults()).Clicked() {
					d.resetSettings(a)
				}
			})
		}
	})
}

// setStrictHTTP pins whether the request's responses take a strict HTTP
// header parse.
func (d *requestDocument) setStrictHTTP(strict bool) {
	d.settings.StrictHTTP = &strict
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

func (d *requestDocument) setFollowOriginalMethod(follow bool) {
	d.settings.FollowOriginalMethod = &follow
}

func (d *requestDocument) setKeepAuthorization(keep bool) {
	d.settings.KeepAuthorization = &keep
}

func (d *requestDocument) setRemoveReferer(remove bool) {
	d.settings.RemoveReferer = &remove
}

// tlsProtocolsLabel names the enabled protocols, or the inherited default.
func tlsProtocolsLabel(s request.Settings) string {
	if s.DisabledTLSVersions == nil {
		return locales.DefaultValue()
	}
	disabled := *s.DisabledTLSVersions
	var enabled []string
	for _, version := range request.SupportedTLSVersions() {
		if !slices.Contains(disabled, version) {
			enabled = append(enabled, locales.Tls()+" "+version)
		}
	}
	if len(enabled) == 0 {
		return locales.NoneSelected()
	}
	return strings.Join(enabled, ", ")
}

// tlsProtocolsMenu toggles the protocol versions a handshake may use. The
// last enabled version cannot be switched off.
func tlsProtocolsMenu(target *request.Settings, effective request.Settings) func(*ui.Menu) {
	return func(m *ui.Menu) {
		enabled := []string{"1.2", "1.3"} // Go's baseline when the setting is inherited.
		if effective.DisabledTLSVersions != nil {
			enabled = enabled[:0]
			for _, version := range request.SupportedTLSVersions() {
				if !slices.Contains(*effective.DisabledTLSVersions, version) {
					enabled = append(enabled, version)
				}
			}
		}
		for _, version := range request.SupportedTLSVersions() {
			checked := slices.Contains(enabled, version)
			item := m.Item(locales.Tls() + " " + version).Checked(checked)
			if !item.Chosen() {
				continue
			}
			nextEnabled := slices.Clone(enabled)
			if checked {
				nextEnabled = slices.DeleteFunc(nextEnabled, func(value string) bool { return value == version })
			} else {
				nextEnabled = append(nextEnabled, version)
			}
			if len(nextEnabled) == 0 {
				return // At least one protocol must remain enabled.
			}
			var nextDisabled []string
			for _, name := range request.SupportedTLSVersions() {
				if !slices.Contains(nextEnabled, name) {
					nextDisabled = append(nextDisabled, name)
				}
			}
			target.DisabledTLSVersions = &nextDisabled
		}
	}
}

// cipherSuitesLabel names the selected TLS 1.0–1.2 suites, or the default.
func cipherSuitesLabel(s request.Settings) string {
	if s.CipherSuites == nil {
		return locales.DefaultValue()
	}
	if len(*s.CipherSuites) == 0 {
		return locales.NoneSelected()
	}
	if len(*s.CipherSuites) == 1 {
		return (*s.CipherSuites)[0]
	}
	return locales.SelectedCount(len(*s.CipherSuites))
}

// cipherSuitesMenu toggles the TLS 1.0–1.2 suites a handshake may use. Clearing
// the last one returns to Go's default selection.
func cipherSuitesMenu(target *request.Settings, effective request.Settings) func(*ui.Menu) {
	return func(m *ui.Menu) {
		selected := []string(nil)
		if effective.CipherSuites != nil {
			selected = *effective.CipherSuites
		}
		if m.Item(locales.DefaultValue()).Checked(effective.CipherSuites == nil).Chosen() {
			target.CipherSuites = nil
			return
		}
		m.Separator()
		for _, name := range request.TLSCipherSuiteNames() {
			checked := slices.Contains(selected, name)
			if !m.Item(name).Checked(checked).Chosen() {
				continue
			}
			next := slices.Clone(selected)
			if checked {
				next = slices.DeleteFunc(next, func(value string) bool { return value == name })
			} else {
				next = append(next, name)
			}
			if len(next) == 0 {
				target.CipherSuites = nil
			} else {
				target.CipherSuites = &next
			}
		}
	}
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
	ancestors := a.ancestorInheritance(f.draft.Path)
	effective := ancestors.InheritSettings(f.settings)
	type settingDefault struct {
		scope      string
		overridden bool
		reset      func()
	}
	defaultFor := func(set func(request.Settings) bool, clear func()) settingDefault {
		out := settingDefault{scope: locales.DefaultSettings(), reset: clear}
		for i := len(ancestors.Folders) - 1; i >= 0; i-- {
			if set(ancestors.Folders[i].Settings) {
				out.scope = locales.DefaultFolder()
				if i == 0 {
					out.scope = locales.DefaultCollection()
				}
				break
			}
		}
		out.overridden = set(f.settings)
		return out
	}
	switchRow := func(label string, value bool, set func(bool)) {
		ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			on := value
			if uikit.Switch(c, &on).Label(label).Changed() {
				set(on)
			}
			word := locales.SwitchOff()
			if value {
				word = locales.SwitchOn()
			}
			uikit.Meta(c, word)
		})
	}
	settingRow := func(title, description string, control func(), inherited settingDefault) {
		uikit.SettingRow(c, uikit.SettingRowOptions{
			Title: title, Description: description, Control: control,
			Default: inherited.scope, Overridden: inherited.overridden, OnReset: inherited.reset,
		})
	}
	refreshNumbers := func() {
		next := ancestors.InheritSettings(f.settings)
		f.timeout, f.maxRedirects = next.Timeout().Seconds(), float64(next.RedirectLimit())
		f.timeoutText, f.maxRedirectsText = strconv.Itoa(int(f.timeout)), strconv.Itoa(int(f.maxRedirects))
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(uikit.SpaceL)).
		Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).PaddingEnd(uikit.ScrollbarInset(t) + t.Space(uikit.SpaceM)).Children(func() {
		uikit.Meta(c, locales.RequestsUnderItInheritThese())
		settingRow(locales.HttpVersion(), locales.HttpVersionAutoHint(), func() {
			choice := httpVersionChoice(effective.HTTPVersion)
			if uikit.Select(c, &choice, httpVersionChoices()).Label(locales.HttpVersion()).Width(uikit.WidthField).Changed() {
				f.settings.HTTPVersion = httpVersionFromChoice(choice)
			}
		}, defaultFor(func(s request.Settings) bool { return s.HTTPVersion != "" }, func() { f.settings.HTTPVersion = "" }))
		settingRow(locales.TlsVerification(), locales.UsesTheSystemRootsAnd(), func() {
			switchRow(locales.VerifyTlsCertificate(), effective.Verifies(), func(verify bool) {
				f.settings.VerifyTLS = &verify
			})
		}, defaultFor(func(s request.Settings) bool { return s.VerifyTLS != nil }, func() { f.settings.VerifyTLS = nil }))
		settingRow(locales.Redirects(), locales.N3xxResponsesAreFollowedUnless(), func() {
			switchRow(locales.FollowRedirects(), effective.Follows(), f.setFollow)
		}, defaultFor(func(s request.Settings) bool { return s.FollowRedirects != nil }, func() { f.settings.FollowRedirects = nil }))
		settingRow(locales.FollowOriginalMethod(), locales.FollowOriginalMethodHint(), func() {
			switchRow(locales.FollowOriginalMethod(), effective.FollowsOriginalMethod(), func(follow bool) {
				f.settings.FollowOriginalMethod = &follow
			})
		}, defaultFor(func(s request.Settings) bool { return s.FollowOriginalMethod != nil }, func() { f.settings.FollowOriginalMethod = nil }))
		settingRow(locales.KeepAuthorizationOnRedirect(), locales.KeepAuthorizationOnRedirectHint(), func() {
			switchRow(locales.KeepAuthorizationOnRedirect(), effective.KeepsAuthorization(), func(keep bool) {
				f.settings.KeepAuthorization = &keep
			})
		}, defaultFor(func(s request.Settings) bool { return s.KeepAuthorization != nil }, func() { f.settings.KeepAuthorization = nil }))
		settingRow(locales.RemoveRefererOnRedirect(), locales.RemoveRefererOnRedirectHint(), func() {
			switchRow(locales.RemoveRefererOnRedirect(), effective.RemovesReferer(), func(remove bool) {
				f.settings.RemoveReferer = &remove
			})
		}, defaultFor(func(s request.Settings) bool { return s.RemoveReferer != nil }, func() { f.settings.RemoveReferer = nil }))
		settingRow(locales.EnableStrictHttpParser(), locales.EnableStrictHttpParserHint(), func() {
			switchRow(locales.EnableStrictHttpParser(), effective.StrictHTTP != nil && *effective.StrictHTTP, func(strict bool) {
				f.settings.StrictHTTP = &strict
			})
		}, defaultFor(func(s request.Settings) bool { return s.StrictHTTP != nil }, func() { f.settings.StrictHTTP = nil }))
		settingRow(locales.EncodeUrlAutomatically(), locales.EncodeUrlAutomaticallyHint(), func() {
			switchRow(locales.EncodeUrlAutomatically(), effective.EncodesURL(), func(encode bool) {
				value := !encode
				f.settings.DisableURLEncoding = &value
			})
		}, defaultFor(func(s request.Settings) bool { return s.DisableURLEncoding != nil }, func() { f.settings.DisableURLEncoding = nil }))
		settingRow(locales.SendCookiesFromTheJar(), locales.SendCookiesFromTheJarHint(), func() {
			switchRow(locales.SendCookiesFromTheJar(), effective.UsesCookies(), func(use bool) {
				value := !use
				f.settings.DisableCookies = &value
			})
		}, defaultFor(func(s request.Settings) bool { return s.DisableCookies != nil }, func() { f.settings.DisableCookies = nil }))
		if effective.Follows() {
			settingRow(locales.MaximumRedirects(), locales.HttpSHops(), func() {
				numberText(&f.maxRedirectsText, f.maxRedirects)
				uikit.NumberField(c, &f.maxRedirectsText, &f.maxRedirects, 1, 50, 1, locales.Hops(), locales.FolderMaximumRedirects(), "", func() {
					value := int(f.maxRedirects)
					f.settings.MaxRedirects = &value
				})
			}, defaultFor(func(s request.Settings) bool { return s.MaxRedirects != nil }, func() {
				f.settings.MaxRedirects = nil
				refreshNumbers()
			}))
		}
		settingRow(locales.TlsProtocols(), locales.TlsProtocolsHint(), func() {
			uikit.MenuSelect(c, tlsProtocolsLabel(effective), tlsProtocolsMenu(&f.settings, effective)).Label(locales.ChooseTlsProtocols())
		}, defaultFor(func(s request.Settings) bool { return s.DisabledTLSVersions != nil }, func() { f.settings.DisabledTLSVersions = nil }))
		settingRow(locales.CipherSuites(), locales.CipherSuitesHint(), func() {
			uikit.MenuSelect(c, cipherSuitesLabel(effective), cipherSuitesMenu(&f.settings, effective)).Label(locales.ChooseCipherSuites())
		}, defaultFor(func(s request.Settings) bool { return s.CipherSuites != nil }, func() { f.settings.CipherSuites = nil }))
		settingRow(locales.Timeout(), locales.Seconds0WaitsWithNo(), func() {
			numberText(&f.timeoutText, f.timeout)
			uikit.NumberField(c, &f.timeoutText, &f.timeout, 0, 3600, 1, "s", locales.FolderTimeoutInSeconds(), "", func() {
				seconds := int(f.timeout)
				f.settings.TimeoutSeconds = &seconds
			})
		}, defaultFor(func(s request.Settings) bool { return s.TimeoutSeconds != nil }, func() {
			f.settings.TimeoutSeconds = nil
			refreshNumbers()
		}))
		if !effective.Verifies() {
			ui.Row(c).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Radius(uikit.RadiusControl).
				Background(t.Warning.Alpha(0.12)).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, warningIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.Warning)
				uikit.Caption(c, locales.CertificateVerificationIsOffFor()).TextColor(t.Warning).Grow(1)
			})
		}
		if !f.settings.IsZero() {
			if uikit.LinkButton(c, locales.ClearRequestDefaults()).Clicked() {
				f.settings = request.Settings{}
				refreshNumbers()
			}
		}
	})
}

// setFollow pins a folder's redirect behavior.
func (f *folderEditor) setFollow(follow bool) {
	f.settings.FollowRedirects = &follow
}
