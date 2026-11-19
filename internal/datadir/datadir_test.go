package datadir

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRootPrefersFlagThenEnvThenHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv(Env, "")
	Reset()
	t.Cleanup(Reset)

	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".vscroxy"); root != want {
		t.Fatalf("default root = %q, want %q", root, want)
	}

	fromEnv := filepath.Join(t.TempDir(), "from-env")
	Reset()
	t.Setenv(Env, fromEnv)
	root, err = Root()
	if err != nil || root != fromEnv {
		t.Fatalf("env root = %q, %v; want %q", root, err, fromEnv)
	}

	fromFlag := filepath.Join(t.TempDir(), "from-flag")
	Reset()
	SetFlag(fromFlag)
	root, err = Root()
	if err != nil || root != fromFlag {
		t.Fatalf("flag root = %q, %v; want %q", root, err, fromFlag)
	}
	path, err := Path("settings.json")
	if err != nil || path != filepath.Join(fromFlag, "settings.json") {
		t.Fatalf("Path = %q, %v", path, err)
	}

	// The cached root does not move when the environment or cwd changes.
	t.Setenv(Env, filepath.Join(home, "elsewhere"))
	t.Chdir(t.TempDir())
	root, err = Root()
	if err != nil || root != fromFlag {
		t.Fatalf("cached root = %q, %v; want %q", root, err, fromFlag)
	}
}

func TestRelativeRootResolvesAgainstWorkingDirectoryWhenSet(t *testing.T) {
	t.Setenv(Env, "")
	Reset()
	t.Cleanup(Reset)
	first := t.TempDir()
	t.Chdir(first)
	t.Setenv(Env, filepath.Join("portable", "data"))
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(first, "portable", "data"); root != want {
		t.Fatalf("relative root = %q, want %q", root, want)
	}

	// A relative flag is resolved when SetFlag is called, not at first use.
	Reset()
	SetFlag("local-root")
	t.Chdir(t.TempDir())
	root, err = Root()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(first, "local-root"); root != want {
		t.Fatalf("flag root moved with cwd: %q, want %q", root, want)
	}
}

func TestInstanceKeyFollowsSymlinksAndCaseOnCaseInsensitiveOS(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if InstanceKey(a) == InstanceKey(b) {
		t.Fatal("different roots share an instance key")
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(a, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if InstanceKey(a) != InstanceKey(link) {
		t.Fatal("a symlink and its target have different instance keys")
	}
	missing := filepath.Join(link, "not-created-yet")
	before := InstanceKey(missing)
	if err := os.MkdirAll(missing, 0o700); err != nil {
		t.Fatal(err)
	}
	if before != InstanceKey(missing) || before != InstanceKey(filepath.Join(a, "not-created-yet")) {
		t.Fatal("a first launch under a symlinked parent gets a different key")
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		if InstanceKey(a) != InstanceKey(strings.ToUpper(a)) {
			t.Fatal("case variants share no key on a case-insensitive OS")
		}
	}
}

func TestFlagValueAcceptsVSCodeSpelling(t *testing.T) {
	for _, arg := range []string{"--user-data-dir=/tmp/data", "-user-data-dir=/tmp/data"} {
		value, ok := FlagValue(arg)
		if !ok || value != "/tmp/data" {
			t.Errorf("FlagValue(%q) = %q, %v", arg, value, ok)
		}
	}
	for _, arg := range []string{"--user-data-dir", "--proxy-guard"} {
		if value, ok := FlagValue(arg); ok {
			t.Errorf("FlagValue(%q) accepted as %q", arg, value)
		}
	}
	if value, ok := FlagValue("--user-data-dir="); !ok || value != "" {
		t.Errorf("empty flag value = %q, %v; want an empty but recognized value", value, ok)
	}
}

func TestLocksAreIndependentPerRoot(t *testing.T) {
	Reset()
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")

	first, err := LockRoot(a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LockRoot(b)
	if err != nil {
		t.Fatalf("another root was blocked: %v", err)
	}
	if _, err := LockRoot(a); !errors.Is(err, ErrLocked) {
		t.Fatalf("same root was not blocked: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("closing twice is not safe: %v", err)
	}
	again, err := LockRoot(a)
	if err != nil {
		t.Fatalf("released root was not available: %v", err)
	}
	if err := errors.Join(again.Close(), second.Close()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a, lockFileName)); err != nil {
		t.Fatalf("no lock file under the root: %v", err)
	}
}
