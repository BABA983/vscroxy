package app

import (
	"os"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// TestBodyFindToggleScreenshots captures the body find bar with the case
// toggle on in both themes. Set VSCROXY_FIND_TOGGLE_SCREENSHOTS to a directory.
func TestBodyFindToggleScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_FIND_TOGGLE_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_FIND_TOGGLE_SCREENSHOTS to an output directory")
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
			a := snapshotApp()
			a.flows[0].resBody = "error Error error"
			a.bodyModes(a.flows[0], 0)
			a.bodyMode[0] = 1
			var c *ui.Context
			tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, 1440, 900)
			tt.SetLocale("en-US")
			tt.SetDark(dark)
			tt.SetScale(2)
			tt.Frame()
			a.bodyText[0].Run(editor.Find)
			find := settleBodyFind(t, a, tt, c)
			*find.Query() = "error"
			*find.CaseSensitive() = true
			tt.Frame()
			toggle, ok := tt.AccessByRole(ui.RoleToggleButton, locales.FindMatchCase())
			if !*find.CaseSensitive() || !ok || !toggle.Checked {
				t.Fatalf("case toggle is not active: %v, %+v", ok, toggle)
			}
			writeFeatureScreenshot(t, dir, "find-toggles-"+appearance+".png", tt.Image())
		})
	}
}
