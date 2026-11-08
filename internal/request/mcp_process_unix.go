//go:build unix

package request

import (
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

type mcpProcessTree struct {
	pgid int
}

// mcpPrepareCommand starts the server in its own process group so a shell,
// npx or uvx wrapper and the server's children can be terminated together.
func mcpPrepareCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func mcpAttachProcess(cmd *exec.Cmd) mcpProcessTree {
	if cmd == nil || cmd.Process == nil {
		return mcpProcessTree{}
	}
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		return mcpProcessTree{pgid: pgid}
	}
	return mcpProcessTree{pgid: cmd.Process.Pid}
}

func mcpTerminateProcessTree(tree mcpProcessTree, force bool) {
	if tree.pgid <= 0 {
		return
	}
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	_ = syscall.Kill(-tree.pgid, signal)
}

func mcpCloseProcessTree(mcpProcessTree) {}

// mcpCloseProcess never signals the saved group after cmd.Wait: the leader
// may have been reaped and its pid reused. The grace period deliberately does
// not reap the leader, so the process group id stays reserved while SIGTERM
// reaches children that outlived it.
func mcpCloseProcess(cmd *exec.Cmd, tree mcpProcessTree, stdin io.Closer, stdout io.Closer, exited <-chan struct{}, grace bool) error {
	if stdin != nil {
		_ = stdin.Close()
	}
	if grace {
		// Let the server exit after stdin EOF without reaping it. A zombie
		// leader keeps the process group id reserved for the signal below.
		// stdout EOF means the process already exited, so do not sleep the
		// full grace period in that case.
		if exited != nil {
			select {
			case <-exited:
			case <-time.After(2 * time.Second):
			}
		} else {
			time.Sleep(2 * time.Second)
		}
	}
	mcpTerminateProcessTree(tree, false)
	if grace {
		// Give the group time to handle SIGTERM. Do not call Wait yet: a
		// child that ignores TERM must still receive KILL while the group id
		// is known to belong to this tree. A closed stdout means the leader
		// is gone; skip the extra second.
		if exited != nil {
			select {
			case <-exited:
			case <-time.After(time.Second):
			}
		} else {
			time.Sleep(time.Second)
		}
	}
	mcpTerminateProcessTree(tree, true)
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		if stdout != nil {
			_ = stdout.Close()
		}
		return err
	case <-time.After(2 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case err := <-wait:
			if stdout != nil {
				_ = stdout.Close()
			}
			return err
		case <-time.After(time.Second):
			if stdout != nil {
				_ = stdout.Close()
			}
			return fmt.Errorf("unresponsive stdio subprocess")
		}
	}
}
