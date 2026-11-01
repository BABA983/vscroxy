package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRotationKeepsTheNewestLinesInFiveFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	r := &rotating{path: path, limit: 100}
	if err := r.open(); err != nil {
		t.Fatal(err)
	}
	// 30-byte lines: three fit under the limit, the fourth rotates, so the
	// files hold 39, 36–38, 33–35, 30–32 and 27–29.
	for i := range 40 {
		if _, err := fmt.Fprintf(r, "line %02d %s\n", i, strings.Repeat("x", 21)); err != nil {
			t.Fatal(err)
		}
	}
	r.Close()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"vscroxy.log", "vscroxy.log.1", "vscroxy.log.2", "vscroxy.log.3", "vscroxy.log.4"}; !slices.Equal(names, want) {
		t.Fatalf("files = %q, want %q", names, want)
	}
	current, _ := os.ReadFile(path)
	oldest, _ := os.ReadFile(path + ".4")
	if !strings.HasPrefix(string(current), "line 39") || !strings.HasPrefix(string(oldest), "line 27") {
		t.Fatalf("current %q, oldest %q", current, oldest)
	}
	for _, name := range names {
		if info, _ := os.Stat(filepath.Join(dir, name)); info.Size() > 100 {
			t.Errorf("%s has %d bytes, over the limit", name, info.Size())
		}
	}
}
