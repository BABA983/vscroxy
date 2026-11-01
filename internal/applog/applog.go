// Package applog writes the app's own log to a file that rotates by size, so
// a problem can be looked into after the fact. It never sees request
// contents: only what callers pass to log/slog and the standard log package.
package applog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

const (
	fileName = "vscroxy.log"
	maxSize  = 5 << 20 // bytes before the file rotates
	keep     = 5       // files kept, the current one included
)

// Open makes the log under dir the default for log/slog and, through it, the
// standard log package. Lines also go to stderr. Close flushes nothing; it
// only closes the file.
func Open(dir string) (io.Closer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f := &rotating{path: filepath.Join(dir, fileName), limit: maxSize}
	if err := f.open(); err != nil {
		return nil, err
	}
	handler := slog.NewTextHandler(io.MultiWriter(os.Stderr, f), &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))
	return f, nil
}

// rotating is a file that moves aside as path.1 … path.(keep-1) once it
// holds limit bytes. Writes are whole lines from slog, so a line never
// splits across files.
type rotating struct {
	mu    sync.Mutex
	path  string
	limit int64
	file  *os.File
	size  int64
}

func (r *rotating) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.file, r.size = f, info.Size()
	return nil
}

func (r *rotating) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return 0, os.ErrClosed
	}
	if r.size > 0 && r.size+int64(len(p)) > r.limit {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotating) rotate() error {
	if err := r.file.Close(); err != nil {
		return err
	}
	r.file = nil
	for i := keep - 1; i > 0; i-- {
		from := r.path
		if i > 1 {
			from = fmt.Sprintf("%s.%d", r.path, i-1)
		}
		// The oldest is replaced; a missing one is a gap in a young log.
		if err := os.Rename(from, fmt.Sprintf("%s.%d", r.path, i)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return r.open()
}

func (r *rotating) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}
