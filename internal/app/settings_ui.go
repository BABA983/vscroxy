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
	"github.com/baba983/vscroxy/internal/version"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// settingsCardRow keeps the Settings pages' label, control, hint order while
// the kit's row takes the title, description and control. It is used across
// all seven pages, so a page reads the same as the card it is built from.
func settingsCardRow(c *ui.Context, label string, control func() ui.Element, hint string, tip ...string) ui.Element {
	return uikit.SettingsCardRow(c, label, hint, func() { control() }, tip...)
}

// settingsCardBlock is settingsCardRow for content that needs the card's full
// width: a multiline field, a list of editors or certificate details.
func settingsCardBlock(c *ui.Context, label string, control func() ui.Element, hint string, tip ...string) ui.Element {
	return uikit.SettingsCardBlock(c, label, hint, func() { control() }, tip...)
}

// restartPending marks a saved setting the running app does not use yet.
func restartPending(c *ui.Context, pending bool) {
	if pending {
		uikit.Tip(c, uikit.StatusPill(c, locales.RestartToApply(), uikit.Colors(c.Theme()).Warning), locales.SavedTheAppUsesIt())
	}
}

func (a *app) generalSettings(c *ui.Context, t *ui.Theme) {
	uikit.SettingsCardPage(c, locales.General(), locales.RulesConfigurationFilesAndHelp(), func() {
		uikit.SettingsCard(c, locales.Language(), func() {
			a.languageSettings(c)
		})
		uikit.SettingsCard(c, locales.Notifications(), func() {
			settingsCardRow(c, locales.AttentionSignals(), func() ui.Element {
				modes := []string{attentionAuto, attentionOn, attentionOff}
				selected := max(slices.Index(modes, a.attentionMode()), 0)
				return ui.Row(c).Children(func() { // as wide as its segments
					uikit.Segmented(c, &selected, locales.Auto(), locales.AttentionSignalsOn(), locales.Off()).Label(locales.AttentionSignals())
					if modes[selected] != a.attentionMode() {
						a.setAttentionMode(modes[selected])
					}
				})
			}, locales.AttentionSignalsHint())
		})
		uikit.SettingsCard(c, locales.Collections(), func() {
			settingsCardBlock(c, locales.ProjectFolder(), func() ui.Element {
				return ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					ui.Row(c).Gap(t.Space(uikit.SpaceM)).Wrap().AlignItems(ui.Center).Children(func() {
						uikit.Input(c, &a.projectInput).Label(locales.ProjectFolder()).Placeholder(locales.Automatic()).MinWidth(260).Grow(1)
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
		uikit.SettingsCard(c, locales.RulesAndConfiguration(), func() {
			settingsCardRow(c, locales.DebuggingRules(), func() ui.Element {
				return ui.Row(c).Children(func() {
					if uikit.SecondaryButton(c, locales.ManageRules()).Clicked() {
						a.leaveSettings()
						a.openRules()
					}
				})
			}, locales.MapRewriteBlockOrDelay())
			settingsCardRow(c, locales.Configuration(), func() ui.Element {
				return ui.Row(c).Children(func() {
					if uikit.SecondaryButton(c, locales.ReloadConfiguration()).Clicked() {
						a.reloadConfig()
					}
				})
			}, locales.ReadRulesScriptsHostProfiles())
			settingsCardRow(c, locales.BackUpAndShare(), func() ui.Element {
				return ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
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
		uikit.SettingsCard(c, locales.ExternalLinksSettings(), func() {
			settingsCardBlock(c, locales.TrustedLinkHosts(), func() ui.Element {
				hosts := slices.Clone(a.trustedLinkHosts)
				slices.Sort(hosts)
				return ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					if len(hosts) == 0 {
						uikit.Hint(c, locales.NoTrustedLinkHosts())
					}
					for _, host := range hosts {
						uikit.Mono(c, host).Selectable()
					}
					if uikit.SecondaryButton(c, locales.ClearTrustedLinkHosts()).Disabled(len(hosts) == 0).Clicked() {
						a.trustedLinkHosts = nil
						a.persistSettings()
						a.done(locales.TrustedLinkHostsCleared())
					}
				})
			}, locales.TrustedLinkHostsHint())
		})
		uikit.SettingsCard(c, locales.LocalAutomation(), func() {
			settingsCardRow(c, locales.EnableLocalAutomation(), func() ui.Element {
				on := a.controlRunning()
				toggle := uikit.SettingsSwitch(c, &on, locales.EnableLocalAutomation())
				if toggle.Changed() {
					a.setLocalControl(on)
				}
				return toggle
			}, locales.LocalAutomationAbout())
			if a.controlRunning() {
				settingsCardRow(c, locales.DiscoveryFile(), func() ui.Element {
					return uikit.Mono(c, shortenHome(a.controlPath)).Selectable()
				}, "")
			}
		})
		uikit.SettingsCard(c, locales.Help(), func() {
			settingsCardRow(c, locales.SetupGuide(), func() ui.Element {
				return ui.Row(c).Children(func() {
					if uikit.SecondaryButton(c, locales.ShowSetupGuide()).Disabled(a.guide).Clicked() {
						a.leaveSettings()
						a.openGuide()
					}
				})
			}, locales.CapturingTrustingTheCertificateAnd())
		})
		uikit.SettingsCard(c, locales.Updates(), func() {
			settingsCardRow(c, locales.CheckForUpdates(), func() ui.Element {
				on := a.updateCheck
				toggle := uikit.SettingsSwitch(c, &on, locales.CheckForUpdates())
				if toggle.Changed() {
					a.updateCheck = on
					a.persistSettings()
				}
				return toggle
			}, locales.CheckForUpdatesHint())
		})
		uikit.SettingsCard(c, locales.AboutVscroxy(), func() {
			settingsCardRow(c, locales.Version(), func() ui.Element {
				return ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
					uikit.Meta(c, version.String())
					if uikit.SecondaryButton(c, locales.AboutVscroxy()).Clicked() {
						a.leaveSettings()
						a.openAbout()
					}
				})
			}, locales.AboutLicenseLine())
		})
	})
}

func (a *app) appearanceSettings(c *ui.Context, t *ui.Theme) {
	uikit.SettingsCardPage(c, locales.Appearance(), locales.HowVscroxyLooks(), func() {
		uikit.SettingsCard(c, locales.Theme(), func() {
			settingsCardRow(c, locales.Appearance(), func() ui.Element {
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
	uikit.SettingsCardPage(c, locales.Proxy(), locales.WhereVscroxyListensAndWhere(), func() {
		uikit.SettingsCard(c, locales.Listener(), func() {
			settingsCardRow(c, locales.Address(), func() ui.Element {
				if a.srv != nil && a.srv.Running() {
					return uikit.Mono(c, a.srv.Addr()).Selectable()
				}
				return uikit.Body(c, locales.CaptureIsNotRunning()).TextColor(t.Warning)
			}, "")
			settingsCardBlock(c, locales.Port(), func() ui.Element {
				return ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					ui.Row(c).Gap(t.Space(uikit.SpaceM)).Wrap().AlignItems(ui.Center).Children(func() {
						uikit.Input(c, &a.proxyPortInput).Label(locales.ProxyPort()).Placeholder(locales.Automatic()).Width(100).
							Invalid(a.proxyPortProblem != "")
						if uikit.SecondaryButton(c, locales.SaveForNextLaunch()).Clicked() {
							a.saveProxyPort()
						}
						restartPending(c, a.proxyPort != a.launchPort)
					})
					if a.proxyPortProblem != "" {
						uikit.ErrorText(c, a.proxyPortProblem)
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
	list := func(label, text string, count func(int) string, about string, open *bool) ui.Element {
		return settingsCardRow(c, label, func() ui.Element {
			return ui.Row(c).Gap(t.Space(uikit.SpaceL)).AlignItems(ui.Center).Children(func() {
				if uikit.SecondaryButton(c, label+"…").Clicked() {
					a.leaveSettings()
					*open = true
				}
				n := len(splitHosts(text))
				summary := locales.None()
				if n > 0 {
					summary = count(n)
				}
				uikit.Hint(c, summary)
			})
		}, about)
	}
	uikit.SettingsCardPage(c, locales.Capture(), locales.WhatCaptureChangesOnThe(), func() {
		uikit.SettingsCard(c, locales.Responses(), func() {
			settingsCardRow(c, locales.Caching(), func() ui.Element {
				on := a.srv != nil && a.srv.NoCaching()
				toggle := uikit.SettingsSwitch(c, &on, locales.NoCaching()).Disabled(a.srv == nil)
				if toggle.Changed() && a.srv != nil {
					a.srv.SetNoCaching(on)
				}
				return toggle
			}, locales.StripCachingHeadersBothWays())
		})
		uikit.SettingsCard(c, locales.AllowList(), func() {
			empty := len(splitHosts(a.allowText))+len(splitHosts(a.allowAppsText)) == 0
			problem := ""
			if a.allowOn && empty {
				problem = locales.NothingShownUntilAdd()
			}
			settingsCardRow(c, locales.AllowList(), func() ui.Element {
				toggle := uikit.SettingsSwitch(c, &a.allowOn, locales.OnlyShowAllowedTraffic())
				if toggle.Changed() {
					a.applyAllow()
				}
				return toggle
			}, locales.ShowOnlyTheHostsAnd())
			if problem != "" {
				settingsCardBlock(c, "", func() ui.Element {
					return uikit.ErrorText(c, problem)
				}, "")
			}
			list(locales.AllowedHosts(), a.allowText, locales.HostsCount, "Each host with its subdomains.", &a.allowOpen)
			list(locales.AllowedApps(), a.allowAppsText, locales.AppsCount, "As the sidebar's Apps names them.", &a.allowAppsOpen)
		})
		uikit.SettingsCard(c, locales.Ignored(), func() {
			list(locales.IgnoredHosts2(), a.ignoreText, locales.HostsCount, "Still proxied, but not shown in the table, even when allowed.", &a.ignoreOpen)
			list(locales.IgnoredApps2(), a.ignoreAppsText, locales.AppsCount, "The same by app.", &a.ignoreAppsOpen)
		})
		uikit.SettingsCard(c, locales.Memory(), func() {
			// limit is a field choosing one of options, labelled by label.
			limit := func(name string, value *int, fallback int, options []int, label func(int) string, hint string) ui.Element {
				return settingsCardRow(c, name, func() ui.Element {
					labels := make([]string, len(options))
					for i, n := range options {
						labels[i] = label(n)
					}
					choice := label(cmp.Or(*value, fallback))
					sel := uikit.Select(c, &choice, labels).Label(name).Width(uikit.WidthField)
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
			limit(locales.HistoryEntries(), &a.historyLimit, defaultExperimentRecords, []int{25, 50, 100, 250, 500, 1000}, func(n int) string {
				return groupThousands(n) + " " + locales.HistoryRecordsUnit()
			}, locales.HistoryEntriesHelp())
			limit(locales.HistorySize(), &a.historySizeMB, defaultExperimentSizeMB, []int{8, 16, 32, 64, 128, 256}, func(mb int) string {
				if mb >= 1024 {
					return fmt.Sprintf("%d GB", mb/1024)
				}
				return fmt.Sprintf("%d MB", mb)
			}, locales.HistorySizeHelp())
			a.applyHistoryLimits()
			settingsCardRow(c, locales.InUse(), func() ui.Element {
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
	uikit.SettingsCardPage(c, locales.Devices(), locales.SendTrafficFromOtherDevices(), func() {
		uikit.SettingsCard(c, locales.Access(), func() {
			settingsCardRow(c, locales.OtherDevices(), func() ui.Element {
				return ui.Row(c).Children(func() {
					if uikit.SecondaryButton(c, locales.ConnectADevice()).Clicked() {
						a.leaveSettings()
						a.openDevices()
					}
				})
			}, locales.PhonesAndComputersOnYour())
			settingsCardRow(c, locales.ThisComputer(), func() ui.Element {
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
