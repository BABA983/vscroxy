package clientsetup

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func testProxy() Proxy {
	return Proxy{
		Addr:   "127.0.0.1:7890",
		CAPath: "/Users/Jane Doe/.mitmproxy/mitmproxy-ca-cert.pem",
	}
}

func TestEnvironmentSetsBothCasesAndEveryCommonCAVariable(t *testing.T) {
	env, err := Environment(testProxy())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"HTTP_PROXY=http://127.0.0.1:7890",
		"HTTPS_PROXY=http://127.0.0.1:7890",
		"ALL_PROXY=http://127.0.0.1:7890",
		"http_proxy=http://127.0.0.1:7890",
		"https_proxy=http://127.0.0.1:7890",
		"all_proxy=http://127.0.0.1:7890",
		"NO_PROXY=localhost,127.0.0.1,::1",
		"no_proxy=localhost,127.0.0.1,::1",
		"NODE_EXTRA_CA_CERTS=/Users/Jane Doe/.mitmproxy/mitmproxy-ca-cert.pem",
		"REQUESTS_CA_BUNDLE=/Users/Jane Doe/.mitmproxy/mitmproxy-ca-cert.pem",
		"SSL_CERT_FILE=/Users/Jane Doe/.mitmproxy/mitmproxy-ca-cert.pem",
		"CURL_CA_BUNDLE=/Users/Jane Doe/.mitmproxy/mitmproxy-ca-cert.pem",
		"GIT_SSL_CAINFO=/Users/Jane Doe/.mitmproxy/mitmproxy-ca-cert.pem",
		"PIP_CERT=/Users/Jane Doe/.mitmproxy/mitmproxy-ca-cert.pem",
		"DENO_CERT=/Users/Jane Doe/.mitmproxy/mitmproxy-ca-cert.pem",
	}
	if !slices.Equal(env, want) {
		t.Fatalf("environment:\n got %q\nwant %q", env, want)
	}
	for _, entry := range env {
		if strings.Contains(entry, "TLS_REJECT") || strings.Contains(entry, "INSECURE") {
			t.Fatalf("environment disables verification: %s", entry)
		}
	}
}

func TestEnvironmentRejectsAnUnusableProxy(t *testing.T) {
	for _, addr := range []string{"", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:70000", "::1"} {
		if _, err := Environment(Proxy{Addr: addr, CAPath: "/tmp/ca.pem"}); err == nil {
			t.Fatalf("%q accepted", addr)
		}
	}
	if _, err := Environment(Proxy{Addr: "127.0.0.1:7890"}); err == nil {
		t.Fatal("empty CA path accepted")
	}
}

func TestPOSIXScriptQuotesSpacesAndApostrophes(t *testing.T) {
	p := Proxy{Addr: "127.0.0.1:7890", CAPath: `/Users/O'Brien/Library/Application Support/vscroxy/ca.pem`}
	script, err := POSIXScript(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"#!/bin/sh\n",
		`export HTTP_PROXY='http://127.0.0.1:7890'`,
		`export NODE_EXTRA_CA_CERTS='/Users/O'\''Brien/Library/Application Support/vscroxy/ca.pem'`,
		`exec "${SHELL:-/bin/sh}" -l`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "export NODE_EXTRA_CA_CERTS=/Users") {
		t.Fatal("CA path was not quoted")
	}
}

func TestWindowsScriptQuotesSpacesAndPercentSigns(t *testing.T) {
	p := Proxy{Addr: "127.0.0.1:7890", CAPath: `C:\Users\Jane Doe\AppData\Local\vscroxy\100% CA\ca.pem`}
	script, err := WindowsScript(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.ReplaceAll(script, "\r\n", "\n"), "\n")
	for _, want := range []string{
		`set "HTTP_PROXY=http://127.0.0.1:7890"`,
		`set "NODE_EXTRA_CA_CERTS=C:\Users\Jane Doe\AppData\Local\vscroxy\100%% CA\ca.pem"`,
	} {
		if !slices.Contains(lines, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "100% CA") {
		t.Fatal("percent sign was not escaped for a batch file")
	}
}

func TestWindowsStartLineKeepsASpacedPathAsOneArgument(t *testing.T) {
	got := WindowsStartLine(`C:\Program Files\vscroxy\terminal 1.cmd`)
	want := `start "" cmd.exe /k "C:\Program Files\vscroxy\terminal 1.cmd"`
	if got != want {
		t.Fatalf("start line:\n got %q\nwant %q", got, want)
	}
}

func TestCommandLineQuoting(t *testing.T) {
	posix := POSIXCommandLine("keytool", []string{"-file", "/Users/Jane Doe/ca.pem"})
	if want := `keytool -file '/Users/Jane Doe/ca.pem'`; posix != want {
		t.Fatalf("POSIX command:\n got %q\nwant %q", posix, want)
	}
	windows := WindowsCommandLine("keytool", []string{"-file", `C:\Users\Jane Doe\ca.pem`})
	if want := `"keytool" "-file" "C:\Users\Jane Doe\ca.pem"`; windows != want {
		t.Fatalf("Windows command:\n got %q\nwant %q", windows, want)
	}
}

func TestChromeArgsUseAnIsolatedProfileAndNeverDisableTLS(t *testing.T) {
	profile := `C:\Users\Jane Doe\AppData\Local\Temp\vscroxy-chrome-1`
	args, err := ChromeArgs(testProxy(), profile)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--user-data-dir=" + profile,
		"--proxy-server=http://127.0.0.1:7890",
		"--proxy-bypass-list=localhost;127.0.0.1;::1",
		"--no-first-run",
		"--no-default-browser-check",
		"--new-window",
		"about:blank",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args:\n got %q\nwant %q", args, want)
	}
	for _, arg := range args {
		if strings.Contains(arg, "ignore-certificate-errors") {
			t.Fatalf("TLS bypass in %q", arg)
		}
	}
}

func TestFirefoxProfilePointsBothSchemesAtTheProxy(t *testing.T) {
	userJS, err := FirefoxUserJS(testProxy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`user_pref("network.proxy.type", 1);`,
		`user_pref("network.proxy.http", "127.0.0.1");`,
		`user_pref("network.proxy.http_port", 7890);`,
		`user_pref("network.proxy.ssl", "127.0.0.1");`,
		`user_pref("network.proxy.ssl_port", 7890);`,
		`user_pref("network.proxy.no_proxies_on", "localhost,127.0.0.1,::1");`,
		`user_pref("security.enterprise_roots.enabled", true);`,
	} {
		if !strings.Contains(userJS, want) {
			t.Fatalf("user.js missing %q:\n%s", want, userJS)
		}
	}
	if _, err := FirefoxUserJS(Proxy{Addr: "bad"}); err == nil {
		t.Fatal("bad proxy accepted")
	}
	args, err := FirefoxArgs(`/Users/Jane Doe/Library/Application Support/vscroxy/firefox`)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"-profile", `/Users/Jane Doe/Library/Application Support/vscroxy/firefox`, "-no-remote"}; !slices.Equal(args, want) {
		t.Fatalf("Firefox args: got %q want %q", args, want)
	}
}

func TestElectronArgsUseTheProxyAndNeverDisableTLS(t *testing.T) {
	args, err := ElectronArgs(testProxy())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--proxy-server=http://127.0.0.1:7890",
		"--proxy-bypass-list=localhost;127.0.0.1;::1",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args:\n got %q\nwant %q", args, want)
	}
	for _, arg := range args {
		if strings.Contains(arg, "ignore-certificate-errors") {
			t.Fatalf("TLS bypass in %q", arg)
		}
	}
}

func TestKeytoolArgsAndJVMOptionsUseTheAppOwnedTruststore(t *testing.T) {
	keystore := `/Users/Jane Doe/Library/Application Support/vscroxy/java/truststore.p12`
	args, err := KeytoolArgs(testProxy(), keystore)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-importcert", "-noprompt",
		"-alias", "vscroxy-ca",
		"-file", "/Users/Jane Doe/.mitmproxy/mitmproxy-ca-cert.pem",
		"-keystore", keystore,
		"-storetype", "PKCS12",
		"-storepass", "changeit",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args:\n got %q\nwant %q", args, want)
	}
	jvm := JVMArgs(keystore)
	if want := []string{"-Djavax.net.ssl.trustStore=" + keystore, "-Djavax.net.ssl.trustStorePassword=changeit"}; !slices.Equal(jvm, want) {
		t.Fatalf("JVM args: got %q want %q", jvm, want)
	}
}

func TestAndroidCommandsReverseThePortAndCanBeUndone(t *testing.T) {
	apply, err := AndroidProxyCommands("emulator-5554", 7890)
	if err != nil {
		t.Fatal(err)
	}
	wantApply := []Argv{
		{Name: "adb", Args: []string{"-s", "emulator-5554", "reverse", "tcp:7890", "tcp:7890"}},
		{Name: "adb", Args: []string{"-s", "emulator-5554", "shell", "settings", "put", "global", "http_proxy", "127.0.0.1:7890"}},
	}
	if !slices.EqualFunc(apply, wantApply, argvEqual) {
		t.Fatalf("apply:\n got %+v\nwant %+v", apply, wantApply)
	}
	undo, err := AndroidUndoCommands("emulator-5554", 7890)
	if err != nil {
		t.Fatal(err)
	}
	wantUndo := []Argv{
		{Name: "adb", Args: []string{"-s", "emulator-5554", "shell", "settings", "put", "global", "http_proxy", ":0"}},
		{Name: "adb", Args: []string{"-s", "emulator-5554", "reverse", "--remove", "tcp:7890"}},
	}
	if !slices.EqualFunc(undo, wantUndo, argvEqual) {
		t.Fatalf("undo:\n got %+v\nwant %+v", undo, wantUndo)
	}
	if _, err := AndroidProxyCommands("", 0); err == nil {
		t.Fatal("port 0 accepted")
	}
}

func TestAndroidWithoutASerialLeavesOutTheSelector(t *testing.T) {
	apply, err := AndroidProxyCommands("", 7891)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"reverse", "tcp:7891", "tcp:7891"}; !slices.Equal(apply[0].Args, want) {
		t.Fatalf("args: got %q want %q", apply[0].Args, want)
	}
}

func TestAndroidCACopyUsesDownloads(t *testing.T) {
	command, err := AndroidCACopyCommand("emulator-5554", "/Users/Jane Doe/ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-s", "emulator-5554", "push", "/Users/Jane Doe/ca.pem", "/sdcard/Download/vscroxy-ca.crt"}
	if !slices.Equal(command.Args, want) {
		t.Fatalf("args: got %q want %q", command.Args, want)
	}
}

func TestParseAndroidDevicesKeepsUnusableDevicesAndTheirState(t *testing.T) {
	out := `List of devices attached
emulator-5554          device product:sdk_gphone64_x86_64 model:Pixel_7 device:emu64xa transport_id:1
R58M12345              unauthorized usb:1-1
192.168.1.2:5555       offline
adb server version (41) doesn't match this client (39); killing...

* daemon started successfully
`
	devices := ParseAndroidDevices(out)
	want := []AndroidDevice{
		{Serial: "emulator-5554", State: "device", Model: "Pixel_7"},
		{Serial: "R58M12345", State: "unauthorized"},
		{Serial: "192.168.1.2:5555", State: "offline"},
	}
	if !slices.Equal(devices, want) {
		t.Fatalf("devices:\n got %+v\nwant %+v", devices, want)
	}
	if got := devices[0].Label(); got != "emulator-5554 · Pixel_7 · device" {
		t.Fatalf("label: %q", got)
	}
}

func TestMissingHelpersSayWhatToInstall(t *testing.T) {
	t.Setenv("PATH", "")
	if _, err := AndroidDevices(); err == nil {
		t.Fatal("adb unexpectedly found")
	} else if missing, ok := errors.AsType[*MissingToolError](err); !ok || missing.Tool != "adb" || !strings.Contains(missing.Error(), "platform-tools") {
		t.Fatalf("adb error: %v", err)
	}
}

func TestImportJavaCANotFoundKeepsTheExistingTruststore(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir()
	keystore := filepath.Join(dir, "truststore.p12")
	if err := os.WriteFile(keystore, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ImportJavaCA(testProxy(), keystore)
	if err == nil {
		t.Fatal("keytool unexpectedly found")
	}
	if missing, ok := errors.AsType[*MissingToolError](err); !ok || missing.Tool != "keytool" {
		t.Fatalf("keytool error: %v", err)
	}
	if data, readErr := os.ReadFile(keystore); readErr != nil || string(data) != "keep" {
		t.Fatalf("existing truststore was touched: %q %v", data, readErr)
	}
}

func TestImportJavaCAFailureKeepsTheExistingTruststore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake keytool is a shell script")
	}
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(binDir, "keytool")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'keytool failed' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	keystore := filepath.Join(dir, "truststore.p12")
	if err := os.WriteFile(keystore, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ImportJavaCA(testProxy(), keystore)
	if err == nil || !strings.Contains(err.Error(), "keytool failed") {
		t.Fatalf("error: %v", err)
	}
	if data, readErr := os.ReadFile(keystore); readErr != nil || string(data) != "keep" {
		t.Fatalf("existing truststore was replaced: %q %v", data, readErr)
	}
	if _, statErr := os.Stat(keystore + ".import"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("temporary truststore left behind: %v", statErr)
	}
}

func TestPrepareFirefoxProfileWritesUserJSAndReportsMissingCertutil(t *testing.T) {
	t.Setenv("PATH", "")
	profile := filepath.Join(t.TempDir(), "profile with space")
	warning, err := PrepareFirefoxProfile(testProxy(), profile)
	if err != nil {
		t.Fatal(err)
	}
	if warning == nil {
		t.Fatal("missing certutil was not reported")
	}
	if missing, ok := errors.AsType[*MissingToolError](warning); !ok || missing.Tool != "certutil" {
		t.Fatalf("warning: %v", warning)
	}
	userJS, err := os.ReadFile(filepath.Join(profile, "user.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(userJS), `user_pref("network.proxy.type", 1);`) {
		t.Fatalf("user.js not written:\n%s", userJS)
	}
	if _, err := os.Stat(filepath.Join(profile, "cert9.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("certutil ran despite an empty PATH")
	}
}

func TestWritePOSIXTerminalScriptIsExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	path, err := WritePOSIXTerminalScript(testProxy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v, want 0700", info.Mode().Perm())
	}
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := POSIXScript(testProxy())
	if err != nil {
		t.Fatal(err)
	}
	if string(script) != want {
		t.Fatalf("script:\n got %q\nwant %q", script, want)
	}
}

func argvEqual(a, b Argv) bool {
	return a.Name == b.Name && slices.Equal(a.Args, b.Args)
}
