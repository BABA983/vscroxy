package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/internal/applog"
	"github.com/baba983/vscroxy/locales"
)

func TestPreviousCrashWarnsOnceAndOffersDiagnostics(t *testing.T) {
	root := t.TempDir()
	path := applog.CrashLogPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	report := "panic: boom\n"
	if err := os.WriteFile(path, []byte(report), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &app{}
	a.reportPreviousCrash(root)
	if len(a.notices) != 1 {
		t.Fatalf("%d notices, want one", len(a.notices))
	}
	n := a.notices[0]
	if n.level != noticeWarning || n.text != locales.QuitUnexpectedly() {
		t.Fatalf("notice = %+v, want the crash warning", n)
	}
	if n.action != locales.Diagnostics() || n.run == nil {
		t.Fatalf("notice action = %q; want Diagnostics with an action", n.action)
	}
	// A following launch finds the live file cleared and stays quiet.
	a.reportPreviousCrash(root)
	if len(a.notices) != 1 {
		t.Fatalf("%d notices after the second launch, want one", len(a.notices))
	}
	if data, err := os.ReadFile(applog.CrashReportPath(root)); err != nil || string(data) != report {
		t.Fatalf("kept report = %q, %v", data, err)
	}
}
