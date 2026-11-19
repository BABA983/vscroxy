package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baba983/vscroxy/internal/markers"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func TestFailedAssertionMarkerClearsOnPassingSend(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusServiceUnavailable)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(int(status.Load()))
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	d := savedRequest{Name: "Flaky", Method: "GET", URL: upstream.URL,
		Post: `pm.test("status 200", function () { pm.expect(pm.response.code).to.equal(200) });`}
	a.addDocument(d, "saved:flaky")
	doc := a.requestDocument
	resource := problemResource(doc)

	a.sendComposed()
	waitAppSignal(t, a, func() bool {
		return len(a.markers.Read(markers.Filter{Owner: problemOwnerAssertions, Resource: resource})) == 1
	}, a.drain, func() string { return "a failed assertion did not create a marker" })
	first := a.markers.Read(markers.Filter{Owner: problemOwnerAssertions, Resource: resource})
	if first[0].Severity != markers.Error || first[0].Message != locales.ProblemCheckFailed("status 200") ||
		first[0].Code != problemFieldChecks {
		t.Fatalf("assertion marker = %+v", first[0])
	}

	status.Store(http.StatusOK)
	a.sendComposed()
	waitAppSignal(t, a, func() bool {
		return !doc.composeWaiting &&
			len(a.markers.Read(markers.Filter{Owner: problemOwnerAssertions, Resource: resource})) == 0
	}, a.drain, func() string { return "a passing send did not clear the assertion marker" })
	if len(doc.composeTests) != 1 || !doc.composeTests[0].Pass {
		t.Fatalf("second send tests = %+v", doc.composeTests)
	}
}

// TestProblemResolverRunsOnlyOnChangedInputs proves a frame with unchanged
// authoring fields and scopes does not re-resolve every open request.
func TestProblemResolverRunsOnlyOnChangedInputs(t *testing.T) {
	a := workspaceApp()
	a.envs = []environment{
		{Name: "one", Vars: map[string]string{"token": "one"}},
		{Name: "two", Vars: map[string]string{"token": "two"}},
	}
	a.activeEnv = "one"
	a.addDocument(savedRequest{Name: "First", Method: "GET", URL: "https://first.test/{{token}}"}, "saved:first")
	a.addDocument(savedRequest{Name: "Second", Method: "GET", URL: "https://second.test/{{token}}"}, "saved:second")
	calls := 0
	a.problems.resolveVariables = func(r savedRequest, in request.Context, vars map[string]string) []request.ResolvedVariable {
		calls++
		return r.Request.ResolveVariables(in, vars, nil)
	}

	a.syncProblems()
	if calls != 2 {
		t.Fatalf("first pass resolved %d requests, want 2", calls)
	}
	a.syncProblems()
	a.syncProblems()
	if calls != 2 {
		t.Fatalf("unchanged frames resolved %d more requests", calls-2)
	}
	a.documents[0].composeURL = "https://changed.test/{{token}}"
	a.syncProblems()
	if calls != 3 {
		t.Fatalf("editing one request resolved %d times, want 1", calls-2)
	}
	a.syncProblems()
	if calls != 3 {
		t.Fatalf("frame after an edit resolved again: %d", calls)
	}
	a.activeEnv = "two"
	a.syncProblems()
	if calls != 5 {
		t.Fatalf("environment switch resolved %d times, want both requests", calls-3)
	}
	a.folders = []request.Folder{{Path: "Shop", Variables: []request.Variable{{Name: "region", Initial: "eu"}}}}
	a.syncProblems()
	if calls != 7 {
		t.Fatalf("inheritance change resolved %d times, want both requests", calls-5)
	}
	changed := a.documents[0]
	changed.auth = request.Auth{Type: "Bearer", Token: "{{auth_token}}"}
	a.syncProblems()
	if calls != 8 {
		t.Fatalf("auth edit resolved %d times, want 1", calls-7)
	}
	changed.formFields = []request.Field{{Name: "token", Value: "{{form_token}}"}}
	a.syncProblems()
	if calls != 9 {
		t.Fatalf("form edit resolved %d times, want 1", calls-8)
	}
	changed.composeCollection = "Other"
	a.syncProblems()
	if calls != 10 {
		t.Fatalf("collection change resolved %d times, want 1", calls-9)
	}
	changed.graphql.Query = "query { {{graphql_token}} }"
	a.syncProblems()
	if calls != 11 {
		t.Fatalf("GraphQL edit resolved %d times, want 1", calls-10)
	}
	a.syncProblems()
	if calls != 11 {
		t.Fatalf("frame after a GraphQL edit resolved again: %d", calls)
	}
}

func TestUnresolvedVariableMarkerFollowsEdits(t *testing.T) {
	a := workspaceApp()
	a.addDocument(savedRequest{Name: "Draft", Method: "GET", URL: "{{missing}}/items",
		Headers: "X-Token: {{token}}"}, "saved:draft")
	d := a.requestDocument
	resource := problemResource(d)

	a.syncProblems()
	got := a.markers.Read(markers.Filter{Owner: problemOwnerVariables, Resource: resource})
	if len(got) != 2 {
		t.Fatalf("unresolved markers = %+v, want missing and token", got)
	}
	for _, m := range got {
		if m.Severity != markers.Warning || m.Message == "" {
			t.Errorf("variable marker = %+v", m)
		}
	}
	byMessage := map[string]markers.Marker{}
	for _, m := range got {
		byMessage[m.Message] = m
	}
	missing := byMessage[locales.ProblemUnresolvedVariable("missing")]
	if missing.Range == nil || missing.Range.StartLineNumber != 1 || missing.Range.StartColumn != 1 {
		t.Errorf("URL variable was not ranged: %+v", missing)
	}
	if url := byMessage[locales.ProblemUnresolvedVariable("token")]; url.Code != "header-value-1" {
		t.Errorf("header variable code = %q", url.Code)
	}

	d.composeURL = "https://api.example.com/items"
	d.composeHeaders = "X-Token: actual"
	d.headerRows = parseHeaderRows(d.composeHeaders)
	a.syncProblems()
	if n := len(a.markers.Read(markers.Filter{Owner: problemOwnerVariables, Resource: resource})); n != 0 {
		t.Fatalf("%d variable markers remain after the edit", n)
	}

	d.composeURL = "{{missing}}/items"
	a.syncProblems()
	if n := len(a.markers.Read(markers.Filter{Owner: problemOwnerVariables, Resource: resource})); n != 1 {
		t.Fatalf("edit did not bring the variable marker back: %d", n)
	}
	// Closing the request clears what its producers left behind.
	a.closeDocument(d)
	if n := len(a.markers.Read(markers.Filter{Resource: resource})); n != 0 {
		t.Fatalf("%d markers remain after closing the request", n)
	}
}

func TestUnresolvedVariableMarkersInAuthAndForm(t *testing.T) {
	a := workspaceApp()
	a.addDocument(savedRequest{Name: "Fields", Method: "GET", URL: "https://api.example.com/items"}, "saved:fields")
	d := a.requestDocument
	resource := problemResource(d)
	read := func() []markers.Marker {
		return a.markers.Read(markers.Filter{Owner: problemOwnerVariables, Resource: resource})
	}

	d.auth = request.Auth{Type: "Bearer", Token: "{{auth_token}}"}
	a.syncProblems()
	got := read()
	if len(got) != 1 || got[0].Message != locales.ProblemUnresolvedVariable("auth_token") {
		t.Fatalf("auth variable markers = %+v", got)
	}
	d.auth = request.Auth{}
	a.syncProblems()
	if n := len(read()); n != 0 {
		t.Fatalf("%d auth variable markers remain after clearing the field", n)
	}

	d.bodyMode = "Form"
	d.formFields = []request.Field{{Name: "token", Value: "{{form_token}}"}}
	a.syncProblems()
	got = read()
	if len(got) != 1 || got[0].Message != locales.ProblemUnresolvedVariable("form_token") {
		t.Fatalf("form variable markers = %+v", got)
	}
	d.formFields = nil
	a.syncProblems()
	if n := len(read()); n != 0 {
		t.Fatalf("%d form variable markers remain after clearing the row", n)
	}
}

func TestScriptErrorMarkerComesFromLastSend(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.addDocument(savedRequest{Name: "Script", Method: "GET", URL: upstream.URL,
		Post: `throw new Error("post script broke")`}, "saved:script")
	d := a.requestDocument
	resource := problemResource(d)

	a.sendComposed()
	waitAppSignal(t, a, func() bool {
		return len(a.markers.Read(markers.Filter{Owner: problemOwnerScripts, Resource: resource})) == 1
	}, a.drain, func() string { return "a failing script did not create a marker" })
	got := a.markers.Read(markers.Filter{Owner: problemOwnerScripts, Resource: resource})
	if got[0].Code != problemFieldPost || got[0].Severity != markers.Error {
		t.Fatalf("script marker = %+v", got[0])
	}
	if !strings.Contains(got[0].Message, "post script broke") {
		t.Errorf("script message does not name the failure: %q", got[0].Message)
	}
}

func TestImportWarningMarkersFollowTheReview(t *testing.T) {
	a := workspaceApp()
	a.reportImport("Imported collection", []string{"auth was kept locally", "script was skipped"})
	a.syncProblems()
	got := a.markers.Read(markers.Filter{Owner: problemOwnerImports, Resource: problemResourceImport})
	if len(got) != 2 || got[0].Severity != markers.Warning {
		t.Fatalf("import markers = %+v", got)
	}
	a.importReport.open = false
	a.syncProblems()
	if n := len(a.markers.Read(markers.Filter{Owner: problemOwnerImports})); n != 0 {
		t.Fatalf("%d import markers remain after dismissal", n)
	}
}

func TestProblemsNavigationOrder(t *testing.T) {
	a := workspaceApp()
	a.addDocument(savedRequest{Name: "A", Method: "GET", URL: "https://a.test"}, "saved:a")
	a.addDocument(savedRequest{Name: "B", Method: "GET", URL: "https://b.test"}, "saved:b")
	aA, aB := a.documents[0], a.documents[1]
	a.markers.ChangeOne(problemOwnerVariables, problemResource(aA), []markers.Marker{
		{Severity: markers.Hint, Message: "hint A"},
		{Severity: markers.Error, Message: "error A", Code: problemFieldURL},
	})
	a.markers.ChangeOne(problemOwnerVariables, problemResource(aB), []markers.Marker{
		{Severity: markers.Warning, Message: "warning B"},
	})

	var seen []string
	selected := func() string {
		list := a.visibleProblems()
		return list[a.problems.selection].Message
	}
	for range 3 {
		a.navigateProblems(1)
		seen = append(seen, selected())
	}
	want := []string{"error A", "hint A", "warning B"}
	if !slices.Equal(seen, want) {
		t.Fatalf("next order = %v, want %v", seen, want)
	}
	a.navigateProblems(-1)
	if got := selected(); got != "hint A" {
		t.Errorf("previous = %q, want hint A", got)
	}
	a.problems.query = "warning"
	if list := a.visibleProblems(); len(list) != 1 || list[0].Message != "warning B" {
		t.Errorf("filtered problems = %+v", list)
	}
	a.problems.query, a.problems.show[0] = "", false
	if list := a.visibleProblems(); len(list) != 2 {
		t.Errorf("severity toggle left %d problems, want 2", len(list))
	}
}

func TestClickingProblemOpensItsRequest(t *testing.T) {
	a := workspaceApp()
	a.addDocument(savedRequest{Name: "First", Method: "GET", URL: "https://first.test"}, "saved:first")
	a.addDocument(savedRequest{Name: "Second", Method: "GET", URL: "https://second.test"}, "saved:second")
	first, second := a.documents[0], a.documents[1]
	first.sentSet, second.sentSet = true, true // keep both saved tabs open across frames
	if a.requestDocument != second {
		t.Fatal("fixture did not leave Second active")
	}
	a.markers.ChangeOne(problemOwnerAssertions, problemResource(first), []markers.Marker{{
		Severity: markers.Error, Message: locales.ProblemCheckFailed("from First"), Code: problemFieldChecks,
	}})
	a.openProblems()

	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	message := locales.ProblemCheckFailed("from First")
	if !tt.HasText(message) {
		t.Fatalf("problem not listed: %v", tt.Texts())
	}
	if err := tt.Click(message); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.requestDocument != first {
		t.Fatalf("clicking the marker opened %q, want First", a.requestDocument.title())
	}
	if first.responseTab != resViewTests {
		t.Errorf("assertion marker opened response tab %d, want Checks", first.responseTab)
	}
}

func TestProblemCommandsAreRegistered(t *testing.T) {
	a := workspaceApp()
	var c *ui.Context
	tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, 1000, 700)
	tt.Frame()
	for _, id := range []string{"problems.focus", "problems.next", "problems.previous"} {
		action := pick(a.actions(c), id)
		if action.label == "" {
			t.Errorf("%s is missing its palette label", id)
		}
		if action.run == nil {
			t.Errorf("%s has no action", id)
		}
	}
	if got := keybindingOf("problems.next"); got.key == 0 {
		t.Error("problems.next has no F8 keybinding")
	}
}
