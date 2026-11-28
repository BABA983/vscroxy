package applog

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
)

// crashFileName is where a fatal panic is written. It sits beside the log and
// holds the runtime's panic output, not request contents the app chose to
// write.
const crashFileName = "vscroxy-crash.log"

// CrashLogPath is the live crash file in the log directory under root.
func CrashLogPath(root string) string {
	return filepath.Join(root, "logs", crashFileName)
}

// CrashReportPath is the copy of the last crash report that TakeCrash keeps
// after it clears the live file.
func CrashReportPath(root string) string {
	return CrashLogPath(root) + ".1"
}

// SetCrashOutput makes the runtime write a fatal panic, on any goroutine
// including the UI thread, to the crash file under root. A report from an
// earlier run is left for TakeCrash. The runtime keeps its own descriptor, so
// the file opened here is closed before returning.
func SetCrashOutput(root string) error {
	path := CrashLogPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return debug.SetCrashOutput(f, debug.CrashOptions{})
}

// TakeCrash returns the fatal panic report a previous run left, then clears the
// crash file so the next launch does not report it again. The report is kept
// as <name>.1 beside the live file for a bug report. It returns "" when the
// previous run left none.
func TakeCrash(root string) (string, error) {
	path := CrashLogPath(root)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return "", nil
	}
	if err := os.WriteFile(CrashReportPath(root), data, 0o600); err != nil {
		return "", err
	}
	if err := os.Truncate(path, 0); err != nil {
		return "", err
	}
	return string(data), nil
}
