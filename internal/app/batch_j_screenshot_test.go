package app

import (
	"image"
	"image/draw"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/markers"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestBatchJScreenshots captures batch J's surfaces in both themes: a
// right-click menu at the pointer with its row still marked, the command
// palette with one and with many results, the shared bottom panel on Problems
// and on Console, and the right sidebar's Runs and Examples panels. Set
// VSCROXY_BATCH_J_SCREENSHOTS to an output directory.
func TestBatchJScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_BATCH_J_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_BATCH_J_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const scale = 2
	for _, dark := range []bool{false, true} {
		appearance := appearanceOf(dark)
		t.Run(appearance, func(t *testing.T) {
			crop := func(img *image.RGBA, r image.Rectangle) *image.RGBA {
				r = r.Intersect(img.Bounds())
				if r.Empty() {
					t.Fatalf("empty crop %v of %v", r, img.Bounds())
				}
				out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
				draw.Draw(out, out.Bounds(), img, r.Min, draw.Src)
				return out
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

			// 1. The collections tree's right-click menu: its top-left sits at
			// the pointer, and the row it acts on stays marked after the
			// pointer leaves it for the menu.
			a := workspaceApp()
			a.sidebarMode = sidebarCollections
			a.requests = append(a.requests, savedRequest{Name: "Second", Collection: "Workspace/API", Method: "POST", URL: "https://api.example.com/second"})
			tt, _ := tester(a, 1600, 1000)
			if err := tt.Click("List projects"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			row, ok := tt.Find("Second")
			if !ok {
				t.Fatalf("the second request is not in the tree: %v", tt.Texts())
			}
			x, y := row.X+row.W/2, row.Y+row.H/2
			tt.RightClickAt(x, y)
			tt.Frame()
			item, ok := tt.Find(locales.Open())
			if !ok {
				t.Fatalf("the tree menu did not open: %v", tt.Menu())
			}
			if item.X < x-1 || item.X > x+8 || item.Y < y-1 || item.Y > y+8 {
				t.Fatalf("the tree menu's first item %v did not open at the pointer (%g, %g)", item, x, y)
			}
			if a.requestChoice != a.requests[1].key() {
				t.Fatalf("the right-clicked row is not the marked one: %q", a.requestChoice)
			}
			tt.Move(item.X+item.W/2, item.Y+item.H/2)
			tt.Frame()
			writeFeatureScreenshot(t, dir, "menu-at-pointer-"+appearance+".png", tt.Image())
			tt.Key(0, ui.KeyEscape)
			tt.Frame()

			// 2. The command palette: a single result keeps the panel short,
			// many results grow it to half the window and scroll inside.
			tt.Key(ui.Cmd, ui.KeyK)
			tt.Type("Toggle right sidebar")
			tt.Frame()
			panel := image.Rect(0, 0, tt.Image().Bounds().Dx(), 620*scale)
			writeFeatureScreenshot(t, dir, "palette-one-"+appearance+".png", crop(tt.Image(), panel))
			if !tt.HasText(locales.ToggleRightSidebar()) {
				t.Fatalf("the one-result palette is missing its row: %v", tt.Texts())
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Type("request")
			tt.Frame()
			writeFeatureScreenshot(t, dir, "palette-many-"+appearance+".png", crop(tt.Image(), panel))
			tt.Key(0, ui.KeyEscape)
			tt.Frame()

			// 3. One bottom panel with Problems and Console as its tabs. The
			// Problems tab carries the search field and the severity filter;
			// hiding Error puts the dot on the filter button.
			p := workspaceApp()
			p.page = pageRequest
			p.appearance = appearance
			for i, name := range []string{"List projects", "Create order"} {
				key := []string{"saved:problem-1", "saved:problem-2"}[i]
				p.addDocument(savedRequest{Name: name, Method: "GET", URL: "https://api.example.com/items"}, key)
				d := p.requestDocument
				d.sentSet, d.sent = true, d.draft()
			}
			first, second := p.documents[0], p.documents[1]
			p.markers.ChangeOne(problemOwnerAssertions, problemResource(first), []markers.Marker{
				{Severity: markers.Error, Message: locales.ProblemCheckFailed("status 200"), Source: "status 200", Code: problemFieldChecks},
				{Severity: markers.Error, Message: "The body has no id yet", Code: problemFieldBody},
				{Severity: markers.Hint, Message: "This check also accepts 201", Code: problemFieldChecks},
			})
			p.markers.ChangeOne(problemOwnerVariables, problemResource(first), []markers.Marker{
				{Severity: markers.Warning, Message: locales.ProblemUnresolvedVariable("tenant"), Code: problemFieldURL,
					Range: &markers.Range{StartLineNumber: 1, StartColumn: 9, EndLineNumber: 1, EndColumn: 17}},
			})
			p.markers.ChangeOne(problemOwnerScripts, problemResource(second), []markers.Marker{
				{Severity: markers.Info, Message: locales.ProblemScriptFailed("deprecated pm API"), Code: problemFieldPost,
					Range: &markers.Range{StartLineNumber: 2, StartColumn: 3, EndLineNumber: 2, EndColumn: 18}},
			})
			first.response = &capture.Flow{Status: 200, Millis: 32, Bytes: 64, ResponseBody: `{"items":[]}`,
				ResponseHeader: http.Header{"Content-Type": {"application/json"}}}
			p.consoleSend(first, requestResult{document: first, outcome: "Response received", response: first.response,
				console: []request.ConsoleMessage{{Phase: "post-response", Kind: "warning", Text: "Deprecated parameter type=json"}}})
			p.openProblems()
			tt, _ = tester(p, 1440, 900)
			bottom := image.Rect(0, tt.Image().Bounds().Dy()-(uikit.HeightFooter+310)*scale,
				tt.Image().Bounds().Dx(), tt.Image().Bounds().Dy()-uikit.HeightFooter*scale)
			writeFeatureScreenshot(t, dir, "bottom-problems-full-"+appearance+".png", tt.Image())
			writeFeatureScreenshot(t, dir, "bottom-problems-"+appearance+".png", crop(tt.Image(), bottom))
			p.problems.show[0] = false // Error hidden: the button takes its dot
			tt.Frame()
			writeFeatureScreenshot(t, dir, "bottom-problems-filtered-"+appearance+".png", crop(tt.Image(), bottom))
			if err := tt.Click(locales.FilterProblemsBySeverity()); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			writeFeatureScreenshot(t, dir, "bottom-problems-filter-menu-"+appearance+".png", crop(tt.Image(), bottom))
			tt.Key(0, ui.KeyEscape)
			tt.Frame()
			p.bottom.tab = bottomConsole
			tt.Frame()
			writeFeatureScreenshot(t, dir, "bottom-console-"+appearance+".png", crop(tt.Image(), bottom))
			if !tt.HasText(locales.Problems()) || !tt.HasText(locales.Console()) {
				t.Fatalf("the panel's tab row lost a tab: %v", tt.Texts())
			}

			// 4. Runs and Examples in the right sidebar, beside Variables,
			// Code and Info.
			r := workspaceApp()
			r.page = pageRequest
			r.requests[0].ID = "list"
			r.requests[0].Examples = []request.Example{{ID: "ok", Name: "Two projects", Status: 200, Body: `{"total":2}`}}
			saved := r.requests[0]
			for i, run := range []struct {
				status int
				body   string
			}{{201, `{"run":"first"}`}, {404, `{"run":"latest"}`}} {
				rec := snapshotExperiment(i+1, saved, "", request.Execution{
					Response: &capture.Flow{Method: "GET", URL: saved.URL, Status: run.status, Millis: 12, ResponseBody: run.body},
				})
				rec.At = time.Now().Add(time.Duration(-i) * time.Hour)
				if err := r.experiments.log.retain(rec, experimentLimits{}); err != nil {
					t.Fatal(err)
				}
			}
			showCollections(t, tt)
			r.sidebarMode = sidebarCollections
			tt, _ = tester(r, 1600, 900)
			if err := tt.Click("List projects"); err != nil {
				t.Fatal(err)
			}
			r.rightSidebar.open, r.rightSidebar.tab = true, rightRuns
			tt.Frame()
			sidebar := image.Rect(1260*scale, 0, tt.Image().Bounds().Dx(), tt.Image().Bounds().Dy())
			writeFeatureScreenshot(t, dir, "sidebar-runs-"+appearance+".png", crop(tt.Image(), sidebar))
			if !tt.HasText("Run #1") || !tt.HasText("Run #2") {
				t.Fatalf("the Runs panel lost a run: %v", tt.Texts())
			}
			r.rightSidebar.tab = rightExamples
			tt.Frame()
			writeFeatureScreenshot(t, dir, "sidebar-examples-"+appearance+".png", crop(tt.Image(), sidebar))
			if !tt.HasText(locales.Examples()) || !tt.HasText(locales.OpenExampleNamed("Two projects")) {
				t.Fatalf("the Examples panel lost its example or its mode: %v", tt.Texts())
			}
		})
	}
}

// TestBottomPanelTabsSwitchAndClose checks the shared panel's memory: the
// chosen tab survives a frame, the footer opens Console, and Close closes
// both views.
func TestBottomPanelTabsSwitchAndClose(t *testing.T) {
	a := workspaceApp()
	a.page = pageRequest
	a.openProblems()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	if !a.bottom.open || a.bottom.tab != bottomProblems {
		t.Fatalf("Problems did not open the panel: %+v", a.bottom)
	}
	if err := tt.Click(locales.BottomPanelName() + ": " + locales.Console()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.bottom.open || a.bottom.tab != bottomConsole {
		t.Fatalf("the Console tab did not switch the panel: %+v", a.bottom)
	}
	if err := tt.Click(locales.Close()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.bottom.open {
		t.Fatal("Close left the panel open")
	}
}

// TestRequestRunsKeepTheirOwnEmptyState checks the sidebar's Runs panel in
// place of the response column: a request with no recorded run explains
// itself instead of opening a page.
func TestRequestRunsKeepTheirOwnEmptyState(t *testing.T) {
	a := workspaceApp()
	a.page = pageRequest
	tt := ui.NewTester(a.view, 1440, 900)
	tt.SetLocale("en-US")
	if err := tt.Click("List projects"); err != nil {
		t.Fatal(err)
	}
	a.showRightSidebarRuns()
	tt.Frame()
	if !a.rightSidebar.open || a.rightSidebar.tab != rightRuns {
		t.Fatalf("the runs entry point did not open the sidebar: %+v", a.rightSidebar)
	}
	if !tt.HasText(locales.NotSentYet()) {
		t.Fatalf("the empty Runs panel has no hint: %v", tt.Texts())
	}
}

// TestCommandPaletteGrowsWithItsResults checks the palette's height: one
// match is a short panel, many grow it toward half the window and no further.
func TestCommandPaletteGrowsWithItsResults(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("Toggle right sidebar")
	tt.Frame()
	hint, ok := tt.Find(locales.SelectHint())
	if !ok {
		t.Fatalf("the palette footer is missing: %v", tt.Texts())
	}
	if hint.Y > 300 {
		t.Fatalf("one result left the palette %.0f tall", hint.Y)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("request")
	tt.Frame()
	hint, _ = tt.Find(locales.SelectHint())
	if hint.Y < 400 {
		t.Fatalf("many results did not grow the palette: footer at %.0f", hint.Y)
	}
	if hint.Y > 600 {
		t.Fatalf("the palette passed half the window: footer at %.0f", hint.Y)
	}
}

// TestFooterConsoleOpensThePanel checks the footer's Console item reaches the
// shared panel's Console tab, even with the panel closed.
func TestFooterConsoleOpensThePanel(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	if err := tt.Click(locales.Console()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.bottom.open || a.bottom.tab != bottomConsole {
		t.Fatalf("the footer Console did not open the panel on Console: %+v", a.bottom)
	}
}

// TestPreviewLimitedOnlyWhenTheBodyWasCut checks the note names a real cut:
// a small body under a recorded size that is not truncated keeps quiet, and a
// body past the retained limit says so even without a last-attempt row.
func TestPreviewLimitedOnlyWhenTheBodyWasCut(t *testing.T) {
	a := workspaceApp()
	a.page = pageRequest
	tt := ui.NewTester(a.view, 1440, 900)
	tt.SetLocale("en-US")
	if err := tt.Click("List projects"); err != nil {
		t.Fatal(err)
	}
	d := a.requestDocument
	d.sent, d.sentAt, d.sentSet = d.draft(), time.Now(), true
	d.response = &capture.Flow{Status: 404, Millis: 20, Bytes: 746609, ResponseBody: `{"error":"Not found"}`,
		ResponseBodySize: 746609, ResponseHeader: http.Header{"Content-Type": {"application/json"}}}
	d.responseArrived()
	tt.Frame()
	tt.Frame()
	if tt.HasText(locales.PreviewLimited()) {
		t.Fatalf("a small body under the retention limit claimed to be a preview: %v", tt.Texts())
	}
	d.sentSet = false
	d.response.ResponseBodySize = 9 << 20
	tt.Frame()
	tt.Frame()
	if !tt.HasText(locales.PreviewLimited()) {
		t.Fatalf("a body past the retained limit does not say its preview is limited: %v", tt.Texts())
	}
}

// A problems group folds from its heading, as the search tree's resources do.
func TestProblemGroupCollapses(t *testing.T) {
	a := workspaceApp()
	a.page = pageRequest
	for i, name := range []string{"List projects", "Create order"} {
		a.addDocument(savedRequest{Name: name, Method: "GET", URL: "https://api.example.com/items"}, []string{"saved:one", "saved:two"}[i])
	}
	first := a.documents[0]
	a.markers.ChangeOne(problemOwnerVariables, problemResource(first), []markers.Marker{
		{Severity: markers.Error, Message: "first marker"},
		{Severity: markers.Warning, Message: "second marker"},
	})
	a.openProblems()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	if err := tt.Click(locales.ProblemGroup("List projects")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("first marker") || tt.HasText("second marker") {
		t.Fatalf("the folded group kept its rows: %v", tt.Texts())
	}
	if err := tt.Click(locales.ProblemGroup("List projects")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("first marker") {
		t.Fatalf("the group did not unfold: %v", tt.Texts())
	}
}
