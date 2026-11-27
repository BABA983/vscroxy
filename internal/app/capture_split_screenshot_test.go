package app

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
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
		{"bottom", "bottom", false, false},
		{"sources-apps", "apps", false, false},
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
				} else if scene.filter == "apps" {
					a.views.closeGroup(a)
					a.query = ""
					a.sourcesApps = true
					a.clients = []domain{{name: "Acme Helper", n: 2}, {name: "Browser", n: 1}}
				} else if scene.filter == "bottom" {
					a.views.closeGroup(a)
					a.workbench.layout = "bottom"
					a.tablePercent = 56
					a.query = "api"
				} else {
					a.views.closeGroup(a)
					a.query = "api"
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
				clickRow := func(prefix string) {
					t.Helper()
					for _, info := range tt.AccessAll() {
						if info.Role == ui.RoleRow && strings.HasPrefix(info.Name, prefix) {
							tt.ClickAt(info.Bounds.X+5, info.Bounds.Y+info.Bounds.H/2)
							tt.Frame()
							return
						}
					}
					t.Fatalf("row %q is not drawn: %v", prefix, tt.Texts())
				}
				if scene.split {
					clickRow("3 500 POST") // group 1
					clickRow("4 200 GET")  // group 2, leaving group 1's marker visible
				} else {
					clickRow("2 401 POST")
				}
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
