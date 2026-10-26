//go:build linux

package appicon

import (
	"os"
	"path/filepath"
	"slices"
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

// lookup finds the icon of the app: the one its .desktop entry names, an icon
// theme's for its name, or a logo beside the executable, which is what an app
// without an entry, such as a bundled Chrome, ships.
func lookup(name, path string) (Icon, bool) {
	for _, dir := range userDirs(desktopDirs, ".local/share/applications") {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".desktop") {
				continue
			}
			icon, ok := desktopIcon(filepath.Join(dir, entry.Name()), name, path)
			if !ok {
				continue
			}
			if resolved := resolve(icon); resolved != "" {
				return read(resolved)
			}
		}
	}
	for _, candidate := range iconNames(name, path) {
		if resolved := resolve(candidate); resolved != "" {
			return read(resolved)
		}
	}
	if path != "" {
		dir := filepath.Dir(path)
		for _, candidate := range []string{"product_logo_48.png", "product_logo_128.png", "product_logo.png", "icon.png"} {
			if icon, ok := read(filepath.Join(dir, candidate)); ok {
				return icon, true
			}
		}
	}
	return Icon{}, false
}

// iconNames are the names an icon theme may hold the app's icon under.
func iconNames(name, path string) []string {
	var names []string
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(path), ".exe"))
	if base != "" {
		names = append(names, base)
	}
	if name != "" && !slices.Contains(names, strings.ToLower(name)) {
		names = append(names, strings.ToLower(name))
	}
	return names
}

// desktopIcon reads a .desktop entry and reports its Icon when its Exec or
// TryExec runs the process.
func desktopIcon(path, name, exe string) (string, bool) {
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
	if runsProcess(exec, name, exe) || runsProcess(tryExec, name, exe) {
		return icon, true
	}
	return "", false
}

// runsProcess reports whether a command line starts the process: its first
// word's name is the process name or its executable's.
func runsProcess(command, name, exe string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	word, _, _ := strings.Cut(command, " ")
	word = strings.Trim(word, `"'`)
	base := filepath.Base(word)
	if exe != "" && (word == exe || base == filepath.Base(exe)) {
		return true
	}
	return base != "" && base == name
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
			for _, theme := range []string{"hicolor", "Adwaita"} {
				if path := filepath.Join(root, theme, size, "apps", icon+".png"); exists(path) {
					return path
				}
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
