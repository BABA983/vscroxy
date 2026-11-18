package app

import (
	"bytes"
	"fmt"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/fuzzy"
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
		{"post", []string{"Create order", "List products", "Orders"}},
		{"status.example.com", []string{"Health"}},
		{"base_url", []string{"base_url", "base_url"}},
		{"current_only", []string{"current_only"}},
		{"global_current", []string{"global_current"}},
		{"local", []string{"Local", "secret", "base_url", "current_only"}},
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

// paletteCanMatch must never reject a real score: ScoreItemFuzzy can score
// description+label in VS Code's order, which is not the prefilter's old
// label+detail concatenation.
func TestPaletteCanMatchNeverRejectsScoreItemFuzzy(t *testing.T) {
	queries := []string{"getuser", "accountsprofile", "billinglist"}
	items := []paletteScoreItem{
		{label: "User", detail: "Get"},
		{label: "Profile", detail: "GET https://api.example.com/v1 · Accounts"},
		{label: "List", detail: "POST /x · Billing"},
	}

	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	sourceAlphabet := alphabet + " /·"
	rng := rand.New(rand.NewSource(7))
	word := func(prefix string, min, max int) string {
		var b strings.Builder
		b.WriteString(prefix)
		for range min + rng.Intn(max-min+1) {
			alphabet := alphabet
			if prefix != "" {
				alphabet = sourceAlphabet
			}
			b.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}
		return b.String()
	}
	subsequence := func(source string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(source) {
			if r >= 'a' && r <= 'z' && rng.Intn(3) != 0 {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	for range 3000 {
		label := word("", 2, 9)
		detail := word("d", 2, 12)
		path := ""
		if rng.Intn(4) == 0 {
			path = word("p", 2, 8) + "/x"
		}
		source := label
		switch rng.Intn(3) {
		case 1:
			source = detail + label
		case 2:
			if path != "" {
				source = path
			}
		}
		query := subsequence(source)
		if query == "" {
			continue
		}
		items = append(items, paletteScoreItem{label: label, detail: detail, path: path})
		queries = append(queries, query)
	}

	positive := 0
	for i, item := range items {
		prepared := fuzzy.PrepareQuery(queries[i])
		score := fuzzy.ScoreItemFuzzy(item, prepared, true, paletteAccessor{}, fuzzy.NewFuzzyScorerCache())
		if i < 3 && score.Score == 0 {
			t.Fatalf("review case %d (%q on %+v) did not score", i, queries[i], item)
		}
		if i < 3 {
			filtered := scorePaletteItem(item, prepared, fuzzy.NewFuzzyScorerCache())
			if filtered.Score != score.Score {
				t.Fatalf("review case %d prefilter changed score from %d to %d", i, score.Score, filtered.Score)
			}
		}
		if score.Score == 0 {
			continue
		}
		positive++
		if !paletteCanMatch(item, prepared) {
			t.Fatalf("prefilter rejected a match: item=%+v query=%q score=%d", item, queries[i], score.Score)
		}
	}
	if positive < 100 {
		t.Fatalf("random sweep produced only %d positive matches", positive)
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

func matchedStrings(text string, matches []fuzzy.Match) []string {
	runes := []rune(text)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if match.Start < 0 || match.End > len(runes) || match.Start >= match.End {
			out = append(out, "<invalid>")
			continue
		}
		out = append(out, string(runes[match.Start:match.End]))
	}
	return out
}

func TestPaletteDetailHighlightsDescription(t *testing.T) {
	a := paletteTestApp()
	a.requests = append(a.requests,
		savedRequest{
			ID: "get", Name: "Get User", Collection: "Accounts", Method: "GET",
			URL: "https://api.example.com/users/1",
		},
	)
	a.requestGen++

	byURL := a.paletteSearchEntries("users/1")
	if len(byURL) == 0 {
		t.Fatal("no request matched users/1")
	}
	entry := byURL[0]
	if got := matchedStrings(entry.detail, entry.detailMatches); !slices.Equal(got, []string{"users/1"}) {
		t.Fatalf("url detail highlights = %v from %q (entry %+v)", got, entry.detail, entry)
	}

	for _, entry := range a.paletteSearchEntries("accounts get user") {
		if entry.label != "Get User" {
			continue
		}
		labels := matchedStrings(entry.label, entry.matches)
		details := matchedStrings(entry.detail, entry.detailMatches)
		if len(labels) == 0 || !slices.Contains(details, "Accounts") {
			t.Fatalf("multi-piece highlights = label %v detail %v from %q", labels, details, entry.detail)
		}
		return
	}
	t.Fatal("Get User did not match accounts get user")
}

func TestPaletteDetailMatchesStayWithinDisplayedDetail(t *testing.T) {
	a := paletteTestApp()
	for _, query := range []string{"shop", "orders", "local", "base_url", "health", "get"} {
		for _, entry := range a.paletteSearchEntries(query) {
			if got := matchedStrings(entry.detail, entry.detailMatches); slices.Contains(got, "<invalid>") {
				t.Fatalf("detail offsets outside %q for %q: %v (%v)", entry.detail, query, got, entry.detailMatches)
			}
			if got := matchedStrings(entry.label, entry.matches); slices.Contains(got, "<invalid>") {
				t.Fatalf("label offsets outside %q for %q: %v (%v)", entry.label, query, got, entry.matches)
			}
		}
	}
}

func TestPaletteFuzzyUsesVSCodeComparator(t *testing.T) {
	a := paletteTestApp()
	a.requests = append(a.requests,
		savedRequest{ID: "get", Name: "Get User", Method: "GET", URL: "https://api.example.com/users/1"},
		savedRequest{ID: "get2", Name: "Get user details", Method: "GET", URL: "https://api.example.com/users/2"},
	)
	a.requestGen++

	// VS Code's scoreFuzzy normalizes a match that ends on the last rune, so
	// "Get User" scores 65582 against "Get user details" at 65583. The ported
	// comparator ranks that one-point higher score first before considering
	// the shorter label, so this is the correct quick-open order.
	got := paletteLabels(a.paletteSearchEntries("getusr"))
	if len(got) < 2 || got[0] != "Get user details" || got[1] != "Get User" {
		t.Fatalf("getusr order = %v", got)
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
// capture label and detail highlights on selected and unselected rows in
// both themes.
func TestPaletteHighlightSnapshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_PALETTE_SNAPSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_PALETTE_SNAPSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	scenes := []struct {
		name, query, want string
		setup             func(*app)
	}{
		{
			name: "label", query: "getusr", want: "Get User",
			setup: func(a *app) {
				a.requests = append(a.requests,
					savedRequest{ID: "get", Name: "Get User", Method: "GET", URL: "https://api.example.com/users/1"},
					savedRequest{ID: "get2", Name: "Get user details", Method: "GET", URL: "https://api.example.com/users/2"},
				)
			},
		},
		{
			name: "url", query: "users/1", want: "users/1",
			setup: func(a *app) {
				a.requests = append(a.requests,
					savedRequest{ID: "one", Name: "Get user one", Method: "GET", URL: "https://api.example.com/users/1"},
					savedRequest{ID: "ten", Name: "Get user ten", Method: "GET", URL: "https://api.example.com/users/10"},
				)
			},
		},
	}
	for _, scene := range scenes {
		for _, dark := range []bool{false, true} {
			appearance := "light"
			if dark {
				appearance = "dark"
			}
			for _, rowSelected := range []bool{false, true} {
				state := "unselected"
				if rowSelected {
					state = "selected"
				}
				t.Run(scene.name+"-"+appearance+"-"+state, func(t *testing.T) {
					a := workspaceApp()
					a.appearance = appearance
					scene.setup(a)
					a.requestGen++
					tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1100, 800)
					tt.SetScale(2)
					tt.Key(ui.Cmd, ui.KeyK)
					tt.Type(scene.query)
					a.workbench.command = 0
					if rowSelected {
						a.workbench.command = 1
					}
					tt.Frame()
					if !tt.HasText(scene.want) {
						t.Fatalf("missing %q: %v", scene.want, tt.Texts())
					}
					var b bytes.Buffer
					if err := png.Encode(&b, tt.Image()); err != nil {
						t.Fatal(err)
					}
					name := "palette-highlight-" + scene.name + "-" + appearance + "-" + state + ".png"
					if err := os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o644); err != nil {
						t.Fatal(err)
					}
				})
			}
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
