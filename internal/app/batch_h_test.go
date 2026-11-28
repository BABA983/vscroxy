package app

import (
	"image"
	"image/draw"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestTabQuickPickFiltersAndActivates proves the strip's ⌄ is a searchable
// picker: a query keeps only the matching tab, and Enter opens it.
func TestTabQuickPickFiltersAndActivates(t *testing.T) {
	a := workspaceApp()
	openCleanTab(a, "Projects", "https://example.test/projects")
	order := openCleanTab(a, "Find order", "https://example.test/v1/orders/42")
	openCleanTab(a, "Settings", "https://example.test/settings")
	a.tabPicker.open = true
	t.Cleanup(a.stopSearch)
	tt := ui.NewTester(a.view, 1400, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	tt.Frame()
	if !tt.HasText(locales.SearchTabs()) {
		t.Fatalf("the tab picker is missing: %v", tt.Texts())
	}
	tt.Type("order")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := a.tabsGroup().active(); got != requestTabID(order) {
		t.Fatalf("Enter opened %+v, want the filtered order tab", got)
	}
	if a.tabPicker.open {
		t.Fatal("the picker stayed open after opening a tab")
	}
}

// TestTabQuickPickCloseKey closes the highlighted tab with Cmd/Ctrl+Backspace
// without closing the picker.
func TestTabQuickPickCloseKey(t *testing.T) {
	a := workspaceApp()
	openCleanTab(a, "Projects", "https://example.test/projects")
	openCleanTab(a, "Order", "https://example.test/order")
	a.tabPicker.open, a.tabPicker.state.Query = true, "projects"
	tt := ui.NewTester(a.view, 1400, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyBackspace)
	tt.Frame()
	if slices.ContainsFunc(a.documents, func(d *requestDocument) bool { return d.title() == "Projects" }) {
		t.Fatal("Cmd/Ctrl+Backspace did not close the highlighted tab")
	}
	if !a.tabPicker.open {
		t.Fatal("closing a tab closed the picker")
	}
}

// TestTabQuickPickMatchesURL checks the query reads a tab's URL as well as
// its title.
func TestTabQuickPickMatchesURL(t *testing.T) {
	a := workspaceApp()
	openCleanTab(a, "First", "https://example.test/home")
	target := openCleanTab(a, "Projects", "https://example.test/v1/orders/42")
	a.tabPicker.open, a.tabPicker.state.Query = true, "v1/orders"
	tt := ui.NewTester(a.view, 1400, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := a.tabsGroup().active(); got != requestTabID(target) {
		t.Fatalf("URL query opened %+v, want the Orders tab", got)
	}
}

// TestTabQuickPickArrowKeysAndEscape checks Up and Down move the highlighted
// row and Escape closes the popover.
func TestTabQuickPickArrowKeysAndEscape(t *testing.T) {
	a := workspaceApp()
	openCleanTab(a, "Projects", "https://example.test/projects")
	openCleanTab(a, "Archive project", "https://example.test/archive")
	a.tabPicker.open, a.tabPicker.state.Query = true, "project"
	tt := ui.NewTester(a.view, 1400, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	tt.Frame()
	if a.tabPicker.state.Selected != 0 {
		t.Fatalf("selection starts at %d", a.tabPicker.state.Selected)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if a.tabPicker.state.Selected != 1 {
		t.Fatalf("Down selected %d", a.tabPicker.state.Selected)
	}
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if a.tabPicker.state.Selected != 0 {
		t.Fatalf("Up selected %d", a.tabPicker.state.Selected)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.tabPicker.open {
		t.Fatal("Escape did not close the picker")
	}
}

// TestTabQuickPickClosedTabRestores checks the Recently closed group lists a
// closed tab and Enter brings it back.
func TestTabQuickPickClosedTabRestores(t *testing.T) {
	a := workspaceApp()
	openCleanTab(a, "Projects", "https://example.test/projects")
	closed := openCleanTab(a, "Archive project", "https://example.test/archive")
	a.closeTab(requestTabID(closed), true)
	if len(a.tabsGroup().closedIDs()) != 1 {
		t.Fatalf("closed tabs = %v", a.tabsGroup().closedIDs())
	}
	a.tabPicker.open, a.tabPicker.state.Query = true, "archive"
	tt := ui.NewTester(a.view, 1400, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	tt.Frame()
	if !tt.HasText(locales.RecentlyClosedGroup()) || !tt.HasText("Archive project") {
		t.Fatalf("Recently closed is missing: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if len(a.documents) != 2 || a.documents[len(a.documents)-1].title() != "Archive project" {
		t.Fatalf("Enter did not reopen the closed tab: %d documents", len(a.documents))
	}
}

// TestTabQuickPickCommandHasAKey checks the palette and the keyboard can open
// the same picker as its button.
func TestTabQuickPickCommandHasAKey(t *testing.T) {
	a := workspaceApp()
	openCleanTab(a, "Projects", "https://example.test/projects")
	var c *ui.Context
	tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, 1200, 800)
	tt.Frame()
	command := pick(a.actions(c), "tabs.search")
	if command.id != "tabs.search" || !command.enabled {
		t.Fatalf("tabs.search = %+v", command)
	}
	if command.shortcut.mods != ui.Cmd|ui.Shift || command.shortcut.key != ui.KeyA {
		t.Fatalf("tabs.search shortcut = %+v", command.shortcut)
	}
	command.run()
	tt.Frame()
	if !a.tabPicker.open || !tt.HasText(locales.SearchTabs()) {
		t.Fatalf("the command did not open the picker: %v", tt.Texts())
	}
}

// TestRightSidebarOnlyOnRequests checks the column steps aside on Live and
// returns with the request tab.
func TestRightSidebarOnlyOnRequests(t *testing.T) {
	a := workspaceApp()
	a.rightSidebar.open = true
	tt := ui.NewTester(a.view, 1400, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	if a.rightSidebarShown() || tt.HasText(locales.VariablesInRequest()) {
		t.Fatal("the right sidebar showed on Live")
	}
	openCleanTab(a, "Find order", "https://example.test/order")
	tt.Frame()
	tt.Frame()
	if !a.rightSidebarShown() || !tt.HasText(locales.VariablesInRequest()) {
		t.Fatalf("the right sidebar did not return for a request: %v", tt.Texts())
	}
}

// rightSidebarApp opens a request with an environment the sidebar can
// resolve and a collection for the path row.
func rightSidebarApp(t *testing.T) (*app, *requestDocument) {
	t.Helper()
	a := workspaceApp()
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	a.envs = []environment{{Name: "Local", Vars: map[string]string{
		"baseUrl": "https://api.example.com",
		"token":   "supersecret-value",
	}}}
	a.activeEnv = "Local"
	a.rightSidebar.open = true
	a.rightSidebar.width = 320
	d := openCleanTab(a, "Find order", "{{baseUrl}}/v1/orders")
	d.composeCollection = "Workspace/Orders"
	d.composeURL = "{{baseUrl}}/v1/orders?token={{token}}&who={{missing}}"
	return a, d
}

// TestRightSidebarVariablesResolveAndAdd checks the Variables pane names the
// resolved and the missing variables, and Add creates the missing one in the
// active environment.
func TestRightSidebarVariablesResolveAndAdd(t *testing.T) {
	a, _ := rightSidebarApp(t)
	a.rightSidebar.tab = 0
	tt := ui.NewTester(a.view, 1500, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	tt.Frame()
	for _, text := range []string{locales.VariablesInRequest(), "baseUrl", "https://api.example.com", "missing", locales.NotDefinedInAnyScope(), locales.Add()} {
		if !tt.HasText(text) {
			t.Errorf("Variables pane is missing %q: %v", text, tt.Texts())
		}
	}
	if tt.HasText("supersecret-value") {
		t.Error("a credential's value was shown in the sidebar")
	}
	if err := tt.Click(locales.Add()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.envs[0].Vars["missing"]; got != "" {
		t.Fatalf("Add wrote %q, want an empty value", got)
	}
	if !a.varsOpen {
		t.Fatal("Add did not open the environment editor")
	}
	tt.Frame()
	tt.Type("created-value")
	tt.Frame()
	found := false
	for _, row := range a.varsRows {
		if row.Name == "missing" && row.Initial == "created-value" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Add did not focus the new value: %+v", a.varsRows)
	}
}

// TestSidebarCurlIsCleanAndMultiline checks the panel's authored curl: one
// argument per line, no exact-replay flags, no empty header suppressions, and
// the request body on its own line.
func TestSidebarCurlIsCleanAndMultiline(t *testing.T) {
	req := &request.Prepared{
		Method: "POST",
		URL:    "https://api.example.com/v1/items",
		Header: http.Header{
			"Accept":         {""},
			"Content-Type":   {"application/json"},
			"User-Agent":     {""},
			"X-Trace":        {"abc"},
			"Content-Length": {"12"},
		},
		Body: `{"id":1}`,
	}
	got, err := sidebarSnippetText(req, "cURL")
	if err != nil {
		t.Fatal(err)
	}
	want := "curl --location 'https://api.example.com/v1/items' \\\n" +
		"  --request 'POST' \\\n" +
		"  --header 'Content-Type: application/json' \\\n" +
		"  --header 'X-Trace: abc' \\\n" +
		"  --data '{\"id\":1}'"
	if got != want {
		t.Fatalf("curl =\n%s\nwant\n%s", got, want)
	}
	for _, noise := range []string{"-q", "--path-as-is", "--globoff", "'Accept:'", "'User-Agent:'"} {
		if strings.Contains(got, noise) {
			t.Errorf("panel curl kept %q:\n%s", noise, got)
		}
	}
}

// TestRightSidebarCodeKeepsOrResolvesVariables checks the Code switch: on
// leaves {{baseUrl}}, off resolves it and masks the secret while the copy
// text keeps the real value.
func TestRightSidebarCodeKeepsOrResolvesVariables(t *testing.T) {
	a, _ := rightSidebarApp(t)
	a.rightSidebar.tab = 1
	a.rightSidebar.codeKeep = true
	tt := ui.NewTester(a.view, 1500, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	tt.Frame()
	if a.rightSidebar.codeView == nil || !strings.Contains(a.rightSidebar.codeView.Buffer.String(), "{{baseUrl}}") {
		t.Fatalf("Keep variables did not leave the reference: %v", a.rightSidebar.codeView)
	}
	if err := tt.Click(locales.KeepVariables()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	code := a.rightSidebar.codeView.Buffer.String()
	if strings.Contains(code, "{{baseUrl}}") || !strings.Contains(code, "https://api.example.com") {
		t.Fatalf("resolved code = %q", code)
	}
	if strings.Contains(code, "supersecret-value") || !strings.Contains(code, "••••••") {
		t.Fatalf("secret not masked in %q", code)
	}
	if !strings.Contains(a.rightSidebar.codeCopy, "supersecret-value") {
		t.Fatalf("copy text lost the real secret: %q", a.rightSidebar.codeCopy)
	}
}

// TestPreserveCaseFollowsTheMatchStyle covers the three styles VS Code's
// Preserve Case changes and leaves a mixed match alone.
func TestPreserveCaseFollowsTheMatchStyle(t *testing.T) {
	for _, tc := range []struct{ match, replacement, want string }{
		{"Order", "orderRef", "OrderRef"},
		{"ORDER", "orderRef", "ORDERREF"},
		{"order", "orderRef", "orderref"},
		{"OrDer", "orderRef", "orderRef"},
	} {
		if got := preserveCase(tc.match, tc.replacement); got != tc.want {
			t.Errorf("preserveCase(%q, %q) = %q, want %q", tc.match, tc.replacement, got, tc.want)
		}
	}
}

// TestSearchResourceDetailHidesWhenNarrow checks the name keeps its room and
// the collection path is left out rather than squeezing both.
func TestSearchResourceDetailHidesWhenNarrow(t *testing.T) {
	a := searchScreenshotApp()
	t.Cleanup(a.stopSearch)
	a.sidebarWidth = 220
	tt := ui.NewTester(a.view, 1400, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	tt.Frame()
	if tt.HasText("Workspace/Orders") {
		t.Fatalf("the narrow tree kept a squeezed location: %v", tt.Texts())
	}
	a.sidebarWidth = 360
	for range 2 {
		tt.Frame()
	}
	if !tt.HasText("Workspace/Orders") {
		t.Fatalf("the wide tree did not show the location: %v", tt.Texts())
	}
}

// TestSearchPreserveCaseToggle checks the AB toggle changes the replacements
// the same plan writes, through the sidebar's own control.
func TestSearchPreserveCaseToggle(t *testing.T) {
	a, _ := searchReplaceFixture(t)
	t.Cleanup(a.stopSearch)
	a.requests[0].Body = "Shape\nshape\nSHAPE\n"
	if err := a.persistRequests(a.requests); err != nil {
		t.Fatal(err)
	}
	prepareSearch(t, a, "shape", "shapeRef")
	a.sidebarMode = sidebarSearch
	a.searchAll.replace.open = true
	tt := ui.NewTester(a.view, 1200, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	tt.Frame()
	if err := tt.Click(locales.PreserveCase()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.searchAll.replace.preserve {
		t.Fatal("the AB toggle did not turn Preserve Case on")
	}
	changes := a.searchChanges()
	got := make([]string, len(changes))
	for i, change := range changes {
		got[i] = change.Replacement
	}
	if !slices.Equal(got, []string{"ShapeRef", "shaperef", "SHAPEREF"}) {
		t.Fatalf("preserved replacements = %v", got)
	}
}

// TestSearchScopeFilterSkipsSources proves a switched-off chip leaves that
// source out of the scan rather than hiding its rows after the fact.
func TestSearchScopeFilterSkipsSources(t *testing.T) {
	a := globalSearchTestApp()
	t.Cleanup(a.stopSearch)
	a.searchAll.input = "findme"
	a.searchAll.armed = a.searchAll.request()
	q, err := compileSearchQuery("findme", &a.searchAll)
	if err != nil {
		t.Fatal(err)
	}
	all := runSearch(t, a.searchSnapshot(q, searchScopeAll))
	if !slices.ContainsFunc(all.results, func(r searchResult) bool { return r.source == searchEnvironments }) {
		t.Fatal("the unfiltered scan found no variable rows")
	}
	s := a.searchSnapshot(q, searchScopeRequests|searchScopeTraffic)
	got := runSearch(t, s)
	if slices.ContainsFunc(got.results, func(r searchResult) bool { return r.source == searchEnvironments }) {
		t.Fatalf("switching Variables off still scanned it: %+v", got.results)
	}
	if !slices.ContainsFunc(got.results, func(r searchResult) bool { return r.source == searchCollections }) {
		t.Fatal("switching Variables off also dropped requests")
	}

	// The chip is the same switch from the view.
	a.searchAll.scopeOff = nil
	tt := ui.NewTester(a.view, 1200, 800)
	tt.SetLocale("en-US")
	a.sidebarMode = sidebarSearch
	tt.Frame()
	if err := tt.Click(locales.SearchInSource(locales.SearchScopeVariables())); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.searchAll.searches(searchEnvironments) {
		t.Fatal("the Variables chip did not switch the source off")
	}
}

// TestReplaceAllConfirmsBeforeWriting checks the sidebar's Replace All asks
// first, and only the confirmed action writes the files.
func TestReplaceAllConfirmsBeforeWriting(t *testing.T) {
	a, _ := searchReplaceFixture(t)
	t.Cleanup(a.stopSearch)
	prepareSearch(t, a, "token", "scope")
	a.sidebarMode = sidebarSearch
	a.searchAll.replace.open = true
	tt := ui.NewTester(a.view, 1200, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	tt.Frame()
	if err := tt.Click(locales.ReplaceAll()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.searchAll.replace.confirmOpen {
		t.Fatal("Replace All wrote without asking")
	}
	if !tt.HasText(locales.ReplaceSearchConfirm("2", "1", "scope")) {
		t.Fatalf("the confirmation does not name the change: %v", tt.Texts())
	}
	if a.requests[0].Body != "token tokenId\ntoken\n" {
		t.Fatalf("Replace All wrote before confirmation: %q", a.requests[0].Body)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.requests[0].Body != "scope tokenId\nscope\n" {
		t.Fatalf("confirmed Replace All = %q", a.requests[0].Body)
	}
}

// TestBatchHScreenshots captures batch H's surfaces in both themes. Set
// VSCROXY_BATCH_H_SCREENSHOTS to an absolute output directory.
func TestBatchHScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_BATCH_H_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_BATCH_H_SCREENSHOTS to an output directory")
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
			write := func(name string, img *image.RGBA, r image.Rectangle) {
				t.Helper()
				writeFeatureScreenshot(t, dir, name+"-"+appearance+".png", crop(img, r))
			}
			tester := func(a *app, width, height float32) (*ui.Tester, *ui.Context) {
				t.Helper()
				a.appearance = appearance
				var ctx *ui.Context
				tt := ui.NewTester(func(ctx2 *ui.Context) { ctx = ctx2; a.view(ctx2) }, int(width), int(height))
				tt.SetLocale("en-US")
				tt.SetDark(dark)
				tt.SetScale(scale)
				tt.Frame()
				tt.Frame()
				return tt, ctx
			}
			sidebar := image.Rect(0, 0, 330*scale, 1000*scale)

			// 1. The document strip's searchable list, filtered by a query.
			a := workspaceApp()
			openCleanTab(a, "Projects", "https://api.example.com/projects")
			openCleanTab(a, "Find order", "https://api.example.com/v1/orders/42")
			last := openCleanTab(a, "Create project", "https://api.example.com/projects")
			last.composeCollection = "Workspace/API"
			closed := openCleanTab(a, "Archive project", "https://api.example.com/archive")
			closed.composeCollection = "Workspace/API"
			a.closeTab(requestTabID(closed), true)
			a.tabPicker.open, a.tabPicker.state.Query = true, "proj"
			tt, _ := tester(a, 1500, 1000)
			write("tab-picker", tt.Image(), tt.Image().Bounds())
			write("tab-picker-crop", tt.Image(), image.Rect(1000*scale, 50*scale, 1480*scale, 300*scale))
			if !tt.HasText(locales.SearchTabs()) || !tt.HasText("Create project") {
				t.Fatalf("the tab picker did not filter: %v", tt.Texts())
			}
			_ = last

			// 2. The right sidebar: Variables, Code resolved and kept, Info.
			a, d := rightSidebarApp(t)
			a.rightSidebar.tab = 0
			tt, _ = tester(a, 1500, 1000)
			write("right-variables", tt.Image(), tt.Image().Bounds())
			write("right-variables-crop", tt.Image(), image.Rect(1170*scale, 0, 1500*scale, 1000*scale))

			d.composeHeaders = "X-Trace: abc"
			d.composeBody = `{"order":42}`
			a.rightSidebar.tab = 1
			a.rightSidebar.codeKeep = false
			tt, _ = tester(a, 1500, 1000)
			write("right-code-resolved", tt.Image(), tt.Image().Bounds())
			write("right-code-resolved-crop", tt.Image(), image.Rect(1170*scale, 0, 1500*scale, 1000*scale))
			write("code-panel-resolved-crop", tt.Image(), image.Rect(1170*scale, 40*scale, 1500*scale, 975*scale))
			a.rightSidebar.codeKeep = true
			tt, _ = tester(a, 1500, 1000)
			write("right-code-kept", tt.Image(), tt.Image().Bounds())
			write("right-code-kept-crop", tt.Image(), image.Rect(1170*scale, 0, 1500*scale, 1000*scale))
			write("code-panel-kept-crop", tt.Image(), image.Rect(1170*scale, 40*scale, 1500*scale, 975*scale))

			a.rightSidebar.tab = 2
			// A long target and file name exercise the Info panel's middle
			// truncation while its tooltip keeps the whole value.
			d.composeURL = "https://very-long-production-orders-api.eu-west-1.example.com/v1/orders"
			d.sent = savedRequest{Method: "GET", URL: d.composeURL}
			d.sentAt = time.Date(2026, 11, 27, 15, 21, 13, 0, time.Local)
			d.sentSet = true
			d.response = &capture.Flow{Status: 200, Millis: 1630, ResponseBody: `{"ok":true}`,
				ResponseHeader: http.Header{"Content-Type": {"application/json"}}}
			d.assetID, d.savedKey = "req_7d4fc2a3", "saved:req_7d4fc2a3"
			tt, _ = tester(a, 1500, 1000)
			write("right-info", tt.Image(), tt.Image().Bounds())
			write("right-info-crop", tt.Image(), image.Rect(1170*scale, 0, 1500*scale, 1000*scale))

			// A response whose draft differs from the attempt and whose body
			// is a preview: the one provenance row carries both marks.
			d.composeBody = `{"order":43}`
			d.response.ResponseBodySize = 9 << 20
			tt, ctx := tester(a, 1500, 1000)
			area := a.editorRespPane.Bounds(ctx)
			write("response-status", tt.Image(), image.Rect(int(area.X)*scale, int(area.Y)*scale,
				int(area.X+area.W)*scale, int(area.Y+100)*scale))
			if !tt.HasText(locales.DraftChangedNotSent()) || !tt.HasText(locales.PreviewLimited()) {
				t.Fatalf("the response status row lacks a mark: %v", tt.Texts())
			}

			// 3. Search with Replace open: the keyboard-selected match shows
			// its actions, and a hovered resource shows its own in place of
			// the count. One scope is off.
			search := searchScreenshotApp()
			search.sidebarWidth = 260
			t.Cleanup(search.stopSearch)
			search.searchAll.replace.open = true
			search.searchAll.replace.input = "found"
			search.searchAll.replace.preserve = true
			search.searchAll.current = 2
			search.searchAll.scopeOff = map[searchSource]bool{searchFlows: true}
			search.searchAll.armed = search.searchAll.request()
			tt, _ = tester(search, 1500, 1000)
			if row, ok := tt.Find("Findme Request"); ok {
				tt.Move(row.X+row.W/2, row.Y+row.H/2)
			}
			tt.Frame()
			tt.Frame()
			write("search-replace", tt.Image(), tt.Image().Bounds())
			write("search-replace-crop", tt.Image(), sidebar)
			if !tt.HasText(locales.PreserveCase()) || !tt.HasText(locales.ReplaceAllInThisResource()) {
				t.Fatalf("the replace actions are not drawn: %v", tt.Texts())
			}

			// 4. The scope chips with Variables switched off.
			search2 := searchScreenshotApp()
			search2.sidebarWidth = 260
			t.Cleanup(search2.stopSearch)
			search2.searchAll.scopeOff = map[searchSource]bool{searchEnvironments: true}
			search2.searchAll.armed = search2.searchAll.request()
			tt, _ = tester(search2, 1400, 900)
			write("search-scopes", tt.Image(), sidebar)
			write("search-tree-crop", tt.Image(), image.Rect(0, 70*scale, int((search2.sidebarWidth+4)*scale), 700*scale))
			if search2.searchAll.searches(searchEnvironments) {
				t.Fatal("the Variables chip is on in the scope capture")
			}
		})
	}
}

// The unresolved chip is the one line the request keeps under the URL, and it
// opens the right sidebar's Variables view; the line itself is gone when every
// name resolves.
func TestUnresolvedVariablesChipOpensTheSidebar(t *testing.T) {
	a, d := rightSidebarApp(t)
	a.rightSidebar.open = false
	tt := ui.NewTester(a.view, 1500, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	tt.Frame()
	chip := locales.VariablesUnresolved(1)
	if !tt.HasText(chip) {
		t.Fatalf("the unresolved chip is missing: %v", tt.Texts())
	}
	if err := tt.Click(chip); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.rightSidebar.open || a.rightSidebar.tab != 0 {
		t.Fatalf("the chip opened the sidebar=%t at tab %d", a.rightSidebar.open, a.rightSidebar.tab)
	}
	if !tt.HasText(locales.VariablesInRequest()) {
		t.Fatalf("the sidebar is not on Variables: %v", tt.Texts())
	}
	d.composeURL = "https://api.example.com/v1/orders?token=abc"
	a.rightSidebar.open = false
	tt.Frame()
	if tt.HasText(chip) {
		t.Fatal("the unresolved line stayed after every name resolved")
	}
}
