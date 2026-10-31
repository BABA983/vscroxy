package app

import (
	"cmp"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"net"
	"net/url"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
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
	_, height := c.Size()
	uikit.Modal(c, &d.open, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(680, height-80)).Children(func() {
			ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
				uikit.DialogTitle(c, "Capture diagnostics").Grow(1)
				if uikit.SecondaryButton(c, "Refresh checks").Disabled(a.systemProxy.checking || a.systemProxy.applying || a.certificate.checking).Clicked() {
					a.checkSystemProxy(false)
					a.checkCertificate(false)
				}
			})
			ui.Text(c, "Read-only checks · no proxy changes, CA installation, test requests or LAN exposure.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
			ui.Form(c, func() {
				ui.Field(c, "Target URL", func() {
					if ui.TextInput(c, &d.target).Label("Target URL").Placeholder("https://api.example.com/").Height(uikit.HeightControl).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).Changed() {
						d.scroll.Y = 0
					}
				})
			})
			ui.Scroll(c).TrackScroll(&d.scroll).Grow(1).MinHeight(0).Gap(t.Space(3)).Padding(0, t.Space(4), t.Space(2), 0).Children(func() {
				ui.Text(c, "1  Capture listener").FontSize(uikit.TextBody).Bold()
				if a.srv != nil && a.srv.Running() {
					ui.Text(c, "Listening at "+a.srv.Addr()).FontSize(uikit.TextBody).TextColor(t.Success)
				} else {
					ui.Text(c, "Capture is stopped. Start capture before configuring a client.").FontSize(uikit.TextBody).TextColor(t.Warning)
				}
				ui.Text(c, "Listener state is not proof that a client can reach it.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)

				ui.Text(c, "2  System proxy · OS readback").FontSize(uikit.TextBody).Bold()
				status, color := a.systemProxyStatus(t)
				ui.Text(c, status).FontSize(uikit.TextBody).TextColor(color)
				detail, _ := a.systemProxyDetail(a.systemProxyTarget())
				ui.Text(c, detail).FontSize(uikit.TextMeta).Selectable()
				ui.Text(c, "Last check only. Apps may ignore these settings or use their own proxy; a matching OS setting does not prove their traffic reaches this app.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)

				ui.Text(c, "3  Target evidence · retained live session").FontSize(uikit.TextBody).Bold()
				u, err := url.Parse(strings.TrimSpace(d.target))
				if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
					ui.Text(c, "Enter an HTTP or HTTPS URL without embedded credentials.").FontSize(uikit.TextBody).TextColor(t.Warning)
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
					ui.Text(c, fmt.Sprintf("%d live records · %d shown · %d hidden in the current capture view", matched, visible, matched-visible)).FontSize(uikit.TextBody)
					if progress, failure := a.views.status(); progress != "" {
						note := " · view visibility may still change"
						if failure {
							note = " · some records could not be evaluated"
						}
						ui.Text(c, progress+note).FontSize(uikit.TextMeta).TextColor(t.Warning)
					}
					if latest == nil {
						ui.Text(c, "No retained live evidence for this origin. This does not establish a connection failure: the client may not have sent a request, bypassed the proxy, or matched an ignored host/app.").FontSize(uikit.TextBody).TextColor(t.Warning)
					} else {
						ui.Text(c, fmt.Sprintf("Latest #%d · %s · %s", latest.id, latest.method, latest.at.Local().Format("15:04:05"))).FontSize(uikit.TextBody)
						if latest.conn.error != "" {
							ui.Text(c, cmp.Or(latest.conn.errorStage, "Connection")+" failed: "+latest.conn.error).FontSize(uikit.TextBody).TextColor(t.Warning).Selectable()
						} else if latest.kind == "connect" {
							ui.Text(c, "CONNECT is tunnel evidence, not proof of a decrypted request or a successful response.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
						} else {
							ui.Text(c, fmt.Sprintf("Recorded HTTP status: %d", latest.status)).FontSize(uikit.TextBody)
						}
						if len(latest.actions) > 0 {
							ui.Text(c, "Recorded actions on this exchange: "+strings.Join(latest.actions, " · ")).FontSize(uikit.TextMeta).Selectable()
						} else {
							ui.Text(c, "No rule/script actions recorded on this exchange.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
						}
						if uikit.SecondaryButton(c, "Inspect latest evidence").Tooltip("Hidden records open in a new capture view; existing filters are preserved").Clicked() {
							d.open = false
							a.inspectCapture(latest.id)
						}
					}
					ui.Text(c, "Matches scheme, host and port, not path. Includes previous traffic from any client in this live session; imported, cleared and evicted records do not count.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
					if u.Scheme == "https" {
						policy := "Disabled for new connections"
						if a.srv != nil && a.srv.SSL().Decrypts(u.Hostname()) {
							policy = "Enabled for new connections; not proof of decryption"
						}
						ui.Text(c, "HTTPS policy: "+policy).FontSize(uikit.TextBody)
						ui.Text(c, fmt.Sprintf("Observed HTTPS: %d decrypted request records · %d CONNECT records", decrypted, tunnels)).FontSize(uikit.TextBody)
						trust, trustColor := a.certificateStatus(t)
						ui.Text(c, "Local CA: "+trust).FontSize(uikit.TextBody).TextColor(trustColor)
						ui.Text(c, "Client CA trust and pinning are not verified here. A decrypted request may come from a client that skips verification. Reconnect after changing HTTPS policy.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
					}
				}

				ui.Text(c, "4  Current configuration ≠ recorded execution").FontSize(uikit.TextBody).Bold()
				if a.srv != nil {
					rules := a.srv.Rules()
					enabled := 0
					for _, rule := range rules {
						if !rule.Disabled && (rule.Scenario == "" || rule.ScenarioActive) {
							enabled++
						}
					}
					ui.Text(c, fmt.Sprintf("Engine: %d enabled rules · No Caching %t · Break requests %t · Upstream configured %t", enabled, a.srv.NoCaching(), a.srv.Breaking(), a.srv.Upstream() != "")).FontSize(uikit.TextMeta)
					ui.Text(c, "Active scenario: "+cmp.Or(capture.ActiveScenario(rules), "none · global rules only")).FontSize(uikit.TextMeta)
					ui.Text(c, fmt.Sprintf("Ignored hosts: %d · Ignored apps: %d · Dropped capture events: %d", len(a.srv.Ignore()), len(a.srv.IgnoreApps()), a.srv.Dropped())).FontSize(uikit.TextMeta)
				}
				ui.Text(c, "Ignored traffic is still proxied but not retained. Current rules do not explain past exchanges; inspect their recorded actions and Connection log. No match is predicted from configuration alone.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
				ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
					if uikit.SecondaryButton(c, "Review ignored hosts").Clicked() {
						d.open, a.ignoreOpen = false, true
					}
					if uikit.SecondaryButton(c, "Review ignored apps").Clicked() {
						d.open, a.ignoreAppsOpen = false, true
					}
					if uikit.SecondaryButton(c, "Review rules").Clicked() {
						d.open = false
						a.openRules()
					}
				})
			})
			hint := "End of diagnostics"
			if d.scroll.Y < d.scroll.MaxY-1 {
				hint = "Scroll for more diagnostics and review actions ↓"
			}
			ui.Text(c, hint).FontSize(uikit.TextMeta).TextColor(t.TextMuted)
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Proxy settings…").Clicked() {
					d.open = false
					a.settingsTab, a.settingsOpen = 1, true
				}
				if uikit.SecondaryButton(c, "HTTPS settings…").Clicked() {
					d.open = false
					a.settingsTab, a.settingsOpen = 2, true
				}
				if uikit.SecondaryButton(c, "Client setup…").Clicked() {
					d.open = false
					a.openClientSetup()
					if target, err := url.Parse(strings.TrimSpace(d.target)); err == nil && target.Scheme == "https" && target.Hostname() != "" && target.User == nil {
						a.clientUI.url = target.String()
					}
				}
				if uikit.SecondaryButton(c, "Close").Clicked() {
					d.open = false
				}
			})
		})
	})
}
