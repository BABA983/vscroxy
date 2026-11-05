package clientsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveBinary follows launcher symlinks so a snap's real path can be
// recognized. A path that cannot be resolved is returned unchanged.
func resolveBinary(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
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
