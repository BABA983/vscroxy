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
	uikit.Modal(c, &s.open, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(t.Space(2)).Children(func() {
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
				ui.Text(c, s.problem).FontSize(uikit.TextBody).TextColor(t.Danger).MaxLines(2).Tooltip(s.problem)
			}
			uikit.DialogFooter(c).Children(func() {
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
			})
		})
	})
}

func (a *app) clientSetupBadge(c *ui.Context, t *ui.Theme) {
	p := a.clientProxy()
	if a.srv != nil && a.srv.Running() {
		ui.Badge(c, "Proxy "+p.Addr).Background(t.Accent.Alpha(0.12)).TextColor(t.Accent)
		return
	}
	ui.Badge(c, "Capture stopped")
}

func (a *app) clientSetupOneClick(c *ui.Context, t *ui.Theme) {
	s := &a.clientUI
	p := a.clientProxy()
	running := a.srv != nil && a.srv.Running()
	_, height := c.Size()
	scrollHeight := max(float32(120), min(float32(320), height-220))
	ui.Form(c, func() {
		ui.Field(c, locales.Client(), func() {
			uikit.Select(c, &s.target, clientTargets).Label(locales.Client()).Width(200)
		})
	})
	if !running && (s.target != clientTargetIOS || runtime.GOOS == "darwin") {
		uikit.Meta(c, locales.StartCaptureFirstTheseActions())
	}
	if s.target == clientTargetAndroid && !s.androidLoaded && !s.androidLoading {
		a.loadAndroidDevices()
	}
	ui.Scroll(c).MaxHeight(scrollHeight).MinHeight(0).Gap(t.Space(3)).Padding(0, t.Space(2), 0, 0).Children(func() {
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
		default:
			a.clientTerminalPanel(c, t, p, running)
		}
		if s.last != "" {
			ui.Divider(c)
			uikit.Hint(c, locales.LastCommand())
			ui.Text(c, s.last).Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable().MaxLines(3)
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
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE":
			preview = append(preview, entry)
		}
	}
	ui.Text(c, strings.Join(preview, "\n")).Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable().MaxLines(7)
	if p.BundlePath == "" {
		ui.Text(c, locales.SystemCaBundleUnavailableOnly()).
			FontSize(uikit.TextMeta).TextColor(t.Warning).MaxLines(2)
	} else {
		uikit.Meta(c, "SSL_CERT_FILE, REQUESTS_CA_BUNDLE, CURL_CA_BUNDLE, GIT_SSL_CAINFO, PIP_CERT and DENO_CERT point at "+p.BundlePath+", a 0600 bundle of the system roots plus the vscroxy CA. Undecrypted hosts keep working.")
	}
	if uikit.PrimaryButton(c, locales.OpenTerminal()).Disabled(!running).Clicked() {
		a.openProxiedTerminal()
	}
	uikit.Meta(c, locales.NodeExtraCaCertsAdds())
}

func (a *app) clientChromePanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	addr := cmp.Or(p.Addr, "127.0.0.1:7890")
	uikit.Hint(c, locales.OpensChromeWithATemporary())
	ui.Text(c, "--user-data-dir=<temporary profile>\n--proxy-server=http://"+addr).
		Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable().MaxLines(2)
	if uikit.PrimaryButton(c, locales.OpenChrome()).Disabled(!running).Clicked() {
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
	ui.Text(c, "network.proxy.type=1 · HTTP/SSL "+addr+"\nsecurity.enterprise_roots.enabled=true").
		Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable().MaxLines(2)
	if uikit.PrimaryButton(c, locales.OpenFirefox()).Disabled(!running).Clicked() {
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
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Wrap().Children(func() {
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
		ui.Text(c, s.androidProblem).FontSize(uikit.TextMeta).TextColor(t.Danger).MaxLines(2).Tooltip(s.androidProblem)
	case len(s.android) == 0:
		uikit.Meta(c, locales.StartAnEmulatorOrConnect())
	}
	_, applied := s.androidApplied[s.androidSerial]
	ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
		if uikit.PrimaryButton(c, locales.SetProxy()).Disabled(!running || s.androidSerial == "" || applied || s.androidPending).Clicked() {
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
	})
	uikit.Meta(c, locales.UsesAdbReverseSo127())
	uikit.Meta(c, locales.TheCaIsCopiedTo())
}

func (a *app) clientIOSPanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	if runtime.GOOS != "darwin" {
		uikit.Meta(c, locales.IosSimulatorsRunOnlyOn())
		return
	}
	uikit.Hint(c, locales.AddsThePublicCaTo())
	ui.Text(c, "xcrun simctl keychain booted add-root-cert "+cmp.Or(p.CAPath, "<CA>")).
		Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable().MaxLines(2)
	if uikit.PrimaryButton(c, locales.InstallCaInBootedSimulator()).Disabled(!running).Clicked() {
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
		ui.Text(c, cmp.Or(s.javaProblem, "JDK cacerts are unavailable.")).FontSize(uikit.TextMeta).TextColor(t.Danger).MaxLines(3).Tooltip(s.javaProblem)
		return
	}
	commands, err := clientsetup.JavaTruststoreCommands(p, keystore, s.javaCACerts)
	if err != nil {
		ui.Text(c, err.Error()).FontSize(uikit.TextMeta).TextColor(t.Danger).MaxLines(3).Tooltip(err.Error())
		return
	}
	command := clientsetup.JoinCommands(commands, runtime.GOOS == "windows")
	uikit.Hint(c, locales.CopiesTheJdkSCacerts())
	uikit.Meta(c, "JDK cacerts: "+s.javaCACerts).Font(uikit.FontMono).Selectable().MaxLines(2).Tooltip(s.javaCACerts)
	uikit.Meta(c, "Truststore: "+keystore).Font(uikit.FontMono).Selectable().MaxLines(2).Tooltip(keystore)
	ui.Text(c, command).Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable().MaxLines(8)
	ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
		if uikit.SecondaryButton(c, locales.CopyCommands()).Clicked() {
			c.Services().WriteClipboard(command)
			a.done(locales.CopiedKeytoolCommands())
		}
		if uikit.PrimaryButton(c, locales.ImportText()).Disabled(!running).Clicked() {
			s.javaConfirm = true
		}
	})
	if s.javaConfirm {
		uikit.Hint(c, locales.ReplaceOnlyThisAppS())
		ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
			if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
				s.javaConfirm = false
			}
			if uikit.PrimaryButton(c, locales.ImportCa()).Clicked() {
				s.javaConfirm = false
				a.importJavaCA()
			}
		})
	}
	uikit.Meta(c, locales.TheTruststoreStartsAsA())
	args, err := clientsetup.JVMArgs(p, keystore)
	if err == nil {
		argsText := clientsetup.POSIXCommandLine("java", args)
		if runtime.GOOS == "windows" {
			argsText = clientsetup.WindowsCommandLine("java", args)
		}
		ui.Text(c, argsText).Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable().MaxLines(3)
	}
}

func (a *app) clientElectronPanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	s := &a.clientUI
	ui.Field(c, locales.App(), func() {
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			uikit.Input(c, &s.electronPath).Label(locales.ElectronApp()).Placeholder("/Applications/Slack.app or /usr/bin/app").Font(uikit.FontMono).Grow(1).MinWidth(160)
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
	ui.Text(c, "--proxy-server=http://"+addr).Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable().MaxLines(1)
	if uikit.PrimaryButton(c, locales.LaunchWithProxy()).Disabled(!running || strings.TrimSpace(s.electronPath) == "").Clicked() {
		a.launchElectronApp()
	}
	uikit.Meta(c, locales.NeverDisablesCertificateVerificationCertificate())
}

func (a *app) clientSetupExamples(c *ui.Context, t *ui.Theme) {
	s := &a.clientUI
	p := a.clientProxy()
	_, height := c.Size()
	scrollHeight := max(float32(120), min(float32(320), height-220))
	ui.Form(c, func() {
		ui.Field(c, locales.ClientRuntime(), func() {
			uikit.Select(c, &s.runtime, []string{"cURL", "Node.js", "Python", "Go"}).Label(locales.ClientRuntime()).Width(150)
		})
		ui.Field(c, locales.SetupHttpsUrl(), func() {
			uikit.Input(c, &s.url).Label(locales.SetupHttpsUrl())
		})
	})
	ui.Scroll(c).MaxHeight(scrollHeight).MinHeight(0).Gap(t.Space(3)).Padding(0, t.Space(2), 0, 0).Children(func() {
		addr := p.Addr
		uikit.Body(c, "Proxy listener: "+cmp.Or(addr, "not running"))
		uikit.Meta(c, locales.TheExampleTrustsOnlyThe())
		example := clientExample(s.runtime, addr, p.CAPath, s.url)
		if uikit.SecondaryButton(c, locales.CopyClientExample()).Disabled(addr == "" || p.CAPath == "").Clicked() {
			c.Services().WriteClipboard(example)
			a.done(locales.CopiedClientSetup(s.runtime))
		}
		ui.Text(c, example).Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable()
		uikit.Meta(c, locales.RunItThenUseDiagnose())
	})
}
