package cli_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/cli"
	"github.com/baba983/vscroxy/internal/control"
)

func TestControlCommandUsesAuthenticatedEndpointAndExitCodes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.json")
	server, err := control.Start(path, func(ctx context.Context, command control.Command) (any, error) {
		switch command.Action {
		case "status":
			return map[string]bool{"proxyRunning": true}, nil
		case "capture.recording":
			var p struct {
				Confirm bool `json:"confirm"`
				Enabled bool `json:"enabled"`
			}
			if json.Unmarshal(command.Params, &p) != nil || !p.Confirm {
				return map[string]string{"error": "confirmation required"}, nil
			}
			return map[string]bool{"recording": p.Enabled}, nil
		default:
			return map[string]string{"error": "unknown action"}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"--file", path, "status"}, 0, `{"proxyRunning":true}`},
		{[]string{"--file", path, "unknown"}, 1, `"error"`},
		{[]string{"--file", path, "--params", `{"enabled":false}`, "capture.recording"}, 1, `"error"`},
		{[]string{"--file", path, "--params", `{"enabled":false}`, "--confirm", "capture.recording"}, 0, `{"recording":false}`},
		{[]string{"--file", path, "--params", `[]`, "status"}, 2, ""},
		{[]string{"--file", path, "--params", `{"x":1,"x":2}`, "status"}, 2, ""},
		{[]string{"--file", path, "--params", `{broken`, "status"}, 2, ""},
		{[]string{"--file", path + ".missing", "status"}, 2, ""},
		{[]string{"--help"}, 0, ""},
	} {
		var out, errOut bytes.Buffer
		code := cli.Run(t.Context(), append([]string{"control"}, tc.args...), &out, &errOut)
		if code != tc.code || !strings.Contains(out.String(), tc.want) {
			t.Fatalf("%v: exit=%d out=%s err=%s", tc.args, code, &out, &errOut)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out, errOut bytes.Buffer
	if code := cli.Run(ctx, []string{"control", "--file", path, "status"}, &out, &errOut); code != 130 {
		t.Fatalf("cancellation exit=%d", code)
	}
}
