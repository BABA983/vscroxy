package app

import (
	"os"
	"path/filepath"
	"runtime"
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
