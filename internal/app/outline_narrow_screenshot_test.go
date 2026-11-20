package app

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// TestOutlineNarrowScreenshots renders the outline beside a real body editor at
// 2x. The narrow pane keeps truncated labels and indented rows; the collapsed
// pane gives the editor its room back. Set the environment variable to an
// output directory to keep the images.
func TestOutlineNarrowScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_OUTLINE_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_OUTLINE_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const body = `{
  "shippingAddressPostalCode": "94105",
  "data": {
    "items": [
      { "id": 3, "sku": "A-1" },
      { "id": 4, "sku": "B-2" }
    ],
    "total": 2
  },
  "ok": true
}`
	scenes := []struct {
		name      string
		pane      float32
		compact   bool
		collapsed bool
	}{
		{"normal", 1164, false, false},
		{"narrow", 740, true, false},
		{"collapsed", 500, true, true},
	}
	for _, scene := range scenes {
		for _, dark := range []bool{false, true} {
			appearance := "light"
			if dark {
				appearance = "dark"
			}
			t.Run(scene.name+"-"+appearance, func(t *testing.T) {
				a := workspaceApp()
				a.flows[0].resBody, a.flows[0].bytes = body, len(body)
				a.flows[0].respHeader = http.Header{"Content-Type": {"application/json"}}
				a.syncDetail()
				a.bodyModes(a.flows[0], 0)
				a.bodyMode[0] = 1
				a.outline.open = true
				tt := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(uikit.Theme(dark))
					ui.Column(c).Fill().Padding(8).Children(func() {
						a.bodySideView(c, a.flows[0], 0)
					})
				}, int(scene.pane)+16, 540)
				tt.SetLocale("en-US")
				tt.SetDark(dark)
				tt.SetScale(2)
				tt.Frame()
				panel := a.outline.active
				if panel == nil {
					t.Fatal("no outline panel for the body")
				}
				for _, sym := range panel.all {
					if sym.branch() {
						panel.tree.Open.Add(sym.key)
					}
				}
				at := strings.Index(body, `"sku"`)
				a.bodyText[0].Select(at+1, at+1)
				tt.Frame()
				if panel.compact != scene.compact || panel.collapsed != scene.collapsed {
					t.Fatalf("pane %.0f: compact=%t collapsed=%t, want %t/%t",
						scene.pane, panel.compact, panel.collapsed, scene.compact, scene.collapsed)
				}
				writeOutlineScreenshot(t, filepath.Join(dir, "outline-"+scene.name+"-"+appearance+".png"), tt)
				if scene.name == "narrow" {
					// The row truncates the label; hovering keeps its full
					// text in a tip for sighted readers too.
					bounds, found := tt.Find("shippingAddressPostalCode")
					if !found {
						t.Fatal("no truncated label to hover")
					}
					tt.Move(bounds.X+bounds.W/2, bounds.Y+bounds.H/2)
					tt.Advance(300 * time.Millisecond)
					if _, found := tt.AccessByRole(ui.RoleTooltip, "shippingAddressPostalCode"); !found {
						t.Fatal("the truncated label has no tooltip with its full text")
					}
				}
				if scene.name == "collapsed" {
					if editor, found := tt.Find(locales.BodyText()); !found || editor.W < scene.pane-20 {
						t.Fatalf("collapsed outline still squeezes the editor: %+v, found %t", editor, found)
					}
					if err := tt.Click(locales.Outline()); err != nil {
						t.Fatal(err)
					}
					tt.Frame()
					if panel.collapsed || !panel.compact {
						t.Fatalf("restore button did not open the compact outline: %+v", panel)
					}
				}
			})
		}
	}
}

// longOutlineBody overfills the editor so the scrollbar and find bar share
// the pane with a collapsed outline's restore button.
func longOutlineBody() string {
	var b strings.Builder
	b.WriteString("{\n  \"items\": [\n")
	for i := range 90 {
		b.WriteString("    {\"id\": ")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(", \"sku\": \"SKU-")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("\"}")
		if i < 89 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("  ],\n  \"ok\": true\n}\n")
	return b.String()
}

// TestOutlineCollapsedChromeScreenshots checks the restore button against the
// editor's scrollbar and open find bar at a 2x collapsed width.
func TestOutlineCollapsedChromeScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_OUTLINE_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_OUTLINE_SCREENSHOTS to an output directory")
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
			a := workspaceApp()
			a.flows[0].resBody = longOutlineBody()
			a.flows[0].bytes = len(a.flows[0].resBody)
			a.flows[0].respHeader = http.Header{"Content-Type": {"application/json"}}
			a.syncDetail()
			a.bodyModes(a.flows[0], 0)
			a.bodyMode[0] = 1
			a.outline.open = true
			var c *ui.Context
			tt := ui.NewTester(func(ctx *ui.Context) {
				c = ctx
				ctx.SetTheme(uikit.Theme(dark))
				ui.Column(ctx).Fill().Padding(8).Children(func() { a.bodySideView(ctx, a.flows[0], 0) })
			}, 516, 540)
			tt.SetLocale("en-US")
			tt.SetDark(dark)
			tt.SetScale(2)
			tt.Frame()
			a.bodyText[0].Run(editor.Find)
			find := settleBodyFind(t, a, tt, c)
			*find.Query() = "sku"
			tt.Frame()
			button, found := tt.AccessByRole(ui.RoleButton, locales.Outline())
			t.Logf("button=%+v found=%t", button.Bounds, found)
			for _, info := range tt.AccessAll() {
				t.Logf("a11y role=%v name=%q bounds=%+v", info.Role, info.Name, info.Bounds)
			}
			writeOutlineScreenshot(t, filepath.Join(dir, "outline-collapsed-chrome-"+appearance+".png"), tt)
		})
	}
}
