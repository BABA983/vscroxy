//go:build windows

package store

import (
	"errors"
	"syscall"
)

// os.Rename on Windows reports Win32 errors, not the invented POSIX names in
// syscall: access denied (the EPERM/EACCES cases), sharing violation (EBUSY)
// and lock violation are the transient ones. Access denied also covers a
// read-only or directory target, which waiting cannot fix.
func isSharingViolation(path string, err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case 5: // ERROR_ACCESS_DENIED
		return !permanentRenameTarget(path)
	case 32, // ERROR_SHARING_VIOLATION
		33: // ERROR_LOCK_VIOLATION
		return true
	}
	return false
}
