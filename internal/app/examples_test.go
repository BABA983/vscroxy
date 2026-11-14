package app

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
)

// A response saved as an example is kept with the request as it was
// authored, under the headers its decoded body still matches, and saving it
// never takes unrelated draft edits along.
func TestSaveAsExampleKeepsTheResponseWithTheAuthoredRequest(t *testing.T) {
	a := workspaceApp()
	a.library.root = filepath.Join(t.TempDir(), "collections")
	a.page = pageRequest
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
		list, err := loadLibraryRequests(a.library.root)
		if err != nil || len(list) != 1 {
			t.Fatalf("library = %+v, %v", list, err)
		}
		var names []string
		for _, e := range list[0].Examples {
			names = append(names, e.Name)
		}
		return names
	}

	respond()
	saveAsExample(t, tt)
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
	saveAsExample(t, tt)
	if got := saved(); !slices.Equal(got, []string{"201 Created"}) {
		t.Fatalf("an edited draft was saved with its example: %q", got)
	}
	if len(d.examples) != 2 || d.examples[1].Name != "201 Created 2" || !d.dirty() {
		t.Fatalf("draft examples = %+v, dirty = %v", d.examples, d.dirty())
	}

	choosePaneView(t, tt, "Request pane", "Examples")
	if !tt.HasText("GET {{base}}/projects") {
		t.Fatalf("the Examples tab does not show the example's request: %q", tt.Texts())
	}
}

// An example's body and headers are edited in the Examples tab. The edit is
// the draft's change, so Save keeps it, and the request view stays read-only.
func TestExampleBodyAndHeadersEditInPlace(t *testing.T) {
	a := workspaceApp()
	a.library.root = filepath.Join(t.TempDir(), "collections")
	a.page = pageRequest
	tt := ui.NewTester(a.view, 1280, 760)
	if err := tt.Click("List projects"); err != nil {
		t.Fatal(err)
	}
	d := a.requestDocument
	d.examples = []request.Example{{ID: "e1", Name: "201 Created", Status: 201,
		Headers: "Content-Type: text/plain", Body: "kept",
		Request: request.ExampleRequest{Method: "GET", URL: "{{base}}/projects"}}}
	choosePaneView(t, tt, "Request pane", "Examples")
	tt.Frame()
	// focus clicks the example's editor, found by its accessible name.
	// segment picks the example's Body, Headers or Request view; the request
	// pane has tabs of the same names, so it is found by its bounds.
	segment := func(name string) {
		t.Helper()
		for _, n := range tt.AccessAll() {
			if n.Name == name && n.Bounds.X > 600 {
				tt.ClickAt(n.Bounds.X+n.Bounds.W/2, n.Bounds.Y+n.Bounds.H/2)
				tt.Frame()
				return
			}
		}
		t.Fatalf("no %s view for the example", name)
	}
	focus := func() {
		t.Helper()
		info, ok := tt.AccessByRole(ui.RoleTextField, "Example 201 Created")
		if !ok {
			t.Fatal("no editor for the example")
		}
		tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
		tt.Frame()
	}

	focus()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("edited body")
	tt.Frame()
	if d.examples[0].Body != "edited body" || !d.dirty() {
		t.Fatalf("body edit: example body %q, dirty %v", d.examples[0].Body, d.dirty())
	}

	segment("Headers")
	focus()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Content-Type: application/json")
	tt.Frame()
	if d.examples[0].Headers != "Content-Type: application/json" {
		t.Fatalf("headers edit: example headers %q", d.examples[0].Headers)
	}

	segment("Request")
	if d.exampleEditor == nil || !d.exampleEditor.ReadOnly {
		t.Fatal("the request view of an example is editable")
	}
}
