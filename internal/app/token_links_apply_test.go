package app

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/egoist/mygo/ui"
)

func tokenPlanApp(t *testing.T) *app {
	t.Helper()
	a := libraryApp(t)
	a.envsPath = filepath.Join(t.TempDir(), "environments.json")
	a.activeEnv = "Local"
	a.envs = []environment{{Name: "Local", Current: map[string]string{"recorded_auth": "Bearer preview-secret-token-123"}}}
	if err := saveEnvs(a.file(a.envsPath), envFile{Active: a.activeEnv, Envs: a.envs}); err != nil {
		t.Fatal(err)
	}
	requests := []savedRequest{
		{ID: "login", Name: "Login", Collection: "Shop", Method: "POST", URL: "https://api.example.com/login", Pre: "// existing login pre", Post: `pm.test("authored", () => pm.expect(true).to.equal(true));`},
		{ID: "profile", Name: "Profile", Collection: "Shop", Method: "GET", URL: "https://api.example.com/profile", Headers: "# keep comment\nX-Keep: untouched\nAuthorization: {{recorded_auth}}", Pre: "// existing consumer pre", HeaderList: []request.Field{{Name: "X-Keep", Value: "untouched"}, {Name: "Authorization ", Value: "{{recorded_auth}}"}, {Name: "Authorization", Value: "disabled", Disabled: true}}},
	}
	if err := a.persistRequests(requests); err != nil {
		t.Fatal(err)
	}
	a.flows = tokenPreviewFlows()
	a.openTokenLinks(a.flows)
	a.tokenLinks.config = tokenLinkConfig{Collection: "Shop", Environment: "Local", Variable: "linked_token"}
	return a
}

func tokenPlan(t *testing.T, a *app) *tokenLinkPlan {
	t.Helper()
	p, err := a.planTokenLink(a.tokenLinks, 0, a.tokenLinks.config)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTokenLinksConfirmedApplication(t *testing.T) {
	a := tokenPlanApp(t)
	a.addDocument(a.requests[0], a.requests[0].key())
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Review token link changes"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Review generated changes") || tt.HasText("preview-secret-token-123") {
		t.Fatalf("missing or unmasked proposal: %v", tt.Texts())
	}
	before := a.requests[0].Post
	if err := tt.Click("Apply token link"); err != nil {
		t.Fatal(err)
	}
	if a.tokenLinks.applied || a.requests[0].Post != before {
		t.Fatal("applied without acknowledgment")
	}
	tt.Scroll(500, 350, 0, 10000)
	if err := tt.Click("I reviewed these script and credential changes"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Apply token link"); err != nil {
		t.Fatal(err)
	}
	if !a.tokenLinks.applied || !tt.HasText("Token link applied") {
		t.Fatalf("application failed: %s", a.tokenLinks.applyError)
	}
	list, _, _, err := loadLibrary(a.library.root)
	if err != nil || len(list) != 2 {
		t.Fatalf("load saved changes: %v", err)
	}
	if !strings.Contains(a.requests[0].Pre, "// existing login pre") || !strings.HasPrefix(a.requests[0].Post, before) || !strings.HasPrefix(a.requests[1].Pre, "// existing consumer pre") || a.requestDocument.dirty() || a.requestDocument.composePost != a.requests[0].Post {
		t.Fatal("authored scripts or clean open tab not preserved")
	}
	if !slices.Equal(a.requestDocument.draft().TokenSources, []request.TokenSource{{Environment: "Local", Variable: "linked_token"}}) || !slices.Equal(list[0].TokenSources, a.requests[0].TokenSources) {
		t.Fatal("source lifecycle binding was not persisted or retained by the native editor")
	}
	consumer := a.requests[1]
	if consumer.Headers != "# keep comment\nX-Keep: untouched\nAuthorization: Bearer {{linked_token}}" || consumer.HeaderList[2].Value != "disabled" || consumer.HeaderList[1].Value != "Bearer {{linked_token}}" {
		t.Fatal("unrelated/disabled headers changed or active header row stale")
	}
	d := a.newDocument(consumer, consumer.key())
	prepared, err := d.draft().Request.Prepare(map[string]string{"linked_token": "fresh-token-from-source"})
	if err != nil || prepared.Header.Get("Authorization") != "Bearer fresh-token-from-source" {
		t.Fatal("opening the native editor restored an old Authorization table value")
	}
	f, err := loadEnvs(&store.File{Path: a.envsPath})
	if err != nil || f.Envs[0].Current["linked_token"] != "" || f.Envs[0].Vars["linked_token"] != "" || f.Active != "Local" {
		t.Fatalf("private empty dependency: %v", err)
	}
	if _, ok := f.Envs[0].Current["linked_token"]; !ok {
		t.Fatal("private slot not persisted")
	}
	if strings.Contains(a.requests[0].Post, "preview-secret-token-123") {
		t.Fatal("captured token embedded in extraction script")
	}
}

func TestTokenLinksRejectConflictingChanges(t *testing.T) {
	for name, change := range map[string]func(*app){
		"dirty draft": func(a *app) {
			a.addDocument(a.requests[1], a.requests[1].key())
			a.requestDocument.composeBody = "unsaved"
		},
		"pending send": func(a *app) {
			a.addDocument(a.requests[0], a.requests[0].key())
			a.requestDocument.composeWaiting = true
		},
		"edited request":     func(a *app) { a.requests[1].Auth = request.Auth{Type: "Basic", Username: "new"} },
		"deleted request":    func(a *app) { a.requests = a.requests[:1] },
		"environment edit":   func(a *app) { a.envs[0].Current["recorded_auth"] = "changed" },
		"environment editor": func(a *app) { a.varsOpen, a.varsName = true, "Local" },
		"folder edit":        func(a *app) { a.folders = []request.Folder{{Path: "Shop", Pre: "new"}} },
		"global edit":        func(a *app) { a.globals = map[string]string{"new": "value"} },
		"recording":          func(a *app) { a.collectionRecording = &collectionRecording{Active: true} },
	} {
		t.Run(name, func(t *testing.T) {
			a := tokenPlanApp(t)
			p := tokenPlan(t, a)
			change(a)
			before, _ := os.ReadFile(a.envsPath)
			if err := a.applyTokenLink(p); err == nil {
				t.Fatal("conflicting application accepted")
			}
			after, _ := os.ReadFile(a.envsPath)
			if string(before) != string(after) {
				t.Fatal("rejected proposal wrote environment")
			}
			list, _, _, _ := loadLibrary(a.library.root)
			if strings.Contains(list[0].Pre, "vscroxy token link") {
				t.Fatal("rejected proposal changed disk requests")
			}
		})
	}
	for _, kind := range []string{"Basic", request.AuthInherit, "Bearer mismatch", "duplicate match", "existing variable", "missing header"} {
		t.Run(kind, func(t *testing.T) {
			a := tokenPlanApp(t)
			switch kind {
			case "Basic", request.AuthInherit:
				a.requests[1].Auth.Type = kind
			case "Bearer mismatch":
				a.requests[1].Auth = request.Auth{Type: "Bearer", Token: "another-token-value"}
			case "duplicate match":
				r := a.requests[0]
				r.ID = "other"
				a.requests = append(a.requests, r)
			case "existing variable":
				a.envs[0].Current["linked_token"] = "private"
			case "missing header":
				a.requests[1].Headers = "Authorization:"
			}
			if _, err := a.planTokenLink(a.tokenLinks, 0, a.tokenLinks.config); err == nil {
				t.Fatal("unsafe proposal accepted")
			}
		})
	}
}

func TestTokenLinksGuardedPersistenceAndPlanSecrecy(t *testing.T) {
	a := tokenPlanApp(t)
	p := tokenPlan(t, a)
	data, _ := json.Marshal(p)
	if strings.Contains(string(data), "preview-secret-token-123") || strings.Contains(string(data), "recorded_auth") || strings.Contains(string(data), "existing login pre") {
		t.Fatal("proposal exposed captured/authored contents")
	}
	path := filepath.Join(a.library.root, "Shop", "Profile.request.json")
	if err := os.WriteFile(path, []byte("external edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.applyTokenLink(p); err == nil {
		t.Fatal("external change overwritten")
	}
	kept, _ := os.ReadFile(path)
	if string(kept) != "external edit" || !p.reserved || a.envs[0].Current["linked_token"] != "" || strings.Contains(a.requests[0].Pre, "vscroxy token link") {
		t.Fatal("failed guarded save did not preserve external edit/private dependency")
	}
}

func TestTokenLinksRunFreshTokensAndFailClosed(t *testing.T) {
	a, _ := recordingApp(t)
	var mu sync.Mutex
	var sent []string
	serial := 0
	broken := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/login" {
			serial++
			if broken != "" {
				fmt.Fprint(w, broken)
				return
			}
			fmt.Fprintf(w, `{"data":{"access_token":"fresh-runtime-token-%03d"}}`, serial)
			return
		}
		sent = append(sent, r.Header.Get("Authorization"))
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()
	b := tokenPlanApp(t)
	for i := range b.requests {
		b.requests[i].URL = upstream.URL + strings.TrimPrefix(b.requests[i].URL, "https://api.example.com")
	}
	b.requests[1].Auth = request.Auth{Type: "Bearer", Token: "preview-secret-token-123"}
	for i := range b.flows {
		b.flows[i].url = upstream.URL + strings.TrimPrefix(b.flows[i].url, "https://api.example.com")
	}
	b.openTokenLinks(b.flows)
	b.tokenLinks.config = tokenLinkConfig{Collection: "Shop", Environment: "Local", Variable: "linked_token"}
	p := tokenPlan(t, b)
	if err := b.applyTokenLink(p); err != nil {
		t.Fatal(err)
	}
	steps := []runner.Step{{Request: b.requests[0].Request, Context: request.Context{Environment: "Local"}}, {Request: b.requests[1].Request, Context: request.Context{Environment: "Local"}}}
	var last runner.Result
	summary, err := runner.Run(t.Context(), a.srv, steps, runner.Options{Iterations: 3, Vars: b.envs[0].values()}, func(r runner.Result) error { last = r; return nil })
	if err != nil || summary.Completed != 6 || summary.Failed != 0 {
		t.Fatalf("fresh tokens run: %+v %v (%v)", summary, err, last.Execution.Err)
	}
	mu.Lock()
	if !reflect.DeepEqual(sent, []string{"Bearer fresh-runtime-token-001", "Bearer fresh-runtime-token-002", "Bearer fresh-runtime-token-003"}) {
		t.Errorf("stale/reused tokens: %v", sent)
	}
	sent, broken = nil, `{"data":{"access_token":null}}`
	mu.Unlock()
	summary, err = runner.Run(t.Context(), a.srv, steps, runner.Options{Iterations: 1, Vars: map[string]string{"linked_token": "old-runtime-token-000"}}, func(r runner.Result) error { last = r; return nil })
	mu.Lock()
	received := slices.Clone(sent)
	mu.Unlock()
	if err != nil || summary.Failed != 2 || len(received) != 0 || last.Execution.Vars["linked_token"] != "" {
		t.Fatalf("failed extraction reused stale token: %+v %v %v", summary, err, received)
	}
	// The same authored scripts must not leak values into another environment.
	wrong := steps[0].Request.Execute(t.Context(), a.srv, request.Context{Environment: "Other"}, map[string]string{"linked_token": "unchanged"}, nil)
	if wrong.Err == nil || wrong.Vars["linked_token"] != "unchanged" {
		t.Fatal("environment guard missing")
	}
	if slices.ContainsFunc(wrong.Console, func(c request.ConsoleMessage) bool { return strings.Contains(c.Text, "old-runtime-token") }) {
		t.Fatal("guard logged credentials")
	}
	for _, kind := range []string{"inherited pre", "authored syntax", "invalid checks"} {
		t.Run(kind, func(t *testing.T) {
			source := steps[0].Request
			in := request.Context{Environment: "Local"}
			switch kind {
			case "inherited pre":
				in.Folders = []request.Folder{{Path: "Shop", Pre: `throw new Error("missing login input")`}}
			case "authored syntax":
				source.Pre += "\nconst ="
			case "invalid checks":
				source.Checks.Status = "invalid"
			}
			failed := source.Execute(t.Context(), a.srv, in, map[string]string{"linked_token": "old-runtime-token-000"}, nil)
			if failed.Err == nil || failed.Vars["linked_token"] != "" {
				t.Fatalf("early source failure kept old token: %v", failed.Err)
			}
			consumer := steps[1].Request.Execute(t.Context(), a.srv, request.Context{Environment: "Local"}, failed.Vars, nil)
			if consumer.Err == nil || consumer.Request != nil {
				t.Fatal("consumer was sent after source setup failure")
			}
		})
	}
	mu.Lock()
	broken = `{"data":{"access_token":"response-secret-token-not-logged"}} "response-secret-token-not-logged"`
	mu.Unlock()
	malformed := steps[0].Request.Execute(t.Context(), a.srv, request.Context{Environment: "Local"}, map[string]string{"linked_token": "old-runtime-token-000"}, nil)
	if malformed.Err == nil || strings.Contains(malformed.Err.Error(), "response-secret-token-not-logged") || slices.ContainsFunc(malformed.Console, func(c request.ConsoleMessage) bool {
		return strings.Contains(c.Text, "response-secret-token-not-logged")
	}) {
		t.Fatal("generated JSON parse failure exposed response credentials")
	}
}
