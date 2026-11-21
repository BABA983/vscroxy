//go:build windows

package clientsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// terminalHelperEnv makes the test binary act as the terminal's command. It is
// set only in the child: OpenTerminal reads ComSpec, so the test points ComSpec
// at this binary instead of cmd.exe.
const terminalHelperEnv = "VSCROXY_TEST_TERMINAL_REPORT"

// getConsoleWindow reports the console this process is attached to, which a
// terminal needs even when its standard handles were redirected.
var getConsoleWindow = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")

func consoleWindow() uintptr {
	window, _, _ := getConsoleWindow.Call()
	return window
}

func TestMain(m *testing.M) {
	if report := os.Getenv(terminalHelperEnv); report != "" && slices.Contains(os.Args[1:], "/k") {
		os.Exit(reportTerminalConsole(report))
	}
	os.Exit(m.Run())
}

// reportTerminalConsole records whether this process got a real console. cmd.exe
// /k does the same thing implicitly: when its stdin is not a console it reads
// EOF and exits, so a terminal that stays open is one with console handles.
func reportTerminalConsole(report string) int {
	lines := make([]string, 0, 8)
	for _, handle := range []struct {
		name string
		file windows.Handle
	}{
		{"stdin", windows.Stdin},
		{"stdout", windows.Stdout},
		{"stderr", windows.Stderr},
	} {
		var mode uint32
		state := "not a console"
		if err := windows.GetConsoleMode(handle.file, &mode); err == nil {
			state = "console"
		}
		lines = append(lines, handle.name+"="+state)
	}
	lines = append(lines, fmt.Sprintf("window=%t", consoleWindow() != 0))
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "NODE_EXTRA_CA_CERTS"} {
		lines = append(lines, name+"="+os.Getenv(name))
	}
	// Rename after writing so the parent never reads a partial report.
	temporary := report + ".tmp"
	if err := os.WriteFile(temporary, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		return 1
	}
	if err := os.Rename(temporary, report); err != nil {
		return 1
	}
	return 0
}

// TestOpenTerminalKeepsAConsoleWindow is the regression test for a proxied
// terminal that flashed a console host and vanished: os/exec gave cmd.exe
// os.DevNull for stdin and stdout, and cmd.exe /k exited on the empty stdin.
func TestOpenTerminalKeepsAConsoleWindow(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	report := filepath.Join(dir, "report.txt")
	t.Setenv("ComSpec", exe)
	t.Setenv(terminalHelperEnv, report)

	command, err := OpenTerminal(testProxy())
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	lines := waitForTerminalReport(t, dir, report)
	for _, name := range []string{"stdin", "stdout", "stderr"} {
		if got := lines[name]; got != "console" {
			t.Errorf("%s = %q, want a console handle; the terminal would exit at once", name, got)
		}
	}
	if lines["window"] != "true" {
		t.Errorf("window = %q, want a new console window", lines["window"])
	}
	if got := lines["HTTP_PROXY"]; got != "http://127.0.0.1:7890" {
		t.Errorf("HTTP_PROXY = %q, want the capture proxy", got)
	}
	if got := lines["HTTPS_PROXY"]; got != "http://127.0.0.1:7890" {
		t.Errorf("HTTPS_PROXY = %q, want the capture proxy", got)
	}
	if got := lines["NO_PROXY"]; got != "127.0.0.1:7890" {
		t.Errorf("NO_PROXY = %q, want only the listener", got)
	}
	if got := lines["NODE_EXTRA_CA_CERTS"]; got != testProxy().CAPath {
		t.Errorf("NODE_EXTRA_CA_CERTS = %q, want %q", got, testProxy().CAPath)
	}
}

// waitForTerminalReport blocks until the child has renamed its report into
// place. A directory change notification is the child's only completion signal,
// so this waits on Windows instead of polling on a timer.
func waitForTerminalReport(t *testing.T, dir, report string) map[string]string {
	t.Helper()
	watch, err := windows.FindFirstChangeNotification(dir, false, windows.FILE_NOTIFY_CHANGE_FILE_NAME)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.FindCloseChangeNotification(watch)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if data, err := os.ReadFile(report); err == nil {
			lines := make(map[string]string, 8)
			for _, line := range strings.Split(string(data), "\n") {
				name, value, ok := strings.Cut(line, "=")
				if ok {
					lines[name] = value
				}
			}
			return lines
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("terminal wrote no report within 30s")
		}
		event, err := windows.WaitForSingleObject(watch, uint32(remaining.Milliseconds()))
		if err != nil {
			t.Fatal(err)
		}
		if event == uint32(windows.WAIT_TIMEOUT) {
			t.Fatalf("terminal wrote no report within 30s")
		}
		if err := windows.FindNextChangeNotification(watch); err != nil {
			t.Fatal(err)
		}
	}
}
