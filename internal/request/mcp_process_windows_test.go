//go:build windows

package request

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The process-group tests skip on Windows; this keeps their helper compiling.
func mcpProcessAlive(int) bool { return false }

func TestMCPWindowsJobObjectIsAssigned(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "exit", "0")
	mcpPrepareCommand(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tree := mcpAttachProcess(cmd)
	if tree.job == 0 {
		// A restrictive parent job can prevent assignment; the transport then
		// falls back to taskkill before Wait.
		t.Skip("job object assignment unavailable in this runner")
	}
	mcpCloseProcessTree(tree)
	_ = cmd.Wait()
}

func TestMCPWindowsSuspendedStartResumes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cmd.exe", "/c", "echo", "ready")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	mcpPrepareCommand(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tree := mcpAttachProcess(cmd)
	defer mcpCloseProcessTree(tree)
	data, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "ready" {
		t.Fatalf("resumed process output = %q", data)
	}
}
