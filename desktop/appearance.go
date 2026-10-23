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
)

// appTheme keeps content quiet and gives navigation and controls a separate
// surface. Both appearances use the same typography and spacing.
func appTheme(dark bool) *ui.Theme {
	t := ui.LightTheme()
	if dark {
		t = ui.DarkTheme()
		t.Background = ui.Hex("#171a21")
		t.Surface = ui.Hex("#20242e")
		t.SurfaceHover = ui.Hex("#2b3140")
		t.SurfacePressed = ui.Hex("#343d50")
		t.Border = ui.Hex("#343b49")
		t.Text = ui.Hex("#e5e9f0")
		t.TextMuted = ui.Hex("#a3adbd")
	} else {
		t.Background = ui.Hex("#ffffff")
		t.Surface = ui.Hex("#f2f4f8")
		t.SurfaceHover = ui.Hex("#e8ecf3")
		t.SurfacePressed = ui.Hex("#dce3ee")
		t.Border = ui.Hex("#dce1e9")
		t.Text = ui.Hex("#202939")
		t.TextMuted = ui.Hex("#667387")
	}
	t.FontSize = 13
	t.Radius = 7
	return t
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
	ui.Modal(c, &a.settingsOpen, func() {
		ui.Column(c).Width(min(620, width-80)).Height(height).Gap(16).Children(func() {
			ui.Text(c, "Settings").FontSize(18).Bold()
			if ui.Tabs(c, &a.settingsTab, "General", "Proxy", "HTTPS").Changed() {
				switch a.settingsTab {
				case 1:
					a.checkSystemProxy()
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
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.Button(c, "Done").Clicked() {
					a.settingsOpen = false
				}
			})
		})
	})
}
