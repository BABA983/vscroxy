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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	fileName = "vscroxy.log"
	maxSize  = 5 << 20 // bytes before the file rotates
	keep     = 5       // files kept, the current one included
)

// Level names are identifiers in settings.json and on the command line, not
// text for the UI: the catalogs name them for the person choosing one.
const (
	LevelTrace   = "trace"
	LevelDebug   = "debug"
	LevelInfo    = "info"
	LevelWarning = "warning"
	LevelError   = "error"
	LevelOff     = "off"
)

var (
	levelNames = []string{LevelTrace, LevelDebug, LevelInfo, LevelWarning, LevelError, LevelOff}
	// levels holds the one source of truth for filtering: handler uses this
	// LevelVar, so setting a level takes effect on the very next record in
	// an already-open file. Off is above Error and suppresses everything.
	levels = func() *slog.LevelVar {
		v := new(slog.LevelVar)
		v.Set(slog.LevelInfo)
		return v
	}()
	// flagLevel is set once by --log-level. It wins over the saved setting
	// for this run only and is never written back to settings.json.
	flagLevel atomic.Bool
)

// LevelNames returns the levels in the order a picker shows them.
func LevelNames() []string { return slices.Clone(levelNames) }

// EffectiveLevel names the level records are currently filtered by.
func EffectiveLevel() string { return levelName(levels.Level()) }

// FlagLevelOverridden reports whether --log-level is deciding this run's level.
func FlagLevelOverridden() bool { return flagLevel.Load() }

// SetLevel applies a saved or freshly chosen level. A --log-level override
// keeps control of this run, while the caller may still save the new choice.
func SetLevel(name string) error {
	level, err := parseLevel(name)
	if err != nil {
		return err
	}
	if !flagLevel.Load() {
		levels.Set(level)
	}
	return nil
}

// SetFlagLevel installs the one-run override from the command line.
func SetFlagLevel(name string) error {
	level, err := parseLevel(name)
	if err != nil {
		return err
	}
	flagLevel.Store(true)
	levels.Set(level)
	return nil
}

func parseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", LevelInfo:
		return slog.LevelInfo, nil
	case LevelTrace:
		return slog.Level(-8), nil // between slog.LevelDebug and nothing
	case LevelDebug:
		return slog.LevelDebug, nil
	case LevelWarning:
		return slog.LevelWarn, nil
	case LevelError:
		return slog.LevelError, nil
	case LevelOff:
		return slog.Level(12), nil // above Error, so every record is filtered
	default:
		return 0, fmt.Errorf("unknown log level %q; want %s", name, strings.Join(levelNames, ", "))
	}
}

func levelName(level slog.Level) string {
	switch {
	case level <= -8:
		return LevelTrace
	case level < slog.LevelInfo:
		return LevelDebug
	case level < slog.LevelWarn:
		return LevelInfo
	case level < slog.LevelError:
		return LevelWarning
	case level < 12:
		return LevelError
	default:
		return LevelOff
	}
}

// For returns a logger for one subsystem. It reads the default logger at the
// call site: the log file is opened after packages are initialised, and there
// is no second handler or logger to keep in step with it.
func For(component string) *slog.Logger {
	return slog.Default().With("component", component)
}

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
	handler := slog.NewTextHandler(io.MultiWriter(os.Stderr, f), &slog.HandlerOptions{Level: levels})
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
