package app

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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
	a.caPath = filepath.Join(dir, "mitmproxy-ca-cert.pem")
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
		"NO_PROXY=localhost,127.0.0.1,::1",
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
		"adb reverse",
		"Undo restores the proxy value",
		"Android 7+ apps do not trust user-installed CAs",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
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
	deadline := time.Now().Add(5 * time.Second)
	for a.clientUI.last == "" && time.Now().Before(deadline) {
		tt.Frame()
		time.Sleep(10 * time.Millisecond)
	}
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
	a := oneClickApp(t)
	t.Setenv("PATH", "")
	t.Setenv("TERMINAL", "")
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Open terminal"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for a.message == "" && time.Now().Before(deadline) {
		tt.Frame()
		time.Sleep(10 * time.Millisecond)
	}
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
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + clientsetup.POSIXQuote(log) + "\nexit 0\n"
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
	deadline := time.Now().Add(5 * time.Second)
	var args string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(log)
		if err == nil {
			args = string(data)
			break
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
	time.Sleep(200 * time.Millisecond) // let the launcher's Wait return
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
	deadline := time.Now().Add(5 * time.Second)
	for len(a.clientUI.androidApplied) > 0 && time.Now().Before(deadline) {
		tt.Frame()
		time.Sleep(10 * time.Millisecond)
	}
	if len(a.clientUI.androidApplied) != 0 {
		t.Fatalf("stale record survived a missing reverse listener: %+v", a.clientUI.androidApplied)
	}
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
	deadline := time.Now().Add(5 * time.Second)
	for a.clientUI.androidPending && time.Now().Before(deadline) {
		a.drainClientSetup()
		time.Sleep(10 * time.Millisecond)
	}
	if len(a.clientUI.androidApplied) != 0 {
		t.Fatalf("recorded vscroxy itself as the previous proxy: %+v", a.clientUI.androidApplied)
	}
	if !strings.Contains(a.message, "already points at vscroxy") {
		t.Fatalf("missing refusal: %q", a.message)
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
	deadline := time.Now().Add(5 * time.Second)
	for a.clientUI.androidPending && time.Now().Before(deadline) {
		a.drainClientSetup()
		time.Sleep(10 * time.Millisecond)
	}
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
	deadline := time.Now().Add(5 * time.Second)
	for len(a.clientUI.androidApplied) == 0 && time.Now().Before(deadline) {
		tt.Frame()
		time.Sleep(10 * time.Millisecond)
	}
	state, ok := a.clientUI.androidApplied["emulator-5554"]
	if !ok || state.Previous != "10.0.2.2:8888" || state.Port == 0 {
		t.Fatalf("applied state: %+v", a.clientUI.androidApplied)
	}
	if err := tt.Click("Undo proxy"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for len(a.clientUI.androidApplied) > 0 && time.Now().Before(deadline) {
		tt.Frame()
		time.Sleep(10 * time.Millisecond)
	}
	if len(a.clientUI.androidApplied) != 0 {
		t.Fatalf("undo did not clear state: %+v", a.clientUI.androidApplied)
	}
}
