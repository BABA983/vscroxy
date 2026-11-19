//go:build linux

package system

import (
	"bytes"
	"encoding/json/jsontext"
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
			"XDG_CONFIG_DIRS":     t.TempDir(), // no system kioslaverc unless a test writes one
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
			if path == f.path {
				return os.ReadFile(path)
			}
			for _, dir := range filepath.SplitList(f.env["XDG_CONFIG_DIRS"]) {
				if dir != "" && path == filepath.Join(dir, kdeProxyFile) {
					return os.ReadFile(path)
				}
			}
			return nil, fmt.Errorf("unexpected config path %q", path)
		},
		atomicWrite: func(path string, data []byte) error {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, data, info.Mode().Perm()); err != nil {
				return err
			}
			f.reloadValues()
			return nil
		},
	}
	t.Cleanup(func() { kdeCmd = previous })
}

// reloadValues lets tests inspect the same physical file the restoration
// edits, including literal flagged lines put back by the raw-line writer.
func (f *fakeKDEDesktop) reloadValues() {
	data, err := os.ReadFile(f.path)
	if err != nil {
		panic(err)
	}
	parsed := parseKDEProxyConfig(data)
	f.values = map[string]string{}
	for key, value := range parsed.Values {
		f.values[key+parsed.Flags[key]] = value
	}
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
			// A plain delete removes every spelling of that key. A flagged
			// key argument is a literal key name to kwriteconfig, not a flag.
			base, flag := splitKDEKeyFlag(key)
			for stored := range f.values {
				storedBase, storedFlag := splitKDEKeyFlag(stored)
				match := stored == kconfigEscapeKey(key)
				if flag == "" && (storedBase == base || strings.HasPrefix(stored, kconfigEscapeKey(base)+`\x5b`)) {
					match = true
				} else if flag != "" && storedBase == base && storedFlag == flag {
					match = true
				}
				if match {
					delete(f.values, stored)
				}
			}
		case len(args) == 7:
			// kwriteconfig takes a logical value but a literal key name, so
			// a bracketed flag becomes an escaped, non-flagged key in the file.
			f.values[kconfigEscapeKey(key)] = args[6]
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
		fmt.Fprintf(&text, "%s=%s\n", key, kconfigEscapeValue(f.values[key]))
	}
	if err := os.WriteFile(f.path, []byte(text.String()), 0o600); err != nil {
		panic(err)
	}
}

// writeSystemKioslaverc puts a file in the first XDG_CONFIG_DIRS directory,
// which KConfig cascades beneath the user's file.
func (f *fakeKDEDesktop) writeSystemKioslaverc(t *testing.T, text string) {
	t.Helper()
	dirs := filepath.SplitList(f.env["XDG_CONFIG_DIRS"])
	if len(dirs) == 0 || dirs[0] == "" {
		t.Fatalf("no system config directory: %q", f.env["XDG_CONFIG_DIRS"])
	}
	if err := os.WriteFile(filepath.Join(dirs[0], kdeProxyFile), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// These mirror the subset of KConfig's stringToPrintable that kwriteconfig
// uses: protect leading/trailing spaces, backslash, control characters, and
// escape bracketed flag keys. Values passed to kwriteconfig stay logical.
func kconfigEscapeKey(key string) string {
	key = strings.ReplaceAll(key, "[", `\x5b`)
	return strings.ReplaceAll(key, "]", `\x5d`)
}

func kconfigEscapeValue(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case ' ':
			if i == 0 || i == len(value)-1 {
				out.WriteString(`\s`)
			} else {
				out.WriteByte(' ')
			}
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\t':
			out.WriteString(`\t`)
		case '\r':
			out.WriteString(`\r`)
		default:
			out.WriteByte(value[i])
		}
	}
	return out.String()
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

func TestKDEEscapedValuesRoundTripByteForByte(t *testing.T) {
	pac := `file:///home/u/my proxy\x.pac`
	bypass := " .internal.example,localhost"
	f := newFakeKDEDesktop(t, initialKDEValues("2", pac, bypass))
	beforeFile, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := snapshotProxy("127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	if got := ptrText(before.KDE.Values[kdePACKey]); got != pac {
		t.Fatalf("PAC snapshot = %q, want logical %q", got, pac)
	}
	if got := ptrText(before.KDE.Values[kdeBypassKey]); got != bypass {
		t.Fatalf("NoProxyFor snapshot = %q, want logical %q", got, bypass)
	}
	bypassList := []string{"localhost", "*.corp.example"}
	if err := before.applyProxy("127.0.0.1:7890", bypassList, true); err != nil {
		t.Fatal(err)
	}
	f.commands = nil // restoration must edit lines directly, not call kwriteconfig
	if err := restoreProxy(before, "127.0.0.1:7890", bypassList, true); err != nil {
		t.Fatal(err)
	}
	afterFile, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeFile, afterFile) {
		t.Fatalf("kioslaverc changed:\nbefore %q\nafter  %q", beforeFile, afterFile)
	}
	for _, command := range f.commands {
		if command[0] == "kwriteconfig5" || command[0] == "kwriteconfig6" {
			t.Fatalf("restore went through kwriteconfig and could double-escape: %q", command)
		}
	}
}

func TestKDEExpandKeyFlagSurvivesEnableAndRestore(t *testing.T) {
	const flag = "[$e]"
	const original = "$NO_PROXY"
	values := initialKDEValues("2", "", "localhost")
	values[kdeBypassKey+flag] = original
	delete(values, kdeBypassKey)
	f := newFakeKDEDesktop(t, values)
	beforeFile, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := snapshotProxy("127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	if got := before.KDE.Flags[kdeBypassKey]; got != flag {
		t.Fatalf("NoProxyFor flag = %q, want %q", got, flag)
	}
	if got := ptrText(before.KDE.Values[kdeBypassKey]); got != original {
		t.Fatalf("flagged NoProxyFor = %q", got)
	}
	bypass := []string{"localhost", "*.corp.example"}
	if err := before.applyProxy("127.0.0.1:7890", bypass, true); err != nil {
		t.Fatal(err)
	}
	deleteAll := []string{"kwriteconfig5", "--file", kdeProxyFile, "--group", kdeProxyGroup, "--key", kdeBypassKey, "--delete"}
	if !f.hasCommand(deleteAll...) {
		t.Fatalf("apply did not clear the flagged key first: %q", f.commands)
	}
	if got := f.values[kdeBypassKey]; got != "localhost,*.corp.example" {
		t.Fatalf("enabled NoProxyFor = %q", got)
	}
	f.commands = nil // a flagged key must be restored as a raw line
	if err := restoreProxy(before, "127.0.0.1:7890", bypass, true); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(f.values, values) {
		t.Fatalf("restored = %q, want %q", f.values, values)
	}
	for _, command := range f.commands {
		if command[0] == "kwriteconfig5" || command[0] == "kwriteconfig6" {
			t.Fatalf("restore tried to write a flag with kwriteconfig: %q", command)
		}
	}
	afterFile, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeFile, afterFile) {
		t.Fatalf("flagged kioslaverc changed:\nbefore %q\nafter  %q", beforeFile, afterFile)
	}
}

// KConfig writes an immutable key as "NoProxyFor[$i]" and refuses writes to
// it; see the kconfigini.cpp KEY_OPTIONS parsing and writeEntries in
// https://github.com/KDE/kconfig.
func TestKDEImmutableKeyIsRejectedBeforeAnyWrite(t *testing.T) {
	values := initialKDEValues("1", "", "localhost")
	values[kdeBypassKey+kdeImmutableOption] = "locked.example"
	delete(values, kdeBypassKey)
	f := newFakeKDEDesktop(t, values)
	original, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := snapshotProxy("127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	if got := before.KDE.Flags[kdeBypassKey]; got != kdeImmutableOption {
		t.Fatalf("NoProxyFor flag = %q, want %q", got, kdeImmutableOption)
	}
	err = before.applyProxy("127.0.0.1:7890", []string{"localhost"}, true)
	locked, ok := errors.AsType[*KDELockedError](err)
	if !ok || !errors.Is(err, ErrKDELocked) || locked.Key != kdeBypassKey || locked.Group != "" {
		t.Fatalf("apply = %v, want a locked key error for %s", err, kdeBypassKey)
	}
	if !strings.Contains(locked.Error(), kdeBypassKey) {
		t.Fatalf("locked error does not name the key: %q", locked.Error())
	}
	assertNoKDEWrite(t, f, original)
}

// KConfig marks a locked group as "[Group][$i]" after its name; the group's
// entries are then all immutable, as in parseConfig of kconfigini.cpp.
func TestKDELockedGroupIsRejectedBeforeAnyWrite(t *testing.T) {
	f := newFakeKDEDesktop(t, initialKDEValues("1", "", "localhost"))
	original, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	header := []byte("[" + kdeProxyGroup + "]\n")
	lockedFile := bytes.Replace(original, header, []byte("["+kdeProxyGroup+"]"+kdeImmutableOption+"\n"), 1)
	if bytes.Equal(original, lockedFile) {
		t.Fatalf("no %q header in %q", header, original)
	}
	if err := os.WriteFile(f.path, lockedFile, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := snapshotProxy("127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	if !before.KDE.GroupLocked {
		t.Fatalf("group was not read as locked: %+v", before.KDE)
	}
	if got := ptrText(before.KDE.Values[kdeTypeKey]); got != "1" {
		t.Fatalf("ProxyType in the locked group = %q, want 1", got)
	}
	err = before.applyProxy("127.0.0.1:7890", nil, false)
	locked, ok := errors.AsType[*KDELockedError](err)
	if !ok || !errors.Is(err, ErrKDELocked) || locked.Group != kdeProxyGroup || locked.Key != "httpProxy" {
		t.Fatalf("apply = %v, want a locked group error naming a write key", err)
	}
	assertNoKDEWrite(t, f, lockedFile)
}

// KConfig reads XDG_CONFIG_DIRS files before the user's kioslaverc, so an
// immutable key, group or whole file at system level makes user values
// ineffective even when kwriteconfig writes the user file successfully.
func TestKDESystemLocksAreRejectedBeforeAnyWrite(t *testing.T) {
	for _, tc := range []struct {
		name, system, key, group string
	}{
		{"immutable key", "[" + kdeProxyGroup + "]\n" + kdeBypassKey + kdeImmutableOption + "=system.example\n", kdeBypassKey, ""},
		{"locked group", "[" + kdeProxyGroup + "]" + kdeImmutableOption + "\n" + kdeTypeKey + "=2\n", "httpProxy", kdeProxyGroup},
		{"immutable file", kdeImmutableOption + "\n[" + kdeProxyGroup + "]\n" + kdeTypeKey + "=2\n", "httpProxy", kdeProxyGroup},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeKDEDesktop(t, initialKDEValues("2", "", "localhost"))
			f.writeSystemKioslaverc(t, tc.system)
			original, err := os.ReadFile(f.path)
			if err != nil {
				t.Fatal(err)
			}
			before, err := snapshotProxy("127.0.0.1:7890")
			if err != nil {
				t.Fatal(err)
			}
			if tc.group == "" && before.KDE.Locks.Flags[tc.key] != kdeImmutableOption {
				t.Fatalf("system key lock = %q, want %q", before.KDE.Locks.Flags[tc.key], kdeImmutableOption)
			}
			if tc.name == "locked group" && !before.KDE.Locks.GroupLocked {
				t.Fatal("system group lock was not recorded")
			}
			if tc.name == "immutable file" && !before.KDE.Locks.FileLocked {
				t.Fatal("system file lock was not recorded")
			}
			err = before.applyProxy("127.0.0.1:7890", []string{"localhost"}, true)
			locked, ok := errors.AsType[*KDELockedError](err)
			if !ok || !errors.Is(err, ErrKDELocked) || locked.Key != tc.key || locked.Group != tc.group {
				t.Fatalf("apply = %v, want a locked %s error with key %q and group %q", err, tc.name, tc.key, tc.group)
			}
			assertNoKDEWrite(t, f, original)
		})
	}
}

func TestKDESystemDefaultWithoutLockDoesNotBlockWrites(t *testing.T) {
	f := newFakeKDEDesktop(t, initialKDEValues("2", "", "localhost"))
	f.writeSystemKioslaverc(t, "["+kdeProxyGroup+"]\n"+kdeTypeKey+"=2\n"+kdeBypassKey+"=system.example\n")
	before, err := snapshotProxy("127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	if len(before.KDE.Locks.Flags) != 0 || before.KDE.Locks.GroupLocked || before.KDE.Locks.FileLocked {
		t.Fatalf("plain system defaults were read as locks: %+v", before.KDE.Locks)
	}
	if err := before.applyProxy("127.0.0.1:7890", []string{"localhost", "*.corp.example"}, true); err != nil {
		t.Fatal(err)
	}
	if got := f.values[kdeBypassKey]; got != "localhost,*.corp.example" {
		t.Fatalf("enabled NoProxyFor = %q", got)
	}
	if !f.hasCommand("kwriteconfig5", "--file", kdeProxyFile, "--group", kdeProxyGroup, "--key", kdeTypeKey, "1") {
		t.Fatalf("apply did not write ProxyType: %q", f.commands)
	}
}

// Drive the real guard: deleting the LockedKey/LockedGroup copy in GuardProxy
// must fail this test, not just a reply built by hand.
func TestGuardReplyCarriesTheLockedKDEKeyAndGroup(t *testing.T) {
	f := newFakeKDEDesktop(t, initialKDEValues("2", "", "localhost"))
	f.writeSystemKioslaverc(t, "["+kdeProxyGroup+"]"+kdeImmutableOption+"\n"+kdeTypeKey+"=2\n")
	original, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	journal := filepath.Join(t.TempDir(), "proxy-recovery.json")
	if err := GuardProxy(journal, "127.0.0.1:7890", `{"list":["localhost"],"set":true}`, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	var reply proxyReply
	if err := json.UnmarshalDecode(jsontext.NewDecoder(&output), &reply); err != nil {
		t.Fatal(err)
	}
	err = reply.err()
	locked, ok := errors.AsType[*KDELockedError](err)
	if !ok || !errors.Is(err, ErrKDELocked) || locked.Key != "httpProxy" || locked.Group != kdeProxyGroup ||
		!strings.Contains(err.Error(), "administrator") {
		t.Fatalf("guard reply = %+v, error = %v; want a locked key and group", reply, err)
	}
	assertNoKDEWrite(t, f, original)
}

func assertNoKDEWrite(t *testing.T, f *fakeKDEDesktop, original []byte) {
	t.Helper()
	if len(f.commands) != 0 {
		t.Fatalf("a locked setting still ran commands: %q", f.commands)
	}
	after, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, after) {
		t.Fatalf("a locked setting changed kioslaverc:\nbefore %q\nafter  %q", original, after)
	}
}

func TestKDERestoreKeepsOriginalServersBesideAReplacement(t *testing.T) {
	f := newFakeKDEDesktop(t, initialKDEValues("2", "http://pac.example/original", "localhost"))
	before, err := snapshotProxy("127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	if err := before.applyProxy("127.0.0.1:7890", nil, false); err != nil {
		t.Fatal(err)
	}
	// Another program replaces one protocol while the manual proxy is active.
	if err := f.run("kwriteconfig5", "--file", kdeProxyFile, "--group", kdeProxyGroup,
		"--key", "httpsProxy", "http://other.example 9999"); err != nil {
		t.Fatal(err)
	}
	if err := restoreProxy(before, "127.0.0.1:7890", nil, false); err != nil {
		t.Fatal(err)
	}
	if got := f.values["httpProxy"]; got != "http://corporate.example 3128" {
		t.Fatalf("original HTTP server was not restored: %q", got)
	}
	if got := f.values["httpsProxy"]; got != "http://other.example 9999" {
		t.Fatalf("replacement HTTPS server was lost: %q", got)
	}
	if got := f.values[kdeTypeKey]; got != "2" {
		t.Fatalf("ProxyType = %q, want the original 2", got)
	}
	if got := f.values[kdePACKey]; got != "http://pac.example/original" {
		t.Fatalf("PAC URL = %q", got)
	}
}

func TestKDEDecodesKConfigEscapes(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`file:///home/u/my proxy\\x.pac`, `file:///home/u/my proxy\x.pac`},
		{`\s leading`, "  leading"},
		{`trailing\s`, "trailing "},
		{`a\tb\nc\rd`, "a\tb\nc\rd"},
		{`a\\b`, `a\b`},
		{`a\;b\,c`, `a\;b\,c`}, // KConfig keeps these two escapes
		{`\x41\x42`, "AB"},
		{`a\qb`, `a\qb`},
	} {
		if got := decodeKDEEscapes(tc.raw); got != tc.want {
			t.Errorf("decodeKDEEscapes(%q) = %q, want %q", tc.raw, got, tc.want)
		}
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
	if got := f.values["httpProxy"]; got != "http://corporate.example 3128" {
		t.Fatalf("restoration did not put back the previous server: %q", got)
	}
	if !f.hasCommand("dbus-send", "--type=signal", "/KIO/Scheduler",
		"org.kde.KIO.Scheduler.reparseSlaveConfiguration", "string:") {
		t.Fatalf("restoration did not notify KIO: %q", f.commands)
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
		lookPath:    exec.LookPath,
		atomicWrite: kdeAtomicWrite,
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
	backslash := string([]byte{92})
	initial := initialKDEValues("2", "file:///home/u/my proxy"+backslash+"x.pac", " .internal.example,localhost")
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
	if !bytes.Equal(beforeText, restored) {
		t.Fatalf("real kwriteconfig changed the file:\nbefore %q\nafter  %q", beforeText, restored)
	}
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

func TestKDERealKWriteConfigFlaggedKeyRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("kwriteconfig6"); err != nil {
		if _, err := exec.LookPath("kwriteconfig5"); err != nil {
			t.Skip("kwriteconfig6 and kwriteconfig5 are not installed")
		}
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
	previous := kdeCmd
	kdeCmd = kdeCommands{
		lookPath:    exec.LookPath,
		atomicWrite: kdeAtomicWrite,
		run: func(name string, args ...string) error {
			if name == "dbus-send" {
				return nil // no session bus in the orb
			}
			return run(name, args...)
		},
		env:      os.Getenv,
		readFile: os.ReadFile,
	}
	t.Cleanup(func() { kdeCmd = previous })
	slash := string([]byte{92})
	// This is the physical file KDE writes for an environment-expanded key.
	initial := "[Proxy Settings]\n" +
		"NoProxyFor[$e]=$HOME_HOST," + slash + "slocalhost\n" +
		"Proxy Config Script=file:///home/u/my proxy" + slash + slash + "x.pac\n" +
		"ProxyType=2\n" +
		"httpProxy=http://old.example 3128\n"
	path := filepath.Join(dir, kdeProxyFile)
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeText, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := snapshotProxy("127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	if got := before.KDE.Flags[kdeBypassKey]; got != "[$e]" {
		t.Fatalf("NoProxyFor flag = %q, want [$e]", got)
	}
	if got := ptrText(before.KDE.Values[kdeBypassKey]); got != "$HOME_HOST, localhost" {
		t.Fatalf("flagged NoProxyFor = %q", got)
	}
	bypass := []string{"localhost", "*.corp"}
	if err := before.applyProxy("127.0.0.1:7890", bypass, true); err != nil {
		t.Fatal(err)
	}
	enabled, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("enabled:\n%s", enabled)
	if err := restoreProxy(before, "127.0.0.1:7890", bypass, true); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("before:\n%s", beforeText)
	t.Logf("restored:\n%s", restored)
	if !bytes.Equal(beforeText, restored) {
		t.Fatalf("flagged kioslaverc changed:\nbefore %q\nafter  %q", beforeText, restored)
	}
	if !strings.Contains(string(restored), "NoProxyFor[$e]=") || strings.Contains(string(restored), `\x5b`) {
		t.Fatalf("flagged key was replaced by a literal key: %q", restored)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %v, want 0600", got)
	}
}
