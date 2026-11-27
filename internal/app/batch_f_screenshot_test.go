package app

import (
	"image"
	"image/draw"
	"os"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestBatchFScreenshots captures batch F's request editor surfaces in both
// themes. Set VSCROXY_BATCH_F_SCREENSHOTS to an output directory. The Params
// table itself has its own opt-in test, params_table_screenshot_test.go.
func TestBatchFScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_BATCH_F_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_BATCH_F_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const scale = 2
	for _, dark := range []bool{false, true} {
		appearance := appearanceOf(dark)
		t.Run(appearance, func(t *testing.T) {
			a := snapshotApp()
			a.appearance = appearance
			openDraft(a, a.flows[0])
			d := a.requestDocument
			d.composeURL = "https://api.example.com/projects?type=json&page=1"
			d.composeHeaders = "Accept: application/json\nX-Trace: 1"
			d.headerRows, d.headerFor = nil, ""
			d.composeBody = `{"name":"Demo project"}`
			d.bodyMode = "Raw"
			d.composePre = `console.log("before")`
			d.composePost = `pm.test("ok", () => pm.response.to.have.status(200))`
			yes := true
			d.settings.FollowOriginalMethod = &yes
			d.composeTab, d.scriptTab = composeTabParams, 0

			var c *ui.Context
			tt := ui.NewTester(func(cx *ui.Context) { c = cx; a.view(cx) }, 1600, 1000)
			tt.SetLocale("en-US")
			tt.SetDark(dark)
			tt.SetScale(scale)
			tt.Frame()
			tt.Frame()

			crop := func(r image.Rectangle) *image.RGBA {
				r = r.Intersect(tt.Image().Bounds())
				if r.Empty() {
					t.Fatalf("empty crop %v of %v", r, tt.Image().Bounds())
				}
				out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
				draw.Draw(out, out.Bounds(), tt.Image(), r.Min, draw.Src)
				return out
			}
			write := func(name string, r image.Rectangle) {
				t.Helper()
				writeFeatureScreenshot(t, dir, name+"-"+appearance+".png", crop(r))
			}
			scaled := func(r ui.Rect) image.Rectangle {
				return image.Rect(int(r.X)*scale, int(r.Y)*scale,
					int(r.X+r.W)*scale, int(r.Y+r.H)*scale)
			}
			requestPane := func(height float32) image.Rectangle {
				pane := a.editorReqPane.Bounds(c)
				pane.H = min(height, pane.H)
				return scaled(pane)
			}

			write("tab-strip", requestPane(36))

			d.composeTab = composeTabSettings
			tt.Frame()
			write("settings", requestPane(1000))
			if !tt.HasText(locales.Reset()) {
				t.Fatalf("the overridden setting has no Reset: %v", tt.Texts())
			}

			d.composeTab = composeTabScripts
			tt.Frame()
			write("scripts", requestPane(1000))
			if !tt.HasText(locales.PreRequest()) {
				t.Fatalf("the Scripts list is not drawn: %v", tt.Texts())
			}

			d.composeTab = composeTabBody
			d.bodyMode = "None"
			tt.Frame()
			write("body-none", requestPane(1000))
			if !tt.HasText(locales.ThisRequestDoesNotHaveABody()) {
				t.Fatalf("the body's none state is not drawn: %v", tt.Texts())
			}

			// Dense header cells with a masked credential and one the reader
			// revealed: the ellipsis patch and both tints must cover the cell.
			d.composeTab = composeTabHeaders
			d.composeHeaders = "Authorization: header-one\nX-Key: header-two"
			d.headerFor = d.composeHeaders
			d.headerRows = []request.Field{
				{Name: "Authorization", Value: "Bearer abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz"},
				{Name: "X-Api-Key", Value: "zyxwvutsrqponmlkjihgfedcba987654", Revealed: true},
			}
			tt.Frame()
			tt.Frame()
			write("headers-secrets", requestPane(160))

			// The same compact list for a folder's inherited defaults.
			a.openFolder("Workspace/API")
			a.folderUI.tab = 4
			strict := true
			a.folderUI.settings.StrictHTTP = &strict
			tt.Frame()
			write("folder-settings", tt.Image().Bounds())
			if !tt.HasText(locales.EnableStrictHttpParser()) {
				t.Fatalf("the folder's strict parser setting is not drawn: %v", tt.Texts())
			}
			if !tt.HasText(locales.Reset()) {
				t.Fatalf("an overridden folder setting has no Reset: %v", tt.Texts())
			}
			tt.Wheel(800, 500, 0, 400)
			tt.Advance(time.Second)
			tt.Frame()
			write("folder-settings-scrolled", tt.Image().Bounds())
			a.folderUI.open = false
			tt.Frame()

			a.page, a.sidebarChoice = pageSearch, "search"
			a.searchAll.input, a.searchAll.results = "", nil
			a.searchAll.matchCase = true
			tt.Frame()
			write("search-empty", tt.Image().Bounds())

			a.page, a.sidebarChoice = pageRequest, "request:"+a.requests[0].key()
			a.showRequestWorkspace()
			d.composeTab, d.response = composeTabParams, nil
			d.composeReplies, d.console, d.sendOutcome = nil, nil, ""
			tt.Frame()
			write("response-empty", scaled(a.editorRespPane.Bounds(c)))

			write("footer", image.Rect(0, tt.Image().Bounds().Dy()-uikit.HeightFooter*scale,
				tt.Image().Bounds().Dx(), tt.Image().Bounds().Dy()))
			if proxy, ok := tt.Find(locales.ProxyStatus()); ok {
				tt.Move(proxy.X+proxy.W/2, proxy.Y+proxy.H/2)
				tt.Frame()
				write("footer-hover", image.Rect(0, tt.Image().Bounds().Dy()-uikit.HeightFooter*scale,
					tt.Image().Bounds().Dx(), tt.Image().Bounds().Dy()))
				tt.Move(0, 0)
				tt.Frame()
			}

			// A document tab's menu, with one item under the pointer.
			openCleanTab(a, "Keep", "https://example.test/keep")
			openCleanTab(a, "Last", "https://example.test/last")
			tt.Frame()
			tab, ok := tt.AccessByRole(ui.RoleButton, "Keep")
			if !ok {
				t.Fatalf("the Keep tab is not drawn: %v", tt.Texts())
			}
			tt.RightClickAt(tab.Bounds.X+20, tab.Bounds.Y+tab.Bounds.H/2)
			tt.Frame()
			item := menuItemBounds(t, tt, locales.CloseOtherTabs())
			tt.Move(item.X+item.W/2, item.Y+item.H/2)
			tt.Frame()
			write("tab-menu", tt.Image().Bounds())
			tt.Key(0, ui.KeyEscape)
			tt.Frame()

			// The collection tree's request menu, with one item under the pointer.
			a.openSaved(a.requests[0].key())
			tt.Frame()
			row, ok := tt.Find("List projects")
			if !ok {
				t.Fatalf("the saved request is not in the tree: %v", tt.Texts())
			}
			tt.RightClickAt(row.X+12, row.Y+row.H/2)
			tt.Frame()
			item = menuItemBounds(t, tt, locales.Open())
			tt.Move(item.X+item.W/2, item.Y+item.H/2)
			tt.Frame()
			write("tree-menu", tt.Image().Bounds())
		})
	}
}

// menuItemBounds is the box of a menu item drawn in the window, for a capture
// that wants the pointer on it.
func menuItemBounds(t *testing.T, tt *ui.Tester, label string) ui.Rect {
	t.Helper()
	item, ok := tt.Find(label)
	if !ok {
		t.Fatalf("menu item %q is not drawn: %v", label, tt.Menu())
	}
	return item
}
