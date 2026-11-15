package clientsetup

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// androidTestCA has subject C=CN, ST=Shanghai, L=Shanghai, O=vscroxy test,
// OU=QA, CN=vscroxy test CA. `openssl x509 -subject_hash_old` names it
// a4f073b6, which is the value AndroidCertFileName must produce.
const androidTestCA = `-----BEGIN CERTIFICATE-----
MIIDojCCAoqgAwIBAgIUKAc+IN1plYSv9OjVmDESQZPdm98wDQYJKoZIhvcNAQEL
BQAwcTELMAkGA1UEBhMCQ04xETAPBgNVBAgMCFNoYW5naGFpMREwDwYDVQQHDAhT
aGFuZ2hhaTEVMBMGA1UECgwMdnNjcm94eSB0ZXN0MQswCQYDVQQLDAJRQTEYMBYG
A1UEAwwPdnNjcm94eSB0ZXN0IENBMB4XDTI2MTExNTA0MjI0NFoXDTM2MTExMjA0
MjI0NFowcTELMAkGA1UEBhMCQ04xETAPBgNVBAgMCFNoYW5naGFpMREwDwYDVQQH
DAhTaGFuZ2hhaTEVMBMGA1UECgwMdnNjcm94eSB0ZXN0MQswCQYDVQQLDAJRQTEY
MBYGA1UEAwwPdnNjcm94eSB0ZXN0IENBMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A
MIIBCgKCAQEA0JggSffHUmbG7BMHBmbwiBe4fCgslfMhf3sP2kuTc9m23IgQmFFV
cJN7nWpDWtW3kHA46gHnV02ryyf2ZlYPtwPWErjmXaIxqCI0YMKa85FFuUeUD41o
aEF/U4AbysirDIYNTKQ8lXwQ9XREKMlx9//Dah/nCZ67Zb9q6n5vE20tHmFv1HyU
EYOWB5+TWL2KIC/ddFdM/RV8RPq2bd0ki+yXk5YAvPnbG5TM/BKu3XN1czapJADo
2LjOukXFsZ9vbJHgplCSD9WpKTYv9/46a40jDJRECiQi4rroUahHnyic/N+wjCKT
xBT2EhfC5eqNOy0b4xknVEpNri+uhxrQGQIDAQABozIwMDAdBgNVHQ4EFgQU/oYG
Hc54ykBcaQf4Nhz4bHPz+aQwDwYDVR0TAQH/BAUwAwEB/zANBgkqhkiG9w0BAQsF
AAOCAQEAljyVBW9S4Nu93VVYB+DfvscQqTqyACBTzB5r6eYNFzoU0/zzfSti6AF+
/fRcmcqI1nCg3lzsyFyuwWwt6EhqNc/nvL4IOE40XHFUm7CylvMyPlCCkrTdwUNE
oYJHPiV/1/xRA2FMnuJdJIu88N7eklpEF30S/FWRsqHqucPRcoEquBQZYry+GSVn
tGhOpNlt3qR9YYis7beot9ydW/XflHF/3Zuwc4fbVKZRdm7ZNQSSEz2qu6tV2av0
WsMRiUdDHu/+KUHOHPCtdY7hPapMXxrhDIJ5CRAYl+7KA/WvoJJ/W08GPqAK8ZLq
yegbk2akkCrQ/lXhVhVeOYgaPa2qVQ==
-----END CERTIFICATE-----
`

func TestAndroidCertFileNameMatchesOpenSSLSubjectHashOld(t *testing.T) {
	name, err := AndroidCertFileName([]byte(androidTestCA))
	if err != nil {
		t.Fatal(err)
	}
	if name != "a4f073b6.0" {
		t.Fatalf("name: got %q, want a4f073b6.0", name)
	}
	for _, bad := range []string{"", "not a certificate", "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"} {
		if _, err := AndroidCertFileName([]byte(bad)); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
}

func TestAndroidAPEXCACommandsKeepEveryCertificateUnderATmpfs(t *testing.T) {
	commands, err := AndroidAPEXCACommands("emulator-5554", AndroidRootAdb, "a4f073b6.0", []int{389})
	if err != nil {
		t.Fatal(err)
	}
	store := string(AndroidSystemStoreApex)
	want := [][]string{
		{"-s", "emulator-5554", "shell", "mkdir -p " + androidCABackup},
		{"-s", "emulator-5554", "shell", "nsenter -t 389 -m -- cp " + store + "/* " + androidCABackup + "/"},
		{"-s", "emulator-5554", "shell", "nsenter -t 389 -m -- sh -c " + POSIXQuote(`if grep -q "^tmpfs `+store+` " /proc/mounts; then umount `+store+`; fi; mount -t tmpfs tmpfs `+store)},
		{"-s", "emulator-5554", "shell", "nsenter -t 389 -m -- cp " + androidCABackup + "/* " + store + "/"},
		{"-s", "emulator-5554", "shell", "nsenter -t 389 -m -- cp " + AndroidCAPath + " " + store + "/a4f073b6.0"},
		{"-s", "emulator-5554", "shell", "nsenter -t 389 -m -- chmod 755 " + store},
		{"-s", "emulator-5554", "shell", "nsenter -t 389 -m -- chcon u:object_r:system_file:s0 " + store},
		{"-s", "emulator-5554", "shell", "nsenter -t 389 -m -- sh -c " + POSIXQuote("chown root:root "+store+"/* && chmod 644 "+store+"/* && chcon u:object_r:system_file:s0 "+store+"/*")},
	}
	if len(commands) != len(want) {
		t.Fatalf("commands: got %d, want %d:\n%+v", len(commands), len(want), commands)
	}
	for i, args := range want {
		if commands[i].Name != "adb" || !slices.Equal(commands[i].Args, args) {
			t.Fatalf("command %d:\n got %+v\nwant adb %q", i, commands[i], args)
		}
	}
}

func TestAndroidAPEXCACommandsNeedAZygoteAndAHashName(t *testing.T) {
	if _, err := AndroidAPEXCACommands("emulator-5554", AndroidRootAdb, "a4f073b6.0", nil); err == nil {
		t.Fatal("no zygote accepted")
	}
	for _, name := range []string{"", "a4f073b6", "../../etc/passwd", "A4F073B6.0", "a4f073b6.0; rm -rf /"} {
		if _, err := AndroidAPEXCACommands("emulator-5554", AndroidRootAdb, name, []int{389}); err == nil {
			t.Fatalf("name %q accepted", name)
		}
	}
}

func TestAndroidSystemCACommandsWriteTheHashedFile(t *testing.T) {
	commands, err := AndroidSystemCACommands("emulator-5554", AndroidRootAdb, "a4f073b6.0")
	if err != nil {
		t.Fatal(err)
	}
	file := string(AndroidSystemStoreSystem) + "/a4f073b6.0"
	want := [][]string{
		{"-s", "emulator-5554", "shell", "cp " + AndroidCAPath + " " + file},
		{"-s", "emulator-5554", "shell", "chmod 644 " + file},
		{"-s", "emulator-5554", "shell", "chown root:root " + file},
		{"-s", "emulator-5554", "shell", "chcon u:object_r:system_file:s0 " + file},
	}
	if len(commands) != len(want) {
		t.Fatalf("commands: got %+v", commands)
	}
	for i, args := range want {
		if !slices.Equal(commands[i].Args, args) {
			t.Fatalf("command %d: got %q want %q", i, commands[i].Args, args)
		}
	}
}

func TestAndroidShellCommandUsesSuWhenAdbdCannotBeRoot(t *testing.T) {
	direct := AndroidShellCommand("emulator-5554", AndroidRootAdb, "id -u")
	if want := []string{"-s", "emulator-5554", "shell", "id -u"}; !slices.Equal(direct.Args, want) {
		t.Fatalf("adb root: %q", direct.Args)
	}
	su := AndroidShellCommand("R58M12345", AndroidRootSu, "cp /data/local/tmp/vscroxy-ca.pem /system/etc/security/cacerts/a4f073b6.0")
	want := []string{"-s", "R58M12345", "shell", "su -c 'cp /data/local/tmp/vscroxy-ca.pem /system/etc/security/cacerts/a4f073b6.0'"}
	if !slices.Equal(su.Args, want) {
		t.Fatalf("su: got %q want %q", su.Args, want)
	}
}

func TestParseAndroidStoreAndPids(t *testing.T) {
	for out, want := range map[string]AndroidSystemStore{
		"apex\n":   AndroidSystemStoreApex,
		"system\n": AndroidSystemStoreSystem,
	} {
		if got, err := ParseAndroidStore(out); err != nil || got != want {
			t.Fatalf("store %q: got %q, %v", out, got, err)
		}
	}
	for _, out := range []string{"", "unknown", "apex system"} {
		if _, err := ParseAndroidStore(out); err == nil {
			t.Fatalf("store %q accepted", out)
		}
	}
	if got := ParseAndroidPids("389 412\n"); !slices.Equal(got, []int{389, 412}) {
		t.Fatalf("pids: %v", got)
	}
	if got := ParseAndroidPids(""); got != nil {
		t.Fatalf("empty pids: %v", got)
	}
}

func TestAndroidNetworkSecurityConfigTrustsTheDebugResource(t *testing.T) {
	config := AndroidNetworkSecurityConfig()
	for _, want := range []string{
		"<debug-overrides>",
		`<certificates src="@raw/vscroxy_ca" />`,
		`<certificates src="user" />`,
		"</network-security-config>",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("snippet lacks %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "<base-config") {
		t.Fatal("the snippet must not change release trust")
	}
}

// fakeAndroidAdb puts a fake adb on PATH that logs its arguments and answers
// like a device. Each answer is a case pattern and the shell line that answers
// it, such as `*"pidof zygote64 zygote"*) echo 389 ;;`.
func fakeAndroidAdb(t *testing.T, answers ...string) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "adb.log")
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + POSIXQuote(logPath) + "\n" +
		"case \"$*\" in\n" + strings.Join(answers, "\n") + "\n*) :;;\nesac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "adb"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	return logPath
}

func androidTestCAFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mitmproxy-ca-cert.pem")
	if err := os.WriteFile(path, []byte(androidTestCA), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallAndroidSystemCAFillsTheAPEXStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a shell script")
	}
	logPath := fakeAndroidAdb(t,
		`*"shell id -u"*) echo 0 ;;`,
		`*"pidof zygote64 zygote"*) echo 389 ;;`,
		`*"vscroxy-ca-backup | wc -l"*) echo 145 ;;`,
		`*"nsenter -t 389 -m -- sh -c"*"wc -l"*) echo 146 ;;`,
		`*"ls /apex/com.android.conscrypt/cacerts/*.0"*) echo apex ;;`,
	)
	install, err := InstallAndroidSystemCA("emulator-5554", androidTestCAFile(t))
	if err != nil {
		t.Fatal(err)
	}
	if install.Store != AndroidSystemStoreApex || install.Name != "a4f073b6.0" {
		t.Fatalf("install: %+v", install)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	for _, want := range []string{
		"-s emulator-5554 root",
		"-s emulator-5554 push ",
		"-s emulator-5554 shell nsenter -t 389 -m -- cp /apex/com.android.conscrypt/cacerts/* /data/local/tmp/vscroxy-ca-backup/",
		"-s emulator-5554 shell nsenter -t 389 -m -- cp /data/local/tmp/vscroxy-ca.pem /apex/com.android.conscrypt/cacerts/a4f073b6.0",
		"-s emulator-5554 shell nsenter -t 389 -m -- sh -c 'ls -Zd /apex/com.android.conscrypt/cacerts | grep -q u:object_r:system_file:s0",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("adb log lacks %q:\n%s", want, log)
		}
	}
	// The certificates must be saved before the tmpfs replaces them, and the
	// mount must come before the copy back.
	saved := strings.Index(log, "cp /apex/com.android.conscrypt/cacerts/* /data/local/tmp/vscroxy-ca-backup/")
	mount := strings.Index(log, "mount -t tmpfs tmpfs /apex/com.android.conscrypt/cacerts")
	copied := strings.Index(log, "cp /data/local/tmp/vscroxy-ca-backup/* /apex/com.android.conscrypt/cacerts/")
	if saved < 0 || mount < 0 || copied < 0 || !(saved < mount && mount < copied) {
		t.Fatalf("order: saved %d mount %d copied %d\n%s", saved, mount, copied, log)
	}
}

func TestInstallAndroidSystemCAUnmountsWhenCertificatesAreMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a shell script")
	}
	logPath := fakeAndroidAdb(t,
		`*"shell id -u"*) echo 0 ;;`,
		`*"pidof zygote64 zygote"*) echo 389 ;;`,
		`*"vscroxy-ca-backup | wc -l"*) echo 145 ;;`,
		`*"nsenter -t 389 -m -- sh -c"*"wc -l"*) echo 12 ;;`,
		`*"ls /apex/com.android.conscrypt/cacerts/*.0"*) echo apex ;;`,
	)
	_, err := InstallAndroidSystemCA("emulator-5554", androidTestCAFile(t))
	if err == nil || !strings.Contains(err.Error(), "fewer than the 145") {
		t.Fatalf("err: %v", err)
	}
	data, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if log := string(data); !strings.Contains(log, "umount /apex/com.android.conscrypt/cacerts") {
		t.Fatalf("a short store was not unmounted:\n%s", log)
	}
}

func TestInstallAndroidSystemCAUnmountsWhenACopyFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a shell script")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "adb.log")
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + POSIXQuote(logPath) + "\n" +
		"case \"$*\" in\n" +
		"*\"shell id -u\"*) echo 0 ;;\n" +
		"*\"pidof zygote64 zygote\"*) echo 389 ;;\n" +
		"*\"chmod 644 /apex\"*) echo 'chmod: Read-only file system' >&2; exit 1 ;;\n" +
		"*\"ls /apex/com.android.conscrypt/cacerts/*.0\"*) echo apex ;;\n" +
		"esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "adb"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	if _, err := InstallAndroidSystemCA("emulator-5554", androidTestCAFile(t)); err == nil {
		t.Fatal("a failed chmod was accepted")
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if log := string(data); !strings.Contains(log, "umount /apex/com.android.conscrypt/cacerts") {
		t.Fatalf("a failed install was not unmounted:\n%s", log)
	}
}

func TestInstallAndroidSystemCANeedsRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a shell script")
	}
	fakeAndroidAdb(t,
		`*"shell id -u"*) echo 2000 ;;`,
		`*"su -c 'id -u'"*) echo 'su: not found' >&2; exit 1 ;;`,
	)
	_, err := InstallAndroidSystemCA("emulator-5554", androidTestCAFile(t))
	if err == nil || !strings.Contains(err.Error(), "needs root") {
		t.Fatalf("err: %v", err)
	}
}

func TestInstallAndroidSystemCAUsesSuForAProductionBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a shell script")
	}
	logPath := fakeAndroidAdb(t,
		`*"shell id -u"*) echo 2000 ;;`,
		`*"su -c 'id -u'"*) echo 0 ;;`,
		`*"ls /apex/com.android.conscrypt/cacerts/*.0"*) echo apex ;;`,
		`*"pidof zygote64 zygote"*) echo 389 ;;`,
		`*"vscroxy-ca-backup | wc -l"*) echo 145 ;;`,
		`*"wc -l"*) echo 146 ;;`,
	)
	if _, err := InstallAndroidSystemCA("emulator-5554", androidTestCAFile(t)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if log := string(data); !strings.Contains(log, `shell su -c 'nsenter -t 389 -m -- cp /data/local/tmp/vscroxy-ca.pem`) {
		t.Fatalf("su was not used for the install:\n%s", log)
	}
}

func TestInstallAndroidSystemCAWritesTheSystemStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a shell script")
	}
	logPath := fakeAndroidAdb(t,
		`*"shell id -u"*) echo 0 ;;`,
		`*"ls /apex/com.android.conscrypt/cacerts/*.0"*) echo system ;;`,
	)
	if _, err := InstallAndroidSystemCA("emulator-5554", androidTestCAFile(t)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	for _, want := range []string{
		"-s emulator-5554 remount",
		"-s emulator-5554 shell cp /data/local/tmp/vscroxy-ca.pem /system/etc/security/cacerts/a4f073b6.0",
		"-s emulator-5554 shell chmod 644 /system/etc/security/cacerts/a4f073b6.0",
		"-s emulator-5554 shell ls /system/etc/security/cacerts/a4f073b6.0",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("adb log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "nsenter") {
		t.Fatalf("the system store was installed through a mount namespace:\n%s", log)
	}
}

func TestInstallAndroidSystemCAUnmountsWhenTheLabelIsWrong(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake adb is a shell script")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "adb.log")
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + POSIXQuote(logPath) + "\n" +
		"case \"$*\" in\n" +
		"*\"shell id -u\"*) echo 0 ;;\n" +
		"*\"pidof zygote64 zygote\"*) echo 389 ;;\n" +
		"*\"vscroxy-ca-backup | wc -l\"*) echo 145 ;;\n" +
		"*\"wc -l\"*) echo 146 ;;\n" +
		"*\"ls -Zd /apex\"*) exit 1 ;;\n" +
		"*\"ls /apex/com.android.conscrypt/cacerts/*.0\"*) echo apex ;;\n" +
		"esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "adb"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	_, err := InstallAndroidSystemCA("emulator-5554", androidTestCAFile(t))
	if err == nil || !strings.Contains(err.Error(), "original certificate store is back") {
		t.Fatalf("err: %v", err)
	}
	data, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if log := string(data); !strings.Contains(log, "umount /apex/com.android.conscrypt/cacerts") {
		t.Fatalf("a mislabeled store was not unmounted:\n%s", log)
	}
}
