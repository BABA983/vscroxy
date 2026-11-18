//go:build linux || android

package system

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// startedFromTerminal checks all three standard streams: a GUI launch has no
// terminal, even when it inherits /dev/null (which looks like a character
// device). The user already has a full environment if any stream is a TTY.
func startedFromTerminal() bool {
	for _, file := range []*os.File{os.Stdin, os.Stdout, os.Stderr} {
		if file != nil {
			if _, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS); err == nil {
				return true
			}
		}
	}
	return false
}

// setShellProcessGroup lets a timeout stop the shell and the commands its
// profile started, rather than leaving them holding the output pipe.
func setShellProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
