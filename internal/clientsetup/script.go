package clientsetup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WritePOSIXTerminalScript writes an executable shell script for a terminal
// window and returns its path. The terminal owns the file after this returns;
// it is small and lives in the system temporary directory.
func WritePOSIXTerminalScript(p Proxy) (string, error) {
	return WritePOSIXTerminalScriptAs(p, "vscroxy-terminal-*.sh")
}

// WritePOSIXTerminalScriptAs is WritePOSIXTerminalScript with a caller-chosen
// name pattern, as macOS Terminal needs a .command suffix to run it.
func WritePOSIXTerminalScriptAs(p Proxy, pattern string) (string, error) {
	script, err := POSIXScript(p)
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	_, err = file.WriteString(script)
	err = errors.Join(err, file.Close())
	if err == nil {
		err = os.Chmod(path, 0o700)
	}
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("write terminal script: %w", err)
	}
	return path, nil
}

// WriteWindowsTerminalScript writes the batch file a new cmd.exe window runs.
func WriteWindowsTerminalScript(p Proxy) (string, error) {
	script, err := WindowsScript(p)
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp("", "vscroxy-terminal-*.cmd")
	if err != nil {
		return "", err
	}
	path := file.Name()
	_, err = file.WriteString(script)
	err = errors.Join(err, file.Close())
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("write terminal script: %w", err)
	}
	return path, nil
}

// RemoveTerminalScript removes a script whose terminal could not start.
func RemoveTerminalScript(path string) {
	if filepath.Base(path) == path {
		return // never remove a bare name from the working directory
	}
	_ = os.Remove(path)
}
