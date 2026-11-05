//go:build windows

package clientsetup

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// configureStart gives the terminal its own console window. Without this flag
// the child inherits the GUI process's lack of a console.
func configureStart(cmd *exec.Cmd, newConsole bool) {
	if newConsole {
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
	}
}
