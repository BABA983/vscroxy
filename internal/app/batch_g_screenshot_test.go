package app

import (
	"image"
	"image/draw"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestBatchGScreenshots captures batch G's surfaces in both themes: the number
// fields, the sidebar's Search mode, the Console, and the response bar's 200
// and 4xx states. Set VSCROXY_BATCH_G_SCREENSHOTS to an output directory.
func TestBatchGScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_BATCH_G_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_BATCH_G_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const scale = 2
	for _, dark := range []bool{false, true} {
		appearance := appearanceOf(dark)
		t.Run(appearance, func(t *testing.T) {
			var c *ui.Context
			crop := func(img *image.RGBA, r image.Rectangle) *image.RGBA {
				r = r.Intersect(img.Bounds())
				if r.Empty() {
					t.Fatalf("empty crop %v of %v", r, img.Bounds())
				}
				out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
				draw.Draw(out, out.Bounds(), img, r.Min, draw.Src)
				return out
			}
			scaled := func(r ui.Rect) image.Rectangle {
				return image.Rect(int(r.X)*scale, int(r.Y)*scale,
					int(r.X+r.W)*scale, int(r.Y+r.H)*scale)
			}
			write := func(name string, img *image.RGBA, r image.Rectangle) {
				t.Helper()
				writeFeatureScreenshot(t, dir, name+"-"+appearance+".png", crop(img, r))
			}
			tester := func(a *app, width, height float32) (*ui.Tester, *ui.Context) {
				t.Helper()
				a.appearance = appearance
				var c *ui.Context
				tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, int(width), int(height))
				tt.SetLocale("en-US")
				tt.SetDark(dark)
				tt.SetScale(scale)
				tt.Frame()
				tt.Frame()
				return tt, c
			}

			// 1. Settings' number fields: a valid redirect limit and an
			// out-of-range timeout, which keeps its invalid face.
			a := snapshotApp()
			openDraft(a, a.flows[0])
			d := a.requestDocument
			d.composeTab = composeTabSettings
			d.maxRedirects, d.maxRedirectsText = 5, "5"
			d.timeout, d.timeoutText = 30, "9999"
			tt, _ := tester(a, 1600, 1100)
			write("settings-number-fields", tt.Image(), tt.Image().Bounds())
			if !tt.HasText(locales.MaximumRedirects()) || !tt.HasText(locales.Hops()) {
				t.Fatalf("the settings number fields are not drawn: %v", tt.Texts())
			}

			// 2. Search in the sidebar: its empty state, its tree with one
			// folded resource, and Replace open over a hovered match.
			empty := globalSearchTestApp()
			empty.sidebarMode = sidebarSearch
			tt, _ = tester(empty, 1400, 900)
			sidebar := image.Rect(0, 0, 300*scale, 900*scale)
			write("search-empty", tt.Image(), sidebar)

			results := searchScreenshotApp()
			t.Cleanup(results.stopSearch)
			tt, c = tester(results, 1400, 900)
			if err := tt.Click("Findme Local"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			write("search-results", tt.Image(), sidebar)
			if !tt.HasText("Findme Local") {
				t.Fatalf("the collapsed resource is not shown: %v", tt.Texts())
			}

			replace := searchScreenshotApp()
			t.Cleanup(replace.stopSearch)
			replace.searchAll.replace.open = true
			replace.searchAll.replace.input = "found"
			replace.searchAll.collapsedResources = map[string]bool{searchResultKey(searchEnvironments, "Findme Local"): true}
			tt, _ = tester(replace, 1400, 900)
			if row, ok := tt.Find(locales.OpenSearchResult("Workspace/Orders / Findme Request")); ok {
				tt.Move(row.X+row.W/2, row.Y+row.H/2)
			}
			tt.Frame()
			write("search-replace-hover", tt.Image(), sidebar)
			if !tt.HasText(locales.ReplaceWith()) {
				t.Fatalf("the replace field is not drawn: %v", tt.Texts())
			}

			// 3. The Console with one expanded request, its script lines at
			// the levels they came in, and the level filter menu open.
			a = snapshotApp()
			openDraft(a, a.flows[0])
			d = a.requestDocument
			d.sent = savedRequest{Method: "GET", URL: "https://postman-echo.com/bytes/1200/kb?type=json"}
			d.sentAt = time.Date(2026, 11, 27, 9, 52, 53, 0, time.Local)
			d.sentSet = true
			flow := &capture.Flow{Status: 200, Millis: 1628, Bytes: 746609, RequestBodySize: 1200, ResponseBodySize: 746609,
				RequestHeader:  http.Header{"Accept": {"application/json"}, "X-Trace": {"1"}},
				ResponseHeader: http.Header{"Content-Type": {"application/json"}, "Server": {"nginx"}},
				ResponseBody:   `{"bytes":1187}`}
			d.response, d.responseTab, d.responseView = flow, resViewBody, 0
			a.consoleSend(d, requestResult{document: d, outcome: "Response received", response: flow,
				console: []request.ConsoleMessage{
					{Phase: "pre-request", Kind: "log", Text: `"before"`},
					{Phase: "pre-request", Kind: "warning", Text: "Deprecated parameter type=json"},
					{Phase: "post-response", Kind: "log", Text: `{"id": 73, "name": "Demo project"}`},
					{Phase: "post-response", Kind: "error", Text: "TypeError: cannot read properties of undefined (reading 'id')"},
				}})
			a.requestConsole.open = true
			a.requestConsole.split = 62
			for _, entry := range a.requestConsole.entries {
				if entry.request {
					a.requestConsole.expanded = map[int]bool{entry.id: true}
				}
			}
			tt, c = tester(a, 1600, 1050)
			write("console", tt.Image(), tt.Image().Bounds())
			if err := tt.Click(locales.AllLogs()); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			write("console-filter", tt.Image(), tt.Image().Bounds())
			if !tt.HasText(locales.LogLevelWarning()) || !tt.HasText(locales.LogLevelError()) {
				t.Fatalf("the level menu is not drawn: %v", tt.Texts())
			}

			// 4. The response bar with a success and a client error. The
			// crop keeps the view switcher, tabs, numbers, history and ⋯.
			a = snapshotApp()
			openDraft(a, a.flows[0])
			d = a.requestDocument
			setResponse := func(status int, body string) {
				d.response = &capture.Flow{Status: status, Millis: 1630, Bytes: 746609, ResponseBodySize: 746609,
					ResponseHeader: http.Header{"Content-Type": {"application/json"}}, ResponseBody: body}
				d.responseArrived()
				d.responseTab, d.responseView = resViewBody, 0
			}
			setResponse(200, `{"projects":[{"id":73,"name":"Demo project"}],"total":1}`)
			tt, c = tester(a, 1600, 1000)
			write("response-bar-200", tt.Image(), scaled(a.editorRespPane.Bounds(c)))
			setResponse(404, `{"error":"Not found"}`)
			d.responseView, d.responseEditor = 1, nil
			for range 10 {
				tt.Frame()
			}
			write("response-bar-404", tt.Image(), scaled(a.editorRespPane.Bounds(c)))
			if !tt.HasText("404 Not Found") {
				t.Fatalf("the 4xx response bar is not drawn: %v", tt.Texts())
			}
		})
	}
}
