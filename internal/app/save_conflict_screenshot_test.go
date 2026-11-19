package app

import (
	"errors"
	"os"
	"testing"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// TestSaveConflictAndHistoryScreenshots captures 2x Light and Dark views of
// the conflict notice, the editable comparison and the local history list.
// Set VSCROXY_CONFLICT_HISTORY_SCREENSHOTS to an output directory.
func TestSaveConflictAndHistoryScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_CONFLICT_HISTORY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_CONFLICT_HISTORY_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		theme := uikit.Theme(dark)
		view := func(a *app) func(*ui.Context) {
			return func(c *ui.Context) {
				c.SetTheme(theme)
				a.view(c)
			}
		}
		t.Run("conflict-notice-"+appearance, func(t *testing.T) {
			a, _, _, _ := saveConflictFixture(t)
			a.openNotifications()
			notice := a.notices[len(a.notices)-1]
			a.noticeUI.expanded = map[uint64]*bool{notice.id: new(true)}
			tt := ui.NewTester(view(a), 1300, 820)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			writeFeatureScreenshot(t, dir, "conflict-notice-"+appearance+".png", tt.Image())
		})
		t.Run("compare-"+appearance, func(t *testing.T) {
			a, _, _, saveErr := saveConflictFixture(t)
			var conflict *libraryConflictError
			if !errors.As(saveErr, &conflict) {
				t.Fatalf("fixture did not return a conflict: %v", saveErr)
			}
			a.reviewSaveConflict(conflict)
			tt := ui.NewTester(view(a), 1300, 820)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			if !tt.HasText("On disk") || !tt.HasText("Your draft") {
				t.Fatalf("compare view is missing its sides: %v", tt.Texts())
			}
			writeFeatureScreenshot(t, dir, "compare-"+appearance+".png", tt.Image())
		})
		t.Run("history-"+appearance, func(t *testing.T) {
			a, _, key := historyFixture(t)
			a.openRequestHistory(key)
			tt := ui.NewTester(view(a), 1300, 820)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			if a.history.diff == nil {
				t.Fatalf("history did not show a diff: %v", tt.Texts())
			}
			writeFeatureScreenshot(t, dir, "history-"+appearance+".png", tt.Image())
		})
	}
}
