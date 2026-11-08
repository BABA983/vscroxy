//go:build windows

package request

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type mcpProcessTree struct {
	job windows.Handle
	pid int
}

// mcpPrepareCommand starts the server suspended so mcpAttachProcess can put
// it in the Job Object before it can create any child processes.
func mcpPrepareCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
}

// mcpAttachProcess puts the server in a kill-on-close Job Object and resumes
// its only (suspended) thread. Closing the job handle terminates every process
// in it without reusing a pid after Wait. If the process is already in a
// restrictive job, fall back to taskkill before Wait rather than failing to
// start the server.
func mcpAttachProcess(cmd *exec.Cmd) mcpProcessTree {
	tree := mcpProcessTree{}
	if cmd == nil || cmd.Process == nil {
		return tree
	}
	tree.pid = cmd.Process.Pid
	if job, ok := mcpAssignJob(cmd.Process.Pid); ok {
		tree.job = job
	}
	if !mcpResumeProcess(cmd.Process.Pid) {
		mcpTerminateProcessTree(tree, true)
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		mcpCloseProcessTree(tree)
		return mcpProcessTree{}
	}
	return tree
}

func mcpAssignJob(pid int) (windows.Handle, bool) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, false
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, false
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		windows.CloseHandle(job)
		return 0, false
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return 0, false
	}
	return job, true
}

// mcpResumeProcess resumes the suspended process's main thread. A fresh
// suspended process has one thread; filtering the Toolhelp snapshot by owner
// avoids touching any other process.
func mcpResumeProcess(pid int) bool {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err := windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != uint32(pid) {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			continue
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		if err == nil {
			return true
		}
	}
	return false
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

func mcpCloseProcess(cmd *exec.Cmd, tree mcpProcessTree, stdin io.Closer, stdout io.Closer, _ <-chan struct{}, grace bool) error {
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
	if grace {
		// Let the server exit after stdin EOF. If it already exited, Wait
		// returns immediately instead of sleeping a fixed grace period.
		select {
		case err := <-wait:
			mcpCloseProcessTree(tree)
			if stdout != nil {
				_ = stdout.Close()
			}
			return err
		case <-time.After(2 * time.Second):
		}
	}
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
			mcpCloseProcessTree(tree)
			if stdout != nil {
				_ = stdout.Close()
			}
			return fmt.Errorf("unresponsive stdio subprocess")
		}
	}
}
