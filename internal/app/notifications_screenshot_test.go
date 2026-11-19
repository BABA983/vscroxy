package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// TestNotificationScreenshots exports 2x Tester captures of the status bar
// entry, the opened list with a notice expanded, and the empty state. Set
// VSCROXY_NOTIFICATION_SCREENSHOTS to an output directory.
func TestNotificationScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_NOTIFICATION_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_NOTIFICATION_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	scenes := []struct {
		name   string
		open   bool
		expand bool
		fill   bool
	}{
		{name: "unread", fill: true},
		{name: "expanded", open: true, expand: true, fill: true},
		{name: "empty", open: true},
	}
	for _, scene := range scenes {
		for _, dark := range []bool{false, true} {
			appearance := "light"
			if dark {
				appearance = "dark"
			}
			t.Run(scene.name+"-"+appearance, func(t *testing.T) {
				a := &app{}
				if scene.fill {
					a.warn("Project watch stopped reading .vscroxy")
					a.notify(notice{level: noticeError, text: "Import collection", detail: "unsupported export format",
						action: "Retry", run: func() {}})
				}
				if scene.open {
					a.openNotifications()
				}
				th := uikit.Theme(dark)
				tt := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(th)
					ui.Column(c).Fill().Background(uikit.Colors(th).Window).Children(func() {
						a.shell(c, th)
					})
				}, 1200, 560)
				tt.SetLocale("en-US")
				tt.SetScale(2)
				tt.Frame()
				if scene.expand {
					row, ok := tt.AccessByRole(ui.RoleDisclosure, "")
					if !ok {
						t.Fatalf("notice not listed: %v", tt.Texts())
					}
					box := row.Bounds
					tt.ClickAt(box.X+box.W-20, box.Y+box.H/2)
					tt.Advance(300 * time.Millisecond)
					tt.Frame()
				}
				path := filepath.Join(dir, scene.name+"-"+appearance+".png")
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				if err := png.Encode(file, tt.Image()); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
