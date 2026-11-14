package app

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/mockserver"
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

// An example's status is typed in the Examples tab; New example adds one with
// no response, under the request's Content-Type; Duplicate copies the chosen
// example, so an error case needs no real response.
func TestExampleStatusNewAndDuplicateByHand(t *testing.T) {
	a := workspaceApp()
	a.library.root = filepath.Join(t.TempDir(), "collections")
	a.page = pageRequest
	tt := ui.NewTester(a.view, 1280, 760)
	if err := tt.Click("List projects"); err != nil {
		t.Fatal(err)
	}
	d := a.requestDocument
	d.composeHeaders = "Content-Type: application/json\nAccept: */*"
	d.examples = []request.Example{{ID: "e1", Name: "201 Created", Status: 201,
		Headers: "Content-Type: text/plain", Body: "kept",
		Request: request.ExampleRequest{Method: "GET", URL: "{{base}}/projects"}}}
	choosePaneView(t, tt, "Request pane", "Examples")
	tt.Frame()

	typeInto := func(name, text string) {
		t.Helper()
		info, ok := tt.AccessByRole(ui.RoleTextField, name)
		if !ok {
			t.Fatalf("no %s field: %q", name, tt.Texts())
		}
		tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
		tt.Frame()
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type(text)
		tt.Frame()
	}

	// 42 is outside 100 to 599, so the status keeps 201 until a valid one.
	typeInto("Status", "42")
	if d.examples[0].Status != 201 {
		t.Fatalf("status 42 was accepted: %d", d.examples[0].Status)
	}
	typeInto("Status", "500")
	if d.examples[0].Status != 500 {
		t.Fatalf("status edit: %d, want 500", d.examples[0].Status)
	}

	if err := tt.Click("New example"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(d.examples) != 2 || d.example != 1 {
		t.Fatalf("New example: %d examples, chosen %d", len(d.examples), d.example)
	}
	blank := d.examples[1]
	if blank.Status != 200 || blank.Body != "" || blank.Name != "200 OK" || blank.Headers != "Content-Type: application/json" {
		t.Fatalf("blank example = %+v", blank)
	}
	if d.exampleEditor == nil || d.exampleEditor.Label != "Example 200 OK" {
		t.Fatal("the editor still shows the example chosen before New example")
	}

	if err := tt.Click("Example: 201 Created"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Duplicate example"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(d.examples) != 3 || d.example != 1 {
		t.Fatalf("Duplicate: %d examples, chosen %d", len(d.examples), d.example)
	}
	dup := d.examples[1]
	if dup.ID == "e1" || dup.Name != "201 Created 2" || dup.Status != 500 || dup.Body != "kept" || dup.Headers != "Content-Type: text/plain" {
		t.Fatalf("duplicate = %+v", dup)
	}
	if d.examples[0].Name != "201 Created" || d.examples[2].Name != "200 OK" {
		t.Fatalf("duplicate moved the other examples: %q, %q", d.examples[0].Name, d.examples[2].Name)
	}
	if d.exampleEditor == nil || d.exampleEditor.Label != "Example 201 Created 2" {
		t.Fatal("the editor does not show the duplicate")
	}
}

// A collection's mock server listens on loopback, answers from the examples
// under its collection, and stops when asked.
func TestCollectionMockServesItsExamplesOnLoopback(t *testing.T) {
	a := workspaceApp()
	path := a.requests[0].Collection
	a.requests[0].Examples = []request.Example{
		{ID: "e1", Name: "201 Created", Status: 201, Body: "created", Request: request.ExampleRequest{Method: "GET", URL: "https://api.example.com/projects"}},
		{ID: "e2", Name: "404 Not Found", Status: 404, Body: "missing", Request: request.ExampleRequest{Method: "GET", URL: "https://api.example.com/projects"}},
	}
	a.startCollectionMock(path)
	m := a.mocks[path]
	if m == nil || !strings.HasPrefix(m.url, "http://127.0.0.1:") {
		t.Fatalf("mock server = %+v, want a loopback URL", m)
	}
	req, _ := http.NewRequest(http.MethodGet, m.url+"/projects", nil)
	req.Header.Set(mockserver.CodeHeader, "404")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 from the 404 example", resp.StatusCode)
	}
	a.stopCollectionMock(path)
	if _, err := http.Get(m.url + "/projects"); err == nil {
		t.Fatal("the mock server still answers after it was stopped")
	}
	if a.mocks[path] != nil {
		t.Fatal("the stopped mock server is still recorded")
	}
}
