package app

import (
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// TestBodyJSONCScreenshots exports the same 2x Tester captures as the other
// screenshot tests. Set VSCROXY_JSONC_BODY_SCREENSHOTS to an output directory.
func TestBodyJSONCScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_JSONC_BODY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_JSONC_BODY_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	const (
		truncated = `{"id":1,"name":"Demo project","items":[{"sku":"A-1"},{"sku":"B-2"`
		ndjson    = "{\"event\":\"start\",\"id\":1}\n{\"event\":\"item\",\"id\":2,\"name\":\"demo\"}\n{\"event\":\"end\",\"id\":3}\n"
		valid     = `{"projects":[{"id":73,"name":"Demo project"}],"total":1}`
		plain     = "not json"
	)
	scenes := []struct {
		name        string
		body        string
		mode        int
		wantText    string
		contentType string
	}{
		{"truncated-tree", truncated, 2, "Parsing stopped at", "application/json"},
		{"truncated-pretty", truncated, 1, `  "id": 1,`, "application/json"},
		{"ndjson-tree", ndjson, 2, "Line 2", "application/x-ndjson"},
		{"ndjson-pretty", ndjson, 1, `  "event": "item",`, "application/x-ndjson"},
		{"valid-tree", valid, 2, `total`, "application/json"},
		{"valid-pretty", valid, 1, `  "projects": [`, "application/json"},
		{"plaintext-pretty", plain, 1, `not json`, "text/plain"},
	}
	for _, scene := range scenes {
		for _, dark := range []bool{false, true} {
			appearance := "light"
			if dark {
				appearance = "dark"
			}
			t.Run(scene.name+"-"+appearance, func(t *testing.T) {
				a := snapshotApp()
				a.flows[0].resBody, a.flows[0].bytes = scene.body, len(scene.body)
				a.flows[0].respHeader = http.Header{"Content-Type": {scene.contentType}}
				a.syncDetail()
				a.bodyModes(a.flows[0], 0) // initialize the per-side cache bodySideView expects
				a.bodyMode[0] = scene.mode
				tt := ui.NewTester(func(c *ui.Context) {
					th := uikit.Theme(dark)
					c.SetTheme(th)
					ui.Column(c).Fill().Background(th.Background).Padding(16).Children(func() {
						a.bodySideView(c, a.flows[0], 0)
					})
				}, 1200, 700)
				tt.SetLocale("en-US")
				tt.SetScale(2)
				tt.Frame()
				waitInspection(t, tt, func() bool { return a.jsonReader[0].ready })

				shown := ""
				if scene.mode == 2 && !tt.HasText(scene.wantText) {
					t.Fatalf("%q missing from JSON tree: %v", scene.wantText, tt.Texts())
				}
				if scene.mode == 1 {
					shown = a.bodyText[0].Buffer.String()
					if !strings.Contains(shown, scene.wantText) {
						t.Fatalf("Pretty text %q does not contain %q", shown, scene.wantText)
					}
				}
				if a.flows[0].resBody != scene.body {
					t.Fatal("the viewer changed the captured bytes")
				}
				file, err := os.Create(filepath.Join(dir, scene.name+"-"+appearance+".png"))
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
