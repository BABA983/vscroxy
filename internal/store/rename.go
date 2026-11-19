package store

import (
	"os"
	"time"
)

// Windows can briefly refuse a rename while another process has the target
// open (an antivirus scanner, an indexer, a sync client). VS Code retries
// those sharing violations for about ten seconds with a growing delay; do
// the same so a save does not fail because somebody glanced at the file.
const (
	renameRetryFor   = 10 * time.Second
	renameRetryFirst = 10 * time.Millisecond
	renameRetryMax   = 250 * time.Millisecond
)

// The seams let tests simulate a busy file and a passing clock without
// waiting for real time or real contention.
var (
	rename        = os.Rename
	renameLstat   = os.Lstat
	renameClock   = time.Now
	renameSleep   = time.Sleep
	retryOnRename = isSharingViolation
)

func replaceFile(from, to string) error {
	err := rename(from, to)
	if err == nil || !retryOnRename(to, err) {
		return err
	}
	deadline := renameClock().Add(renameRetryFor)
	for delay := renameRetryFirst; ; {
		left := deadline.Sub(renameClock())
		if left <= 0 {
			return err
		}
		renameSleep(min(delay, left))
		err = rename(from, to)
		if err == nil || !retryOnRename(to, err) {
			return err
		}
		delay = min(delay*2, renameRetryMax)
	}
}

// permanentRenameTarget rules out errors that waiting cannot fix: renaming
// onto a directory, or onto a read-only file (on Windows, the read-only
// attribute is part of FileMode's permission bits). Without this check,
// ERROR_ACCESS_DENIED for either would freeze the caller for the whole retry
// window before failing anyway.
func permanentRenameTarget(path string) bool {
	info, err := renameLstat(path)
	if err != nil {
		return false // a missing target is not a read-only target
	}
	return info.IsDir() || info.Mode().Perm()&0o200 == 0
}
