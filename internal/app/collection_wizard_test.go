package app

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/vault"
	"github.com/egoist/mygo/ui"
)

func collectionWizardFlows() []flow {
	var out []flow
	for i, entry := range []struct {
		method, host, path string
		status             int
	}{
		{"GET", "api.example.com", "/v1/users/42", 200},
		{"GET", "api.example.com", "/v1/users/7", 200},
		{"GET", "api.example.com", "/v1/users/1093", 404},
		{"PATCH", "api.example.com", "/v1/users/42", 200},
		{"GET", "api.example.com", "/v1/users", 200},
		{"POST", "api.example.com", "/v1/orders", 201},
		{"GET", "api.example.com", "/v1/orders/18", 200},
		{"DELETE", "api.example.com", "/v1/orders/18", 204},
		{"POST", "auth.example.com", "/auth/token", 200},
		{"GET", "api.example.com", "/assets/app.js", 200},
	} {
		out = append(out, flow{id: i + 1, method: entry.method, scheme: "https", host: entry.host, path: entry.path, url: "https://" + entry.host + entry.path,
			status: entry.status, at: snapshotTime.Add(time.Duration(i) * time.Second), millis: 23 + i*7,
			reqHeader:  http.Header{"Authorization": {"Bearer wizard-fixture-secret"}, "Cookie": {"session=wizard-cookie"}, "Accept": {"application/json"}},
			respHeader: http.Header{"Content-Type": {"application/json"}}, resBody: `{"ok":true}`, process: "Browser"})
	}
	return out
}

func TestCollectionWizardCreatesReusableRequestsWithoutLeakingCredentials(t *testing.T) {
	a := libraryApp(t)
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	flows := collectionWizardFlows()
	a.openCollectionWizard(flows)
	w := &a.collectionUI
	w.name, w.environmentName = "Example API", "Captured"
	w.grouping = 1
	w.organize()
	w.generateAPI = true
	tt := ui.NewTester(a.view, 1280, 800)
	for _, step := range collectionSteps[1:] {
		if err := tt.Click("Next: " + step); err != nil {
			t.Fatal(err)
		}
		if len(a.requests) != 0 || len(a.envs) != 0 || len(libraryTree(t, a.library.root)) != 0 {
			t.Fatal("navigation wrote assets before Create")
		}
	}
	if err := tt.Click("Create"); err != nil {
		t.Fatal(err)
	}
	if w.open || len(a.requests) != 7 {
		t.Fatalf("created %d requests, open=%v problem=%s", len(a.requests), w.open, w.problem)
	}
	list, folders, _, err := loadLibrary(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	var get savedRequest
	for _, r := range list {
		if r.Method == "GET" && strings.HasSuffix(r.URL, "/users/{{id}}") {
			get = r
		}
	}
	if get.Collection != "Example API/users" || get.Name != "Get user" || get.Auth.Type != request.AuthInherit {
		t.Fatalf("saved request %+v", get)
	}
	if len(get.Examples) != 2 || get.Examples[0].Status != 200 || get.Examples[1].Status != 404 {
		t.Fatalf("examples %+v", get.Examples)
	}
	vars := a.activeVars()
	ctx := request.Context{Folders: request.Chain(folders, get.Collection)}
	r := get.Request
	r.Auth, _ = ctx.InheritAuth(r.Auth)
	prepared, err := r.Prepare(vars)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.URL != "https://api.example.com/v1/users/1093" || prepared.Header.Get("Authorization") != "Bearer wizard-fixture-secret" || prepared.Header.Get("Cookie") != "session=wizard-cookie" {
		t.Fatalf("prepared request %+v", prepared)
	}
	for _, file := range libraryTree(t, a.library.root) {
		data, err := os.ReadFile(filepath.Join(a.library.root, filepath.FromSlash(file)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "wizard-fixture-secret") || strings.Contains(string(data), "wizard-cookie") {
			t.Fatalf("credential leaked into %s", file)
		}
	}
	env, err := loadEnvs(&store.File{Path: a.envsPath})
	if err != nil {
		t.Fatal(err)
	}
	if env.Envs[0].Current["authorization"] != "Bearer wizard-fixture-secret" || env.Envs[0].Vars["authorization"] != "" {
		t.Fatalf("credential not local-only: %+v", env.Envs)
	}
	a.openVars()
	a.exportPostmanVariables()
	if strings.Contains(string(a.exportUI.data), "wizard-fixture-secret") || strings.Contains(string(a.exportUI.data), "wizard-cookie") {
		t.Fatal("current values leaked into environment export")
	}
	if len(a.apis) != 1 || a.apis[0].parsed == nil || a.apis[0].parsed.Paths.Value("/v1/users/{id}") == nil {
		t.Fatal("OpenAPI draft not generated from nested collection")
	}
}

func TestCollectionWizardGroupingBoundariesAndManualCorrections(t *testing.T) {
	a := libraryApp(t)
	flows := collectionWizardFlows()[:3]
	// Keep capture order distinct from time, and preserve HTTP errors.
	flows[0].at = flows[1].at.Add(time.Minute)
	for _, raw := range []string{"https://other.example.com/v1/users/42", "https://api.example.com/v1/users/me", "https://api.example.com/v1/users/v2"} {
		u, _ := url.Parse(raw)
		flows = append(flows, flow{method: "GET", host: u.Host, path: u.Path, url: raw, status: 200})
	}
	a.openCollectionWizard(flows)
	w := &a.collectionUI
	if len(w.groups) != 4 || !w.examples[0] || w.examples[1] || !w.examples[2] {
		t.Fatalf("grouping or latest-success defaults incorrect: groups=%d examples=%v", len(w.groups), w.examples)
	}
	w.step = 1
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("Split"); err != nil {
		t.Fatal(err)
	}
	if len(w.groups) != 6 {
		t.Fatalf("split produced %d groups", len(w.groups))
	}
	if err := tt.Click("Merge with…"); err != nil {
		t.Fatal(err)
	}
	// The same path also appears behind the menu. Select its first item
	// through the focused menu rather than clicking the underlying label.
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if len(w.groups) != 5 {
		t.Fatalf("manual merge produced %d groups: %s", len(w.groups), w.problem)
	}
	if err := tt.Click("Next: Variables"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if len(w.groups) != 5 {
		t.Fatal("Back lost manual edits")
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if w.open || len(a.requests) != 0 || len(a.envs) != 0 {
		t.Fatal("Cancel did not discard draft")
	}
}

func TestCollectionWizardExamplesKeepTheirProducingRequests(t *testing.T) {
	for _, storage := range []string{credentialCurrent, credentialVault, credentialKeep} {
		t.Run(storage, func(t *testing.T) {
			a := libraryApp(t)
			a.envsPath = filepath.Join(t.TempDir(), "env.json")
			a.vault = vaultApp(t, &fakeCredentials{keys: map[string][]byte{}}).vault
			flows := collectionWizardFlows()[:2]
			for i := range flows {
				f := &flows[i]
				f.method = "POST"
				f.url += []string{"?attempt=success&tag=a%2Fb&tag=c", "?attempt=failure"}[i]
				f.reqBody = []string{`{"user":"alice"}`, `{"user":"bob"}`}[i]
				f.reqHeader = http.Header{
					"Authorization": {"Basic dXNlcjpwYXNz"},
					"Cookie":        {[]string{"session=old-private", "session=new-private"}[i]},
					"X-Api-Key":     {"shared-private-api-key"},
					"Api-Key":       {[]string{"old-private-api-key", "new-private-api-key"}[i]},
					"X-Attempt":     {[]string{"success", "failure"}[i]},
					"X-Tag":         {"one", "two"},
					"Content-Type":  {"application/json"},
				}
				f.status = []int{200, 401}[i]
				f.resBody = []string{`{"ok":true}`, `{"ok":false}`}[i]
				f.respHeader.Set("Set-Cookie", "response-private-cookie")
			}
			// The older request must read its own retained body, not its preview
			// or the newer request's body.
			bodyFile := filepath.Join(t.TempDir(), "request-body")
			if err := os.WriteFile(bodyFile, []byte(flows[0].reqBody), 0600); err != nil {
				t.Fatal(err)
			}
			flows[0].reqFile, flows[0].reqSize, flows[0].reqBody = bodyFile, len(flows[0].reqBody), "preview"
			a.openCollectionWizard(flows)
			w := &a.collectionUI
			for i := range w.credentials {
				w.credentials[i].storage = storage
			}
			a.createCollection()
			if w.open {
				t.Fatalf("create failed: %s", w.problem)
			}
			list, folders, _, err := loadLibrary(a.library.root)
			if err != nil || len(list) != 1 {
				t.Fatalf("load collection: %d requests, %v", len(list), err)
			}
			r := list[0]
			if request.ExpandVars(r.URL, a.activeVars()) != flows[1].url || r.Body != flows[1].reqBody || request.ParseHeaderBlock(r.Headers).Get("X-Attempt") != "failure" {
				t.Fatalf("representative request is not the newest: %+v", r)
			}
			if storage != credentialKeep && (r.Auth.Type != request.AuthInherit || request.Chain(folders, r.Collection)[0].Auth.Value == "") {
				t.Fatal("shared authorization was not promoted")
			}
			if len(r.Examples) != 2 {
				t.Fatalf("got %d examples", len(r.Examples))
			}
			for i, ex := range r.Examples {
				f := flows[i]
				if ex.Status != f.status || ex.Body != f.resBody || ex.Request.Method != f.method || ex.Request.URL != f.url || ex.Request.Body != []string{`{"user":"alice"}`, `{"user":"bob"}`}[i] {
					t.Errorf("example %d lost its producing request/response: %+v", i, ex)
				}
				h := request.ParseHeaderBlock(ex.Request.Headers)
				for _, name := range []string{"X-Attempt", "X-Tag", "Content-Type"} {
					if !slices.Equal(h.Values(name), f.reqHeader.Values(name)) {
						t.Errorf("example %d changed %s: %v", i, name, h.Values(name))
					}
				}
				for _, c := range w.credentials {
					want := c.value
					switch storage {
					case credentialCurrent:
						want = "{{" + c.variable + "}}"
					case credentialVault:
						want = "{{vault:" + c.variable + "}}"
					}
					if slices.Contains(f.reqHeader.Values(c.header), c.value) && h.Get(c.header) != want {
						t.Errorf("example %d changed credential reference %s: %q", i, c.header, h.Get(c.header))
					}
				}
				if request.ParseHeaderBlock(ex.Headers).Get("Set-Cookie") != "" {
					t.Error("example kept a session response cookie")
				}
			}
			if storage == credentialKeep {
				return // Explicit opt-in keeps literal credentials in snapshots too.
			}
			for _, file := range libraryTree(t, a.library.root) {
				data, err := os.ReadFile(filepath.Join(a.library.root, filepath.FromSlash(file)))
				if err != nil {
					t.Fatal(err)
				}
				for _, secret := range []string{"dXNlcjpwYXNz", "private", "response-private-cookie"} {
					if strings.Contains(string(data), secret) {
						t.Errorf("credential leaked into %s", file)
					}
				}
			}
			for _, c := range w.credentials {
				if !strings.Contains(c.value, "old-private") {
					continue
				}
				if _, exists := a.activeVars()[c.variable]; exists || slices.ContainsFunc(a.vault.secrets, func(s vault.Secret) bool { return s.Name == c.variable }) {
					t.Error("example-only credential was newly persisted")
				}
			}
		})
	}
}

func TestCollectionWizardRepeatedMergesKeepDistinctPathParameters(t *testing.T) {
	for _, tc := range []struct{ reverse, rename bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		t.Run(fmt.Sprintf("reverse=%v/rename=%v", tc.reverse, tc.rename), func(t *testing.T) {
			a := libraryApp(t)
			a.envsPath = filepath.Join(t.TempDir(), "env.json")
			var flows []flow
			for i, path := range []string{"/v1/users/42", "/v2/users/42", "/v1/orders/42", "/v2/orders/42"} {
				flows = append(flows, flow{method: "GET", url: "https://api.example.com" + path, status: 200, at: snapshotTime.Add(time.Duration(i) * time.Second)})
			}
			a.openCollectionWizard(flows)
			w := &a.collectionUI
			if err := w.merge(0, 1); err != nil {
				t.Fatal(err)
			}
			if err := w.merge(1, 2); err != nil {
				t.Fatal(err)
			}
			if tc.rename {
				// Both templates' names must be reserved, even at positions
				// that the next merge will replace.
				w.groups[1].template = "/:param2/orders/:id"
			}
			from, to := 0, 1
			if tc.reverse {
				from, to = to, from
			}
			if err := w.merge(from, to); err != nil {
				t.Fatal(err)
			}
			a.createCollection()
			if w.open {
				t.Fatalf("create failed: %s", w.problem)
			}
			list, _, _, err := loadLibrary(a.library.root)
			if err != nil || len(list) != 1 {
				t.Fatalf("load collection: %d requests, %v", len(list), err)
			}
			if got := request.ExpandVars(list[0].URL, a.activeVars()); got != flows[3].url {
				t.Fatalf("merged request expanded to %s instead of %s (template %s)", got, flows[3].url, list[0].URL)
			}
			if tc.rename && (strings.Contains(list[0].URL, "{{param1}}") || strings.Contains(list[0].URL, "{{param2}}")) {
				t.Fatalf("merge reused a parameter name from an input template: %s", list[0].URL)
			}
		})
	}
}

func TestCollectionWizardRejectsIncompleteExamplesAndEnvironmentConflicts(t *testing.T) {
	a := libraryApp(t)
	f := collectionWizardFlows()[0]
	f.resSize = len(f.resBody) + 1
	a.openCollectionWizard([]flow{f})
	a.createCollection()
	if !a.collectionUI.open || !strings.Contains(a.collectionUI.problem, "example") || len(a.requests) != 0 || len(a.envs) != 0 {
		t.Fatalf("incomplete example was saved: %s", a.collectionUI.problem)
	}
	a.collectionUI.examples[0] = false
	a.envs = []environment{{Name: "Existing", Vars: map[string]string{"base_url": "https://different.example.com"}}}
	a.collectionUI.environment = "Existing"
	before, _ := json.Marshal(a.envs)
	a.createCollection()
	after, _ := json.Marshal(a.envs)
	if !reflect.DeepEqual(before, after) || len(a.requests) != 0 || !strings.Contains(a.collectionUI.problem, "different value") {
		t.Fatalf("existing environment overwritten: %s", a.collectionUI.problem)
	}
	for _, kind := range []string{"incomplete", "missing spill", "binary", "trailers"} {
		t.Run("older example request/"+kind, func(t *testing.T) {
			a := libraryApp(t)
			a.envsPath = filepath.Join(t.TempDir(), "env.json")
			flows := collectionWizardFlows()[:2]
			flows[1].status = 401
			switch kind {
			case "incomplete":
				flows[0].reqSize = 1
			case "missing spill":
				flows[0].reqFile = filepath.Join(t.TempDir(), "missing")
			case "binary":
				flows[0].reqBody = "\xff"
			case "trailers":
				flows[0].reqTrailer = http.Header{"X-Trailer": {"value"}}
			}
			a.openCollectionWizard(flows)
			a.createCollection()
			if !a.collectionUI.open || !strings.Contains(a.collectionUI.problem, "example") || len(a.envs) != 0 || len(libraryTree(t, a.library.root)) != 0 {
				t.Fatalf("unrepresentable example request changed stored assets: %s", a.collectionUI.problem)
			}
			a.collectionUI.examples[0] = false
			a.createCollection()
			if a.collectionUI.open || len(a.requests) != 1 || len(a.requests[0].Examples) != 1 || a.requests[0].Examples[0].Status != 401 {
				t.Fatalf("deselecting the incomplete example did not unblock creation: %s", a.collectionUI.problem)
			}
		})
	}
}

func TestCollectionWizardStructureDragAndKeyboard(t *testing.T) {
	a := libraryApp(t)
	a.openCollectionWizard(collectionWizardFlows())
	w := &a.collectionUI
	w.step, w.grouping = 3, 1
	w.organize()
	tt := ui.NewTester(a.view, 1280, 800)
	from, ok := tt.Find("Organize Get user")
	if !ok {
		t.Fatal("missing request")
	}
	to, ok := tt.Find("Collection root")
	if !ok {
		t.Fatal("missing root")
	}
	tt.Press(from.X+12, from.Y+from.H/2)
	tt.Move(to.X+12, to.Y+to.H/2)
	tt.Release(to.X+12, to.Y+to.H/2)
	if w.groups[0].folder != "" {
		t.Fatal("drag did not move request to collection root")
	}
	before := slices.Clone(w.groups)
	if err := tt.Click("Move down"); err != nil {
		t.Fatal(err)
	}
	if w.groups[1] != before[0] || w.groups[0] != before[1] {
		t.Fatal("keyboard-accessible move did not reorder draft")
	}
	tt.Key(0, ui.KeyEscape)
	if w.open {
		t.Fatal("Escape did not cancel wizard")
	}
}

func TestCollectionWizardOnlyStoresIncludedCredentialsAndPreservesPathSegments(t *testing.T) {
	a := libraryApp(t)
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	flows := collectionWizardFlows()[:4]
	flows[0].reqHeader = http.Header{"Authorization": {"Bearer old-not-used"}}
	flows[3].reqHeader = http.Header{"Authorization": {"Bearer excluded-not-used"}}
	flows[2].url, flows[2].path = "https://api.example.com/v1/users/a%2Fb", "/v1/users/a%2Fb"
	a.openCollectionWizard(flows)
	w := &a.collectionUI
	// Merge the encoded segment explicitly; automatic inference leaves it literal.
	w.groups[0].flows = []int{0, 1, 2}
	w.groups = w.groups[:1]
	w.examples[0] = true
	a.createCollection()
	if w.open || len(a.requests) != 1 {
		t.Fatalf("create failed: %s", w.problem)
	}
	vars := a.activeVars()
	if len(vars) != 4 || vars["id"] != "a%2Fb" || vars["authorization_2"] != "Bearer wizard-fixture-secret" {
		t.Fatalf("wrong included values: %v", vars)
	}
	for _, value := range vars {
		if strings.Contains(value, "not-used") {
			t.Fatal("an older or excluded capture persisted its credential")
		}
	}
	if got := request.ExpandVars(a.requests[0].URL, vars); got != flows[2].url {
		t.Fatalf("path segment changed: %s", got)
	}
	list, _, _, err := loadLibrary(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	if got := request.ParseHeaderBlock(list[0].Examples[0].Request.Headers).Get("Authorization"); got != "{{authorization}}" {
		t.Fatalf("older example lost its private unresolved credential reference: %q", got)
	}
}

func TestCollectionWizardRejectsVariableShadowing(t *testing.T) {
	for _, kind := range []string{"current shadows host", "shared credential exists", "parameter reuses host", "parameter reuses credential", "example credential reuses host", "example credential reuses path", "example credential reuses credential", "example credential invalid"} {
		t.Run(kind, func(t *testing.T) {
			a := libraryApp(t)
			a.envsPath = filepath.Join(t.TempDir(), "env.json")
			a.openCollectionWizard(collectionWizardFlows()[:1])
			w := &a.collectionUI
			a.envs = []environment{{Name: "Existing", Vars: map[string]string{}, Current: map[string]string{}}}
			w.environment = "Existing"
			switch kind {
			case "current shadows host":
				a.envs[0].Current["base_url"] = "https://wrong.example.com"
			case "shared credential exists":
				a.envs[0].Vars["authorization"] = "Bearer another-secret"
			case "parameter reuses host":
				w.groups[0].template = "/v1/users/:base_url"
			case "parameter reuses credential":
				w.groups[0].template = "/v1/users/:authorization"
			case "example credential reuses host", "example credential reuses path", "example credential reuses credential", "example credential invalid":
				flows := collectionWizardFlows()[:2]
				flows[0].reqHeader.Set("Authorization", "Bearer older-example-only")
				a.openCollectionWizard(flows)
				w.environment = "Existing"
				w.examples[0] = true
				name := map[string]string{
					"example credential reuses host":       "base_url",
					"example credential reuses path":       "id",
					"example credential reuses credential": "cookie",
					"example credential invalid":           "not an identifier",
				}[kind]
				for i := range w.credentials {
					if w.credentials[i].value == "Bearer older-example-only" {
						w.credentials[i].variable = name
					}
				}
			}
			before, _ := json.Marshal(a.envs)
			a.createCollection()
			after, _ := json.Marshal(a.envs)
			if !w.open || w.problem == "" || string(before) != string(after) || len(libraryTree(t, a.library.root)) != 0 {
				t.Fatalf("conflict changed stored assets: %s", w.problem)
			}
		})
	}
}

func TestCurrentValuesEditRoundTripAndScriptWritesStayLocal(t *testing.T) {
	a := libraryApp(t)
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	a.envs = []environment{{Name: "Local", Vars: map[string]string{"token": "shared"}, Current: map[string]string{"token": "private"}}}
	a.activeEnv = "Local"
	a.openVars()
	tt := ui.NewTester(a.view, 960, 600)
	current, currentOK := tt.Find("Variable current value 1")
	save, saveOK := tt.Find("Save")
	if !currentOK || !saveOK || current.Y+current.H > save.Y {
		t.Fatalf("current value is hidden behind the footer: field %+v, Save %+v", current, save)
	}
	if err := tt.Click("Variable current value 1"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("edited-private")
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.varsOpen || a.activeVars()["token"] != "edited-private" {
		t.Fatalf("current value edit not saved: %s", a.varsProblem)
	}
	a.applyVars("Local", a.activeVars(), map[string]string{"token": "script-private"})
	f, err := loadEnvs(&store.File{Path: a.envsPath})
	if err != nil || f.Envs[0].Vars["token"] != "shared" || f.Envs[0].Current["token"] != "script-private" {
		t.Fatalf("script promoted a private value to shared: %+v %v", f, err)
	}
}

func TestCollectionWizardCredentialStorageChoices(t *testing.T) {
	for _, storage := range []string{credentialVault, credentialKeep} {
		t.Run(storage, func(t *testing.T) {
			a := libraryApp(t)
			a.vault = vaultApp(t, &fakeCredentials{keys: map[string][]byte{}}).vault
			a.envsPath = filepath.Join(t.TempDir(), "env.json")
			a.openCollectionWizard(collectionWizardFlows())
			for _, g := range a.collectionUI.groups {
				g.include = g.origin == "https://api.example.com"
			}
			for i := range a.collectionUI.credentials {
				a.collectionUI.credentials[i].storage = storage
			}
			a.createCollection()
			if a.collectionUI.open || len(a.requests) != 6 {
				t.Fatalf("create failed: %s", a.collectionUI.problem)
			}
			vars := a.activeVars()
			if _, exists := vars["authorization"]; exists {
				t.Fatal("non-environment credential written to environment")
			}
			if storage == credentialKeep {
				if request.ParseHeaderBlock(a.requests[0].Headers).Get("Authorization") != "Bearer wizard-fixture-secret" || a.vault.loaded {
					t.Fatal("explicit Leave in requests was not respected")
				}
				return
			}
			in := a.sendContext(a.requests[0].Request)
			if in.Vault == nil {
				t.Fatal("vault reference not available to send")
			}
			if value, err := in.Vault("authorization", "api.example.com"); err != nil || value != "Bearer wizard-fixture-secret" {
				t.Fatalf("vault reference failed: %v", err)
			}
			if _, err := in.Vault("authorization", "auth.example.com"); err == nil {
				t.Fatal("excluded host was allowed to use the credential")
			}
			data, err := os.ReadFile(a.vault.path)
			if err != nil || strings.Contains(string(data), "wizard-fixture-secret") {
				t.Fatal("vault did not encrypt the credential")
			}
		})
	}
}

func TestCollectionWizardSidebarScopeIgnoresTableFilters(t *testing.T) {
	for _, entry := range []struct {
		label string
		count int
	}{{"api.example.com", 9}, {"Browser", 10}} {
		t.Run(entry.label, func(t *testing.T) {
			a := libraryApp(t)
			a.flows = collectionWizardFlows()
			a.recount()
			a.query = "/not-in-capture"
			a.applyFilter()
			tt := ui.NewTester(a.view, 1280, 800)
			if err := tt.RightClick(entry.label); err != nil {
				t.Fatal(err)
			}
			if err := chooseMenu(tt, "Create collection…"); err != nil {
				t.Fatal(err)
			}
			if !a.collectionUI.open || len(a.collectionUI.flows) != entry.count {
				t.Fatalf("sidebar scope used table filters: %d flows", len(a.collectionUI.flows))
			}
		})
	}
}
