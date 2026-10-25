package main

import (
	"cmp"
	"strconv"

	"github.com/egoist/mygo/ui"
)

var (
	captureIcon  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M2 12h5l3-8 4 16 3-8h5"/></svg>`))
	requestIcon  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M8 13l-2 2 2 2m8-4 2 2-2 2m-3-5-2 6"/></svg>`))
	settingsIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m9 3-.6 3-2 .9L3.6 6 2 9l2.3 2v2L2 15l1.6 3 2.8-.9 2 .9.6 3h6l.6-3 2-.9 2.8.9 1.6-3-2.3-2v-2L22 9l-1.6-3-2.8.9-2-.9L15 3Z"/><circle cx="12" cy="12" r="3"/></svg>`))
	plusIcon     = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>`))
	lockIcon     = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="5" y="10" width="14" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3m-4 5v2"/></svg>`))
	appIcon      = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"><rect x="3" y="3" width="18" height="18" rx="4"/><path d="M3 9h18M7 6h.01M10 6h.01M8 13l-2 2 2 2m4-4h5"/></svg>`))
	trafficIcon  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18M9 9v12M12 13h6m-6 4h6"/></svg>`))
	domainIcon   = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/><path d="M3 12h18M5 6h14M5 18h14"/></svg>`))
)

// appTheme keeps content quiet and gives navigation and controls a separate
// surface. Both appearances use the same typography and spacing.
func appTheme(dark bool) *ui.Theme {
	t := ui.LightTheme()
	if dark {
		t = ui.DarkTheme()
		t.Background = ui.Hex("#191d20")
		t.Surface = ui.Hex("#202529")
		t.SurfaceHover = ui.Hex("#2b3238")
		t.SurfacePressed = ui.Hex("#333b43")
		t.Border = ui.Hex("#343c43")
		t.Text = ui.Hex("#e7edf3")
		t.TextMuted = ui.Hex("#9da8b3")
		t.Accent = ui.Hex("#087bff")
	} else {
		t.Background = ui.Hex("#ffffff")
		t.Surface = ui.Hex("#f3f3f5")
		t.SurfaceHover = ui.Hex("#e9eaed")
		t.SurfacePressed = ui.Hex("#dedfe4")
		t.Border = ui.Hex("#dedfe4")
		t.Text = ui.Hex("#24262c")
		t.TextMuted = ui.Hex("#686c76")
	}
	t.FontSize = 13
	t.Radius = 6
	return t
}

// DialogBase retains the library's modal focus and dismissal rules; only
// the shared desktop chrome is ours.
func appModal(c *ui.Context, open *bool, content func()) ui.Element {
	t := c.Theme()
	width, height := c.Size()
	return ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.48))
		panel.Padding(20).Gap(12).Radius(8).Background(t.Background).Border(1, t.Border).
			MaxWidth(width-40).MaxHeight(height-40).Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.28))
		content()
	})
}

func dialogTitle(c *ui.Context, title string) ui.Element {
	return ui.Text(c, title).FontSize(16).Bold().TextColor(c.Theme().Text)
}

// Body modes and request/response share the same quiet, keyboard-native track.
func inspectorSegments(c *ui.Context, selected *int, labels ...string) ui.Element {
	t := c.Theme()
	group := ui.SegmentedBase(c, selected, len(labels))
	group.Track.Padding(2).Radius(6).Border(1, t.Border).Background(t.Surface).Children(func() {
		for i, label := range labels {
			segment := group.Segment(i).Height(24).Padding(0, 9).Radius(4).AlignItems(ui.Center).Label(label).TextColor(t.TextMuted)
			if *selected == i {
				segment.Background(t.Background).TextColor(t.Text)
			} else if segment.Pressed() {
				segment.Background(t.SurfacePressed)
			} else if segment.Hovered() {
				segment.Background(t.SurfaceHover).TextColor(t.Text)
			}
			segment.Children(func() { ui.Text(c, label).FontSize(11) })
		}
	})
	return group.Track
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
