package app

import (
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
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
	plusIcon          = lucide.Plus()
	sideLayoutIcon    = lucide.Columns2()
	stackedLayoutIcon = lucide.Rows2()
	detachIcon        = lucide.PanelRightOpen()
	lockIcon          = lucide.Lock()
	settingsIcon      = lucide.Settings()
	trafficIcon       = lucide.LayoutList()
	overviewIcon      = lucide.ChartColumn()
	pinIcon           = lucide.Pin()
	filterIcon        = lucide.Funnel()
	breakIcon         = lucide.CirclePause()
	clearIcon         = lucide.Trash()
	clockIcon         = lucide.Clock()
	backIcon          = lucide.ArrowLeft()
	searchIcon        = lucide.Search()
	sidebarOpenIcon   = lucide.PanelLeftOpen()
	sidebarCloseIcon  = lucide.PanelLeftClose()
	monitorIcon       = lucide.Monitor()
	layersIcon        = lucide.Layers()
	layersPlusIcon    = lucide.LayersPlus()
	closeIcon         = lucide.X()
	sendHistoryIcon   = lucide.RotateCcwClock()
	sendIcon          = lucide.Send()
	compareIcon       = lucide.Columns2()
	errorIcon         = lucide.CircleAlert()
	fileInputIcon     = lucide.FileInput()
	copyIcon          = lucide.Copy()
	formatIcon        = lucide.Braces()
	eyeIcon           = lucide.Eye()
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
	hintIcon          = lucide.Lightbulb()
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
	defer func() { a.systemProxy.shown = a.settingsOpen && !a.toolsOpen && a.settingsTab == 1 }()
	if !a.settingsOpen {
		return
	}
	width, height := c.Size()
	uikit.Modal(c, &a.settingsOpen, locales.Settings(), func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Width(min(1040, width-80)).Height(min(640, height-80)).Children(func() {
			// The title shares the search field's left edge, so the two
			// read as one column above the categories.
			uikit.DialogTitle(c, locales.Settings()).PaddingStart(t.Space(uikit.SpaceXL))
			a.settingsContent(c, t, !a.windowBehind, func() {
				uikit.DialogFooter(c).Children(func() {
					if uikit.SecondaryButton(c, locales.Done()).Clicked() {
						a.settingsOpen = false
					}
				})
			}, true)
		})
	})
	if !a.settingsOpen && a.toolsOpen {
		a.closeTools()
	}
}

// openSettings shows Settings on a category, or the last one for a negative
// tab: in a window of its own beside the main one, focused if already open.
func (a *app) openSettings(tab int) {
	if tab >= 0 {
		a.settingsTab = tab
	}
	if a.toolsOpen {
		a.closeTools()
	}
	a.settingsChoice = strconv.Itoa(a.settingsTab)
	switch a.settingsTab {
	case 1:
		// Let the panel start the entry check, even when focusing an already
		// open Settings window. Starting here too races with its first build.
		a.systemProxy.shown = false
	case 2:
		a.checkCertificate(false)
	}
	a.showSettings()
}

// showSettings brings up the Settings window, which holds the settings and
// the debugging tools, on whatever settingsChoice names.
func (a *app) showSettings() {
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
		Title:  locales.Settings(),
		Width:  1080,
		Height: 680,
		// The tools' list-and-editor pages need the room Tools had.
		MinWidth:        760,
		MinHeight:       460,
		StateKey:        "settings",
		AutoHideMenuBar: true,
		// Settings is the same window material as the main window: the view
		// draws the title bar under the controls, instead of the system's.
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 16, Y: 13},
		Content:              ui.View(a.settingsView),
	})
	a.settingsWin, a.settingsFocused = w, true
	w.OnFocus(func() { w.Update(func() { a.settingsFocused = true }) })
	w.OnBlur(func() { w.Update(func() { a.settingsFocused = false }) })
	w.OnClosed(func() {
		a.win.Update(func() {
			if a.settingsWin == w {
				a.settingsWin, a.settingsOpen, a.settingsFocused = nil, false, false
				a.systemProxy.shown = false
				a.closeTools()
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
	for _, text := range a.attention.drainAnnouncements() {
		c.Announce(text)
	}
	dark := a.appearance == "dark" || a.appearance == "" && c.Theme().Dark
	c.SetTheme(uikit.Theme(dark))
	c.SetMenuPresentation(ui.PopoverMenus)
	t := c.Theme()
	a.drainCertificate()
	a.drainSystemProxy()
	a.drainUpstream()
	a.drainHosts()
	if a.settingsFocused {
		a.showNotices(c)
	}
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.settingsTitleBar(c, t)
		a.settingsContent(c, t, a.settingsFocused, nil, false)
	})
	uikit.Toasts(c)
	a.systemProxy.shown = !a.toolsOpen && a.settingsTab == 1
	if a.win != nil {
		a.win.Invalidate()
	}
}

// settingsTitleBar is the Settings window's own title bar: the traffic lights
// sit on it, then the heading and the search field, and what is neither drags
// the window, as the main window's brand bar does, so Settings is the same
// material as the rest of the app.
func (a *app) settingsTitleBar(c *ui.Context, t *ui.Theme) {
	bar := c.TitleBar()
	start := t.Space(uikit.SpaceXL)
	if bar.Left > 0 {
		start += bar.Left
	}
	ui.Row(c).Height(max(bar.Height, uikit.HeightBar)).PaddingStart(start).PaddingEnd(t.Space(uikit.SpaceXL)).
		Gap(t.Space(uikit.SpaceXL)).AlignItems(ui.Center).DragWindow().Children(func() {
		uikit.Heading(c, locales.Settings()).TextColor(t.Text).SingleLine().Shrink(0)
		a.settingsSearchField(c)
	})
}

// chooseSettingsCategory shows a settings category ("0" to "6") or a tool's
// page by its id. Leaving a tool forgets its unsaved new-rule draft, as
// closing Tools did.
func (a *app) chooseSettingsCategory(id string) {
	a.settingsChoice = id
	if tab, err := strconv.Atoi(id); err == nil {
		if a.toolsOpen {
			a.closeTools()
		}
		a.settingsTab = tab
		if tab == 2 {
			a.checkCertificate(false)
		}
		return
	}
	a.profiles.menuOpen = false
	if id == "rules" && !a.ruleUI.open && len(a.rules) > 0 {
		a.editRule(0, a.rules[0])
	}
	a.toolsOpen, a.toolsPage = true, id
}

// settingsContent is Settings itself: categories on the left, the chosen
// one on the right. active draws the chosen category as in a window in
// front; search says whether the search bar is drawn here, as in the dialog,
// or in the window's title bar. The editors it opens own their values and
// persistence.
func (a *app) settingsContent(c *ui.Context, t *ui.Theme, active bool, footer func(), search bool) {
	if a.settingsQuery != "" {
		c.OnShortcut(0, ui.KeyEscape, func() { a.settingsQuery = "" })
	}
	ui.Column(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
		if search {
			a.settingsSearchBar(c, t)
		}
		if a.settingsQuery != "" {
			ui.Scroll(c).Grow(1).MinHeight(0).MinWidth(0).Padding(0, t.Space(uikit.Space2XL)).Children(func() {
				a.settingsSearchResults(c, t)
			})
			return
		}
		ui.Row(c).Grow(1).FillWidth().MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			uikit.SidebarList(c, "settings-categories", &a.settingsChoice, a.chooseSettingsCategory, func() {
				// Two groups in one list: what the app is set to, then the
				// debugging tools that change traffic, as VS Code keeps its
				// settings and its editors behind one gear.
				uikit.Label(c, locales.Settings()).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceS))
				for _, item := range []struct {
					id    string
					icon  *ui.SVG
					label string
				}{
					{"0", settingsIcon, locales.General()},
					{"3", lucide.SunMoon(), locales.Appearance()},
					{"1", upstreamIcon, locales.Proxy()},
					{"2", lockIcon, locales.HttpsDecryption()},
					{"4", captureIcon, locales.Capture()},
					{"5", lucide.MonitorSmartphone(), locales.Devices()},
					{"6", lucide.KeyRound(), locales.Certificates()},
				} {
					uikit.SidebarItem(c, item.id, item.icon, item.label, a.settingsChoice == item.id, active).Label(locales.SettingsCategory(item.label))
				}
				uikit.Label(c, locales.DebuggingTools()).Padding(t.Space(uikit.SpaceL), t.Space(uikit.SpaceS), t.Space(uikit.SpaceXS), t.Space(uikit.SpaceS))
				for _, page := range toolPages() {
					uikit.SidebarItem(c, page.id, page.icon, page.label, a.settingsChoice == page.id, active).Label(locales.ToolsCategory(page.label))
				}
			}).Width(188).Padding(t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceHair))
			if a.toolsOpen {
				// A tool's page is a list beside its editor, each scrolling
				// on its own: no page scroll or padding around it.
				ui.Divider(c)
				ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
					ui.Row(c).Grow(1).FillWidth().MinHeight(0).AlignItems(ui.Stretch).Children(func() {
						a.toolsPageContent(c, t, active)
					})
					if footer != nil {
						footer()
					}
				})
				return
			}
			// The categories and the page are one surface: the chosen one's
			// wash and the space between the two columns say where each is.
			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				// The search bar above replaces the page's old top inset.
				top := float32(0)
				bottom := t.Space(uikit.Space2XL)
				if a.settingsTab == 6 {
					// The certificates footer is fixed under the scroll: leave
					// its height as content padding so the last client card can
					// scroll clear of it.
					bottom += 64
				}
				ui.Scroll(c).Grow(1).MinHeight(0).MinWidth(0).Padding(top, t.Space(uikit.Space2XL), bottom, t.Space(uikit.Space2XL)).Children(func() {
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
				if a.settingsTab == 6 {
					a.certificateFooter(c, t)
				}
				// The dialog's actions belong to the page, not to the dialog:
				// the categories run to the window's bottom edge beside them.
				if footer != nil {
					footer()
				}
			})
		})
	})
}
