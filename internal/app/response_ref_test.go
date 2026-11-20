package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/library"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func TestRecognizeResponseRefsFindsReuseAndIgnoresShortValues(t *testing.T) {
	token := "a-longer-renewal-token-123456"
	at := time.Unix(200, 0)
	flows := []flow{
		{id: 1, method: "POST", url: "https://api.example.com/login", status: 200, at: at,
			respHeader: http.Header{"Content-Type": {"application/json"}},
			resBody:    `{"token":"` + token + `","echo":"abc"}`},
		{id: 2, method: "GET", url: "https://api.example.com/profile", status: 200, at: at.Add(time.Second),
			reqHeader: http.Header{"Authorization": {"Bearer " + token}}},
		{id: 3, method: "GET", url: "https://api.example.com/items?access_token=" + token, status: 200, at: at.Add(2 * time.Second)},
		{id: 4, method: "GET", url: "https://api.example.com/short", status: 200, at: at.Add(3 * time.Second),
			reqHeader: http.Header{"X-Token": {"abc"}}},
	}
	got, err := recognizeResponseRefs(t.Context(), flows)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Suggestions) != 1 || !slices.Equal(got.Suggestions[0].Path, []string{"token"}) {
		t.Fatalf("suggestions = %+v; want one /token source", got.Suggestions)
	}
	if len(got.Suggestions[0].Consumers) != 2 {
		t.Fatalf("consumers = %+v; want the Bearer header and the query parameter", got.Suggestions[0].Consumers)
	}
	if strings.Contains(got.Suggestions[0].Consumers[0].Field, "abc") {
		t.Fatal("the short coincidental value was suggested")
	}
	if got.Suggestions[0].token != token {
		t.Fatalf("suggested token = %q", got.Suggestions[0].token)
	}
}

func TestResponseReferenceSourceLabelUsesPlural(t *testing.T) {
	en := locales.In("en-US")
	one, many := en.ResponseReferenceSourceLabel(1, 1), en.ResponseReferenceSourceLabel(1, 2)
	if !strings.Contains(one, "1 reused value") || strings.Contains(one, "reused values") {
		t.Errorf("singular label = %q", one)
	}
	if !strings.Contains(many, "2 reused values") {
		t.Errorf("plural label = %q", many)
	}
}

func TestResponseRefsSuggestionUIIsMasked(t *testing.T) {
	a := libraryApp(t)
	a.flows = tokenPreviewFlows()
	a.openTokenLinks(a.flows)
	tt := ui.NewTester(a.view, 1180, 760)
	defer tt.Frame()
	wantMask := locales.MaskedResponseValue(len("preview-secret-token-123"))
	if !tt.HasText("Response reference suggestions") || !tt.HasText(wantMask) ||
		!tt.HasText(`{{response "#1" "/data/access_token"}}`) {
		t.Fatalf("suggestion UI = %v", tt.Texts())
	}
	if tt.HasText("preview-secret-token-123") {
		t.Fatal("the suggestion UI painted the captured secret")
	}
}

func responseRefPlanApp(t *testing.T) *app {
	t.Helper()
	a := libraryApp(t)
	a.responseRefs = &request.Responses{}
	a.envsPath = filepath.Join(t.TempDir(), "environments.json")
	a.activeEnv = "Local"
	a.envs = []environment{{Name: "Local", Vars: map[string]string{}, Current: map[string]string{"preview": "preview-secret-token-123"}}}
	if err := saveEnvs(a.file(a.envsPath), envFile{Active: a.activeEnv, Envs: a.envs}); err != nil {
		t.Fatal(err)
	}
	requests := []savedRequest{
		{Request: request.Request{ID: "login", Name: "Login", Collection: "Shop", Method: "POST",
			URL: "https://api.example.com/login"}},
		{Request: request.Request{ID: "profile", Name: "Profile", Collection: "Shop", Method: "GET",
			URL: "https://api.example.com/profile", Auth: request.Auth{Type: "Bearer", Token: "{{preview}}"}}},
	}
	if err := a.persistRequests(requests); err != nil {
		t.Fatal(err)
	}
	a.flows = tokenPreviewFlows()
	a.openTokenLinks(a.flows)
	return a
}

func TestResponseRefsProposalIsPreviewOnlyUntilConfirmed(t *testing.T) {
	a := responseRefPlanApp(t)
	p, err := a.planResponseRefs(a.tokenLinks, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Expression != `{{response "Login" "/data/access_token"}}` || len(p.Consumers) != 1 {
		t.Fatalf("proposal = %+v", p)
	}
	if strings.Contains(p.Expression, "preview-secret-token-123") || strings.Contains(p.Expression, "//") {
		t.Fatal("the secret or a comment reached the expression")
	}
	if a.requests[1].Auth.Token != "{{preview}}" {
		t.Fatal("the preview changed a saved request")
	}
	if err := a.applyResponseRefs(p); err != nil {
		t.Fatal(err)
	}
	if got := a.requests[1].Auth.Token; got != p.Expression {
		t.Fatalf("saved consumer token = %q; want %q", got, p.Expression)
	}
	list, _, _, err := loadLibrary(a.library.root)
	if err != nil || len(list) != 2 || list[1].Auth.Token != p.Expression {
		t.Fatalf("persisted = %+v, %v", list, err)
	}
	for _, r := range list {
		if strings.Contains(string(mustJSON(r)), "preview-secret-token-123") {
			t.Fatal("a captured secret was written to the collection")
		}
	}
}

func TestResponseRefsNeedExplicitReviewInTheSuggestionUI(t *testing.T) {
	a := responseRefPlanApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Scroll(500, 350, 0, 10000)
	if err := tt.Click("Preview response reference"); err != nil {
		t.Fatal(err)
	}
	if a.tokenLinks.refPlan == nil {
		t.Fatal("preview did not prepare a proposal")
	}
	if before := a.requests[1].Auth.Token; before != "{{preview}}" {
		t.Fatalf("preview changed the request to %q", before)
	}
	tt.Scroll(500, 350, 0, 10000)
	if err := tt.Click("Apply response references"); err != nil {
		t.Fatal(err)
	}
	if !a.responseRefsRefusalHeld(t) {
		t.Fatal("the proposal was applied without an explicit review")
	}
	tt.Scroll(500, 350, 0, 10000)
	if err := tt.Click("I reviewed the response reference changes"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Apply response references"); err != nil {
		t.Fatal(err)
	}
	if got := a.requests[1].Auth.Token; got == "{{preview}}" || !a.tokenLinks.refApplied {
		t.Fatalf("reviewed application did not save the reference: %q", got)
	}
}

func (a *app) responseRefsRefusalHeld(t *testing.T) bool {
	t.Helper()
	return a.requests[1].Auth.Token == "{{preview}}" && !a.tokenLinks.refApplied
}

func TestResponseRefsSurviveRenameAndExpandFromSessionResponse(t *testing.T) {
	a := responseRefPlanApp(t)
	p, err := a.planResponseRefs(a.tokenLinks, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.applyResponseRefs(p); err != nil {
		t.Fatal(err)
	}
	renamed := slices.Clone(a.requests)
	renamed[0].Name = "Sign in"
	if err := a.persistRequests(renamed); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(a.requests[0].ReferenceNames, "Login") {
		t.Fatalf("former name was not kept: %+v", a.requests[0])
	}
	loaded, _, _, err := loadLibrary(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	all := make([]request.Request, len(loaded))
	for i, r := range loaded {
		all[i] = r.Request
	}
	callee, err := library.Resolver(all, nil)("Login")
	if err != nil || callee.Request.ID != "login" {
		t.Fatalf("old name resolved to %+v, %v", callee, err)
	}

	var mu sync.Mutex
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	a.responseRefs.Add("login", &capture.Flow{Status: 200,
		ResponseBody: `{"data":{"access_token":"preview-secret-token-123"}}`})
	consumer := a.requests[1]
	consumer.URL = upstream.URL + "/profile"
	out := consumer.Execute(t.Context(), requestProxy(t), a.sendContext(consumer.Request), a.activeVars(), nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "Bearer preview-secret-token-123" {
		t.Fatalf("sends = %q; want the renamed source's cached token", seen)
	}
}
