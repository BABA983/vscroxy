//go:build unix

package clientsetup

import (
	"os"
	"syscall"
)

func platformOwnedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}
