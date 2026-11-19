package app

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/async"
	"github.com/baba983/vscroxy/internal/vault"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func globalSearchTestApp() *app {
	a := workspaceApp()
	a.clock = async.NewFakeClock()
	a.requests = []savedRequest{{
		ID: "find-1", Name: "Findme Request", Collection: "Workspace/Orders", Method: "POST",
		URL: "https://api.example.com/findme", Headers: "X-Findme: 1",
		Body: `{"findme":true}`, Pre: `pm.environment.set("findme", 1)`,
		Post: `console.log("findme")`, Description: "findme documentation",
	}}
	a.envs = []environment{{Name: "Findme Local", Vars: map[string]string{"findme_var": "findme-value"},
		Current: map[string]string{"findme_current": "local-value"}}}
	a.globals = map[string]string{"findme_global": "global-value"}
	a.flows = []flow{
		{id: 1, method: "GET", url: "https://api.example.com/findme-flow", host: "api.example.com", path: "/findme-flow",
			reqHeaders: "X-Findme: 1", reqHeader: http.Header{"X-Findme": {"1"}},
			reqBody: "{\"findme\":1}", respHeader: http.Header{}, resHeaders: "X-Findme: 2", resBody: "{\"findme\":2}"},
	}
	a.recount()
	a.applyFilter()
	return a
}

type searchRun struct {
	results []searchResult
	batches int
	limited bool
}

// runSearch drives the real scanner through its channels without goroutine
// sleeps: the context and the channels are all the test needs to observe it.
func runSearch(t *testing.T, s searchSnapshot) searchRun {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sink := make(chan searchBatch, 64)
	done := make(chan struct{})
	go func() { scanSearch(ctx, s, 1, sink, nil); close(done) }()
	var out searchRun
	collect := func(b searchBatch) {
		out.batches++
		out.results = append(out.results, b.results...)
		if b.limited > 0 {
			out.limited = true
		}
	}
	for {
		select {
		case b := <-sink:
			collect(b)
		case <-done:
			for {
				select {
				case b := <-sink:
					collect(b)
				default:
					return out
				}
			}
		}
	}
}

func scanQuery(t *testing.T, a *app, query string) searchRun {
	t.Helper()
	q, err := compileSearchQuery(query, &a.searchAll)
	if err != nil {
		t.Fatalf("compile %q: %v", query, err)
	}
	return runSearch(t, a.searchSnapshot(q))
}

func TestGlobalSearchFindsEverySourceAndField(t *testing.T) {
	a := globalSearchTestApp()
	got := scanQuery(t, a, "findme")
	bySource := map[searchSource][]string{}
	for _, r := range got.results {
		bySource[r.source] = append(bySource[r.source], r.field)
		if r.start < 0 || r.end > len(r.text) || r.text[r.start:r.end] == "" {
			t.Errorf("result %+v has no match range", r)
		}
	}
	want := map[searchSource][]string{
		searchCollections:  {locales.Name(), locales.Url(), locales.Headers(), locales.RequestBody(), locales.PreRequestScript(), locales.PostResponseScript(), locales.Description()},
		searchEnvironments: {locales.Name(), locales.Initial(), locales.Current(), locales.GlobalVariable()},
		searchFlows:        {locales.Url(), locales.RequestHeaders(), locales.ResponseHeaders(), locales.RequestBody(), locales.ResponseBody()},
	}
	for source, fields := range want {
		for _, field := range fields {
			if !containsString(bySource[source], field) {
				t.Errorf("source %d has no %q field: %v", source, field, bySource[source])
			}
		}
	}
	if got.batches < 2 {
		t.Errorf("search delivered %d batches, want streaming in several", got.batches)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestGlobalSearchOptionsChangeMatches(t *testing.T) {
	a := globalSearchTestApp()
	a.requests = []savedRequest{{ID: "case", Name: "Error errors", Method: "GET", URL: "https://case.test/error",
		Body: "Error\nerror\nerrors\n"}}
	plain := scanQuery(t, a, "error")
	if len(plain.results) != 5 {
		t.Fatalf("case-insensitive matches = %d, want 5", len(plain.results))
	}

	a.searchAll.matchCase = true
	strict := scanQuery(t, a, "error")
	if len(strict.results) != 4 {
		t.Errorf("match case = %d, want 4", len(strict.results))
	}
	a.searchAll.matchCase = false

	a.searchAll.wholeWord = true
	whole := scanQuery(t, a, "error")
	if len(whole.results) != 4 {
		t.Errorf("whole word = %d, want 4", len(whole.results))
	}
	a.searchAll.wholeWord = false

	a.searchAll.regexpQuery = true
	regex := scanQuery(t, a, "err(or|ors)")
	if len(regex.results) != 5 {
		t.Errorf("regexp matches = %d, want 5", len(regex.results))
	}
	if _, err := compileSearchQuery("[", &a.searchAll); err == nil {
		t.Fatal("an invalid regular expression compiled without error")
	}
	if text := searchProblemText("missing ]"); text == "" {
		t.Fatal("an invalid regular expression has no inline explanation")
	}
	a.setSearchQuery("[")
	if a.searchAll.problem == "" {
		t.Fatal("the Search view did not keep the invalid regexp error")
	}
	a.page, a.sidebarChoice = pageSearch, "search"
	var c *ui.Context
	tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, 1000, 720)
	tt.Frame()
	if !tt.HasText(searchProblemText(a.searchAll.problem)) {
		t.Errorf("the invalid regexp error is not shown: %v", tt.Texts())
	}
	_ = c
}

func TestGlobalSearchNeverShowsVaultSecrets(t *testing.T) {
	const secret = "s3cr3t-needle"
	a := globalSearchTestApp()
	a.vault.secrets = vault.Secrets{{Name: "token", Value: secret}}
	a.requests[0].Name = "Build " + secret
	a.requests[0].Headers = "Authorization: Bearer " + secret
	a.envs[0].Name = "Team " + secret
	a.envs[0].Current["api_token"] = secret
	a.flows[0].url = "https://api.example.com/" + secret
	a.flows[0].reqBody = secret

	for _, r := range scanQuery(t, a, "needle").results {
		for _, text := range []string{r.text, r.resource, r.field, r.key, r.match()} {
			if strings.Contains(strings.ToLower(text), secret) {
				t.Errorf("vault secret leaked into result %+v", r)
			}
		}
	}
	if got := scanQuery(t, a, secret).results; len(got) != 0 {
		t.Errorf("%d results matched the vault secret: %+v", len(got), got)
	}
}

func TestGlobalSearchCapsResultsAndReportsIt(t *testing.T) {
	a := globalSearchTestApp()
	a.requests = nil
	a.envs = nil
	a.globals = nil
	a.flows = []flow{{id: 9, method: "GET", url: "https://cap.test/", resBody: strings.Repeat("needle line\n", 30)}}
	q, err := compileSearchQuery("needle", &a.searchAll)
	if err != nil {
		t.Fatal(err)
	}
	s := a.searchSnapshot(q)
	s.limit, s.batch = 7, 3
	got := runSearch(t, s)
	if len(got.results) != s.limit || !got.limited {
		t.Errorf("results = %d, limited = %t; want %d and true", len(got.results), got.limited, s.limit)
	}
	if got.batches < 3 {
		t.Errorf("cap still produced %d batches, want streamed pages", got.batches)
	}
}

func TestGlobalSearchDebouncesAndCancelsOldQuery(t *testing.T) {
	a := globalSearchTestApp()
	clock := a.clock.(*async.FakeClock)
	a.setSearchQuery("first")
	r := a.searchRunner()
	firstGen, firstContext := r.gen, r.ctx
	clock.Advance(globalSearchDebounce / 2)
	a.setSearchQuery("second")
	if !searchIsCanceled(firstContext.Err()) {
		t.Fatal("changing the query did not cancel the previous search")
	}
	if r.gen <= firstGen {
		t.Fatalf("generation did not advance: %d -> %d", firstGen, r.gen)
	}
	clock.Advance(globalSearchDebounce / 2)
	select {
	case <-r.starts:
		t.Fatal("an old query started after only half its debounce")
	default:
	}
	clock.Advance(globalSearchDebounce / 2)
	select {
	case <-a.changed: // the timer's completion is what posts the start
	case <-t.Context().Done():
		t.Fatal("the debounced query never started")
	}
	select {
	case gen := <-r.starts:
		if gen != r.gen {
			t.Fatalf("started generation %d, want %d", gen, r.gen)
		}
	default:
		t.Fatal("the newer generation did not start")
	}

	a.applySearchBatch(searchBatch{gen: firstGen, results: []searchResult{{text: "stale"}}})
	if len(a.searchAll.results) != 0 {
		t.Errorf("a stale batch was shown: %+v", a.searchAll.results)
	}
	a.applySearchBatch(searchBatch{gen: r.gen, done: true})
	if a.searchAll.running {
		t.Error("the done batch left the view searching")
	}
}

func TestGlobalSearchCommandsAreRegistered(t *testing.T) {
	a := globalSearchTestApp()
	var c *ui.Context
	tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, 1100, 760)
	tt.Frame()
	for _, want := range []struct {
		id, label string
		key       shortcut
	}{
		{"search.focus", locales.SearchEverywhere(), shortcut{ui.Cmd | ui.Shift, ui.KeyF}},
		{"search.next-match", locales.NextSearchMatch(), shortcut{}},
		{"search.previous-match", locales.PreviousSearchMatch(), shortcut{}},
	} {
		action := pick(a.actions(c), want.id)
		if action.id != want.id || action.label != want.label || !action.enabled || action.shortcut != want.key {
			t.Errorf("command %s = %+v, want label %q key %v", want.id, action, want.label, want.key)
		}
	}
	pick(a.actions(c), "search.focus").run()
	if a.page != pageSearch || a.sidebarChoice != "search" {
		t.Fatalf("search.focus opened page %v choice %q", a.page, a.sidebarChoice)
	}
	tt.Frame()
	tt.Key(ui.Alt, ui.KeyC)
	tt.Frame()
	if !a.searchAll.matchCase {
		t.Fatal("Alt+C did not toggle the Search view's case option through the shared command")
	}
	if x := pick(a.actions(c), "find.match-case"); !x.enabled {
		t.Fatal("the shared find toggle is unavailable while the Search field is focused")
	}
	for _, id := range []string{"find.match-whole-word", "find.use-regexp"} {
		before := a.searchAll.request()
		pick(a.actions(c), id).run()
		after := a.searchAll.request()
		if before == after {
			t.Errorf("%s did not flip its shared option", id)
		}
	}
}

func TestGlobalSearchClickOpensResult(t *testing.T) {
	a := globalSearchTestApp()
	a.page, a.sidebarChoice = pageSearch, "search"
	a.searchAll.input = "findme"
	a.searchAll.armed = a.searchAll.request()
	a.searchAll.results = []searchResult{
		{source: searchCollections, key: "saved:find-1", resource: "Workspace/Orders / Findme Request", field: locales.Name(),
			text: "Findme Request", start: 0, end: 6},
		{source: searchFlows, key: "1", resource: "GET https://api.example.com/findme-flow", field: locales.RequestBody(),
			text: "{\"findme\":1}", start: 2, end: 8, flowID: 1, side: 0},
	}
	var c *ui.Context
	tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, 1100, 760)
	tt.Frame()
	if err := tt.Click(locales.OpenSearchResult(a.searchAll.results[0].resource)); err != nil {
		t.Fatal(err)
	}
	if a.page != pageRequest || a.requestDocument == nil || a.requestDocument.savedKey != "saved:find-1" {
		t.Fatalf("request result opened page %v document %+v", a.page, a.requestDocument)
	}

	a.page, a.sidebarChoice = pageSearch, "search"
	tt.Frame()
	if err := tt.Click(locales.OpenSearchResult(a.searchAll.results[1].resource)); err != nil {
		t.Fatal(err)
	}
	if a.page != pageLive || a.pickedID != 1 {
		t.Fatalf("flow result opened page %v flow %d", a.page, a.pickedID)
	}
	// The reveal is consumed once the flow's body reader exists; until then
	// it stays queued. Either way the target is this flow's request body.
	reveal := a.searchAll.reveal
	if reveal.flowID != 0 && (reveal.flowID != 1 || reveal.side != 0) {
		t.Fatalf("flow reveal = %+v", reveal)
	}
	if reveal.flowID == 0 {
		count, _ := a.bodyText[0].FindMatches()
		if count == 0 {
			t.Fatal("the revealed flow body has no selected match")
		}
	}
	_ = c
}
