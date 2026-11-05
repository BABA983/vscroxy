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

// CleanupOldDirs removes this app's temporary profiles and terminal scripts
// older than olderThan. A browser that is still running keeps a recent
// directory; a crash leaves one that the next launch can remove.
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
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return removed, err
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
