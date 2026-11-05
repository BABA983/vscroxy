package clientsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const snapBinDir = "/snap/bin"

// resolveBinary follows launcher symlinks so a snap's real path can be
// recognized. A path that cannot be resolved is returned unchanged.
func resolveBinary(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// browserBinary prefers a snap launcher when a distribution's /usr/bin wrapper
// is a shell script that execs /snap/bin/<name>. Ubuntu 22.04+ ships such
// scripts for Firefox and chromium-browser; they are not symlinks.
func browserBinary(path string) string {
	if launcher, ok := snapLauncher(path); ok {
		return launcher
	}
	return path
}

func snapLauncher(path string) (string, bool) {
	return snapLauncherAt(path, snapBinDir)
}

// snapLauncherAt recognizes direct snap paths, wrapper symlinks and small
// shell wrappers. binDir is injectable so a regression test can use a fake
// wrapper tree without writing to /snap.
func snapLauncherAt(path, binDir string) (string, bool) {
	if path == "" || binDir == "" {
		return "", false
	}
	root := filepath.Dir(binDir)
	underSnap := func(candidate string) bool {
		return candidate == root || strings.HasPrefix(candidate, root+string(filepath.Separator))
	}
	var candidates []string
	if underSnap(path) {
		candidates = append(candidates, path)
	}
	if target, err := os.Readlink(path); err == nil {
		candidates = append(candidates, target)
	}
	if data := readWrapper(path); data != nil {
		for _, field := range strings.Fields(string(data)) {
			field = strings.Trim(field, "\"'`;")
			if strings.HasPrefix(field, binDir+string(filepath.Separator)) {
				candidates = append(candidates, field)
			}
		}
	}
	base := filepath.Base(path)
	for _, name := range []string{base, strings.TrimSuffix(base, "-browser")} {
		if name != "" {
			candidates = append(candidates, filepath.Join(binDir, name))
		}
	}
	for _, candidate := range candidates {
		if !underSnap(candidate) {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

// readWrapper reads only small regular files; a real browser binary is much
// larger and is never read into memory just to look for a launcher path.
func readWrapper(path string) []byte {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// BrowserProfile creates a temporary profile for Chrome or Firefox. Snap
// packages have a private /tmp, so a profile in the system temp directory is
// invisible to them; those profiles live under ~/snap/<name>/common instead.
func BrowserProfile(kind string) (string, error) {
	var (
		binary string
		err    error
	)
	switch kind {
	case "chrome":
		binary, err = chromeBinary()
	case "firefox":
		binary, err = firefoxBinary()
	default:
		return "", fmt.Errorf("unknown browser %q", kind)
	}
	if err != nil {
		return "", err
	}
	if root, ok := snapProfileRoot(binary, os.Getenv("HOME")); ok {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return "", fmt.Errorf("snap profile directory %s: %w", root, err)
		}
		return os.MkdirTemp(root, "vscroxy-"+kind+"-*")
	}
	return os.MkdirTemp("", "vscroxy-"+kind+"-*")
}

// snapProfileRoot maps a snap binary to the per-snap shared directory. A
// /snap/bin wrapper that could not be resolved falls back to its file name.
func snapProfileRoot(binary, home string) (string, bool) {
	if home == "" {
		return "", false
	}
	// A /snap/bin wrapper is a symlink to /usr/bin/snap. Keep the wrapper for
	// launching, but recognize it, a one-level wrapper symlink and an
	// already-resolved snap binary.
	candidates := []string{binary}
	if launcher, ok := snapLauncher(binary); ok {
		candidates = append(candidates, launcher)
	}
	if target, err := os.Readlink(binary); err == nil {
		candidates = append(candidates, target)
		if target == "/usr/bin/snap" {
			name := strings.TrimSuffix(filepath.Base(binary), "-browser")
			if name != "" {
				return filepath.Join(home, "snap", name, "common"), true
			}
		}
	}
	candidates = append(candidates, resolveBinary(binary))
	for _, candidate := range candidates {
		if !strings.HasPrefix(candidate, "/snap/") {
			continue
		}
		rest := strings.TrimPrefix(candidate, "/snap/")
		name, _, _ := strings.Cut(rest, "/")
		if name == "bin" {
			name = filepath.Base(candidate)
		}
		if name != "" {
			return filepath.Join(home, "snap", name, "common"), true
		}
	}
	return "", false
}
