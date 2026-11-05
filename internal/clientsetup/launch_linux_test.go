//go:build linux

package clientsetup

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestTerminalArgsMatchEachEmulator(t *testing.T) {
	script := "/tmp/vscroxy terminal.sh"
	for terminal, want := range map[string][]string{
		"/usr/bin/gnome-terminal":      {"--", script},
		"/usr/bin/xfce4-terminal":      {"-x", script},
		"/usr/bin/kitty":               {script},
		"/usr/bin/foot":                {script},
		"/usr/bin/wezterm":             {"start", "--", script},
		"/usr/bin/xterm":               {"-e", script},
		"/usr/bin/konsole":             {"-e", script},
		"/usr/bin/alacritty":           {"-e", script},
		"/usr/bin/x-terminal-emulator": {"-e", script},
	} {
		if got := terminalArgs(terminal, script); !slices.Equal(got, want) {
			t.Errorf("%s: got %q want %q", terminal, got, want)
		}
	}
}

func TestOpenTerminalReportsAMissingTerminal(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("TERMINAL", "")
	_, err := OpenTerminal(testProxy())
	if err == nil {
		t.Fatal("terminal unexpectedly found")
	}
	missing, ok := errors.AsType[*MissingToolError](err)
	if !ok || missing.Tool != "terminal emulator" || !strings.Contains(missing.Error(), "xterm") {
		t.Fatalf("error: %v", err)
	}
}

func TestOpenChromeAndFirefoxReportMissingBrowsers(t *testing.T) {
	t.Setenv("PATH", "")
	if _, err := OpenChrome(testProxy(), "/tmp/profile"); err == nil {
		t.Fatal("Chrome unexpectedly found")
	} else if missing, ok := errors.AsType[*MissingToolError](err); !ok || missing.Tool != "Google Chrome or Chromium" {
		t.Fatalf("Chrome error: %v", err)
	}
	if _, err := OpenFirefox("/tmp/profile"); err == nil {
		t.Fatal("Firefox unexpectedly found")
	} else if missing, ok := errors.AsType[*MissingToolError](err); !ok || missing.Tool != "Firefox" {
		t.Fatalf("Firefox error: %v", err)
	}
}

func TestOpenElectronNeedsAnExistingExecutable(t *testing.T) {
	_, err := OpenElectron("/nonexistent/vscroxy-electron-app", testProxy())
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error: %v", err)
	}
}
