package app

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestNotificationScreenshots exports 2x Tester captures of the bell with its
// toasts, the opened center with a notice expanded, and the empty state. Set
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
				a := workspaceApp()
				a.appearance = appearance
				if scene.fill {
					a.warn("Project watch stopped reading .vscroxy")
					a.notify(notice{level: noticeError, text: "Import collection", detail: "unsupported export format",
						action: "Retry", run: func() {}})
				}
				if scene.open {
					a.openNotifications()
				}
				tt := ui.NewTester(a.view, 1200, 700)
				tt.SetLocale("en-US")
				tt.SetDark(dark)
				tt.SetScale(2)
				tt.Frame()
				tt.Frame()
				if scene.expand {
					var row ui.Rect
					for _, info := range tt.AccessAll() {
						if info.Role == ui.RoleDisclosure && strings.Contains(info.Name, "Import collection") {
							row = info.Bounds
						}
					}
					if row.W == 0 {
						t.Fatalf("notice not listed: %v", tt.Texts())
					}
					tt.ClickAt(row.X+row.W/2, row.Y+row.H/2)
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
