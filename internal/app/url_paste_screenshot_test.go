package app

import (
	"os"
	"testing"
	"time"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestCurlPasteNoticeScreenshots captures the paste's notice and its "Paste
// as text" action in both themes. Set VSCROXY_URL_PASTE_SCREENSHOTS to an
// output directory.
func TestCurlPasteNoticeScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_URL_PASTE_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_URL_PASTE_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run(appearance, func(t *testing.T) {
			a := workspaceApp()
			openTestDraft(a)
			tt := ui.NewTester(a.view, 1280, 800)
			tt.SetLocale("en-US")
			tt.SetDark(dark)
			tt.SetScale(2)
			tt.Frame()
			if err := tt.Click("URL"); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.SetClipboard(`curl --request PUT 'https://api.test/v2/item?id=7' -H 'X-Trace: demo' --data-raw '{"ok":true}'`)
			tt.Key(ui.Cmd, ui.KeyV)
			tt.Frame()
			tt.Advance(150 * time.Millisecond)
			if !tt.HasText(locales.CurlPasteAsTextAction()) {
				t.Fatalf("paste action is not shown: %v", tt.Texts())
			}
			writeFeatureScreenshot(t, dir, "paste-notice-"+appearance+".png", tt.Image())
		})
	}
}
