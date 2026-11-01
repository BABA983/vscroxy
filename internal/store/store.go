// Package store keeps the app's local files safe: every write replaces a file
// whole and privately, and a file someone changed on disk since the app read
// it is never overwritten behind their back. It depends only on the standard
// library, so the capture engine and the UI share it.
//
// There are no format versions or migrations yet: while the app is a
// prototype, formats change directly (see AGENTS.md).
package store

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrChanged is the error Write returns for a file changed on disk since it
// was read. Nothing is written; reload to see the change, then save again.
var ErrChanged = errors.New("changed on disk since it was read; reload, then save again")

// WriteFile replaces path with data through a temporary file and a rename, so
// a failure leaves the old file whole. The file is private (0600) and so is a
// directory it creates (0700): app files can hold credentials.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".vscroxy-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// File is one file the app owns and a person may also edit. It remembers what
// it last read or wrote. A file that could not be read is read-only until it
// reads cleanly, so a damaged file is never replaced by defaults.
//
// A File is not safe for concurrent use; its owner uses it from one goroutine.
type File struct {
	Path string

	exists  bool
	sum     [sha256.Size]byte
	problem error
}

// Read returns the file's contents, nil for a file that does not exist yet,
// and remembers them for the next Write.
func (f *File) Read() ([]byte, error) {
	data, err := os.ReadFile(f.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		f.remember(nil, false)
		return nil, nil
	case err != nil:
		f.problem = err
		return nil, err
	}
	f.remember(data, true)
	return data, nil
}

// Fail marks the file read-only: it was read but its contents are unusable,
// as JSON that does not parse. Read again to clear it.
func (f *File) Fail(err error) {
	f.problem = err
}

// Problem is why the file is read-only, or nil.
func (f *File) Problem() error {
	return f.problem
}

// Write replaces the file with data, unless it could not be read or changed
// on disk since the last Read or Write. A File never read writes freely only
// where no file exists yet.
func (f *File) Write(data []byte) error {
	if f.problem != nil {
		return f.problem
	}
	disk, err := os.ReadFile(f.Path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if exists != f.exists || exists && sha256.Sum256(disk) != f.sum {
		return ErrChanged
	}
	if exists && bytes.Equal(disk, data) {
		return nil
	}
	if err := WriteFile(f.Path, data); err != nil {
		return err
	}
	f.remember(data, true)
	return nil
}

func (f *File) remember(data []byte, exists bool) {
	f.exists, f.problem = exists, nil
	f.sum = sha256.Sum256(data)
}
