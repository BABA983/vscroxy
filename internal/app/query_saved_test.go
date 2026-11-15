package app

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSavedQueriesRoundTrip(t *testing.T) {
	a := workspaceApp()
	a.queriesPath = filepath.Join(t.TempDir(), "queries.json")
	if !a.saveQuery("Cheap items", "JSONPath", "$[?(@.price < 10)]") ||
		!a.saveQuery("Names", "jq", ".items[].name") {
		t.Fatal(a.queriesProblem)
	}
	if len(a.queries) != 2 || a.queries[0].Name != "Cheap items" || a.queries[1].Name != "Names" {
		t.Fatalf("queries: %+v", a.queries)
	}
	a.queries = nil
	a.loadQueries()
	if len(a.queries) != 2 || a.queries[1].Expression != ".items[].name" {
		t.Fatalf("reloaded: %+v", a.queries)
	}
	if !a.deleteQuery("Cheap items") || len(a.queries) != 1 || a.queries[0].Name != "Names" {
		t.Fatalf("after delete: %+v %s", a.queries, a.queriesProblem)
	}
	if a.saveQuery("Names", "JSONPath", "$") == false {
		t.Fatal("replacing a query failed")
	}
	if a.queries[0].Dialect != "JSONPath" || len(a.queries) != 1 {
		t.Fatalf("replacement: %+v", a.queries)
	}
	for _, bad := range []savedQuery{
		{Name: "", Dialect: "jq", Expression: "."},
		{Name: " x", Dialect: "jq", Expression: "."},
		{Name: "x", Dialect: "graphql", Expression: "."},
		{Name: "x", Dialect: "jq", Expression: ""},
	} {
		if err := validateSavedQueries([]savedQuery{bad}); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestJSONTreeShowsInlineQueryPanel(t *testing.T) {
	a := workspaceApp()
	a.flows = []flow{{id: 1, method: "GET", url: "http://example.test/", status: 200,
		resBody: `{"items":[{"price":5}]}`, respHeader: http.Header{"Content-Type": {"application/json"}}}}
	a.applyFilter()
	a.bodyFormatByType = map[string]string{"application/json": "JSON"}
	tt := ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	for _, want := range []string{"Run query", "Query name", "Saved query"} {
		if !tt.HasText(want) {
			t.Fatalf("inline query panel misses %q", want)
		}
	}
}
