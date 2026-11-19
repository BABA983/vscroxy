//go:build unix

package datadir

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func acquire(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func release(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}

func isContention(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}
