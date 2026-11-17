package app

import (
	"path/filepath"
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
	uikit.SettingsCardPage(c, locales.Certificates(), locales.ClientCertificatesAndExtraRoots(), func() {
		if a.certProblem != "" {
			uikit.Callout(c, uikit.CalloutDanger, a.certProblem, nil)
		}
		if e.problem != "" {
			uikit.Callout(c, uikit.CalloutDanger, e.problem, nil)
		}
		uikit.SettingsCard(c, locales.SigningCa(), func() {
			settingsCardBlock(c, "", func() ui.Element {
				return uikit.Meta(c, locales.SigningCaHint())
			}, "")
			state := locales.SigningCaGenerated()
			switch {
			case e.signingClear:
				state = locales.SigningCaAfterSave()
			case e.signingSaved:
				subject := e.signingSubject
				if subject == "" {
					subject = e.signingCert
				}
				state = locales.CustomCaSubject(subject)
			}
			settingsCardBlock(c, "", func() ui.Element {
				return ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					uikit.Body(c, state).TextColor(t.Text)
					if e.signingSaved && !e.signingClear {
						uikit.Tip(c, uikit.Meta(c, e.signingCert).SingleLine(), e.signingCert)
					}
				})
			}, "")
			settingsCardBlock(c, locales.SigningCaCertificate(), func() ui.Element {
				return ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.End).Children(func() {
					if uikit.Input(c, &e.signingCert).Label(locales.CertificateFile()).Placeholder(locales.PemRootCertificate()).Grow(1).Changed() {
						e.dirty = true
					}
					if uikit.SecondaryButton(c, locales.Choose()).Clicked() {
						a.chooseCertificateFile(&e.signingCert, locales.SigningCaCertificateFile(), []string{"pem", "crt", "cer"})
					}
				})
			}, "")
			settingsCardBlock(c, locales.SigningCaPrivateKey(), func() ui.Element {
				return ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.End).Children(func() {
					if uikit.Input(c, &e.signingKey).Label(locales.SigningCaPrivateKey()).Placeholder(locales.PemPrivateKey()).Grow(1).Changed() {
						e.dirty = true
					}
					if uikit.SecondaryButton(c, locales.Choose()).Clicked() {
						a.chooseCertificateFile(&e.signingKey, locales.SigningCaPrivateKeyFile(), []string{"pem", "key"})
					}
				})
			}, locales.SigningCaKeyHint())
			showActions := !e.signingClear && (e.signingSaved || e.signingCert != "" || e.signingKey != "") ||
				e.signingClear && a.certificates.SigningCA != nil
			if showActions {
				settingsCardBlock(c, "", func() ui.Element {
					return ui.Row(c).Gap(t.Space(uikit.SpaceM)).Wrap().Children(func() {
						if !e.signingClear && (e.signingSaved || e.signingCert != "" || e.signingKey != "") {
							if uikit.SecondaryButton(c, locales.UseGeneratedCa()).Clicked() {
								e.signingClear, e.signingSaved = true, false
								e.signingCert, e.signingKey, e.signingSubject = "", "", ""
								e.dirty = true
							}
						}
						if e.signingClear && a.certificates.SigningCA != nil {
							if uikit.SecondaryButton(c, locales.KeepCustomCa()).Clicked() {
								e.signingClear, e.signingSaved = false, true
								e.signingCert = a.certificates.SigningCA.CertFile
								e.signingSubject = certs.CertificateSubject(e.signingCert)
								e.dirty = true
							}
						}
					})
				}, "")
			}
		})
		uikit.SettingsCard(c, locales.ClientCertificates(), func() {
			settingsCardBlock(c, "", func() ui.Element {
				return uikit.Meta(c, locales.PresentedToAMatchingHost())
			}, "")
			if len(e.rows) == 0 {
				settingsCardBlock(c, "", func() ui.Element {
					return uikit.Hint(c, locales.NoClientCertificates())
				}, "")
			}
			remove := -1
			for i := range e.rows {
				row := &e.rows[i]
				if e.open == nil {
					e.open = map[string]bool{}
				}
				expanded := e.open[row.ID]
				settingsCardBlock(c, "", func() ui.Element {
					return ui.Column(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
						ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
							if uikit.Input(c, &row.Host).Label(locales.Host()).Placeholder("api.example.com or *.example.com:8443").Grow(1).MinWidth(0).Changed() {
								e.dirty = true
							}
							summary := certificateRowSummary(*row)
							if row.CertFile != "" {
								uikit.Tip(c, uikit.Meta(c, summary).SingleLine(), row.CertFile)
							} else {
								uikit.Meta(c, summary).SingleLine()
							}
							label := locales.Edit2()
							if expanded {
								label = locales.Done()
							}
							if uikit.CompactButton(c, label).Clicked() {
								e.open[row.ID] = !expanded
							}
							if uikit.RowIconButton(c, locales.Remove(), locales.Remove(), closeIcon).Clicked() {
								remove = i
							}
						})
						if !expanded {
							return
						}
						format := 0
						if row.Format == certs.FormatPKCS12 {
							format = 1
						}
						ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
							uikit.Meta(c, locales.CertificateFormat()).Grow(1)
							if uikit.Segmented(c, &format, locales.Pem(), locales.Pkcs12()).Label(locales.CertificateFormat()).Changed() {
								row.Format = certs.FormatPEM
								if format == 1 {
									row.Format, row.KeyFile = certs.FormatPKCS12, ""
								}
								e.dirty = true
							}
						})
						ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.End).Children(func() {
							if uikit.Input(c, &row.CertFile).Label(locales.CertificateFile()).Placeholder(locales.PemChainOrP12Pfx()).Grow(1).Changed() {
								e.dirty = true
							}
							if uikit.SecondaryButton(c, locales.Choose()).Clicked() {
								a.chooseCertificateFile(&row.CertFile, locales.CertificateFile(), []string{"pem", "crt", "cer", "p12", "pfx"})
							}
						})
						if row.Format != certs.FormatPKCS12 {
							ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.End).Children(func() {
								if uikit.Input(c, &row.KeyFile).Label(locales.KeyFile()).Placeholder(locales.PemPrivateKey()).Grow(1).Changed() {
									e.dirty = true
								}
								if uikit.SecondaryButton(c, locales.Choose()).Clicked() {
									a.chooseCertificateFile(&row.KeyFile, locales.PrivateKeyFile(), []string{"key", "pem"})
								}
							})
						}
						ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.End).Children(func() {
							if uikit.Input(c, &row.Passphrase).Password().Label(locales.Passphrase()).Placeholder(locales.Optional()).Grow(1).Changed() {
								row.Clear, e.dirty = false, true
							}
						})
						ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
							switch {
							case row.Clear:
								uikit.Hint(c, locales.SavedPassphraseWillBeRemoved()).Grow(1)
								if uikit.SecondaryButton(c, locales.Keep()).Clicked() {
									row.Clear, e.dirty = false, true
								}
							case row.Saved && row.Passphrase == "":
								uikit.Hint(c, locales.SavedPassphraseInUse()).Grow(1)
								if uikit.SecondaryButton(c, locales.Clear()).Clicked() {
									row.Clear, e.dirty = true, true
								}
							default:
								uikit.Hint(c, locales.SealedLocallyNeverExported())
							}
						})
					})
				}, "")
			}
			if remove >= 0 {
				e.rows = slices.Delete(e.rows, remove, remove+1)
				e.dirty = true
			}
			settingsCardBlock(c, "", func() ui.Element {
				return ui.Row(c).Children(func() {
					b := uikit.SecondaryButton(c, locales.AddClientCertificate())
					if b.Clicked() {
						id := uuid.NewString()
						e.rows = append(e.rows, certificateRow{ID: id, Format: certs.FormatPEM})
						if e.open == nil {
							e.open = map[string]bool{}
						}
						e.open[id] = true
						e.dirty = true
					}
				})
			}, "")
		})
		uikit.SettingsCard(c, locales.TrustedRoots(), func() {
			settingsCardRow(c, locales.TrustSystemRoots(), func() ui.Element {
				toggle := uikit.SettingsSwitch(c, &e.trustSystem, locales.TrustSystemRoots())
				if toggle.Changed() {
					e.dirty = true
				}
				return toggle
			}, locales.ExtraPemRootsAreAdded())
			remove := -1
			for i := range e.roots {
				root := &e.roots[i]
				settingsCardBlock(c, "", func() ui.Element {
					return ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.End).Children(func() {
						if uikit.Input(c, &root.Path).Label(locales.CustomCaFile()).Placeholder(locales.PemRootCertificate()).Grow(1).Changed() {
							e.dirty = true
						}
						if uikit.SecondaryButton(c, locales.Choose()).Clicked() {
							a.chooseCertificateFile(&root.Path, locales.CustomCaFile(), []string{"pem", "crt", "cer"})
						}
						if uikit.SecondaryButton(c, locales.Remove()).Clicked() {
							remove = i
						}
					})
				}, "")
			}
			if remove >= 0 {
				e.roots = slices.Delete(e.roots, remove, remove+1)
				e.dirty = true
			}
			settingsCardBlock(c, "", func() ui.Element {
				return ui.Row(c).Children(func() {
					b := uikit.SecondaryButton(c, locales.AddCaFile())
					if b.Clicked() {
						e.roots = append(e.roots, certificateRoot{})
						e.dirty = true
					}
				})
			}, "")
		})
	})
}

// certificateFooter keeps the editor's one primary action in view while the
// page scrolls: the Save stays under the list rather than below Trusted
// roots, where a 560-point Settings window cut it off.
func (a *app) certificateFooter(c *ui.Context, t *ui.Theme) {
	e := &a.certificateUI
	uikit.DialogFooter(c).Shrink(0).Padding(t.Space(uikit.SpaceM), t.Space(uikit.Space2XL)).Children(func() {
		uikit.Meta(c, locales.PassphrasesAreSealedInA()).Grow(1).MinWidth(0)
		if e.dirty && uikit.SecondaryButton(c, locales.DiscardChanges()).Clicked() {
			e.loaded, e.dirty, e.problem = false, false, ""
		}
		if uikit.PrimaryButton(c, locales.SaveCertificates()).Clicked() {
			a.saveCertificates()
		}
	})
}

// certificateRowSummary is the one line a client certificate shows while it
// is folded: its format and file name, or what is still missing.
func certificateRowSummary(row certificateRow) string {
	if row.CertFile == "" {
		return locales.ChooseACertificate()
	}
	format := "PEM"
	if row.Format == certs.FormatPKCS12 {
		format = "PKCS#12"
	}
	summary := format + " · " + filepath.Base(row.CertFile)
	if row.Saved {
		summary += " · " + locales.SealedPassphrase()
	}
	return summary
}
