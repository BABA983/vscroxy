package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/baba983/vscroxy/internal/datadir"
)

func TestParseLaunchArgsSetsDataRootAndGuardArguments(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	datadir.Reset()
	t.Cleanup(datadir.Reset)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(datadir.Env, "")
	guard := []string{"journal.json", "127.0.0.1:7890", `["localhost"]`}

	args := append([]string{"--user-data-dir", root, "--proxy-guard"}, guard...)
	got, showVersion, level, err := parseLaunchArgs(args)
	if err != nil || showVersion || level != "" {
		t.Fatalf("parse = %v, %v, %q, %v", got, showVersion, level, err)
	}
	if !reflect.DeepEqual(got, guard) {
		t.Fatalf("guard args = %q, want %q", got, guard)
	}
	if found, err := datadir.Root(); err != nil || found != root {
		t.Fatalf("guard root = %q, %v; want %q", found, err, root)
	}

	got, showVersion, level, err = parseLaunchArgs([]string{"--version"})
	if err != nil || !showVersion || got != nil || level != "" {
		t.Fatalf("version parse = %v, %v, %q, %v", got, showVersion, level, err)
	}
	// The flag stays set for the launch: env and home cannot override it.
	t.Setenv(datadir.Env, filepath.Join(home, "elsewhere"))
	if found, err := datadir.Root(); err != nil || found != root {
		t.Fatalf("flag lost precedence: %q, %v", found, err)
	}
	if _, _, _, err := parseLaunchArgs([]string{"--user-data-dir"}); err == nil {
		t.Fatal("a missing --user-data-dir path was accepted")
	}
}

func TestParseLaunchArgsAcceptsVSCodeEqualsForm(t *testing.T) {
	datadir.Reset()
	t.Cleanup(datadir.Reset)
	wd := t.TempDir()
	t.Chdir(wd)
	guard := []string{"journal.json", "target"}
	got, _, level, err := parseLaunchArgs(append([]string{"--user-data-dir=relative-root", "--log-level=trace", "--proxy-guard"}, guard...))
	if err != nil || !reflect.DeepEqual(got, guard) || level != "trace" {
		t.Fatalf("parse = %v, %q, %v", got, level, err)
	}
	t.Chdir(t.TempDir())
	if found, err := datadir.Root(); err != nil || found != filepath.Join(wd, "relative-root") {
		t.Fatalf("equals root moved with cwd: %q, %v", found, err)
	}

	datadir.Reset()
	if _, _, _, err := parseLaunchArgs([]string{"--user-data-dir="}); err == nil {
		t.Fatal("an empty --user-data-dir= was accepted")
	}
}

func TestParseLaunchArgsReadsLogLevelFlag(t *testing.T) {
	if _, _, level, err := parseLaunchArgs([]string{"--log-level", "debug"}); err != nil || level != "debug" {
		t.Fatalf("separate flag = %q, %v", level, err)
	}
	if _, _, level, err := parseLaunchArgs([]string{"-log-level=Warning"}); err != nil || level != "Warning" {
		t.Fatalf("equals flag = %q, %v", level, err)
	}
	for _, args := range [][]string{{"--log-level"}, {"--log-level="}, {"--log-level=debug", "--log-level="}} {
		if _, _, _, err := parseLaunchArgs(args); err == nil {
			t.Errorf("missing level accepted from %q", args)
		}
	}
}
