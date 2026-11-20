package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestCaptureSplitScreenshots exports 2x Tester captures of a single capture
// view, two groups with different filters, and a narrow window, in both
// themes. Set VSCROXY_CAPTURE_SPLIT_SCREENSHOTS to an absolute directory.
func TestCaptureSplitScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_CAPTURE_SPLIT_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_CAPTURE_SPLIT_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	scenes := []struct {
		name, filter  string
		split, narrow bool
	}{
		{"single", "", false, false},
		{"two-filters", "split", true, false},
		{"narrow", "narrow", true, true},
	}
	for _, scene := range scenes {
		for _, dark := range []bool{false, true} {
			appearance := "light"
			if dark {
				appearance = "dark"
			}
			t.Run(scene.name+"-"+appearance, func(t *testing.T) {
				a := splitTestApp(t)
				a.appearance = appearance
				if scene.split {
					groupFilter(a, 0, "errors", "")
					groupFilter(a, 1, "host:other.test", "")
				} else {
					a.views.closeGroup(a)
					a.query = "api"
				}
				if scene.split {
					for group, id := range map[int]int{0: 3, 1: 4} {
						a.views.focusCaptureGroup(a, group)
						a.pickedID = id
						a.restoreSelection()
						a.views.captureGroupFields(a, a.views.activeView())
					}
				} else {
					a.pickedID = 2
					a.restoreSelection()
					a.views.capture(a)
				}
				width, height := 1500, 900
				if scene.narrow {
					width, height = 980, 760
				}
				tt := ui.NewTester(a.view, width, height)
				tt.SetLocale("en-US")
				tt.SetDark(dark)
				tt.SetScale(2)
				tt.Frame()
				tt.Frame()
				if scene.split {
					for _, label := range []string{locales.CaptureGroup(1), locales.CaptureGroup(2)} {
						if !tt.HasText(label) {
							t.Fatalf("group header %q is not drawn: %v", label, tt.Texts())
						}
					}
				}
				path := filepath.Join(dir, "capture-"+scene.name+"-"+appearance+".png")
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
