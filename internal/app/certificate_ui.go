package app

import (
	"slices"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
)

// chooseCertificateFile fills target from a file picker.
func (a *app) chooseCertificateFile(target *string, title string, extensions []string) {
	if a.win == nil {
		return
	}
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: title,
			Filters: []mygo.FileFilter{{Name: title, Extensions: extensions}}})
		win.Update(func() {
			if err != nil {
				a.fail(title, err)
				return
			}
			if len(paths) > 0 {
				*target = paths[0]
				a.certificateUI.dirty = true
			}
		})
	}()
}

// certificateSettings is Settings → Certificates: client certificates by
// host and the extra roots used when a server is verified.
func (a *app) certificateSettings(c *ui.Context, t *ui.Theme) {
	e := &a.certificateUI
	e.load(a)
	uikit.SettingsPage(c, locales.Certificates(), locales.ClientCertificatesAndExtraRoots(), func() {
		if a.certProblem != "" {
			ui.Text(c, a.certProblem).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Danger).Selectable()
		}
		if e.problem != "" {
			ui.Text(c, e.problem).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Danger).Selectable()
		}
		uikit.SettingsGroup(c, locales.ClientCertificates(), func() {
			uikit.Meta(c, locales.PresentedToAMatchingHost())
			if len(e.rows) == 0 {
				uikit.Hint(c, locales.NoClientCertificates())
			}
			remove := -1
			for i := range e.rows {
				row := &e.rows[i]
				ui.Column(c).Gap(t.Space(2)).Children(func() {
					ui.Row(c).Gap(t.Space(2)).AlignItems(ui.End).Children(func() {
						if uikit.Input(c, &row.Host).Label(locales.Host()).Placeholder("api.example.com or *.example.com:8443").Grow(1).Changed() {
							e.dirty = true
						}
						format := 0
						if row.Format == certs.FormatPKCS12 {
							format = 1
						}
						if uikit.Segmented(c, &format, locales.Pem(), locales.Pkcs12()).Label(locales.CertificateFormat()).Changed() {
							row.Format = certs.FormatPEM
							if format == 1 {
								row.Format, row.KeyFile = certs.FormatPKCS12, ""
							}
							e.dirty = true
						}
					})
					ui.Row(c).Gap(t.Space(2)).AlignItems(ui.End).Children(func() {
						if uikit.Input(c, &row.CertFile).Label(locales.CertificateFile()).Placeholder(locales.PemChainOrP12Pfx()).Grow(1).Changed() {
							e.dirty = true
						}
						if uikit.SecondaryButton(c, locales.Choose()).Clicked() {
							a.chooseCertificateFile(&row.CertFile, "Certificate file", []string{"pem", "crt", "cer", "p12", "pfx"})
						}
					})
					if row.Format != certs.FormatPKCS12 {
						ui.Row(c).Gap(t.Space(2)).AlignItems(ui.End).Children(func() {
							if uikit.Input(c, &row.KeyFile).Label(locales.KeyFile()).Placeholder(locales.PemPrivateKey()).Grow(1).Changed() {
								e.dirty = true
							}
							if uikit.SecondaryButton(c, locales.Choose()).Clicked() {
								a.chooseCertificateFile(&row.KeyFile, "Private key file", []string{"key", "pem"})
							}
						})
					}
					ui.Row(c).Gap(t.Space(2)).AlignItems(ui.End).Children(func() {
						if uikit.Input(c, &row.Passphrase).Password().Label(locales.Passphrase()).Placeholder(locales.Optional()).Grow(1).Changed() {
							row.Clear, e.dirty = false, true
						}
						if uikit.SecondaryButton(c, locales.Remove()).Clicked() {
							remove = i
						}
					})
					ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
						switch {
						case row.Clear:
							uikit.Hint(c, locales.SavedPassphraseWillBeRemoved())
							if uikit.SecondaryButton(c, locales.Keep()).Clicked() {
								row.Clear, e.dirty = false, true
							}
						case row.Saved && row.Passphrase == "":
							uikit.Hint(c, locales.SavedPassphraseInUse())
							if uikit.SecondaryButton(c, locales.Clear()).Clicked() {
								row.Clear, e.dirty = true, true
							}
						default:
							uikit.Hint(c, locales.SealedLocallyNeverExported())
						}
					})
				})
				ui.Divider(c)
			}
			if remove >= 0 {
				e.rows = slices.Delete(e.rows, remove, remove+1)
				e.dirty = true
			}
			if uikit.SecondaryButton(c, locales.AddClientCertificate()).Clicked() {
				e.rows = append(e.rows, certificateRow{ID: uuid.NewString(), Format: certs.FormatPEM})
				e.dirty = true
			}
		})
		uikit.SettingsGroup(c, locales.TrustedRoots(), func() {
			ui.Row(c).Gap(t.Space(2)).MinHeight(uikit.HeightControl).AlignItems(ui.Center).Children(func() {
				if ui.Switch(c, &e.trustSystem).Label(locales.TrustSystemRoots()).Changed() {
					e.dirty = true
				}
				ui.Text(c, locales.TrustSystemRoots()).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Text)
			})
			uikit.Meta(c, locales.ExtraPemRootsAreAdded())
			remove := -1
			for i := range e.roots {
				root := &e.roots[i]
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.End).Children(func() {
					if uikit.Input(c, &root.Path).Label(locales.CustomCaFile()).Placeholder(locales.PemRootCertificate()).Grow(1).Changed() {
						e.dirty = true
					}
					if uikit.SecondaryButton(c, locales.Choose()).Clicked() {
						a.chooseCertificateFile(&root.Path, "Custom CA file", []string{"pem", "crt", "cer"})
					}
					if uikit.SecondaryButton(c, locales.Remove()).Clicked() {
						remove = i
					}
				})
			}
			if remove >= 0 {
				e.roots = slices.Delete(e.roots, remove, remove+1)
				e.dirty = true
			}
			if uikit.SecondaryButton(c, locales.AddCaFile()).Clicked() {
				e.roots = append(e.roots, certificateRoot{})
				e.dirty = true
			}
		})
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			if uikit.PrimaryButton(c, locales.SaveCertificates()).Clicked() {
				a.saveCertificates()
			}
			if e.dirty && uikit.SecondaryButton(c, locales.DiscardChanges()).Clicked() {
				e.loaded, e.dirty, e.problem = false, false, ""
			}
			uikit.Meta(c, locales.PassphrasesAreSealedInA())
		})
	})
}
