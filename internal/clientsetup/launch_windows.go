//go:build windows

package clientsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OpenTerminal opens a new cmd.exe window whose environment points at the
// proxy. Passing the environment directly avoids a batch file and its OEM code
// page, so non-ASCII user names and paths work. The window stays open at a
// prompt and only its process tree changes.
func OpenTerminal(p Proxy) (Argv, error) {
	env, err := Environment(p)
	if err != nil {
		return Argv{}, err
	}
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = "cmd.exe"
	}
	if _, err := toolPath(comspec); err != nil {
		return Argv{}, err
	}
	return Argv{
		Name:       comspec,
		Args:       []string{"/k"},
		Env:        MergeEnv(os.Environ(), env),
		NewConsole: true,
	}, nil
}

// OpenChrome starts Chrome or Chromium on a separate temporary profile.
func OpenChrome(p Proxy, profile string) (Argv, error) {
	path, ok := findExecutable(windowsChromePaths(), []string{"chrome.exe", "chromium.exe"})
	if !ok {
		return Argv{}, &MissingToolError{
			Tool: "Google Chrome or Chromium",
			Hint: "install Chrome, or put chrome.exe on PATH",
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
	path, ok := findExecutable(windowsFirefoxPaths(), []string{"firefox.exe"})
	if !ok {
		return Argv{}, &MissingToolError{Tool: "Firefox", Hint: "install Firefox"}
	}
	args, err := FirefoxArgs(profile)
	if err != nil {
		return Argv{}, err
	}
	return Argv{Name: path, Args: args}, nil
}

// OpenElectron starts an Electron app's executable with Chromium's proxy flag.
func OpenElectron(app string, p Proxy) (Argv, error) {
	app = strings.TrimSpace(app)
	if app == "" {
		return Argv{}, fmt.Errorf("choose an Electron app executable")
	}
	if info, err := os.Stat(app); err != nil || info.IsDir() {
		return Argv{}, fmt.Errorf("Electron app %q not found; choose its .exe", app)
	}
	args, err := ElectronArgs(p)
	if err != nil {
		return Argv{}, err
	}
	return Argv{Name: app, Args: args}, nil
}

func windowsChromePaths() []string {
	var paths []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		if dir := os.Getenv(env); dir != "" {
			paths = append(paths,
				filepath.Join(dir, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(dir, "Chromium", "Application", "chrome.exe"))
		}
	}
	return paths
}

func windowsFirefoxPaths() []string {
	var paths []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		if dir := os.Getenv(env); dir != "" {
			paths = append(paths, filepath.Join(dir, "Mozilla Firefox", "firefox.exe"))
		}
	}
	return paths
}
