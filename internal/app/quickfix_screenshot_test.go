package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestQuickFixScreenshots exports 2x captures of the Problems panel's
// lightbulb menu and the editor's Cmd/Ctrl+. menu in both themes. Set
// VSCROXY_QUICKFIX_SCREENSHOTS to an output directory to write them.
func TestQuickFixScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_QUICKFIX_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_QUICKFIX_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		for _, source := range []string{"panel", "editor"} {
			t.Run(appearance+"-"+source, func(t *testing.T) {
				a := workspaceApp()
				a.page = pageRequest
				a.appearance = appearance
				a.globals = map[string]string{"tenant": "acme"}
				a.addDocument(savedRequest{Name: "Quick fix", Method: "GET",
					URL: "https://api.example.com/{{tenat}}"}, "saved:quickfix")
				a.syncProblems()
				a.openProblems()
				tt := ui.NewTester(a.view, 1440, 900)
				tt.SetLocale("en-US")
				tt.SetScale(2)
				tt.Frame()
				if source == "panel" {
					if err := tt.Click("Quick Fix…"); err != nil {
						t.Fatal(err)
					}
				} else {
					a.urlInput.Focus()
					tt.Frame()
					// Put the caret inside {{tenat}}: five keystrokes back
					// from the end puts it between 't' and '}'.
					for range 3 {
						tt.Key(0, ui.KeyLeft)
						tt.Frame()
					}
					tt.Key(ui.Cmd, ui.KeyPeriod)
				}
				tt.Frame()
				if !tt.HasText("Change to “tenant”") {
					t.Fatalf("quick fixes are not shown: %v", tt.Texts())
				}
				path := filepath.Join(dir, "quickfix-"+source+"-"+appearance+".png")
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				if err := png.Encode(file, tt.Image()); err != nil {
					t.Fatal(err)
				}
				t.Logf("wrote %s", path)
			})
		}
	}
}
