// Package store keeps the app's local files safe: every write replaces a file
// whole and privately, a file someone changed on disk since the app read it is
// never overwritten behind their back, and every file carries the version of
// the format it was written in (see version.go). It depends only on the
// standard library, so the capture engine and the UI share it.
package store

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
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
	return Write(path, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// Write streams into a private temporary file and replaces path only after
// write succeeds. A callback error leaves an existing destination untouched.
func Write(path string, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".vscroxy-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = write(f); err == nil {
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
	// Format is the file's format version, or nil for a file with no format.
	// Read upgrades an older file and writes the upgrade back; Write stamps
	// the current version. A newer file is ErrNewer and is left untouched.
	Format Formatter
	// BackupDir keeps copies of the file before it is replaced or migrated,
	// or "" for a file not worth keeping.
	BackupDir string

	exists  bool
	sum     [sha256.Size]byte
	problem error
}

// Read returns the file's contents at the current format version, nil for a
// file that does not exist yet, and remembers them for the next Write.
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
	var upgraded []byte
	if f.Format != nil {
		var err error
		upgraded, err = f.Format.Read(data)
		if err != nil {
			f.problem = err
			return nil, err
		}
		// Only a migration step is written back now; a file that merely lacks
		// its version marker is stamped when it next changes.
		if f.Format.Upgraded(data) && !bytes.Equal(upgraded, data) {
			if f.BackupDir != "" {
				_ = Backup(f.BackupDir, f.Path)
			}
			// A file that cannot be rewritten still reads this session; the
			// migration is made again next time.
			if writeErr := WriteFile(f.Path, upgraded); writeErr == nil {
				data = upgraded
			}
		}
	}
	f.remember(data, true)
	if upgraded == nil {
		return data, nil
	}
	return upgraded, nil
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
	if f.Format != nil {
		var err error
		if data, err = f.Format.Write(data); err != nil {
			return err
		}
	}
	disk, err := os.ReadFile(f.Path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if exists && f.Format != nil {
		if _, err := f.Format.Read(disk); err != nil {
			return err
		}
	}
	if f.changedFrom(disk, exists) {
		return ErrChanged
	}
	if exists && bytes.Equal(disk, data) {
		return nil
	}
	if exists && f.BackupDir != "" {
		if err := Backup(f.BackupDir, f.Path); err != nil {
			return err
		}
	}
	if err := WriteFile(f.Path, data); err != nil {
		return err
	}
	f.remember(data, true)
	return nil
}

// Changed reports whether the file on disk differs from what the last Read or
// Write saw: a file that appeared, vanished or has other contents.
func (f *File) Changed() (bool, error) {
	disk, err := os.ReadFile(f.Path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return f.changedFrom(disk, exists), nil
}

func (f *File) changedFrom(disk []byte, exists bool) bool {
	return exists != f.exists || exists && sha256.Sum256(disk) != f.sum
}

func (f *File) remember(data []byte, exists bool) {
	f.exists, f.problem = exists, nil
	f.sum = sha256.Sum256(data)
}
