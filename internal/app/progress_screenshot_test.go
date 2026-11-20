package app

import (
	"context"
	"image"
	"image/draw"
	"os"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestProgressScreenshots exports 2x Tester captures of a live progress notice
// in both locations and of the Settings entry that resets suppressed notices.
// Set VSCROXY_PROGRESS_SCREENSHOTS to an output directory.
func TestProgressScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_PROGRESS_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_PROGRESS_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run("running-"+appearance, func(t *testing.T) {
			a := workspaceApp()
			startedWindow, startedNotice, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			window := a.withProgress(progressOptions{title: "Window task", location: progressWindow, cancellable: true},
				func(ctx context.Context, report func(progressReport)) (string, error) {
					report(progressReport{message: "Reading file", percentage: 42})
					close(startedWindow)
					select {
					case <-release:
					case <-t.Context().Done():
						return "", t.Context().Err()
					}
					return "", nil
				})
			notice := a.withProgress(progressOptions{title: "Notification task", location: progressNotification, cancellable: true},
				func(ctx context.Context, report func(progressReport)) (string, error) {
					report(progressReport{message: "Merging requests", percentage: 65})
					close(startedNotice)
					select {
					case <-release:
					case <-t.Context().Done():
						return "", t.Context().Err()
					}
					return "", nil
				})
			awaitProgressSignal(t, startedWindow)
			awaitProgressSignal(t, startedNotice)
			if p := a.progressSnapshot(progressWindow); !p.active {
				t.Fatalf("no Window progress snapshot: %+v", a.notices)
			}
			tt := ui.NewTester(a.view, 1280, 800)
			tt.SetLocale("en-US")
			tt.SetDark(dark)
			tt.SetScale(2)
			tt.Frame()
			if !tt.HasText("Reading file") || !tt.HasText("Merging requests") || !tt.HasText(locales.ProgressPercent(42)) {
				t.Fatalf("running progress is missing: %v", tt.Texts())
			}
			writeFeatureScreenshot(t, dir, "progress-running-"+appearance+".png", tt.Image())
			close(release)
			awaitProgressSignal(t, window.done)
			awaitProgressSignal(t, notice.done)
		})
		t.Run("settings-"+appearance, func(t *testing.T) {
			a := workspaceApp()
			a.dismissedNotices = []string{updateNoticeID}
			a.settingsOpen, a.settingsTab, a.settingsChoice = true, 0, "0"
			tt := ui.NewTester(a.view, 1100, 760)
			tt.SetLocale("en-US")
			tt.SetDark(dark)
			tt.SetScale(2)
			tt.Frame()
			tt.Scroll(800, 500, 0, 700)
			tt.Frame()
			label, ok := tt.Find(locales.DismissedNotices())
			if !ok {
				t.Fatalf("the reset entry is not drawn: %v", tt.Texts())
			}
			if !tt.HasText(locales.ResetDismissedNotices()) || !tt.HasText(updateNoticeID) {
				t.Fatalf("the reset entry has no button or id: %v", tt.Texts())
			}
			full := tt.Image()
			scale := 2
			crop := image.Rect(
				int(label.X-160)*scale, int(label.Y-60)*scale,
				int(label.X+720)*scale, int(label.Y+100)*scale,
			).Intersect(full.Bounds())
			out := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
			draw.Draw(out, out.Bounds(), full, crop.Min, draw.Src)
			writeFeatureScreenshot(t, dir, "progress-settings-reset-"+appearance+".png", out)
		})
	}
}
