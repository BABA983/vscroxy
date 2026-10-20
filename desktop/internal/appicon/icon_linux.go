//go:build linux

package appicon

import (
	"os"
	"path/filepath"
	"strings"
)

// desktopDirs are where an application's .desktop entry may live.
var desktopDirs = []string{
	"/usr/share/applications",
	"/usr/local/share/applications",
	"/var/lib/flatpak/exports/share/applications",
}

// iconRoots are the icon themes searched, most specific first.
var iconRoots = []string{
	"/usr/share/icons",
	"/usr/local/share/icons",
}

// sizes are tried in the order an icon looks best at a small size.
var sizes = []string{"128x128", "64x64", "96x96", "48x48", "256x256", "32x32", "512x512"}

// lookup matches the process to a .desktop entry by its executable, then
// resolves the entry's Icon to a file.
func lookup(process string) string {
	for _, dir := range userDirs(desktopDirs, ".local/share/applications") {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".desktop") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			icon, ok := desktopIcon(path, process)
			if !ok {
				continue
			}
			if resolved := resolve(icon); resolved != "" {
				return resolved
			}
		}
	}
	return ""
}

// desktopIcon reads a .desktop entry and reports its Icon when its Exec or
// TryExec runs the process.
func desktopIcon(path, process string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var icon, exec, tryExec string
	inEntry := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"
			continue
		}
		if !inEntry {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "Icon":
			icon = value
		case "Exec":
			exec = value
		case "TryExec":
			tryExec = value
		}
	}
	if icon == "" {
		return "", false
	}
	if runsProcess(exec, process) || runsProcess(tryExec, process) {
		return icon, true
	}
	return "", false
}

// runsProcess reports whether a command line starts the process: its first
// word's name is the process name.
func runsProcess(command, process string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	word, _, _ := strings.Cut(command, " ")
	word = strings.Trim(word, `"'`)
	return filepath.Base(word) == process
}

// resolve turns an Icon value into a file: an absolute path as it is, or a
// name looked up in the icon themes, then in the pixmaps.
func resolve(icon string) string {
	if strings.HasPrefix(icon, "/") {
		if exists(icon) {
			return icon
		}
		return ""
	}
	for _, root := range userDirs(iconRoots, ".local/share/icons") {
		for _, size := range sizes {
			if path := filepath.Join(root, "hicolor", size, "apps", icon+".png"); exists(path) {
				return path
			}
			if path := filepath.Join(root, "Adwaita", size, "apps", icon+".png"); exists(path) {
				return path
			}
		}
		for _, theme := range []string{"hicolor", "Adwaita"} {
			if path := filepath.Join(root, theme, "scalable", "apps", icon+".svg"); exists(path) {
				return path
			}
		}
	}
	if path := "/usr/share/pixmaps/" + icon + ".png"; exists(path) {
		return path
	}
	return ""
}

// userDirs puts the same directories under the user's home in front.
func userDirs(dirs []string, suffix string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return dirs
	}
	return append([]string{filepath.Join(home, suffix)}, dirs...)
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
