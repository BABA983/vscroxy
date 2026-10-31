package app

import (
	"cmp"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
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

// appTheme is one neutral, flat palette for both workspaces: quiet chrome,
// a single accent for the focused table and the primary action, and no
// decoration that competes with traffic or bodies.
func appTheme(dark bool) *ui.Theme {
	registerAppFonts()
	t := ui.LightTheme()
	if dark {
		t = ui.DarkTheme()
		t.Background = ui.Hex("#1e1e1e")
		t.Surface = ui.Hex("#252525")
		t.SurfaceHover = ui.Hex("#303030")
		t.SurfacePressed = ui.Hex("#3a3a3a")
		t.Border = ui.Hex("#383838")
		t.Text = ui.Hex("#ededed")
		t.TextMuted = ui.Hex("#9a9a9a")
		t.Accent = ui.Hex("#7c7cf0")
		t.AccentHover = ui.Hex("#8b8bf5")
		t.AccentPressed = ui.Hex("#7373e6")
		t.AccentText = ui.Hex("#14142b")
		t.Danger = ui.Hex("#ff8585")
		t.Warning = ui.Hex("#f0ad60")
		t.Success = ui.Hex("#6cce91")
		t.Inverse = ui.Hex("#3a3a3a")
		t.InverseText = ui.Hex("#f5f5f5")
	} else {
		t.Background = ui.Hex("#ffffff")
		t.Surface = ui.Hex("#f5f5f7")
		t.SurfaceHover = ui.Hex("#ebebed")
		t.SurfacePressed = ui.Hex("#e0e0e3")
		t.Border = ui.Hex("#d9d9de")
		t.Text = ui.Hex("#1d1d1f")
		t.TextMuted = ui.Hex("#6e6e73")
		t.Accent = ui.Hex("#5b5bd6")
		t.AccentHover = ui.Hex("#5050c5")
		t.AccentPressed = ui.Hex("#4444b0")
		t.AccentText = ui.Hex("#ffffff")
		t.Danger = ui.Hex("#bb2530")
		t.Warning = ui.Hex("#995000")
		t.Success = ui.Hex("#1d723b")
		t.Inverse = ui.Hex("#e8e8ec")
		t.InverseText = ui.Hex("#1d1d1f")
	}
	t.Font = fontUI
	t.FontSize = textBody
	t.Radius = radiusControl
	t.Spacing = 4
	t.Selection = t.Accent.Alpha(0.3)
	t.Focus = t.Accent
	return t
}

// appColors extends MyGo's theme with the app's remaining semantic roles.
// Success/warning/danger stay on Theme so native widgets share those colors.
type appPalette struct {
	TextFaint, Info, MethodGet, MethodPatch ui.Color
}

var lightPalette = appPalette{
	TextFaint: ui.Hex("#85858c"), Info: ui.Hex("#076b80"),
	MethodGet: ui.Hex("#235eba"), MethodPatch: ui.Hex("#a51db5"),
}

var darkPalette = appPalette{
	TextFaint: ui.Hex("#85858e"), Info: ui.Hex("#69cadc"),
	MethodGet: ui.Hex("#7eb3ff"), MethodPatch: ui.Hex("#e98aef"),
}

func appColors(t *ui.Theme) appPalette {
	if t.Dark {
		return darkPalette
	}
	return lightPalette
}

func methodColor(method string, t *ui.Theme) ui.Color {
	switch method {
	case "GET":
		return appColors(t).MethodGet
	case "POST":
		return t.Success
	case "PUT":
		return t.Warning
	case "DELETE":
		return t.Danger
	case "PATCH":
		return appColors(t).MethodPatch
	case "WS":
		return appColors(t).Info
	default:
		return t.TextMuted
	}
}

// Body readers and the authored body share syntax colors, not error colors.
func bodyEditorTheme(t *ui.Theme) *editor.Theme {
	theme := editor.DefaultTheme(t)
	if !t.Dark {
		theme.Tokens[editor.TokenProperty].Color = ui.Hex("#007e8a")
		theme.Tokens[editor.TokenString].Color = ui.Hex("#a34516")
	}
	return theme
}

// DialogBase retains the library's modal focus and dismissal rules; only
// the shared desktop chrome is ours.
func appModal(c *ui.Context, open *bool, content func()) ui.Element {
	t := c.Theme()
	width, height := c.Size()
	dialog := ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.45))
		panel.Padding(t.Space(5)).Gap(t.Space(3)).Radius(radiusDialog).Background(t.Background).Border(1, t.Border).
			MaxWidth(width-40).MaxHeight(height-40).Shadow(0, 16, 40, 0, ui.RGBA(0, 0, 0, 0.3))
		content()
	})
	if dialog.Dismissed() {
		// MyGo v0.7.3 restores the opener after painting. Render its focus
		// ring even when no hover or animation would request another frame.
		c.Invalidate()
	}
	return dialog
}

func dialogTitle(c *ui.Context, title string) ui.Element {
	return ui.Text(c, title).FontSize(textTitle).Bold().TextColor(c.Theme().Text)
}

// flatTabs is a row of plain text tabs with an underline under the chosen
// one. The prefix names each tab for assistive technology, so a screen with
// two tab rows of the same labels can name the one it means.
func flatTabs(c *ui.Context, selected *int, prefix string, labels ...string) ui.Element {
	t := c.Theme()
	group := ui.TabsBase(c, selected, len(labels))
	group.List.Gap(t.Space(0.5)).Children(func() {
		for i, label := range labels {
			name := label
			if prefix != "" {
				name = prefix + ": " + label
			}
			tab := group.Tab(i).Label(name).Height(heightTabs).Padding(0, t.Space(2)).Radius(0).AlignItems(ui.Center).TextColor(t.TextMuted)
			if i == *selected {
				tab.TextColor(t.Text).Border(0, t.Accent).BorderWidth(0, 0, 2, 0)
			}
			if tab.Pressed() {
				tab.Background(t.SurfacePressed)
			} else if tab.Hovered() {
				tab.Background(t.SurfaceHover)
			}
			tab.Children(func() {
				text := ui.Text(c, label).FontSize(textBody)
				if i == *selected {
					text.Bold()
				}
			})
		}
	})
	return group.List
}

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
	appModal(c, &a.settingsOpen, func() {
		dialogColumn(c, dialogLarge).Height(min(640, height-80)).Children(func() {
			dialogTitle(c, "Settings")
			ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				choice := strconv.Itoa(a.settingsTab)
				ui.Sidebar(c, &choice, func() {
					for _, item := range []struct {
						id    string
						label string
					}{{"0", "General"}, {"3", "Appearance"}, {"1", "Proxy"}, {"2", "HTTPS"}, {"4", "Capture"}, {"5", "Devices"}} {
						sidebarItem(c, item.id, nil, item.label, choice == item.id).Label("Settings category: " + item.label)
					}
				}).Width(140).Padding(4).Gap(0).OnChange(func() {
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
						ui.Text(c, "Rules & scripts").FontSize(textBody).Bold()
						ui.Text(c, "Reload proxy rules, DNS mappings, ignore lists and the capture script from disk.").FontSize(textMeta).TextColor(t.TextMuted)
						ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
							if secondaryButton(c, "Manage rules…").Clicked() {
								a.settingsOpen = false
								a.openRules()
							}
							if secondaryButton(c, "Reload configuration").Clicked() {
								a.reloadConfig()
							}
						})
					case 1:
						addr := "Capture is not running"
						if a.srv != nil && a.srv.Running() {
							addr = "Proxy address: " + a.srv.Addr()
						}
						ui.Text(c, addr).FontSize(textBody).Bold()
						ui.Form(c, func() {
							ui.Field(c, "Listening port", func() {
								ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
									ui.TextInput(c, &a.proxyPortInput).Label("Proxy port").Placeholder("Automatic").Width(100).Height(heightControl).FontSize(textBody).Radius(radiusControl)
									if secondaryButton(c, "Save for next launch").Clicked() {
										a.saveProxyPort()
									}
								})
							}).Description("Blank = automatic (prefers 7890). Fixed port: 1024–65535, no fallback.").Error(a.proxyPortProblem)
						})
						next := "Next launch: Automatic · restart the app to apply"
						if a.proxyPort != 0 {
							next = "Next launch: 127.0.0.1:" + strconv.Itoa(a.proxyPort) + " · restart the app to apply"
						}
						ui.Text(c, next).FontSize(textMeta).FontFeatures("tnum").TextColor(t.TextMuted)
						ui.Divider(c)
						a.systemProxySettings(c, t)
						ui.Divider(c)
						ui.Text(c, "Upstream: "+cmp.Or(a.upstream, "Direct connection")).FontSize(textMeta).TextColor(t.TextMuted).SingleLine()
						ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
							if secondaryButton(c, "Configure upstream…").Clicked() {
								a.settingsOpen = false
								a.upstreamAddr, a.upstreamOpen = a.upstream, true
							}
							if secondaryButton(c, "Connect a device…").Clicked() {
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
						ui.Text(c, "Capture visibility").FontSize(textBody).Bold()
						ui.Text(c, "Ignored traffic stays proxied but is not shown in the table.").FontSize(textMeta).TextColor(t.TextMuted)
						ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
							if secondaryButton(c, "Ignored hosts…").Clicked() {
								a.settingsOpen, a.ignoreOpen = false, true
							}
							if secondaryButton(c, "Ignored apps…").Clicked() {
								a.settingsOpen, a.ignoreAppsOpen = false, true
							}
						})
					case 5:
						ui.Text(c, "Device and client access").FontSize(textBody).Bold()
						ui.Text(c, "Device access requires explicit network consent and an allowlist.").FontSize(textMeta).TextColor(t.TextMuted)
						ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
							if secondaryButton(c, "Connect a device…").Clicked() {
								a.settingsOpen = false
								a.openDevices()
							}
							if secondaryButton(c, "Set up a local client…").Clicked() {
								a.settingsOpen = false
								a.openClientSetup()
							}
						})
					}
				})
			})
			dialogFooter(c).Children(func() {
				if secondaryButton(c, "Done").Clicked() {
					a.settingsOpen = false
				}
			})
		})
	})
}

// Swatches are user-facing labels rather than semantic roles, so they keep
// the same saturated value in both appearances and always carry white text.
var swatchText = ui.Hex("#ffffff")

// highlightColors are the colors a flow can be marked with, in menu order.
var highlightColors = []struct {
	name  string
	color ui.Color
}{
	{"Red", ui.Hex("#dc2626")},
	{"Orange", ui.Hex("#ea580c")},
	{"Yellow", ui.Hex("#ca8a04")},
	{"Green", ui.Hex("#16a34a")},
	{"Blue", ui.Hex("#2563eb")},
	{"Purple", ui.Hex("#7c3aed")},
}

// badgePalette colors a client's initial when the system has no icon for it.
var badgePalette = []ui.Color{
	ui.Hex("#2563eb"), ui.Hex("#7c3aed"), ui.Hex("#db2777"), ui.Hex("#ea580c"),
	ui.Hex("#0891b2"), ui.Hex("#16a34a"), ui.Hex("#ca8a04"),
}
