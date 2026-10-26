//go:build linux || darwin

package system

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

func proxyGuardSignals() {
	// The owner can disappear before the enable reply. Stdout's EPIPE must
	// return an error, not terminate this process before rollback runs.
	signal.Ignore(syscall.SIGPIPE)
}

func separateProxyGuard(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func lockProxyJournal(path string) (*os.File, error) {
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another proxy session or recovery is active: %w", err)
	}
	return file, nil
}
