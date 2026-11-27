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
	match := func(text, resource, location string, source searchSource, key, field string, kind searchFieldKind, flowID, side int) searchResult {
		at := strings.Index(strings.ToLower(text), "findme")
		return searchResult{source: source, key: key, resource: resource, location: location, field: field, kind: kind, text: text,
			start: at, end: at + len("findme"), line: 1, flowID: flowID, side: side}
	}
	const request = "Findme Request"
	const flow = "GET /findme-flow"
	withTarget := func(r searchResult, target searchTarget, env, variable string) searchResult {
		r.target, r.env, r.variable = target, env, variable
		return r
	}
	return []searchResult{
		{source: searchCollections, key: "saved:find-1", resource: request, location: "Workspace/Orders", method: "POST", field: locales.Name(), kind: searchFieldOther,
			text: "Findme Request", start: strings.Index(strings.ToLower("Findme Request"), "findme"), end: strings.Index(strings.ToLower("Findme Request"), "findme") + len("findme"), line: 1,
			target: searchTarget{owner: searchValueEnvCurrent, key: "Findme Local", valueKind: searchValueEnvCurrent}},
		match("https://api.example.com/findme", request, "Workspace/Orders", searchCollections, "saved:find-1", locales.Url(), searchFieldOther, 0, -1),
		withTarget(match("findme_var = findme-value", "findme_var", "Findme Local", searchEnvironments, "Findme Local", locales.Initial(), searchFieldOther, 0, -1),
			searchTarget{owner: searchValueEnvInitial, key: "Findme Local", code: "findme_var", valueKind: searchValueEnvInitial}, "Findme Local", "findme_var"),
		withTarget(match("findme_global = global-value", "findme_global", locales.ScopeGlobal(), searchEnvironments, "", locales.GlobalVariable(), searchFieldOther, 0, -1),
			searchTarget{owner: searchValueGlobalInitial, key: "", code: "findme_global", valueKind: searchValueGlobalInitial}, "", "findme_global"),
		match("https://api.example.com/findme-flow", flow, "api.example.com", searchFlows, "1", locales.Url(), searchFieldOther, 1, -1),
		match(`{"findme":2}`, flow, "api.example.com", searchFlows, "1", locales.ResponseBody(), searchFieldFlowBody, 1, 1),
	}
}

func searchScreenshotApp() *app {
	a := globalSearchTestApp()
	a.sidebarMode = sidebarSearch
	a.searchAll.input = "findme"
	a.searchAll.armed = a.searchAll.request()
	a.searchAll.results = searchScreenshotResults(a)
	return a
}

func TestGlobalSearchViewGroupsResults(t *testing.T) {
	a := searchScreenshotApp()
	a.sidebarWidth = 360 // wide enough for resource names and their locations
	tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1100, 800)
	tt.Frame()
	for _, text := range []string{
		locales.SearchSourceRequests(), locales.Variables(), locales.Traffic(),
		"Findme Request", "Workspace/Orders", locales.OpenSearchResult("findme_var"),
		locales.SearchResultsInResources(len(a.searchAll.results), searchResourceCount(a.searchAll.results)),
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

func TestGlobalSearchTreeCollapsesSourcesAndResources(t *testing.T) {
	a := searchScreenshotApp()
	t.Cleanup(a.stopSearch)
	tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1100, 800)
	tt.Frame()
	const request = "Findme Request"
	echo := locales.OpenSearchResult("findme_var")
	if !tt.HasText(request) || !tt.HasText(echo) {
		t.Fatalf("the tree is not open at first: %v", tt.Texts())
	}
	if err := tt.Click(locales.SearchSourceResults(locales.SearchSourceRequests())); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText(request) {
		t.Fatal("collapsing the Requests source kept its resources")
	}
	if err := tt.Click(locales.SearchSourceResults(locales.SearchSourceRequests())); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("findme_var"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText(echo) || !tt.HasText("findme_var") {
		t.Fatalf("collapsing a resource did not keep its row and hide its matches: %v", tt.Texts())
	}
	// A new query searches new rows, so the folded branch opens again.
	a.setSearchQuery("other")
	a.setSearchQuery("findme")
	a.searchAll.results = searchScreenshotResults(a)
	tt.Frame()
	if !tt.HasText(echo) {
		t.Fatal("a new query kept the old collapsed resource")
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
