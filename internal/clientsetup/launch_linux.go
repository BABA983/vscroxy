//go:build linux

package clientsetup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// linuxTerminals are tried in order. $TERMINAL comes first so a user who
// already chose a terminal gets that one.
var linuxTerminals = []string{
	"x-terminal-emulator", "gnome-terminal", "konsole", "xfce4-terminal",
	"alacritty", "kitty", "wezterm", "foot", "xterm",
}

// OpenTerminal opens a new terminal window whose shell has the proxy and CA
// environment. The window's process tree is the only thing changed.
func OpenTerminal(p Proxy) (Argv, error) {
	names := linuxTerminals
	if name := strings.TrimSpace(os.Getenv("TERMINAL")); name != "" {
		names = append([]string{name}, names...)
	}
	for _, name := range names {
		bin, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		script, err := WritePOSIXTerminalScript(p)
		if err != nil {
			return Argv{}, err
		}
		command := Argv{Name: bin, Args: terminalArgs(bin, script)}
		command.OnStartError = func() { RemoveTerminalScript(script) }
		return command, nil
	}
	return Argv{}, &MissingToolError{
		Tool: "terminal emulator",
		Hint: "install xterm, gnome-terminal, konsole or another terminal, or set $TERMINAL",
	}
}

// terminalArgs is the per-emulator spelling of "run this script".
func terminalArgs(terminal, script string) []string {
	switch filepath.Base(terminal) {
	case "gnome-terminal":
		return []string{"--", script}
	case "xfce4-terminal":
		return []string{"-x", script}
	case "kitty", "foot":
		return []string{script}
	case "wezterm":
		return []string{"start", "--", script}
	default:
		return []string{"-e", script}
	}
}

func chromeBinary() (string, error) {
	path, ok := findExecutable(nil, []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"})
	if !ok {
		return "", &MissingToolError{
			Tool: "Google Chrome or Chromium",
			Hint: "install one, or put google-chrome or chromium on PATH",
		}
	}
	return path, nil
}

func firefoxBinary() (string, error) {
	path, ok := findExecutable(nil, []string{"firefox", "firefox-esr"})
	if !ok {
		return "", &MissingToolError{Tool: "Firefox", Hint: "install Firefox, or put firefox on PATH"}
	}
	return path, nil
}

// OpenChrome starts Chrome or Chromium on a separate temporary profile.
func OpenChrome(p Proxy, profile string) (Argv, error) {
	path, err := chromeBinary()
	if err != nil {
		return Argv{}, err
	}
	args, err := ChromeArgs(p, profile)
	if err != nil {
		return Argv{}, err
	}
	return Argv{Name: path, Args: args}, nil
}

// OpenFirefox starts Firefox on a separate temporary profile.
func OpenFirefox(profile string) (Argv, error) {
	path, err := firefoxBinary()
	if err != nil {
		return Argv{}, err
	}
	args, err := FirefoxArgs(profile)
	if err != nil {
		return Argv{}, err
	}
	return Argv{Name: path, Args: args}, nil
}

// OpenElectron starts an Electron app with Chromium's proxy flag.
func OpenElectron(app string, p Proxy) (Argv, error) {
	app = strings.TrimSpace(app)
	if app == "" {
		return Argv{}, fmt.Errorf("choose an Electron app executable")
	}
	bin, err := exec.LookPath(app)
	if err != nil {
		if info, statErr := os.Stat(app); statErr != nil || info.IsDir() {
			return Argv{}, fmt.Errorf("Electron app %q not found; choose its executable", app)
		}
		bin = app
	}
	args, err := ElectronArgs(p)
	if err != nil {
		return Argv{}, err
	}
	return Argv{Name: bin, Args: args}, nil
}
