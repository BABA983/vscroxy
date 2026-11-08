//go:build windows

package request

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type mcpProcessTree struct {
	job windows.Handle
	pid int
}

func mcpPrepareCommand(cmd *exec.Cmd) {}

// mcpAttachProcess puts the server in a kill-on-close Job Object. Closing the
// job handle terminates every process in it without reusing a pid after Wait.
// If the process is already in a restrictive job, fall back to taskkill before
// Wait rather than failing to start the server.
func mcpAttachProcess(cmd *exec.Cmd) mcpProcessTree {
	tree := mcpProcessTree{}
	if cmd == nil || cmd.Process == nil {
		return tree
	}
	tree.pid = cmd.Process.Pid
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return tree
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return tree
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return tree
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return tree
	}
	tree.job = job
	return tree
}

func mcpTerminateProcessTree(tree mcpProcessTree, force bool) {
	if tree.job != 0 {
		_ = windows.TerminateJobObject(tree.job, 1)
		return
	}
	if tree.pid <= 0 {
		return
	}
	args := []string{"/T", "/PID", strconv.Itoa(tree.pid)}
	if force {
		args = append(args, "/F")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "taskkill", args...).Run()
}

func mcpCloseProcessTree(tree mcpProcessTree) {
	if tree.job != 0 {
		_ = windows.CloseHandle(tree.job)
	}
}

func mcpCloseProcess(cmd *exec.Cmd, tree mcpProcessTree, stdin io.Closer, stdout io.Closer, grace bool) error {
	if stdin != nil {
		_ = stdin.Close()
	}
	if tree.job == 0 {
		// Assignment to a job can fail inside a restrictive parent job. Kill
		// the tree while the leader still exists; never after Wait.
		mcpTerminateProcessTree(tree, true)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	delay := 2 * time.Second
	if !grace {
		delay = 0
	}
	select {
	case err := <-wait:
		mcpCloseProcessTree(tree)
		if stdout != nil {
			_ = stdout.Close()
		}
		return err
	case <-time.After(delay):
		mcpTerminateProcessTree(tree, true)
		select {
		case err := <-wait:
			mcpCloseProcessTree(tree)
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
				mcpCloseProcessTree(tree)
				if stdout != nil {
					_ = stdout.Close()
				}
				return err
			case <-time.After(time.Second):
				return fmt.Errorf("unresponsive stdio subprocess")
			}
		}
	}
}
