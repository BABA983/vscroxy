package app

import (
	"github.com/baba983/vscroxy/internal/uikit"
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
	persistIcon       = lucide.ArrowUpToLine()
	resetIcon         = lucide.Undo2()
)

// settingsModal is Settings without a main window to open its own beside,
// as in headless tests: the same content, in a dialog.
func (a *app) settingsModal(c *ui.Context, t *ui.Theme) {
	if a.win != nil {
		return // the Settings window owns its panel's visit state
	}
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
		// Let the panel start the entry check, even when focusing an already
		// open Settings window. Starting here too races with its first build.
		a.systemProxy.shown = false
	case 2:
		a.checkCertificate(false)
	}
	if !a.settingsOpen {
		a.upstreamUI.loaded = false
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
	c.SetMenuPresentation(ui.PopoverMenus)
	t := c.Theme()
	a.drainCertificate()
	a.drainSystemProxy()
	if a.settingsFocused {
		a.showNotices(c)
	}
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
				icon  *ui.SVG
				label string
			}{
				{"0", lucide.SlidersHorizontal(), "General"},
				{"3", lucide.SunMoon(), "Appearance"},
				{"1", upstreamIcon, "Proxy"},
				{"2", lockIcon, "HTTPS"},
				{"4", captureIcon, "Capture"},
				{"5", lucide.MonitorSmartphone(), "Devices"},
				{"6", lucide.KeyRound(), "Certificates"},
			} {
				uikit.SidebarItem(c, item.id, item.icon, item.label, choice == item.id, active).Label("Settings category: " + item.label)
			}
		}).Width(176).Padding(t.Space(2)).Gap(t.Space(0.5)).Background(uikit.Colors(t).Panel).OnChange(func() {
			a.settingsTab, _ = strconv.Atoi(choice)
			if a.settingsTab == 2 {
				a.checkCertificate(false)
			}
		})
		ui.Divider(c)
		top := t.Space(2)
		if a.settingsWin != nil {
			top = t.Space(6) // no dialog title above it
		}
		ui.Scroll(c).Grow(1).MinWidth(0).Padding(top, t.Space(6), t.Space(6), t.Space(6)).Children(func() {
			switch a.settingsTab {
			case 0:
				a.generalSettings(c, t)
			case 1:
				a.proxySettings(c, t)
			case 2:
				a.httpsSettings(c, t)
			case 3:
				a.appearanceSettings(c, t)
			case 4:
				a.captureSettings(c, t)
			case 5:
				a.deviceSettings(c, t)
			case 6:
				a.certificateSettings(c, t)
			}
		})
	})
}
