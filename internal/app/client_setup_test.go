package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/clientsetup"
	"github.com/egoist/mygo/ui"
)

func oneClickApp(t *testing.T) *app {
	t.Helper()
	a := workspaceApp()
	dir := t.TempDir()
	srv, err := capture.New("127.0.0.1:0", dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	a.srv = srv
	a.caPath = filepath.Join(dir, "vscroxy-ca-cert.pem")
	a.settingsPath = filepath.Join(dir, ".vscroxy", "settings.json")
	a.clientUI = clientSetup{open: true, tab: 0, target: clientTargetTerminal}
	return a
}

func TestOneClickTerminalShowsTheEnvironmentAndOnlyThatWindow(t *testing.T) {
	a := oneClickApp(t)
	tt := ui.NewTester(a.view, 960, 600)
	for _, want := range []string{
		"Open terminal",
		"HTTP_PROXY=http://" + a.srv.Addr(),
		"NODE_EXTRA_CA_CERTS=" + a.caPath,
		"SSL_CERT_FILE=" + a.clientBundlePath(),
		"REQUESTS_CA_BUNDLE=" + a.clientBundlePath(),
		"NO_PROXY=" + a.srv.Addr(),
		"Only that window changes",
		"system roots",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	if tt.HasText("ALL_PROXY") || tt.HasText("all_proxy") {
		t.Fatal("terminal setup routes every protocol to the HTTP proxy")
	}
	if tt.HasText("--ignore-certificate-errors") {
		t.Fatal("terminal setup mentions a TLS bypass")
	}
}

func TestClientSetupMakesStartCaptureTheStoppedPrimary(t *testing.T) {
	a := workspaceApp()
	a.srv = &capture.Server{}
	a.clientUI = clientSetup{open: true, tab: 0, target: clientTargetTerminal}
	tt := ui.NewTester(a.view, 960, 600)
	if !tt.HasText("Start capture") {
		t.Fatal("a stopped client setup has no Start capture action")
	}
}

func TestClientSetupHandsThePrimaryToTheRunningPanel(t *testing.T) {
	a := oneClickApp(t)
	tt := ui.NewTester(a.view, 960, 600)
	if tt.HasText("Start capture") {
		t.Fatal("a running client setup still offers Start capture")
	}
	if !tt.HasText("Open terminal") {
		t.Fatal("a running terminal panel lost its action")
	}
}

func TestOneClickChromeAndFirefoxUseSeparateProfiles(t *testing.T) {
	a := oneClickApp(t)
	a.clientUI.target = clientTargetChrome
	tt := ui.NewTester(a.view, 960, 600)
	if !tt.HasText("--proxy-server=http://"+a.srv.Addr()) || !tt.HasText("temporary profile") {
		t.Fatalf("Chrome setup missing: %v", tt.Texts())
	}
	if tt.HasText("ignore-certificate-errors") {
		t.Fatal("Chrome setup offers a TLS bypass")
	}
	a.clientUI.target = clientTargetFirefox
	tt = ui.NewTester(a.view, 960, 600)
	if !tt.HasText("user.js") || !tt.HasText("Your main profile is untouched") {
		t.Fatalf("Firefox setup missing: %v", tt.Texts())
	}
}

func TestOneClickAndroidShowsReverseProxyAndCertificateLimits(t *testing.T) {
	a := oneClickApp(t)
	a.clientUI.target = clientTargetAndroid
	a.clientUI.androidLoaded = true
	a.clientUI.android = []clientsetup.AndroidDevice{{Serial: "emulator-5554", State: "device", Model: "Pixel_7"}}
	a.clientUI.androidSerial = "emulator-5554"
	tt := ui.NewTester(a.view, 960, 600)
	for _, want := range []string{
		"emulator-5554 · Pixel_7 · device",
		"Set proxy",
		"Undo proxy",
		"Forget saved proxy",
		"Copy CA to device",
		"Install system CA",
		"Network security config",
		"adb reverse",
		"Undo restores the proxy value",
		"Apps that target Android 7+ do not trust user certificates",
		"Installing a system CA runs adb root",
		"Chrome 115 and later uses its own root store",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	// The snippet stays hidden until asked for, then shows the debug resource.
	if tt.HasText("vscroxy_ca") {
		t.Fatalf("the snippet was shown before it was requested: %v", tt.Texts())
	}
	if err := tt.Click("Network security config"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"vscroxy_ca", "Copy snippet", "app/src/debug/res/raw/vscroxy_ca.pem"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q after showing the snippet: %v", want, tt.Texts())
		}
	}
}

func TestOneClickJavaShowsAReviewableCommandBeforeConfirming(t *testing.T) {
	a := oneClickApp(t)
	a.clientUI.target = clientTargetJava
	a.clientUI.javaCACerts = "/Library/Java/JavaVirtualMachines/jdk-27.jdk/Contents/Home/lib/security/cacerts"
	tt := ui.NewTester(a.view, 960, 600)
	for _, want := range []string{
		"keytool -importkeystore -noprompt",
		"keytool -importcert -noprompt",
		a.javaTruststorePath(),
		"-Dhttp.proxyHost=127.0.0.1",
		"-Dhttps.proxyPort=",
		"mv ",
		".import",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	if tt.HasText("direct public TLS may fail") {
		t.Fatal("Java panel still warns about public TLS")
	}
	if err := tt.Click("Import…"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Replace only this app's truststore") || !tt.HasText("Import CA") {
		t.Fatalf("confirmation missing: %v", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if a.clientUI.javaConfirm {
		t.Fatal("cancel kept the confirmation open")
	}
}

func TestOneClickElectronShowsTheProxyFlagAndNeedsAnApp(t *testing.T) {
	a := oneClickApp(t)
	a.clientUI.target = clientTargetElectron
	tt := ui.NewTester(a.view, 960, 600)
	if !tt.HasText("--proxy-server=http://"+a.srv.Addr()) || !tt.HasText("Launch with proxy") {
		t.Fatalf("Electron setup missing: %v", tt.Texts())
	}
	if tt.HasText("ignore-certificate-errors") {
		t.Fatal("Electron setup offers a TLS bypass")
	}
}

func TestCommandPaletteFindsOneClickSetup(t *testing.T) {
	a := oneClickApp(t)
	a.workbench.openPalette()
	a.workbench.query = "proxied"
	tt := ui.NewTester(a.view, 960, 600)
	for _, want := range []string{"Open a proxied terminal", "Open proxied Chrome", "Open proxied Firefox"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	if strings.Contains(strings.Join(tt.Texts(), "\n"), "ignore-certificate-errors") {
		t.Fatal("palette offers a TLS bypass")
	}
}

func TestOneClickTerminalLaunchesTheGeneratedScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake terminal is a shell script")
	}
	if runtime.GOOS == "darwin" {
		// macOS opens Terminal.app with /usr/bin/open on a .command script.
		// Clicking here would open a real window on the machine running the
		// tests; the command it builds is checked in clientsetup instead.
		t.Skip("launching would open Terminal.app; see clientsetup's darwin launch test")
	}
	a := oneClickApp(t)
	binDir := t.TempDir()
	out := filepath.Join(t.TempDir(), "terminal args")
	fake := filepath.Join(binDir, "xterm")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + clientsetup.POSIXQuote(out) + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("TERMINAL", "")
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Open terminal"); err != nil {
		t.Fatal(err)
	}
	waitAppSignal(t, a, func() bool { return a.clientUI.last != "" }, tt.Frame,
		func() string { return "terminal launch did not finish" })
	if a.clientUI.problem != "" {
		t.Fatalf("launch problem: %s", a.clientUI.problem)
	}
	args, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("terminal did not run: %v", err)
	}
	if !strings.Contains(string(args), "-e") || !strings.Contains(string(args), "vscroxy-terminal") {
		t.Fatalf("unexpected terminal args: %q", args)
	}
	if !strings.Contains(a.clientUI.last, "xterm -e") {
		t.Fatalf("last command: %q", a.clientUI.last)
	}
}

func TestOneClickTerminalMissingHelperIsReported(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS opens Terminal.app with /usr/bin/open, which is always present")
	}
	a := oneClickApp(t)
	t.Setenv("PATH", "")
	t.Setenv("TERMINAL", "")
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Open terminal"); err != nil {
		t.Fatal(err)
	}
	waitAppSignal(t, a, func() bool { return a.message != "" }, tt.Frame,
		func() string { return "missing helper was not reported" })
	if !strings.HasPrefix(a.message, "Open proxied terminal:") || !strings.Contains(a.message, "terminal emulator not found") || !strings.Contains(a.message, "xterm") {
		t.Fatalf("missing terminal was not explained: %q", a.message)
	}
}

func TestOneClickFirefoxKeepsTheProfileAfterTheLauncherExits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Firefox is a shell script")
	}
	a := oneClickApp(t)
	binDir := t.TempDir()
	log := filepath.Join(t.TempDir(), "firefox args")
	fake := filepath.Join(binDir, "firefox")
	// Publish the arguments with one rename. A plain redirect exposes an
	// empty file between truncation and the write, which the test used to
	// mistake for a launcher that had already run.
	script := "#!/bin/sh\n" +
		"tmp=" + clientsetup.POSIXQuote(log+".tmp") + "\n" +
		"printf '%s\\n%s\\n' \"$$\" \"$*\" > \"$tmp\"\n" +
		"/bin/mv -f \"$tmp\" " + clientsetup.POSIXQuote(log) + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("TERMINAL", "")
	a.clientUI.target = clientTargetFirefox
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Open Firefox"); err != nil {
		t.Fatal(err)
	}
	// Wait for the launcher's PID and arguments, which it publishes together.
	deadline := time.Now().Add(20 * time.Second)
	var args string
	var pid int
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(log)
		if err == nil {
			pidLine, argLine, ok := strings.Cut(string(data), "\n")
			pid, err = strconv.Atoi(strings.TrimSpace(pidLine))
			if err == nil && ok {
				args = strings.TrimSuffix(argLine, "\n")
			}
			if args != "" {
				break
			}
		}
		tt.Frame()
		time.Sleep(10 * time.Millisecond)
	}
	if args == "" {
		t.Fatal("fake Firefox did not run")
	}
	fields := strings.Fields(args)
	profile := ""
	for i, field := range fields {
		if field == "-profile" && i+1 < len(fields) {
			profile = fields[i+1]
			break
		}
	}
	if profile == "" {
		t.Fatalf("no profile in %q", args)
	}
	// ESRCH means the launcher has exited and been reaped by the Wait that
	// clientsetup starts. Only then can cleanup after exit have happened.
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find fake Firefox: %v", err)
	}
	exitDeadline := time.Now().Add(20 * time.Second)
	reaped := false
	for time.Now().Before(exitDeadline) {
		err := process.Signal(syscall.Signal(0))
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			reaped = true
			break
		}
		tt.Frame()
		time.Sleep(10 * time.Millisecond)
	}
	if !reaped {
		t.Fatal("fake Firefox did not exit")
	}
	if _, err := os.Stat(filepath.Join(profile, "user.js")); err != nil {
		t.Fatalf("profile was removed after the launcher exited: %v", err)
	}
}

func TestOneClickJavaCachesAMissingTool(t *testing.T) {
	a := oneClickApp(t)
	a.clientUI.target = clientTargetJava
	t.Setenv("PATH", "")
	t.Setenv("JAVA_HOME", "")
	tt := ui.NewTester(a.view, 960, 600)
	tt.Frame()
	if !a.clientUI.javaChecked || a.clientUI.javaProblem == "" || a.clientUI.javaCACerts != "" {
		t.Fatalf("missing keytool was not cached: checked=%v problem=%q cacerts=%q", a.clientUI.javaChecked, a.clientUI.javaProblem, a.clientUI.javaCACerts)
	}
	problem := a.clientUI.javaProblem
	jdk := t.TempDir()
	binDir := filepath.Join(jdk, "bin")
	cacerts := filepath.Join(jdk, "lib", "security", "cacerts")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cacerts), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacerts, []byte("cacerts"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "keytool"), nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("JAVA_HOME", jdk)
	tt = ui.NewTester(a.view, 960, 600)
	tt.Frame()
	if a.clientUI.javaCACerts != "" || a.clientUI.javaProblem != problem {
		t.Fatalf("failed lookup was retried: cacerts=%q problem=%q", a.clientUI.javaCACerts, a.clientUI.javaProblem)
	}
}

func TestAndroidProxyRecordsPersistAcrossRuns(t *testing.T) {
	a := workspaceApp()
	a.androidProxyPath = filepath.Join(t.TempDir(), "android-proxy.json")
	a.clientUI.androidApplied = map[string]androidProxyState{
		"emulator-5554": {Previous: "10.0.2.2:8888", Port: 7890},
	}
	a.saveAndroidProxyRecords()
	info, err := os.Stat(a.androidProxyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", info.Mode().Perm())
	}
	a.clientUI.androidApplied = nil
	a.loadAndroidProxyRecords()
	state, ok := a.clientUI.androidApplied["emulator-5554"]
	if !ok || state.Previous != "10.0.2.2:8888" || state.Port != 7890 {
		t.Fatalf("records: %+v", a.clientUI.androidApplied)
	}
	delete(a.clientUI.androidApplied, "emulator-5554")
	a.saveAndroidProxyRecords()
	a.clientUI.androidApplied = nil
	a.loadAndroidProxyRecords()
	if len(a.clientUI.androidApplied) != 0 {
		t.Fatalf("records not cleared: %+v", a.clientUI.androidApplied)
	}
}

func TestOneClickAndroidClearsRecordWhenReverseIsGone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a shell script")
	}
	a := oneClickApp(t)
	a.androidProxyPath = filepath.Join(t.TempDir(), "android-proxy.json")
	a.clientUI.target = clientTargetAndroid
	a.clientUI.androidLoaded = true
	a.clientUI.android = []clientsetup.AndroidDevice{{Serial: "emulator-5554", State: "device", Model: "Pixel_7"}}
	a.clientUI.androidSerial = "emulator-5554"
	a.clientUI.androidApplied = map[string]androidProxyState{
		"emulator-5554": {Previous: "10.0.2.2:8888", Port: 7890},
	}
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "adb")
	script := "#!/bin/sh\ncase \"$*\" in *\"reverse --remove\"*) echo 'listener not found' >&2; exit 1;; esac\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Undo proxy"); err != nil {
		t.Fatal(err)
	}
	waitAppSignal(t, a, func() bool { return len(a.clientUI.androidApplied) == 0 }, tt.Frame,
		func() string {
			return fmt.Sprintf("stale record survived a missing reverse listener: %+v", a.clientUI.androidApplied)
		})
	if !strings.Contains(a.message, "listener not found") {
		t.Fatalf("missing warning: %q", a.message)
	}
}

func TestOneClickAndroidForgetsSavedProxy(t *testing.T) {
	a := oneClickApp(t)
	a.androidProxyPath = filepath.Join(t.TempDir(), "android-proxy.json")
	a.clientUI.target = clientTargetAndroid
	a.clientUI.androidLoaded = true
	a.clientUI.android = []clientsetup.AndroidDevice{{Serial: "emulator-5554", State: "device", Model: "Pixel_7"}}
	a.clientUI.androidSerial = "emulator-5554"
	a.clientUI.androidApplied = map[string]androidProxyState{
		"emulator-5554": {Previous: "10.0.2.2:8888; rm -rf /", Port: 7890},
	}
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Forget saved proxy"); err != nil {
		t.Fatal(err)
	}
	if len(a.clientUI.androidApplied) != 0 {
		t.Fatalf("record not forgotten: %+v", a.clientUI.androidApplied)
	}
	if !strings.Contains(a.message, "Forgot saved Android proxy") {
		t.Fatalf("missing confirmation: %q", a.message)
	}
}

func TestOneClickAndroidRejectsItsOwnPreviousProxy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a shell script")
	}
	a := oneClickApp(t)
	a.androidProxyPath = filepath.Join(t.TempDir(), "android-proxy.json")
	a.clientUI.androidSerial = "emulator-5554"
	a.clientUI.android = []clientsetup.AndroidDevice{{Serial: "emulator-5554", State: "device", Model: "Pixel_7"}}
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "adb")
	script := "#!/bin/sh\ncase \"$*\" in *\"settings get global http_proxy\"*) echo '" + a.srv.Addr() + "'; esac\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	a.applyAndroidProxy()
	waitAppSignal(t, a, func() bool { return !a.clientUI.androidPending }, a.drainClientSetup,
		func() string { return "reverse-proxy check did not finish" })
	if len(a.clientUI.androidApplied) != 0 {
		t.Fatalf("recorded vscroxy itself as the previous proxy: %+v", a.clientUI.androidApplied)
	}
	if !strings.Contains(a.message, "already points at vscroxy") {
		t.Fatalf("missing refusal: %q", a.message)
	}
}

func TestOneClickAndroidRestoresReverseAfterReboot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a shell script")
	}
	a := oneClickApp(t)
	a.androidProxyPath = filepath.Join(t.TempDir(), "android-proxy.json")
	a.clientUI.target = clientTargetAndroid
	a.clientUI.androidLoaded = true
	a.clientUI.android = []clientsetup.AndroidDevice{{Serial: "emulator-5554", State: "device", Model: "Pixel_7"}}
	a.clientUI.androidSerial = "emulator-5554"
	_, portText, ok := strings.Cut(a.srv.Addr(), ":")
	if !ok {
		t.Fatalf("proxy address %q has no port", a.srv.Addr())
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	const original = "10.0.2.2:8888"
	a.clientUI.androidApplied = map[string]androidProxyState{
		"emulator-5554": {Previous: original, Port: port},
	}
	log := filepath.Join(t.TempDir(), "adb.log")
	if err := os.WriteFile(log, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "adb")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + clientsetup.POSIXQuote(log) + "\n" +
		"case \"$*\" in *\"settings get global http_proxy\"*) echo '" + a.srv.Addr() + "'; esac\n" +
		"exit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Set proxy"); err != nil {
		t.Fatal(err)
	}
	waitAppSignal(t, a, func() bool { return !a.clientUI.androidPending }, a.drainClientSetup,
		func() string { return "reverse-proxy restore did not finish" })
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if want := "-s emulator-5554 reverse tcp:" + portText + " tcp:" + portText; !strings.Contains(string(data), want) {
		t.Fatalf("Set proxy did not restore the missing reverse listener; message=%q record=%+v adb log:\n%s", a.message, a.clientUI.androidApplied, data)
	}
	if strings.Contains(string(data), "settings put global http_proxy 127.0.0.1:") {
		t.Fatalf("Set proxy rewrote a device setting that was already correct:\n%s", data)
	}
	if state := a.clientUI.androidApplied["emulator-5554"]; state.Previous != original || state.Port != port {
		t.Fatalf("saved record changed: %+v", state)
	}
	tt.Frame()
	if err := tt.Click("Undo proxy"); err != nil {
		t.Fatal(err)
	}
	waitAppSignal(t, a, func() bool { return len(a.clientUI.androidApplied) == 0 }, a.drainClientSetup,
		func() string { return "undo did not finish" })
	data, err = os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"-s emulator-5554 shell settings put global http_proxy " + original,
		"-s emulator-5554 reverse --remove tcp:" + portText,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("Undo proxy missed %q; adb log:\n%s", want, data)
		}
	}
}

func TestOneClickAndroidPendingBlocksASecondSet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a shell script")
	}
	a := oneClickApp(t)
	a.androidProxyPath = filepath.Join(t.TempDir(), "android-proxy.json")
	a.clientUI.androidSerial = "emulator-5554"
	a.clientUI.android = []clientsetup.AndroidDevice{{Serial: "emulator-5554", State: "device", Model: "Pixel_7"}}
	log := filepath.Join(t.TempDir(), "adb.log")
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "adb")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + clientsetup.POSIXQuote(log) + "\n" +
		"case \"$*\" in *\"settings get global http_proxy\"*) echo '10.0.2.2:8888'; sleep 0.2;; esac\n" +
		"exit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	a.applyAndroidProxy()
	if !a.clientUI.androidPending {
		t.Fatal("pending flag was not set")
	}
	a.applyAndroidProxy()
	waitAppSignal(t, a, func() bool { return !a.clientUI.androidPending }, a.drainClientSetup,
		func() string { return "proxy setup did not finish" })
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "settings get global http_proxy"); got != 1 {
		t.Fatalf("second Set ran while pending: %d queries\n%s", got, data)
	}
	if state, ok := a.clientUI.androidApplied["emulator-5554"]; !ok || state.Previous != "10.0.2.2:8888" {
		t.Fatalf("record: %+v", a.clientUI.androidApplied)
	}
}

func TestAndroidProxyCorruptFileIsPreserved(t *testing.T) {
	a := workspaceApp()
	a.androidProxyPath = filepath.Join(t.TempDir(), "android-proxy.json")
	if err := os.WriteFile(a.androidProxyPath, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.loadAndroidProxyRecords()
	if _, err := os.Stat(a.androidProxyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("damaged file still at the original path: %v", err)
	}
	backups, err := filepath.Glob(a.androidProxyPath + ".corrupt-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups: %v %v", backups, err)
	}
	data, err := os.ReadFile(backups[0])
	if err != nil || string(data) != "{broken" {
		t.Fatalf("backup contents: %q %v", data, err)
	}
}

func TestCleanupRootsIncludesTmpForMacOS(t *testing.T) {
	if got, want := cleanupRoots("/var/folders/x", "darwin"), []string{"/var/folders/x", "/tmp"}; !slices.Equal(got, want) {
		t.Fatalf("darwin roots: got %q want %q", got, want)
	}
	if got, want := cleanupRoots("/tmp", "linux"), []string{"/tmp"}; !slices.Equal(got, want) {
		t.Fatalf("linux roots: got %q want %q", got, want)
	}
	if got, want := cleanupRoots(`C:\Temp`, "windows"), []string{`C:\Temp`}; !slices.Equal(got, want) {
		t.Fatalf("windows roots: got %q want %q", got, want)
	}
}

func TestOneClickAndroidPersistsAndClearsUndoState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a shell script")
	}
	a := oneClickApp(t)
	a.androidProxyPath = filepath.Join(t.TempDir(), "android-proxy.json")
	a.clientUI.target = clientTargetAndroid
	a.clientUI.androidLoaded = true
	a.clientUI.android = []clientsetup.AndroidDevice{{Serial: "emulator-5554", State: "device", Model: "Pixel_7"}}
	a.clientUI.androidSerial = "emulator-5554"
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "adb")
	script := "#!/bin/sh\ncase \"$*\" in *\"settings get global http_proxy\"*) echo '10.0.2.2:8888';; esac\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Set proxy"); err != nil {
		t.Fatal(err)
	}
	waitAppSignal(t, a, func() bool { return len(a.clientUI.androidApplied) > 0 }, tt.Frame,
		func() string { return "proxy setup did not apply" })
	state, ok := a.clientUI.androidApplied["emulator-5554"]
	if !ok || state.Previous != "10.0.2.2:8888" || state.Port == 0 {
		t.Fatalf("applied state: %+v", a.clientUI.androidApplied)
	}
	if err := tt.Click("Undo proxy"); err != nil {
		t.Fatal(err)
	}
	waitAppSignal(t, a, func() bool { return len(a.clientUI.androidApplied) == 0 }, tt.Frame,
		func() string { return fmt.Sprintf("undo did not clear state: %+v", a.clientUI.androidApplied) })
}

func TestOneClickAndroidInstallsTheSystemCA(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a shell script")
	}
	a := oneClickApp(t)
	a.clientUI.target = clientTargetAndroid
	a.clientUI.androidLoaded = true
	a.clientUI.android = []clientsetup.AndroidDevice{{Serial: "emulator-5554", State: "device", Model: "Pixel_7"}}
	a.clientUI.androidSerial = "emulator-5554"
	logPath := filepath.Join(t.TempDir(), "adb.log")
	binDir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + clientsetup.POSIXQuote(logPath) + "\n" +
		"case \"$*\" in\n" +
		"*\"shell id -u\"*) echo 0 ;;\n" +
		"*\"pidof zygote64 zygote\"*) echo 389 ;;\n" +
		"*\"vscroxy-ca-backup | wc -l\"*) echo 145 ;;\n" +
		"*\"wc -l\"*) echo 146 ;;\n" +
		"*\"ls /apex/com.android.conscrypt/cacerts/*.0\"*) echo apex ;;\n" +
		"esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "adb"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Install system CA"); err != nil {
		t.Fatal(err)
	}
	waitAppSignal(t, a, func() bool { return !a.clientUI.androidPending }, tt.Frame,
		func() string { return "the install did not finish" })
	if !strings.Contains(a.message, "Installed CA in the Android system store") {
		t.Fatalf("message: %q", a.message)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if log := string(data); !strings.Contains(log, "mount -t tmpfs tmpfs /apex/com.android.conscrypt/cacerts") {
		t.Fatalf("the install did not mount the APEX store:\n%s", log)
	}
}

func TestMissingAndroidProxyRecordsIsNotDamage(t *testing.T) {
	a := workspaceApp()
	a.androidProxyPath = filepath.Join(t.TempDir(), "android-proxy.json")
	a.loadAndroidProxyRecords()
	if len(a.notices) != 0 {
		t.Fatalf("a missing file was reported: %+v", a.notices)
	}
	if a.androidProxyPath == "" {
		t.Fatal("a missing file disabled the records path")
	}
}

func TestOneClickContainerShowsProxyAndCaMount(t *testing.T) {
	a := oneClickApp(t)
	a.clientUI.target = clientTargetContainer
	a.clientUI.container = "my-app"
	tt := ui.NewTester(a.view, 960, 600)
	host := "host.docker.internal"
	if runtime.GOOS == "linux" {
		host = "127.0.0.1 (host network)"
	}
	for _, want := range []string{
		"Docker container", "Intercept container", "Container",
		host, "/vscroxy/ca.pem", "Volumes, published ports",
	} {
		if !tt.HasText(want) {
			t.Fatalf("container setup lacks %q: %v", want, tt.Texts())
		}
	}
}
