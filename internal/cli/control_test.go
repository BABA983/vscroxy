package cli_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

func TestControlHARExportOutput(t *testing.T) {
	for _, details := range []bool{false, true} {
		for _, destination := range []string{"stdout", "new-file", "replace-file"} {
			t.Run(fmt.Sprintf("%s/details=%v", destination, details), func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "control.json")
				want := `{"log":{"version":"1.2","creator":{"name":"test","version":"1"},"entries":[]}}`
				if details {
					want = `{"log":{"version":"1.2","creator":{"name":"test","version":"1"},"entries":[],"comment":"raw-secret"}}`
				}
				params := `{"ids":[7,2]}`
				if details {
					params = `{"ids":[7,2],"details":true}`
				}
				server, err := control.Start(path, func(_ context.Context, c control.Command) (any, error) {
					var p struct {
						IDs     []int `json:"ids"`
						Details bool  `json:"details"`
					}
					if c.Action != "flows.export-har" || json.Unmarshal(c.Params, &p, json.RejectUnknownMembers(true)) != nil || !slices.Equal(p.IDs, []int{7, 2}) || p.Details != details {
						return nil, errors.New("unexpected export action or parameters")
					}
					return jsontext.Value(want), nil
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = server.Close() })
				args := []string{"control", "--file", path, "--params", params}
				output := filepath.Join(root, "exports", "capture.har")
				if destination == "replace-file" {
					output = filepath.Join(root, "capture.har")
					if err := os.WriteFile(output, []byte("old"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if destination != "stdout" {
					args = append(args, "--output", output)
				}
				var out, errOut bytes.Buffer
				if code := cli.Run(t.Context(), append(args, "flows.export-har"), &out, &errOut); code != 0 || errOut.Len() != 0 {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
				}
				got := out.Bytes()
				if destination != "stdout" {
					if out.Len() != 0 {
						t.Fatalf("file export printed traffic: %s", &out)
					}
					got, err = os.ReadFile(output)
					if err != nil {
						t.Fatal(err)
					}
					info, err := os.Stat(output)
					if err != nil {
						t.Fatal(err)
					}
					if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
						t.Fatalf("output permissions=%o", info.Mode().Perm())
					}
					if destination == "new-file" {
						info, err = os.Stat(filepath.Dir(output))
						if err != nil {
							t.Fatal(err)
						}
						if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
							t.Fatalf("output directory permissions=%o", info.Mode().Perm())
						}
					}
				}
				if !jsontext.Value(got).IsValid() || strings.TrimSpace(string(got)) != want {
					t.Fatalf("export is not the direct HAR document: %s", got)
				}
			})
		}
	}
}

func TestControlOutputFailuresPreserveDestination(t *testing.T) {
	for _, failure := range []string{"app", "dispatch", "invalid-response", "disconnected", "cancel", "invalid-params"} {
		for _, exists := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%v", failure, exists), func(t *testing.T) {
				root := t.TempDir()
				path, output := filepath.Join(root, "control.json"), filepath.Join(root, "capture.har")
				server, err := control.Start(path, func(context.Context, control.Command) (any, error) {
					if failure == "dispatch" {
						return nil, errors.New("private backend exception")
					}
					if failure == "invalid-response" {
						return jsontext.Value(`{broken`), nil
					}
					return map[string]string{"error": "flow not found"}, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = server.Close() })
				if failure == "disconnected" {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if err := server.Close(); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if exists {
					if err := os.WriteFile(output, []byte("preserve-me"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				params, wantCode := `{"ids":[1]}`, 2
				switch failure {
				case "app":
					wantCode = 1
				case "cancel":
					cancel()
					wantCode = 130
				case "invalid-params":
					params = `{broken`
				}
				var out, errOut bytes.Buffer
				code := cli.Run(ctx, []string{"control", "--file", path, "--output", output, "--params", params, "flows.export-har"}, &out, &errOut)
				if code != wantCode || out.Len() != 0 || strings.Contains(errOut.String(), "private backend exception") {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
				}
				got, err := os.ReadFile(output)
				if exists && (err != nil || string(got) != "preserve-me") || !exists && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed export changed destination: %q, %v", got, err)
				}
			})
		}
	}
}

func TestControlOutputWriteError(t *testing.T) {
	root := t.TempDir()
	path, output := filepath.Join(root, "control.json"), filepath.Join(root, "directory")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(output, "keep")
	if err := os.WriteFile(sentinel, []byte("preserve-me"), 0o600); err != nil {
		t.Fatal(err)
	}
	server, err := control.Start(path, func(context.Context, control.Command) (any, error) {
		return map[string]bool{"ok": true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	var out, errOut bytes.Buffer
	code := cli.Run(t.Context(), []string{"control", "--file", path, "--output", output, "status"}, &out, &errOut)
	if code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "Could not write output") {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "preserve-me" {
		t.Fatalf("write failure changed destination: %q, %v", got, err)
	}
	if files, err := filepath.Glob(filepath.Join(root, ".vscroxy-*")); err != nil || len(files) != 0 {
		t.Fatalf("write failure left temporary files: %v, %v", files, err)
	}
}

func TestControlOutputCannotReplaceDiscovery(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "control.json")
	server, err := control.Start(path, func(context.Context, control.Command) (any, error) {
		t.Error("discovery overlap must be rejected before dispatch")
		return map[string]bool{"ok": true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	outputs := []string{path, filepath.Join(root, "unused", "..", "control.json")}
	for _, link := range []string{"symlink", "hardlink", "directory-symlink"} {
		alias := filepath.Join(t.TempDir(), "alias")
		switch link {
		case "symlink":
			err = os.Symlink(path, alias)
		case "hardlink":
			err = os.Link(path, alias)
		case "directory-symlink":
			err = os.Symlink(root, alias)
			alias = filepath.Join(alias, "control.json")
		}
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, alias)
	}
	for _, output := range outputs {
		var out, errOut bytes.Buffer
		code := cli.Run(t.Context(), []string{"control", "--file", path, "--output", output, "flows.export-har"}, &out, &errOut)
		if code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "discovery file") {
			t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
		}
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, original) {
			t.Fatal("output overwrote discovery")
		}
		if got, err := os.ReadFile(output); err != nil || !bytes.Equal(got, original) {
			t.Fatal("output overwrote discovery alias")
		}
	}
}
