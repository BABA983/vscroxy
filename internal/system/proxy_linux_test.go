//go:build linux

package system

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "--proxy-guard" {
		if err := GuardProxy(os.Args[2], os.Args[3], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) == 4 && os.Args[1] == "--proxy-owner" {
		session, err := BeginProxy(os.Args[2], os.Args[3])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, session.cmd.Process.Pid)
		io.Copy(io.Discard, os.Stdin)
		if err := session.Restore(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// gsettingsReplies is the desktop's answer for one configuration, keyed the way
// gsettings is asked.
func gsettingsReplies(mode, httpHost, httpPort, httpsHost, httpsPort, same, pac string) map[string]string {
	return map[string]string{
		"gsettings get org.gnome.system.proxy mode":           "'" + mode + "'",
		"gsettings get org.gnome.system.proxy autoconfig-url": "'" + pac + "'",
		"gsettings get org.gnome.system.proxy use-same-proxy": same,
		"gsettings get org.gnome.system.proxy.http host":      "'" + httpHost + "'",
		"gsettings get org.gnome.system.proxy.http port":      httpPort,
		"gsettings get org.gnome.system.proxy.https host":     "'" + httpsHost + "'",
		"gsettings get org.gnome.system.proxy.https port":     httpsPort,
	}
}

func TestReadProxyReadsTheDesktopSettings(t *testing.T) {
	target := "127.0.0.1:7890"
	for _, tc := range []struct {
		name    string
		replies map[string]string
		want    ProxyConfig
		status  ProxyStatus
	}{
		{
			name:    "manual with one proxy for every protocol",
			replies: gsettingsReplies("manual", "127.0.0.1", "7890", "", "0", "true", ""),
			want:    manual("Desktop", endpoint("127.0.0.1", 7890), endpoint("127.0.0.1", 7890)),
			status:  ProxyMatches,
		},
		{
			name:    "manual with separate HTTPS",
			replies: gsettingsReplies("manual", "127.0.0.1", "7890", "127.0.0.1", "7891", "false", ""),
			want:    manual("Desktop", endpoint("127.0.0.1", 7890), endpoint("127.0.0.1", 7891)),
			status:  ProxyMismatch,
		},
		{
			name:    "automatic script",
			replies: gsettingsReplies("auto", "127.0.0.1", "7890", "", "0", "true", "http://pac.example/proxy.pac"),
			want:    ProxyConfig{Scope: "Desktop", Mode: ProxyModeAutomatic, PAC: "http://pac.example/proxy.pac"},
			status:  ProxyAutomatic,
		},
		{
			name:    "off",
			replies: gsettingsReplies("none", "127.0.0.1", "7890", "", "0", "true", ""),
			want:    ProxyConfig{Scope: "Desktop"},
			status:  ProxyOff,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configs, err := readProxy(fakeCommands(t, tc.replies))
			if err != nil {
				t.Fatal(err)
			}
			if len(configs) != 1 || configs[0] != tc.want {
				t.Fatalf("configs = %+v, want %+v", configs, tc.want)
			}
			if got := checkProxy(target, configs, nil).Status; got != tc.status {
				t.Fatalf("status = %v, want %v", got, tc.status)
			}
		})
	}
}

func TestReadProxyReportsAMissingDesktopTool(t *testing.T) {
	out := func(string, ...string) ([]byte, error) { return nil, errors.New("gsettings not found") }
	configs, err := readProxy(out)
	if err == nil || configs != nil {
		t.Fatalf("read = %+v, %v; want an unreadable configuration", configs, err)
	}
	if check := checkProxy("127.0.0.1:7890", configs, err); check.Status != ProxyUnknown || check.Err == nil {
		t.Fatalf("check = %+v; an unreadable configuration must keep its reason", check)
	}
}

func TestGVariantReadsQuotedStrings(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"'manual'\n", "manual"},
		{"'it\\'s'\n", "it's"},
		{"''\n", ""},
		{"7890\n", "7890"},
		{"true\n", "true"},
	} {
		if got := gvariant(tc.raw); got != tc.want {
			t.Errorf("gvariant(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// fakeRecoveryDesktop is a disposable gsettings source shared by real owner
// and guardian processes, never the settings of the machine running the test.
func fakeRecoveryDesktop(t *testing.T) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
key="$2.$3"
case $1 in
get) cat "$PROXY_TEST_STATE/$key";;
set)
  if [ -f "$PROXY_TEST_STATE/deny" ]; then echo 'authorization denied' >&2; exit 1; fi
  if [ "$key" = 'org.gnome.system.proxy.http.port' ] && [ -f "$PROXY_TEST_STATE/pause" ]; then
    touch "$PROXY_TEST_STATE/paused"
    while [ -f "$PROXY_TEST_STATE/pause" ]; do sleep 0.01; done
  fi
  value="$4"
  case "$key" in
    *.host|*.mode) case "$value" in \'*) ;; *) value="'$value'";; esac;;
  esac
  printf '%s' "$value" > "$PROXY_TEST_STATE/$key";;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gsettings"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	before := gsettingsReplies("manual", "corporate.example", "3128", "secure.example", "8443", "false", "http://pac.example/original")
	before["gsettings get org.gnome.system.proxy ignore-hosts"] = "['localhost', '*.internal.example']"
	for command, value := range before {
		args := strings.Fields(command)
		if err := os.WriteFile(filepath.Join(dir, args[2]+"."+args[3]), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PROXY_TEST_STATE", dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir, before
}

func assertRecoverySettings(t *testing.T, before map[string]string) {
	t.Helper()
	for command, want := range before {
		args := strings.Fields(command)
		got, err := commandOutput(args[0], args[1:]...)
		if err != nil || strings.TrimSpace(string(got)) != want {
			t.Fatalf("%s = %q, %v; want %q", command, got, err, want)
		}
	}
}

func TestProxySessionRestoresPreviousSettingsAndKeepsReplacement(t *testing.T) {
	for _, scenario := range []string{"manual", "none", "auto", "replace HTTPS", "replace HTTPS while off"} {
		t.Run(scenario, func(t *testing.T) {
			dir, before := fakeRecoveryDesktop(t)
			if scenario == "none" || scenario == "auto" || scenario == "replace HTTPS while off" {
				mode := scenario
				if scenario == "replace HTTPS while off" {
					mode = "none"
				}
				if err := run("gsettings", "set", proxySchema, "mode", mode); err != nil {
					t.Fatal(err)
				}
				before["gsettings get org.gnome.system.proxy mode"] = "'" + mode + "'"
			}
			path := filepath.Join(dir, "recovery.json")
			session, err := BeginProxy(path, "127.0.0.1:7890")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { session.Restore() })
			if check := CheckProxy("127.0.0.1:7890"); check.Status != ProxyMatches {
				t.Fatalf("enable = %+v", check)
			}
			if err := RecoverProxy(path); err == nil {
				t.Fatal("startup recovery must not change an active session")
			}
			other, err := BeginProxy(path, "127.0.0.1:7891")
			if err == nil || other != nil {
				t.Fatal("another app must not take ownership of an active journal")
			}
			if strings.HasPrefix(scenario, "replace HTTPS") {
				if err := run("gsettings", "set", proxySchema+".https", "port", "8081"); err != nil {
					t.Fatal(err)
				}
				before["gsettings get org.gnome.system.proxy.https host"] = "'127.0.0.1'"
				before["gsettings get org.gnome.system.proxy.https port"] = "8081"
				before["gsettings get org.gnome.system.proxy ignore-hosts"] = "['localhost', '127.0.0.1', '::1']"
				if scenario == "replace HTTPS while off" {
					before["gsettings get org.gnome.system.proxy mode"] = "'manual'"
					before["gsettings get org.gnome.system.proxy.http host"] = "''"
				}
			}
			if err := session.Restore(); err != nil {
				t.Fatal(err)
			}
			assertRecoverySettings(t, before)
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("completed journal was retained: %v", err)
			}
		})
	}
}

func TestProxySessionRetainsFailedRecoveryForRetry(t *testing.T) {
	dir, before := fakeRecoveryDesktop(t)
	path := filepath.Join(dir, "recovery.json")
	session, err := BeginProxy(path, "127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(filepath.Join(dir, "deny")); session.Restore() })
	if err := os.WriteFile(filepath.Join(dir, "deny"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := session.Restore(); err == nil {
		t.Fatal("a rejected restore must not report success")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("failed recovery lost the original settings", err)
	}
	if err := os.Remove(filepath.Join(dir, "deny")); err != nil {
		t.Fatal(err)
	}
	if err := session.Restore(); err != nil {
		t.Fatal(err)
	}
	assertRecoverySettings(t, before)
}

func TestProxyRecoverySurvivesOwnerSIGKILL(t *testing.T) {
	for _, scenario := range []string{"enabled", "during enable", "guardian also lost"} {
		t.Run(scenario, func(t *testing.T) {
			dir, before := fakeRecoveryDesktop(t)
			path := filepath.Join(dir, "recovery.json")
			if scenario == "during enable" {
				if err := os.WriteFile(filepath.Join(dir, "pause"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(executable, "--proxy-owner", path, "127.0.0.1:7890")
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			t.Cleanup(func() { cmd.Process.Kill(); os.Remove(filepath.Join(dir, "pause")) })
			if scenario == "during enable" {
				waitRecovery(t, func() bool { _, err := os.Stat(filepath.Join(dir, "paused")); return err == nil })
			} else {
				ready := make(chan string, 1)
				go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- strings.TrimSpace(line) }()
				select {
				case line := <-ready:
					pid, err := strconv.Atoi(line)
					if err != nil {
						t.Fatalf("owner did not enable its proxy: %q", line)
					}
					if scenario == "guardian also lost" {
						process, err := os.FindProcess(pid)
						if err != nil {
							t.Fatal(err)
						}
						if err := process.Kill(); err != nil {
							t.Fatal(err)
						}
						process.Release()
					}
				case <-time.After(10 * time.Second):
					t.Fatal("owner did not finish enabling")
				}
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			cmd.Wait()
			os.Remove(filepath.Join(dir, "pause"))
			if scenario == "guardian also lost" {
				waitRecovery(t, func() bool { return RecoverProxy(path) == nil })
			} else {
				waitRecovery(t, func() bool { _, err := os.Stat(path); return errors.Is(err, os.ErrNotExist) })
			}
			assertRecoverySettings(t, before)
		})
	}
}

func waitRecovery(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for proxy recovery")
}
