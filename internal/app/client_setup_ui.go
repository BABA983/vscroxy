package app

import (
	"cmp"
	"runtime"
	"strings"

	"github.com/baba983/vscroxy/internal/clientsetup"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *app) clientSetupModal(c *ui.Context, t *ui.Theme) {
	s := &a.clientUI
	if !s.open {
		return
	}
	_, height := c.Size()
	uikit.Modal(c, &s.open, locales.SetUpALocalClient2(), func() {
		// The dialog is as tall as its content, up to the window: a short
		// panel does not leave a block of empty canvas under the last card.
		uikit.DialogColumn(c, uikit.SettingsWidth).MaxHeight(min(700, height-80)).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(t.Space(uikit.SpaceM)).Children(func() {
				uikit.DialogTitle(c, locales.SetUpALocalClient2())
				ui.Spacer(c)
				a.clientSetupBadge(c, t)
			})
			uikit.Tabs(c, &s.tab, "Client setup", locales.OneClick(), locales.Examples())
			if s.tab == 0 {
				a.clientSetupOneClick(c, t)
			} else {
				a.clientSetupExamples(c, t)
			}
			if s.problem != "" {
				uikit.Tip(c, uikit.ErrorText(c, s.problem).MaxLines(2), s.problem)
			}
			uikit.DialogFooter(c).Children(func() {
				if s.tab == 0 && s.target == clientTargetJava && s.javaConfirm {
					uikit.Hint(c, locales.ReplaceOnlyThisAppS()).Grow(1)
					if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
						s.javaConfirm = false
					}
					if clientActionButton(c, locales.ImportCa(), a.srv != nil && a.srv.Running()).Clicked() {
						s.javaConfirm = false
						a.importJavaCA()
					}
					return
				}
				if uikit.SecondaryButton(c, locales.PhoneTabletSetup()).Clicked() {
					s.open = false
					a.openDevices()
				}
				if uikit.SecondaryButton(c, locales.Diagnose()).Clicked() {
					s.open = false
					a.diagnostics.target = s.url
					a.openDiagnostics(nil, diagnoseProcess)
				}
				if uikit.SecondaryButton(c, locales.Close()).Clicked() {
					s.open = false
				}
				if a.srv == nil || !a.srv.Running() {
					if uikit.PrimaryButton(c, locales.StartCapture()).Disabled(a.srv == nil).Clicked() {
						go a.startCapture()
					}
				}
			})
		})
	})
}

// clientActionButton is the one action a panel offers while capture runs. With
// capture stopped the footer's Start capture owns the primary face, so a
// panel action is secondary and disabled until the listener exists.
func clientActionButton(c *ui.Context, label string, running bool) ui.Element {
	// A panel action hugs its label: a column would otherwise stretch it to
	// the card's width, which reads as a second full-width field.
	if running {
		return uikit.PrimaryButton(c, label).AlignSelf(ui.Start)
	}
	return uikit.SecondaryButton(c, label).AlignSelf(ui.Start)
}

func (a *app) clientSetupBadge(c *ui.Context, t *ui.Theme) {
	p := a.clientProxy()
	if a.srv != nil && a.srv.Running() {
		uikit.Badge(c, locales.ProxyBadge(p.Addr), t.Accent)
		return
	}
	uikit.Badge(c, locales.CaptureStopped(), t.TextMuted)
}

func (a *app) clientSetupOneClick(c *ui.Context, t *ui.Theme) {
	s := &a.clientUI
	p := a.clientProxy()
	running := a.srv != nil && a.srv.Running()
	if s.target == clientTargetAndroid && !s.androidLoaded && !s.androidLoading {
		a.loadAndroidDevices()
	}
	ui.Scroll(c).Shrink(1).MinHeight(0).Gap(t.Space(uikit.SpaceL)).Children(func() {
		uikit.SettingsCard(c, "", func() {
			uikit.SettingsCardRow(c, locales.Client(), "", func() {
				uikit.Select(c, &s.target, clientTargets).Label(locales.Client()).Width(220)
			})
		})
		uikit.SettingsCard(c, "", func() {
			uikit.SettingsCardBlock(c, "", "", func() {
				switch s.target {
				case clientTargetChrome:
					a.clientChromePanel(c, t, p, running)
				case clientTargetFirefox:
					a.clientFirefoxPanel(c, t, p, running)
				case clientTargetAndroid:
					a.clientAndroidPanel(c, t, p, running)
				case clientTargetIOS:
					a.clientIOSPanel(c, t, p, running)
				case clientTargetJava:
					a.clientJavaPanel(c, t, p, running)
				case clientTargetElectron:
					a.clientElectronPanel(c, t, p, running)
				case clientTargetContainer:
					a.clientContainerPanel(c, t, p, running)
				default:
					a.clientTerminalPanel(c, t, p, running)
				}
			})
		})
		if s.last != "" {
			uikit.SettingsCard(c, "", func() {
				uikit.SettingsCardBlock(c, locales.LastCommand(), "", func() {
					uikit.Code(c, s.last).Selectable().MaxLines(3)
				})
			})
		}
	})
}

func (a *app) clientTerminalPanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	uikit.Hint(c, locales.OpensANewTerminalWindow())
	env, _ := clientsetup.Environment(p)
	var preview []string
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "NODE_USE_ENV_PROXY", "NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE":
			preview = append(preview, entry)
		}
	}
	uikit.Code(c, strings.Join(preview, "\n")).Selectable().MaxLines(7)
	if p.BundlePath == "" {
		uikit.Callout(c, uikit.CalloutWarning, locales.SystemCaBundleUnavailableOnly(), nil)
	} else {
		uikit.Meta(c, "SSL_CERT_FILE, REQUESTS_CA_BUNDLE, CURL_CA_BUNDLE, GIT_SSL_CAINFO, PIP_CERT and DENO_CERT point at "+p.BundlePath+", a 0600 bundle of the system roots plus the vscroxy CA. Undecrypted hosts keep working.")
	}
	if clientActionButton(c, locales.OpenTerminal(), running).Disabled(!running).Clicked() {
		a.openProxiedTerminal()
	}
	uikit.Meta(c, locales.NodeExtraCaCertsAdds())
}

func (a *app) clientContainerPanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	s := &a.clientUI
	uikit.Hint(c, locales.RunsANewContainerFrom())
	uikit.FormRow(c, locales.Container(), "", "", func() {
		uikit.Input(c, &s.container).Label(locales.Container()).Placeholder(locales.ContainerPlaceholder()).FillWidth()
	})
	host := "host.docker.internal"
	if runtime.GOOS == "linux" {
		host = "127.0.0.1 (host network)"
	}
	uikit.Meta(c, locales.ContainerProxyHint(host))
	if p.BundlePath == "" {
		uikit.Callout(c, uikit.CalloutWarning, locales.SystemCaBundleUnavailableOnly(), nil)
	} else {
		uikit.Meta(c, locales.ContainerCaMounted())
	}
	if clientActionButton(c, locales.InterceptContainer(), running).
		Disabled(!running || strings.TrimSpace(s.container) == "").Clicked() {
		a.openInterceptContainer()
	}
	uikit.Meta(c, locales.ContainerLimitations())
}

func (a *app) clientChromePanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	addr := cmp.Or(p.Addr, "127.0.0.1:7890")
	uikit.Hint(c, locales.OpensChromeWithATemporary())
	uikit.Code(c, "--user-data-dir=<temporary profile>\n--proxy-server=http://"+addr).Selectable().MaxLines(2)
	if clientActionButton(c, locales.OpenChrome(), running).Disabled(!running).Clicked() {
		a.openProxiedChrome()
	}
	switch runtime.GOOS {
	case "linux":
		uikit.Meta(c, locales.ChromeOnLinuxReadsNss())
	default:
		uikit.Meta(c, locales.ChromeUsesThisSystemS())
	}
}

func (a *app) clientFirefoxPanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	addr := cmp.Or(p.Addr, "127.0.0.1:7890")
	uikit.Hint(c, locales.OpensFirefoxWithATemporary())
	uikit.Code(c, "network.proxy.type=1 · HTTP/SSL "+addr+"\nsecurity.enterprise_roots.enabled=true").Selectable().MaxLines(2)
	if clientActionButton(c, locales.OpenFirefox(), running).Disabled(!running).Clicked() {
		a.openProxiedFirefox()
	}
	if runtime.GOOS == "windows" {
		uikit.Meta(c, locales.FirefoxUsesTheWindowsRoot())
	} else {
		uikit.Meta(c, locales.TheTemporaryProfileTrustsThe())
	}
}

func (a *app) clientAndroidPanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	s := &a.clientUI
	ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Wrap().Children(func() {
		if len(s.android) > 0 {
			choice := s.androidChoice()
			uikit.Select(c, &choice, s.androidLabels()).Label(locales.AndroidDevice()).Width(280)
			s.setAndroidChoice(choice)
		} else {
			uikit.Meta(c, locales.NoAndroidDeviceOrEmulator())
		}
		if uikit.SecondaryButton(c, locales.Refresh()).Clicked() {
			s.androidLoaded = false
			a.loadAndroidDevices()
		}
	})
	switch {
	case s.androidLoading:
		uikit.Meta(c, locales.LookingForAdbDevices())
	case s.androidProblem != "":
		uikit.Tip(c, uikit.ErrorText(c, s.androidProblem).MaxLines(2), s.androidProblem)
	case len(s.android) == 0:
		uikit.Meta(c, locales.StartAnEmulatorOrConnect())
	}
	_, applied := s.androidApplied[s.androidSerial]
	ui.Row(c).Gap(t.Space(uikit.SpaceM)).Wrap().Children(func() {
		if clientActionButton(c, locales.SetProxy(), running).Disabled(!running || s.androidSerial == "" || applied || s.androidPending).Clicked() {
			a.applyAndroidProxy()
		}
		if uikit.SecondaryButton(c, locales.UndoProxy()).Disabled(s.androidSerial == "" || (!applied && p.Addr == "")).Clicked() {
			a.clearAndroidProxy()
		}
		if uikit.SecondaryButton(c, locales.ForgetSavedProxy()).Disabled(!applied).Clicked() {
			a.forgetAndroidProxy()
		}
		if uikit.SecondaryButton(c, locales.CopyCaToDevice()).Disabled(!running || s.androidSerial == "").Clicked() {
			a.pushAndroidCA()
		}
		if uikit.SecondaryButton(c, locales.InstallSystemCa()).Disabled(!running || s.androidSerial == "" || s.androidPending).Clicked() {
			a.installAndroidSystemCA()
		}
		if uikit.SecondaryButton(c, locales.NetworkSecurityConfig()).Clicked() {
			s.androidSnippet = !s.androidSnippet
		}
	})
	uikit.Meta(c, locales.UsesAdbReverseSo127())
	if s.androidSnippet {
		ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
			if uikit.SecondaryButton(c, locales.CopySnippet()).Clicked() {
				c.Services().WriteClipboard(clientsetup.AndroidNetworkSecurityConfig())
				a.done(locales.CopiedTheNetworkSecurityConfig())
			}
		})
		uikit.Meta(c, locales.TheSnippetTrustsTheCaOnlyIn())
		uikit.Code(c, clientsetup.AndroidNetworkSecurityConfig()).Selectable().MaxLines(12)
	}
	uikit.Meta(c, locales.TheCaIsCopiedTo())
	uikit.Meta(c, locales.InstallingASystemCaRunsAdbRoot())
}

func (a *app) clientIOSPanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	if runtime.GOOS != "darwin" {
		uikit.Meta(c, locales.IosSimulatorsRunOnlyOn())
		return
	}
	uikit.Hint(c, locales.AddsThePublicCaTo())
	uikit.Code(c, "xcrun simctl keychain booted add-root-cert "+cmp.Or(p.CAPath, "<CA>")).Selectable().MaxLines(2)
	if clientActionButton(c, locales.InstallCaInBootedSimulator(), running).Disabled(!running).Clicked() {
		a.installSimulatorCA()
	}
}

func (a *app) clientJavaPanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	s := &a.clientUI
	keystore := a.javaTruststorePath()
	if keystore == "" {
		uikit.Meta(c, locales.TheSettingsDirectoryIsUnknown())
		return
	}
	if !s.javaChecked && s.javaCACerts == "" {
		s.javaChecked = true
		if plan, err := clientsetup.JavaTruststorePlan(p, keystore); err != nil {
			s.javaProblem = err.Error()
		} else {
			s.javaCACerts = plan.CACerts
		}
	}
	if s.javaCACerts == "" {
		uikit.Tip(c, uikit.ErrorText(c, cmp.Or(s.javaProblem, "JDK cacerts are unavailable.")).MaxLines(3), s.javaProblem)
		return
	}
	commands, err := clientsetup.JavaTruststoreCommands(p, keystore, s.javaCACerts)
	if err != nil {
		uikit.Tip(c, uikit.ErrorText(c, err.Error()).MaxLines(3), err.Error())
		return
	}
	command := clientsetup.JoinCommands(commands, runtime.GOOS == "windows")
	uikit.Hint(c, locales.CopiesTheJdkSCacerts())
	uikit.Tip(c, uikit.Meta(c, "JDK cacerts: "+s.javaCACerts).Font(uikit.FontMono).Selectable().MaxLines(2), s.javaCACerts)
	uikit.Tip(c, uikit.Meta(c, "Truststore: "+keystore).Font(uikit.FontMono).Selectable().MaxLines(2), keystore)
	uikit.Code(c, command).Selectable().MaxLines(8)
	ui.Row(c).Gap(t.Space(uikit.SpaceM)).Wrap().Children(func() {
		if uikit.SecondaryButton(c, locales.CopyCommands()).Clicked() {
			c.Services().WriteClipboard(command)
			a.done(locales.CopiedKeytoolCommands())
		}
		if clientActionButton(c, locales.ImportText(), running).Disabled(!running).Clicked() {
			s.javaConfirm = true
		}
	})
	uikit.Meta(c, locales.TheTruststoreStartsAsA())
	args, err := clientsetup.JVMArgs(p, keystore)
	if err == nil {
		argsText := clientsetup.POSIXCommandLine("java", args)
		if runtime.GOOS == "windows" {
			argsText = clientsetup.WindowsCommandLine("java", args)
		}
		uikit.Code(c, argsText).Selectable().MaxLines(3)
	}
}

func (a *app) clientElectronPanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	s := &a.clientUI
	uikit.FormRow(c, locales.App(), "", "", func() {
		ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			uikit.Input(c, &s.electronPath).Label(locales.ElectronApp()).Placeholder("/Applications/Slack.app or /usr/bin/app").Font(uikit.FontMono).Grow(1).MinWidth(uikit.WidthField)
			if uikit.SecondaryButton(c, locales.ChooseApp()).Clicked() && a.win != nil {
				win := a.win
				go func() {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
						Title:                      locales.ChooseAnElectronApp(),
						Directory:                  runtime.GOOS == "darwin",
						TreatPackagesAsDirectories: false,
					})
					win.Update(func() {
						if err != nil {
							a.fail(locales.ChooseElectronApp(), err)
						} else if len(paths) > 0 {
							s.electronPath = paths[0]
						}
					})
				}()
			}
		})
	})
	addr := cmp.Or(p.Addr, "127.0.0.1:7890")
	uikit.Code(c, "--proxy-server=http://"+addr).Selectable().MaxLines(1)
	if clientActionButton(c, locales.LaunchWithProxy(), running).Disabled(!running || strings.TrimSpace(s.electronPath) == "").Clicked() {
		a.launchElectronApp()
	}
	uikit.Meta(c, locales.NeverDisablesCertificateVerificationCertificate())
}

func (a *app) clientSetupExamples(c *ui.Context, t *ui.Theme) {
	s := &a.clientUI
	p := a.clientProxy()
	running := a.srv != nil && a.srv.Running()
	ui.Scroll(c).Shrink(1).MinHeight(0).Gap(t.Space(uikit.SpaceL)).Children(func() {
		uikit.SettingsCard(c, "", func() {
			uikit.SettingsCardBlock(c, "", "", func() {
				ui.Form(c, func() {
					uikit.FormRow(c, locales.ClientRuntime(), "", "", func() {
						uikit.Select(c, &s.runtime, []string{"cURL", "Node.js", "Python", "Go"}).Label(locales.ClientRuntime()).Width(uikit.WidthField)
					})
					uikit.FormRow(c, locales.SetupHttpsUrl(), "", "", func() {
						uikit.Input(c, &s.url).Label(locales.SetupHttpsUrl()).FillWidth()
					})
				})
			})
		})
		uikit.SettingsCard(c, "", func() {
			uikit.SettingsCardBlock(c, "", "", func() {
				addr := p.Addr
				uikit.Body(c, locales.ProxyListener(cmp.Or(addr, locales.NotRunning())))
				uikit.Meta(c, locales.TheExampleTrustsOnlyThe())
				example := clientExample(s.runtime, addr, p.CAPath, s.url)
				if clientActionButton(c, locales.CopyClientExample(), running).Disabled(addr == "" || p.CAPath == "").Clicked() {
					c.Services().WriteClipboard(example)
					a.done(locales.CopiedClientSetup(s.runtime))
				}
				uikit.Code(c, example).Selectable()
				uikit.Meta(c, locales.RunItThenUseDiagnose())
			})
		})
	})
}
