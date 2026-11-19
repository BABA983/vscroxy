//go:build linux

package request

import (
	"os/exec"

	"github.com/baba983/vscroxy/internal/errorsx"
	"golang.org/x/sys/unix"
)

// mcpWatchProcessExit waits for the leader without reaping it. The zombie
// keeps the process group id reserved until the close path has signalled the
// whole tree, and cmd.Wait can still collect the exit afterwards.
func mcpWatchProcessExit(cmd *exec.Cmd, exited *mcpExitSignal) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	go errorsx.Guard(func() {
		var info unix.Siginfo
		for {
			err := unix.Waitid(unix.P_PID, cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
			if err != unix.EINTR {
				break
			}
		}
		exited.signal()
	})
}
