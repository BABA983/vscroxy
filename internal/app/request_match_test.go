package app

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
)

func TestCapturedRequestMatchingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, template, method, target string
		want                           bool
	}{
		{"literal", "https://api.example.com/projects/73", "GET", "https://api.example.com/projects/73?token=ignored", true},
		{"literal is not wildcard", "https://api.example.com/projects/73", "GET", "https://api.example.com/projects/74", false},
		{"mustache segment", "{{base}}/projects/{{id}}", "GET", "https://api.example.com/v1/projects/73", true},
		{"colon segment", "{{base}}/projects/:id", "GET", "https://api.example.com/v1/projects/73", true},
		{"brace segment", "{{base}}/projects/{id}", "GET", "https://api.example.com/v1/projects/73", true},
		{"cross host", "{{base}}/projects/{{id}}", "GET", "https://other.example.com/v1/projects/73", false},
		{"cross scheme", "{{base}}/projects/{{id}}", "GET", "http://api.example.com/v1/projects/73", false},
		{"cross port", "{{base}}/projects/{{id}}", "GET", "https://api.example.com:8443/v1/projects/73", false},
		{"default port", "{{base}}/projects/{{id}}", "GET", "https://API.EXAMPLE.COM:443/v1/projects/73", true},
		{"cross method", "{{base}}/projects/{{id}}", "POST", "https://api.example.com/v1/projects/73", false},
		{"no prefix match", "{{base}}/projects/{{id}}", "GET", "https://api.example.com/v1/projects/73/members", false},
		{"no suffix match", "{{base}}/projects/{{id}}", "GET", "https://api.example.com/other/v1/projects/73", false},
		{"no empty parameter", "{{base}}/projects/{{id}}", "GET", "https://api.example.com/v1/projects/", false},
		{"trailing slash literal", "{{base}}/projects/{{id}}", "GET", "https://api.example.com/v1/projects/73/", false},
		{"embedded unknown is not wildcard", "{{base}}/projects/prefix-{{missing}}", "GET", "https://api.example.com/v1/projects/prefix-73", false},
		{"embedded known is literal", "{{base}}/projects/prefix-{{id}}", "GET", "https://api.example.com/v1/projects/prefix-fixed", true},
		{"encoded parameter is literal", "{{base}}/projects/%7Bid%7D", "GET", "https://api.example.com/v1/projects/73", false},
		{"encoded literal matches", "{{base}}/projects/%7Bid%7D", "GET", "https://api.example.com/v1/projects/%7Bid%7D", true},
		{"encoded slash boundary", "{{base}}/projects/:id/members", "GET", "https://api.example.com/v1/projects/73%2Fmembers", false},
		{"unknown origin", "{{missing}}/projects/{{id}}", "GET", "https://api.example.com/v1/projects/73", false},
		{"unknown host", "https://{{missing}}/projects/{{id}}", "GET", "https://api.example.com/projects/73", false},
		{"vault host", "https://{{vault:host}}/projects/{{id}}", "GET", "https://api.example.com/projects/73", false},
		{"scheme and host variables", "{{scheme}}://{{host}}/projects/{id}", "GET", "https://api.example.com/projects/73", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := workspaceApp()
			a.requests = []savedRequest{{Name: "Get project", Collection: "Shop/Projects", Method: tc.method, URL: tc.template}}
			a.globals = map[string]string{"base": "https://global.example.com"}
			a.folders = []request.Folder{
				{Path: "Shop", Variables: []request.Field{{Name: "base", Value: "https://collection.example.com"}}},
				{Path: "Shop/Projects", Variables: []request.Field{{Name: "base", Value: "https://folder.example.com"}}},
			}
			a.activeEnv = "Local"
			a.envs = []environment{{Name: "Local", Vars: map[string]string{
				"base": "https://api.example.com/v1", "id": "fixed", "scheme": "https", "host": "api.example.com",
			}}}
			got := a.matchingRequests(flow{method: "GET", url: tc.target})
			if (len(got) == 1) != tc.want {
				t.Fatalf("matches = %+v, want match %v", got, tc.want)
			}
			if a.vault.loaded || a.vault.open {
				t.Fatal("matching opened the vault")
			}
		})
	}

	// Folder scope wins over globals and the collection when no environment
	// overrides it. Disabled variables must not change the target host.
	a := workspaceApp()
	a.requests[0].Collection, a.requests[0].URL = "Shop/Projects", "{{base}}/projects"
	a.globals = map[string]string{"base": "https://global.example.com"}
	a.folders = []request.Folder{
		{Path: "Shop", Variables: []request.Field{{Name: "base", Value: "https://collection.example.com"}}},
		{Path: "Shop/Projects", Variables: []request.Field{{Name: "base", Value: "https://api.example.com"}, {Name: "base", Value: "https://disabled.example.com", Disabled: true}}},
	}
	if got := a.matchingRequests(a.flows[0]); len(got) != 1 {
		t.Fatalf("folder matching = %+v", got)
	}
}

func TestCapturedRequestMatchControlsShowAmbiguityAndOpenTheChosenRequest(t *testing.T) {
	a := workspaceApp()
	a.requests = []savedRequest{
		{ID: "literal", Name: "Exact project", Collection: "Workspace/API", Method: "GET", URL: "https://api.example.com/projects/73"},
		{ID: "template", Name: "Project template", Collection: "Workspace/Templates", Method: "GET", URL: "https://api.example.com/projects/:id"},
		{ID: "other-host", Name: "Other host", Method: "GET", URL: "https://other.example.com/projects/:id"},
		{ID: "other-method", Name: "Other method", Method: "POST", URL: "https://api.example.com/projects/:id"},
	}
	f := a.flows[0]
	f.path, f.url = "/projects/73", "https://api.example.com/projects/73"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			ui.Box(c).Height(400)
			a.capturedHeader(c, c.Theme(), f, a.newDocument(savedRequest{}, ""))
			ui.Text(c, "URL and Send remain below")
		})
	}, 1000, 720)
	if !tt.HasText("Matches: 2 requests") {
		t.Fatalf("ambiguous matches hidden: %q", tt.Texts())
	}
	if err := tt.Click("Matches: 2 requests"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Exact project", "Project template", "Collection: Workspace/Templates", "GET https://api.example.com/projects/:id"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q: %q", text, tt.Texts())
		}
	}
	if tt.HasText("Other host") || tt.HasText("Other method") || a.requestDocument != nil {
		t.Fatal("a non-match was offered or an ambiguous request was opened silently")
	}
	anchor, _ := tt.Find("Matches: 2 requests")
	button, ok := tt.Find("Open request: Project template")
	if !ok || button.Y+button.H > anchor.Y {
		t.Fatalf("popup did not stay above the header: anchor %+v, action %+v", anchor, button)
	}
	if err := tt.Click("Open request: Project template"); err != nil {
		t.Fatal(err)
	}
	if a.requestDocument == nil || a.savedKey != "saved:template" || a.composeURL != a.requests[1].URL || a.matchUI.open {
		t.Fatal("Open request did not open the chosen saved template")
	}
	if a.sentSet || a.composeWaiting || a.vault.loaded {
		t.Fatal("opening a match sent the request or opened the vault")
	}
}

func TestCapturedRequestMatchSavesExampleWithoutSendingOrSavingDraftEdits(t *testing.T) {
	a := workspaceApp()
	a.library.root = filepath.Join(t.TempDir(), "collections")
	a.requests[0].ID, a.requests[0].URL = "projects", "{{base}}/projects/{{id}}"
	a.requests[0].Headers, a.requests[0].Body = "Authorization: Bearer {{vault:token}}", "{{payload}}"
	a.envs, a.activeEnv = []environment{{Name: "Local", Vars: map[string]string{"base": "https://api.example.com"}}}, "Local"
	a.openSaved(a.requests[0].key())
	d := a.requestDocument
	d.composeName = "An unrelated unsaved edit"
	f := a.flows[0]
	f.path, f.url = "/projects/73", "https://api.example.com/projects/73?expanded=secret"
	f.reqHeader, f.reqBody = http.Header{"Authorization": {"Bearer captured-secret"}}, "captured-secret"
	f.resBody, f.resSize = `{"id":73}`, len(`{"id":73}`)
	f.respHeader = http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}, "Content-Length": {"29"}}
	a.flows[0] = f
	a.inspectCapture(f.id)
	a.page = pageLive
	tt := ui.NewTester(a.view, 1280, 760)
	if err := tt.Click("Matches: List projects"); err != nil {
		t.Fatal(err)
	}
	urlBar, urlOK := tt.Find("URL")
	popup, popupOK := tt.Find("Matches method + full path segments on the current scheme and host. Query ignored; not a response comparison.")
	if !urlOK || !popupOK || popup.Y < urlBar.Y+urlBar.H {
		t.Fatalf("flipped popup covers URL/Send: URL %+v, popup %+v", urlBar, popup)
	}
	if err := tt.Click("Save response as example: List projects"); err != nil {
		t.Fatal(err)
	}
	list, err := loadLibraryRequests(a.library.root)
	if err != nil || len(list) != 1 || len(list[0].Examples) != 1 {
		t.Fatalf("saved library = %+v, %v", list, err)
	}
	e := list[0].Examples[0]
	if e.Request.URL != a.requests[0].URL || e.Request.Headers != a.requests[0].Headers || e.Request.Body != "{{payload}}" ||
		e.Body != f.resBody || e.Status != 200 || e.Headers != "Content-Type: application/json" {
		t.Fatalf("example did not keep template and decoded response: %+v", e)
	}
	if list[0].Name != "List projects" || d.composeName != "An unrelated unsaved edit" || !d.dirty() || len(d.examples) != 1 {
		t.Fatal("saving the example saved/lost unrelated draft edits or left the open editor stale")
	}
	if d.sentSet || d.composeWaiting || a.requestDocument.sentSet || a.requestDocument.composeWaiting || a.vault.loaded || a.matchUI.open {
		t.Fatal("save sent a request, opened the vault, or did not close the popup")
	}
}

func TestCapturedExampleRefusesIncompleteBinaryAndConflictingWrites(t *testing.T) {
	for _, kind := range []string{"incomplete", "missing bytes", "binary", "too large", "conflict"} {
		t.Run(kind, func(t *testing.T) {
			a := workspaceApp()
			a.library.root = filepath.Join(t.TempDir(), "collections")
			if err := a.persistRequests(a.requests); err != nil {
				t.Fatal(err)
			}
			f := a.flows[0]
			switch kind {
			case "incomplete":
				f.resError = "unexpected EOF"
			case "missing bytes":
				f.resSize = len(f.resBody) + 10
			case "binary":
				f.resBody = string([]byte{0xff})
			case "too large":
				f.resSize = request.MaxExampleBody + 1
			case "conflict":
				for name := range a.library.known {
					if strings.HasSuffix(name, ".json") {
						if err := os.WriteFile(filepath.Join(a.library.root, filepath.FromSlash(name)), []byte("externally edited"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if a.saveCapturedExample(f, a.requests[0].key()) || slices.ContainsFunc(a.requests, func(r savedRequest) bool { return len(r.Examples) != 0 }) {
				t.Fatal("an invalid response or conflicting library write changed the saved request")
			}
		})
	}
}
