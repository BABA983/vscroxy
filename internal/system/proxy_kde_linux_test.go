//go:build linux

package system

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeKDEDesktop is an injectable KDE command runner with a disposable
// kioslaverc. It records every command and applies kwriteconfig's file edits,
// so a test can assert both without a Plasma session.
type fakeKDEDesktop struct {
	path     string
	values   map[string]string
	head     string
	commands [][]string
	lookPath map[string]error
	env      map[string]string
	deny     bool
}

func newFakeKDEDesktop(t *testing.T, values map[string]string) *fakeKDEDesktop {
	t.Helper()
	dir := t.TempDir()
	f := &fakeKDEDesktop{
		path:   filepath.Join(dir, kdeProxyFile),
		values: maps.Clone(values),
		head:   "ProxyUrlDisplayFlags=15\n\n",
		lookPath: map[string]error{
			"kwriteconfig6": exec.ErrNotFound, // exercise the 6 → 5 fallback
		},
		env: map[string]string{
			"XDG_CURRENT_DESKTOP": "KDE",
			"XDG_CONFIG_HOME":     dir,
		},
	}
	f.writeConfig()
	f.install(t)
	return f
}

func (f *fakeKDEDesktop) install(t *testing.T) {
	t.Helper()
	previous := kdeCmd
	kdeCmd = kdeCommands{
		lookPath: func(name string) (string, error) {
			if err := f.lookPath[name]; err != nil {
				return "", err
			}
			return "/fake/bin/" + name, nil
		},
		run: f.run,
		env: func(key string) string { return f.env[key] },
		readFile: func(path string) ([]byte, error) {
			if path != f.path {
				return nil, fmt.Errorf("unexpected config path %q", path)
			}
			return os.ReadFile(path)
		},
	}
	t.Cleanup(func() { kdeCmd = previous })
}

func (f *fakeKDEDesktop) run(name string, args ...string) error {
	f.commands = append(f.commands, append([]string{name}, args...))
	if f.deny {
		return errors.New("authorization denied")
	}
	switch name {
	case "kwriteconfig6", "kwriteconfig5":
		if len(args) < 6 || args[0] != "--file" || args[1] != kdeProxyFile ||
			args[2] != "--group" || args[3] != kdeProxyGroup || args[4] != "--key" {
			return fmt.Errorf("unexpected kwriteconfig arguments %q", args)
		}
		key := args[5]
		switch {
		case slices.Equal(args[6:], []string{"--delete"}):
			delete(f.values, key)
		case len(args) == 7:
			f.values[key] = args[6]
		default:
			return fmt.Errorf("unexpected kwriteconfig value %q", args[6:])
		}
		f.writeConfig()
		return nil
	case "dbus-send":
		return nil
	default:
		return fmt.Errorf("unexpected command %q", name)
	}
}

func (f *fakeKDEDesktop) writeConfig() {
	keys := make([]string, 0, len(f.values))
	for key := range f.values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var text strings.Builder
	text.WriteString(f.head)
	text.WriteString("[" + kdeProxyGroup + "]\n")
	for _, key := range keys {
		fmt.Fprintf(&text, "%s=%s\n", key, f.values[key])
	}
	if err := os.WriteFile(f.path, []byte(text.String()), 0o600); err != nil {
		panic(err)
	}
}

func (f *fakeKDEDesktop) hasCommand(want ...string) bool {
	for _, command := range f.commands {
		if slices.Equal(command, want) {
			return true
		}
	}
	return false
}

// A global [Proxy Settings] key outside the section, and a custom key inside
// it, make the round trip test check more than the keys vscroxy writes.
func initialKDEValues(proxyType, pac, bypass string) map[string]string {
	return map[string]string{
		kdeTypeKey:   proxyType,
		kdePACKey:    pac,
		kdeBypassKey: bypass,
		"httpProxy":  "http://corporate.example 3128",
		"httpsProxy": "http://secure.example 8443",
		"ftpProxy":   "ftp://ftp.example 2121",
		"socksProxy": "socks://socks.example 1080",
		"AuthMode":   "2",
	}
}

func TestKDESetAndRestoreRoundTripEveryProxyKey(t *testing.T) {
	for _, tc := range []struct {
		name, proxyType, pac, bypass string
	}{
		{"manual with custom exceptions", "1", "http://pac.example/original", "localhost,.internal.example,10.0.0.0/8"},
		{"PAC with custom exceptions", "2", "http://pac.example/original.pac", "localhost,127.0.0.1"},
		{"auto-detect", "3", "", "localhost"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeKDEDesktop(t, initialKDEValues(tc.proxyType, tc.pac, tc.bypass))
			original := maps.Clone(f.values)
			before, err := snapshotProxy("127.0.0.1:7890")
			if err != nil {
				t.Fatal(err)
			}
			if before.GNOME != nil || before.KDE.Path != f.path {
				t.Fatalf("snapshot = %+v, want only the KDE file at %q", before, f.path)
			}
			for key, want := range original {
				if got := ptrText(before.KDE.Values[key]); got != want {
					t.Fatalf("snapshot %s = %q, want %q", key, got, want)
				}
			}
			bypass := []string{"localhost", "*.corp.example", "10.0.0.0/8"}
			if err := before.applyProxy("127.0.0.1:7890", bypass, true); err != nil {
				t.Fatal(err)
			}
			if got := f.values[kdeTypeKey]; got != "1" {
				t.Fatalf("enabled ProxyType = %q, want 1", got)
			}
			for _, key := range []string{"httpProxy", "httpsProxy", "ftpProxy", "socksProxy"} {
				if got := f.values[key]; got != "http://127.0.0.1 7890" {
					t.Fatalf("enabled %s = %q", key, got)
				}
			}
			if got := f.values[kdeBypassKey]; got != "localhost,*.corp.example,10.0.0.0/8" {
				t.Fatalf("enabled NoProxyFor = %q", got)
			}
			if got := f.values[kdePACKey]; got != tc.pac {
				t.Fatalf("enabled PAC = %q, want %q", got, tc.pac)
			}
			if err := restoreProxy(before, "127.0.0.1:7890", bypass, true); err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(f.values, original) {
				t.Fatalf("restored = %q, want %q exactly", f.values, original)
			}

			// The same snapshot must survive the journal that crash recovery reads.
			data, err := json.Marshal(proxyJournal{Version: 2, Target: "127.0.0.1:7890", Before: before, Bypass: bypass, BypassSet: true})
			if err != nil {
				t.Fatal(err)
			}
			var journal proxyJournal
			if err := json.Unmarshal(data, &journal); err != nil {
				t.Fatal(err)
			}
			if err := journal.Before.applyProxy("127.0.0.1:7890", bypass, true); err != nil {
				t.Fatal(err)
			}
			if err := restoreProxy(journal.Before, journal.Target, journal.Bypass, journal.BypassSet); err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(f.values, original) {
				t.Fatalf("journal restore = %q, want %q exactly", f.values, original)
			}
		})
	}
}

func TestKDEBypassMappingAndUnconfiguredList(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bypass    []string
		set       bool
		wantWrite string
	}{
		{"mapped", []string{"localhost", "*.corp.example", "10.0.0.0/8"}, true, "localhost,*.corp.example,10.0.0.0/8"},
		{"not configured", nil, false, "localhost,.internal.example"},
		{"explicitly empty", nil, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeKDEDesktop(t, initialKDEValues("1", "", "localhost,.internal.example"))
			before, err := snapshotProxy("127.0.0.1:7890")
			if err != nil {
				t.Fatal(err)
			}
			if err := before.applyProxy("127.0.0.1:7890", tc.bypass, tc.set); err != nil {
				t.Fatal(err)
			}
			if got := f.values[kdeBypassKey]; got != tc.wantWrite {
				t.Fatalf("NoProxyFor = %q, want %q", got, tc.wantWrite)
			}
			if err := restoreProxy(before, "127.0.0.1:7890", tc.bypass, tc.set); err != nil {
				t.Fatal(err)
			}
			if got := f.values[kdeBypassKey]; got != "localhost,.internal.example" {
				t.Fatalf("restored NoProxyFor = %q", got)
			}
		})
	}
}

func TestKDEUsesKWriteConfig5When6IsMissingAndSignalsKIO(t *testing.T) {
	f := newFakeKDEDesktop(t, initialKDEValues("1", "", "localhost"))
	before, err := snapshotProxy("127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	if err := before.applyProxy("127.0.0.1:7890", nil, false); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "httpProxy", "http://127.0.0.1 7890"},
		{"kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "httpsProxy", "http://127.0.0.1 7890"},
		{"kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ftpProxy", "http://127.0.0.1 7890"},
		{"kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "socksProxy", "http://127.0.0.1 7890"},
		{"kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "1"},
	}
	if !slices.EqualFunc(f.commands[:len(want)], want, slices.Equal) {
		t.Fatalf("enable commands = %q, want %q", f.commands, want)
	}
	if !f.hasCommand("dbus-send", "--type=signal", "/KIO/Scheduler",
		"org.kde.KIO.Scheduler.reparseSlaveConfiguration", "string:") {
		t.Fatalf("KIO was not notified: %q", f.commands)
	}
	f.commands = nil
	if err := restoreProxy(before, "127.0.0.1:7890", nil, false); err != nil {
		t.Fatal(err)
	}
	for _, command := range f.commands {
		if command[0] == "kwriteconfig6" {
			t.Fatalf("restoration tried the missing kwriteconfig6: %q", command)
		}
	}
	if !f.hasCommand("kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "httpProxy", "http://corporate.example 3128") {
		t.Fatalf("restoration did not put back the previous server: %q", f.commands)
	}
}

func TestKDERecoveryFromJournalAfterCrash(t *testing.T) {
	for _, scenario := range []string{"journal after owner crash", "guardian EOF"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFakeKDEDesktop(t, initialKDEValues("2", "http://pac.example/original", "localhost,.internal.example"))
			original := maps.Clone(f.values)
			path := filepath.Join(t.TempDir(), "recovery.json")
			bypass := []string{"localhost", "*.corp.example"}
			if scenario == "journal after owner crash" {
				before, err := snapshotProxy("127.0.0.1:7890")
				if err != nil {
					t.Fatal(err)
				}
				if err := saveProxyJournal(path, proxyJournal{Version: 2, Target: "127.0.0.1:7890", Before: before, Bypass: bypass, BypassSet: true}); err != nil {
					t.Fatal(err)
				}
				if err := before.applyProxy("127.0.0.1:7890", bypass, true); err != nil {
					t.Fatal(err)
				}
				if err := RecoverProxy(path); err != nil {
					t.Fatal(err)
				}
			} else {
				var reply strings.Builder
				bypassJSON := `{"list":["localhost","*.corp.example"],"set":true}`
				if err := GuardProxy(path, "127.0.0.1:7890", bypassJSON, strings.NewReader(""), &reply); err != nil {
					t.Fatal(err)
				}
				if got := f.values[kdeTypeKey]; got != "2" {
					t.Fatalf("EOF did not roll back ProxyType: %q", got)
				}
			}
			if !maps.Equal(f.values, original) {
				t.Fatalf("recovered = %q, want %q exactly", f.values, original)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("journal was not removed: %v", err)
			}
		})
	}
}

func TestBothDesktopsAreJournaledAndRestoredTogether(t *testing.T) {
	dir, gnomeBefore := fakeRecoveryDesktop(t)
	f := newFakeKDEDesktop(t, initialKDEValues("1", "http://pac.example/original", "localhost,.internal.example"))
	f.env["XDG_CURRENT_DESKTOP"] = "GNOME:KDE"
	kdeBefore := maps.Clone(f.values)
	before, err := snapshotProxy("127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	if before.GNOME == nil || before.KDE.Path != f.path {
		t.Fatalf("snapshot = %+v, want both desktops", before)
	}
	if err := before.applyProxy("127.0.0.1:7890", nil, false); err != nil {
		t.Fatal(err)
	}
	gnomeState, err := readProxySnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if gnomeState["http.host"] != "'127.0.0.1'" || gnomeState["http.port"] != "7890" || gnomeState["mode"] != "'manual'" {
		t.Fatalf("GNOME was not enabled: %q", gnomeState)
	}
	if f.values["ProxyType"] != "1" || f.values["httpProxy"] != "http://127.0.0.1 7890" {
		t.Fatalf("KDE was not enabled: %q", f.values)
	}
	if err := restoreProxy(before, "127.0.0.1:7890", nil, false); err != nil {
		t.Fatal(err)
	}
	assertRecoverySettings(t, gnomeBefore)
	if !maps.Equal(f.values, kdeBefore) {
		t.Fatalf("restored KDE = %q, want %q exactly", f.values, kdeBefore)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}

func TestKDEDesktopDetectionOnAsymmetricEnvironments(t *testing.T) {
	for _, tc := range []struct {
		name      string
		xdg, full string
		wantGnome bool
		wantKDE   bool
	}{
		{"plain session keeps gsettings", "", "", true, false},
		{"GNOME", "ubuntu:GNOME", "", true, false},
		{"Plasma", "KDE", "", false, true},
		{" Plasma name", "plasma", "", false, true},
		{"KDE_FULL_SESSION alone", "", "true", false, true},
		{"GNOME plus KDE_FULL_SESSION", "GNOME", "1", true, true},
		{"both identifiers", "KDE:GNOME", "", true, true},
		{"KDE with a stale zero full session", "KDE", "0", false, true},
		{"unrelated desktop", "X-Cinnamon", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"XDG_CURRENT_DESKTOP": tc.xdg, "KDE_FULL_SESSION": tc.full}
			got := activeDesktops(func(key string) string { return env[key] })
			if got.gnome != tc.wantGnome || got.kde != tc.wantKDE {
				t.Fatalf("activeDesktops = %+v, want gnome=%v kde=%v", got, tc.wantGnome, tc.wantKDE)
			}
		})
	}
}

func TestKDEReadProxyReportsEveryProxyType(t *testing.T) {
	for _, tc := range []struct {
		name, proxyType, pac string
		mode                 ProxyMode
		pacRead              string
		scope                string
	}{
		{"manual", "1", "", ProxyModeManual, "", "KDE"},
		{"PAC", "2", "http://pac.example/x.pac", ProxyModeAutomatic, "http://pac.example/x.pac", "KDE"},
		{"auto-detect", "3", "", ProxyModeAutomatic, "", "KDE"},
		{"environment", "4", "", ProxyModeAutomatic, "", "KDE environment"},
		{"off", "0", "", ProxyModeOff, "", "KDE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := initialKDEValues(tc.proxyType, tc.pac, "localhost")
			newFakeKDEDesktop(t, values)
			out := func(string, ...string) ([]byte, error) { return nil, errors.New("gsettings must not be read on KDE") }
			configs, err := readProxy(out)
			if err != nil {
				t.Fatal(err)
			}
			want := ProxyConfig{Scope: tc.scope, Mode: tc.mode, PAC: tc.pacRead}
			if tc.mode == ProxyModeManual {
				want.HTTP = endpoint("corporate.example", 3128)
				want.HTTPS = endpoint("secure.example", 8443)
			}
			if len(configs) != 1 || configs[0] != want {
				t.Fatalf("configs = %+v, want %+v", configs, want)
			}
			if got := CheckProxy("127.0.0.1:7890"); got.Err != nil {
				t.Fatalf("CheckProxy = %+v", got)
			}
		})
	}
}

func TestKDEMissingToolsLeaveSettingsAlone(t *testing.T) {
	f := newFakeKDEDesktop(t, initialKDEValues("1", "", "localhost"))
	f.lookPath["kwriteconfig5"] = exec.ErrNotFound
	before, err := snapshotProxy("127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	original := maps.Clone(f.values)
	if err := before.applyProxy("127.0.0.1:7890", nil, false); !errors.Is(err, ErrKDEHelpersMissing) {
		t.Fatalf("apply = %v, want ErrKDEHelpersMissing", err)
	}
	if !maps.Equal(f.values, original) {
		t.Fatalf("a failed apply changed the config: %q", f.values)
	}
	for _, command := range f.commands {
		if command[0] == "kwriteconfig5" || command[0] == "kwriteconfig6" {
			t.Fatalf("a missing tool was invoked: %q", command)
		}
	}
}

func TestKDERealKWriteConfigRoundTrip(t *testing.T) {
	var have bool
	for _, name := range []string{"kwriteconfig6", "kwriteconfig5"} {
		if _, err := exec.LookPath(name); err == nil {
			have = true
			break
		}
	}
	if !have {
		t.Skip("kwriteconfig6 and kwriteconfig5 are not installed")
	}
	dir := t.TempDir()
	// kwriteconfig also reads the real process environment, not kdeCmd.env.
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
	previous := kdeCmd
	kdeCmd = kdeCommands{
		lookPath: exec.LookPath,
		run: func(name string, args ...string) error {
			if name == "dbus-send" {
				return nil // no session bus in the orb
			}
			return run(name, args...)
		},
		env: func(key string) string {
			switch key {
			case "XDG_CURRENT_DESKTOP":
				return "KDE"
			case "XDG_CONFIG_HOME":
				return dir
			case "HOME":
				return dir
			}
			return ""
		},
		readFile: os.ReadFile,
	}
	t.Cleanup(func() { kdeCmd = previous })
	initial := initialKDEValues("2", "http://pac.example/original", "localhost,.internal.example")
	command := ""
	for _, name := range []string{"kwriteconfig6", "kwriteconfig5"} {
		if _, err := exec.LookPath(name); err == nil {
			command = name
			break
		}
	}
	for _, key := range slices.Sorted(maps.Keys(initial)) {
		if err := run(command, "--file", kdeProxyFile, "--group", kdeProxyGroup, "--key", key, initial[key]); err != nil {
			t.Fatal(err)
		}
	}
	beforeText, err := os.ReadFile(filepath.Join(dir, kdeProxyFile))
	if err != nil {
		t.Fatal(err)
	}
	before, err := snapshotProxy("127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("before:\n%s", beforeText)
	if err := before.applyProxy("127.0.0.1:7890", []string{"localhost", "*.corp.example"}, true); err != nil {
		t.Fatal(err)
	}
	enabled, err := os.ReadFile(filepath.Join(dir, kdeProxyFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("enabled:\n%s", enabled)
	if err := restoreProxy(before, "127.0.0.1:7890", []string{"localhost", "*.corp.example"}, true); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(filepath.Join(dir, kdeProxyFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("restored:\n%s", restored)
	state, err := kdeReadConfigAt(filepath.Join(dir, kdeProxyFile))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range initial {
		if got := ptrText(state.Values[key]); got != want {
			t.Fatalf("restored %s = %q, want %q", key, got, want)
		}
	}
}
