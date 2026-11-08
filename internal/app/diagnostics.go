package app

import (
	"cmp"
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// Connection diagnostics is the one place that answers "why don't I see this
// traffic": every other surface (a failed row's Explain, client and device
// setup, empty states) links here instead of keeping its own checks.
type captureDiagnostics struct {
	target string
	client int  // diagnoseMac, diagnoseDevice or diagnoseProcess
	flowID int  // the exchange Explain was opened from; 0 uses the latest match
	open   int  // the expanded step; diagnoseAuto follows the first failing one
	reveal bool // scroll the expanded step into view next frame, once
}

// open values besides a step index.
const (
	diagnoseAuto    = -1
	diagnoseNone    = -2
	diagnoseTraffic = 4 // the Actual traffic step, where Explain lands
)

const (
	diagnoseMac = iota
	diagnoseDevice
	diagnoseProcess
)

var (
	diagnosticsIcon = lucide.Stethoscope()
	checkPassIcon   = lucide.CircleCheck()
	checkFailIcon   = lucide.CircleX()
	checkWaitIcon   = lucide.CircleMinus()
)

type checkState int

const (
	checkWait checkState = iota // not checked, not reached, or not verifiable here
	checkPass
	checkFail
)

// diagnosticCheck is one step. fix runs only when its button is pressed:
// building the page never changes the proxy, trust store or rules.
type diagnosticCheck struct {
	title, result string
	state         checkState
	detail        []string
	fix           string
	run           func(c *ui.Context)
	failure       *flow // a recorded connection failure, explained in place
}

// openDiagnostics shows the page. Explain passes the failed exchange so the
// target and its evidence are that exchange's, not the latest for the host.
func (a *app) openDiagnostics(f *flow, client int) {
	d := &a.diagnostics
	d.client, d.flowID, d.open = client, 0, diagnoseAuto
	switch {
	case f != nil:
		d.target, d.flowID, d.open, d.reveal = cmp.Or(f.url, f.scheme+"://"+f.host+f.path), f.id, diagnoseTraffic, true
	case d.target == "":
		d.target = "https://example.com/"
		if cur, ok := a.current(); ok && cur.url != "" {
			d.target = cur.url
		}
	}
	a.page, a.sidebarChoice = pageDiagnostics, "diagnostics"
	a.refreshDiagnostics()
}

func (a *app) refreshDiagnostics() {
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

func (a *app) diagnosticChecks(t *ui.Theme, target *url.URL) []diagnosticCheck {
	d := &a.diagnostics
	running := a.srv != nil && a.srv.Running()
	checks := make([]diagnosticCheck, 0, 5)

	listener := diagnosticCheck{title: "Listener", fix: "Proxy settings…", run: func(*ui.Context) { a.openSettings(1) }}
	switch {
	case running:
		listener.state, listener.result, listener.fix = checkPass, "Listening at "+a.srv.Addr(), ""
	case a.errText != "":
		listener.state, listener.result = checkFail, a.errText
	default:
		listener.state, listener.result = checkFail, "Capture is stopped"
		listener.detail = []string{"A fixed port that another program holds keeps capture stopped; choose another port or clear it."}
	}
	checks = append(checks, listener)

	proxy := diagnosticCheck{title: "Client proxy"}
	switch d.client {
	case diagnoseMac:
		proxy.title = "System proxy"
		status, _ := a.systemProxyStatus(t)
		line, _ := a.systemProxyDetail(a.systemProxyTarget())
		proxy.result, proxy.detail = status, []string{line, "Apps may ignore the system proxy or use their own; a matching OS setting does not prove their traffic reaches this app."}
		switch s := &a.systemProxy; {
		case s.checking || s.applying || !s.checked:
		case status == "HTTP and HTTPS point at this app":
			proxy.state = checkPass
		default:
			proxy.state, proxy.fix = checkFail, "Turn on system proxy"
			proxy.run = func(*ui.Context) { a.setSystemProxy(true) }
		}
	case diagnoseDevice:
		proxy.title, proxy.fix, proxy.run = "Device access", "Device setup…", func(*ui.Context) { a.openDevices() }
		if g := a.deviceUI.gateway; g == nil {
			proxy.state, proxy.result = checkFail, "Device access is off"
		} else {
			status := g.Status()
			proxy.result = "Devices connect to " + g.Addr()
			proxy.detail = []string{"Last proxy client: " + cmp.Or(status.LastProxyClient, "not seen yet"),
				"Last certificate download: " + cmp.Or(status.LastCertificateClient, "not seen yet")}
			if status.LastProxyClient != "" {
				proxy.state, proxy.fix = checkPass, ""
			} else {
				proxy.detail = append(proxy.detail, "No device seen? Check the device's IP in the allow list, VPNs, the firewall and Wi-Fi client isolation.")
			}
		}
	default:
		proxy.title, proxy.fix, proxy.run = "Process proxy", "Client setup…", func(*ui.Context) { a.openClientSetup() }
		proxy.result = "Set by the process itself; not checkable from here"
		proxy.detail = []string{"Client setup has an example for cURL, Node.js, Python and Go that sets the proxy and CA for one process only."}
	}
	checks = append(checks, proxy)

	https := target.Scheme == "https"
	cert := diagnosticCheck{title: "Certificate"}
	switch {
	case !https:
		cert.result = "Not needed for HTTP"
	case d.client == diagnoseMac:
		status, _ := a.certificateStatus(t)
		cert.result = status
		switch s := &a.certificate; {
		case s.checking || s.installing:
		case s.cert != nil && s.err == nil:
			cert.state = checkPass
		default:
			cert.state, cert.fix = checkFail, "Install and trust CA…"
			cert.run = func(*ui.Context) { a.checkCertificate(true) }
			cert.detail = []string{"Clients with their own trust store (Firefox, Java, Node.js, Python) need the CA added separately."}
		}
	default:
		cert.result = "Trust on the client can't be checked from here; Actual traffic shows the outcome"
		if a.srv != nil {
			fingerprint := fmt.Sprintf("%X", sha256.Sum256(a.srv.CACertificate()))
			cert.detail = []string{"CA SHA-256 " + fingerprint}
			cert.fix = "Copy CA fingerprint"
			cert.run = func(c *ui.Context) {
				c.Services().WriteClipboard(fingerprint)
				a.done("Copied CA fingerprint")
			}
		}
	}
	checks = append(checks, cert)

	decrypt := diagnosticCheck{title: "HTTPS decryption"}
	switch {
	case !https:
		decrypt.result = "Not needed for HTTP"
	case a.inspectionConfig().Decrypts(target.Hostname()):
		decrypt.state, decrypt.result = checkPass, target.Hostname()+" is decrypted on new connections"
	default:
		decrypt.state, decrypt.result = checkFail, target.Hostname()+" is not in the decryption list"
		decrypt.fix = "Decrypt this host"
		host := target.Hostname()
		decrypt.run = func(*ui.Context) { a.decryptHost(host) }
	}
	checks = append(checks, decrypt)

	checks = append(checks, a.trafficCheck(target, https))
	return checks
}

// trafficCheck is what this live session recorded for the target's origin:
// the exchange Explain came from, or the latest one.
func (a *app) trafficCheck(target *url.URL, https bool) diagnosticCheck {
	check := diagnosticCheck{title: "Actual traffic"}
	var latest, decrypted *flow
	matched := 0
	for i := range a.flows {
		f := &a.flows[i]
		if f.id == a.diagnostics.flowID {
			latest = f // the exchange Explain came from, live or not
		}
		captured, err := url.Parse(f.url)
		if !f.live || err != nil || diagnosticOrigin(captured) != diagnosticOrigin(target) {
			continue
		}
		matched++
		if a.diagnostics.flowID == 0 {
			latest = f
		}
		if f.kind != "connect" && f.reqHeaders != "" {
			decrypted = f
		}
	}
	if a.diagnostics.flowID != 0 && latest == nil {
		check.result = fmt.Sprintf("#%d is no longer in the live capture", a.diagnostics.flowID)
		return check
	}
	inspect := func(f *flow) {
		id := f.id
		check.fix, check.run = fmt.Sprintf("Inspect #%d", id), func(*ui.Context) { a.inspectCapture(id) }
	}
	switch {
	case latest == nil:
		check.result = "Waiting for a request to " + target.Host
		check.detail = []string{"Nothing recorded does not prove a failure: the client may not have sent a request, bypassed the proxy, or matched an ignored host or app."}
		if a.srv != nil {
			check.detail = append(check.detail, fmt.Sprintf("Ignored hosts: %d · ignored apps: %d", len(a.srv.Ignore()), len(a.srv.IgnoreApps())))
		}
	case latest.conn.error != "":
		check.state, check.failure = checkFail, latest
		check.result = fmt.Sprintf("#%d · %s failed", latest.id, cmp.Or(latest.conn.errorStage, "Connection"))
		inspect(latest)
	case https && latest.kind == "connect" && decrypted == nil:
		check.state = checkFail
		check.result = fmt.Sprintf("#%d · encrypted tunnel only; no decrypted request yet", latest.id)
		check.detail = []string{"Reconnect the client after enabling decryption; open connections keep their old policy."}
		inspect(latest)
	default:
		f := latest
		if decrypted != nil && a.diagnostics.flowID == 0 {
			f = decrypted
		}
		check.state = checkPass
		check.result = fmt.Sprintf("#%d · %s %s · %d", f.id, f.method, f.path, f.status)
		inspect(f)
	}
	if matched > 1 {
		check.detail = append(check.detail, fmt.Sprintf("%d live records for this origin, from any client.", matched))
	}
	return check
}

func (a *app) diagnosticsView(c *ui.Context, t *ui.Theme) {
	d := &a.diagnostics
	ui.Scroll(c).Fill().Padding(t.Space(5), t.Space(6)).Gap(t.Space(3)).Children(func() {
		uikit.Heading(c, locales.ConnectionDiagnostics())
		uikit.Hint(c, locales.ChecksWhatLiesBetweenA())
		ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Wrap().Children(func() {
			if uikit.Segmented(c, &d.client, locales.ThisMac(), locales.PhoneOrTablet(), locales.OneProcess()).Changed() {
				d.open = diagnoseAuto
			}
			if uikit.Input(c, &d.target).Label(locales.TargetUrl()).Placeholder("https://api.example.com/").Grow(1).MinWidth(240).Changed() {
				d.flowID, d.open = 0, diagnoseAuto
			}
			if uikit.SecondaryButton(c, locales.CheckAgain()).Disabled(a.systemProxy.checking || a.systemProxy.applying || a.certificate.checking).Clicked() {
				a.refreshDiagnostics()
			}
		})
		u, err := url.Parse(strings.TrimSpace(d.target))
		if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
			ui.Text(c, locales.EnterAnHttpOrHttps()).FontSize(uikit.TextBody).TextColor(t.Warning)
			return
		}
		ui.Divider(c)
		checks := a.diagnosticChecks(t, u)
		first := -1 // the first failing step: its fix is the one to press
		for i, check := range checks {
			if check.state == checkFail {
				first = i
				break
			}
		}
		open := d.open
		if open == diagnoseAuto {
			open = first
		}
		for i, check := range checks {
			a.diagnosticRow(c, t, i, check, i == open, i == first)
		}
		uikit.Meta(c, locales.ReadOnlyUntilYouPress())
	})
}

func (a *app) diagnosticRow(c *ui.Context, t *ui.Theme, i int, check diagnosticCheck, expanded, primary bool) {
	d := &a.diagnostics
	icon, color := checkWaitIcon, t.TextMuted
	switch check.state {
	case checkPass:
		icon, color = checkPassIcon, t.Success
	case checkFail:
		icon, color = checkFailIcon, t.Danger
	}
	ui.Column(c.Key(check.title)).Gap(t.Space(2)).Children(func() {
		ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon).Size(20, 20).TextColor(color)
			row := uikit.RowButton(c, check.title).Grow(1).MinWidth(0).Tooltip(locales.ShowDetails())
			row.Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, strconv.Itoa(i+1)+".  "+check.title).FontSize(uikit.TextBody).FontWeight(600)
					resultColor := t.TextMuted
					if check.state == checkFail {
						resultColor = t.Danger
					}
					ui.Text(c, check.result).FontSize(uikit.TextBody).TextColor(resultColor).SingleLine().Tooltip(check.result)
				})
			})
			if row.Clicked() {
				d.open, d.reveal = i, true
				if expanded {
					d.open = diagnoseNone
				}
			}
			if check.fix != "" {
				button := uikit.SecondaryButton
				if primary {
					button = uikit.PrimaryButton
				}
				if button(c, check.fix).Clicked() {
					check.run(c)
				}
			}
		})
		if !expanded || len(check.detail) == 0 && check.failure == nil {
			return
		}
		details := ui.Column(c).Margin(0, 0, 0, 20+t.Space(3)).Gap(t.Space(2))
		if d.reveal {
			c.CloseToast("") // an earlier toast would cover the fix buttons
			details.ScrollIntoView()
			d.reveal = false
		}
		details.Children(func() {
			for _, line := range check.detail {
				uikit.Hint(c, line).Selectable()
			}
			if f := check.failure; f != nil {
				a.explainFailure(c, t, *f)
			}
		})
	})
	ui.Divider(c)
}

// explainFailure is a recorded connection failure in words. Causes are
// possibilities; the observed error is the only fact.
func (a *app) explainFailure(c *ui.Context, t *ui.Theme, f flow) {
	e := f.conn.explanation()
	ui.Column(c).Padding(t.Space(3)).Gap(t.Space(2)).Radius(uikit.RadiusPanel).Background(t.Danger.Alpha(0.06)).Border(1, t.Danger.Alpha(0.2)).Children(func() {
		uikit.Label(c, "Observed failure · "+cmp.Or(f.conn.errorStage, "unknown stage"))
		ui.Text(c, e.title).FontSize(uikit.TextBody).FontWeight(600)
		ui.Text(c, f.conn.error).Font(uikit.FontMono).FontSize(uikit.TextMeta).TextColor(t.Danger).Selectable()
		uikit.Hint(c, e.summary)
		ui.Text(c, locales.PossibleCausesNotConfirmed()).FontSize(uikit.TextBody).FontWeight(600)
		uikit.Hint(c, e.causes)
		ui.Text(c, locales.WhatToTry()).FontSize(uikit.TextBody).FontWeight(600)
		uikit.Body(c, e.steps)
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			uikit.Meta(c, locales.ReviewUrlsAddressesAndRaw()).Grow(1)
			if uikit.SecondaryButton(c, locales.CopyDiagnostics()).Clicked() {
				c.Services().WriteClipboard(f.connectionDiagnostics())
				a.done("Copied connection diagnostics")
			}
		})
	})
}
