// Package datadir owns the root of the app's local files and the lock that
// keeps one desktop instance per root. Every path that changes with the data
// root is resolved here: the desktop, the headless CLI and the log share it.
package datadir

import (
	"cmp"
	"errors"
	"os"
	"path/filepath"
)

// Env is the environment variable that selects the data root.
const Env = "VSCROXY_HOME"

var flagDir string

// SetFlag records the --user-data-dir value, which takes precedence over the
// environment. The executables call it once before loading any app state.
func SetFlag(path string) { flagDir = path }

// Root returns the data root: --user-data-dir, then VSCROXY_HOME, then
// ~/.vscroxy. A relative path is relative to the working directory at the
// time it is resolved. An empty path in either setting is treated as unset.
func Root() (string, error) {
	path := cmp.Or(flagDir, os.Getenv(Env))
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, ".vscroxy")
	}
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(wd, path)
	}
	return filepath.Clean(path), nil
}

// Path returns name below the data root.
func Path(name string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

// ErrLocked is returned when another instance holds the data root's lock.
var ErrLocked = errors.New("another vscroxy instance is using this data folder")
