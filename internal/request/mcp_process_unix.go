//go:build unix

package request

import (
	"os/exec"
	"syscall"
)

// mcpPrepareCommand starts the server in its own process group so a shell,
// npx or uvx wrapper and the server's children can be terminated together.
func mcpPrepareCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// mcpProcessGroupID captures the group id after Start. It is kept because
// Getpgid fails once the leader has been reaped, while its children may still
// be alive in the group.
func mcpProcessGroupID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		return pgid
	}
	return cmd.Process.Pid
}

// mcpKillProcessGroup signals the whole process group. A non-force signal
// gives the server a chance to shut down; force uses SIGKILL.
func mcpKillProcessGroup(pgid int, force bool) {
	if pgid <= 0 {
		return
	}
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	_ = syscall.Kill(-pgid, signal)
}
