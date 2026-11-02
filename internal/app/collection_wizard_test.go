package app

import (
	"encoding/json/v2"
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
}

func TestCollectionWizardRejectsVariableShadowing(t *testing.T) {
	for _, kind := range []string{"current shadows host", "shared credential exists", "parameter reuses host", "parameter reuses credential"} {
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
	current, currentOK := tt.Find("Current variable value 1")
	save, saveOK := tt.Find("Save")
	if !currentOK || !saveOK || current.Y+current.H > save.Y {
		t.Fatalf("current value is hidden behind the footer: field %+v, Save %+v", current, save)
	}
	if err := tt.Click("Current variable value 1"); err != nil {
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
