package app

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// TestOutlineScreenshots exports the outline for a JSON body and a script in
// both themes at 2x. Set the environment variable to an output directory to
// keep the images.
func TestOutlineScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_OUTLINE_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_OUTLINE_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const jsonBody = `{
  "data": {
    "items": [
      { "id": 3, "sku": "A-1" },
      { "id": 4, "sku": "B-2" }
    ],
    "total": 2
  },
  "ok": true
}`
	const script = `function buildURL(base) {
  return base + "/users";
}
describe("checkout", function () {
  it("creates an order", function () {
    pm.test("status is 201", function () {
      pm.response.to.have.status(201);
    });
    pm.test("order has an id", () => {
      pm.expect(pm.response.json().id).to.be.a("number");
    });
  });
});
const total = () => 42;`
	scenes := []struct {
		name, text string
		kind       outlineKind
		caret      string
	}{
		{"json", jsonBody, outlineJSON, `"sku"`},
		{"script", script, outlineScript, `"checkout"`},
	}
	for _, scene := range scenes {
		for _, dark := range []bool{false, true} {
			appearance := "light"
			if dark {
				appearance = "dark"
			}
			t.Run(scene.name+"-"+appearance, func(t *testing.T) {
				a := workspaceApp()
				a.outline.open = true
				buf := editor.NewBuffer(scene.text)
				view := &editor.State{Buffer: buf, ReadOnly: true, SoftWrap: true, Label: "Body"}
				target := outlineTarget{key: "screenshot:" + scene.name, kind: scene.kind}
				var panel *outlinePanel
				tt := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(uikit.Theme(dark))
					panel = a.outlineLoad(view, target)
					a.outline.active = panel
					ui.Box(c).Fill().Padding(12).Children(func() {
						ui.Row(c).Width(1156).Children(func() {
							ui.Box(c).Size(780, 560).MinWidth(0).Children(func() {
								view.Font, view.Theme = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}, uikit.EditorTheme(c.Theme())
								editor.View(c, view).Size(780, 560)
							})
							ui.Box(c).Size(360, 560).Children(func() { a.outlineSection(c, panel, view) })
						})
					})
				}, 1180, 620)
				tt.SetLocale("en-US")
				tt.SetDark(dark)
				tt.SetScale(2)
				tt.Frame()
				tt.Frame()
				if at := strings.Index(scene.text, scene.caret); at >= 0 {
					view.Select(at+1, at+1)
					tt.Frame()
				}
				for _, sym := range panel.all {
					if sym.branch() {
						panel.tree.Open.Add(sym.key)
					}
				}
				tt.Frame()
				if scene.name == "script" {
					row, found := tt.AccessByRole(ui.RoleTreeItem, "checkout")
					if !found || !row.Expanded {
						t.Fatalf("the expanded describe row is not reported expanded: %+v", row)
					}
				}
				writeOutlineScreenshot(t, filepath.Join(dir, scene.name+"-"+appearance+".png"), tt)
			})
		}
	}
}

func writeOutlineScreenshot(t *testing.T, path string, tt *ui.Tester) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
