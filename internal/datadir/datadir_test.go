package datadir

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRootPrefersFlagThenEnvThenHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv(Env, "")
	SetFlag("")

	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".vscroxy"); root != want {
		t.Fatalf("default root = %q, want %q", root, want)
	}

	fromEnv := filepath.Join(t.TempDir(), "from-env")
	t.Setenv(Env, fromEnv)
	root, err = Root()
	if err != nil || root != fromEnv {
		t.Fatalf("env root = %q, %v; want %q", root, err, fromEnv)
	}

	fromFlag := filepath.Join(t.TempDir(), "from-flag")
	SetFlag(fromFlag)
	t.Cleanup(func() { SetFlag("") })
	root, err = Root()
	if err != nil || root != fromFlag {
		t.Fatalf("flag root = %q, %v; want %q", root, err, fromFlag)
	}
	path, err := Path("settings.json")
	if err != nil || path != filepath.Join(fromFlag, "settings.json") {
		t.Fatalf("Path = %q, %v", path, err)
	}
}

func TestRelativeRootResolvesAgainstWorkingDirectory(t *testing.T) {
	t.Setenv(Env, "")
	SetFlag("")
	t.Chdir(t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(Env, filepath.Join("portable", "data"))
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wd, "portable", "data"); root != want {
		t.Fatalf("relative root = %q, want %q", root, want)
	}
}

func TestLocksAreIndependentPerRoot(t *testing.T) {
	t.Setenv(Env, "")
	SetFlag("")
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
