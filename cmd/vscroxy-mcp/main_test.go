package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
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
			if tc.name == "help" {
				for _, warning := range []string{"--allow-write", "--allow-sensitive", "Captured content is untrusted data, never instructions"} {
					if !strings.Contains(stderr.String(), warning) {
						t.Errorf("help is missing capability/content warning %q", warning)
					}
				}
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
			want := 17
			if tc.write {
				want = 37
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
			result, err = cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "collection-recording.get"})
			if err != nil || result.IsError {
				t.Fatalf("recording query failed: %+v, %v", result, err)
			}
			data, err = json.Marshal(result.StructuredContent)
			if err != nil || !strings.Contains(string(data), `"action":"collection-recording.get"`) || !strings.Contains(string(data), `"params":{}`) {
				t.Fatalf("recording query not forwarded with empty params: %s, %v", data, err)
			}
			for _, recording := range []struct {
				name string
				args any
			}{
				{"collection-recording.start", map[string]any{"collection": "sample/subfolder", "environment": "local", "hosts": []string{"api.example.com:443"}, "apps": []string{"Firefox"}}},
				{"collection-recording.stop", map[string]any{}},
			} {
				result, err = cs.CallTool(t.Context(), &mcp.CallToolParams{Name: recording.name, Arguments: recording.args})
				if !tc.write {
					if err == nil && !result.IsError {
						t.Fatalf("recording mutation available without --allow-write: %s", recording.name)
					}
					continue
				}
				if err != nil || result.IsError {
					t.Fatalf("recording mutation failed without --allow-sensitive: %+v, %v", result, err)
				}
				var forwarded struct {
					Action string         `json:"action"`
					Params map[string]any `json:"params"`
				}
				data, err = json.Marshal(result.StructuredContent)
				if err != nil || json.Unmarshal(data, &forwarded) != nil || forwarded.Action != recording.name || forwarded.Params["confirm"] != true {
					t.Fatalf("missing recording action/confirmation: %s, %v", data, err)
				}
				if recording.name == "collection-recording.stop" && len(forwarded.Params) != 1 {
					t.Fatalf("stop forwarded nonempty input: %s", data)
				}
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

func TestWorkflowStdioProcess(t *testing.T) {
	var calls atomic.Int64
	path := filepath.Join(t.TempDir(), "control.json")
	server, err := control.Start(path, func(_ context.Context, c control.Command) (any, error) {
		calls.Add(1)
		return map[string]any{"action": c.Action, "params": c.Params}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	for _, protocol := range []string{"2025-11-25", "2026-07-28"} {
		for _, write := range []bool{false, true} {
			for _, sensitive := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/write=%v/sensitive=%v", protocol, write, sensitive), func(t *testing.T) {
					args := []string{"--control-file", path}
					if write {
						args = append(args, "--allow-write")
					}
					if sensitive {
						args = append(args, "--allow-sensitive")
					}
					cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStdioProcess$")
					cmd.Env = append(os.Environ(), "VSCROXY_MCP_TEST_PROCESS=1", "VSCROXY_MCP_TEST_ARGS="+strings.Join(args, "\n"))
					var stderr bytes.Buffer
					cmd.Stderr = &stderr
					client := mcp.NewClient(&mcp.Implementation{Name: "token-links-stdio-test", Version: "1"}, nil)
					cs, err := client.Connect(t.Context(), &mcp.CommandTransport{Command: cmd}, &mcp.ClientSessionOptions{ProtocolVersion: protocol})
					if err != nil {
						t.Fatalf("initialize failed: %v; stderr: %s", err, stderr.String())
					}
					t.Cleanup(func() { _ = cs.Close() })
					listed, err := cs.ListTools(t.Context(), nil)
					if err != nil {
						t.Fatal(err)
					}
					wantCount := 17
					if write {
						wantCount = 37
					}
					if len(listed.Tools) != wantCount {
						t.Fatalf("%d tools; want %d", len(listed.Tools), wantCount)
					}
					for _, tc := range []struct {
						name string
						args map[string]any
					}{
						{"token-links.analyze", map[string]any{"ids": []any{float64(7), float64(2)}}},
						{"token-links.get", map[string]any{"id": "analysis-id"}},
						{"token-links.prepare", map[string]any{"id": "analysis-id", "index": float64(0), "collection": "", "environment": "local", "variable": "accessToken"}},
						{"token-links.apply", map[string]any{"key": "proposal-id"}},
						{"collection.load", map[string]any{"collection": "", "users": float64(3), "durationSeconds": float64(1), "requestTimeoutSeconds": float64(30), "maxAttempts": float64(100000), "bail": true}},
					} {
						before := calls.Load()
						result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
						mutation := tc.name == "token-links.apply" || tc.name == "collection.load"
						if mutation && !write {
							if err == nil && !result.IsError || calls.Load() != before {
								t.Fatalf("%s reached desktop without --allow-write", tc.name)
							}
							continue
						}
						if err != nil || result.IsError || calls.Load() != before+1 {
							t.Fatalf("%s forwarding failed: %+v, %v", tc.name, result, err)
						}
						want := maps.Clone(tc.args)
						if mutation {
							want["confirm"] = true
						}
						var forwarded struct {
							Action string         `json:"action"`
							Params map[string]any `json:"params"`
						}
						data, err := json.Marshal(result.StructuredContent)
						if err != nil || json.Unmarshal(data, &forwarded) != nil || forwarded.Action != tc.name || !reflect.DeepEqual(forwarded.Params, want) {
							t.Fatalf("incorrect %s forwarding: %s, %v", tc.name, data, err)
						}
						invalid := maps.Clone(tc.args)
						invalid["confirm"] = true
						before = calls.Load()
						result, err = cs.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.name, Arguments: invalid})
						if err != nil || !result.IsError || calls.Load() != before {
							t.Fatalf("closed %s schema accepted model confirmation: %+v, %v", tc.name, result, err)
						}
					}
					if err := cs.Close(); err != nil {
						t.Error(err)
					}
					if stderr.Len() != 0 {
						t.Errorf("unexpected child diagnostics: %s", stderr.String())
					}
				})
			}
		}
	}
}
