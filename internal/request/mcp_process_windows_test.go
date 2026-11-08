//go:build windows

package request

import (
	"os/exec"
	"testing"
)

// The process-group tests skip on Windows; this keeps their helper compiling.
func mcpProcessAlive(int) bool { return false }

func TestMCPWindowsJobObjectIsAssigned(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "exit", "0")
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
