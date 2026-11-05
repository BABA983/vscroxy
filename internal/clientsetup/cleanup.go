package clientsetup

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

var clientDirPrefixes = []string{
	"vscroxy-chrome-",
	"vscroxy-firefox-",
	"vscroxy-terminal-",
}

// removeAll and ownedByCurrentUser are variables so tests can exercise a
// directory that cannot be removed and one owned by another user.
var (
	removeAll          = os.RemoveAll
	ownedByCurrentUser = platformOwnedByCurrentUser
)

// CleanupOldDirs removes this app's temporary profiles and terminal scripts
// older than olderThan. A directory owned by another user is left alone, and a
// failure to remove one entry does not stop the rest of the sweep.
func CleanupOldDirs(root string, olderThan time.Duration) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() || !hasClientPrefix(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) || !ownedByCurrentUser(info) {
			continue
		}
		if err := removeAll(filepath.Join(root, entry.Name())); err != nil {
			continue // a shared /tmp entry or a busy directory must not stop the sweep
		}
		removed++
	}
	return removed, nil
}

func hasClientPrefix(name string) bool {
	for _, prefix := range clientDirPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
