package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/clientsetup"
)

// One-click targets. The labels are the picker's values, so a target can be
// opened from a command without a second identifier.
const (
	clientTargetTerminal = "Terminal"
	clientTargetChrome   = "Chrome"
	clientTargetFirefox  = "Firefox"
	clientTargetAndroid  = "Android emulator"
	clientTargetIOS      = "iOS simulator"
	clientTargetJava     = "Java"
	clientTargetElectron = "Electron app"
)

var clientTargets = []string{
	clientTargetTerminal, clientTargetChrome, clientTargetFirefox,
	clientTargetAndroid, clientTargetIOS, clientTargetJava, clientTargetElectron,
}

// clientSetup is the client and device setup dialog. One-click actions change
// only the launched process tree, a temporary browser profile, an emulator's
// reversible proxy setting, a simulator keychain or this app's own Java
// truststore.
type clientSetup struct {
	open   bool
	tab    int // 0 one-click, 1 copyable examples
	target string
	// runtime and url belong to the examples tab.
	runtime string
	url     string

	// addr and caPath are the last listener and CA shown. They survive a
	// stopped listener so commands remain reviewable, while actions stay
	// disabled until capture runs again.
	addr, caPath string

	android        []clientsetup.AndroidDevice
	androidSerial  string
	androidLoaded  bool
	androidLoading bool
	androidProblem string

	javaConfirm bool

	electronPath string
	last         string
	problem      string

	// result carries work done off the UI thread back to drainClientSetup.
	result chan clientSetupResult
}

type clientSetupResult struct {
	action  string // devices, launch, android, undo, push, ios, java
	what    string
	command string
	err     error
	warning error
	devices []clientsetup.AndroidDevice
}

type clientLaunch struct {
	command clientsetup.Argv
	warning error
}

func (a *app) openClientSetup() {
	s := &a.clientUI
	s.open = true
	if s.tab < 0 || s.tab > 1 {
		s.tab = 0
	}
	if s.target == "" {
		s.target = clientTargetTerminal
	}
	if s.runtime == "" {
		s.runtime = "cURL"
	}
	if s.url == "" {
		s.url = "https://example.com/"
	}
	if a.srv != nil && a.srv.Running() {
		s.addr = a.srv.Addr()
	}
	if a.caPath != "" {
		s.caPath = a.caPath
	}
	_ = a.clientSetupResults()
}

// openClientSetupTarget opens the dialog on one one-click target, for the
// command palette and the device panel.
func (a *app) openClientSetupTarget(target string) {
	a.openClientSetup()
	a.clientUI.tab = 0
	a.clientUI.target = target
	if target == clientTargetAndroid && !a.clientUI.androidLoaded {
		a.loadAndroidDevices()
	}
}

// clientProxy is the address and CA the dialog shows. A stopped listener keeps
// the last known values for review; only clientProxyReady permits an action.
func (a *app) clientProxy() clientsetup.Proxy {
	s := &a.clientUI
	if a.srv != nil && a.srv.Running() {
		s.addr = a.srv.Addr()
	}
	if a.caPath != "" {
		s.caPath = a.caPath
	}
	return clientsetup.Proxy{Addr: s.addr, CAPath: s.caPath}
}

func (a *app) clientProxyReady() (clientsetup.Proxy, error) {
	p := a.clientProxy()
	if a.srv == nil || !a.srv.Running() {
		return p, errors.New("start capture first")
	}
	if p.Addr == "" {
		return p, errors.New("the proxy address is not available")
	}
	if p.CAPath == "" {
		return p, errors.New("the CA path is not available")
	}
	if _, err := os.Stat(p.CAPath); err != nil {
		return p, fmt.Errorf("CA file: %w", err)
	}
	return p, nil
}

func (a *app) clientSetupResults() chan clientSetupResult {
	s := &a.clientUI
	if s.result == nil {
		s.result = make(chan clientSetupResult, 8)
	}
	return s.result
}

// drainClientSetup applies results from terminal, browser, adb, simctl and
// keytool work. It runs before the frame is drawn, on the UI thread.
func (a *app) drainClientSetup() {
	s := &a.clientUI
	if s.result == nil {
		return
	}
	for {
		select {
		case r := <-s.result:
			switch r.action {
			case "devices":
				s.androidLoading, s.androidLoaded = false, true
				if r.err != nil {
					s.android, s.androidProblem, s.androidSerial = nil, r.err.Error(), ""
					a.fail("Find Android devices", r.err)
					continue
				}
				s.android, s.androidProblem = r.devices, ""
				if !slices.ContainsFunc(s.android, func(d clientsetup.AndroidDevice) bool { return d.Serial == s.androidSerial }) {
					s.androidSerial = ""
					for _, d := range s.android {
						if d.State == "device" {
							s.androidSerial = d.Serial
							break
						}
					}
					if s.androidSerial == "" && len(s.android) > 0 {
						s.androidSerial = s.android[0].Serial // show why it cannot be used
					}
				}
			default:
				if r.err != nil {
					s.problem = r.err.Error()
					a.fail(r.what, r.err)
					continue
				}
				s.problem = ""
				if r.command != "" {
					s.last = r.command
				}
				if r.warning != nil {
					a.notify(notice{level: noticeWarning, text: r.what, detail: r.warning.Error()})
				} else {
					a.notify(notice{level: noticeSuccess, text: r.what, detail: r.command})
				}
			}
		default:
			return
		}
	}
}

func (a *app) launchClient(what string, f func(clientsetup.Proxy) (clientLaunch, error)) {
	p, err := a.clientProxyReady()
	if err != nil {
		a.fail(what, err)
		return
	}
	results := a.clientSetupResults()
	go func() {
		launch, err := f(p)
		if err == nil {
			err = launch.command.Start()
		}
		results <- clientSetupResult{
			action:  "launch",
			what:    what,
			command: commandString(launch.command),
			err:     err,
			warning: launch.warning,
		}
		a.invalidate()
	}()
}

// commandString renders a command for a notification, or nothing when the
// launcher failed before it could build one.
func commandString(command clientsetup.Argv) string {
	if command.Name == "" {
		return ""
	}
	return command.String(runtime.GOOS == "windows")
}

func (a *app) openProxiedTerminal() {
	a.launchClient("Opened proxied terminal", func(p clientsetup.Proxy) (clientLaunch, error) {
		command, err := clientsetup.OpenTerminal(p)
		return clientLaunch{command: command}, err
	})
}

func (a *app) openProxiedChrome() {
	a.launchClient("Opened proxied Chrome", func(p clientsetup.Proxy) (clientLaunch, error) {
		profile, err := os.MkdirTemp("", "vscroxy-chrome-*")
		if err != nil {
			return clientLaunch{}, err
		}
		if err := clientsetup.PrepareChromeProfile(profile); err != nil {
			_ = os.RemoveAll(profile)
			return clientLaunch{}, err
		}
		command, err := clientsetup.OpenChrome(p, profile)
		if err != nil {
			_ = os.RemoveAll(profile)
			return clientLaunch{}, err
		}
		return clientLaunch{command: command}, nil
	})
}

func (a *app) openProxiedFirefox() {
	a.launchClient("Opened proxied Firefox", func(p clientsetup.Proxy) (clientLaunch, error) {
		profile, err := os.MkdirTemp("", "vscroxy-firefox-*")
		if err != nil {
			return clientLaunch{}, err
		}
		warning, err := clientsetup.PrepareFirefoxProfile(p, profile)
		if err != nil {
			_ = os.RemoveAll(profile)
			return clientLaunch{}, err
		}
		command, err := clientsetup.OpenFirefox(profile)
		if err != nil {
			_ = os.RemoveAll(profile)
			return clientLaunch{}, err
		}
		return clientLaunch{command: command, warning: warning}, nil
	})
}

func (a *app) launchElectronApp() {
	path := strings.TrimSpace(a.clientUI.electronPath)
	if path == "" {
		a.warn("Choose an Electron app first")
		return
	}
	a.launchClient("Launched Electron app", func(p clientsetup.Proxy) (clientLaunch, error) {
		command, err := clientsetup.OpenElectron(path, p)
		return clientLaunch{command: command}, err
	})
}

func (a *app) loadAndroidDevices() {
	s := &a.clientUI
	if s.androidLoading {
		return
	}
	s.androidLoading, s.androidProblem = true, ""
	results := a.clientSetupResults()
	go func() {
		devices, err := clientsetup.AndroidDevices()
		results <- clientSetupResult{action: "devices", devices: devices, err: err}
		a.invalidate()
	}()
}

func (s *clientSetup) androidLabels() []string {
	labels := make([]string, len(s.android))
	for i, device := range s.android {
		labels[i] = device.Label()
	}
	return labels
}

func (s *clientSetup) androidChoice() string {
	for _, device := range s.android {
		if device.Serial == s.androidSerial {
			return device.Label()
		}
	}
	return ""
}

func (s *clientSetup) setAndroidChoice(label string) {
	for _, device := range s.android {
		if device.Label() == label {
			s.androidSerial = device.Serial
			return
		}
	}
}

func (a *app) applyAndroidProxy() {
	s := &a.clientUI
	p, err := a.clientProxyReady()
	if err != nil {
		a.fail("Set Android proxy", err)
		return
	}
	_, port, err := p.HostPort()
	if err != nil {
		a.fail("Set Android proxy", err)
		return
	}
	if s.androidSerial == "" {
		a.warn("Choose a connected Android device or emulator")
		return
	}
	for _, device := range s.android {
		if device.Serial == s.androidSerial && device.State != "device" {
			a.warn("Android device " + device.Serial + " is " + device.State + "; authorize or reconnect it first")
			return
		}
	}
	serial := s.androidSerial
	results := a.clientSetupResults()
	go func() {
		err := clientsetup.ApplyAndroidProxy(serial, port)
		commands, _ := clientsetup.AndroidProxyCommands(serial, port)
		results <- clientSetupResult{
			action:  "android",
			what:    "Set Android proxy",
			command: clientsetup.JoinCommands(commands, runtime.GOOS == "windows"),
			err:     err,
		}
		a.invalidate()
	}()
}

func (a *app) clearAndroidProxy() {
	s := &a.clientUI
	p := a.clientProxy()
	_, port, err := p.HostPort()
	if err != nil {
		a.fail("Undo Android proxy", err)
		return
	}
	if s.androidSerial == "" {
		a.warn("Choose a connected Android device or emulator")
		return
	}
	serial := s.androidSerial
	results := a.clientSetupResults()
	go func() {
		err := clientsetup.ClearAndroidProxy(serial, port)
		commands, _ := clientsetup.AndroidUndoCommands(serial, port)
		results <- clientSetupResult{
			action:  "undo",
			what:    "Undid Android proxy",
			command: clientsetup.JoinCommands(commands, runtime.GOOS == "windows"),
			err:     err,
		}
		a.invalidate()
	}()
}

func (a *app) pushAndroidCA() {
	s := &a.clientUI
	p, err := a.clientProxyReady()
	if err != nil {
		a.fail("Copy CA to Android device", err)
		return
	}
	if s.androidSerial == "" {
		a.warn("Choose a connected Android device or emulator")
		return
	}
	serial := s.androidSerial
	command, err := clientsetup.AndroidCACopyCommand(serial, p.CAPath)
	if err != nil {
		a.fail("Copy CA to Android device", err)
		return
	}
	results := a.clientSetupResults()
	go func() {
		err := command.Run()
		results <- clientSetupResult{
			action:  "push",
			what:    "Copied CA to Android Downloads",
			command: commandString(command),
			err:     err,
		}
		a.invalidate()
	}()
}

func (a *app) installSimulatorCA() {
	p, err := a.clientProxyReady()
	if err != nil {
		a.fail("Install CA in iOS simulator", err)
		return
	}
	results := a.clientSetupResults()
	go func() {
		command, err := clientsetup.AddSimulatorCA(p.CAPath)
		if err == nil {
			err = command.Run()
		}
		results <- clientSetupResult{
			action:  "ios",
			what:    "Installed CA in iOS simulator",
			command: commandString(command),
			err:     err,
		}
		a.invalidate()
	}()
}

func (a *app) javaTruststorePath() string {
	if a.settingsPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(a.settingsPath), "java", "truststore.p12")
}

func (a *app) importJavaCA() {
	p, err := a.clientProxyReady()
	if err != nil {
		a.fail("Import CA into Java truststore", err)
		return
	}
	keystore := a.javaTruststorePath()
	if keystore == "" {
		a.fail("Import CA into Java truststore", errors.New("the settings directory is unknown"))
		return
	}
	results := a.clientSetupResults()
	go func() {
		command, err := clientsetup.ImportJavaCA(p, keystore)
		results <- clientSetupResult{
			action:  "java",
			what:    "Imported CA into Java truststore",
			command: commandString(command),
			err:     err,
		}
		a.invalidate()
	}()
}

// Examples configure only the requesting process. They never install a root
// certificate, change the system proxy, or disable certificate verification.
func clientExample(runtime, addr, ca, target string) string {
	proxy := "http://" + addr
	switch runtime {
	case "Python":
		return fmt.Sprintf("# Requires requests: python -m pip install requests\nimport requests\ns = requests.Session()\ns.trust_env = False\nr = s.get(%q, proxies={\"http\": %q, \"https\": %q}, verify=%q, timeout=30)\nprint(r.status_code, r.text)", target, proxy, proxy, ca)
	case "Node.js":
		return fmt.Sprintf("// npm install https-proxy-agent\nimport https from 'node:https';\nimport {readFileSync} from 'node:fs';\nimport {HttpsProxyAgent} from 'https-proxy-agent';\nhttps.get(%q, {agent: new HttpsProxyAgent(%q), ca: readFileSync(%q)}, res => {\n  console.log(res.statusCode);\n  res.pipe(process.stdout);\n}).on('error', console.error);", target, proxy, ca)
	case "Go":
		return fmt.Sprintf("package main\nimport (\"crypto/tls\"; \"crypto/x509\"; \"fmt\"; \"io\"; \"net/http\"; \"net/url\"; \"os\"; \"time\")\nfunc main() {\n  pem, err := os.ReadFile(%q); if err != nil { panic(err) }\n  roots, err := x509.SystemCertPool(); if err != nil { roots = x509.NewCertPool() }\n  if !roots.AppendCertsFromPEM(pem) { panic(\"invalid CA file\") }\n  proxy, err := url.Parse(%q); if err != nil { panic(err) }\n  client := &http.Client{Timeout: 30*time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: roots}}}\n  res, err := client.Get(%q); if err != nil { panic(err) }; defer res.Body.Close()\n  fmt.Println(res.Status); io.Copy(os.Stdout, res.Body)\n}", ca, proxy, target)
	default:
		return "curl --noproxy '' --proxy " + shellQuote(proxy) + " --cacert " + shellQuote(ca) + " --url " + shellQuote(target)
	}
}
