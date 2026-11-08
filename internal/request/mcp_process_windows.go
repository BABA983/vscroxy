//go:build windows

package request

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

// mcpPrepareCommand leaves process creation to Windows. The process tree is
// terminated with taskkill /T, which covers the server and its children; a
// full Job Object would be stronger but is not required for this prototype.
func mcpPrepareCommand(cmd *exec.Cmd) {}

// mcpProcessGroupID is the process id; taskkill /T walks the tree from it.
func mcpProcessGroupID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}

func mcpKillProcessGroup(pid int, force bool) {
	if pid <= 0 {
		return
	}
	args := []string{"/T", "/PID", strconv.Itoa(pid)}
	if force {
		args = append(args, "/F")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "taskkill", args...).Run()
}
