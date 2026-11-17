package app

import (
	"cmp"
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/system"
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
	open   int  // the step whose failure evidence is open; diagnoseAuto follows the first failing one
	reveal bool // scroll the expanded step into view next frame, once
	// demoRunning and demoAddr let the gallery draw the all-pass plate
	// without starting a listener. The app always uses a.srv.
	demoRunning bool
	demoAddr    string
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
	checkWarnIcon   = lucide.TriangleAlert()
	checkWaitIcon   = lucide.CircleMinus()
)

type checkState int

const (
	checkWait checkState = iota // not checked, not reached, or not verifiable here
	checkPass
	checkWarn // a real problem that is not a plain failure, such as a stale system proxy
	checkFail
)

// diagnosticCheck is one step. fix runs only when its button is pressed:
// building the page never changes the proxy, trust store or rules.
type diagnosticCheck struct {
	title, result string
	// report is the result for the diagnostics bundle when result names
	// request content, such as a URL path. Empty uses result.
	report  string
	state   checkState
	detail  []string
	fix     string
	run     func(c *ui.Context)
	failure *flow // a recorded connection failure, explained in place
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
	running := a.diagnostics.demoRunning || a.srv != nil && a.srv.Running()
	addr := a.diagnostics.demoAddr
	if addr == "" && a.srv != nil {
		addr = a.srv.Addr()
	}
	checks := make([]diagnosticCheck, 0, 5)

	listener := diagnosticCheck{title: locales.DiagListener(), fix: "Proxy settings…", run: func(*ui.Context) { a.openSettings(1) }}
	switch {
	case running:
		listener.state, listener.result, listener.fix = checkPass, locales.ListeningAddr(cmp.Or(addr, locales.ThisApp())), ""
	case a.errText != "":
		listener.state, listener.result = checkFail, a.errText
		listener.detail = []string{"A fixed port that another program holds keeps capture stopped; choose another port or clear it."}
	default:
		listener.state, listener.result = checkFail, "Capture is stopped"
		listener.fix = "Start capture"
		listener.run = func(*ui.Context) { go a.startCapture() }
		listener.detail = []string{"Start capture to listen again, or open Proxy settings to change the port."}
	}
	checks = append(checks, listener)

	proxy := diagnosticCheck{title: locales.DiagClientProxy()}
	switch d.client {
	case diagnoseMac:
		proxy.title = locales.DiagSystemProxy()
		status, _ := a.systemProxyStatus(t)
		line, _ := a.systemProxyDetail(a.systemProxyTarget())
		proxy.result, proxy.detail = status, []string{line, "Apps may ignore the system proxy or use their own; a matching OS setting does not prove their traffic reaches this app."}
		switch s := &a.systemProxy; {
		case s.checking || s.applying || !s.checked:
			proxy.state = checkWait
		case !running:
			// The OS setting can outlive the listener after a crash. Do
			// not call that a pass: traffic would go to a closed port.
			if s.check.Status == system.ProxyMatches {
				proxy.state = checkWarn
				proxy.result = "Points at this app, but capture is stopped"
				proxy.detail = append([]string{"Traffic is sent to a closed port until capture starts or the system proxy is turned off."}, proxy.detail...)
				proxy.fix = "Turn off system proxy"
				proxy.run = func(*ui.Context) { a.setSystemProxy(false) }
			} else {
				proxy.state = checkWait
				proxy.detail = append(proxy.detail, "The system proxy is not changed while capture is stopped.")
			}
		case s.check.Status == system.ProxyMatches:
			proxy.state = checkPass
		default:
			proxy.state, proxy.fix = checkFail, "Turn on system proxy"
			proxy.run = func(*ui.Context) { a.setSystemProxy(true) }
		}
	case diagnoseDevice:
		proxy.title, proxy.fix, proxy.run = locales.DiagDeviceAccess(), "Device setup…", func(*ui.Context) { a.openDevices() }
		if !running {
			proxy.state = checkWait
			proxy.result = "Waiting for capture to start"
			proxy.detail = []string{"Device access is checked while capture is running."}
		} else if g := a.deviceUI.gateway; g == nil {
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
		proxy.title, proxy.fix, proxy.run = locales.DiagProcessProxy(), "Client setup…", func(*ui.Context) { a.openClientSetup() }
		proxy.result = "Set by the process itself; not checkable from here"
		proxy.detail = []string{"Client setup has an example for cURL, Node.js, Python and Go that sets the proxy and CA for one process only."}
	}
	checks = append(checks, proxy)

	https := target.Scheme == "https"
	cert := diagnosticCheck{title: locales.DiagCertificate()}
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
				a.done(locales.CopiedCaFingerprint())
			}
		}
	}
	checks = append(checks, cert)

	decrypt := diagnosticCheck{title: locales.HttpsDecryption()}
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
	check := diagnosticCheck{title: locales.DiagActualTraffic()}
	running := a.diagnostics.demoRunning || a.srv != nil && a.srv.Running()
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
	if !running {
		// The count belongs to this run, but a failure that Explain was
		// opened from is still the recorded evidence for the target.
		check.state = checkWait
		check.result = "Waiting for capture to start"
		check.detail = []string{"Actual traffic is counted only while capture is running in this session; imported and cleared records do not count."}
		if latest != nil && latest.conn.error != "" {
			check.failure = latest
			inspect(latest)
		}
		return check
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
		check.report = fmt.Sprintf("#%d · %s · %d", f.id, f.method, f.status)
		inspect(f)
	}
	if matched > 1 {
		check.detail = append(check.detail, fmt.Sprintf("%d live records for this origin, from any client.", matched))
	}
	return check
}

func (a *app) diagnosticsView(c *ui.Context, t *ui.Theme) {
	d := &a.diagnostics
	ui.Scroll(c).Fill().Padding(t.Space(uikit.Space2XL)).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(uikit.SettingsWidth).Gap(t.Space(uikit.SpaceXL)).Children(func() {
			ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Children(func() {
				uikit.Display(c, locales.ConnectionDiagnostics())
				uikit.Hint(c, locales.ChecksWhatLiesBetweenA())
			})
			ui.Row(c).Gap(t.Space(uikit.SpaceL)).AlignItems(ui.Center).Wrap().Children(func() {
				if uikit.Segmented(c, &d.client, locales.ThisMac(), locales.PhoneOrTablet(), locales.OneProcess()).Changed() {
					d.open = diagnoseAuto
				}
			})
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				if uikit.Input(c, &d.target).Label(locales.TargetUrl()).Placeholder("https://api.example.com/").Grow(1).MinWidth(uikit.WidthFieldWide).Changed() {
					d.flowID, d.open = 0, diagnoseAuto
				}
				if uikit.SecondaryButtonBusy(c, locales.CheckAgain(), a.systemProxy.checking || a.systemProxy.applying || a.certificate.checking).Clicked() {
					a.refreshDiagnostics()
				}
				if uikit.SecondaryButton(c, locales.ExportDiagnostics()).Clicked() {
					if u, err := url.Parse(strings.TrimSpace(d.target)); err == nil {
						a.chooseDiagnosticsExport(t, u)
					}
				}
			})
			u, err := url.Parse(strings.TrimSpace(d.target))
			if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
				uikit.Body(c, locales.EnterAnHttpOrHttps()).TextColor(t.Warning)
				return
			}
			checks := a.diagnosticChecks(t, u)
			first := -1 // the first problem step: its fix is the one to press
			for i, check := range checks {
				if check.state == checkFail || check.state == checkWarn {
					first = i
					break
				}
			}
			open := d.open
			if open == diagnoseAuto {
				open = first
			}
			for i, check := range checks {
				a.diagnosticRow(c, t, i, check, i == first, i == open)
			}
		})
	})
}

func (a *app) diagnosticRow(c *ui.Context, t *ui.Theme, i int, check diagnosticCheck, primary, expanded bool) {
	d := &a.diagnostics
	icon, color := checkWaitIcon, t.TextMuted
	switch check.state {
	case checkPass:
		icon, color = checkPassIcon, t.Success
	case checkWarn:
		icon, color = checkWarnIcon, t.Warning
	case checkFail:
		icon, color = checkFailIcon, t.Danger
	}
	var tipParts []string
	if len(check.detail) > 0 {
		tipParts = append(tipParts, strings.Join(check.detail, "\n"))
	}
	if f := check.failure; f != nil {
		e := f.conn.explanation()
		tipParts = append(tipParts, e.title, e.summary, "Possible causes: "+e.causes, "What to try: "+e.steps, f.conn.error)
	}
	tip := strings.Join(tipParts, "\n\n")
	expandable := check.failure != nil || len(check.detail) > 0
	uikit.SettingsCard(c, "", func() {
		row := uikit.RowButton(c, check.title).FillWidth().Height(uikit.SettingsRowHeight).Padding(0, t.Space(uikit.SpaceXL)).Gap(t.Space(uikit.SpaceL))
		row.Children(func() {
			// A row that does not expand keeps the disclosure's slot empty, so
			// its status mark and title sit where the other rows' do.
			if expandable {
				arrow := statusMoreIcon
				if expanded {
					arrow = chevronIcon
				}
				ui.Icon(c, arrow).Size(uikit.IconInline, uikit.IconInline).TextColor(t.TextMuted)
			} else {
				ui.Box(c).Size(uikit.IconInline, uikit.IconInline)
			}
			ui.Icon(c, icon).Size(uikit.IconLarge, uikit.IconLarge).TextColor(color)
			ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
				uikit.Body(c, strconv.Itoa(i+1)+".  "+check.title).FontWeight(500)
				status := uikit.Small(c, check.result).TextColor(color).SingleLine()
				if tip != "" {
					uikit.Tip(c, status, tip)
				}
			})
			if check.fix != "" {
				button := uikit.SecondaryButton
				if primary {
					button = uikit.PrimaryButton
				}
				if button(c, check.fix).Clicked() && check.run != nil {
					check.run(c)
				}
			}
		})
		if row.Clicked() && expandable {
			if expanded {
				d.open = diagnoseNone
			} else {
				d.open, d.reveal = i, true
			}
		}
		if !expanded || !expandable {
			return
		}
		details := ui.Column(c).Padding(0, t.Space(uikit.SpaceXL), t.Space(uikit.SpaceL), t.Space(uikit.SpaceXL)).Gap(t.Space(uikit.SpaceM))
		if d.reveal {
			c.CloseToast("") // an earlier toast would cover the fix buttons
			details.ScrollIntoView()
			d.reveal = false
		}
		details.Children(func() {
			if check.failure != nil {
				a.explainFailure(c, t, *check.failure)
				return
			}
			for _, line := range check.detail {
				uikit.Body(c, line)
			}
		})
	})
}

// explainFailure is a recorded connection failure in words. Causes are
// possibilities; the observed error is the only fact.
func (a *app) explainFailure(c *ui.Context, t *ui.Theme, f flow) {
	e := f.conn.explanation()
	ui.Column(c).Padding(t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Radius(uikit.RadiusPanel).Background(t.Danger.Alpha(0.06)).Border(1, t.Danger.Alpha(0.2)).Children(func() {
		uikit.Label(c, locales.ObservedFailure(cmp.Or(f.conn.errorStage, locales.UnknownStage())))
		uikit.Body(c, e.title).FontWeight(500)
		uikit.DataMeta(c, f.conn.error).TextColor(t.Danger).Selectable()
		uikit.Hint(c, e.summary)
		uikit.Body(c, locales.PossibleCausesNotConfirmed()).FontWeight(500)
		uikit.Hint(c, e.causes)
		uikit.Body(c, locales.WhatToTry()).FontWeight(500)
		uikit.Body(c, e.steps)
		ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			uikit.Meta(c, locales.ReviewUrlsAddressesAndRaw()).Grow(1)
			if uikit.SecondaryButton(c, locales.CopyDiagnostics()).Clicked() {
				c.Services().WriteClipboard(f.connectionDiagnostics())
				a.done(locales.CopiedConnectionDiagnostics())
			}
		})
	})
}
