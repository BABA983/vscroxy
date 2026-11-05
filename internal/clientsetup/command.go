package clientsetup

import (
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
	// Cleanup runs after a started child exits, such as removing a temporary
	// browser profile.
	Cleanup func()
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
// when it exits instead of leaving a zombie for the app's lifetime. Cleanup
// runs after the child exits.
func (c Argv) Start() error {
	bin, err := toolPath(c.Name)
	if err != nil {
		return err
	}
	cmd := c.command(bin)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", c.Name, err)
	}
	go func() {
		_ = cmd.Wait()
		if c.Cleanup != nil {
			c.Cleanup()
		}
	}()
	return nil
}

// RunOutput returns a helper's standard output.
func RunOutput(c Argv) ([]byte, error) {
	bin, err := toolPath(c.Name)
	if err != nil {
		return nil, err
	}
	out, err := c.command(bin).CombinedOutput()
	if err != nil {
		line := c.String(runtime.GOOS == "windows")
		if msg := strings.TrimSpace(string(out)); msg != "" {
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

// MergeEnv returns base with extra's names replaced, ignoring case because
// Windows environment names are case-insensitive. Duplicate names in base are
// removed, so a child never sees two HTTP_PROXY entries.
func MergeEnv(base, extra []string) []string {
	merged := make([]string, 0, len(base)+len(extra))
	index := make(map[string]int, len(base))
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		key := strings.ToUpper(name)
		if _, exists := index[key]; exists {
			continue
		}
		index[key] = len(merged)
		merged = append(merged, entry)
	}
	for _, entry := range extra {
		name, value, _ := strings.Cut(entry, "=")
		key := strings.ToUpper(name)
		if at, exists := index[key]; exists {
			// Keep the first spelling of a name; Windows treats names
			// case-insensitively, so a second casing would be a duplicate.
			existing, _, _ := strings.Cut(merged[at], "=")
			merged[at] = existing + "=" + value
			continue
		}
		index[key] = len(merged)
		merged = append(merged, entry)
	}
	return merged
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
