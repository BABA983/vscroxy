package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/control"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
		text string
	}{
		{"help", []string{"--help"}, 0, "--allow-sensitive"},
		{"required", nil, 2, "Provide --control-file"},
		{"positional", []string{"--control-file", "missing", "unexpected"}, 2, "no positional arguments"},
		{"unknown", []string{"--secret-token=do-not-echo-this"}, 2, "Invalid flags"},
		{"invalid-bool", []string{"--allow-write=do-not-echo-this"}, 2, "Invalid flags"},
		{"bad-discovery", []string{"--control-file", "do-not-echo-this"}, 2, "Could not load"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if got := run(t.Context(), tc.args, &stderr); got != tc.code {
				t.Fatalf("exit %d; want %d: %s", got, tc.code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.text) || strings.Contains(stderr.String(), "do-not-echo-this") {
				t.Fatalf("unexpected diagnostics: %s", stderr.String())
			}
		})
	}
}

// Re-execute the test binary as the actual stdio entry point. No protocol code
// is duplicated: the official SDK drives both ends of a real child process.
func TestStdioProcess(t *testing.T) {
	if os.Getenv("VSCROXY_MCP_TEST_PROCESS") == "1" {
		os.Args = append([]string{"vscroxy-mcp"}, strings.Split(os.Getenv("VSCROXY_MCP_TEST_ARGS"), "\n")...)
		main()
		return
	}
	path := filepath.Join(t.TempDir(), "control.json")
	server, err := control.Start(path, func(_ context.Context, c control.Command) (any, error) {
		return map[string]any{"action": c.Action, "recording": true, "params": c.Params}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	for _, tc := range []struct {
		name      string
		write     bool
		sensitive bool
	}{{"read-only", false, false}, {"write", true, false}, {"sensitive", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"--control-file", path}
			if tc.write {
				args = append(args, "--allow-write")
			}
			if tc.sensitive {
				args = append(args, "--allow-sensitive")
			}
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStdioProcess$")
			cmd.Env = append(os.Environ(), "VSCROXY_MCP_TEST_PROCESS=1", "VSCROXY_MCP_TEST_ARGS="+strings.Join(args, "\n"))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "1"}, nil)
			cs, err := client.Connect(t.Context(), &mcp.CommandTransport{Command: cmd}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
			if err != nil {
				t.Fatalf("initialize failed: %v; stderr: %s", err, stderr.String())
			}
			t.Cleanup(func() { _ = cs.Close() })
			listed, err := cs.ListTools(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			want := 8
			if tc.write {
				want = 13
			}
			if len(listed.Tools) != want {
				t.Errorf("%d tools; want %d", len(listed.Tools), want)
			}
			result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "status"})
			if err != nil || result.IsError {
				t.Fatalf("stdio call failed: %+v, %v", result, err)
			}
			data, err := json.Marshal(result.StructuredContent)
			if err != nil || !strings.Contains(string(data), `"recording":true`) {
				t.Fatalf("unexpected result: %s, %v", data, err)
			}
			result, err = cs.CallTool(t.Context(), &mcp.CallToolParams{
				Name: "requests.get", Arguments: map[string]any{"id": "saved-id", "details": true},
			})
			if err != nil || result.IsError == tc.sensitive {
				t.Fatalf("sensitive startup flag not enforced: %+v, %v", result, err)
			}
			if tc.write {
				result, err = cs.CallTool(t.Context(), &mcp.CallToolParams{
					Name: "capture.recording", Arguments: map[string]any{"enabled": false},
				})
				if err != nil || result.IsError {
					t.Fatalf("mutation failed: %+v, %v", result, err)
				}
				data, err = json.Marshal(result.StructuredContent)
				if err != nil || !strings.Contains(string(data), `"confirm":true`) || !strings.Contains(string(data), `"enabled":false`) {
					t.Fatalf("missing explicit confirmation or false enabled value: %s, %v", data, err)
				}
			}
			if err := cs.Close(); err != nil {
				t.Errorf("close stdio: %v", err)
			}
			if stderr.Len() != 0 {
				t.Errorf("unexpected process diagnostics: %s", stderr.String())
			}
		})
	}
}
