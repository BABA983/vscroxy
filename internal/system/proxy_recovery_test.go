package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoverProxyRejectsAnOlderJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.json")
	if err := os.WriteFile(path, []byte(`{"Version":1,"Target":"127.0.0.1:7890"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := recoverProxy(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported proxy recovery journal") {
		t.Fatalf("recoverProxy = %v, want an unsupported-version error", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("a rejected journal was removed: %v", statErr)
	}
}
