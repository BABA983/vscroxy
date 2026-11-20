package cli_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/baba983/vscroxy/internal/cli"
	"github.com/baba983/vscroxy/internal/control"
)

func desktopFixture(t *testing.T, reject func(control.Command) bool) (string, func() []control.Command) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "desktop.json")
	var mu sync.Mutex
	var calls []control.Command
	server, err := control.Start(path, func(_ context.Context, command control.Command) (any, error) {
		mu.Lock()
		calls = append(calls, command)
		mu.Unlock()
		if reject != nil && reject(command) {
			return map[string]string{"error": "setting is not available to automation"}, nil
		}
		return map[string]any{"action": command.Action, "params": command.Params}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if _, err := control.Load(path); err != nil {
		t.Fatal(err)
	}
	return path, func() []control.Command {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cli.Run(t.Context(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestDesktopSubcommandsUseControlAndRequireConfirmation(t *testing.T) {
	path, calls := desktopFixture(t, nil)
	for _, tc := range []struct {
		name   string
		args   []string
		action string
		params string
	}{
		{"settings list", []string{"settings", "list"}, "settings.list", `"settings.list"`},
		{"settings get", []string{"settings", "get", "--id", "capture.flowLimit"}, "settings.get", `"id":"capture.flowLimit"`},
		{"settings set", []string{"settings", "set", "--id", "general.checkForUpdates", "--value", "false", "--confirm"}, "settings.set", `"confirm":true`},
		{"sessions list", []string{"sessions", "list"}, "sessions.list", `"sessions.list"`},
		{"sessions save", []string{"sessions", "save", "--confirm"}, "sessions.save", `"confirm":true`},
		{"sessions open", []string{"sessions", "open", "key.json", "--confirm"}, "sessions.open", `"key":"key.json"`},
		{"sessions close", []string{"sessions", "close", "key.json", "--confirm"}, "sessions.close", `"key":"key.json"`},
		{"certificates status", []string{"certificates", "status"}, "certificates.get", `"certificates.get"`},
		{"mocks list", []string{"mocks", "list"}, "mock.get", `"mock.get"`},
		{"mocks start", []string{"mocks", "start", "--collection", "Shop", "--rate-limit", "5", "--error-rate", "0.25", "--error-status", "429", "--confirm"}, "mock.start", `"rateLimit":5`},
		{"mocks stop", []string{"mocks", "stop", "--collection", "Shop", "--confirm"}, "mock.stop", `"collection":"Shop"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(calls())
			args := append([]string{tc.args[0], "--file", path}, tc.args[1:]...)
			code, out, errOut := runCLI(t, args...)
			if code != 0 || errOut != "" || !strings.Contains(out, tc.params) {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, errOut)
			}
			got := calls()
			if len(got) != before+1 || got[len(got)-1].Action != tc.action {
				t.Fatalf("control calls = %v", got)
			}
		})
	}

	for _, args := range [][]string{
		{"settings", "--file", path, "set", "--id", "general.checkForUpdates", "--value", "false"},
		{"sessions", "--file", path, "save"},
		{"sessions", "--file", path, "open", "key.json"},
		{"sessions", "--file", path, "close", "key.json"},
		{"mocks", "--file", path, "start", "--collection", "Shop"},
		{"mocks", "--file", path, "stop", "--collection", "Shop"},
	} {
		before := len(calls())
		code, out, errOut := runCLI(t, args...)
		if code != 2 || out != "" || !strings.Contains(errOut, "--confirm") || len(calls()) != before {
			t.Fatalf("unconfirmed %v: exit=%d out=%s err=%s", args, code, out, errOut)
		}
	}
}

func TestDesktopSubcommandsRejectSecretsAndInvalidInput(t *testing.T) {
	secret := "seed-secret-cli-4e77"
	path, calls := desktopFixture(t, func(command control.Command) bool {
		if command.Action != "settings.get" && command.Action != "settings.set" {
			return false
		}
		var p struct {
			ID    string         `json:"id"`
			Value jsontext.Value `json:"value"`
		}
		if json.Unmarshal(command.Params, &p) != nil {
			return false
		}
		return p.ID == "proxy.socks.password" || string(p.Value) == `"`+secret+`"`
	})
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"settings", "--file", path, "get", "--id", "proxy.socks.password"}, 1, "not available"},
		{[]string{"settings", "--file", path, "set", "--id", "proxy.socks.password", "--value", `"` + secret + `"`, "--confirm"}, 2, "--value"},
		{[]string{"settings", "--file", path, "set", "--id", "proxy.socks.password", "--value", "true", "--confirm"}, 1, "not available"},
		{[]string{"settings", "--file", path, "set", "--id", "capture.flowLimit", "--value", `"1000"`, "--confirm"}, 2, "--value"},
		{[]string{"sessions", "--file", path, "open", "--confirm"}, 2, "session key"},
		{[]string{"mocks", "--file", path, "start", "--collection", "Shop", "--rate-limit", "100001", "--confirm"}, 2, "rate limit"},
		{[]string{"certificates", "--file", path}, 2, "verb"},
	} {
		before := len(calls())
		code, out, errOut := runCLI(t, tc.args...)
		if code != tc.code || strings.Contains(out, secret) || strings.Contains(errOut, secret) {
			t.Fatalf("%v: exit=%d out=%s err=%s", tc.args, code, out, errOut)
		}
		if !strings.Contains(out+errOut, tc.want) {
			t.Fatalf("%v: missing %q in %s %s", tc.args, tc.want, out, errOut)
		}
		if tc.code == 2 && tc.want == "--value" && len(calls()) != before {
			t.Fatal("invalid value reached the desktop")
		}
	}
	// The two rejected setting calls reached the desktop; neither their
	// values nor the refusal appeared in either output stream.
}

func TestDesktopSubcommandCancellationAndHelp(t *testing.T) {
	path, calls := desktopFixture(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out, errOut bytes.Buffer
	if code := cli.Run(ctx, []string{"certificates", "--file", path, "status"}, &out, &errOut); code != 130 {
		t.Fatalf("cancellation exit=%d err=%s", code, &errOut)
	}
	if len(calls()) != 0 {
		t.Fatal("canceled command reached the desktop")
	}
	for _, args := range [][]string{{"--help"}, {"settings", "--help"}, {"sessions", "--help"}, {"certificates", "--help"}, {"mocks", "--help"}} {
		var helpOut, helpErr bytes.Buffer
		if code := cli.Run(t.Context(), args, &helpOut, &helpErr); code != 0 || helpOut.Len() == 0 {
			t.Fatalf("help %v: exit=%d out=%s err=%s", args, code, &helpOut, &helpErr)
		}
	}
}
