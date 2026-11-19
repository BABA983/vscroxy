//go:build windows

package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWindowsAccessDeniedSkipsReadOnlyAndDirectoryTargets(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	denied := &os.LinkError{Op: "rename", Err: syscall.Errno(5)}
	if !isSharingViolation(target, denied) {
		t.Error("access denied on a writable file is transient")
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o600) })
	if err := os.Chmod(target, 0o400); err != nil {
		t.Fatal(err)
	}
	if isSharingViolation(target, denied) {
		t.Error("access denied on a read-only file must not be retried")
	}
	if isSharingViolation(dir, denied) {
		t.Error("access denied on a directory must not be retried")
	}
	busy := &os.LinkError{Op: "rename", Err: syscall.Errno(32)}
	if !isSharingViolation(target, busy) {
		t.Error("a sharing violation is transient")
	}
}
