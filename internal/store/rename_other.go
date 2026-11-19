//go:build !windows

package store

// A rename that failed on another OS is not a Windows sharing violation, so
// WriteFile returns it at once.
func isSharingViolation(error) bool { return false }
