package app

import (
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
)

func TestOpenAPISyncBothWays(t *testing.T) {
	a := workspaceApp()
	a.requests = []savedRequest{{Request: request.Request{ID: "one", Name: "List", Collection: "Shop", Method: "GET", URL: "http://api.test/items"}}}
	source := `{"openapi":"3.0.3","info":{"title":"Shop","version":"1"},"paths":{` +
		`"/items":{"get":{"summary":"Items","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}}},` +
		`"/orders":{"post":{"summary":"Order","responses":{"201":{"description":"created"}}}}}}`
	a.addAPI(apiSpec{Source: source, Collection: "Shop"}, true)
	d := a.activeAPI()
	if d == nil || d.parsed == nil {
		t.Fatalf("spec did not validate: %+v", d)
	}
	plan, err := a.planOpenAPISync(d, "to-collection")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Added != 1 || plan.Updated != 0 || plan.Unchanged != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	if !strings.Contains(plan.Diff, "+") || !strings.Contains(plan.Diff, "Order") {
		t.Fatalf("diff does not show the new operation:\n%s", plan.Diff)
	}
	if err := a.applyOpenAPISync(d, plan); err != nil {
		t.Fatal(err)
	}
	var orders *savedRequest
	for i := range a.requests {
		if a.requests[i].Collection == "Shop" && a.requests[i].Name == "Order" {
			orders = &a.requests[i]
		}
	}
	if orders == nil || orders.Method != "POST" {
		t.Fatalf("collection after sync = %+v", a.requests)
	}
	for _, r := range a.requests {
		if r.ID == "one" && r.URL != "http://api.test/items" {
			t.Fatalf("sync replaced the request's own host: %q", r.URL)
		}
	}

	// A request the spec does not describe stays in the collection and shows
	// up when the spec is regenerated.
	a.requests = append(a.requests, savedRequest{Request: request.Request{ID: "extra", Name: "Extra", Collection: "Shop", Method: "GET", URL: "http://api.test/extra"}})
	back, err := a.planOpenAPISync(d, "to-spec")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(back.Diff, "/extra") {
		t.Fatalf("regeneration diff does not mention /extra:\n%s", back.Diff)
	}
	if err := a.applyOpenAPISync(d, back); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.code.Buffer.String(), "/extra") {
		t.Fatalf("draft was not updated:\n%s", d.code.Buffer.String())
	}
	if d.parsed == nil || d.problem != "" {
		t.Fatalf("regenerated draft does not validate: %s", d.problem)
	}
}
