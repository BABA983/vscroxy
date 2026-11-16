package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Save is the Certificates editor's one primary action: it stays in view
// under the scrolling list, not below Trusted roots where a 560-point
// Settings window used to cut it off.
func TestCertificatesSaveStaysInView(t *testing.T) {
	a := workspaceApp()
	a.settingsOpen, a.settingsTab = true, 6
	tt := ui.NewTester(a.view, 760, 560)
	tt.Frame()
	tt.Frame()
	save, ok := tt.Find("Save certificates")
	if !ok || save.H == 0 || save.Y < 0 || save.Y+save.H > 560 {
		t.Fatalf("Save certificates is not in view: %+v", save)
	}
	if save.Y < 300 {
		t.Fatalf("Save certificates is in the page header, not the footer: %+v", save)
	}
}
