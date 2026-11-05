package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// One send sees each scope's Current value before its Initial value, and an
// empty Current falls back to Initial. Locals and the data row stay above the
// environment, as in Postman.
func TestVariableScopesPreferCurrentOverInitial(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	a.envs = []environment{{Name: "Local",
		Vars: map[string]string{
			"env": "env-initial", "env_fallback": "env-initial-fallback", "env_initial": "env-initial-only",
			"local": "env-local", "data": "env-data",
		},
		Current: map[string]string{"env": "env-current", "env_fallback": "", "env_only": "env-current-only"},
	}}
	a.activeEnv = "Local"
	a.globals = map[string]string{
		"global": "global-initial", "global_fallback": "global-initial-fallback", "global_initial": "global-initial-only",
	}
	a.globalsCurrent = map[string]string{"global": "global-current", "global_fallback": "", "global_only": "global-current-only"}
	a.folders = []request.Folder{
		{Path: "Shop", Variables: []request.Variable{
			{Name: "collection", Initial: "collection-initial"},
			{Name: "collection_fallback", Initial: "collection-initial-fallback"},
			{Name: "collection_initial", Initial: "collection-initial-only"},
			{Name: "disabled", Initial: "disabled-initial", Disabled: true},
		}},
		{Path: "Shop/Orders", Variables: []request.Variable{
			{Name: "folder", Initial: "folder-initial"},
			{Name: "folder_fallback", Initial: "folder-initial-fallback"},
			{Name: "folder_initial", Initial: "folder-initial-only"},
		}},
	}
	a.folderCurrent = map[string]map[string]string{
		"Shop":        {"collection": "collection-current", "collection_fallback": "", "collection_only": "collection-current-only"},
		"Shop/Orders": {"folder": "folder-current", "folder_fallback": ""},
	}
	r := request.Request{Method: "GET", Collection: "Shop/Orders", URL: upstream.URL,
		Headers: strings.Join([]string{
			"X-Env: {{env}}", "X-Env-Fallback: {{env_fallback}}", "X-Env-Initial: {{env_initial}}", "X-Env-Only: {{env_only}}",
			"X-Global: {{global}}", "X-Global-Fallback: {{global_fallback}}", "X-Global-Initial: {{global_initial}}", "X-Global-Only: {{global_only}}",
			"X-Collection: {{collection}}", "X-Collection-Fallback: {{collection_fallback}}", "X-Collection-Initial: {{collection_initial}}", "X-Collection-Only: {{collection_only}}",
			"X-Folder: {{folder}}", "X-Folder-Fallback: {{folder_fallback}}", "X-Folder-Initial: {{folder_initial}}",
			"X-Local: {{local}}", "X-Data: {{data}}", "X-Disabled: {{disabled}}",
		}, "\n"),
		Pre: `pm.variables.set("local", "local-value")`,
	}
	in := a.sendContext(r)
	in.Data = map[string]string{"data": "data-value"}
	out := r.Execute(t.Context(), a.srv, in, a.activeVars(), nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	for name, want := range map[string]string{
		"X-Env": "env-current", "X-Env-Fallback": "env-initial-fallback", "X-Env-Initial": "env-initial-only", "X-Env-Only": "env-current-only",
		"X-Global": "global-current", "X-Global-Fallback": "global-initial-fallback", "X-Global-Initial": "global-initial-only", "X-Global-Only": "global-current-only",
		"X-Collection": "collection-current", "X-Collection-Fallback": "collection-initial-fallback", "X-Collection-Initial": "collection-initial-only", "X-Collection-Only": "collection-current-only",
		"X-Folder": "folder-current", "X-Folder-Fallback": "folder-initial-fallback", "X-Folder-Initial": "folder-initial-only",
		"X-Local": "local-value", "X-Data": "data-value", "X-Disabled": "{{disabled}}",
	} {
		if got.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, got.Get(name), want)
		}
	}
	sources := map[string]string{}
	for _, v := range out.Variables {
		sources[v.Name] = v.Source
	}
	for name, want := range map[string]string{
		"env": "Run input", "collection": "Collection: Shop", "folder": "Collection: Shop/Orders",
		"global": "Global", "local": "Pre-request script", "data": "Data file",
	} {
		if sources[name] != want {
			t.Errorf("source of %s = %q, want %q", name, sources[name], want)
		}
	}
}

// Script writes are Current values: they never rewrite an environment's,
// global's or collection's Initial value, and an export carries Initial only.
func TestScriptWritesAreCurrentAndExportsCarryInitial(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	a.envs = []environment{{Name: "Local",
		Vars:    map[string]string{"public": "initial", "untouched": "keep"},
		Current: map[string]string{"token": "old-local"},
	}}
	a.activeEnv = "Local"
	a.globals = map[string]string{"team": "initial-team"}
	a.folders = []request.Folder{{Path: "Shop", Variables: []request.Variable{{Name: "polls", Initial: "0"}}}}
	r := request.Request{Method: "GET", Collection: "Shop", URL: upstream.URL,
		Pre: `pm.environment.set("public", "script"); pm.environment.set("token", "new-local"); pm.globals.set("team", "script-team"); pm.collectionVariables.set("polls", "3")`}
	before := a.activeVars()
	out := r.Execute(t.Context(), a.srv, a.sendContext(r), before, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	a.applyVars("Local", before, out.Vars)
	a.applyWrites(out.Writes)

	env := a.envs[0]
	if env.Vars["public"] != "initial" || env.Vars["untouched"] != "keep" || env.Vars["token"] != "" {
		t.Errorf("script rewrote Initial environment values: %+v", env.Vars)
	}
	if env.Current["public"] != "script" || env.Current["token"] != "new-local" {
		t.Errorf("script writes did not land in Current: %+v", env.Current)
	}
	if a.globals["team"] != "initial-team" || a.globalsCurrent["team"] != "script-team" {
		t.Errorf("global write changed Initial: %+v / %+v", a.globals, a.globalsCurrent)
	}
	if len(a.folders) != 1 || a.folders[0].Variables[0].Initial != "0" || a.folders[0].Variables[0].Current != "" {
		t.Errorf("collection write changed Initial: %+v", a.folders)
	}
	if a.folderCurrent["Shop"]["polls"] != "3" {
		t.Errorf("collection write did not stay local: %+v", a.folderCurrent)
	}

	// The local file round-trips every scope.
	f, err := loadEnvs(&store.File{Path: a.envsPath})
	if err != nil || f.Envs[0].Current["public"] != "script" || f.GlobalsCurrent["team"] != "script-team" || f.FolderCurrent["Shop"]["polls"] != "3" {
		t.Fatalf("local file = %+v, %v", f, err)
	}

	// Postman environment and globals exports carry Initial values only.
	a.openVars()
	a.exportPostmanVariables()
	exported := string(a.exportUI.data)
	if !strings.Contains(exported, `"initial"`) || !strings.Contains(exported, `"keep"`) || strings.Contains(exported, `"script"`) || strings.Contains(exported, `"new-local"`) {
		t.Errorf("environment export = %s", exported)
	}
	a.editGlobals()
	a.exportPostmanVariables()
	exported = string(a.exportUI.data)
	if !strings.Contains(exported, `"initial-team"`) || strings.Contains(exported, `"script-team"`) {
		t.Errorf("globals export = %s", exported)
	}

	// A collection file cannot carry a Current value at all.
	data, _, err := postman.WriteCollection("Shop", nil, []request.Folder{{Path: "Shop", Variables: []request.Variable{
		{Name: "base", Initial: "shared", Current: "local-secret"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"shared"`) || strings.Contains(string(data), "local-secret") {
		t.Errorf("collection export = %s", data)
	}
}

// A fresh environment has no folderCurrent map at all; saving a folder's
// Current value must create it instead of panicking on a nil map.
func TestSaveFolderCreatesMissingCurrentMap(t *testing.T) {
	a := workspaceApp()
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	a.folders = []request.Folder{{Path: "Shop", Variables: []request.Variable{{Name: "base", Initial: "shared"}}}}
	a.folderCurrent = nil
	a.folderCredentials = nil
	a.folderUI = folderEditor{
		open: true, name: "Shop", draft: request.Folder{Path: "Shop"},
		variables: []variableRow{{Name: "base", Initial: "shared", HasInitial: true, Current: "local", HasCurrent: true}},
	}
	a.saveFolder()
	if a.folderUI.problem != "" {
		t.Fatalf("save failed: %s", a.folderUI.problem)
	}
	if got := a.folderCurrent["Shop"]["base"]; got != "local" {
		t.Fatalf("in-memory Current = %q, want local", got)
	}
	f, err := loadEnvs(&store.File{Path: a.envsPath})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.FolderCurrent["Shop"]["base"]; got != "local" {
		t.Fatalf("saved Current = %q, want local; file %+v", got, f)
	}
}

// A script unset removes the local override and falls back to the Initial
// value, both during the send and after it; a Current-only name disappears.
func TestScriptUnsetFallsBackToInitial(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	a.envs = []environment{{Name: "Local",
		Vars:    map[string]string{"env": "env-initial", "gone": "gone-initial"},
		Current: map[string]string{"env": "env-current", "gone": "gone-current", "only": "only-current"},
	}}
	a.activeEnv = "Local"
	a.globals = map[string]string{"team": "team-initial"}
	a.globalsCurrent = map[string]string{"team": "team-current"}
	a.folders = []request.Folder{{Path: "Shop", Variables: []request.Variable{{Name: "collection", Initial: "collection-initial"}}}}
	a.folderCurrent = map[string]map[string]string{"Shop": {"collection": "collection-current"}}
	r := request.Request{Method: "GET", Collection: "Shop", URL: upstream.URL,
		Headers: strings.Join([]string{
			"X-Env: {{env}}", "X-Gone: {{gone}}", "X-Only: {{only}}",
			"X-Global: {{team}}", "X-Collection: {{collection}}",
		}, "\n"),
		Pre: `pm.environment.unset("env"); pm.environment.unset("gone"); pm.environment.unset("only"); pm.globals.unset("team"); pm.collectionVariables.unset("collection")`,
	}
	before := a.activeVars()
	out := r.Execute(t.Context(), a.srv, a.sendContext(r), before, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	for name, want := range map[string]string{
		"X-Env": "env-initial", "X-Gone": "gone-initial", "X-Only": "{{only}}",
		"X-Global": "team-initial", "X-Collection": "collection-initial",
	} {
		if got.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, got.Get(name), want)
		}
	}
	a.applyVars("Local", before, out.Vars, out.Writes...)
	a.applyWrites(out.Writes)
	if a.envs[0].Current["env"] != "" || a.envs[0].Current["gone"] != "" {
		t.Errorf("unset left Current values: %+v", a.envs[0].Current)
	}
	if _, ok := a.envs[0].Current["only"]; ok {
		t.Errorf("unset left a Current-only value: %+v", a.envs[0].Current)
	}
	if a.globalsCurrent["team"] != "" {
		t.Errorf("global unset left a Current value: %+v", a.globalsCurrent)
	}
	if _, ok := a.folderCurrent["Shop"]; ok {
		t.Errorf("collection unset left a Current value: %+v", a.folderCurrent)
	}
	if a.envs[0].Vars["env"] != "env-initial" || a.globals["team"] != "team-initial" || a.folders[0].Variables[0].Initial != "collection-initial" {
		t.Errorf("unset changed Initial values: %+v / %+v / %+v", a.envs[0].Vars, a.globals, a.folders)
	}
}

// Collection Current values follow a rename, disappear with a deletion, and
// never enter the collection directory.
func TestFolderCurrentFollowsRenameAndDeletion(t *testing.T) {
	a := workspaceApp()
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	a.folders = []request.Folder{{Path: "Shop", Variables: []request.Variable{{Name: "base", Initial: "shared"}}}}
	a.folderCurrent = map[string]map[string]string{"Shop": {"base": "local", "only": "local-only"}}
	if got := a.inheritance("Shop").Values(nil); got["base"] != "local" || got["only"] != "local-only" {
		t.Fatalf("inheritance = %v", got)
	}
	if err := a.saveLocalEnvs(); err != nil {
		t.Fatal(err)
	}
	f, err := loadEnvs(&store.File{Path: a.envsPath})
	if err != nil || f.FolderCurrent["Shop"]["base"] != "local" {
		t.Fatalf("local file = %+v, %v", f, err)
	}
	if err := a.renameFolder("Shop", "Store"); err != nil {
		t.Fatal(err)
	}
	if a.folderCurrent["Shop"] != nil || a.folderCurrent["Store"]["base"] != "local" {
		t.Fatalf("rename left Current values at the old path: %+v", a.folderCurrent)
	}
	if err := a.deleteAssets(deletion{path: "Store"}); err != nil {
		t.Fatal(err)
	}
	if len(a.folderCurrent) != 0 {
		t.Fatalf("deletion left Current values: %+v", a.folderCurrent)
	}
}

// The table's actions turn a local override into a shared Initial value, or
// drop it so Initial applies again.
func TestVariablesTablePersistsAndResetsCurrent(t *testing.T) {
	a := workspaceApp()
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	a.envs = []environment{{Name: "Local", Vars: map[string]string{"base": "shared"}, Current: map[string]string{"base": "private"}}}
	a.activeEnv = "Local"
	a.openVars()
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("Persist current value 1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.envs[0].Vars["base"] != "private" || a.envs[0].Current["base"] != "private" {
		t.Fatalf("persist = %+v", a.envs[0])
	}
	a.openVars()
	tt.Frame()
	if err := tt.Click("Reset current value 1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.envs[0].Vars["base"] != "private" || a.envs[0].Current["base"] != "" {
		t.Fatalf("reset = %+v", a.envs[0])
	}
}

// Persisting all current values leaves credential slots and credential-like
// names local, including camelCase names, and reports what it skipped.
func TestPersistAllCurrentSkipsCredentials(t *testing.T) {
	rows := []variableRow{
		{Name: "base_url", Initial: "https://shared", Current: "https://local", HasInitial: true, HasCurrent: true},
		{Name: "accessToken", Initial: "shared-token", Current: "local-token", HasInitial: true, HasCurrent: true},
		{Name: "session", Initial: "", Current: "local-session", HasInitial: true, HasCurrent: true, Credential: true},
		{Name: "unchanged", Initial: "same", HasInitial: true},
	}
	persisted, skipped := persistAllCurrent(rows)
	if persisted != 1 || skipped != 2 {
		t.Fatalf("persisted %d, skipped %d; want 1 and 2", persisted, skipped)
	}
	if rows[0].Initial != "https://local" {
		t.Errorf("ordinary Current was not persisted: %+v", rows[0])
	}
	if rows[1].Initial != "shared-token" || rows[2].Initial != "" {
		t.Errorf("a credential was persisted: %+v / %+v", rows[1], rows[2])
	}
	if rows[1].Current != "local-token" || rows[2].Current != "local-session" {
		t.Errorf("Current values changed: %+v / %+v", rows[1], rows[2])
	}
}

// A credential row keeps its mark after Reset current and after a new Current
// value is entered, so Persist current stays disabled for it.
func TestCredentialMarkSurvivesResetAndRefill(t *testing.T) {
	rows := []variableRow{{Name: "token", Initial: "shared", HasInitial: true, Credential: true}}
	_, current, credentials, err := variablesOf(rows)
	if err != nil || current["token"] != "" || !credentials["token"] {
		t.Fatalf("after reset: current %v, credentials %v, err %v", current, credentials, err)
	}
	rows[0].HasCurrent, rows[0].Current = true, "new-local"
	_, current, credentials, err = variablesOf(rows)
	if err != nil || current["token"] != "new-local" || !credentials["token"] {
		t.Fatalf("after refill: current %v, credentials %v, err %v", current, credentials, err)
	}
	_, _, folderCredentials, err := folderVariablesOf(rows)
	if err != nil || !folderCredentials["token"] {
		t.Fatalf("folder credentials after refill: %v, err %v", folderCredentials, err)
	}
}

func TestVariableHintsPreferCurrentAndMaskCredentials(t *testing.T) {
	a := workspaceApp()
	a.envs = []environment{{Name: "Local",
		Vars:    map[string]string{"base": "https://initial", "token": "shared-token"},
		Current: map[string]string{"base": "https://current", "token": "private-token"},
	}}
	a.activeEnv = "Local"
	a.globals = map[string]string{"team": "core"}
	a.folders = []request.Folder{{Path: "Shop", Variables: []request.Variable{
		{Name: "region", Initial: "eu"},
		{Name: "fallback", Initial: "from-initial"},
	}}}
	a.folderCurrent = map[string]map[string]string{"Shop": {"fallback": ""}}
	hints := map[string]variableHint{}
	for _, h := range a.variableHints("Shop") {
		hints[h.Name] = h
	}
	for name, want := range map[string]struct{ value, source, display string }{
		"base":     {"https://current", "Environment · Local · current", "https://current"},
		"token":    {"private-token", "Environment · Local · current", "••••••"},
		"region":   {"eu", "Collection · Shop · initial", "eu"},
		"fallback": {"from-initial", "Collection · Shop · initial", "from-initial"},
		"team":     {"core", "Global · initial", "core"},
	} {
		h, ok := hints[name]
		if !ok || h.Value != want.value || h.source() != want.source || h.display() != want.display {
			t.Errorf("%s = %+v (%q, %q), want value %q source %q display %q", name, h, h.source(), h.display(), want.value, want.source, want.display)
		}
	}
	if h, ok := hints["$guid"]; !ok || !h.Dynamic || h.display() != "generated at send" {
		t.Errorf("dynamic hint = %+v, %v", h, ok)
	}
	marks := a.variableMarks("{{base}} {{missing}} {{token}} {{$guid}}", "Shop")
	if len(marks) != 4 || !marks[0].Defined || marks[1].Defined || !marks[2].Defined || !marks[3].Defined {
		t.Fatalf("marks = %+v", marks)
	}
	if marks[2].Hint.display() != "••••••" || marks[1].Hint.display() != "(empty)" {
		t.Errorf("mark display = %q / %q", marks[2].Hint.display(), marks[1].Hint.display())
	}
}

func TestVariableTokenEditing(t *testing.T) {
	for _, tc := range []struct {
		name, text, insert string
		caret, start       int
		want               string
		after              int
	}{
		{"open at end", "{{base", "baseUrl", 6, 0, "{{baseUrl}}", 11},
		{"existing close", "{{base}}/x", "baseUrl", 6, 0, "{{baseUrl}}/x", 11},
		{"middle of text", "a/{{ba?q=1", "baseUrl", 6, 2, "a/{{baseUrl}}?q=1", 13},
		{"no token", "plain", "baseUrl", 5, 0, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, _, ok := openVariableToken(tc.text, tc.caret)
			if ok != (tc.want != "") || start != tc.start {
				t.Fatalf("openVariableToken = %d, %v", start, ok)
			}
			got, after, ok := replaceVariableToken(tc.text, tc.insert, tc.caret)
			if ok != (tc.want != "") || got != tc.want || after != tc.after {
				t.Fatalf("replace = %q, %d, %v; want %q, %d", got, after, ok, tc.want, tc.after)
			}
		})
	}
	if got := runeToByte("a中b", 2); got != len("a中") {
		t.Errorf("runeToByte = %d", got)
	}
}

func TestVariableCompletionAndHoverInURL(t *testing.T) {
	a := workspaceApp()
	a.envs = []environment{{Name: "Local", Vars: map[string]string{"baseUrl": "https://api.example.com"}}}
	a.activeEnv = "Local"
	openDraft(a, a.flows[0])
	a.composeURL = ""
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	a.urlInput.Focus()
	tt.Frame()
	tt.Type("{{ba")
	tt.Frame()
	if !tt.HasText("baseUrl") || !tt.HasText("Environment · Local · initial") {
		t.Fatalf("no variable completion: %v", tt.Texts())
	}
	if err := tt.Click("baseUrl"); err != nil {
		t.Fatal(err)
	}
	if a.composeURL != "{{baseUrl}}" {
		t.Fatalf("completion wrote %q", a.composeURL)
	}
	a.composeURL = "{{baseUrl}}/projects?q={{missing}}"
	tt.Frame()
	rect, ok := tt.Find("URL")
	if !ok {
		t.Fatal("URL field missing")
	}
	tt.Move(rect.X+24, rect.Y+rect.H/2)
	tt.Advance(700 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("https://api.example.com") || !tt.HasText("undefined") {
		t.Fatalf("hover preview missing: %v", tt.Texts())
	}
}

func TestVariableCompletionInBodyEditor(t *testing.T) {
	a := workspaceApp()
	a.envs = []environment{{Name: "Local", Vars: map[string]string{"baseUrl": "https://api.example.com"}}}
	a.activeEnv = "Local"
	openDraft(a, a.flows[0])
	d := a.requestDocument
	d.composeTab, d.bodyMode, d.composeBody = composeTabBody, "Raw", ""
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("Request body"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, text := range []string{"{", "{", "b", "a"} {
		tt.Type(text)
		tt.Frame()
	}
	if !tt.HasText("baseUrl") {
		t.Fatalf("no editor completion: %v", tt.Texts())
	}
}

func TestVariableCompletionInHeaderAndAuthFields(t *testing.T) {
	a := workspaceApp()
	a.envs = []environment{{Name: "Local", Vars: map[string]string{"baseUrl": "https://api.example.com"}}}
	a.activeEnv = "Local"
	openDraft(a, a.flows[0])
	d := a.requestDocument
	d.composeTab = composeTabHeaders
	d.headerRows = []request.Field{{Name: "X-Test"}}
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("header value 1"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("{{ba")
	tt.Frame()
	if !tt.HasText("baseUrl") {
		t.Fatalf("no header completion: %v", tt.Texts())
	}
	if err := tt.Click("baseUrl"); err != nil {
		t.Fatal(err)
	}
	if got := d.headerRows[0].Value; got != "{{baseUrl}}" {
		t.Fatalf("header value = %q", got)
	}

	d.composeTab, d.auth.Type, d.auth.Token = composeTabAuth, "Bearer", ""
	tt.Frame()
	if err := tt.Click("Bearer token"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("{{ba")
	tt.Frame()
	if !tt.HasText("baseUrl") {
		t.Fatalf("no auth completion: %v", tt.Texts())
	}
	if err := tt.Click("baseUrl"); err != nil {
		t.Fatal(err)
	}
	if d.auth.Token != "{{baseUrl}}" {
		t.Fatalf("auth token = %q", d.auth.Token)
	}
}

func TestVariableHoverInBodyEditor(t *testing.T) {
	a := workspaceApp()
	a.envs = []environment{{Name: "Local", Vars: map[string]string{"baseUrl": "https://api.example.com"}}}
	a.activeEnv = "Local"
	openDraft(a, a.flows[0])
	d := a.requestDocument
	d.composeTab, d.bodyMode, d.composeBody = composeTabBody, "Raw", "{{baseUrl}}"
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if d.bodyEditor == nil || d.bodyEditor.OnHover == nil {
		t.Fatal("body editor hover is not wired")
	}
	d.bodyEditor.OnHover(editor.HoverRequest{Offset: 2})
	tt.Frame()
	if !tt.HasText("https://api.example.com") || !tt.HasText("Environment · Local · initial") {
		t.Fatalf("no editor hover: %v", tt.Texts())
	}
}

func TestSensitiveVariableNames(t *testing.T) {
	for name, want := range map[string]bool{
		"token": true, "access_token": true, "client_secret": true, "api_key": true,
		"authorization": true, "cookie": true, "password": true, "session_id": true,
		"accessToken": true, "apiKey": true, "clientSecret": true, "authToken": true,
		"APIToken": true, "HTTPSecret": true,
		"pwd": true, "pass": true, "bearer": true, "jwt": true, "x-api-key": true,
		"base_url": false, "monkey": false, "keyboard": false, "region": false,
	} {
		if got := sensitiveVariable(name); got != want {
			t.Errorf("sensitiveVariable(%q) = %v, want %v", name, got, want)
		}
	}
}
