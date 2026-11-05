//go:build windows

package clientsetup

import (
	"slices"
	"strings"
	"testing"
)

func TestWindowsTerminalUsesCmdEnvAndANewConsole(t *testing.T) {
	t.Setenv("ComSpec", "cmd.exe")
	command, err := OpenTerminal(testProxy())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(command.Args, []string{"/k"}) || !command.NewConsole {
		t.Fatalf("terminal command: %+v", command)
	}
	if got := envValue(command.Env, "HTTP_PROXY"); got != "http://127.0.0.1:7890" {
		t.Fatalf("environment proxy %q: %q", got, command.Env)
	}
	if got := envValue(command.Env, "NODE_EXTRA_CA_CERTS"); got != "/Users/Jane Doe/.mitmproxy/mitmproxy-ca-cert.pem" {
		t.Fatalf("environment CA %q: %q", got, command.Env)
	}
	for _, entry := range command.Env {
		if strings.Contains(strings.ToLower(entry), "all_proxy") {
			t.Fatalf("environment routes every protocol to the HTTP proxy: %s", entry)
		}
	}
}

func envValue(env []string, name string) string {
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}
