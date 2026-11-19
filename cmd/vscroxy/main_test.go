package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/baba983/vscroxy/internal/datadir"
)

func TestParseLaunchArgsSetsDataRootAndGuardArguments(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(datadir.Env, "")
	t.Cleanup(func() { datadir.SetFlag("") })
	guard := []string{"journal.json", "127.0.0.1:7890", `["localhost"]`}

	args := append([]string{"--user-data-dir", root, "--proxy-guard"}, guard...)
	got, showVersion, err := parseLaunchArgs(args)
	if err != nil || showVersion {
		t.Fatalf("parse = %v, %v, %v", got, showVersion, err)
	}
	if !reflect.DeepEqual(got, guard) {
		t.Fatalf("guard args = %q, want %q", got, guard)
	}
	if found, err := datadir.Root(); err != nil || found != root {
		t.Fatalf("guard root = %q, %v; want %q", found, err, root)
	}

	got, showVersion, err = parseLaunchArgs([]string{"--version"})
	if err != nil || !showVersion || got != nil {
		t.Fatalf("version parse = %v, %v, %v", got, showVersion, err)
	}
	// The flag stays set for the launch: env and home cannot override it.
	t.Setenv(datadir.Env, filepath.Join(home, "elsewhere"))
	if found, err := datadir.Root(); err != nil || found != root {
		t.Fatalf("flag lost precedence: %q, %v", found, err)
	}
	if _, _, err := parseLaunchArgs([]string{"--user-data-dir"}); err == nil {
		t.Fatal("a missing --user-data-dir path was accepted")
	}
}
