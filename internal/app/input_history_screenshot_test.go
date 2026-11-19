package app

import (
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestInputHistoryScreenshots captures the request URL bar with a recalled
// entry, in both themes at 2x. Set VSCROXY_INPUT_HISTORY_SCREENSHOTS to an
// absolute output directory.
func TestInputHistoryScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_INPUT_HISTORY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_INPUT_HISTORY_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const recalled = "https://api.example.com/v1/projects?cursor=42"
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run(appearance, func(t *testing.T) {
			a := workspaceApp()
			d := openTestDraft(a)
			a.appearance = appearance
			d.composeURL = ""
			a.urlHistory.add(recalled)
			tt := ui.NewTester(a.view, 1280, 720)
			tt.SetLocale("en-US")
			tt.SetDark(dark)
			tt.SetScale(2)
			tt.Frame()
			if err := tt.Click("URL"); err != nil {
				t.Fatal(err)
			}
			tt.Key(0, ui.KeyUp)
			tt.Frame()
			if d.composeURL != recalled {
				t.Fatalf("URL bar = %q, want the recalled entry", d.composeURL)
			}
			writeFeatureScreenshot(t, dir, "url-history-recalled-"+appearance+".png", tt.Image())
		})
	}
}
