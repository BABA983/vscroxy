package app

import (
	"fmt"
	"slices"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
)

func paletteTestApp() *app {
	a := workspaceApp()
	a.folders = []request.Folder{
		{Path: "Shop", Description: "Storefront endpoints"},
		{Path: "Shop/Orders", Description: "Order history"},
	}
	a.requests = []savedRequest{
		{ID: "r1", Name: "List products", Collection: "Shop", Method: "GET", URL: "https://api.example.com/products"},
		{ID: "r2", Name: "Create order", Collection: "Shop/Orders", Method: "POST", URL: "https://api.example.com/orders"},
		{ID: "r3", Name: "Health", Method: "GET", URL: "https://status.example.com/health"},
	}
	a.envs = []environment{
		{Name: "Local", Vars: map[string]string{"base_url": "https://api.example.com", "secret": "needle-value"}},
		{Name: "Staging", Vars: map[string]string{"base_url": "https://staging.example.com"}},
	}
	a.globals = map[string]string{"trace_id": "global-value"}
	a.requestGen++
	return a
}

func paletteLabels(entries []paletteEntry) []string {
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		labels = append(labels, entry.label)
	}
	return labels
}

func TestPaletteSearchMatchesAndOrdersLibraryAndVariables(t *testing.T) {
	a := paletteTestApp()
	cases := []struct {
		query string
		want  []string
	}{
		{"order", []string{"Orders", "Create order"}},
		{"storefront", []string{"Shop"}},
		{"products", []string{"List products"}},
		{"post", []string{"Create order"}},
		{"status.example.com", []string{"Health"}},
		{"base_url", []string{"base_url", "base_url"}},
		{"local", []string{"Local"}},
		{"trace", []string{"trace_id"}},
	}
	for _, tc := range cases {
		if got := paletteLabels(a.paletteSearchEntries(tc.query)); !slices.Equal(got, tc.want) {
			t.Errorf("query %q = %v, want %v", tc.query, got, tc.want)
		}
	}
}

func TestPaletteSearchNeverMatchesVariableValues(t *testing.T) {
	a := paletteTestApp()
	for _, query := range []string{"needle", "global-value", "staging.example.com"} {
		if got := a.paletteSearchEntries(query); len(got) != 0 {
			t.Errorf("query %q matched a variable value: %+v", query, got)
		}
	}
	entries := a.paletteSearchEntries("base_url")
	for _, entry := range entries {
		if entry.detail == "https://api.example.com" || entry.detail == "https://staging.example.com" {
			t.Fatalf("variable result exposed a value: %+v", entry)
		}
	}
}

func TestPaletteSearchEntriesOpenTheirTarget(t *testing.T) {
	a := paletteTestApp()
	entry := func(query, label string) paletteEntry {
		t.Helper()
		for _, candidate := range a.paletteSearchEntries(query) {
			if candidate.label == label {
				return candidate
			}
		}
		t.Fatalf("query %q did not find %q", query, label)
		return paletteEntry{}
	}

	entry("create order", "Create order").run()
	if a.page != pageRequest || a.requestDocument == nil || a.requestDocument.savedKey != a.requests[1].key() {
		t.Fatalf("request result opened %+v", a.requestDocument)
	}

	entry("local", "Local").run()
	if !a.varsOpen || a.varsName != "Local" {
		t.Fatalf("environment result opened vars=%t name=%q", a.varsOpen, a.varsName)
	}
	a.varsOpen = false

	entry("base_url", "base_url").run()
	if !a.varsOpen || a.varsName != "Local" {
		t.Fatalf("variable result opened vars=%t name=%q", a.varsOpen, a.varsName)
	}
	a.varsOpen = false

	entry("trace", "trace_id").run()
	if !a.varsOpen || !a.varsGlobals {
		t.Fatalf("global variable result opened vars=%t globals=%t", a.varsOpen, a.varsGlobals)
	}
	a.varsOpen = false

	a.requestQuery, a.workbench.sidebarHidden = "hides everything", true
	entry("storefront", "Shop").run()
	if a.page != pageRequest || !a.collectionsOpen || a.requestQuery != "" || a.workbench.sidebarHidden || !*a.collectionShown("Shop") {
		t.Fatalf("collection result did not reveal its path: page=%v open=%t query=%q hidden=%t", a.page, a.collectionsOpen, a.requestQuery, a.workbench.sidebarHidden)
	}
}

func TestCommandPaletteOpensRequestAndEnvironmentResults(t *testing.T) {
	a := paletteTestApp()
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("Create order")
	tt.Key(0, ui.KeyEnter)
	if a.requestDocument == nil || a.requestDocument.savedKey != a.requests[1].key() || a.page != pageRequest {
		t.Fatalf("palette request result opened %+v", a.requestDocument)
	}
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("Staging")
	tt.Key(0, ui.KeyEnter)
	if !a.varsOpen || a.varsName != "Staging" {
		t.Fatalf("palette environment result opened vars=%t name=%q", a.varsOpen, a.varsName)
	}
}

func TestPaletteSearchIndexUpdatesWithTheLibrary(t *testing.T) {
	a := workspaceApp()
	a.requests = []savedRequest{{ID: "one", Name: "Alpha", Method: "GET", URL: "https://api.example.com/alpha"}}
	a.requestGen++
	if got := paletteLabels(a.paletteSearchEntries("alpha")); !slices.Equal(got, []string{"Alpha"}) {
		t.Fatalf("initial search = %v", got)
	}
	if err := a.persistRequests([]savedRequest{{ID: "two", Name: "Beta", Method: "GET", URL: "https://api.example.com/beta"}}); err != nil {
		t.Fatal(err)
	}
	if got := paletteLabels(a.paletteSearchEntries("beta")); !slices.Equal(got, []string{"Beta"}) {
		t.Fatalf("search after a library change = %v", got)
	}
	if got := a.paletteSearchEntries("alpha"); len(got) != 0 {
		t.Fatalf("replaced library still matched: %+v", got)
	}
}

func BenchmarkPaletteSearch5000RequestsWarm(b *testing.B) {
	a := workspaceApp()
	a.requests = make([]savedRequest, 5000)
	for i := range a.requests {
		a.requests[i] = savedRequest{
			ID: fmt.Sprintf("r%d", i), Name: fmt.Sprintf("Request %04d", i),
			Collection: fmt.Sprintf("Collection %d", i/100), Method: "GET",
			URL: fmt.Sprintf("https://api.example.com/items/%d", i),
		}
	}
	a.requestGen++
	a.paletteSearchEntries("warm the index")
	b.ResetTimer()
	for b.Loop() {
		if got := a.paletteSearchEntries("items/4999"); len(got) != 1 {
			b.Fatalf("matches = %d", len(got))
		}
	}
}

func BenchmarkPaletteSearch5000RequestsCold(b *testing.B) {
	a := workspaceApp()
	a.requests = make([]savedRequest, 5000)
	for i := range a.requests {
		a.requests[i] = savedRequest{
			ID: fmt.Sprintf("r%d", i), Name: fmt.Sprintf("Request %04d", i),
			Collection: fmt.Sprintf("Collection %d", i/100), Method: "GET",
			URL: fmt.Sprintf("https://api.example.com/items/%d", i),
		}
	}
	b.ResetTimer()
	for b.Loop() {
		a.requestGen++
		if got := a.paletteSearchEntries("items/4999"); len(got) != 1 {
			b.Fatalf("matches = %d", len(got))
		}
	}
}
