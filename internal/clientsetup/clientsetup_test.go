package clientsetup

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func testProxy() Proxy {
	return Proxy{
		Addr:       "127.0.0.1:7890",
		CAPath:     "/Users/Jane Doe/.mitmproxy/mitmproxy-ca-cert.pem",
		BundlePath: "/Users/Jane Doe/.vscroxy/client-ca-bundle.pem",
	}
}

func TestEnvironmentUsesTheSystemBundleForReplacementVariables(t *testing.T) {
	env, err := Environment(testProxy())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"HTTP_PROXY=http://127.0.0.1:7890",
		"HTTPS_PROXY=http://127.0.0.1:7890",
		"http_proxy=http://127.0.0.1:7890",
		"https_proxy=http://127.0.0.1:7890",
		"NO_PROXY=localhost,127.0.0.1,::1",
		"no_proxy=localhost,127.0.0.1,::1",
		"NODE_EXTRA_CA_CERTS=/Users/Jane Doe/.mitmproxy/mitmproxy-ca-cert.pem",
		"REQUESTS_CA_BUNDLE=/Users/Jane Doe/.vscroxy/client-ca-bundle.pem",
		"SSL_CERT_FILE=/Users/Jane Doe/.vscroxy/client-ca-bundle.pem",
		"CURL_CA_BUNDLE=/Users/Jane Doe/.vscroxy/client-ca-bundle.pem",
		"GIT_SSL_CAINFO=/Users/Jane Doe/.vscroxy/client-ca-bundle.pem",
		"PIP_CERT=/Users/Jane Doe/.vscroxy/client-ca-bundle.pem",
		"DENO_CERT=/Users/Jane Doe/.vscroxy/client-ca-bundle.pem",
	}
	if !slices.Equal(env, want) {
		t.Fatalf("environment:\n got %q\nwant %q", env, want)
	}
	for _, entry := range env {
		if strings.Contains(entry, "ALL_PROXY") || strings.Contains(entry, "all_proxy") {
			t.Fatalf("ALL_PROXY must not send every protocol to an HTTP proxy: %s", entry)
		}
		if strings.Contains(entry, "TLS_REJECT") || strings.Contains(entry, "INSECURE") {
			t.Fatalf("environment disables verification: %s", entry)
		}
	}
}

func TestEnvironmentWithoutBundleOnlyAddsTheAppCA(t *testing.T) {
	p := testProxy()
	p.BundlePath = ""
	env, err := Environment(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"HTTP_PROXY=http://127.0.0.1:7890",
		"HTTPS_PROXY=http://127.0.0.1:7890",
		"http_proxy=http://127.0.0.1:7890",
		"https_proxy=http://127.0.0.1:7890",
		"NO_PROXY=localhost,127.0.0.1,::1",
		"no_proxy=localhost,127.0.0.1,::1",
		"NODE_EXTRA_CA_CERTS=/Users/Jane Doe/.mitmproxy/mitmproxy-ca-cert.pem",
	}
	if !slices.Equal(env, want) {
		t.Fatalf("environment:\n got %q\nwant %q", env, want)
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

func TestPOSIXScriptQuotesSpacesAndRemovesItself(t *testing.T) {
	p := Proxy{
		Addr:       "127.0.0.1:7890",
		CAPath:     `/Users/O'Brien/Library/Application Support/vscroxy/ca.pem`,
		BundlePath: `/Users/O'Brien/Library/Application Support/vscroxy/client-ca-bundle.pem`,
	}
	script, err := POSIXScript(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"#!/bin/sh\n",
		`export HTTP_PROXY='http://127.0.0.1:7890'`,
		`export NODE_EXTRA_CA_CERTS='/Users/O'\''Brien/Library/Application Support/vscroxy/ca.pem'`,
		`export SSL_CERT_FILE='/Users/O'\''Brien/Library/Application Support/vscroxy/client-ca-bundle.pem'`,
		`rm -f "$0"`,
		`rmdir "${0%/*}" 2>/dev/null || true`,
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

func TestWindowsCommandLineKeepsPercentSignsForCopying(t *testing.T) {
	windows := WindowsCommandLine("keytool", []string{"-file", `C:\Users\Jane Doe\100% CA\ca.pem`})
	if want := `"keytool" "-file" "C:\Users\Jane Doe\100% CA\ca.pem"`; windows != want {
		t.Fatalf("Windows command:\n got %q\nwant %q", windows, want)
	}
	posix := POSIXCommandLine("keytool", []string{"-file", "/Users/Jane Doe/ca.pem"})
	if want := `keytool -file '/Users/Jane Doe/ca.pem'`; posix != want {
		t.Fatalf("POSIX command:\n got %q\nwant %q", posix, want)
	}
}

func TestMergeEnvReplacesNamesIgnoringCase(t *testing.T) {
	base := []string{"Path=C:\\Windows", "HTTP_PROXY=old", "http_proxy=old-lower"}
	extra := []string{"HTTP_PROXY=new", "NODE_EXTRA_CA_CERTS=C:\\ca.pem"}
	got := MergeEnv(base, extra)
	want := []string{"Path=C:\\Windows", "HTTP_PROXY=new", "NODE_EXTRA_CA_CERTS=C:\\ca.pem"}
	if !slices.Equal(got, want) {
		t.Fatalf("merged env:\n got %q\nwant %q", got, want)
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
	jvm, err := JVMArgs(testProxy(), keystore)
	if err != nil {
		t.Fatal(err)
	}
	wantJVM := []string{
		"-Djavax.net.ssl.trustStore=" + keystore,
		"-Djavax.net.ssl.trustStorePassword=changeit",
		"-Dhttp.proxyHost=127.0.0.1",
		"-Dhttp.proxyPort=7890",
		"-Dhttps.proxyHost=127.0.0.1",
		"-Dhttps.proxyPort=7890",
		"-Dhttp.nonProxyHosts=localhost|127.0.0.1",
	}
	if !slices.Equal(jvm, wantJVM) {
		t.Fatalf("JVM args:\n got %q\nwant %q", jvm, wantJVM)
	}
}

func TestJavaCACertsPathFindsTheJDKStore(t *testing.T) {
	jdk := t.TempDir()
	cacerts := filepath.Join(jdk, "lib", "security", "cacerts")
	if err := os.MkdirAll(filepath.Dir(cacerts), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacerts, []byte("cacerts"), 0o600); err != nil {
		t.Fatal(err)
	}
	keytool := filepath.Join(jdk, "bin", "keytool")
	if err := os.MkdirAll(filepath.Dir(keytool), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keytool, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := JavaCACertsPath(keytool, "")
	if err != nil || got != cacerts {
		t.Fatalf("from keytool: got %q %v, want %q", got, err, cacerts)
	}
	got, err = JavaCACertsPath("/not/a/jdk/bin/keytool", jdk)
	if err != nil || got != cacerts {
		t.Fatalf("from JAVA_HOME: got %q %v, want %q", got, err, cacerts)
	}
	if _, err := JavaCACertsPath("/not/a/jdk/bin/keytool", ""); err == nil || !strings.Contains(err.Error(), "JDK cacerts not found") {
		t.Fatalf("missing cacerts error: %v", err)
	}
}

func TestJavaTruststoreCommandsCopyCACertsThenAddTheCA(t *testing.T) {
	keystore := `/Users/Jane Doe/Library/Application Support/vscroxy/java/truststore.p12`
	cacerts := `/Library/Java/JavaVirtualMachines/jdk-27.jdk/Contents/Home/lib/security/cacerts`
	commands, err := JavaTruststoreCommands(testProxy(), keystore, cacerts)
	if err != nil {
		t.Fatal(err)
	}
	want := []Argv{
		{Name: "keytool", Args: []string{
			"-importkeystore", "-noprompt",
			"-srckeystore", cacerts,
			"-srcstorepass", "changeit",
			"-destkeystore", keystore + ".import",
			"-deststoretype", "PKCS12",
			"-deststorepass", "changeit",
		}},
		{Name: "keytool", Args: []string{
			"-importcert", "-noprompt",
			"-alias", "vscroxy-ca",
			"-file", "/Users/Jane Doe/.mitmproxy/mitmproxy-ca-cert.pem",
			"-keystore", keystore + ".import",
			"-storetype", "PKCS12",
			"-storepass", "changeit",
		}},
	}
	if !slices.EqualFunc(commands, want, argvEqual) {
		t.Fatalf("commands:\n got %+v\nwant %+v", commands, want)
	}
}

func TestAndroidCommandsReverseThePortAndRestoreThePreviousProxy(t *testing.T) {
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
	undo, err := AndroidUndoCommands("emulator-5554", 7890, "10.0.2.2:8888")
	if err != nil {
		t.Fatal(err)
	}
	wantUndo := []Argv{
		{Name: "adb", Args: []string{"-s", "emulator-5554", "shell", "settings", "put", "global", "http_proxy", "10.0.2.2:8888"}},
		{Name: "adb", Args: []string{"-s", "emulator-5554", "reverse", "--remove", "tcp:7890"}},
	}
	if !slices.EqualFunc(undo, wantUndo, argvEqual) {
		t.Fatalf("undo:\n got %+v\nwant %+v", undo, wantUndo)
	}
	for _, previous := range []string{"", "null"} {
		undo, err := AndroidUndoCommands("emulator-5554", 7890, previous)
		if err != nil || undo[0].Args[len(undo[0].Args)-1] != ":0" {
			t.Fatalf("previous %q did not clear the proxy: %+v %v", previous, undo, err)
		}
	}
	if _, err := AndroidProxyCommands("", 0); err == nil {
		t.Fatal("port 0 accepted")
	}
}

func TestApplyAndroidProxyReadsThePreviousValue(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "adb.log")
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(binDir, "adb")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + POSIXQuote(log) + "\n" +
		"case \"$*\" in *\"settings get global http_proxy\"*) echo '10.0.2.2:8888';; esac\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	previous, err := ApplyAndroidProxy("emulator-5554", 7890)
	if err != nil || previous != "10.0.2.2:8888" {
		t.Fatalf("apply: previous %q err %v", previous, err)
	}
	if err := ClearAndroidProxy("emulator-5554", 7890, previous); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, want := range []string{
		"-s emulator-5554 shell settings get global http_proxy",
		"-s emulator-5554 reverse tcp:7890 tcp:7890",
		"-s emulator-5554 shell settings put global http_proxy 127.0.0.1:7890",
		"-s emulator-5554 shell settings put global http_proxy 10.0.2.2:8888",
		"-s emulator-5554 reverse --remove tcp:7890",
	} {
		if !slices.Contains(lines, want) {
			t.Fatalf("adb log missing %q:\n%s", want, data)
		}
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
	jdk := t.TempDir()
	binDir := filepath.Join(jdk, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cacerts := filepath.Join(jdk, "lib", "security", "cacerts")
	if err := os.MkdirAll(filepath.Dir(cacerts), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacerts, []byte("cacerts"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(binDir, "keytool")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'keytool failed' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("JAVA_HOME", jdk)
	keystore := filepath.Join(jdk, "truststore.p12")
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

func TestNSSIsNotUsedOnWindows(t *testing.T) {
	if nssSupported("windows") {
		t.Fatal("Windows certutil is not NSS")
	}
	if !nssSupported("linux") || !nssSupported("darwin") {
		t.Fatal("NSS should be used on Linux and macOS")
	}
}

func TestPrepareFirefoxProfileWritesUserJSAndReportsMissingCertutil(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses enterprise roots, not NSS")
	}
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

func TestWritePOSIXTerminalScriptIsExecutableAndUsesASpaceFreeDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	path, err := WritePOSIXTerminalScript(testProxy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })
	if strings.Contains(filepath.Dir(path), " ") {
		t.Fatalf("terminal script directory contains a space: %q", path)
	}
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

func TestWriteTrustBundleKeepsBothRootsAndTheAppCA(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "client-ca-bundle.pem")
	if err := WriteTrustBundle(testCertificate(t, "System Root"), testCertificate(t, "vscroxy CA"), path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := countCertificates(data); got != 2 {
		t.Fatalf("bundle has %d certificates, want 2", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", info.Mode().Perm())
	}
	if err := WriteTrustBundle(nil, testCertificate(t, "vscroxy CA"), path); !errors.Is(err, ErrNoSystemRoots) {
		t.Fatalf("missing roots: %v", err)
	}
	if err := WriteTrustBundle(testCertificate(t, "System Root"), []byte("not a certificate"), path); err == nil {
		t.Fatal("invalid CA accepted")
	}
}

func TestEnsureTrustBundleUsesSystemRootsAndFallsBackWithoutThem(t *testing.T) {
	old := systemRoots
	t.Cleanup(func() { systemRoots = old })

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, testCertificate(t, "vscroxy CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(t.TempDir(), "bundle.pem")
	systemRoots = func() ([]byte, error) { return testCertificate(t, "System Root"), nil }
	if err := EnsureTrustBundle(caPath, bundlePath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := countCertificates(data); got != 2 {
		t.Fatalf("bundle has %d certificates, want 2", got)
	}

	systemRoots = func() ([]byte, error) { return nil, ErrNoSystemRoots }
	missing := filepath.Join(t.TempDir(), "missing.pem")
	if err := EnsureTrustBundle(caPath, missing); !errors.Is(err, ErrNoSystemRoots) {
		t.Fatalf("missing roots: %v", err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("bundle was written without system roots")
	}
}

func TestCleanupOldDirsOnlyRemovesThisAppsOldDirectories(t *testing.T) {
	root := t.TempDir()
	oldChrome := filepath.Join(root, "vscroxy-chrome-old")
	newFirefox := filepath.Join(root, "vscroxy-firefox-new")
	oldTerminal := filepath.Join(root, "vscroxy-terminal-old")
	unrelated := filepath.Join(root, "other-app")
	for _, dir := range []string{oldChrome, newFirefox, oldTerminal, unrelated} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	for _, dir := range []string{oldChrome, oldTerminal, unrelated} {
		if err := os.Chtimes(dir, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := CleanupOldDirs(root, 24*time.Hour)
	if err != nil || removed != 2 {
		t.Fatalf("cleanup: removed %d, err %v", removed, err)
	}
	for _, dir := range []string{oldChrome, oldTerminal} {
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s was not removed", dir)
		}
	}
	for _, dir := range []string{newFirefox, unrelated} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s was removed: %v", dir, err)
		}
	}
}

func TestArgvStartRunsCleanupAfterTheChildExits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh is not available")
	}
	marker := filepath.Join(t.TempDir(), "cleaned")
	command := Argv{Name: "sh", Args: []string{"-c", "exit 0"}, Cleanup: func() { _ = os.WriteFile(marker, nil, 0o600) }}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("cleanup did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func testCertificate(t *testing.T, name string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func countCertificates(data []byte) int {
	count := 0
	for {
		block, rest := pem.Decode(data)
		if block == nil {
			return count
		}
		if block.Type == "CERTIFICATE" {
			count++
		}
		data = rest
	}
}

func argvEqual(a, b Argv) bool {
	return a.Name == b.Name && slices.Equal(a.Args, b.Args)
}
