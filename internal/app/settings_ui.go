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
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// restartPending marks a saved setting the running app does not use yet.
func restartPending(c *ui.Context, pending bool) {
	if pending {
		uikit.StatusPill(c, "Restart to apply", uikit.Colors(c.Theme()).Warning).Tooltip("Saved; the app uses it after it restarts")
	}
}

func (a *app) generalSettings(c *ui.Context, t *ui.Theme) {
	uikit.SettingsPage(c, "General", "Rules, configuration files and help.", func() {
		uikit.SettingsGroup(c, "Rules and configuration", func() {
			ui.Field(c, "Debugging rules", func() {
				ui.Row(c).Children(func() {
					if uikit.SecondaryButton(c, "Manage rules…").Clicked() {
						a.leaveSettings()
						a.openRules()
					}
				})
			}).Description("Map, rewrite, block or delay matching requests.")
			ui.Field(c, "Configuration", func() {
				ui.Row(c).Children(func() {
					if uikit.SecondaryButton(c, "Reload configuration").Clicked() {
						a.reloadConfig()
					}
				})
			}).Description("Read rules, scripts, Host profiles and ignore lists from disk again.")
			ui.Field(c, "Back up and share", func() {
				ui.Row(c).Gap(t.Space(2)).Children(func() {
					if uikit.SecondaryButton(c, "Export…").Clicked() {
						a.chooseConfigExport()
					}
					if uikit.SecondaryButton(c, "Import…").Clicked() {
						a.leaveSettings()
						a.chooseConfigImport()
					}
				})
			}).Description("One file with the settings, lists, rules and the files they answer from, rule scripts, Host profiles and breakpoint templates. Never secrets or variables.")
		})
		uikit.SettingsGroup(c, "Help", func() {
			ui.Field(c, "Setup guide", func() {
				ui.Row(c).Children(func() {
					if uikit.SecondaryButton(c, "Show setup guide").Disabled(a.guide).Clicked() {
						a.leaveSettings()
						a.openGuide()
					}
				})
			}).Description("Capturing, trusting the certificate and choosing what to decrypt.")
		})
	})
}

func (a *app) appearanceSettings(c *ui.Context, t *ui.Theme) {
	uikit.SettingsPage(c, "Appearance", "How vscroxy looks.", func() {
		uikit.SettingsGroup(c, "Theme", func() {
			ui.Field(c, "Appearance", func() {
				ui.Row(c).Children(func() { // as wide as its segments
					modes := []string{"", "light", "dark"}
					choice := max(slices.Index(modes, a.appearance), 0)
					uikit.Segmented(c, &choice, "System", "Light", "Dark").Label("Appearance")
					a.appearance = modes[choice]
				})
			}).Description("System follows this computer's light or dark appearance.")
		})
	})
}

func (a *app) proxySettings(c *ui.Context, t *ui.Theme) {
	uikit.SettingsPage(c, "Proxy", "Where vscroxy listens, and where requests go next.", func() {
		uikit.SettingsGroup(c, "Listener", func() {
			ui.Field(c, "Address", func() {
				if a.srv != nil && a.srv.Running() {
					uikit.Mono(c, a.srv.Addr()).Selectable()
				} else {
					ui.Text(c, "Capture is not running").FontSize(uikit.TextBody).TextColor(t.Warning)
				}
			})
			ui.Field(c, "Port", func() {
				ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
					uikit.Input(c, &a.proxyPortInput).Label("Proxy port").Placeholder("Automatic").Width(100)
					if uikit.SecondaryButton(c, "Save for next launch").Clicked() {
						a.saveProxyPort()
					}
					restartPending(c, a.proxyPort != a.launchPort)
				})
			}).Description("Blank is automatic (prefers 7890). A fixed port (1024–65535) never falls back. Applies after a restart.").Error(a.proxyPortProblem)
		})
		a.systemProxySettings(c, t)
		uikit.SettingsGroup(c, "Upstream", func() {
			ui.Field(c, "Upstream proxy", func() {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					in := uikit.Input(c, &a.upstreamAddr).Label("Upstream proxy address").Placeholder("Direct connection").Grow(1).MinWidth(160)
					apply := uikit.SecondaryButton(c, "Apply").Disabled(a.srv == nil || strings.TrimSpace(a.upstreamAddr) == a.upstream)
					if apply.Clicked() || in.Submitted() {
						a.applyUpstream()
					}
				})
			}).Description("Send requests on through another proxy, such as a company one. Empty connects directly.")
		})
	})
}

// applyUpstream points the proxy at the drafted upstream, or directly.
func (a *app) applyUpstream() {
	if a.srv == nil {
		return
	}
	if err := a.srv.SetUpstream(strings.TrimSpace(a.upstreamAddr)); err != nil {
		a.fail("Upstream", err)
		return
	}
	a.upstream = a.srv.Upstream()
	a.upstreamAddr = a.upstream
	a.inform("Upstream: " + cmp.Or(a.upstream, "off"))
}

func (a *app) captureSettings(c *ui.Context, t *ui.Theme) {
	// list is a field of a host or app list: its editor and how many it has.
	list := func(label, text, unit, about string, open *bool) ui.Element {
		return ui.Field(c, label, func() {
			ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
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
		}).Description(about)
	}
	uikit.SettingsPage(c, "Capture", "What capture changes on the way, and what the table shows.", func() {
		uikit.SettingsGroup(c, "Responses", func() {
			ui.Field(c, "Caching", func() {
				on := a.srv != nil && a.srv.NoCaching()
				if ui.Switch(c, &on).Label("No Caching").Disabled(a.srv == nil).Changed() && a.srv != nil {
					a.srv.SetNoCaching(on)
				}
			}).Description("Strip caching headers both ways, so every response comes from the server.")
		})
		uikit.SettingsGroup(c, "Allow list", func() {
			empty := len(splitHosts(a.allowText))+len(splitHosts(a.allowAppsText)) == 0
			problem := ""
			if a.allowOn && empty {
				problem = "Nothing is shown until you add a host or an app."
			}
			ui.Field(c, "Allow list", func() {
				if ui.Switch(c, &a.allowOn).Label("Only show allowed traffic").Changed() {
					a.applyAllow()
				}
			}).Description("Show only the hosts and apps below. Everything else is still proxied, just not recorded.").Error(problem)
			list("Allowed hosts", a.allowText, "host", "Each host with its subdomains.", &a.allowOpen)
			list("Allowed apps", a.allowAppsText, "app", "As the sidebar's Apps names them.", &a.allowAppsOpen)
		})
		uikit.SettingsGroup(c, "Ignored", func() {
			list("Ignored hosts", a.ignoreText, "host", "Still proxied, but not shown in the table, even when allowed.", &a.ignoreOpen)
			list("Ignored apps", a.ignoreAppsText, "app", "The same by app.", &a.ignoreAppsOpen)
		})
		uikit.SettingsGroup(c, "Memory", func() {
			// limit is a field choosing one of options, labelled by label.
			limit := func(name string, value *int, fallback int, options []int, label func(int) string) ui.Element {
				return ui.Field(c, name, func() {
					labels := make([]string, len(options))
					for i, n := range options {
						labels[i] = label(n)
					}
					choice := label(cmp.Or(*value, fallback))
					uikit.Select(c, &choice, labels).Label(name).Width(160)
					if i := slices.Index(labels, choice); i >= 0 && options[i] != cmp.Or(*value, fallback) {
						*value = options[i]
						a.trimFlows()
						a.recount()
						a.applyFilter()
					}
				})
			}
			limit("Keep up to", &a.flowLimit, defaultFlowLimit, flowLimits, func(n int) string {
				return groupThousands(n) + " requests"
			})
			limit("Memory limit", &a.memoryLimit, defaultMemoryLimit, memoryLimits, func(mb int) string {
				if mb >= 1024 {
					return fmt.Sprintf("%d GB", mb/1024)
				}
				return fmt.Sprintf("%d MB", mb)
			})
			limit("Disk limit", &a.diskLimit, defaultDiskLimit, diskLimits, func(gb int) string {
				return fmt.Sprintf("%d GB", gb)
			}).Description("Bodies past 64 KB are kept in temporary files. Past any limit the oldest requests go first, with their files; pinned ones stay.")
			ui.Field(c, "In use", func() {
				memory, disk := a.retainedTotal()
				uikit.Body(c, fmt.Sprintf("%s in memory · %s on disk · %s %s", humanBytes(int(memory)), humanBytes(int(disk)), groupThousands(len(a.flows)), plural(len(a.flows), "request", "requests"))).FontFeatures("tnum")
			})
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
	uikit.SettingsPage(c, "Devices", "Send traffic from other devices and programs through vscroxy.", func() {
		uikit.SettingsGroup(c, "Access", func() {
			ui.Field(c, "Other devices", func() {
				ui.Row(c).Children(func() {
					if uikit.SecondaryButton(c, "Connect a device…").Clicked() {
						a.leaveSettings()
						a.openDevices()
					}
				})
			}).Description("Phones and computers on your network. Needs your consent and an allowlist.")
			ui.Field(c, "This computer", func() {
				ui.Row(c).Children(func() {
					if uikit.SecondaryButton(c, "Set up a local client…").Clicked() {
						a.leaveSettings()
						a.openClientSetup()
					}
				})
			}).Description("Point one program at the proxy: cURL, Node.js, Python or Go.")
		})
	})
}
