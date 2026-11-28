package app

import (
	"fmt"
	"image"
	"image/draw"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestParamsTableScreenshots captures the Params grid's long unfocused cell
// (an ellipsis), the same cell focused, and the grid scrolled under its
// sticky header with the Description column beside the scroll bar. Set
// VSCROXY_PARAMS_TABLE_SCREENSHOTS to an output directory.
func TestParamsTableScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_PARAMS_TABLE_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_PARAMS_TABLE_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const scale = 2
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run(appearance, func(t *testing.T) {
			a := snapshotApp()
			openDraft(a, a.flows[0])
			const long = "https://accounts.example.com/oauth2/callback?client=vscroxy-desktop&scope=openid%20profile%20email%20offline_access&state=8f14e45fceea167a5a36dedd4bea2543&redirect_uri=https%3A%2F%2Fapp.example.com%2Fauth%2Fcallback"
			var query strings.Builder
			query.WriteString("tag=first&redirect_uri=" + url.QueryEscape(long))
			query.WriteString("&locale_label=" + url.QueryEscape("中文参数值中文参数值中文参数值"))
			query.WriteString("&endpoint=" + url.QueryEscape("{{baseUrl}}/very/long/path/that/keeps/going/and/going/until/it/overflows"))
			query.WriteString("&page=1")
			for i := range 24 {
				fmt.Fprintf(&query, "&filter_%02d=%s", i, url.QueryEscape(strings.Repeat("segment/", 6)+fmt.Sprint(i)))
			}
			a.composeURL = "https://api.example.com/projects?" + query.String()
			a.composeTab = composeTabParams
			var ctx *ui.Context
			tt := ui.NewTester(func(c *ui.Context) { ctx = c; a.view(c) }, 1440, 900)
			tt.SetLocale("en-US")
			tt.SetDark(dark)
			tt.SetScale(scale)
			tt.Frame()
			for i := range a.requestDocument.queryRows {
				a.requestDocument.queryRows[i].Description = fmt.Sprintf("Shown in the request's documentation and the URL, %d", i+1)
			}
			tt.Frame()
			tt.Frame()

			pane := a.editorReqPane.Bounds(ctx)
			crop := image.Rect(int(pane.X)*scale, int(pane.Y)*scale,
				int(pane.X+pane.W)*scale, int(pane.Y+pane.H)*scale).Intersect(tt.Image().Bounds())
			save := func(name string) {
				t.Helper()
				out := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
				draw.Draw(out, out.Bounds(), tt.Image(), crop.Min, draw.Src)
				writeFeatureScreenshot(t, dir, fmt.Sprintf("params-table-%s-%s.png", name, appearance), out)
			}
			if !tt.HasText("parameter key 1") {
				t.Fatalf("the Params table is not shown: %v", tt.Texts())
			}
			save("unfocused")

			if err := clickLabel(tt, "parameter value 2"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			save("focused")

			// Type at the end of a long value, then leave it: an unfocused
			// input shows its start again, so the cut must be a prefix.
			cell, ok := tt.Find("parameter value 2")
			if !ok {
				t.Fatal("the long value is missing")
			}
			tt.ClickAt(cell.X+cell.W-6, cell.Y+cell.H/2)
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Type("edited-" + long)
			if got := a.requestDocument.queryRows[1].Value; got != "edited-"+long {
				t.Fatalf("typing did not replace the value: %q", got)
			}
			tt.Key(0, ui.KeyTab)
			if err := clickLabel(tt, "URL"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			save("typed-left")

			row, ok := tt.Find("parameter key 3")
			if !ok {
				t.Fatal("no row to scroll from")
			}
			tt.Scroll(row.X+row.W/2, row.Y+row.H/2, 0, 420)
			tt.Advance(250 * time.Millisecond)
			tt.Frame()
			save("scrolled")
		})
	}
}
