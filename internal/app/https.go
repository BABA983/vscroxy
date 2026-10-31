package app

import (
	"cmp"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/system"
	"github.com/egoist/mygo/ui"
)

type certificateResult struct {
	cert       *x509.Certificate
	err        error
	installErr error
}

// Only the UI accesses this state. Workers return results through pending.
type certificateSetup struct {
	certificateResult
	checked    bool
	checking   bool
	installing bool
	pending    chan certificateResult
}

func (a *app) checkCertificate(install bool) {
	s := &a.certificate
	if s.checking {
		return
	}
	if s.pending == nil {
		s.pending = make(chan certificateResult, 1)
	}
	s.checking, s.installing = true, install
	previousError := s.installErr
	if install {
		s.installErr = nil
	}
	path, pending := a.caPath, s.pending
	go func() {
		var result certificateResult
		if install {
			result.installErr = system.InstallCA(path)
		} else {
			result.installErr = previousError
		}
		result.cert, result.err = system.CheckCA(path)
		if result.err == nil {
			result.installErr = nil
		}
		pending <- result
		a.invalidate()
	}()
}

func (a *app) drainCertificate() {
	s := &a.certificate
	select {
	case result := <-s.pending:
		if s.installing {
			switch {
			case errors.Is(result.installErr, system.ErrInstallCancelled):
				a.setMessage("Certificate installation cancelled; system trust not verified")
			case result.installErr != nil:
				a.setMessage("Install certificate: " + result.installErr.Error())
			case result.err != nil:
				a.setMessage("Installation finished; system trust not verified")
			default:
				a.setMessage("CA trusted by this system; client trust may differ")
			}
		}
		s.certificateResult = result
		s.checked, s.checking, s.installing = true, false, false
	default:
	}
}

func (a *app) certificateStatus(t *ui.Theme) (string, ui.Color) {
	s := &a.certificate
	if !s.checked && !s.checking {
		a.checkCertificate(false)
	}
	switch {
	case s.installing:
		return "Installing certificate…", t.TextMuted
	case s.checking:
		return "Checking system trust…", t.TextMuted
	case s.cert == nil:
		return "Local CA unavailable", t.Warning
	case s.err != nil:
		return "Trust not verified on this system", t.Warning
	default:
		return "Trusted by this system", t.Success
	}
}

func (a *app) httpsSettings(c *ui.Context, t *ui.Theme) {
	status, color := a.certificateStatus(t)
	s := &a.certificate
	uikit.Heading(c, "HTTPS inspection")
	if a.srv != nil && a.srv.Running() {
		ui.Text(c, "Proxy listening · "+a.srv.Addr()).FontSize(uikit.TextBody).TextColor(t.Success)
	} else {
		uikit.Hint(c, "Capture is not running")
	}
	uikit.Hint(c, "Trusting the CA only makes decryption possible. Domain and app rules expose request and response headers — including Cookie and Set-Cookie — and bodies. Connections not covered by a rule stay encrypted CONNECT tunnels.")
	ui.Divider(c)
	ui.Column(c).Gap(t.Space(3)).Children(func() {
		ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
			ui.Text(c, "Certificate").FontSize(uikit.TextBody).Bold().Grow(1)
			ui.Text(c, status).FontSize(uikit.TextBody).Bold().TextColor(color)
		})
		if s.cert != nil {
			uikit.Body(c, cmp.Or(s.cert.Subject.CommonName, s.cert.Subject.String()))
			uikit.Meta(c, "Valid: "+s.cert.NotBefore.Local().Format("2006-01-02")+" → "+s.cert.NotAfter.Local().Format("2006-01-02"))
			fingerprint := fmt.Sprintf("%X", sha256.Sum256(s.cert.Raw))
			uikit.Meta(c, "SHA-256: "+fingerprint).Font(uikit.FontMono).SingleLine().Tooltip(fingerprint).Selectable()
		}
		uikit.Hint(c, "Trust this CA on the requesting device or in the requesting application. Separate trust stores and certificate pinning may need client-side configuration.")
		if runtime.GOOS == "windows" {
			uikit.Hint(c, "Installation trusts this CA for the current Windows user, not other accounts or services.")
		}
		if s.installing {
			uikit.Hint(c, "If the system asks for authorization, approve it to continue. Trust will be checked afterwards.")
		}
		if s.checked && !s.checking && s.err != nil {
			problem := s.err.Error()
			if errors.Is(s.err, os.ErrNotExist) {
				problem = "CA file not found. Start capture, then check again."
			}
			ui.Text(c, problem).FontSize(uikit.TextMeta).TextColor(t.Warning).SingleLine().Tooltip(s.err.Error())
		}
		if s.installErr != nil {
			if errors.Is(s.installErr, system.ErrInstallCancelled) {
				uikit.Hint(c, "Installation cancelled. You can try again when ready.")
			} else {
				ui.Text(c, "Installation failed: "+s.installErr.Error()).FontSize(uikit.TextMeta).TextColor(t.Danger).MaxLines(2).Tooltip(s.installErr.Error())
				switch runtime.GOOS {
				case "darwin":
					uikit.Hint(c, "Manual fallback: use Show certificate to locate the public CA, import it in Keychain Access, then set Trust → When using this certificate to Always Trust. Check again afterwards.")
				case "windows":
					uikit.Hint(c, "Manual fallback: use Show certificate and import the public CA into Trusted Root Certification Authorities for the current user. Check again afterwards.")
				default:
					uikit.Hint(c, "Manual fallback: use Show certificate and import the public CA using your distribution's trust-store tools. Check again afterwards.")
				}
			}
		}
		ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
			if uikit.PrimaryButton(c, "Install & Trust…").Tooltip("Trust the local CA on this computer; approve system authorization if requested").Disabled(s.checking || s.cert == nil || s.checked && s.err == nil).Clicked() {
				a.checkCertificate(true)
			}
			if uikit.SecondaryButton(c, "Check again").Disabled(s.checking).Clicked() {
				a.checkCertificate(false)
			}
			if uikit.SecondaryButton(c, "Show certificate").Tooltip(a.caPath).Disabled(a.caPath == "").Clicked() {
				a.revealCA()
			}
		})
	})
	ui.Divider(c)
	ui.Column(c).Gap(t.Space(3)).Children(func() {
		ui.Text(c, "Decryption rules").FontSize(uikit.TextBody).Bold()
		domains := splitHosts(a.sslHosts)
		summary := "No domains enabled — HTTPS stays an encrypted tunnel"
		if a.sslAll {
			summary = "Every domain enabled for new connections"
		} else if len(domains) > 0 {
			summary = strings.Join(domains, ", ")
		}
		if !a.sslAll && len(a.sslApps) > 0 {
			summary = fmt.Sprintf("%d domain rules · %d local executable rules", len(domains), len(a.sslApps))
		}
		uikit.Body(c, summary).MaxLines(2).Tooltip(summary)
		switch {
		case a.sslAll:
			uikit.Hint(c, "All new HTTPS connections are eligible for inspection. Client trust and TLS compatibility still apply.")
		case len(a.sslApps) > 0:
			uikit.Hint(c, "Listed domains or exact local executables are eligible. Other clients stay encrypted unless a domain rule also matches.")
		case len(domains) > 0:
			uikit.Hint(c, "Only these domains and their subdomains are decrypted. Every other host stays an encrypted tunnel.")
		default:
			uikit.Hint(c, "Enable a domain here, or every domain explicitly, to inspect its headers, cookies, and bodies.")
		}
		uikit.Hint(c, "Trusting the CA and enabling domain decryption are separate steps.")
		ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
			if uikit.SecondaryButton(c, "Manage domains…").Clicked() {
				a.settingsOpen, a.sslOpen = false, true
			}
			if uikit.SecondaryButton(c, "TLS key log…").Clicked() {
				a.settingsOpen = false
				a.openKeyLog()
			}
		})
	})
	uikit.Meta(c, "After changing HTTPS setup, establish a new connection and retry the request.")
}

func (a *app) inspectionConfig() capture.SSLConfig {
	return capture.SSLConfig{All: a.sslAll, Hosts: splitHosts(a.sslHosts), Apps: slices.Clone(a.sslApps)}
}

func (a *app) appInspectionEnabled(path string) bool {
	return path != "" && slices.ContainsFunc(a.sslApps, func(app capture.SSLApp) bool { return app.Path == path })
}

// A sidebar name may represent more than one executable. Never silently give
// all of them a permission intended for one, or infer a parent/Helper group.
func (a *app) capturedApps(name string) []capture.SSLApp {
	var apps []capture.SSLApp
	for _, f := range a.flows {
		if f.process == name && filepath.IsAbs(f.processPath) && !slices.ContainsFunc(apps, func(app capture.SSLApp) bool { return app.Path == f.processPath }) {
			apps = append(apps, capture.SSLApp{Name: name, Path: f.processPath})
		}
	}
	slices.SortFunc(apps, func(x, y capture.SSLApp) int { return strings.Compare(x.Path, y.Path) })
	return apps
}

func (a *app) setAppInspection(app capture.SSLApp, enabled bool) {
	if !filepath.IsAbs(app.Path) {
		a.setMessage("HTTPS app rule unavailable: a local executable path is required")
		return
	}
	app.Name = cmp.Or(app.Name, filepath.Base(app.Path))
	cfg := a.inspectionConfig()
	cfg.Apps = slices.DeleteFunc(cfg.Apps, func(rule capture.SSLApp) bool { return rule.Path == app.Path })
	if enabled {
		cfg.Apps = append(cfg.Apps, app)
	}
	if a.saveSSL(cfg) {
		message := "App rule disabled for " + app.Name + "; domain and global rules still apply to new connections"
		if enabled {
			message = "App rule enabled for " + app.Name + "; reconnect the client · decryption not yet verified"
		}
		a.setMessage(message)
	}
}

func (a *app) appInspectionMenu(m *ui.Menu, app capture.SSLApp) {
	label := "Enable HTTPS for " + cmp.Or(app.Name, "this app")
	enabled := a.appInspectionEnabled(app.Path)
	if enabled {
		label = "Disable HTTPS for " + app.Name
	}
	if m.Item(label).Checked(enabled).Disabled(!filepath.IsAbs(app.Path)).Chosen() {
		a.setAppInspection(app, !enabled)
	}
}

// connectPanel describes this historical tunnel separately from today's rules:
// what happened, what is configured, and what to do next, in that order.
func (a *app) connectPanel(c *ui.Context, f flow) {
	t := c.Theme()
	width, height := c.Size()
	trust, trustColor := a.certificateStatus(t)
	domainEnabled := a.inspectionConfig().Decrypts(f.host)
	appEnabled := a.appInspectionEnabled(f.processPath)
	e := f.conn.explanation()
	failed := f.conn.error != ""
	// The broader executable permission widens inspection; it never repairs
	// the failure on screen, so it stays a quiet link below the actions.
	broadScope := !appEnabled && !a.sslAll && filepath.IsAbs(f.processPath)
	inspection := func() {
		padding, gap := float32(16), float32(10)
		if height <= 760 {
			padding, gap = 10, 6
		}
		// One scroll: at a short window the whole explanation moves, rather
		// than a sliver of configuration scrolling inside it. A detail panel
		// reads in a column of a comfortable measure, not the full width of a
		// wide window.
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).MaxWidth(880).Padding(padding, t.Space(4)).Gap(gap).Children(func() {
				title := "Encrypted tunnel"
				if failed {
					title = e.title
				}
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, lockIcon).Size(uikit.IconLarge, uikit.IconLarge).TextColor(t.Text)
					uikit.Heading(c, title).Label(title + " — not individual requests")
				})
				summary := "This connection is encrypted. CONNECT is not a decrypted HTTP response."
				if failed {
					summary = e.summary
				}
				uikit.Hint(c, summary)
				if failed {
					// What happened: the stage and the raw error, copyable.
					ui.Column(c).FillWidth().Padding(t.Space(2), t.Space(2)).Gap(t.Space(1)).Radius(uikit.RadiusPanel).Background(t.Danger.Alpha(0.06)).Border(1, t.Danger.Alpha(0.2)).Children(func() {
						ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
							uikit.Label(c, "Recorded failure · "+cmp.Or(f.conn.errorStage, "unknown stage")).Grow(1)
							if uikit.ToolbarButton(c, "Copy error", "Copy the recorded error", false).Height(uikit.HeightCompact).Gap(t.Space(1)).Children(func() {
								ui.Icon(c, copyIcon).Size(uikit.IconInline, uikit.IconInline)
								ui.Text(c, "Copy").FontSize(uikit.TextMeta)
							}).Clicked() {
								c.Services().WriteClipboard(f.conn.error)
								a.setMessage("Copied the recorded error")
							}
						})
						ui.Text(c, f.conn.error).Font(uikit.FontMono).FontSize(uikit.TextMeta).TextColor(t.Danger).Selectable().SingleLine().Tooltip(f.conn.error)
					})
				}
				// What to do next. A failure is read before anything is
				// reconfigured, so the record leads and enabling inspection
				// is an independent configuration step; a healthy tunnel
				// still leads with the rule that would decrypt it.
				ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
					record := func(primary bool) {
						const tip = "Open this exchange's Connection tab: the recorded log and addresses"
						var button ui.Element
						if primary {
							button = uikit.PrimaryButton(c, "View connection record").Tooltip(tip)
						} else {
							button = uikit.SecondaryButton(c, "View connection record").Tooltip(tip)
						}
						if button.Clicked() {
							a.inspect.pane = inspectorConnection
						}
					}
					enable := func(primary bool) {
						var button ui.Element
						if primary {
							button = uikit.PrimaryButton(c, "Inspect "+hostOnly(f.host)).Label("Enable for " + f.host).
								Tooltip("Enable this hostname and subdomains on new connections; client CA trust is still required")
							if !a.workbench.sideBySide {
								button.MaxWidth(260)
							}
						} else {
							button = uikit.SecondaryButton(c, "Inspect "+hostOnly(f.host)).Label("Enable for " + f.host).
								Tooltip("Configuration for new connections; a recorded failure stays as it was")
						}
						if button.Clicked() {
							a.decryptHost(f.host)
						}
					}
					switch {
					case failed:
						// Read the evidence first; enabling inspection is a
						// configuration step, not the fix for this failure.
						record(true)
						if !domainEnabled {
							enable(false)
						}
					default:
						if !domainEnabled {
							enable(true)
						}
						record(false)
					}
					if uikit.SecondaryButton(c, "Certificate setup…").Tooltip("Check or install the local CA; this client may still need its own trust or a debug build").Clicked() {
						a.settingsTab, a.settingsOpen = 2, true
						a.checkCertificate(false)
					}
				})
				// Known configuration, secondary: today's rules never
				// explain a tunnel that was recorded earlier.
				uikit.Label(c, "Known configuration")
				if domainEnabled {
					ui.Text(c, "Domain rule enabled · reconnect to inspect new requests").FontSize(uikit.TextMeta).TextColor(t.Text)
				} else {
					uikit.Meta(c, "Domain decryption disabled")
				}
				if appEnabled {
					ui.Text(c, "App rule enabled · reconnect to inspect new requests").FontSize(uikit.TextMeta).Tooltip(f.processPath)
				} else if !filepath.IsAbs(f.processPath) {
					uikit.Meta(c, "App rule unavailable · local executable identity not recorded")
				}
				ui.Text(c, trust).FontSize(uikit.TextMeta).TextColor(trustColor).
					Tooltip("System trust is not proof that this client accepts the proxy's CA")
				if broadScope {
					ui.Row(c).Children(func() {
						if uikit.ToolbarButton(c, "Inspect all domains from "+f.process, "Wider scope for new connections from "+f.processPath+"; not a fix for this failure", false).
							Height(uikit.HeightCompact).Padding(0, t.Space(1)).Children(func() {
							ui.Text(c, "Inspect all domains from \""+f.process+"\"…").FontSize(uikit.TextMeta)
						}).Clicked() {
							a.setAppInspection(capture.SSLApp{Name: f.process, Path: f.processPath}, true)
						}
						ui.Spacer(c)
					})
				}
			})
		})
	}
	if !a.workbench.sideBySide && width >= 1100 && (len(f.reqHeader) > 0 || f.reqHeaders != "") {
		ui.Row(c).Grow(1).Children(func() {
			ui.Column(c).WidthPercent(44).FillHeight().Padding(t.Space(4), t.Space(4)).Children(func() {
				a.headerBlock(c, t, "Request", strings.TrimSpace("CONNECT "+f.host+" "+f.conn.proto), "", f.reqHeader, f.reqHeaders, nil)
			})
			ui.Box(c).Width(1).FillHeight().Background(t.Border)
			ui.Column(c).Grow(1).FillHeight().Children(inspection)
		})
	} else {
		ui.Column(c).Grow(1).FillWidth().Children(inspection)
	}
}
