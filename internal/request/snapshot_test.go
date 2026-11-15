package request

import (
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
)

func TestSnapshotEvaluateIgnoresLearnedFields(t *testing.T) {
	snapshot := Snapshot{Body: `{"id":1,"name":"Rex","tags":["a"],"owner":{"id":7,"name":"Ada"}}`}
	if problems := snapshot.Evaluate(`{"name":"Rex","tags":["a"],"owner":{"name":"Ada","id":7},"id":1}`, true); len(problems) != 0 {
		t.Fatalf("reordered body: %q", problems)
	}
	problems := snapshot.Evaluate(`{"id":2,"name":"Rex","tags":["a"],"owner":{"id":7}}`, true)
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"/id: value differs", "/owner/name: missing"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q: %s", want, joined)
		}
	}

	snapshot.Vary = []string{"/id", "/owner/name"}
	snapshot.VaryMode = SnapshotVaryIgnore
	if problems := snapshot.Evaluate(`{"id":99,"name":"Rex","tags":["a"],"owner":{"id":7,"name":"Grace"}}`, true); len(problems) != 0 {
		t.Fatalf("ignored fields: %q", problems)
	}

	snapshot.VaryMode = SnapshotVaryType
	if problems := snapshot.Evaluate(`{"id":"99","name":"Rex","tags":["a"],"owner":{"id":7,"name":"Grace"}}`, true); !strings.Contains(strings.Join(problems, "\n"), "/id: expected number, got string") {
		t.Fatalf("type-only fields: %q", problems)
	}
	if problems := snapshot.Evaluate(`{"id":99,"name":"Rex","tags":["a"],"owner":{"id":7,"name":"Grace"}}`, true); len(problems) != 0 {
		t.Fatalf("same types: %q", problems)
	}

	if problems := snapshot.Evaluate(`{}`, false); !slices.Equal(problems, []string{"response body is incomplete"}) {
		t.Fatalf("incomplete body: %q", problems)
	}
}

func TestLearnVaryingFields(t *testing.T) {
	expected := `{"id":1,"name":"Rex","items":[{"id":10},{"id":11}],"meta":{"page":1}}`
	captures := []string{
		`{"id":2,"name":"Rex","items":[{"id":20},{"id":21}],"meta":{"page":1}}`,
		`{"id":3,"name":"Rex","items":[{"id":30},{"id":31}],"meta":{"page":1}}`,
	}
	paths := LearnVaryingFields(expected, captures)
	for _, want := range []string{"/id", "/items/0/id", "/items/1/id"} {
		if !slices.Contains(paths, want) {
			t.Errorf("learned %q lacks %q", paths, want)
		}
	}
	if slices.Contains(paths, "/name") || slices.Contains(paths, "/meta/page") {
		t.Errorf("learned stable fields: %q", paths)
	}

	shorter := []string{`{"id":1,"name":"Rex","items":[{"id":10}],"meta":{"page":1}}`}
	if paths := LearnVaryingFields(expected, shorter); !slices.Contains(paths, "/items") {
		t.Errorf("array length change learned as %q", paths)
	}
	if paths := LearnVaryingFields("not json", captures); paths != nil {
		t.Errorf("non-JSON expected learned %q", paths)
	}
}

func TestChecksEvaluateAndScriptSnapshot(t *testing.T) {
	checks := Checks{Snapshot: &Snapshot{Body: `{"ok":true,"id":1}`, Vary: []string{"/id"}}}
	flow := capture.Flow{Status: 200, ResponseBody: `{"ok":true,"id":99}`, ResponseBodySize: len(`{"ok":true,"id":99}`)}
	results := checks.Evaluate(flow)
	if len(results) != 1 || !results[0].Pass || results[0].Name != "Body matches snapshot" {
		t.Fatalf("results = %+v", results)
	}
	flow.ResponseBody = `{"ok":false,"id":99}`
	results = checks.Evaluate(flow)
	if len(results) != 1 || results[0].Pass || !strings.Contains(results[0].Error, "/ok: value differs") {
		t.Fatalf("results = %+v", results)
	}
	script, err := checks.Script()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Body matches snapshot", `vary["/id"]`, "walk(want, got"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if err := (Snapshot{Body: `{}`, Vary: []string{"bad"}, VaryMode: "other"}).Validate(); err == nil {
		t.Error("invalid snapshot validated")
	}
}
