//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package request

import (
	"os/exec"

	"github.com/baba983/vscroxy/internal/errorsx"
	"golang.org/x/sys/unix"
)

// mcpWatchProcessExit watches NOTE_EXIT without reaping the leader. Darwin's
// x/sys/unix has no Waitid, and a kqueue watch leaves cmd.Wait free to collect
// the exit after the close path has signalled the whole process tree.
func mcpWatchProcessExit(cmd *exec.Cmd, exited *mcpExitSignal) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	go errorsx.Guard(func() {
		kq, err := unix.Kqueue()
		if err != nil {
			exited.signal()
			return
		}
		defer unix.Close(kq)

		var event unix.Kevent_t
		unix.SetKevent(&event, cmd.Process.Pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
		event.Fflags = unix.NOTE_EXIT
		if _, err := unix.Kevent(kq, []unix.Kevent_t{event}, nil, nil); err != nil {
			exited.signal()
			return
		}
		var watched [1]unix.Kevent_t
		for {
			n, err := unix.Kevent(kq, nil, watched[:], nil)
			if err == unix.EINTR {
				continue
			}
			if err != nil || n > 0 {
				exited.signal()
				return
			}
		}
	})
}
