//go:build unix

package request

import "syscall"

func mcpProcessAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
