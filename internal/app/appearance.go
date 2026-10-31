package app

import (
	"cmp"
	"github.com/baba983/vscroxy/internal/uikit"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// Icons are outline glyphs from one set, always sized by the place that uses
// them, so the whole app shares one drawing weight.
var (
	// Icons come from the Lucide set pinned by onevra, so the whole app draws
	// one stroke family instead of hand-traced paths.
	captureIcon     = lucide.Activity()
	requestIcon     = lucide.SquarePen()
	settingsIcon    = lucide.Settings()
	plusIcon        = lucide.Plus()
	lockIcon        = lucide.Lock()
	appIcon         = lucide.AppWindow()
	trafficIcon     = lucide.LayoutList()
	domainIcon      = lucide.Globe()
	pinIcon         = lucide.Pin()
	filterIcon      = lucide.Funnel()
	breakIcon       = lucide.CirclePause()
	clockIcon       = lucide.Clock()
	backIcon        = lucide.ArrowLeft()
	commandIcon     = lucide.Command()
	monitorIcon     = lucide.Monitor()
	layersIcon      = lucide.Layers()
	closeIcon       = lucide.X()
	brandIcon       = lucide.Route()
	sendHistoryIcon = lucide.RotateCcwClock()
	compareIcon     = lucide.Columns2()
	errorIcon       = lucide.CircleAlert()
	fileInputIcon   = lucide.FileInput()
	copyIcon        = lucide.Copy()
	formatIcon      = lucide.Braces()
	eyeIcon         = lucide.Eye()
	moreIcon        = lucide.Ellipsis()
	chevronIcon     = lucide.ChevronDown()
	exampleIcon     = lucide.BookmarkPlus()
	toolsIcon       = lucide.Wrench()
	networkIcon     = lucide.Gauge()
	cacheIcon       = lucide.ArchiveX()
	upstreamIcon    = lucide.Network()
	successIcon     = lucide.CircleCheck()
	warningIcon     = lucide.TriangleAlert()
)

// settingsModal groups infrequent system actions away from capture controls.
// The existing editors still own their values and persistence.
func (a *app) settingsModal(c *ui.Context, t *ui.Theme) {
	// Closing Settings or leaving the Proxy tab makes the next visit read the
	// OS again.
	defer func() { a.systemProxy.shown = a.settingsOpen && a.settingsTab == 1 }()
	if !a.settingsOpen {
		return
	}
	_, height := c.Size()
	uikit.Modal(c, &a.settingsOpen, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(640, height-80)).Children(func() {
			uikit.DialogTitle(c, "Settings")
			ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				choice := strconv.Itoa(a.settingsTab)
				ui.Sidebar(c, &choice, func() {
					for _, item := range []struct {
						id    string
						label string
					}{{"0", "General"}, {"3", "Appearance"}, {"1", "Proxy"}, {"2", "HTTPS"}, {"4", "Capture"}, {"5", "Devices"}} {
						uikit.SidebarItem(c, item.id, nil, item.label, choice == item.id).Label("Settings category: " + item.label)
					}
				}).Width(140).Padding(t.Space(1)).Gap(0).Background(uikit.Colors(t).Panel).OnChange(func() {
					a.settingsTab, _ = strconv.Atoi(choice)
					switch a.settingsTab {
					case 1:
						a.checkSystemProxy(true)
					case 2:
						a.checkCertificate(false)
					}
				})
				ui.Divider(c)
				ui.Scroll(c).Grow(1).MinWidth(0).Gap(t.Space(3)).Padding(0, t.Space(3)).Children(func() {
					switch a.settingsTab {
					case 0:
						ui.Text(c, "Rules & scripts").FontSize(uikit.TextBody).Bold()
						ui.Text(c, "Reload proxy rules, DNS mappings, ignore lists and the capture script from disk.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
						ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
							if uikit.SecondaryButton(c, "Manage rules…").Clicked() {
								a.settingsOpen = false
								a.openRules()
							}
							if uikit.SecondaryButton(c, "Reload configuration").Clicked() {
								a.reloadConfig()
							}
						})
					case 1:
						addr := "Capture is not running"
						if a.srv != nil && a.srv.Running() {
							addr = "Proxy address: " + a.srv.Addr()
						}
						ui.Text(c, addr).FontSize(uikit.TextBody).Bold()
						ui.Form(c, func() {
							ui.Field(c, "Listening port", func() {
								ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
									ui.TextInput(c, &a.proxyPortInput).Label("Proxy port").Placeholder("Automatic").Width(100).Height(uikit.HeightControl).FontSize(uikit.TextBody).Radius(uikit.RadiusControl)
									if uikit.SecondaryButton(c, "Save for next launch").Clicked() {
										a.saveProxyPort()
									}
								})
							}).Description("Blank = automatic (prefers 7890). Fixed port: 1024–65535, no fallback.").Error(a.proxyPortProblem)
						})
						next := "Next launch: Automatic · restart the app to apply"
						if a.proxyPort != 0 {
							next = "Next launch: 127.0.0.1:" + strconv.Itoa(a.proxyPort) + " · restart the app to apply"
						}
						ui.Text(c, next).FontSize(uikit.TextMeta).FontFeatures("tnum").TextColor(t.TextMuted)
						ui.Divider(c)
						a.systemProxySettings(c, t)
						ui.Divider(c)
						ui.Text(c, "Upstream: "+cmp.Or(a.upstream, "Direct connection")).FontSize(uikit.TextMeta).TextColor(t.TextMuted).SingleLine()
						ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
							if uikit.SecondaryButton(c, "Configure upstream…").Clicked() {
								a.settingsOpen = false
								a.upstreamAddr, a.upstreamOpen = a.upstream, true
							}
							if uikit.SecondaryButton(c, "Connect a device…").Clicked() {
								a.settingsOpen = false
								a.openDevices()
							}
						})
					case 2:
						a.httpsSettings(c, t)
					case 3:
						ui.Form(c, func() {
							ui.Field(c, "Dark appearance", func() { ui.Switch(c, &a.dark).Label("Dark appearance") }).
								Description("Use a low-glare palette for traffic and editors.")
						})
					case 4:
						ui.Text(c, "Capture visibility").FontSize(uikit.TextBody).Bold()
						ui.Text(c, "Ignored traffic stays proxied but is not shown in the table.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
						ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
							if uikit.SecondaryButton(c, "Ignored hosts…").Clicked() {
								a.settingsOpen, a.ignoreOpen = false, true
							}
							if uikit.SecondaryButton(c, "Ignored apps…").Clicked() {
								a.settingsOpen, a.ignoreAppsOpen = false, true
							}
						})
					case 5:
						ui.Text(c, "Device and client access").FontSize(uikit.TextBody).Bold()
						ui.Text(c, "Device access requires explicit network consent and an allowlist.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
						ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
							if uikit.SecondaryButton(c, "Connect a device…").Clicked() {
								a.settingsOpen = false
								a.openDevices()
							}
							if uikit.SecondaryButton(c, "Set up a local client…").Clicked() {
								a.settingsOpen = false
								a.openClientSetup()
							}
						})
					}
				})
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Done").Clicked() {
					a.settingsOpen = false
				}
			})
		})
	})
}
