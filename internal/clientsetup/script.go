package clientsetup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WritePOSIXTerminalScript writes an executable shell script for a terminal
// window and returns its path. The script removes itself when the terminal
// starts, so no file is left behind.
func WritePOSIXTerminalScript(p Proxy) (string, error) {
	return WritePOSIXTerminalScriptAs(p, "terminal.sh")
}

// WritePOSIXTerminalScriptAs is WritePOSIXTerminalScript with a caller-chosen
// file name, as macOS Terminal needs a .command suffix to run it.
func WritePOSIXTerminalScriptAs(p Proxy, name string) (string, error) {
	script, err := POSIXScript(p)
	if err != nil {
		return "", err
	}
	// Some terminal emulators re-split -e arguments on spaces. A private
	// directory under /tmp keeps the script path free of spaces even when
	// TMPDIR contains one. Fall back to the system temp directory.
	dir, err := os.MkdirTemp("/tmp", "vscroxy-terminal-*")
	if err != nil {
		dir, err = os.MkdirTemp("", "vscroxy-terminal-*")
		if err != nil {
			return "", err
		}
	}
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	_, err = file.WriteString(script)
	err = errors.Join(err, file.Close())
	if err == nil {
		err = os.Chmod(path, 0o700)
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("write terminal script: %w", err)
	}
	return path, nil
}

// RemoveTerminalScript removes a script whose terminal could not start, and
// the private directory created for it.
func RemoveTerminalScript(path string) {
	_ = os.Remove(path)
	if strings.HasPrefix(filepath.Base(filepath.Dir(path)), "vscroxy-terminal-") {
		_ = os.Remove(filepath.Dir(path))
	}
}
