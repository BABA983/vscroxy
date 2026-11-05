//go:build darwin

package clientsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OpenTerminal opens Terminal.app on a .command script that exports the proxy
// environment and then starts the user's login shell.
func OpenTerminal(p Proxy) (Argv, error) {
	script, err := WritePOSIXTerminalScriptAs(p, "vscroxy-terminal-*.command")
	if err != nil {
		return Argv{}, err
	}
	if _, err := toolPath("/usr/bin/open"); err != nil {
		RemoveTerminalScript(script)
		return Argv{}, err
	}
	return Argv{Name: "/usr/bin/open", Args: []string{"-a", "Terminal", script}}, nil
}

// OpenChrome starts Chrome or Chromium on a separate temporary profile.
func OpenChrome(p Proxy, profile string) (Argv, error) {
	home, _ := os.UserHomeDir()
	path, ok := findExecutable([]string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		filepath.Join(home, "Applications/Google Chrome.app/Contents/MacOS/Google Chrome"),
		filepath.Join(home, "Applications/Chromium.app/Contents/MacOS/Chromium"),
	}, []string{"google-chrome", "chromium"})
	if !ok {
		return Argv{}, &MissingToolError{
			Tool: "Google Chrome or Chromium",
			Hint: "install Chrome, or put its executable on PATH",
		}
	}
	args, err := ChromeArgs(p, profile)
	if err != nil {
		return Argv{}, err
	}
	return Argv{Name: path, Args: args}, nil
}

// OpenFirefox starts Firefox on a separate temporary profile.
func OpenFirefox(profile string) (Argv, error) {
	home, _ := os.UserHomeDir()
	path, ok := findExecutable([]string{
		"/Applications/Firefox.app/Contents/MacOS/firefox",
		filepath.Join(home, "Applications/Firefox.app/Contents/MacOS/firefox"),
	}, []string{"firefox"})
	if !ok {
		return Argv{}, &MissingToolError{Tool: "Firefox", Hint: "install Firefox"}
	}
	args, err := FirefoxArgs(profile)
	if err != nil {
		return Argv{}, err
	}
	return Argv{Name: path, Args: args}, nil
}

// OpenElectron starts an Electron app with Chromium's proxy flag. A .app bundle
// or an application name goes through open; an executable inside a bundle is
// run directly so arguments reach it.
func OpenElectron(app string, p Proxy) (Argv, error) {
	app = strings.TrimSpace(app)
	if app == "" {
		return Argv{}, fmt.Errorf("choose an Electron app")
	}
	args, err := ElectronArgs(p)
	if err != nil {
		return Argv{}, err
	}
	if info, err := os.Stat(app); err == nil && !info.IsDir() {
		return Argv{Name: app, Args: args}, nil
	}
	return Argv{Name: "/usr/bin/open", Args: append([]string{"-n", "-a", app, "--args"}, args...)}, nil
}

// AddSimulatorCA adds the public CA to the booted iOS simulator's keychain.
func AddSimulatorCA(caPath string) (Argv, error) {
	if _, err := os.Stat(caPath); err != nil {
		return Argv{}, fmt.Errorf("CA file: %w", err)
	}
	if _, err := toolPath("xcrun"); err != nil {
		return Argv{}, err
	}
	return Argv{Name: "xcrun", Args: []string{"simctl", "keychain", "booted", "add-root-cert", caPath}}, nil
}
