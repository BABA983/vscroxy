package app

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/locales"
)

// variableRenameFixture has an environment definition, a collection definition
// shadowed in a folder, an unrelated {{tenant2}}, script literals and a folder
// field. It is small enough that every expected change can be named.
func variableRenameFixture(t *testing.T) (*app, savedRequest) {
	t.Helper()
	a := libraryApp(t)
	a.backupDir = filepath.Join(t.TempDir(), "backups")
	a.library.backupDir = a.backupDir
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	a.envs = []environment{
		{
			Name:    "Dev",
			Vars:    map[string]string{"token": "dev-token", "taken": "already here"},
			Current: map[string]string{"token": "local-token"},
		},
		{Name: "Staging", Vars: map[string]string{"token": "staging-token"}},
	}
	a.activeEnv = "Dev"
	a.globals = map[string]string{"global_token": "g"}
	a.folders = []request.Folder{
		{Path: "Shop", Variables: []request.Variable{{Name: "tenant", Initial: "shop"}, {Name: "shadow", Initial: "shop"}}},
		{Path: "Shop/Orders", Description: "Uses {{shadow}} here", Variables: []request.Variable{{Name: "shadow", Initial: "orders"}}},
	}
	first := savedRequest{Request: request.Request{
		ID: "one", Name: "List", Collection: "Shop", Method: "GET",
		URL:     "https://api.test/{{tenant}}/{{tenant2}}",
		Headers: "X-Token: {{token}}\nX-Tenant: {{tenant}}",
		Body:    `{"tenant":"{{tenant}}","token":"{{token}}","shadow":"{{shadow}}"}`,
		Pre:     `pm.environment.get("token"); pm.variables.set("token", "local"); pm.variables.set("tenant", "local")`,
		Post:    `pm.globals.set("global_token", "value")`,
		Auth:    request.Auth{Type: "Bearer", Token: "{{tenant}}"},
		Form:    []request.Field{{Name: "tenant", Value: "{{tenant}}"}},
	}}
	second := savedRequest{Request: request.Request{
		ID: "two", Name: "Order", Collection: "Shop/Orders", Method: "POST",
		URL:  "https://api.test/{{shadow}}",
		Body: "{{shadow}}",
		Pre:  `pm.collectionVariables.set("shadow", "x")`,
	}}
	form := savedRequest{Request: request.Request{
		ID: "three", Name: "Form", Collection: "Shop", Method: "POST",
		URL: "https://api.test/form", BodyMode: "URL encoded",
		Form: []request.Field{{Name: "tenant", Value: "{{tenant}}"}},
	}}
	a.requests = []savedRequest{first, second, form}
	if err := a.persistRequests(a.requests); err != nil {
		t.Fatal(err)
	}
	return a, first
}

func findMatch(matches []variableMatch, code, field string) (variableMatch, bool) {
	for _, m := range matches {
		if m.Code == code && strings.Contains(m.Field, field) {
			return m, true
		}
	}
	return variableMatch{}, false
}

// References use request.VariableRefs in every authored field, and also see
// pm.* string literals. An unrelated {{name2}} is never a result.
func TestVariableReferencesAcrossFieldsAndScriptLiterals(t *testing.T) {
	a, _ := variableRenameFixture(t)
	matches := a.variableMatches("tenant")
	fields := make(map[string]bool)
	for _, m := range matches {
		fields[m.Field] = true
		if strings.Contains(m.Text[m.Start:m.End], "tenant2") {
			t.Fatalf("unrelated {{tenant2}} listed as a tenant reference: %+v", m)
		}
	}
	for _, want := range []string{locales.Url(), locales.Headers(), locales.RequestBody(), locales.FormFields(), locales.Auth()} {
		if !slices.ContainsFunc(matches, func(m variableMatch) bool { return strings.Contains(m.Field, want) }) {
			t.Errorf("no %s field among %v", want, fields)
		}
	}
	if script, ok := findMatch(matches, problemFieldPre, ""); !ok || script.Text[script.Start:script.End] != "tenant" {
		// The tenant use in the script is a pm.variables.set literal.
		t.Error("no script literal reference for pm.variables.set(\"tenant\")")
	}
	defs := 0
	for _, m := range matches {
		if m.Kind == "definition" {
			defs++
		}
	}
	if defs == 0 {
		t.Error("no collection definition in the references")
	}
	token := a.variableMatches("token")
	script, ok := findMatch(token, problemFieldPre, "")
	if !ok || script.Kind != "script" || script.Text[script.Start:script.End] != "token" {
		t.Fatalf("pm.environment.get literal not found: %+v", token)
	}
	global, ok := findMatch(a.variableMatches("global_token"), problemFieldPost, "")
	if !ok || global.Scope.Kind != "Global" {
		t.Fatalf("pm.globals.set not scoped to globals: %+v", global)
	}
	folder, ok := findMatch(a.variableMatches("shadow"), problemFieldDescription, "")
	if !ok || folder.Owner != "folder" {
		t.Error("folder description reference missing")
	}
}

func TestPMLiteralRefsOnlyReadFirstArgument(t *testing.T) {
	text := `pm.environment.get("token"); pm.variables.set('token', pm.variables.get("other")); pm.response.code`
	refs := scriptVariableRefs(text)
	if len(refs) != 3 || refs[0].Name != "token" || refs[1].Name != "token" || refs[2].Name != "other" {
		t.Fatalf("pm literals = %+v", refs)
	}
	if refs[0].Store != "environment" || refs[1].Store != "variables" {
		t.Fatalf("stores = %+v", refs)
	}
}

// A collection variable shadowed by a folder definition renames only the
// collection definition and uses that actually resolve to the collection.
func TestRenameRespectsScopedShadowing(t *testing.T) {
	a, _ := variableRenameFixture(t)
	matches := a.variableMatches("shadow")
	collection := renameChanges(matches, variableTarget{Name: "shadow", Scope: folderScope("Shop"), Defined: true})
	for _, m := range collection {
		if m.Owner == "folder-def" && m.Key == "Shop/Orders" || m.Owner == "folder" ||
			m.Collection == "Shop/Orders" && strings.Contains(m.Text[m.Start:m.End], "{{shadow}}") {
			t.Errorf("shadowed folder use/definition included in collection rename: %+v", m)
		}
	}
	if len(collection) < 2 {
		t.Fatalf("collection rename should include its definition and a use, got %+v", collection)
	}
	folder := renameChanges(matches, variableTarget{Name: "shadow", Scope: folderScope("Shop/Orders"), Defined: true})
	for _, m := range folder {
		if strings.Contains(m.Resource, "Definition") && strings.Contains(m.Field, "Shop") && m.Key == "Shop" {
			t.Errorf("collection definition included in folder rename: %+v", m)
		}
	}
	if !slices.ContainsFunc(folder, func(m variableMatch) bool { return m.Owner == "folder" && m.Code == problemFieldDescription }) {
		t.Error("folder's own description use missing from folder rename")
	}
	if !slices.ContainsFunc(folder, func(m variableMatch) bool { return m.Resource == "Order" && m.Kind == "reference" }) {
		t.Error("requests resolving to the folder variable were not renamed")
	}
}

// The preview is a bulk edit: unchecking a change leaves that byte untouched
// while the definition and the other checked uses still reach the library.
func TestRenamePreviewUncheckedChangesAreNotApplied(t *testing.T) {
	a, _ := variableRenameFixture(t)
	target := variableTarget{Name: "token", Scope: variableScope{Kind: "Environment", Path: "Dev"}, Defined: true}
	a.openVariableRename(target)
	a.variableRename.input = "token_renamed"
	a.prepareVariableRename()
	if a.variableRename.problem != "" || len(a.variableRename.changes) < 3 {
		t.Fatalf("preview problem=%q changes=%+v", a.variableRename.problem, a.variableRename.changes)
	}
	for i := range a.variableRename.changes {
		if a.variableRename.changes[i].Code == problemFieldBody {
			a.variableRename.changes[i].Checked = false
		}
	}
	if !a.applyVariableRename() {
		t.Fatal("rename was not applied")
	}
	_, oldKey := a.envs[0].Vars["token"]
	_, newKey := a.envs[0].Vars["token_renamed"]
	if oldKey || !newKey {
		t.Fatalf("definition was not renamed: %v", a.envs[0].Vars)
	}
	if !strings.Contains(a.requests[0].Headers, "{{token_renamed}}") {
		t.Errorf("checked header use was not renamed: %q", a.requests[0].Headers)
	}
	if !strings.Contains(a.requests[0].Pre, `pm.environment.get("token_renamed")`) {
		t.Errorf("checked script literal was not renamed: %q", a.requests[0].Pre)
	}
	if !strings.Contains(a.requests[0].Body, "{{token}}") {
		t.Errorf("unchecked body use was renamed: %q", a.requests[0].Body)
	}
}

func TestRenameValidatesNameAndWarnsOnCollision(t *testing.T) {
	a, _ := variableRenameFixture(t)
	target := variableTarget{Name: "token", Scope: variableScope{Kind: "Environment", Path: "Dev"}, Defined: true}
	for _, input := range []string{"bad name", "bad{name}", ""} {
		a.openVariableRename(target)
		a.variableRename.input = input
		a.prepareVariableRename()
		if a.variableRename.preview || a.variableRename.problem == "" {
			t.Errorf("name %q was accepted with problem %q", input, a.variableRename.problem)
		}
	}
	a.openVariableRename(target)
	a.variableRename.input = "taken"
	a.prepareVariableRename()
	if !a.variableRename.preview || a.variableRename.warning == "" {
		t.Fatalf("no collision warning: %+v", a.variableRename)
	}
	if !strings.Contains(a.variableRename.warning, "taken") {
		t.Errorf("warning does not name the collision: %q", a.variableRename.warning)
	}
	if a.applyVariableRename() {
		t.Error("a colliding rename was applied")
	}
	if _, ok := a.envs[0].Vars["token"]; !ok {
		t.Error("collision attempt changed the definition")
	}
}

// A draft's own edits are never saved by a rename. The rename reaches both
// the saved copy and the open tab, but the tab keeps its edits and stays dirty
// until the reader explicitly saves it.
func TestRenameUpdatesDirtyDocumentAndKeepsOtherEdits(t *testing.T) {
	a, first := variableRenameFixture(t)
	a.openSaved(first.key())
	d := a.requestDocument
	d.composeBody += "\n// my local edit"
	d.composeURL += "?keep=this"
	if !d.dirty() {
		t.Fatal("the fixture tab is not dirty")
	}
	target := variableTarget{Name: "token", Scope: variableScope{Kind: "Environment", Path: "Dev"}, Defined: true}
	a.openVariableRename(target)
	a.variableRename.input = "token_renamed"
	a.prepareVariableRename()
	a.applyVariableRename()

	// The draft keeps its edits and the rename, and remains dirty against a
	// baseline that holds only the renamed saved request.
	if !strings.Contains(d.composeBody, "// my local edit") || !strings.Contains(d.composeURL, "?keep=this") {
		t.Fatalf("other draft edits were lost: body=%q url=%q", d.composeBody, d.composeURL)
	}
	if !strings.Contains(d.composeHeaders, "{{token_renamed}}") || !strings.Contains(d.composePre, `pm.environment.get("token_renamed")`) {
		t.Fatalf("rename did not reach the open draft: headers=%q pre=%q", d.composeHeaders, d.composePre)
	}
	if !d.dirty() {
		t.Errorf("the reader's edits are no longer unsaved: baseline=%+v", d.baseline)
	}
	if strings.Contains(d.baseline.Body, "// my local edit") || strings.Contains(d.baseline.URL, "?keep=this") {
		t.Fatalf("the baseline adopted the reader's edits: %+v", d.baseline)
	}
	if !strings.Contains(d.baseline.Headers, "{{token_renamed}}") {
		t.Errorf("the baseline lost the library rename: %+v", d.baseline)
	}

	// The file has the rename, not the draft's unsaved edits.
	at := slices.IndexFunc(a.requests, func(r savedRequest) bool { return r.ID == "one" })
	if at < 0 || strings.Contains(a.requests[at].Body, "// my local edit") || strings.Contains(a.requests[at].URL, "?keep=this") ||
		!strings.Contains(a.requests[at].Headers, "{{token_renamed}}") {
		t.Fatalf("a rename saved the reader's draft: %+v", a.requests[at])
	}
	path, err := a.savedRequestPath(a.requests[at])
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(onDisk), "// my local edit") || strings.Contains(string(onDisk), "?keep=this") ||
		!strings.Contains(string(onDisk), "{{token_renamed}}") {
		t.Fatalf("wrong bytes on disk after the rename: %s", onDisk)
	}
	backups, err := store.ListBackups(a.backupDir, path)
	if err != nil || len(backups) == 0 {
		t.Fatalf("no local history backup for the rename: %v, %v", backups, err)
	}
	if !a.saveComposed() {
		t.Fatal("saving the dirty tab afterwards failed")
	}
	onDisk, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), "// my local edit") || !strings.Contains(string(onDisk), "?keep=this") ||
		!strings.Contains(string(onDisk), "{{token_renamed}}") {
		t.Fatalf("the tab's own save lost its edits or rename: %s", onDisk)
	}
}

func TestRenameKeepsCleanOpenTabClean(t *testing.T) {
	a, first := variableRenameFixture(t)
	a.openSaved(first.key())
	d := a.requestDocument
	if d.dirty() {
		t.Fatal("the open tab starts dirty")
	}
	target := variableTarget{Name: "token", Scope: variableScope{Kind: "Environment", Path: "Dev"}, Defined: true}
	a.openVariableRename(target)
	a.variableRename.input = "token_renamed"
	a.prepareVariableRename()
	a.applyVariableRename()
	if d.dirty() {
		t.Errorf("a clean tab became dirty after the rename: baseline=%+v value=%+v", d.baseline, d.value())
	}
	if !strings.Contains(d.composeHeaders, "{{token_renamed}}") {
		t.Errorf("the open tab missed the rename: %q", d.composeHeaders)
	}
	loaded, err := loadLibraryRequests(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	at := slices.IndexFunc(loaded, func(r savedRequest) bool { return r.ID == "one" })
	if at < 0 || !strings.Contains(loaded[at].Headers, "{{token_renamed}}") {
		t.Fatalf("disk missed the rename: %+v", loaded)
	}
}

// An outside edit refuses the write through writeLibrary's conflict error,
// which the ordinary fail path turns into Compare/Overwrite choices.
func TestRenameSurfacesSaveConflictThroughExistingFlow(t *testing.T) {
	a, first := variableRenameFixture(t)
	target, err := a.savedRequestPath(first)
	if err != nil {
		t.Fatal(err)
	}
	outside := a.requests[0]
	outside.URL = "https://outside.example"
	data, err := marshalFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteFileVersioned(target, store.Collection, data, a.backupDir); err != nil {
		t.Fatal(err)
	}

	name := variableTarget{Name: "token", Scope: variableScope{Kind: "Environment", Path: "Dev"}, Defined: true}
	a.openVariableRename(name)
	a.variableRename.input = "token_renamed"
	a.prepareVariableRename()
	a.applyVariableRename()
	notice := a.notices[len(a.notices)-1]
	if notice.action != locales.CompareWithDisk() || notice.alternate != locales.OverwriteWithDraft() || notice.source == nil {
		t.Fatalf("conflict did not offer the usual choices: %+v", notice)
	}
	notice.run()
	conflict := a.conflict
	if conflict == nil || !conflict.open {
		t.Fatal("Compare did not open the draft/disk view")
	}
	draft := conflict.drafts[conflict.files[0].Name]
	if draft == nil || !strings.Contains(draft.String(), "{{token_renamed}}") {
		t.Fatalf("draft side lost the rename: %v", draft)
	}
	onDisk, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(onDisk), "outside.example") {
		t.Fatalf("outside change was overwritten before confirmation: %q, %v", onDisk, err)
	}
}

// A variable can be defined in several environments. The preview offers each
// other environment's definition as its own checked change, and unchecking
// one leaves that environment exactly as it was.
func TestRenameOffersEveryEnvironmentDefinition(t *testing.T) {
	a, _ := variableRenameFixture(t)
	target := variableTarget{Name: "token", Scope: variableScope{Kind: "Environment", Path: "Dev"}, Defined: true}
	a.openVariableRename(target)
	a.variableRename.input = "tok2"
	a.prepareVariableRename()
	seen := map[string]bool{}
	for i := range a.variableRename.changes {
		m := a.variableRename.changes[i]
		if m.Kind != "definition" || !strings.HasPrefix(m.Owner, "env-") {
			continue
		}
		if !m.Checked {
			t.Errorf("%s definition is not checked by default: %+v", m.Key, m)
		}
		seen[m.Key] = true
		if m.Key == "Staging" {
			a.variableRename.changes[i].Checked = false
		}
	}
	if !seen["Dev"] || !seen["Staging"] {
		t.Fatalf("preview missed an environment definition: %v", seen)
	}
	if !a.applyVariableRename() {
		t.Fatal("rename was not applied")
	}
	if _, ok := a.envs[0].Vars["tok2"]; !ok {
		t.Errorf("Dev definition was not renamed: %v", a.envs[0].Vars)
	}
	if _, ok := a.envs[1].Vars["token"]; !ok || a.envs[1].Vars["tok2"] != "" {
		t.Errorf("unchecked Staging definition was renamed: %v", a.envs[1].Vars)
	}
	if !strings.Contains(a.requests[0].Headers, "{{tok2}}") {
		t.Errorf("uses were not renamed: %q", a.requests[0].Headers)
	}
}

func uncheckRenameDefinitions(a *app) {
	for i := range a.variableRename.changes {
		if a.variableRename.changes[i].Kind == "definition" {
			a.variableRename.changes[i].Checked = false
		}
	}
}

// Even with no tab open and the definition unchecked, the checked uses must
// reach disk. Reloading from the library tree is the contract.
func TestRenameUnopenedSavedRequestIsWritten(t *testing.T) {
	a, _ := variableRenameFixture(t)
	target := variableTarget{Name: "token", Scope: variableScope{Kind: "Environment", Path: "Dev"}, Defined: true}
	a.openVariableRename(target)
	a.variableRename.input = "tok2"
	a.prepareVariableRename()
	uncheckRenameDefinitions(a)
	if !a.applyVariableRename() {
		t.Fatal("rename was not applied")
	}
	loaded, err := loadLibraryRequests(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	at := slices.IndexFunc(loaded, func(r savedRequest) bool { return r.ID == "one" })
	if at < 0 || !strings.Contains(loaded[at].Headers, "{{tok2}}") || !strings.Contains(loaded[at].Pre, `"tok2"`) {
		t.Fatalf("disk lost the rename of the unopened request: %+v", loaded)
	}
	if _, ok := a.envs[0].Vars["token"]; !ok {
		t.Error("unchecked definition was renamed")
	}
}

func TestRenameFolderFieldIsWritten(t *testing.T) {
	a, _ := variableRenameFixture(t)
	target := variableTarget{Name: "shadow", Scope: folderScope("Shop/Orders"), Defined: true}
	a.openVariableRename(target)
	a.variableRename.input = "sh2"
	a.prepareVariableRename()
	// Isolate the folder field: a request in this folder also uses {{shadow}}.
	for i := range a.variableRename.changes {
		m := a.variableRename.changes[i]
		a.variableRename.changes[i].Checked = m.Owner == "folder" && m.Code == problemFieldDescription
	}
	if !a.applyVariableRename() {
		t.Fatal("rename was not applied")
	}
	loaded, folders, _, err := loadLibrary(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range loaded {
		if strings.Contains(r.URL, "{{sh2}}") || strings.Contains(r.Body, "{{sh2}}") {
			t.Errorf("unchecked request use was renamed: %s", r.Name)
		}
	}
	at := slices.IndexFunc(folders, func(f request.Folder) bool { return f.Path == "Shop/Orders" })
	if at < 0 || !strings.Contains(folders[at].Description, "{{sh2}}") {
		t.Fatalf("disk lost the folder field rename: %+v", folders)
	}
	if folders[at].Variables[0].Name != "shadow" {
		t.Errorf("unchecked folder definition was renamed: %+v", folders[at].Variables)
	}
}

// A refused library write must leave the library, scopes and open draft as
// they were; only the conflict view holds the proposed bytes.
func TestRenameConflictLeavesLiveStateUnchanged(t *testing.T) {
	a, first := variableRenameFixture(t)
	a.openSaved(first.key())
	d := a.requestDocument
	d.composeBody += "\n// local edit to keep"

	second := a.requests[1]
	target, err := a.savedRequestPath(second)
	if err != nil {
		t.Fatal(err)
	}
	outside := second
	outside.URL = "https://outside.example"
	data, err := marshalFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteFileVersioned(target, store.Collection, data, a.backupDir); err != nil {
		t.Fatal(err)
	}

	requestsBefore := slices.Clone(a.requests)
	foldersBefore := slices.Clone(a.folders)
	envsBefore := slices.Clone(a.envs)
	draftBefore := d.composeBody
	name := variableTarget{Name: "shadow", Scope: folderScope("Shop/Orders"), Defined: true}
	a.openVariableRename(name)
	a.variableRename.input = "sh2"
	a.prepareVariableRename()
	uncheckRenameDefinitions(a)
	if a.applyVariableRename() {
		t.Fatal("a conflicting rename was reported as applied")
	}
	if !reflect.DeepEqual(a.requests, requestsBefore) {
		t.Errorf("requests changed after the refused write: %+v", a.requests)
	}
	if !reflect.DeepEqual(a.folders, foldersBefore) {
		t.Errorf("folders changed after the refused write: %+v", a.folders)
	}
	if !reflect.DeepEqual(a.envs, envsBefore) {
		t.Errorf("environments changed after the refused write: %+v", a.envs)
	}
	if d.composeBody != draftBefore {
		t.Errorf("open draft changed after the refused write: %q", d.composeBody)
	}
	notice := a.notices[len(a.notices)-1]
	if notice.action != locales.CompareWithDisk() {
		t.Fatalf("conflict did not offer Compare: %+v", notice)
	}
	notice.run()
	if a.conflict == nil || a.conflict.drafts[conflictFileName(t, a)].String() == "" {
		t.Fatal("conflict view has no proposed draft")
	}
}

func conflictFileName(t *testing.T, a *app) string {
	t.Helper()
	if a.conflict == nil || len(a.conflict.files) == 0 {
		t.Fatal("no conflict files")
	}
	return a.conflict.files[0].Name
}

// A comment and a longer variable name in the draft must not shift which
// parsed ref is renamed in the saved copy. The old ordinal-by-substring code
// skipped the use here and left get("token") on disk.
func TestRenameSkipsCommentsAndLongerNamesInSavedCopy(t *testing.T) {
	a, first := variableRenameFixture(t)
	script := `pm.environment.get("token_id"); pm.environment.get("token")`
	a.requests[0].Pre = script
	if err := a.persistRequests(a.requests); err != nil {
		t.Fatal(err)
	}
	a.openSaved(first.key())
	d := a.requestDocument
	d.composePre = "// token note\n" + script

	target := variableTarget{Name: "token", Scope: variableScope{Kind: "Environment", Path: "Dev"}, Defined: true}
	a.openVariableRename(target)
	a.variableRename.input = "tok2"
	a.prepareVariableRename()
	a.applyVariableRename()

	loaded, err := loadLibraryRequests(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	at := slices.IndexFunc(loaded, func(r savedRequest) bool { return r.ID == "one" })
	want := `pm.environment.get("token_id"); pm.environment.get("tok2")`
	if at < 0 || loaded[at].Pre != want {
		t.Fatalf("saved Pre = %q, want %q", loaded[at].Pre, want)
	}
	if !strings.Contains(d.composePre, "// token note\n") || !strings.Contains(d.composePre, `get("tok2")`) ||
		!strings.Contains(d.composePre, `get("token_id")`) {
		t.Fatalf("draft lost its comment or rename: %q", d.composePre)
	}
	if !d.dirty() {
		t.Error("the draft's own comment is no longer an unsaved edit")
	}
}

// When the draft and saved copy disagree on the parsed count, mapping by
// ordinal is unsafe. The draft here deletes one of two header lines; the
// ordinal path would rename only A, while the fallback must rewrite both
// saved refs so the file stays consistent on its own.
func TestRenameSavedFallbackOnDifferingUseCounts(t *testing.T) {
	a, first := variableRenameFixture(t)
	a.requests[0].Headers = "A: {{token}}\nB: {{token}}"
	if err := a.persistRequests(a.requests); err != nil {
		t.Fatal(err)
	}
	a.openSaved(first.key())
	d := a.requestDocument
	draftHeaders := "B: {{token}}"
	d.composeHeaders, d.headerFor = draftHeaders, draftHeaders
	d.headerRows = parseHeaderRows(draftHeaders)

	target := variableTarget{Name: "token", Scope: variableScope{Kind: "Environment", Path: "Dev"}, Defined: true}
	a.openVariableRename(target)
	a.variableRename.input = "tok2"
	a.prepareVariableRename()
	a.applyVariableRename()

	loaded, err := loadLibraryRequests(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	at := slices.IndexFunc(loaded, func(r savedRequest) bool { return r.ID == "one" })
	want := "A: {{tok2}}\nB: {{tok2}}"
	if at < 0 || loaded[at].Headers != want {
		t.Fatalf("saved headers = %q, want %q", loaded[at].Headers, want)
	}
	if d.composeHeaders != "B: {{tok2}}" {
		t.Errorf("draft header = %q, want only the reader's remaining line renamed", d.composeHeaders)
	}
}
