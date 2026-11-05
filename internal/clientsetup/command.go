package clientsetup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ErrUnsupported means the feature needs an operating system this build does
// not run on.
var ErrUnsupported = errors.New("not available on this platform")

// MissingToolError names a helper the one-click setup needs. The hint says what
// to install or configure; it is shown as-is in a notification.
type MissingToolError struct {
	Tool string
	Hint string
}

func (e *MissingToolError) Error() string {
	if e.Hint == "" {
		return e.Tool + " not found"
	}
	return e.Tool + " not found; " + e.Hint
}

var toolHints = map[string]string{
	"adb":      "install Android platform-tools and put adb on PATH",
	"keytool":  "install a JDK and put keytool on PATH",
	"xcrun":    "install Xcode and its command-line tools",
	"certutil": "install NSS tools (libnss3-tools on Linux)",
}

// Argv is one external command. Building it never runs anything, so a caller
// can show it for review first.
type Argv struct {
	Name string
	Args []string
	// Env, when non-nil, is the complete environment for the child. Windows
	// uses it to open a terminal with the proxy variables without a batch file.
	Env []string
	// NewConsole asks Windows to create a console window for the child.
	NewConsole bool
	// OnStartError removes resources such as a temporary browser profile when
	// the client could not be started. It must not run after a successful
	// start: Firefox and Chrome launchers may exit while the real browser is
	// still using the profile.
	OnStartError func()
}

// String renders the command for a person to review or copy.
func (c Argv) String(windows bool) string {
	if windows {
		return WindowsCommandLine(c.Name, c.Args)
	}
	return POSIXCommandLine(c.Name, c.Args)
}

func (c Argv) command(bin string) *exec.Cmd {
	cmd := exec.Command(bin, c.Args...)
	if c.Env != nil {
		cmd.Env = c.Env
	}
	configureStart(cmd, c.NewConsole)
	return cmd
}

// Run waits for the command and returns its output in the error when it fails.
func (c Argv) Run() error {
	bin, err := toolPath(c.Name)
	if err != nil {
		return err
	}
	out, err := c.command(bin).CombinedOutput()
	if err != nil {
		line := c.String(runtime.GOOS == "windows")
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s: %s", line, firstLine(msg))
		}
		return fmt.Errorf("%s: %w", line, err)
	}
	return nil
}

// Start launches a long-lived client and releases it from this process. The
// caller does not wait for a terminal or a browser; a background Wait reaps it
// when it exits instead of leaving a zombie for the app's lifetime. It does not
// remove a browser profile on exit: launcher processes can exit while the real
// browser is still running.
func (c Argv) Start() error {
	bin, err := toolPath(c.Name)
	if err != nil {
		return err
	}
	cmd := c.command(bin)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", c.Name, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// RunOutput returns a helper's standard output. Standard error is used only in
// the error message, never returned as data: adb writes daemon chatter there.
func RunOutput(c Argv) ([]byte, error) {
	bin, err := toolPath(c.Name)
	if err != nil {
		return nil, err
	}
	cmd := c.command(bin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		line := c.String(runtime.GOOS == "windows")
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s: %s", line, firstLine(msg))
		}
		return nil, fmt.Errorf("%s: %w", line, err)
	}
	return out, nil
}

// toolPath finds a helper and turns the usual "executable file not found" into
// a message that says what to install. An absolute path still works.
func toolPath(name string) (string, error) {
	bin, err := exec.LookPath(name)
	if err != nil {
		return "", &MissingToolError{Tool: name, Hint: toolHints[name]}
	}
	return bin, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

// JoinCommands renders a sequence for review, as a shell would chain it.
func JoinCommands(commands []Argv, windows bool) string {
	parts := make([]string, 0, len(commands))
	for _, command := range commands {
		parts = append(parts, command.String(windows))
	}
	return strings.Join(parts, " && ")
}

// MergeEnv appends extra to base. exec deduplicates names (case-insensitively
// on Windows) and keeps the last value, so later entries win without this
// function rewriting names such as =C:.
func MergeEnv(base, extra []string) []string {
	merged := make([]string, 0, len(base)+len(extra))
	merged = append(merged, base...)
	return append(merged, extra...)
}

// findExecutable prefers an explicit path, then a name on PATH.
func findExecutable(paths, names []string) (string, bool) {
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, true
		}
	}
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path, true
		}
	}
	return "", false
}
