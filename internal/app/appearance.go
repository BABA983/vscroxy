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
		t.AccentPressed = ui.Hex("#6b6bda")
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
		t.Danger = ui.Hex("#c42b36")
		t.Warning = ui.Hex("#995000")
		t.Success = ui.Hex("#217a40")
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
	TextFaint: ui.Hex("#85858c"), Info: ui.Hex("#087e96"),
	MethodGet: ui.Hex("#2563c4"), MethodPatch: ui.Hex("#c026d3"),
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
	return ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.45))
		panel.Padding(20).Gap(12).Radius(radiusDialog).Background(t.Background).Border(1, t.Border).
			MaxWidth(width-40).MaxHeight(height-40).Shadow(0, 16, 40, 0, ui.RGBA(0, 0, 0, 0.3))
		content()
	})
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
	group.List.Gap(2).Children(func() {
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
	height := float32(372)
	if a.settingsTab == 1 || a.settingsTab == 2 {
		height = 520
	}
	if a.settingsTab == 2 {
		height = 600
	}
	width, windowHeight := c.Size()
	height = min(height, windowHeight-80)
	appModal(c, &a.settingsOpen, func() {
		ui.Column(c).Width(min(620, width-80)).Height(height).Gap(12).Children(func() {
			dialogTitle(c, "Settings")
			if ui.Tabs(c, &a.settingsTab, "General", "Proxy", "HTTPS").Changed() {
				switch a.settingsTab {
				case 1:
					a.checkSystemProxy(true)
				case 2:
					a.checkCertificate(false)
				}
			}
			ui.Scroll(c).Grow(1).Gap(14).Padding(8, 20, 8, 0).Children(func() {
				switch a.settingsTab {
				case 0:
					ui.Row(c).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).Gap(4).Children(func() {
							ui.Text(c, "Dark appearance").Bold()
							ui.Text(c, "Use a low-glare palette for traffic and editors.").FontSize(12).TextColor(t.TextMuted)
						})
						ui.Switch(c, &a.dark).Label("Dark appearance")
					})
					ui.Divider(c)
					ui.Text(c, "Rules & scripts").Bold()
					ui.Text(c, "Reload proxy rules, DNS mappings, ignore lists and the capture script from disk.").FontSize(12).TextColor(t.TextMuted)
					ui.Row(c).Gap(8).Children(func() {
						if ui.Button(c, "Manage rules…").Clicked() {
							a.settingsOpen = false
							a.openRules()
						}
						if ui.Button(c, "Reload configuration").Clicked() {
							a.reloadConfig()
						}
					})
				case 1:
					addr := "Capture is not running"
					if a.srv != nil && a.srv.Running() {
						addr = "Proxy address: " + a.srv.Addr()
					}
					ui.Text(c, addr).Bold()
					ui.Column(c).Gap(6).Children(func() {
						ui.Text(c, "Listening port").FontSize(12).Bold()
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.TextInput(c, &a.proxyPortInput).Label("Proxy port").Placeholder("Automatic").Width(120)
							if ui.Button(c, "Save for next launch").Clicked() {
								a.saveProxyPort()
							}
						})
						ui.Text(c, "Blank = automatic (prefers 7890). Fixed port: 1024–65535, no fallback.").FontSize(12).TextColor(t.TextMuted)
						next := "Next launch: Automatic · restart the app to apply"
						if a.proxyPort != 0 {
							next = "Next launch: 127.0.0.1:" + strconv.Itoa(a.proxyPort) + " · restart the app to apply"
						}
						ui.Text(c, next).FontSize(12).TextColor(t.TextMuted)
						if a.proxyPortProblem != "" {
							ui.Text(c, a.proxyPortProblem).FontSize(12).TextColor(t.Danger).SingleLine().Tooltip(a.proxyPortProblem)
						}
					})
					ui.Divider(c)
					a.systemProxySettings(c, t)
					ui.Divider(c)
					ui.Text(c, "Upstream: "+cmp.Or(a.upstream, "Direct connection")).FontSize(12).TextColor(t.TextMuted).SingleLine()
					ui.Row(c).Gap(8).Children(func() {
						if ui.Button(c, "Configure upstream…").Clicked() {
							a.settingsOpen = false
							a.upstreamAddr, a.upstreamOpen = a.upstream, true
						}
						if ui.Button(c, "Connect a device…").Clicked() {
							a.settingsOpen = false
							a.openDevices()
						}
					})
				case 2:
					a.httpsSettings(c, t)
				}
			})
			ui.Divider(c)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Done").Clicked() {
					a.settingsOpen = false
				}
			})
		})
	})
}
