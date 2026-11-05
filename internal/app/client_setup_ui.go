package app

import (
	"cmp"
	"runtime"
	"strings"

	"github.com/baba983/vscroxy/internal/clientsetup"
	"github.com/baba983/vscroxy/internal/uikit"
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
				uikit.DialogTitle(c, "Set up a local client")
				ui.Spacer(c)
				a.clientSetupBadge(c, t)
			})
			uikit.Tabs(c, &s.tab, "Client setup", "One-click", "Examples")
			if s.tab == 0 {
				a.clientSetupOneClick(c, t)
			} else {
				a.clientSetupExamples(c, t)
			}
			if s.problem != "" {
				ui.Text(c, s.problem).FontSize(uikit.TextBody).TextColor(t.Danger).MaxLines(2).Tooltip(s.problem)
			}
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Phone / tablet setup…").Clicked() {
					s.open = false
					a.openDevices()
				}
				if uikit.SecondaryButton(c, "Diagnose…").Clicked() {
					s.open = false
					a.diagnostics.target = s.url
					a.openDiagnostics(nil, diagnoseProcess)
				}
				if uikit.SecondaryButton(c, "Close").Clicked() {
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
		ui.Field(c, "Client", func() {
			uikit.Select(c, &s.target, clientTargets).Label("Client").Width(200)
		})
	})
	if !running && (s.target != clientTargetIOS || runtime.GOOS == "darwin") {
		uikit.Meta(c, "Start capture first; these actions stay disabled until the proxy is listening.")
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
			uikit.Hint(c, "Last command")
			ui.Text(c, s.last).Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable().MaxLines(3)
		}
	})
}

func (a *app) clientTerminalPanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	uikit.Hint(c, "Opens a new terminal window with the proxy and CA environment. Only that window changes.")
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
		ui.Text(c, "System CA bundle unavailable: only NODE_EXTRA_CA_CERTS is set, so undecrypted hosts use their own default trust.").
			FontSize(uikit.TextMeta).TextColor(t.Warning).MaxLines(2)
	} else {
		uikit.Meta(c, "SSL_CERT_FILE, REQUESTS_CA_BUNDLE, CURL_CA_BUNDLE, GIT_SSL_CAINFO, PIP_CERT and DENO_CERT point at "+p.BundlePath+", a 0600 bundle of the system roots plus the vscroxy CA. Undecrypted hosts keep working.")
	}
	if uikit.PrimaryButton(c, "Open terminal").Disabled(!running).Clicked() {
		a.openProxiedTerminal()
	}
	uikit.Meta(c, "NODE_EXTRA_CA_CERTS adds the vscroxy CA to Node's default roots.")
}

func (a *app) clientChromePanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	addr := cmp.Or(p.Addr, "127.0.0.1:7890")
	uikit.Hint(c, "Opens Chrome with a temporary profile and --proxy-server. Your main profile is untouched.")
	ui.Text(c, "--user-data-dir=<temporary profile>\n--proxy-server=http://"+addr).
		Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable().MaxLines(2)
	if uikit.PrimaryButton(c, "Open Chrome").Disabled(!running).Clicked() {
		a.openProxiedChrome()
	}
	switch runtime.GOOS {
	case "linux":
		uikit.Meta(c, "Chrome on Linux reads NSS, not the CA file. Trust the CA system-wide in Settings → HTTPS, or add it with certutil; no TLS bypass is used.")
	default:
		uikit.Meta(c, "Chrome uses this system's trust store. Trust the CA in Settings → HTTPS first; no TLS bypass is used.")
	}
}

func (a *app) clientFirefoxPanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	addr := cmp.Or(p.Addr, "127.0.0.1:7890")
	uikit.Hint(c, "Opens Firefox with a temporary profile whose user.js points at the proxy. Your main profile is untouched.")
	ui.Text(c, "network.proxy.type=1 · HTTP/SSL "+addr+"\nsecurity.enterprise_roots.enabled=true").
		Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable().MaxLines(2)
	if uikit.PrimaryButton(c, "Open Firefox").Disabled(!running).Clicked() {
		a.openProxiedFirefox()
	}
	if runtime.GOOS == "windows" {
		uikit.Meta(c, "Firefox uses the Windows root store through enterprise roots. Trust the CA in Settings → HTTPS first; no TLS bypass is used.")
	} else {
		uikit.Meta(c, "The temporary profile trusts the CA through certutil when NSS tools are installed; otherwise Firefox may use this system's enterprise roots. No TLS bypass is used.")
	}
}

func (a *app) clientAndroidPanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	s := &a.clientUI
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Wrap().Children(func() {
		if len(s.android) > 0 {
			choice := s.androidChoice()
			uikit.Select(c, &choice, s.androidLabels()).Label("Android device").Width(280)
			s.setAndroidChoice(choice)
		} else {
			uikit.Meta(c, "No Android device or emulator")
		}
		if uikit.SecondaryButton(c, "Refresh").Clicked() {
			s.androidLoaded = false
			a.loadAndroidDevices()
		}
	})
	switch {
	case s.androidLoading:
		uikit.Meta(c, "Looking for adb devices…")
	case s.androidProblem != "":
		ui.Text(c, s.androidProblem).FontSize(uikit.TextMeta).TextColor(t.Danger).MaxLines(2).Tooltip(s.androidProblem)
	case len(s.android) == 0:
		uikit.Meta(c, "Start an emulator or connect a device with USB debugging, then Refresh.")
	}
	ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
		if uikit.PrimaryButton(c, "Set proxy").Disabled(!running || s.androidSerial == "").Clicked() {
			a.applyAndroidProxy()
		}
		if uikit.SecondaryButton(c, "Undo proxy").Disabled(s.androidSerial == "" || p.Addr == "").Clicked() {
			a.clearAndroidProxy()
		}
		if uikit.SecondaryButton(c, "Copy CA to device").Disabled(!running || s.androidSerial == "").Clicked() {
			a.pushAndroidCA()
		}
	})
	uikit.Meta(c, "Uses adb reverse, so 127.0.0.1 inside the device reaches this computer. Undo restores the proxy value that was set before vscroxy changed it, and removes the reverse listener it added. Apps that ignore the system proxy or pin certificates need app-side changes.")
	uikit.Meta(c, "The CA is copied to Downloads only. Android 7+ apps do not trust user-installed CAs; installing it and enabling trust is a device-side step. System-store installation needs a rooted, writable-system emulator image and is not automated.")
}

func (a *app) clientIOSPanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	if runtime.GOOS != "darwin" {
		uikit.Meta(c, "iOS simulators run only on macOS. This build cannot install the CA.")
		return
	}
	uikit.Hint(c, "Adds the public CA to the booted simulator's root store. It does not configure a physical iPhone; enable trust in the simulator's Certificate Trust Settings if asked.")
	ui.Text(c, "xcrun simctl keychain booted add-root-cert "+cmp.Or(p.CAPath, "<CA>")).
		Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable().MaxLines(2)
	if uikit.PrimaryButton(c, "Install CA in booted simulator").Disabled(!running).Clicked() {
		a.installSimulatorCA()
	}
}

func (a *app) clientJavaPanel(c *ui.Context, t *ui.Theme, p clientsetup.Proxy, running bool) {
	s := &a.clientUI
	keystore := a.javaTruststorePath()
	if keystore == "" {
		uikit.Meta(c, "The settings directory is unknown, so no truststore path is available.")
		return
	}
	if s.javaCACerts == "" {
		plan, err := clientsetup.JavaTruststorePlan(p, keystore)
		if err != nil {
			ui.Text(c, err.Error()).FontSize(uikit.TextMeta).TextColor(t.Danger).MaxLines(3).Tooltip(err.Error())
			return
		}
		s.javaCACerts = plan.CACerts
	}
	commands, err := clientsetup.JavaTruststoreCommands(p, keystore, s.javaCACerts)
	if err != nil {
		ui.Text(c, err.Error()).FontSize(uikit.TextMeta).TextColor(t.Danger).MaxLines(3).Tooltip(err.Error())
		return
	}
	command := clientsetup.JoinCommands(commands, runtime.GOOS == "windows")
	uikit.Hint(c, "Copies the JDK's cacerts into this app's own truststore, then adds the public CA. No JDK files change.")
	uikit.Meta(c, "JDK cacerts: "+s.javaCACerts).Font(uikit.FontMono).Selectable().MaxLines(2).Tooltip(s.javaCACerts)
	uikit.Meta(c, "Truststore: "+keystore).Font(uikit.FontMono).Selectable().MaxLines(2).Tooltip(keystore)
	ui.Text(c, command).Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable().MaxLines(8)
	ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
		if uikit.SecondaryButton(c, "Copy commands").Clicked() {
			c.Services().WriteClipboard(command)
			a.done("Copied keytool commands")
		}
		if uikit.PrimaryButton(c, "Import…").Disabled(!running).Clicked() {
			s.javaConfirm = true
		}
	})
	if s.javaConfirm {
		uikit.Hint(c, "Replace only this app's truststore? The old file is kept if any keytool step fails. The displayed commands write a temporary file first.")
		ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
			if uikit.SecondaryButton(c, "Cancel").Clicked() {
				s.javaConfirm = false
			}
			if uikit.PrimaryButton(c, "Import CA").Clicked() {
				s.javaConfirm = false
				a.importJavaCA()
			}
		})
	}
	uikit.Meta(c, "The truststore starts as a copy of the JDK's cacerts, so public TLS keeps working. The JVM options below also point the JVM at the proxy.")
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
	ui.Field(c, "App", func() {
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			uikit.Input(c, &s.electronPath).Label("Electron app").Placeholder("/Applications/Slack.app or /usr/bin/app").Font(uikit.FontMono).Grow(1).MinWidth(160)
			if uikit.SecondaryButton(c, "Choose app…").Clicked() && a.win != nil {
				win := a.win
				go func() {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
						Title:                      "Choose an Electron app",
						Directory:                  runtime.GOOS == "darwin",
						TreatPackagesAsDirectories: false,
					})
					win.Update(func() {
						if err != nil {
							a.fail("Choose Electron app", err)
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
	if uikit.PrimaryButton(c, "Launch with proxy").Disabled(!running || strings.TrimSpace(s.electronPath) == "").Clicked() {
		a.launchElectronApp()
	}
	uikit.Meta(c, "Never disables certificate verification. Certificate trust comes from the system store on macOS/Windows and NSS on Linux. Apps that set their own proxy or pin certificates can ignore the flag.")
}

func (a *app) clientSetupExamples(c *ui.Context, t *ui.Theme) {
	s := &a.clientUI
	p := a.clientProxy()
	_, height := c.Size()
	scrollHeight := max(float32(120), min(float32(320), height-220))
	ui.Form(c, func() {
		ui.Field(c, "Client runtime", func() {
			uikit.Select(c, &s.runtime, []string{"cURL", "Node.js", "Python", "Go"}).Label("Client runtime").Width(150)
		})
		ui.Field(c, "Setup HTTPS URL", func() {
			uikit.Input(c, &s.url).Label("Setup HTTPS URL")
		})
	})
	ui.Scroll(c).MaxHeight(scrollHeight).MinHeight(0).Gap(t.Space(3)).Padding(0, t.Space(2), 0, 0).Children(func() {
		addr := p.Addr
		uikit.Body(c, "Proxy listener: "+cmp.Or(addr, "not running"))
		uikit.Meta(c, "The example trusts only the public CA file for this client. No system changes; no insecure TLS bypass. cURL commands use a POSIX shell.")
		example := clientExample(s.runtime, addr, p.CAPath, s.url)
		if uikit.SecondaryButton(c, "Copy client example").Disabled(addr == "" || p.CAPath == "").Clicked() {
			c.Services().WriteClipboard(example)
			a.done("Copied " + s.runtime + " setup; review URL and CA path before running")
		}
		ui.Text(c, example).Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable()
		uikit.Meta(c, "Run it, then use Diagnose to see whether the request arrived and was decrypted.")
	})
}
