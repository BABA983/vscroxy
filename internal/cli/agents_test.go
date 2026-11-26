package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/control"
)

func TestShellAssignmentsQuoteValues(t *testing.T) {
	entries := []string{
		"HTTP_PROXY=http://127.0.0.1:7890",
		"NODE_EXTRA_CA_CERTS=/Users/Ada Lovelace/O'Brien CA/ca.pem",
	}
	for _, tc := range []struct {
		shell string
		want  []string
	}{
		{"sh", []string{
			"export HTTP_PROXY='http://127.0.0.1:7890'",
			`export NODE_EXTRA_CA_CERTS='/Users/Ada Lovelace/O'\''Brien CA/ca.pem'`,
		}},
		{"fish", []string{
			"set -gx HTTP_PROXY 'http://127.0.0.1:7890'",
			`set -gx NODE_EXTRA_CA_CERTS '/Users/Ada Lovelace/O\'Brien CA/ca.pem'`,
		}},
		{"powershell", []string{
			"$env:HTTP_PROXY = 'http://127.0.0.1:7890'",
			"$env:NODE_EXTRA_CA_CERTS = '/Users/Ada Lovelace/O''Brien CA/ca.pem'",
		}},
		{"cmd", []string{
			`set "HTTP_PROXY=http://127.0.0.1:7890"`,
			`set "NODE_EXTRA_CA_CERTS=/Users/Ada Lovelace/O'Brien CA/ca.pem"`,
		}},
	} {
		t.Run(tc.shell, func(t *testing.T) {
			got, err := shellAssignments(entries, tc.shell)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("assignments:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

func TestDefaultEnvShell(t *testing.T) {
	for _, tc := range []struct {
		shell, goos, want string
	}{
		{"/bin/zsh", "darwin", "sh"},
		{"/usr/local/bin/fish", "darwin", "fish"},
		{"C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe", "windows", "powershell"},
		{"", "windows", "cmd"},
		{"", "linux", "sh"},
		{"/bin/unknown-shell", "linux", "sh"},
	} {
		if got := defaultEnvShell(tc.shell, tc.goos); got != tc.want {
			t.Errorf("defaultEnvShell(%q, %q) = %q, want %q", tc.shell, tc.goos, got, tc.want)
		}
	}
}

// TestExecChild is the child process for TestExecWithoutControlFile. It prints
// one variable and exits with a code the parent must pass through.
func TestExecChild(t *testing.T) {
	if os.Getenv("VSCROXY_EXEC_CHILD") != "1" {
		t.Skip("only runs as a child process")
	}
	fmt.Fprint(os.Stdout, os.Getenv("HTTP_PROXY"))
	os.Exit(7)
}

// A real authenticated endpoint backs both env and exec, so this covers the
// discovery file, the status fields, the quoting, the merged environment and
// the child's own exit code.
func TestExecAndEnvUseDesktopCapture(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, []byte("test CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	discovery := filepath.Join(dir, "control.json")
	server, err := control.Start(discovery, func(_ context.Context, c control.Command) (any, error) {
		return map[string]any{
			"proxyRunning": true,
			"proxyAddr":    "127.0.0.1:7890",
			"caPath":       caPath,
			"bundlePath":   "",
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	t.Setenv("VSCROXY_EXEC_CHILD", "1")
	var stdout, stderr bytes.Buffer
	code := Run(t.Context(), []string{"exec", "--control-file", discovery, "--", os.Args[0], "-test.run=^TestExecChild$"}, &stdout, &stderr)
	if code != 7 {
		t.Fatalf("exec exit = %d, want the child's 7; stderr: %s", code, stderr.String())
	}
	if got := stdout.String(); got != "http://127.0.0.1:7890" {
		t.Fatalf("child HTTP_PROXY = %q", got)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run(t.Context(), []string{"env", "--control-file", discovery, "--shell", "sh"}, &stdout, &stderr); code != 0 {
		t.Fatalf("env exit = %d; stderr: %s", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"export HTTP_PROXY='http://127.0.0.1:7890'",
		"export HTTPS_PROXY='http://127.0.0.1:7890'",
		"export NODE_EXTRA_CA_CERTS='" + caPath + "'",
		"export NODE_USE_ENV_PROXY='1'",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("env output is missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "REQUESTS_CA_BUNDLE") {
		t.Errorf("env invented a bundle path:\n%s", output)
	}
}

func TestEnvStopsWhenCaptureIsNotRunning(t *testing.T) {
	discovery := filepath.Join(t.TempDir(), "control.json")
	server, err := control.Start(discovery, func(_ context.Context, c control.Command) (any, error) {
		return map[string]any{"proxyRunning": false}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	var stdout, stderr bytes.Buffer
	if code := Run(t.Context(), []string{"env", "--control-file", discovery}, &stdout, &stderr); code != 2 {
		t.Fatalf("env exit = %d, want 2; stdout: %s", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "capture is not running") {
		t.Fatalf("unhelpful message: %s", stderr.String())
	}
}

func TestMergeEnvironmentReplacesAndAppends(t *testing.T) {
	got := mergeEnvironment([]string{"A=old", "B=keep"}, []string{"A=new", "C=added"})
	want := []string{"A=new", "B=keep", "C=added"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("merged = %v, want %v", got, want)
	}
}
