package app

// The sections of Settings: configuration that is set once and rarely
// changed, as macOS forms, labels in a column beside their controls and what
// a setting does under it. A change applies at once; the few that need the
// app to restart say so, and show Restart to apply while one is pending.
// Tools that are switched often (rules, throttling, breakpoints) live in
// the title bar and Tools, not here.

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// restartPending marks a saved setting the running app does not use yet.
func restartPending(c *ui.Context, pending bool) {
	if pending {
		uikit.Tip(c, uikit.StatusPill(c, locales.RestartToApply(), uikit.Colors(c.Theme()).Warning), locales.SavedTheAppUsesIt())
	}
}

func (a *app) generalSettings(c *ui.Context, t *ui.Theme) {
	uikit.SettingsPage(c, locales.General(), locales.RulesConfigurationFilesAndHelp(), func() {
		uikit.SettingsGroup(c, locales.Language(), func() {
			a.languageSettings(c)
		})
		uikit.SettingsGroup(c, locales.Collections(), func() {
			uikit.SettingsRow(c, locales.ProjectFolder(), func() ui.Element {
				return ui.Column(c).Gap(t.Space(1)).Children(func() {
					ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
						uikit.Input(c, &a.projectInput).Label(locales.ProjectFolder()).Placeholder(locales.Automatic()).Width(260)
						if uikit.SecondaryButton(c, locales.Save()).Clicked() {
							a.setProjectDir(a.projectInput)
						}
						if a.projectDir != "" && uikit.SecondaryButton(c, locales.UseDefaultFolder()).Clicked() {
							a.setProjectDir("")
						}
					})
				})
			}, locales.ProjectFolderHint())
		})
		uikit.SettingsGroup(c, locales.RulesAndConfiguration(), func() {
			uikit.SettingsRow(c, locales.DebuggingRules(), func() ui.Element {
				return ui.Row(c).Children(func() {
					if uikit.SecondaryButton(c, locales.ManageRules()).Clicked() {
						a.leaveSettings()
						a.openRules()
					}
				})
			}, locales.MapRewriteBlockOrDelay())
			uikit.SettingsRow(c, locales.Configuration(), func() ui.Element {
				return ui.Row(c).Children(func() {
					if uikit.SecondaryButton(c, locales.ReloadConfiguration()).Clicked() {
						a.reloadConfig()
					}
				})
			}, locales.ReadRulesScriptsHostProfiles())
			uikit.SettingsRow(c, locales.BackUpAndShare(), func() ui.Element {
				return ui.Row(c).Gap(t.Space(2)).Children(func() {
					if uikit.SecondaryButton(c, locales.Export()).Clicked() {
						a.chooseConfigExport()
					}
					if uikit.SecondaryButton(c, locales.ImportText()).Clicked() {
						a.leaveSettings()
						a.chooseConfigImport()
					}
				})
			}, locales.OneFileWithTheSettings())
		})
		uikit.SettingsGroup(c, locales.Help(), func() {
			uikit.SettingsRow(c, locales.SetupGuide(), func() ui.Element {
				return ui.Row(c).Children(func() {
					if uikit.SecondaryButton(c, locales.ShowSetupGuide()).Disabled(a.guide).Clicked() {
						a.leaveSettings()
						a.openGuide()
					}
				})
			}, locales.CapturingTrustingTheCertificateAnd())
		})
	})
}

func (a *app) appearanceSettings(c *ui.Context, t *ui.Theme) {
	uikit.SettingsPage(c, locales.Appearance(), locales.HowVscroxyLooks(), func() {
		uikit.SettingsGroup(c, locales.Theme(), func() {
			uikit.SettingsRow(c, locales.Appearance(), func() ui.Element {
				return ui.Row(c).Children(func() { // as wide as its segments
					modes := []string{"", "light", "dark"}
					if !a.appearanceChoiceSet {
						a.appearanceChoice, a.appearanceChoiceSet = max(slices.Index(modes, a.appearance), 0), true
					}
					uikit.Segmented(c, &a.appearanceChoice, locales.System(), locales.Light(), locales.Dark()).Label(locales.Appearance())
					if a.appearanceChoice >= 0 && a.appearanceChoice < len(modes) && modes[a.appearanceChoice] != a.appearance {
						a.appearance = modes[a.appearanceChoice]
						a.syncNativeAppearance()
						a.persistSettings()
					}
				})
			}, locales.SystemFollowsThisComputerS())
		})
	})
}

func (a *app) proxySettings(c *ui.Context, t *ui.Theme) {
	uikit.SettingsPage(c, locales.Proxy(), locales.WhereVscroxyListensAndWhere(), func() {
		uikit.SettingsGroup(c, locales.Listener(), func() {
			uikit.SettingsRow(c, locales.Address(), func() ui.Element {
				if a.srv != nil && a.srv.Running() {
					return uikit.Mono(c, a.srv.Addr()).Selectable()
				}
				return ui.Text(c, locales.CaptureIsNotRunning()).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Warning)
			}, "")
			uikit.SettingsRow(c, locales.Port(), func() ui.Element {
				return ui.Column(c).Gap(t.Space(1)).Children(func() {
					ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
						uikit.Input(c, &a.proxyPortInput).Label(locales.ProxyPort()).Placeholder(locales.Automatic()).Width(100).
							Invalid(a.proxyPortProblem != "")
						if uikit.SecondaryButton(c, locales.SaveForNextLaunch()).Clicked() {
							a.saveProxyPort()
						}
						restartPending(c, a.proxyPort != a.launchPort)
					})
					if a.proxyPortProblem != "" {
						ui.Text(c, a.proxyPortProblem).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Danger)
					}
				})
			}, locales.BlankIsAutomaticPrefers7890())
		})
		a.systemProxySettings(c, t)
		a.upstreamSettingsGroup(c, t)
		a.socksSettings(c, t)
		a.reverseProxySettings(c, t)
	})
}

func (a *app) captureSettings(c *ui.Context, t *ui.Theme) {
	// list is a field of a host or app list: its editor and how many it has.
	list := func(label, text, unit, about string, open *bool) ui.Element {
		return uikit.SettingsRow(c, label, func() ui.Element {
			return ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
				if uikit.SecondaryButton(c, label+"…").Clicked() {
					a.leaveSettings()
					*open = true
				}
				n := len(splitHosts(text))
				summary := "None"
				if n > 0 {
					summary = fmt.Sprintf("%d %s", n, plural(n, unit, unit+"s"))
				}
				uikit.Hint(c, summary)
			})
		}, about)
	}
	uikit.SettingsPage(c, locales.Capture(), locales.WhatCaptureChangesOnThe(), func() {
		uikit.SettingsGroup(c, locales.Responses(), func() {
			uikit.SettingsRow(c, locales.Caching(), func() ui.Element {
				on := a.srv != nil && a.srv.NoCaching()
				toggle := ui.Switch(c, &on).Label(locales.NoCaching()).Disabled(a.srv == nil)
				if toggle.Changed() && a.srv != nil {
					a.srv.SetNoCaching(on)
				}
				return toggle
			}, locales.StripCachingHeadersBothWays())
		})
		uikit.SettingsGroup(c, locales.AllowList(), func() {
			empty := len(splitHosts(a.allowText))+len(splitHosts(a.allowAppsText)) == 0
			problem := ""
			if a.allowOn && empty {
				problem = "Nothing is shown until you add a host or an app."
			}
			uikit.SettingsRow(c, locales.AllowList(), func() ui.Element {
				return ui.Column(c).Gap(t.Space(1)).Children(func() {
					if ui.Switch(c, &a.allowOn).Label(locales.OnlyShowAllowedTraffic()).Changed() {
						a.applyAllow()
					}
					if problem != "" {
						ui.Text(c, problem).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Danger)
					}
				})
			}, locales.ShowOnlyTheHostsAnd())
			list("Allowed hosts", a.allowText, "host", "Each host with its subdomains.", &a.allowOpen)
			list("Allowed apps", a.allowAppsText, "app", "As the sidebar's Apps names them.", &a.allowAppsOpen)
		})
		uikit.SettingsGroup(c, locales.Ignored(), func() {
			list("Ignored hosts", a.ignoreText, "host", "Still proxied, but not shown in the table, even when allowed.", &a.ignoreOpen)
			list("Ignored apps", a.ignoreAppsText, "app", "The same by app.", &a.ignoreAppsOpen)
		})
		uikit.SettingsGroup(c, locales.Memory(), func() {
			// limit is a field choosing one of options, labelled by label.
			limit := func(name string, value *int, fallback int, options []int, label func(int) string, hint string) ui.Element {
				return uikit.SettingsRow(c, name, func() ui.Element {
					labels := make([]string, len(options))
					for i, n := range options {
						labels[i] = label(n)
					}
					choice := label(cmp.Or(*value, fallback))
					sel := uikit.Select(c, &choice, labels).Label(name).Width(160)
					if i := slices.Index(labels, choice); i >= 0 && options[i] != cmp.Or(*value, fallback) {
						*value = options[i]
						a.trimFlows()
						a.recount()
						a.applyFilter()
					}
					return sel
				}, hint)
			}
			limit("Keep up to", &a.flowLimit, defaultFlowLimit, flowLimits, func(n int) string {
				return groupThousands(n) + " requests"
			}, "")
			limit("Memory limit", &a.memoryLimit, defaultMemoryLimit, memoryLimits, func(mb int) string {
				if mb >= 1024 {
					return fmt.Sprintf("%d GB", mb/1024)
				}
				return fmt.Sprintf("%d MB", mb)
			}, "")
			limit("Disk limit", &a.diskLimit, defaultDiskLimit, diskLimits, func(gb int) string {
				return fmt.Sprintf("%d GB", gb)
			}, locales.BodiesPast64KbAre())
			uikit.SettingsRow(c, locales.InUse(), func() ui.Element {
				memory, disk := a.retainedTotal()
				return uikit.Body(c, locales.InUseSummary(humanBytes(int(memory)), humanBytes(int(disk)), len(a.flows))).FontFeatures("tnum")
			}, "")
		})
	})
}

// groupThousands writes n with comma thousands separators.
func groupThousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// applyAllow hands the allow list to the proxy, for new flows.
func (a *app) applyAllow() {
	if a.srv != nil {
		a.srv.SetAllow(capture.AllowList{On: a.allowOn, Hosts: splitHosts(a.allowText), Apps: splitHosts(a.allowAppsText)})
	}
}

func (a *app) deviceSettings(c *ui.Context, t *ui.Theme) {
	uikit.SettingsPage(c, locales.Devices(), locales.SendTrafficFromOtherDevices(), func() {
		uikit.SettingsGroup(c, locales.Access(), func() {
			uikit.SettingsRow(c, locales.OtherDevices(), func() ui.Element {
				return ui.Row(c).Children(func() {
					if uikit.SecondaryButton(c, locales.ConnectADevice()).Clicked() {
						a.leaveSettings()
						a.openDevices()
					}
				})
			}, locales.PhonesAndComputersOnYour())
			uikit.SettingsRow(c, locales.ThisComputer(), func() ui.Element {
				return ui.Row(c).Children(func() {
					if uikit.SecondaryButton(c, locales.SetUpALocalClient()).Clicked() {
						a.leaveSettings()
						a.openClientSetup()
					}
				})
			}, locales.PointOneProgramAtThe())
		})
	})
}
