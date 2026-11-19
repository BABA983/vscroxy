package store

import (
	"os"
	"path/filepath"
	"testing"
)

// A plausible wrong implementation lists the shared directory in readdir
// order, or matches every file whose name starts with the path hash alone.
// This checks both the newest-first order and the per-path filter.
func TestListBackupsNewestFirstAndOnlyForTheRequestedPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "one.request.json")
	other := filepath.Join(dir, "two.request.json")
	write := func(path, stamp, data string) {
		t.Helper()
		name := backupPrefix(path) + filepath.Base(path) + "." + stamp
		if err := WriteFile(filepath.Join(dir, name), []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	write(target, "20260101T090000.000000000", "oldest")
	write(target, "20260102T090000.000000000", "newest")
	write(other, "20260103T090000.000000000", "another file")
	write(target, "20260104Tnot-a-time", "not a backup")

	entries, err := ListBackups(dir, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	for i, want := range []string{"newest", "oldest"} {
		data, err := os.ReadFile(entries[i].Path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("entry %d = %q, want %q", i, data, want)
		}
	}
	if !entries[0].At.After(entries[1].At) {
		t.Fatalf("entry times are not newest first: %v", entries)
	}
	otherEntries, err := ListBackups(dir, other)
	if err != nil || len(otherEntries) != 1 {
		t.Fatalf("another path's backups = %v, %v", otherEntries, err)
	}
	missing, err := ListBackups(filepath.Join(dir, "absent"), target)
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing backup directory = %v, %v", missing, err)
	}
}
