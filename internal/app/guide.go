package app

// The setup guide: four steps from a fresh install to the first decrypted
// request, shown in place of the traffic list on the first launch (no
// ~/.vscroxy yet). Each step completes from what the app observes, not from
// a button the user presses: the OS proxy read back, the certificate check,
// a decryption rule, a decrypted request. Skip guide closes it; the command
// palette opens it again.

import (
	"fmt"
	"slices"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/system"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// guideChoices bounds the apps and domains step 3 lists: the busiest first.
const guideChoices = 6

// guideSteps reports which steps are done, in order, and the first decrypted
// request once one arrived.
func (a *app) guideSteps() (done [4]bool, decrypted int) {
	decrypted = -1
	captured := false
	for _, f := range a.flows {
		if !f.live {
			continue
		}
		captured = true
		if f.kind == "" && f.scheme == "https" && decrypted < 0 {
			decrypted = f.id
		}
	}
	cert := &a.certificate
	cfg := a.inspectionConfig()
	done[0] = captured || a.systemProxy.check.Status == system.ProxyMatches
	done[1] = cert.checked && cert.cert != nil && cert.err == nil
	done[2] = cfg.All || len(cfg.Hosts) > 0 || len(cfg.Apps) > 0
	done[3] = decrypted >= 0
	return done, decrypted
}

// openGuide shows the guide again over the traffic list.
func (a *app) openGuide() {
	a.guide, a.page = true, pageLive
}

// closeGuide hides it, for good unless the palette opens it again.
func (a *app) closeGuide() {
	a.guide = false
	a.lastSave = time.Time{}
	a.persistSettings()
}

func (a *app) setupGuide(c *ui.Context) {
	t := c.Theme()
	if s := &a.certificate; !s.checked && !s.checking {
		a.checkCertificate(false)
	}
	if s := &a.systemProxy; !s.checked && !s.checking && !s.applying {
		a.checkSystemProxy(false)
	}
	done, decrypted := a.guideSteps()
	if done[3] {
		// The last step is the request itself: select it, so its details
		// show beside the list.
		a.closeGuide()
		a.pickedID = decrypted
		a.restoreSelection()
		if view := a.views.activeView(); view != nil {
			view.list.ScrollIntoView(a.picked)
		}
		a.setMessage("Setup complete · the first decrypted request is selected")
		return
	}
	current := 0
	for current < 3 && done[current] {
		current++
	}
	ui.Scroll(c).Fill().Padding(t.Space(8), t.Space(4)).Children(func() {
		ui.Column(c).Width(520).MaxWidth(520).AlignSelf(ui.Center).Gap(t.Space(3)).Label("Setup guide").Children(func() {
			ui.Column(c).FillWidth().Border(1, t.Border).Radius(uikit.RadiusPanel).Background(t.Background).Children(func() {
				steps := []struct{ title, summary string }{
					{"Start capturing", "Point clients at this app's proxy."},
					{"Trust the certificate", "HTTPS stays encrypted until clients trust this app's CA."},
					{"Choose what to decrypt", "Pick the apps and domains whose HTTPS you want to read."},
					{"Done", "The first decrypted request opens here."},
				}
				for i, step := range steps {
					if i > 0 {
						ui.Divider(c)
					}
					// Step 3 stays open while waiting for the first request, so
					// more apps and domains can still be ticked.
					open := i == current || i == 2 && current == 3
					a.guideStep(c, t, i, step.title, step.summary, done[i], i == current, open)
				}
			})
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				if n := len(a.flows); n > 0 {
					uikit.Meta(c, fmt.Sprintf("%d %s captured so far", n, plural(n, "request", "requests")))
				}
				ui.Spacer(c)
				if uikit.ToolbarButton(c, "Skip guide", "Show the traffic list; reopen the guide from the command palette", false).
					Children(func() { ui.Text(c, "Skip guide") }).Clicked() {
					a.closeGuide()
				}
			})
		})
	})
}

// guideStep is one row of the checklist: its mark, title and summary, and
// what it asks for while open.
func (a *app) guideStep(c *ui.Context, t *ui.Theme, i int, title, summary string, done, current, open bool) {
	p := uikit.Colors(t)
	row := ui.Row(c.Key(fmt.Sprintf("guide-step-%d", i))).FillWidth().Gap(t.Space(3)).Padding(t.Space(3), t.Space(4)).AlignItems(ui.Start)
	if current {
		row.Background(p.AccentTint.Alpha(0.5))
	}
	row.Label(fmt.Sprintf("Step %d: %s", i+1, title)).Children(func() {
		mark := ui.Row(c).Size(20, 20).Shrink(0).Radius(10).Justify(ui.Center).AlignItems(ui.Center)
		switch {
		case done:
			mark.Background(p.Accent).TextColor(p.AccentText).Label("Done").Children(func() {
				ui.Icon(c, checkIcon).Size(12, 12)
			})
		case current:
			mark.Background(p.Accent).TextColor(p.AccentText).Children(func() {
				ui.Text(c, fmt.Sprint(i+1)).FontSize(uikit.TextMeta).Bold()
			})
		default:
			mark.Background(t.Text.Alpha(0.08)).TextColor(t.TextMuted).Children(func() {
				ui.Text(c, fmt.Sprint(i+1)).FontSize(uikit.TextMeta)
			})
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(t.Space(1)).Children(func() {
			heading := ui.Text(c, title).FontSize(uikit.TextBody).TextColor(t.Text)
			if current {
				heading.Bold()
			}
			uikit.Meta(c, summary)
			if open {
				ui.Column(c).FillWidth().Gap(t.Space(2)).Padding(t.Space(1), 0, 0, 0).Children(func() {
					switch i {
					case 0:
						a.guideCapture(c, t)
					case 1:
						a.guideCertificate(c, t)
					case 2:
						a.guideDecrypt(c, t)
					case 3:
						uikit.Meta(c, "Rules apply to new connections: reload the page or restart the app you are inspecting.")
					}
				})
			}
		})
	})
}

func (a *app) guideCapture(c *ui.Context, t *ui.Theme) {
	if a.srv == nil || !a.srv.Running() {
		ui.Text(c, "Capture is stopped.").FontSize(uikit.TextBody).TextColor(t.Warning)
		ui.Row(c).Children(func() {
			if uikit.SecondaryButton(c, "Diagnose capture…").Clicked() {
				a.openDiagnostics()
			}
		})
		return
	}
	ui.Row(c).Gap(t.Space(1)).Children(func() {
		uikit.Meta(c, "Listening at")
		uikit.Meta(c, a.srv.Addr()).Font(uikit.FontMono).Selectable()
	})
	s := &a.systemProxy
	ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
		if uikit.PrimaryButton(c, "Turn on system proxy").Disabled(s.applying || s.checking).Clicked() {
			a.setSystemProxy(true)
		}
		if uikit.SecondaryButton(c, "Capture one app only…").Clicked() {
			a.openClientSetup()
		}
	})
	if s.checked || s.applying {
		status, color := a.systemProxyStatus(t)
		ui.Text(c, status).FontSize(uikit.TextMeta).TextColor(color)
	}
}

func (a *app) guideCertificate(c *ui.Context, t *ui.Theme) {
	s := &a.certificate
	status, color := a.certificateStatus(t)
	ui.Row(c).Children(func() {
		if uikit.PrimaryButton(c, "Install and trust").Disabled(s.checking).Clicked() {
			a.checkCertificate(true)
		}
	})
	ui.Text(c, status).FontSize(uikit.TextMeta).TextColor(color)
	uikit.Meta(c, "Some apps keep their own trust store, or pin certificates; they may need setting up themselves.")
}

// guideDecrypt lists the apps and domains seen so far, each a check box
// that adds or removes its decryption rule at once.
func (a *app) guideDecrypt(c *ui.Context, t *ui.Theme) {
	cfg := a.inspectionConfig()
	var apps []capture.SSLApp
	for _, client := range a.clients {
		apps = append(apps, a.capturedApps(client.name)...)
		if len(apps) >= guideChoices {
			break
		}
	}
	apps = apps[:min(len(apps), guideChoices)]
	domains := a.domains[:min(len(a.domains), guideChoices)]
	if len(apps) == 0 && len(domains) == 0 {
		uikit.Meta(c, "Apps and domains show here as their traffic arrives.")
		return
	}
	box := func(key, label string, on bool, set func(bool)) {
		ui.Checkbox(c.Key(key), &on, label).FontSize(uikit.TextBody).OnClick(func() { set(on) })
	}
	for _, app := range apps {
		box("guide-app:"+app.Path, app.Name, a.appInspectionEnabled(app.Path), func(on bool) { a.setAppInspection(app, on) })
	}
	for _, d := range domains {
		host := hostOnly(d.name)
		box("guide-host:"+host, host, cfg.All || slices.Contains(cfg.Hosts, host), func(on bool) {
			if on {
				a.decryptHost(host)
				return
			}
			next := a.inspectionConfig()
			next.Hosts = slices.DeleteFunc(next.Hosts, func(h string) bool { return h == host })
			a.saveSSL(next)
		})
	}
	uikit.Meta(c, "Headers, cookies and bodies of these become readable. More rules in HTTPS rules.")
}
