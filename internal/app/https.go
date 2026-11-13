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
	"github.com/baba983/vscroxy/locales"
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
				a.warn(locales.CertificateInstallationCancelledSystemTrust())
			case result.installErr != nil:
				a.fail(locales.InstallCertificate(), result.installErr)
			case result.err != nil:
				a.warn(locales.InstallationFinishedSystemTrustNot())
			default:
				a.done(locales.CaTrustedByThisSystem())
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

// certificateBanner says, under the filter bar, why HTTPS traffic still reads
// as encrypted tunnels: this system does not trust the CA the app decrypts
// with. It is the one place on the capture screen that says so, and it can be
// put away; the connection's own pane keeps its facts, not this app's.
func (a *app) certificateBanner(c *ui.Context, t *ui.Theme) {
	s := &a.certificate
	if a.certBannerOff || a.guide {
		return
	}
	// Nothing to say until the launch's trust check has answered, and
	// nothing to say while it trusts the CA.
	if !s.checked || s.cert != nil && s.err == nil {
		return
	}
	ui.Row(c).Height(uikit.HeightLarge).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).
		Background(t.Warning.Alpha(0.12)).Children(func() {
		ui.Icon(c, warningIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.Warning)
		ui.Text(c, locales.HttpsContentIsNotDecrypted()).FontSize(uikit.Scaled(uikit.TextMeta)).
			TextColor(t.Warning).Grow(1).SingleLine()
		if uikit.CompactButton(c, locales.InstallCertificate()).Clicked() {
			a.openSettings(2)
			a.checkCertificate(false)
		}
		if uikit.IconButton(c, locales.DismissCertificateBanner(), locales.DismissCertificateBanner(), closeIcon).Clicked() {
			a.certBannerOff = true
		}
	})
}

func (a *app) httpsSettings(c *ui.Context, t *ui.Theme) {
	status, color := a.certificateStatus(t)
	s := &a.certificate
	uikit.SettingsPage(c, locales.Https(), locales.TrustingTheCaMakesDecryption(), func() {
		uikit.SettingsGroup(c, locales.Certificate(), func() {
			uikit.SettingsRowMultiline(c, locales.Trust(), func() ui.Element {
				return ui.Column(c).Gap(t.Space(1)).Children(func() {
					ui.Text(c, status).FontSize(uikit.Scaled(uikit.TextBody)).FontWeight(500).TextColor(color)
					if s.cert != nil {
						uikit.Body(c, cmp.Or(s.cert.Subject.CommonName, s.cert.Subject.String()))
						uikit.Meta(c, "Valid: "+s.cert.NotBefore.Local().Format("2006-01-02")+" → "+s.cert.NotAfter.Local().Format("2006-01-02"))
						fingerprint := fmt.Sprintf("%X", sha256.Sum256(s.cert.Raw))
						uikit.Tip(c, uikit.Meta(c, "SHA-256: "+fingerprint).Font(uikit.FontMono).SingleLine(), fingerprint).Selectable()
					}
					if s.installing {
						uikit.Meta(c, locales.IfTheSystemAsksFor())
					}
					if s.checked && !s.checking && s.err != nil {
						problem := s.err.Error()
						if errors.Is(s.err, os.ErrNotExist) {
							problem = "CA file not found. Start capture, then check again."
						}
						uikit.Tip(c, ui.Text(c, problem).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Warning).SingleLine(), s.err.Error())
					}
					if s.installErr != nil {
						if errors.Is(s.installErr, system.ErrInstallCancelled) {
							uikit.Meta(c, locales.InstallationCancelledYouCanTry())
						} else {
							uikit.Tip(c, ui.Text(c, "Installation failed: "+s.installErr.Error()).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Danger).MaxLines(2), s.installErr.Error())
							switch runtime.GOOS {
							case "darwin":
								uikit.Meta(c, locales.ManualFallbackUseShowCertificate())
							case "windows":
								uikit.Meta(c, locales.ManualFallbackUseShowCertificate2())
							default:
								uikit.Meta(c, locales.ManualFallbackUseShowCertificate3())
							}
						}
					}
				})
			}, "")
			// The buttons are their own row, so a disabled one does not
			// gray the Trust label.
			uikit.SettingsRow(c, "", func() ui.Element {
				return ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
					if uikit.Tip(c, uikit.PrimaryButton(c, locales.InstallTrust()), locales.TrustTheLocalCaOn()).Disabled(s.checking || s.cert == nil || s.checked && s.err == nil).Clicked() {
						a.checkCertificate(true)
					}
					if uikit.SecondaryButton(c, locales.CheckAgain()).Disabled(s.checking).Clicked() {
						a.checkCertificate(false)
					}
					if uikit.Tip(c, uikit.SecondaryButton(c, locales.ShowCertificate()), a.caPath).Disabled(a.caPath == "").Clicked() {
						a.revealCA()
					}
				})
			}, certificateScope())
		})
		uikit.SettingsGroup(c, locales.Decryption(), func() {
			uikit.SettingsRow(c, locales.EveryDomain(), func() ui.Element {
				all := a.sslAll
				toggle := ui.Switch(c, &all).Label(locales.DecryptEveryDomain())
				if toggle.Changed() {
					cfg := a.inspectionConfig()
					cfg.All = all
					a.saveSSL(cfg)
				}
				return toggle
			}, locales.DecryptEveryNewHttpsConnection())
			domains := splitHosts(a.sslHosts)
			summary := "None"
			hint := "Enable a domain or a local app to read its headers, cookies and bodies."
			switch {
			case len(a.sslApps) > 0:
				summary = fmt.Sprintf("%d domain rules · %d local executable rules", len(domains), len(a.sslApps))
				hint = "Listed domains or exact local executables are decrypted. Other clients stay encrypted unless a domain rule also matches."
			case len(domains) > 0:
				summary = strings.Join(domains, ", ")
				hint = "These domains and their subdomains are decrypted. Every other host stays an encrypted tunnel."
			}
			uikit.SettingsRow(c, locales.DomainsAndApps(), func() ui.Element {
				return ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
					if uikit.SecondaryButton(c, locales.ManageDomains()).Clicked() {
						a.leaveSettings()
						a.sslOpen = true
					}
					uikit.Tip(c, uikit.Hint(c, summary).SingleLine().Shrink(1).MinWidth(0), summary)
				})
			}, hint)
		})
		uikit.SettingsGroup(c, locales.Wireshark(), func() {
			uikit.SettingsRow(c, locales.TlsKeyLog(), func() ui.Element {
				return ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
					if uikit.SecondaryButton(c, locales.TlsKeyLog2()).Clicked() {
						a.leaveSettings()
						a.openKeyLog()
					}
					uikit.Tip(c, uikit.Mono(c, cmp.Or(a.keyLogPath, "Off")).TextColor(t.TextMuted).SingleLine().MaxWidth(260), a.keyLogPath)
					restartPending(c, a.keyLogPath != a.launchKeyLog)
				})
			}, locales.WriteTlsSecretsToA())
		})
	})
}

// certificateScope says where trusting the CA here reaches, and where not.
func certificateScope() string {
	scope := "Trust this CA on the requesting device or in the requesting application too. Separate trust stores and certificate pinning may need client-side configuration."
	if runtime.GOOS == "windows" {
		scope += " On Windows, installation trusts it for the current user, not other accounts or services."
	}
	return scope
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
	return a.clientApps[name]
}

func (a *app) setAppInspection(app capture.SSLApp, enabled bool) {
	if !filepath.IsAbs(app.Path) {
		a.warn(locales.HttpsAppRuleUnavailableA())
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
		a.inform(message)
	}
}

func (a *app) appInspectionActions(app capture.SSLApp) []action {
	label := "Enable HTTPS for " + cmp.Or(app.Name, "this app")
	enabled := a.appInspectionEnabled(app.Path)
	if enabled {
		label = "Disable HTTPS for " + app.Name
	}
	return []action{do("https.app", label, func() { a.setAppInspection(app, !enabled) }).
		when(filepath.IsAbs(app.Path), "The app's executable is unknown").checked(checkbox(enabled))}
}

// connectPanel describes this historical tunnel separately from today's rules:
// what happened, what is configured, and what to do next, in that order.
func (a *app) connectPanel(c *ui.Context, f flow) {
	t := c.Theme()
	width, height := c.Size()
	domainEnabled := a.inspectionConfig().Decrypts(f.host)
	e := f.conn.explanation()
	failed := f.conn.error != ""
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
				// One line says what this row is, and the button beside it
				// is the way out: everything else about why the tunnel is
				// closed lives in the Connection tab.
				title := "Encrypted, not decrypted"
				if failed {
					title = e.title
				}
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, lockIcon).Size(uikit.IconLarge, uikit.IconLarge).TextColor(t.Text)
					uikit.Heading(c, title).Label(title + " — not individual requests").Grow(1).SingleLine()
				})
				if failed {
					uikit.Hint(c, e.summary)
				}
				if failed {
					// What happened: the stage and the raw error, copyable.
					ui.Column(c).FillWidth().Padding(0, t.Space(3)).Gap(t.Space(1)).BorderWidth(0, 0, 0, 2).BorderColor(t.Danger).Children(func() {
						ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
							uikit.Label(c, "Recorded failure · "+cmp.Or(f.conn.errorStage, "unknown stage")).Grow(1)
							if uikit.ToolbarButton(c, locales.CopyError(), locales.CopyTheRecordedError(), false).Height(uikit.HeightControl).Gap(t.Space(1)).Children(func() {
								ui.Icon(c, copyIcon).Size(uikit.IconInline, uikit.IconInline)
								ui.Text(c, locales.Copy()).FontSize(uikit.Scaled(uikit.TextMeta))
							}).Clicked() {
								c.Services().WriteClipboard(f.conn.error)
								a.done(locales.CopiedTheRecordedError())
							}
						})
						uikit.Tip(c, ui.Text(c, f.conn.error).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Danger).Selectable().SingleLine(), f.conn.error)
					})
				}
				// What to do next. A failure is read before anything is
				// reconfigured, so the record leads and enabling inspection
				// is an independent configuration step; a healthy tunnel
				// still leads with the rule that would decrypt it.
				ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
					record := func() {
						const tip = "Open this exchange's Connection tab: the recorded log, addresses and rules"
						button := uikit.Tip(c, uikit.SecondaryButton(c, locales.ViewConnectionRecord()), tip)
						if button.Clicked() {
							a.inspect.pane = inspectorConnection
						}
					}
					switch {
					case failed:
						// Read the evidence first; enabling inspection is a
						// configuration step, not the fix for this failure.
						record()
					default:
						if !domainEnabled {
							if uikit.SecondaryButton(c, "Decrypt "+hostOnly(f.host)).Label("Decrypt " + f.host).
								Tooltip(locales.ConfigurationForNewConnectionsA()).Clicked() {
								a.decryptHost(f.host)
							}
						}
						record()
					}
					// The certificate is the capture screen's banner to
					// offer: a tunnel pane that offered it too would say
					// the same thing in a second place.
				})
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
