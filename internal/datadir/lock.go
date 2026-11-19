package datadir

import (
	"fmt"
	"os"
	"path/filepath"
)

const lockFileName = "instance.lock"

// Lock holds the data root for one desktop instance. The OS lock belongs to
// the process, not to a stale path: a killed process releases it, and two
// roots can be locked at the same time. Close releases it.
type Lock struct {
	file *os.File
}

// LockRoot takes the lock for root, creating the root if needed. A second
// locker on the same root fails with ErrLocked.
func LockRoot(root string) (*Lock, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(root, lockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := acquire(f); err != nil {
		_ = f.Close()
		if isContention(err) {
			return nil, fmt.Errorf("%w: %s", ErrLocked, root)
		}
		return nil, err
	}
	return &Lock{file: f}, nil
}

// Close releases the lock. It is safe to call more than once.
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	f, err := l.file, release(l.file)
	l.file = nil
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}
