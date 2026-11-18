package system

import (
	"os"
	"reflect"
	"testing"
)

func TestParseShellEnvOutputReadsBetweenMarkersOnly(t *testing.T) {
	output := "last login: today\n" +
		"MARKER\n" +
		"PATH=/opt/brew/bin:/usr/bin\n" +
		"HOME=/Users/me\n" +
		"SECRET=not-for-children\n" +
		"a line without an equals sign\n" +
		"LANG=en_US.UTF-8\n" +
		"MARKER\n" +
		"PATH=/the/wrong/path\n"
	got := parseShellEnvOutput(output, "MARKER")
	want := map[string]string{"PATH": "/opt/brew/bin:/usr/bin", "HOME": "/Users/me", "LANG": "en_US.UTF-8"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
}

func TestMergeShellEnvChangesOnlyKnownVariables(t *testing.T) {
	current := []string{"PATH=/usr/bin", "HOME=/old", "SECRET=inherited", "VSCROXY_CONTROL_FILE=/tmp/control"}
	got := mergeShellEnv(current, map[string]string{
		"PATH":          "/opt/brew/bin:/usr/bin",
		"HOME":          "/new",
		"SECRET":        "from-shell",
		"SSH_AUTH_SOCK": "/tmp/ssh.sock",
	})
	want := []string{"PATH=/opt/brew/bin:/usr/bin", "HOME=/new", "SECRET=inherited", "VSCROXY_CONTROL_FILE=/tmp/control", "SSH_AUTH_SOCK=/tmp/ssh.sock"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged %v, want %v", got, want)
	}
}

func TestShellEnvKeepsCurrentEnvironmentForTests(t *testing.T) {
	got := ShellEnv(t.Context())
	if !reflect.DeepEqual(got, os.Environ()) {
		t.Fatal("a test process must not resolve a login shell")
	}
}
