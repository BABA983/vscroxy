package main

import (
	"cmp"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"runtime"
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
		ui.Text(c, "Domain decryption").Bold()
		domains := splitHosts(a.sslHosts)
		summary := "No domains enabled"
		if a.sslAll {
			summary = "Every domain enabled"
		} else if len(domains) > 0 {
			summary = strings.Join(domains, ", ")
		}
		ui.Text(c, summary).FontSize(12).MaxLines(2).Tooltip(summary)
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

// connectPanel describes this historical tunnel separately from today's rules.
func (a *app) connectPanel(c *ui.Context, f flow) {
	t := c.Theme()
	status, color := a.certificateStatus(t)
	enabled := (capture.SSLConfig{All: a.sslAll, Hosts: splitHosts(a.sslHosts)}).Decrypts(f.host)
	ui.Scroll(c).Grow(1).FillWidth().Padding(16).Gap(10).Children(func() {
		ui.Text(c, "Encrypted tunnel — not individual requests").FontSize(15).Bold()
		ui.Text(c, "This connection was forwarded without decryption. Its HTTPS requests and responses are not visible.").FontSize(12).TextColor(t.TextMuted)
		ui.Divider(c)
		ui.Row(c).Gap(12).Wrap().Children(func() {
			ui.Text(c, "1  Proxy connection received").FontSize(12).Bold().TextColor(t.Success)
			ui.Text(c, "CONNECT · "+f.host).FontSize(12).TextColor(t.TextMuted)
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "2  "+status).FontSize(12).Bold().TextColor(color)
				ui.Text(c, "The requesting device or application must trust this CA. System trust does not verify other clients.").FontSize(11).TextColor(t.TextMuted)
			})
			if ui.Button(c, "Set up certificate…").Shrink(0).Clicked() {
				a.settingsTab, a.settingsOpen = 2, true
				a.checkCertificate(false)
			}
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				if enabled {
					ui.Text(c, "3  Domain decryption is now enabled").FontSize(12).Bold().TextColor(t.Success)
					ui.Text(c, "Existing tunnels stay encrypted. New connections use the current rules.").FontSize(11).TextColor(t.TextMuted)
				} else {
					ui.Text(c, "3  Domain decryption disabled").FontSize(12).Bold().TextColor(t.Warning)
					ui.Text(c, "Enable inspection for this domain and its subdomains.").FontSize(11).TextColor(t.TextMuted)
				}
			})
			if !enabled && ui.PrimaryButton(c, "Enable for "+f.host).Shrink(0).Clicked() {
				a.decryptHost(f.host)
			}
		})
		ui.Divider(c)
		ui.Text(c, "After setup, establish a new connection and retry the request. This CONNECT record will remain a tunnel.").FontSize(11).TextColor(t.TextMuted)
	})
}
