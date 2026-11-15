package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
)

func TestPromoteExampleToSnapshot(t *testing.T) {
	a := workspaceApp()
	a.openSaved(a.requests[0].key())
	a.composeTab = composeTabExamples
	d := a.requestDocument
	d.examples = []request.Example{{
		ID: "example-1", Name: "Rex", Status: 201,
		Headers: "Content-Type: application/json\nDate: Mon, 01 Jan 2026 00:00:00 GMT\nX-Trace: keep",
		Body:    `{"id":1,"name":"Rex"}`,
	}}
	d.example = 0
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("Use as snapshot"); err != nil {
		t.Fatal(err)
	}
	snapshot := d.checks.Snapshot
	if snapshot == nil || snapshot.Body != `{"id":1,"name":"Rex"}` || d.checks.Status != "201" {
		t.Fatalf("snapshot = %+v status = %q", snapshot, d.checks.Status)
	}
	if strings.Contains(d.checks.Headers, "Date") || !strings.Contains(d.checks.Headers, "X-Trace") {
		t.Fatalf("headers = %q", d.checks.Headers)
	}
}

func TestLearnSnapshotVaryingFieldsFromCaptures(t *testing.T) {
	a := workspaceApp()
	a.openSaved(a.requests[0].key())
	d := a.requestDocument
	d.checks.Snapshot = &request.Snapshot{Body: `{"id":1,"name":"Rex"}`}
	a.flows = nil
	a.flows = append(a.flows,
		flow{id: 101, method: "GET", url: "https://api.example.com/projects", status: 200, resBody: `{"id":2,"name":"Rex"}`, resSize: len(`{"id":2,"name":"Rex"}`)},
		flow{id: 102, method: "GET", url: "https://api.example.com/projects?page=2", status: 200, resBody: `{"id":3,"name":"Rex"}`, resSize: len(`{"id":3,"name":"Rex"}`)},
	)
	a.learnSnapshotVary(d)
	if !slices.Contains(d.checks.Snapshot.Vary, "/id") || slices.Contains(d.checks.Snapshot.Vary, "/name") {
		t.Fatalf("vary = %q", d.checks.Snapshot.Vary)
	}
	d.checks.Snapshot.VaryMode = request.SnapshotVaryType
	results := d.checks.Evaluate(capture.Flow{ResponseBody: `{"id":"three","name":"Rex"}`, ResponseBodySize: len(`{"id":"three","name":"Rex"}`)})
	if len(results) != 1 || results[0].Pass || !strings.Contains(results[0].Error, "/id: expected number, got string") {
		t.Fatalf("results = %+v", results)
	}
}

func TestSnapshotPathMatchesTemplates(t *testing.T) {
	for _, tc := range []struct {
		template, actual string
		want             bool
	}{
		{"/pets/{{id}}", "/pets/7", true},
		{"/pets/:id", "/pets/7", true},
		{"/pets/{id}", "/pets/7", true},
		{"/pets/7", "/pets/7", true},
		{"/pets/{{id}}", "/owners/7", false},
		{"/pets/{{id}}", "/pets", false},
	} {
		if got := pathMatches(tc.template, tc.actual); got != tc.want {
			t.Errorf("pathMatches(%q, %q) = %v", tc.template, tc.actual, got)
		}
	}
}

func TestRequestPathStripsBaseAndQuery(t *testing.T) {
	for _, tc := range [][2]string{
		{"{{base}}/pets/7?page=2", "/pets/7"},
		{"https://api.example.com/v1/pets/7", "/v1/pets/7"},
		{"https://api.example.com", "/"},
	} {
		if got := requestPath(tc[0]); got != tc[1] {
			t.Errorf("requestPath(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}
