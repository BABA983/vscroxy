package main

import (
	"cmp"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/desktop/internal/capture"
	"github.com/baba983/vscroxy/desktop/internal/system"
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
	ui.Text(c, "HTTPS inspection").FontSize(15).Bold()
	if a.srv != nil && a.srv.Running() {
		ui.Text(c, "Proxy listening · "+a.srv.Addr()).FontSize(12).TextColor(t.Success)
	} else {
		ui.Text(c, "Capture is not running").FontSize(12).TextColor(t.TextMuted)
	}
	ui.Text(c, "Trusting the CA only makes decryption possible. Domain and app rules expose request and response headers — including Cookie and Set-Cookie — and bodies. Connections not covered by a rule stay encrypted CONNECT tunnels.").FontSize(12).TextColor(t.TextMuted)
	ui.Divider(c)
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(8).Wrap().Children(func() {
			ui.Text(c, "Certificate").Bold().Grow(1)
			ui.Text(c, status).FontSize(12).Bold().TextColor(color)
		})
		if s.cert != nil {
			ui.Text(c, cmp.Or(s.cert.Subject.CommonName, s.cert.Subject.String())).FontSize(12)
			ui.Text(c, "Valid: "+s.cert.NotBefore.Local().Format("2006-01-02")+" → "+s.cert.NotAfter.Local().Format("2006-01-02")).FontSize(11).TextColor(t.TextMuted)
			fingerprint := fmt.Sprintf("%X", sha256.Sum256(s.cert.Raw))
			ui.Text(c, "SHA-256: "+fingerprint).FontSize(10).TextColor(t.TextMuted).SingleLine().Tooltip(fingerprint).Selectable()
		}
		ui.Text(c, "Trust this CA on the requesting device or in the requesting application. Separate trust stores and certificate pinning may need client-side configuration.").FontSize(12).TextColor(t.TextMuted)
		if runtime.GOOS == "windows" {
			ui.Text(c, "Installation trusts this CA for the current Windows user, not other accounts or services.").FontSize(12).TextColor(t.TextMuted)
		}
		if s.installing {
			ui.Text(c, "If the system asks for authorization, approve it to continue. Trust will be checked afterwards.").FontSize(12).TextColor(t.TextMuted)
		}
		if s.checked && !s.checking && s.err != nil {
			problem := s.err.Error()
			if errors.Is(s.err, os.ErrNotExist) {
				problem = "CA file not found. Start capture, then check again."
			}
			ui.Text(c, problem).FontSize(11).TextColor(t.Warning).SingleLine().Tooltip(s.err.Error())
		}
		if s.installErr != nil {
			if errors.Is(s.installErr, system.ErrInstallCancelled) {
				ui.Text(c, "Installation cancelled. You can try again when ready.").FontSize(12).TextColor(t.TextMuted)
			} else {
				ui.Text(c, "Installation failed: "+s.installErr.Error()).FontSize(11).TextColor(t.Danger).MaxLines(2).Tooltip(s.installErr.Error())
				switch runtime.GOOS {
				case "darwin":
					ui.Text(c, "Manual fallback: use Show certificate to locate the public CA, import it in Keychain Access, then set Trust → When using this certificate to Always Trust. Check again afterwards.").FontSize(12).TextColor(t.TextMuted)
				case "windows":
					ui.Text(c, "Manual fallback: use Show certificate and import the public CA into Trusted Root Certification Authorities for the current user. Check again afterwards.").FontSize(12).TextColor(t.TextMuted)
				default:
					ui.Text(c, "Manual fallback: use Show certificate and import the public CA using your distribution's trust-store tools. Check again afterwards.").FontSize(12).TextColor(t.TextMuted)
				}
			}
		}
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if ui.PrimaryButton(c, "Install & Trust…").Tooltip("Trust the local CA on this computer; approve system authorization if requested").Disabled(s.checking || s.cert == nil || s.checked && s.err == nil).Clicked() {
				a.checkCertificate(true)
			}
			if ui.Button(c, "Check again").Disabled(s.checking).Clicked() {
				a.checkCertificate(false)
			}
			if ui.Button(c, "Show certificate").Tooltip(a.caPath).Disabled(a.caPath == "").Clicked() {
				a.revealCA()
			}
		})
	})
	ui.Divider(c)
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, "Decryption rules").Bold()
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
		ui.Text(c, summary).FontSize(12).MaxLines(2).Tooltip(summary)
		switch {
		case a.sslAll:
			ui.Text(c, "All new HTTPS connections are eligible for inspection. Client trust and TLS compatibility still apply.").FontSize(12).TextColor(t.TextMuted)
		case len(a.sslApps) > 0:
			ui.Text(c, "Listed domains or exact local executables are eligible. Other clients stay encrypted unless a domain rule also matches.").FontSize(12).TextColor(t.TextMuted)
		case len(domains) > 0:
			ui.Text(c, "Only these domains and their subdomains are decrypted. Every other host stays an encrypted tunnel.").FontSize(12).TextColor(t.TextMuted)
		default:
			ui.Text(c, "Enable a domain here, or every domain explicitly, to inspect its headers, cookies, and bodies.").FontSize(12).TextColor(t.TextMuted)
		}
		ui.Text(c, "Trusting the CA and enabling domain decryption are separate steps.").FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "Manage domains…").Clicked() {
				a.settingsOpen, a.sslOpen = false, true
			}
			if ui.Button(c, "TLS key log…").Clicked() {
				a.settingsOpen = false
				a.openKeyLog()
			}
		})
	})
	ui.Text(c, "After changing HTTPS setup, establish a new connection and retry the request.").FontSize(11).TextColor(t.TextMuted)
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

// connectPanel describes this historical tunnel separately from today's rules.
func (a *app) connectPanel(c *ui.Context, f flow) {
	t := c.Theme()
	width, height := c.Size()
	status, color := a.certificateStatus(t)
	domainEnabled := a.inspectionConfig().Decrypts(f.host)
	inspection := func() {
		padding, gap := float32(16), float32(10)
		if height <= 650 {
			padding, gap = 8, 6
		}
		ui.Column(c).Fill().Padding(padding, 20).Gap(gap).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, lockIcon).Size(20, 20).TextColor(t.Text)
				ui.Text(c, "Encrypted tunnel").Label("Encrypted tunnel — not individual requests").FontSize(15).Bold()
			})
			ui.Text(c, "This connection is encrypted. CONNECT is not a decrypted HTTP response.").FontSize(12).TextColor(t.TextMuted)
			if a.workbench.sideBySide {
				ui.Column(c).FillWidth().Padding(10).Gap(7).Radius(6).Border(1, t.Border).Background(t.Surface).Children(func() {
					state := "Encrypted tunnel"
					if f.conn.error != "" {
						state = "Connection failed"
					}
					for _, field := range []struct{ label, value string }{
						{"URL", f.url},
						{"Method", f.method},
						{"State", state},
						{"Client", cmp.Or(f.process, "Unidentified")},
						{"Duration", fmt.Sprintf("%d ms", f.millis)},
					} {
						ui.Row(c).Gap(8).Children(func() {
							ui.Text(c, field.label).Width(64).FontSize(11).TextColor(t.TextMuted)
							ui.Text(c, field.value).Grow(1).FontSize(11).Font("monospace").SingleLine().Tooltip(field.value)
						})
					}
				})
			}
			// Keep the narrow domain action primary and the broader executable
			// permission secondary. Both remain outside the scrolling notes.
			if !domainEnabled {
				domain := ui.PrimaryButton(c, "Inspect "+hostOnly(f.host)).Label("Enable for " + f.host).Height(30).FontSize(12).FillWidth().Tooltip("Enable this hostname and subdomains on new connections; client CA trust is still required")
				if !a.workbench.sideBySide {
					domain.MaxWidth(260)
				}
				if domain.Clicked() {
					a.decryptHost(f.host)
				}
			}
			if !a.appInspectionEnabled(f.processPath) && !a.sslAll && filepath.IsAbs(f.processPath) {
				if ui.Button(c, "Inspect all domains from \""+f.process+"\"").Label("Enable HTTPS for " + f.process).Height(30).FontSize(12).FillWidth().MaxWidth(440).Tooltip("Exact executable only: " + f.processPath).Clicked() {
					a.setAppInspection(capture.SSLApp{Name: f.process, Path: f.processPath}, true)
				}
			}
			ui.Row(c).Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Text(c, "New connections only · Client must trust the local CA").FontSize(11).TextColor(t.TextMuted)
				if ui.ButtonBase(c).Label("Set up certificate…").Height(22).Tooltip("Certificate setup and system trust").Children(func() {
					ui.Text(c, "Certificate setup ↗").FontSize(11).TextColor(t.Accent)
				}).Clicked() {
					a.settingsTab, a.settingsOpen = 2, true
					a.checkCertificate(false)
				}
			})
			ui.Scroll(c).Grow(1).FillWidth().Gap(6).Children(func() {
				if f.conn.error != "" {
					ui.Text(c, "Connection failed before an HTTP exchange").FontSize(12).TextColor(t.Danger)
					ui.Text(c, "See Connection for the recorded failure; client trust or pinning may require client-side changes.").FontSize(11).TextColor(t.TextMuted)
				}
				if domainEnabled {
					ui.Text(c, "Domain rule enabled · reconnect to inspect new requests").FontSize(11).TextColor(t.Text)
				} else {
					ui.Text(c, "Domain decryption disabled").FontSize(11).TextColor(t.TextMuted)
				}
				if a.appInspectionEnabled(f.processPath) {
					ui.Text(c, "App rule enabled · reconnect to inspect new requests").FontSize(11).Tooltip(f.processPath)
				} else if !filepath.IsAbs(f.processPath) {
					ui.Text(c, "App rule unavailable · local executable identity not recorded").FontSize(11).TextColor(t.TextMuted)
				}
				ui.Text(c, status).FontSize(11).TextColor(color)
				ui.Text(c, "Rules do not reveal this historical tunnel. Enabled is not proof of successful decryption.").FontSize(11).TextColor(t.TextMuted)
			})
		})
	}
	if !a.workbench.sideBySide && width >= 1100 {
		ui.Row(c).Grow(1).Children(func() {
			ui.Column(c).WidthPercent(44).FillHeight().Padding(18, 20).Children(func() {
				a.headerBlock(c, t, "Request", strings.TrimSpace("CONNECT "+f.host+" "+f.conn.proto), "", f.reqHeader, f.reqHeaders, nil)
			})
			ui.Box(c).Width(1).FillHeight().Background(t.Border)
			ui.Column(c).Grow(1).FillHeight().Children(inspection)
		})
	} else {
		ui.Column(c).Grow(1).FillWidth().Children(inspection)
	}
}
