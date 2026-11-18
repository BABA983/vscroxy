package app

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
		{Name: "Local", Vars: map[string]string{"base_url": "https://api.example.com", "secret": "needle-value"},
			Current: map[string]string{"secret": "current-needle", "current_only": "current-only-needle"}},
		{Name: "Staging", Vars: map[string]string{"base_url": "https://staging.example.com"}},
	}
	a.globals = map[string]string{"trace_id": "global-value"}
	a.globalsCurrent = map[string]string{"global_current": "global-current-needle"}
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
		{"post", []string{"Create order", "Orders", "List products"}},
		{"status.example.com", []string{"Health"}},
		{"base_url", []string{"base_url", "base_url"}},
		{"current_only", []string{"current_only"}},
		{"global_current", []string{"global_current"}},
		{"local", []string{"Local", "base_url", "current_only", "secret"}},
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
	for _, query := range []string{"needle", "current-needle", "current-only-needle", "global-value", "global-current-needle", "staging.example.com"} {
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
	for _, query := range []string{"current_only", "global_current"} {
		for _, entry := range a.paletteSearchEntries(query) {
			if strings.Contains(entry.detail, "needle") {
				t.Fatalf("Current-only result exposed a value: %+v", entry)
			}
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
	if a.page != pageRequest || a.requestQuery != "" || a.workbench.sidebarHidden || !*a.collectionShown("Shop") || !*a.regionOpen("collections") {
		t.Fatalf("collection result did not reveal its path: page=%v query=%q hidden=%t", a.page, a.requestQuery, a.workbench.sidebarHidden)
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

func TestPaletteUnavailableCommandKeepsReasonDetail(t *testing.T) {
	a := workspaceApp()
	var unavailable action
	for _, item := range a.actions(nil) {
		if !item.enabled && item.reason != "" {
			unavailable = item
			break
		}
	}
	if unavailable.label == "" {
		t.Fatal("no unavailable command to test")
	}
	result := a.paletteRankedResults(unavailable.label, a.actions(nil))
	for _, entry := range result.entries {
		if entry.label != unavailable.label || entry.enabled {
			continue
		}
		if entry.detail != entry.why {
			t.Fatalf("unavailable %q detail = %q, want %q", entry.label, entry.detail, entry.why)
		}
		return
	}
	t.Fatalf("unavailable command %q was not ranked", unavailable.label)
}

func TestPaletteFuzzyRanksGetUserAndRecentRows(t *testing.T) {
	a := paletteTestApp()
	getUser := savedRequest{ID: "get", Name: "Get User", Method: "GET", URL: "https://api.example.com/users/1"}
	a.requests = append(a.requests, getUser)
	a.requestGen++

	entries := a.paletteSearchEntries("getusr")
	if len(entries) == 0 || entries[0].label != "Get User" {
		t.Fatalf("fuzzy query getusr = %v", paletteLabels(entries))
	}
	if len(entries[0].matches) == 0 {
		t.Fatalf("Get User has no highlight ranges: %+v", entries[0])
	}
	for _, match := range entries[0].matches {
		if match.Start < 0 || match.End > len([]rune("Get User")) || match.Start >= match.End {
			t.Fatalf("invalid highlight range %+v", match)
		}
	}

	// A recently used row outranks a better fuzzy score, like VS Code quick open.
	a.workbench.rememberPalette("request:" + getUser.key())
	recent := a.paletteSearchEntries("getusr")
	if len(recent) == 0 || recent[0].label != "Get User" || recent[0].run == nil {
		t.Fatalf("recent row not first: %v", paletteLabels(recent))
	}
	if a.workbench.paletteHistory == nil || a.workbench.paletteHistory.Size() != 1 {
		t.Fatalf("palette history = %+v", a.workbench.paletteHistory)
	}
}

func TestPaletteSearchLimitsBroadResultsWithHistory(t *testing.T) {
	a := workspaceApp()
	a.requests = make([]savedRequest, 80)
	for i := range a.requests {
		a.requests[i] = savedRequest{ID: fmt.Sprintf("r%d", i), Name: fmt.Sprintf("Entry %02d", i), Method: "GET", URL: "https://api.example.com/entry"}
	}
	a.requestGen++
	a.workbench.rememberPalette("request:" + a.requests[79].key())

	got := a.paletteSearch("entry")
	if len(got.entries) != paletteResultLimit || got.more != 80-paletteResultLimit {
		t.Fatalf("broad search = %d entries, %d more", len(got.entries), got.more)
	}
	if got.entries[0].label != "Entry 79" {
		t.Fatalf("recent row is not first: %q", got.entries[0].label)
	}
}

func TestPaletteSearchLimitsBroadResults(t *testing.T) {
	a := workspaceApp()
	a.requests = make([]savedRequest, 80)
	for i := range a.requests {
		a.requests[i] = savedRequest{ID: fmt.Sprintf("r%d", i), Name: fmt.Sprintf("Entry %02d", i), Method: "GET", URL: "https://api.example.com/entry"}
	}
	a.requestGen++
	got := a.paletteSearch("entry")
	if len(got.entries) != paletteResultLimit || got.more != 80-paletteResultLimit {
		t.Fatalf("broad search = %d entries, %d more", len(got.entries), got.more)
	}
	if got.entries[0].label != "Entry 00" || got.entries[len(got.entries)-1].label != "Entry 49" {
		t.Fatalf("broad search kept the wrong prefix: %q .. %q", got.entries[0].label, got.entries[len(got.entries)-1].label)
	}
}

func TestCommandPaletteShowsBroadResultOverflow(t *testing.T) {
	a := workspaceApp()
	a.requests = make([]savedRequest, 80)
	for i := range a.requests {
		a.requests[i] = savedRequest{ID: fmt.Sprintf("r%d", i), Name: fmt.Sprintf("Entry %02d", i), Method: "GET", URL: "https://api.example.com/entry"}
	}
	a.requestGen++
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("entry")
	if !tt.HasText("30 more results · narrow the search") {
		t.Fatalf("missing overflow count: %v", tt.Texts())
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

// TestPaletteHighlightSnapshots uses the repository's Tester/screenshot
// workflow. Set VSCROXY_PALETTE_SNAPSHOTS to an absolute output directory to
// capture the matched label on selected and unselected rows in both themes.
func TestPaletteHighlightSnapshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_PALETTE_SNAPSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_PALETTE_SNAPSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		selected := "unselected"
		if dark {
			appearance = "dark"
		}
		for _, rowSelected := range []bool{false, true} {
			if rowSelected {
				selected = "selected"
			}
			t.Run(appearance+"-"+selected, func(t *testing.T) {
				a := workspaceApp()
				a.appearance = appearance
				a.requests = append(a.requests,
					savedRequest{ID: "get", Name: "Get User", Method: "GET", URL: "https://api.example.com/users/1"},
					savedRequest{ID: "get2", Name: "Get user details", Method: "GET", URL: "https://api.example.com/users/2"},
				)
				a.requestGen++
				tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1100, 800)
				tt.SetScale(2)
				tt.Key(ui.Cmd, ui.KeyK)
				tt.Type("getusr")
				a.workbench.command = 0
				if rowSelected {
					a.workbench.command = 1
				}
				tt.Frame()
				if !tt.HasText("Get User") {
					t.Fatalf("missing highlighted row: %v", tt.Texts())
				}
				var b bytes.Buffer
				if err := png.Encode(&b, tt.Image()); err != nil {
					t.Fatal(err)
				}
				name := "palette-highlight-" + appearance + "-" + selected + ".png"
				if err := os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			})
		}
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

func BenchmarkPaletteSearch5000RequestsBroad(b *testing.B) {
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
	a.paletteSearch("warm the index")
	b.ResetTimer()
	for b.Loop() {
		got := a.paletteSearch("request")
		if len(got.entries) != paletteResultLimit || got.more != 5000-paletteResultLimit {
			b.Fatalf("broad search = %d entries, %d more", len(got.entries), got.more)
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
