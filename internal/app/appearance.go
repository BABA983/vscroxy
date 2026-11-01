package app

import (
	"cmp"
	"github.com/baba983/vscroxy/internal/uikit"
	"slices"
	"strconv"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// Icons are outline glyphs from one set, always sized by the place that uses
// them, so the whole app shares one drawing weight.
var (
	// Icons come from the Lucide set pinned by onevra, so the whole app draws
	// one stroke family instead of hand-traced paths.
	captureIcon       = lucide.Activity()
	requestIcon       = lucide.SquarePen()
	settingsIcon      = lucide.Settings()
	plusIcon          = lucide.Plus()
	sideLayoutIcon    = lucide.Columns2()
	stackedLayoutIcon = lucide.Rows2()
	lockIcon          = lucide.Lock()
	appIcon           = lucide.AppWindow()
	trafficIcon       = lucide.LayoutList()
	domainIcon        = lucide.Globe()
	pinIcon           = lucide.Pin()
	filterIcon        = lucide.Funnel()
	breakIcon         = lucide.CirclePause()
	clockIcon         = lucide.Clock()
	backIcon          = lucide.ArrowLeft()
	commandIcon       = lucide.Command()
	monitorIcon       = lucide.Monitor()
	layersIcon        = lucide.Layers()
	closeIcon         = lucide.X()
	brandIcon         = lucide.Route()
	sendHistoryIcon   = lucide.RotateCcwClock()
	compareIcon       = lucide.Columns2()
	errorIcon         = lucide.CircleAlert()
	fileInputIcon     = lucide.FileInput()
	copyIcon          = lucide.Copy()
	formatIcon        = lucide.Braces()
	eyeIcon           = lucide.Eye()
	moreIcon          = lucide.Ellipsis()
	chevronIcon       = lucide.ChevronDown()
	statusMoreIcon    = lucide.ChevronRight()
	pauseIcon         = lucide.Pause()
	resumeIcon        = lucide.Play()
	exampleIcon       = lucide.BookmarkPlus()
	toolsIcon         = lucide.Wrench()
	networkIcon       = lucide.Gauge()
	cacheIcon         = lucide.ArchiveX()
	upstreamIcon      = lucide.Network()
	successIcon       = lucide.CircleCheck()
	checkIcon         = lucide.Check()
	warningIcon       = lucide.TriangleAlert()
	saveIcon          = lucide.Save()
)

// settingsModal is Settings without a main window to open its own beside,
// as in headless tests: the same content, in a dialog.
func (a *app) settingsModal(c *ui.Context, t *ui.Theme) {
	// Closing Settings or leaving the Proxy tab makes the next visit read the
	// OS again.
	defer func() { a.systemProxy.shown = a.settingsOpen && a.settingsTab == 1 }()
	if !a.settingsOpen || a.win != nil {
		return
	}
	_, height := c.Size()
	uikit.Modal(c, &a.settingsOpen, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(640, height-80)).Children(func() {
			uikit.DialogTitle(c, "Settings")
			a.settingsContent(c, t, !a.windowBehind)
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Done").Clicked() {
					a.settingsOpen = false
				}
			})
		})
	})
}

// openSettings shows Settings on a category, or the last one for a negative
// tab: in a window of its own beside the main one, focused if already open.
func (a *app) openSettings(tab int) {
	if tab >= 0 {
		a.settingsTab = tab
	}
	switch a.settingsTab {
	case 1:
		a.checkSystemProxy(true)
	case 2:
		a.checkCertificate(false)
	}
	a.settingsOpen = true
	if a.win == nil {
		return
	}
	if a.settingsWin != nil {
		a.settingsWin.Invalidate()
		a.settingsWin.Focus()
		return
	}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:           "Settings",
		Width:           760,
		Height:          560,
		MinWidth:        600,
		MinHeight:       420,
		StateKey:        "settings",
		AutoHideMenuBar: true,
		Content:         ui.View(a.settingsView),
	})
	a.settingsWin, a.settingsFocused = w, true
	w.OnFocus(func() { w.Update(func() { a.settingsFocused = true }) })
	w.OnBlur(func() { w.Update(func() { a.settingsFocused = false }) })
	w.OnClosed(func() {
		a.win.Update(func() {
			if a.settingsWin == w {
				a.settingsWin, a.settingsOpen, a.settingsFocused = nil, false, false
				a.systemProxy.shown = false
			}
		})
	})
}

// leaveSettings makes way for something Settings opens in the main window:
// the dialog closes, while the Settings window stays where it is.
func (a *app) leaveSettings() {
	if a.settingsWin != nil {
		a.win.Focus()
		a.win.Invalidate()
		return
	}
	a.settingsOpen = false
}

// settingsView is the Settings window: the categories and their content,
// on the same state as the main window, which it redraws as it changes.
func (a *app) settingsView(c *ui.Context) {
	dark := a.appearance == "dark" || a.appearance == "" && c.Theme().Dark
	c.SetTheme(uikit.Theme(dark))
	t := c.Theme()
	a.drainCertificate()
	a.drainSystemProxy()
	a.mu.Lock()
	if a.messagePending && a.settingsFocused {
		c.Toast(a.message)
		a.messagePending = false
	}
	a.mu.Unlock()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.settingsContent(c, t, a.settingsFocused)
	})
	uikit.Toasts(c)
	a.systemProxy.shown = a.settingsTab == 1
	if a.win != nil {
		a.win.Invalidate()
	}
}

// settingsContent is Settings itself: categories on the left, the chosen
// one on the right. active draws the chosen category as in a window in
// front. The editors it opens own their values and persistence.
func (a *app) settingsContent(c *ui.Context, t *ui.Theme, active bool) {
	ui.Row(c).Grow(1).FillWidth().MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		choice := strconv.Itoa(a.settingsTab)
		ui.Sidebar(c, &choice, func() {
			for _, item := range []struct {
				id    string
				label string
			}{{"0", "General"}, {"3", "Appearance"}, {"1", "Proxy"}, {"2", "HTTPS"}, {"4", "Capture"}, {"5", "Devices"}} {
				uikit.SidebarItem(c, item.id, nil, item.label, choice == item.id, active).Label("Settings category: " + item.label)
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
		top := float32(0)
		if a.settingsWin != nil {
			top = t.Space(4) // no dialog title above it
		}
		ui.Scroll(c).Grow(1).MinWidth(0).Gap(t.Space(3)).Padding(top, t.Space(4), t.Space(4), t.Space(3)).Children(func() {
			switch a.settingsTab {
			case 0:
				ui.Text(c, "Rules & scripts").FontSize(uikit.TextBody).Bold()
				uikit.Meta(c, "Reload proxy rules, DNS mappings, ignore lists and the capture script from disk.")
				ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
					if uikit.SecondaryButton(c, "Manage rules…").Clicked() {
						a.leaveSettings()
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
							uikit.Input(c, &a.proxyPortInput).Label("Proxy port").Placeholder("Automatic").Width(100)
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
				uikit.Meta(c, next).FontFeatures("tnum")
				ui.Divider(c)
				a.systemProxySettings(c, t)
				ui.Divider(c)
				uikit.Meta(c, "Upstream: "+cmp.Or(a.upstream, "Direct connection")).SingleLine()
				ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
					if uikit.SecondaryButton(c, "Configure upstream…").Clicked() {
						a.leaveSettings()
						a.upstreamAddr, a.upstreamOpen = a.upstream, true
					}
					if uikit.SecondaryButton(c, "Connect a device…").Clicked() {
						a.leaveSettings()
						a.openDevices()
					}
				})
			case 2:
				a.httpsSettings(c, t)
			case 3:
				ui.Form(c, func() {
					ui.Field(c, "Appearance", func() {
						modes := []string{"", "light", "dark"}
						choice := max(slices.Index(modes, a.appearance), 0)
						uikit.Segmented(c, &choice, "System", "Light", "Dark").Label("Appearance")
						a.appearance = modes[choice]
					}).Description("System follows the light or dark appearance of this computer.")
				})
			case 4:
				ui.Text(c, "Capture visibility").FontSize(uikit.TextBody).Bold()
				uikit.Meta(c, "Ignored traffic stays proxied but is not shown in the table.")
				ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
					if uikit.SecondaryButton(c, "Ignored hosts…").Clicked() {
						a.leaveSettings()
						a.ignoreOpen = true
					}
					if uikit.SecondaryButton(c, "Ignored apps…").Clicked() {
						a.leaveSettings()
						a.ignoreAppsOpen = true
					}
				})
			case 5:
				ui.Text(c, "Device and client access").FontSize(uikit.TextBody).Bold()
				uikit.Meta(c, "Device access requires explicit network consent and an allowlist.")
				ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
					if uikit.SecondaryButton(c, "Connect a device…").Clicked() {
						a.leaveSettings()
						a.openDevices()
					}
					if uikit.SecondaryButton(c, "Set up a local client…").Clicked() {
						a.leaveSettings()
						a.openClientSetup()
					}
				})
			}
		})
	})
}
