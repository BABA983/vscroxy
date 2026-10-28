package app

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
)

// A response saved as an example is kept with the request as it was
// authored, under the headers its decoded body still matches, and saving it
// never takes unrelated draft edits along.
func TestSaveAsExampleKeepsTheResponseWithTheAuthoredRequest(t *testing.T) {
	a := workspaceApp()
	a.library.root = filepath.Join(t.TempDir(), "collections")
	a.workspace = 1
	a.requests[0].URL = "{{base}}/projects"
	a.requests[0].Headers = "Authorization: Bearer {{vault:token}}"
	tt := ui.NewTester(a.view, 1280, 760)
	if err := tt.Click("List projects"); err != nil {
		t.Fatal(err)
	}
	d := a.requestDocument
	respond := func() {
		d.sent, d.sentAt, d.sentSet = d.draft(), time.Now(), true
		d.response = &capture.Flow{Status: 201, Bytes: 8, ResponseBody: `{"id":7}`, ResponseBodySize: 8, ResponseHeader: http.Header{
			"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}, "Content-Length": {"28"}, "Connection": {"close"},
		}}
		d.responseArrived()
		tt.Frame()
	}
	saved := func() []string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(a.library.root, "Workspace", "API", "List projects.request.json"))
		if err != nil {
			t.Fatal(err)
		}
		list, err := parseRequests([]byte("[" + string(data) + "]"))
		if err != nil || len(list) != 1 {
			t.Fatalf("request file = %s, %v", data, err)
		}
		var names []string
		for _, e := range list[0].Examples {
			names = append(names, e.Name)
		}
		return names
	}

	respond()
	if err := tt.Click("Save as example"); err != nil {
		t.Fatal(err)
	}
	if got := saved(); !slices.Equal(got, []string{"201 Created"}) {
		t.Fatalf("saved examples = %q", got)
	}
	e := d.examples[0]
	if e.Request.URL != "{{base}}/projects" || e.Request.Headers != "Authorization: Bearer {{vault:token}}" || e.Status != 201 || e.Body != `{"id":7}` {
		t.Fatalf("example = %+v", e)
	}
	if e.Headers != "Content-Type: application/json" {
		t.Fatalf("example headers = %q, want the decoded body's headers only", e.Headers)
	}
	if d.dirty() || !slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.HasPrefix(s, "Draft matches last attempt") }) {
		t.Fatalf("saving an example left the draft dirty or unsent: %q", tt.Texts())
	}

	// A draft with other edits keeps the example until the request is saved.
	d.composeName = "List all projects"
	respond()
	if err := tt.Click("Save as example"); err != nil {
		t.Fatal(err)
	}
	if got := saved(); !slices.Equal(got, []string{"201 Created"}) {
		t.Fatalf("an edited draft was saved with its example: %q", got)
	}
	if len(d.examples) != 2 || d.examples[1].Name != "201 Created 2" || !d.dirty() {
		t.Fatalf("draft examples = %+v, dirty = %v", d.examples, d.dirty())
	}

	if err := tt.Click("Request editor: Examples (2)"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("GET {{base}}/projects") {
		t.Fatalf("the Examples tab does not show the example's request: %q", tt.Texts())
	}
}
