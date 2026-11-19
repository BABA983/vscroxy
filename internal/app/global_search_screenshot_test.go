package app

import (
	"os"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// searchScreenshotResults keeps one or two representative lines of each
// source, so both themes show the groups, resources and highlighted runs
// rather than a long list the window has to scroll.
func searchScreenshotResults(a *app) []searchResult {
	match := func(text, resource string, source searchSource, key, field string, kind searchFieldKind, flowID, side int) searchResult {
		at := strings.Index(strings.ToLower(text), "findme")
		return searchResult{source: source, key: key, resource: resource, field: field, kind: kind, text: text,
			start: at, end: at + len("findme"), line: 1, flowID: flowID, side: side}
	}
	const request = "Workspace/Orders / Findme Request"
	const flow = "GET https://api.example.com/findme-flow"
	return []searchResult{
		match("Findme Request", request, searchCollections, "saved:find-1", locales.Name(), searchFieldOther, 0, -1),
		match("https://api.example.com/findme", request, searchCollections, "saved:find-1", locales.Url(), searchFieldOther, 0, -1),
		match("findme_var = findme-value", "Findme Local", searchEnvironments, "Findme Local", locales.Initial(), searchFieldOther, 0, -1),
		match("findme_global = global-value", locales.GlobalVariable(), searchEnvironments, "", locales.GlobalVariable(), searchFieldOther, 0, -1),
		match("https://api.example.com/findme-flow", flow, searchFlows, "1", locales.Url(), searchFieldOther, 1, -1),
		match(`{"findme":2}`, flow, searchFlows, "1", locales.ResponseBody(), searchFieldFlowBody, 1, 1),
	}
}

func searchScreenshotApp() *app {
	a := globalSearchTestApp()
	a.page, a.sidebarChoice = pageSearch, "search"
	a.searchAll.input = "findme"
	a.searchAll.armed = a.searchAll.request()
	a.searchAll.results = searchScreenshotResults(a)
	return a
}

func TestGlobalSearchViewGroupsResults(t *testing.T) {
	a := searchScreenshotApp()
	tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1100, 800)
	tt.Frame()
	for _, text := range []string{
		locales.Collections(), locales.QuickAccessEnvironmentsAndVariables(), locales.CapturedRequests(),
		"Workspace/Orders / Findme Request", locales.OpenSearchResult("Findme Local"),
		locales.SearchResultCount(len(a.searchAll.results)),
	} {
		if !tt.HasText(text) {
			t.Errorf("Search view is missing %q: %v", text, tt.Texts())
		}
	}
	a.searchAll.limited = 1
	tt.Frame()
	if !tt.HasText(locales.SearchResultsLimited(globalSearchLimit)) {
		t.Errorf("the capped-search note is missing: %v", tt.Texts())
	}
}

// TestGlobalSearchScreenshots renders the result view at 2x in both themes.
// Set VSCROXY_GLOBAL_SEARCH_SCREENSHOTS to an absolute output directory.
func TestGlobalSearchScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_GLOBAL_SEARCH_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_GLOBAL_SEARCH_SCREENSHOTS to an output directory")
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
			a := searchScreenshotApp()
			a.appearance = appearance
			tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1100, 800)
			tt.SetScale(2)
			tt.Frame()
			writeFeatureScreenshot(t, dir, "global-search-"+appearance+".png", tt.Image())
		})
	}
}
