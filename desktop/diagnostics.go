package main

import (
	"cmp"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/egoist/mygo/ui"
)

type captureDiagnostics struct {
	open   bool
	target string
	scroll ui.ScrollState
}

func (a *app) openDiagnostics() {
	target := "https://example.com/"
	if f, ok := a.current(); ok && f.url != "" {
		target = f.url
	}
	a.diagnostics = captureDiagnostics{open: true, target: target}
	a.checkSystemProxy(false)
	a.checkCertificate(false)
}

// A CONNECT authority with an explicit :443 and a decrypted HTTPS URL without
// it describe the same origin; :8443 and a different scheme do not.
func diagnosticOrigin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func (a *app) diagnosticsModal(c *ui.Context, t *ui.Theme) {
	d := &a.diagnostics
	if !d.open {
		return
	}
	width, height := c.Size()
	ui.Modal(c, &d.open, func() {
		ui.Column(c).Width(min(800, width-80)).Height(min(680, height-80)).Gap(12).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Capture diagnostics").FontSize(18).Bold().Grow(1)
				if ui.Button(c, "Refresh checks").Disabled(a.systemProxy.checking || a.systemProxy.applying || a.certificate.checking).Clicked() {
					a.checkSystemProxy(false)
					a.checkCertificate(false)
				}
			})
			ui.Text(c, "Read-only checks · no proxy changes, CA installation, test requests or LAN exposure.").FontSize(11).TextColor(t.TextMuted)
			if ui.TextInput(c, &d.target).Label("Target URL").Placeholder("https://api.example.com/").Changed() {
				d.scroll.Y = 0
			}
			ui.Scroll(c).TrackScroll(&d.scroll).Grow(1).Gap(10).Padding(0, 16, 8, 0).Children(func() {
				ui.Text(c, "1  Capture listener").Bold()
				if a.srv != nil && a.srv.Running() {
					ui.Text(c, "Listening at "+a.srv.Addr()).FontSize(12).TextColor(t.Success)
				} else {
					ui.Text(c, "Capture is stopped. Start capture before configuring a client.").FontSize(12).TextColor(t.Warning)
				}
				ui.Text(c, "Listener state is not proof that a client can reach it.").FontSize(11).TextColor(t.TextMuted)

				ui.Text(c, "2  System proxy · OS readback").Bold()
				status, color := a.systemProxyStatus(t)
				ui.Text(c, status).FontSize(12).TextColor(color)
				detail, _ := a.systemProxyDetail(a.systemProxyTarget())
				ui.Text(c, detail).FontSize(11).Selectable()
				ui.Text(c, "Last check only. Apps may ignore these settings or use their own proxy; a matching OS setting does not prove their traffic reaches this app.").FontSize(11).TextColor(t.TextMuted)

				ui.Text(c, "3  Target evidence · retained live session").Bold()
				u, err := url.Parse(strings.TrimSpace(d.target))
				if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
					ui.Text(c, "Enter an HTTP or HTTPS URL without embedded credentials.").FontSize(12).TextColor(t.Warning)
				} else {
					shown := make(map[int]bool, len(a.visible))
					for _, i := range a.visible {
						shown[i] = true
					}
					var latest *flow
					matched, visible, decrypted, tunnels := 0, 0, 0, 0
					for i := range a.flows {
						f := &a.flows[i]
						if !f.live {
							continue
						}
						captured, parseErr := url.Parse(f.url)
						if parseErr != nil || diagnosticOrigin(captured) != diagnosticOrigin(u) {
							continue
						}
						matched++
						latest = f
						if shown[i] {
							visible++
						}
						if f.kind == "connect" {
							tunnels++
						} else if f.scheme == "https" && f.reqHeaders != "" {
							decrypted++
						}
					}
					ui.Text(c, fmt.Sprintf("%d live records · %d shown · %d hidden in the current capture view", matched, visible, matched-visible)).FontSize(12)
					if progress, failure := a.views.status(); progress != "" {
						note := " · view visibility may still change"
						if failure {
							note = " · some records could not be evaluated"
						}
						ui.Text(c, progress+note).FontSize(11).TextColor(t.Warning)
					}
					if latest == nil {
						ui.Text(c, "No retained live evidence for this origin. This does not establish a connection failure: the client may not have sent a request, bypassed the proxy, or matched an ignored host/app.").FontSize(12).TextColor(t.Warning)
					} else {
						ui.Text(c, fmt.Sprintf("Latest #%d · %s · %s", latest.id, latest.method, latest.at.Local().Format("15:04:05"))).FontSize(12)
						if latest.conn.error != "" {
							ui.Text(c, cmp.Or(latest.conn.errorStage, "Connection")+" failed: "+latest.conn.error).FontSize(12).TextColor(t.Warning).Selectable()
						} else if latest.kind == "connect" {
							ui.Text(c, "CONNECT is tunnel evidence, not proof of a decrypted request or a successful response.").FontSize(11).TextColor(t.TextMuted)
						} else {
							ui.Text(c, fmt.Sprintf("Recorded HTTP status: %d", latest.status)).FontSize(12)
						}
						if len(latest.actions) > 0 {
							ui.Text(c, "Recorded actions on this exchange: "+strings.Join(latest.actions, " · ")).FontSize(11).Selectable()
						} else {
							ui.Text(c, "No rule/script actions recorded on this exchange.").FontSize(11).TextColor(t.TextMuted)
						}
						if ui.Button(c, "Inspect latest evidence").FontSize(11).Tooltip("Hidden records open in a new capture view; existing filters are preserved").Clicked() {
							d.open = false
							a.inspectCapture(latest.id)
						}
					}
					ui.Text(c, "Matches scheme, host and port, not path. Includes previous traffic from any client in this live session; imported, cleared and evicted records do not count.").FontSize(11).TextColor(t.TextMuted)
					if u.Scheme == "https" {
						policy := "Disabled for new connections"
						if a.srv != nil && a.srv.SSL().Decrypts(u.Hostname()) {
							policy = "Enabled for new connections; not proof of decryption"
						}
						ui.Text(c, "HTTPS policy: "+policy).FontSize(12)
						ui.Text(c, fmt.Sprintf("Observed HTTPS: %d decrypted request records · %d CONNECT records", decrypted, tunnels)).FontSize(12)
						trust, trustColor := a.certificateStatus(t)
						ui.Text(c, "Local CA: "+trust).FontSize(12).TextColor(trustColor)
						ui.Text(c, "Client CA trust and pinning are not verified here. A decrypted request may come from a client that skips verification. Reconnect after changing HTTPS policy.").FontSize(11).TextColor(t.TextMuted)
					}
				}

				ui.Text(c, "4  Current configuration ≠ recorded execution").Bold()
				if a.srv != nil {
					rules := a.srv.Rules()
					enabled := 0
					for _, rule := range rules {
						if !rule.Disabled {
							enabled++
						}
					}
					ui.Text(c, fmt.Sprintf("Engine: %d enabled rules · No Caching %t · Break requests %t · Upstream configured %t", enabled, a.srv.NoCaching(), a.srv.Breaking(), a.srv.Upstream() != "")).FontSize(11)
					ui.Text(c, fmt.Sprintf("Ignored hosts: %d · Ignored apps: %d · Dropped capture events: %d", len(a.srv.Ignore()), len(a.srv.IgnoreApps()), a.srv.Dropped())).FontSize(11)
				}
				ui.Text(c, "Ignored traffic is still proxied but not retained. Current rules do not explain past exchanges; inspect their recorded actions and Connection log. No match is predicted from configuration alone.").FontSize(11).TextColor(t.TextMuted)
				ui.Row(c).Gap(8).Wrap().Children(func() {
					if ui.Button(c, "Review ignored hosts").FontSize(11).Clicked() {
						d.open, a.ignoreOpen = false, true
					}
					if ui.Button(c, "Review ignored apps").FontSize(11).Clicked() {
						d.open, a.ignoreAppsOpen = false, true
					}
					if ui.Button(c, "Review rules").FontSize(11).Clicked() {
						d.open = false
						a.openRules()
					}
				})
			})
			hint := "End of diagnostics"
			if d.scroll.Y < d.scroll.MaxY-1 {
				hint = "Scroll for more diagnostics and review actions ↓"
			}
			ui.Text(c, hint).FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Wrap().Justify(ui.End).Children(func() {
				if ui.Button(c, "Proxy settings…").Clicked() {
					d.open = false
					a.settingsTab, a.settingsOpen = 1, true
				}
				if ui.Button(c, "HTTPS settings…").Clicked() {
					d.open = false
					a.settingsTab, a.settingsOpen = 2, true
				}
				if ui.Button(c, "Client setup…").Clicked() {
					d.open = false
					a.openClientSetup()
					if target, err := url.Parse(strings.TrimSpace(d.target)); err == nil && target.Scheme == "https" && target.Hostname() != "" && target.User == nil {
						a.clientUI.url = target.String()
					}
				}
				if ui.Button(c, "Close").Clicked() {
					d.open = false
				}
			})
		})
	})
}
