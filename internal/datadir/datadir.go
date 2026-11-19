// Package datadir owns the root of the app's local files, the key that keeps
// one desktop instance per root and the lock that guards that root. Every
// path that changes with the data root is resolved here: the desktop, the
// headless CLI and the log share it.
package datadir

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Env is the environment variable that selects the data root.
const Env = "VSCROXY_HOME"

var (
	rootMu       sync.Mutex
	flagDir      string
	cachedRoot   string
	rootResolved bool
)

// SetFlag records the --user-data-dir value, which takes precedence over the
// environment. The executables call it once before loading any app state. It
// resolves a relative path now, before an app can change the working
// directory, and caches the result for the rest of the process.
func SetFlag(path string) {
	rootMu.Lock()
	defer rootMu.Unlock()
	flagDir = path
	cachedRoot, rootResolved = "", false
	if path == "" {
		return
	}
	if absolute, err := absolutePath(path); err == nil {
		cachedRoot, rootResolved = absolute, true
	}
}

// Reset clears the flag and the cached root. It is for tests that start a new
// process's worth of configuration in the same test binary.
func Reset() {
	SetFlag("")
}

// Root returns the data root: --user-data-dir, then VSCROXY_HOME, then
// ~/.vscroxy. A relative path is relative to the working directory when the
// setting is first resolved. The answer is cached, so a later chdir cannot
// move files that were already opened to a different root.
func Root() (string, error) {
	rootMu.Lock()
	defer rootMu.Unlock()
	if rootResolved {
		return cachedRoot, nil
	}
	path := cmp.Or(flagDir, os.Getenv(Env))
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, ".vscroxy")
	}
	absolute, err := absolutePath(path)
	if err != nil {
		return "", err
	}
	cachedRoot, rootResolved = absolute, true
	return absolute, nil
}

func absolutePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, path), nil
}

// Path returns name below the data root.
func Path(name string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

// InstanceKey identifies the root for MyGo's single-instance socket. The
// resolved path keeps a symlink and its target together; lowercasing on
// Windows and macOS keeps case variants of one folder together there.
func InstanceKey(root string) string {
	canonical := canonicalRoot(root)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		canonical = strings.ToLower(canonical)
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:6])
}

// canonicalRoot resolves symlinks even on the first launch, when the data
// root does not exist yet: resolve its nearest existing parent and append the
// rest. Otherwise the first and second launch would compute different keys.
func canonicalRoot(root string) string {
	var rest []string
	for path := root; ; {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, rest[i])
			}
			return resolved
		}
		parent := filepath.Dir(path)
		if parent == path {
			return root
		}
		rest = append(rest, filepath.Base(path))
		path = parent
	}
}

// FlagValue reads VS Code's --user-data-dir=<path> spelling.
func FlagValue(arg string) (string, bool) {
	for _, prefix := range []string{"--user-data-dir=", "-user-data-dir="} {
		if value, ok := strings.CutPrefix(arg, prefix); ok {
			return value, true
		}
	}
	return "", false
}

// ErrLocked is returned when another instance holds the data root's lock.
var ErrLocked = errors.New("another vscroxy instance is using this data folder")
