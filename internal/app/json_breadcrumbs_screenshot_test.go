package app

import (
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestBreadcrumbScreenshots exports the opt-in JSON path bar at 2x in both
// themes, with and without a sibling menu open. Set the environment variable
// to an output directory to keep the images.
func TestBreadcrumbScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_BREADCRUMB_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_BREADCRUMB_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const body = `{"data":{"items":[{"id":3,"sku":"A-1"},{"id":4,"sku":"B-2"}],"total":2},"ok":true}`
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		for _, menu := range []bool{false, true} {
			name := "path"
			if menu {
				name = "menu"
			}
			t.Run(name+"-"+appearance, func(t *testing.T) {
				a := snapshotApp()
				a.showJSONPath = true
				a.flows[0].resBody, a.flows[0].bytes = body, len(body)
				a.flows[0].respHeader = http.Header{"Content-Type": {"application/json"}}
				a.syncDetail()
				a.bodyModes(a.flows[0], 0)
				a.bodyMode[0] = 1
				view := func(c *ui.Context) {
					th := uikit.Theme(dark)
					c.SetTheme(th)
					ui.Column(c).Fill().Background(th.Background).Padding(16).Children(func() {
						a.bodySideView(c, a.flows[0], 0)
					})
				}
				tt := ui.NewTester(view, 1100, 640)
				tt.SetLocale("en-US")
				tt.SetScale(2)
				tt.Frame()
				text := a.bodyText[0].Buffer.String()
				pos := strings.Index(text, `"id"`)
				if pos < 0 {
					t.Fatalf("no key in %q", text)
				}
				a.bodyText[0].Select(pos+3, pos+3)
				tt.Frame()
				if menu {
					if err := tt.Click(locales.JsonBreadcrumbSiblingFor("[0]")); err != nil {
						t.Fatal(err)
					}
					tt.Frame()
				}
				file, err := os.Create(filepath.Join(dir, name+"-"+appearance+".png"))
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
