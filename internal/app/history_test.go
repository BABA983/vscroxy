package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestLegacyHistoryRemainsReadOnlyAndOpensAnUnsentCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	data := []byte(`[{"method":"POST","url":"https://example.test/x","draft":{"name":"Old request","method":"POST","url":"https://example.test/x","body":"old body"}}]`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	a := workspaceApp()
	var err error
	a.history, err = loadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	a.showRunRecords()
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("POST example.test/x"); err != nil {
		t.Fatal(err)
	}
	if a.composeURL != "https://example.test/x" || a.composeBody != "old body" || a.composeWaiting || a.assetID != "" {
		t.Fatal("legacy history did not open as an unsent draft")
	}
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != string(data) {
		t.Fatal("legacy history was rewritten")
	}
}
