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

func (a *app) httpsSettings(c *ui.Context, t *ui.Theme) {
	status, color := a.certificateStatus(t)
	s := &a.certificate
	uikit.SettingsPage(c, locales.Https(), locales.TrustingTheCaMakesDecryption(), func() {
		uikit.SettingsGroup(c, locales.Certificate(), func() {
			ui.Field(c, locales.Trust(), func() {
				ui.Column(c).Gap(t.Space(1)).Children(func() {
					ui.Text(c, status).FontSize(uikit.TextBody).FontWeight(500).TextColor(color)
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
						uikit.Tip(c, ui.Text(c, problem).FontSize(uikit.TextMeta).TextColor(t.Warning).SingleLine(), s.err.Error())
					}
					if s.installErr != nil {
						if errors.Is(s.installErr, system.ErrInstallCancelled) {
							uikit.Meta(c, locales.InstallationCancelledYouCanTry())
						} else {
							uikit.Tip(c, ui.Text(c, "Installation failed: "+s.installErr.Error()).FontSize(uikit.TextMeta).TextColor(t.Danger).MaxLines(2), s.installErr.Error())
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
			})
			// The buttons are their own field, so a disabled one does not
			// gray the Trust label.
			ui.Field(c, "", func() {
				ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
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
			}).Description(certificateScope())
		})
		uikit.SettingsGroup(c, locales.Decryption(), func() {
			ui.Field(c, locales.EveryDomain(), func() {
				all := a.sslAll
				if ui.Switch(c, &all).Label(locales.DecryptEveryDomain()).Changed() {
					cfg := a.inspectionConfig()
					cfg.All = all
					a.saveSSL(cfg)
				}
			}).Description(locales.DecryptEveryNewHttpsConnection())
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
			ui.Field(c, locales.DomainsAndApps(), func() {
				ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
					if uikit.SecondaryButton(c, locales.ManageDomains()).Clicked() {
						a.leaveSettings()
						a.sslOpen = true
					}
					uikit.Tip(c, uikit.Hint(c, summary).SingleLine().Shrink(1).MinWidth(0), summary)
				})
			}).Description(hint)
		})
		uikit.SettingsGroup(c, locales.Wireshark(), func() {
			ui.Field(c, locales.TlsKeyLog(), func() {
				ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
					if uikit.SecondaryButton(c, locales.TlsKeyLog2()).Clicked() {
						a.leaveSettings()
						a.openKeyLog()
					}
					uikit.Tip(c, uikit.Mono(c, cmp.Or(a.keyLogPath, "Off")).TextColor(t.TextMuted).SingleLine().MaxWidth(260), a.keyLogPath)
					restartPending(c, a.keyLogPath != a.launchKeyLog)
				})
			}).Description(locales.WriteTlsSecretsToA())
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
					ui.Column(c).FillWidth().Padding(0, t.Space(3)).Gap(t.Space(1)).BorderWidth(0, 0, 0, 2).BorderColor(t.Danger).Children(func() {
						ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
							uikit.Label(c, "Recorded failure · "+cmp.Or(f.conn.errorStage, "unknown stage")).Grow(1)
							if uikit.ToolbarButton(c, locales.CopyError(), locales.CopyTheRecordedError(), false).Height(uikit.HeightControl).Gap(t.Space(1)).Children(func() {
								ui.Icon(c, copyIcon).Size(uikit.IconInline, uikit.IconInline)
								ui.Text(c, locales.Copy()).FontSize(uikit.TextMeta)
							}).Clicked() {
								c.Services().WriteClipboard(f.conn.error)
								a.done(locales.CopiedTheRecordedError())
							}
						})
						uikit.Tip(c, ui.Text(c, f.conn.error).Font(uikit.FontMono).FontSize(uikit.TextMeta).TextColor(t.Danger).Selectable().SingleLine(), f.conn.error)
					})
				}
				// What to do next. A failure is read before anything is
				// reconfigured, so the record leads and enabling inspection
				// is an independent configuration step; a healthy tunnel
				// still leads with the rule that would decrypt it.
				ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
					record := func() {
						const tip = "Open this exchange's Connection tab: the recorded log and addresses"
						button := uikit.Tip(c, uikit.SecondaryButton(c, locales.ViewConnectionRecord()), tip)
						if button.Clicked() {
							a.inspect.pane = inspectorConnection
						}
					}
					enable := func() {
						button := uikit.SecondaryButton(c, "Inspect "+hostOnly(f.host)).Label("Enable for " + f.host).
							Tooltip(locales.ConfigurationForNewConnectionsA())
						if button.Clicked() {
							a.decryptHost(f.host)
						}
					}
					switch {
					case failed:
						// Read the evidence first; enabling inspection is a
						// configuration step, not the fix for this failure.
						record()
						if !domainEnabled {
							enable()
						}
					default:
						if !domainEnabled {
							enable()
						}
						record()
					}
					if uikit.Tip(c, uikit.SecondaryButton(c, locales.CertificateSetup()), locales.CheckOrInstallTheLocal()).Clicked() {
						a.openSettings(2)
						a.checkCertificate(false)
					}
				})
				// Known configuration, secondary: today's rules never
				// explain a tunnel that was recorded earlier.
				uikit.Label(c, locales.KnownConfiguration())
				if domainEnabled {
					ui.Text(c, locales.DomainRuleEnabledReconnectTo()).FontSize(uikit.TextMeta).TextColor(t.Text)
				} else {
					uikit.Meta(c, locales.DomainDecryptionDisabled())
				}
				if appEnabled {
					uikit.Tip(c, ui.Text(c, locales.AppRuleEnabledReconnectTo()).FontSize(uikit.TextMeta), f.processPath)
				} else if !filepath.IsAbs(f.processPath) {
					uikit.Meta(c, locales.AppRuleUnavailableLocalExecutable())
				}
				ui.Text(c, trust).FontSize(uikit.TextMeta).TextColor(trustColor).
					Tooltip(locales.SystemTrustIsNotProof())
				if broadScope {
					ui.Row(c).Children(func() {
						if uikit.ToolbarButton(c, "Inspect all domains from "+f.process, "Wider scope for new connections from "+f.processPath+"; not a fix for this failure", false).
							Height(uikit.HeightControl).Padding(0, t.Space(1)).Children(func() {
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
