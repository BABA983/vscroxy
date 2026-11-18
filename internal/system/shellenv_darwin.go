//go:build darwin || freebsd || openbsd || netbsd || dragonfly

package system

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func startedFromTerminal() bool {
	for _, file := range []*os.File{os.Stdin, os.Stdout, os.Stderr} {
		if file != nil {
			if _, err := unix.IoctlGetTermios(int(file.Fd()), unix.TIOCGETA); err == nil {
				return true
			}
		}
	}
	return false
}

func setShellProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
