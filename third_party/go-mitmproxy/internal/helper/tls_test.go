package helper

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A caller can change SSLKEYLOGFILE without calling ClearTlsKeyLogWriter.
// Getting the writer for the new path must close the one for the old path.
func TestGetTlsKeyLogWriterClosesOldPathWithoutClear(t *testing.T) {
	t.Setenv("SSLKEYLOGFILE", "")
	ClearTlsKeyLogWriter()
	t.Cleanup(ClearTlsKeyLogWriter)

	dir := t.TempDir()
	first := filepath.Join(dir, "first.log")
	second := filepath.Join(dir, "second.log")
	t.Setenv("SSLKEYLOGFILE", first)
	old := GetTlsKeyLogWriter()
	if old == nil {
		t.Fatal("the writer for the first path was not opened")
	}

	t.Setenv("SSLKEYLOGFILE", second)
	current := GetTlsKeyLogWriter()
	if current == nil || current == old {
		t.Fatalf("writer for the new path = %v, want a different writer", current)
	}
	if _, err := old.Write([]byte("tls secret")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("old writer after the path changed = %v, want a closed file", err)
	}
}
