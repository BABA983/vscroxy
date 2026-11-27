package app

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/vault"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// searchReplaceFixture writes its authored assets once, as an app that was
// relaunched would read them. Every test starts from that same disk state.
func searchReplaceFixture(t *testing.T) (*app, savedRequest) {
	t.Helper()
	a := libraryApp(t)
	a.backupDir = filepath.Join(t.TempDir(), "backups")
	a.library.backupDir = a.backupDir
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	first := savedRequest{Request: request.Request{
		ID: "one", Name: "Orders", Collection: "Shop", Method: "GET",
		URL: "https://api.test/items", Body: "token tokenId\ntoken\n",
	}}
	a.requests = []savedRequest{first}
	a.folders = []request.Folder{{Path: "Shop", Description: "shop notes"}}
	a.envs = []environment{{Name: "Dev", Vars: map[string]string{"base": "https://api.test"}}}
	if err := a.persistRequests(a.requests); err != nil {
		t.Fatal(err)
	}
	return a, first
}

// prepareSearch runs the real scanner and puts its results in the view. It
// drives no timers: the channels close when the worker has finished.
func prepareSearch(t *testing.T, a *app, query, replacement string) []searchResult {
	t.Helper()
	q, err := compileSearchQuery(query, &a.searchAll)
	if err != nil {
		t.Fatal(err)
	}
	results := scanQuery(t, a, query).results
	a.searchAll.query, a.searchAll.input = q, query
	a.searchAll.armed = a.searchAll.request()
	a.searchAll.results = results
	a.searchAll.replace.input = replacement
	return results
}

func savedBody(t *testing.T, a *app, id string) string {
	t.Helper()
	loaded, err := loadLibraryRequests(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	at := slices.IndexFunc(loaded, func(r savedRequest) bool { return r.ID == id })
	if at < 0 {
		t.Fatalf("no saved request %s after apply", id)
	}
	return loaded[at].Body
}

// Regexp replacement follows Go's Expand: $1 and ${1} take the capture, $$
// is a literal dollar. Only the new text is written; the pattern is unchanged.
func TestSearchReplaceExpandsRegexpCaptures(t *testing.T) {
	a, _ := searchReplaceFixture(t)
	a.requests[0].Body = "alice@example.test\nbob@example.test\n"
	if err := a.persistRequests(a.requests); err != nil {
		t.Fatal(err)
	}
	a.searchAll.regexpQuery = true
	prepareSearch(t, a, `(\w+)@example\.test`, `${1}@example.org $$1`)
	for _, r := range a.searchAll.results {
		if !strings.Contains(r.match(), "@example.test") {
			t.Fatalf("regexp search = %+v", r)
		}
	}
	if !a.applySearchReplace() {
		t.Fatal("regexp replace was not applied")
	}
	want := "alice@example.org $1\nbob@example.org $1\n"
	if got := a.requests[0].Body; got != want {
		t.Errorf("memory body = %q, want %q", got, want)
	}
	if got := savedBody(t, a, "one"); got != want {
		t.Errorf("saved body = %q, want %q", got, want)
	}
}

// Whole word and case options are the ones the search used: tokenId must be
// left alone when token is replaced, including on a Unicode-adjacent line.
func TestSearchReplaceWholeWordLeavesLongerIdentifier(t *testing.T) {
	a, _ := searchReplaceFixture(t)
	// tokenId has a line to itself: without the whole-word check it becomes
	// an ordinary substring match and would be replaced too.
	a.requests[0].Body = "token\ntokenId\ntoken\n"
	if err := a.persistRequests(a.requests); err != nil {
		t.Fatal(err)
	}
	a.searchAll.wholeWord = true
	results := prepareSearch(t, a, "token", "scope")
	if len(results) != 2 {
		t.Fatalf("whole-word matches = %d (%+v), want 2", len(results), results)
	}
	if len(a.searchChanges()) != 2 {
		t.Fatalf("whole-word changes = %+v", a.searchChanges())
	}
	if !a.applySearchReplace() {
		t.Fatal("replace was not applied")
	}
	want := "scope\ntokenId\nscope\n"
	if got := a.requests[0].Body; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// All checked by default, then one unchecked: the same preview and apply
// path must leave one byte alone and write the other.
func TestSearchReplaceUncheckedChangeIsNotApplied(t *testing.T) {
	a, _ := searchReplaceFixture(t)
	results := prepareSearch(t, a, "token", "scope")
	if len(results) != 2 {
		t.Fatalf("matches = %d, want 2", len(results))
	}
	a.openSearchPreview()
	if !a.searchAll.replace.preview || len(a.searchAll.replace.changes) != 2 {
		t.Fatalf("preview not prepared: %+v", a.searchAll.replace)
	}
	a.searchAll.replace.changes[1].Checked = false
	if !a.applySearchReplace() {
		t.Fatal("replace was not applied")
	}
	want := "scope tokenId\ntoken\n"
	if got := a.requests[0].Body; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := savedBody(t, a, "one"); got != want {
		t.Errorf("saved body = %q, want %q", got, want)
	}
}

// No document is open: the only copy of the request is on disk, and a
// restart must read the replacement back.
func TestSearchReplaceWritesUnopenedSavedRequest(t *testing.T) {
	a, _ := searchReplaceFixture(t)
	prepareSearch(t, a, "Orders", "Invoices")
	if !a.applySearchReplace() {
		t.Fatal("replace was not applied")
	}
	if a.requests[0].Name != "Invoices" {
		t.Errorf("memory name = %q", a.requests[0].Name)
	}
	loaded, err := loadLibraryRequests(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Name != "Invoices" {
		t.Fatalf("reloaded requests = %+v", loaded)
	}
}

// CRLF line endings must not shift the byte ranges the scanner captures:
// strings.Lines keeps the two-character terminator, so advancing by the
// trimmed line length plus one would land every later match early.
func TestSearchReplaceKeepsCrlfOffsets(t *testing.T) {
	a, _ := searchReplaceFixture(t)
	const body = "a\r\nb token\r\ntoken"
	a.requests[0].Body = body
	if err := a.persistRequests(a.requests); err != nil {
		t.Fatal(err)
	}
	results := prepareSearch(t, a, "token", "scope")
	if len(results) != 2 {
		t.Fatalf("CRLF matches = %d (%+v), want 2", len(results), results)
	}
	if !a.applySearchReplace() {
		t.Fatal("replace was not applied")
	}
	want := "a\r\nb scope\r\nscope"
	if got := a.requests[0].Body; got != want {
		t.Errorf("in-memory body = %q, want %q", got, want)
	}
	if got := savedBody(t, a, "one"); got != want {
		t.Errorf("saved body = %q, want %q", got, want)
	}
}

// A dirty field's free text has no safe byte mapping to the saved copy, so
// the replacement stays in the undoable editor buffer and the file is left
// entirely alone.
func TestSearchReplaceKeepsDraftEditsUnsaved(t *testing.T) {
	a, first := searchReplaceFixture(t)
	a.openSaved(first.key())
	d := a.requestDocument
	buffer := editor.NewBuffer("// my note\ntoken tokenId\ntoken\n")
	d.bodyEditor = &editor.State{Buffer: buffer}
	d.composeBody, d.bodyFor = buffer.String(), buffer.String()

	results := prepareSearch(t, a, "token", "scope")
	if len(results) != 2 {
		t.Fatalf("draft matches = %d (%+v), want 2", len(results), results)
	}
	if !a.applySearchReplace() {
		t.Fatal("replace was not applied")
	}
	wantDraft := "// my note\nscope tokenId\nscope\n"
	if d.composeBody != wantDraft || buffer.String() != wantDraft {
		t.Errorf("draft = %q, buffer = %q, want %q", d.composeBody, buffer.String(), wantDraft)
	}
	if !d.dirty() {
		t.Error("the reader's note is no longer an unsaved edit")
	}
	if got := savedBody(t, a, "one"); got != "token tokenId\ntoken\n" {
		t.Errorf("dirty field wrote to disk: %q", got)
	}
	if !buffer.Undo() || buffer.String() != "// my note\ntoken tokenId\nscope\n" {
		t.Errorf("editor undo did not reverse one draft edit: %q", buffer.String())
	}
	if !buffer.Undo() || buffer.String() != "// my note\ntoken tokenId\ntoken\n" {
		t.Errorf("editor undo did not reverse both draft edits: %q", buffer.String())
	}
}

// An open field that matches its saved copy takes the replacement on both
// sides, and the tab becomes clean.
func TestSearchReplaceWritesCleanDraftField(t *testing.T) {
	a, first := searchReplaceFixture(t)
	a.openSaved(first.key())
	d := a.requestDocument
	buffer := editor.NewBuffer("token tokenId\ntoken\n")
	d.bodyEditor = &editor.State{Buffer: buffer}
	d.composeBody, d.bodyFor = buffer.String(), buffer.String()
	if d.dirty() {
		t.Fatal("the fixture draft is unexpectedly dirty")
	}
	prepareSearch(t, a, "token", "scope")
	if !a.applySearchReplace() {
		t.Fatal("replace was not applied")
	}
	want := "scope tokenId\nscope\n"
	if d.composeBody != want || buffer.String() != want {
		t.Errorf("draft = %q, buffer = %q, want %q", d.composeBody, buffer.String(), want)
	}
	if got := savedBody(t, a, "one"); got != want {
		t.Errorf("saved body = %q, want %q", got, want)
	}
	if d.dirty() {
		t.Error("a clean draft with a written field should no longer be dirty")
	}
}

// An outside write is refused through writeLibrary: memory, other scopes and
// the open draft must still agree with the disk the reader had before.
func TestSearchReplaceConflictLeavesEveryStateUnchanged(t *testing.T) {
	a, first := searchReplaceFixture(t)
	a.openSaved(first.key())
	d := a.requestDocument
	d.composeBody = "// unsaved\ntoken\n"
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
	requests, folders, envs := slices.Clone(a.requests), slices.Clone(a.folders), slices.Clone(a.envs)
	globals, globalsCurrent := slices.Collect(maps.Values(a.globals)), slices.Collect(maps.Values(a.globalsCurrent))
	draft := d.composeBody
	// Replace a saved, non-dirty field; the dirty body must not be carried
	// into the write, and the outside change must still refuse the file.
	prepareSearch(t, a, "Orders", "Invoices")

	if a.applySearchReplace() {
		t.Fatal("a conflicting write was accepted")
	}
	if !reflect.DeepEqual(a.requests, requests) || !reflect.DeepEqual(a.folders, folders) ||
		!reflect.DeepEqual(a.envs, envs) {
		t.Fatalf("apply changed live state after a refused write: %+v %+v %+v", a.requests, a.folders, a.envs)
	}
	if !slices.Equal(slices.Collect(maps.Values(a.globals)), globals) ||
		!slices.Equal(slices.Collect(maps.Values(a.globalsCurrent)), globalsCurrent) {
		t.Fatal("apply changed globals after a refused write")
	}
	if d.composeBody != draft {
		t.Errorf("draft changed on conflict: %q", d.composeBody)
	}
	onDisk, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(onDisk), "outside.example") {
		t.Fatalf("outside version was overwritten: %q, %v", onDisk, err)
	}
	notice := a.notices[len(a.notices)-1]
	if notice.action != locales.CompareWithDisk() || notice.alternate != locales.OverwriteWithDraft() {
		t.Fatalf("conflict did not offer the usual choices: %+v", notice)
	}
}

// A result is bound to the bytes the scanner saw. If the field changed before
// apply, the whole change is skipped rather than writing at a shifted offset.
func TestSearchReplaceSkipsStaleRange(t *testing.T) {
	a, _ := searchReplaceFixture(t)
	prepareSearch(t, a, "Orders", "Invoices")
	a.requests[0].Name = "Something else"
	if a.applySearchReplace() {
		t.Fatal("a stale change was applied")
	}
	if a.requests[0].Name != "Something else" {
		t.Errorf("stale write changed memory: %q", a.requests[0].Name)
	}
	if got := savedBody(t, a, "one"); got != "token tokenId\ntoken\n" {
		t.Errorf("stale write touched disk: %q", got)
	}
}

// Captured traffic is opened, never authored. Its results carry no edit
// target: no per-row Replace, no preview change, no write.
func TestSearchReplaceNeverOffersCapturedTraffic(t *testing.T) {
	a, _ := searchReplaceFixture(t)
	a.flows = []flow{{id: 9, method: "GET", url: "https://api.test/token", reqBody: "token"}}
	results := prepareSearch(t, a, "token", "scope")
	flowAt := slices.IndexFunc(results, func(r searchResult) bool { return r.source == searchFlows })
	if flowAt < 0 {
		t.Fatal("no captured result in the search")
	}
	if results[flowAt].replaceable() {
		t.Errorf("captured result offered a replace: %+v", results[flowAt])
	}
	if changes := a.searchChanges(flowAt); len(changes) != 0 {
		t.Errorf("captured result produced changes: %+v", changes)
	}
	if a.replaceOneSearchMatch(flowAt) {
		t.Error("per-row Replace applied a captured result")
	}
	editable := 0
	for _, r := range results {
		if r.replaceable() {
			editable++
		}
	}
	if editable == 0 || a.replaceableSearchCount() != editable {
		t.Errorf("replaceable count = %d, want %d", a.replaceableSearchCount(), editable)
	}
}

// A credential-marked value and a conventionally sensitive name keep their
// values out of Search even when no vault entry is involved. The names stay
// searchable, but a name carries no editable value target.
func TestSearchReplaceExcludesCredentialValues(t *testing.T) {
	const (
		credentialValue   = "credential-only-value"
		conventionalValue = "conventional-only-value"
	)
	a, _ := searchReplaceFixture(t)
	a.envs[0].Credentials = map[string]bool{"token": true}
	a.envs[0].Vars["token"] = credentialValue
	a.envs[0].Vars["password"] = conventionalValue
	for _, value := range []string{credentialValue, conventionalValue} {
		if results := prepareSearch(t, a, value, "set"); len(results) != 0 {
			t.Errorf("guessing %q produced results: %+v", value, results)
		}
	}
	if a.replaceableSearchCount() != 0 || a.applySearchReplace() {
		t.Error("a hidden value offered or applied a replacement")
	}
	names := prepareSearch(t, a, "password", "set")
	if len(names) == 0 || !slices.ContainsFunc(names, func(r searchResult) bool {
		return r.text == "password" && !r.replaceable()
	}) {
		t.Errorf("sensitive variable name is not a read-only result: %+v", names)
	}
	if got := a.envs[0].Vars["token"]; got != credentialValue {
		t.Errorf("credential value changed: %q", got)
	}
	if got := a.envs[0].Vars["password"]; got != conventionalValue {
		t.Errorf("sensitive value changed: %q", got)
	}
}

// A multi-line vault value pasted into a body is one secret even though no
// single line holds all of it. The field-level check in scanField hides the
// whole field; a per-line check alone would expose every line of the key.
func TestSearchReplaceExcludesMultiLineVaultValue(t *testing.T) {
	const key = "-----BEGIN KEY-----\nAAAA1111\nBBBB2222\n-----END KEY-----"
	a, _ := searchReplaceFixture(t)
	a.vault.secrets = vault.Secrets{{Name: "api", Value: key}}
	body := "harmless first line\n" + key + "\nharmless last line\n"
	a.requests[0].Body = body
	if err := a.persistRequests(a.requests); err != nil {
		t.Fatal(err)
	}
	if results := prepareSearch(t, a, "BBBB2222", "set"); len(results) != 0 {
		t.Fatalf("a line of a multi-line vault value produced results: %+v", results)
	}
	if a.replaceableSearchCount() != 0 || a.applySearchReplace() {
		t.Error("a multi-line vault value offered or applied a replacement")
	}
	loaded, err := loadLibraryRequests(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Body != body {
		t.Errorf("vault-bearing field changed on disk: %+v", loaded)
	}
}

// The command registry and the palette both carry the two search actions and
// the toggle; the toggle key must have only one enabled owner.
func TestSearchReplaceCommandsAreRegistered(t *testing.T) {
	a, _ := searchReplaceFixture(t)
	prepareSearch(t, a, "token", "scope")
	var c *ui.Context
	tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, 1100, 800)
	tt.Frame()
	for _, id := range []string{"search.action.replaceAll", "search.action.replace", "search.action.toggleReplace"} {
		x := pick(a.actions(c), id)
		if x.id != id || !x.enabled {
			t.Errorf("command %s = %+v", id, x)
		}
	}
	pick(a.actions(c), "search.action.replaceAll").run()
	if !a.searchAll.replace.confirmOpen || a.searchAll.replace.preview {
		t.Fatalf("Replace All did not ask first: %+v", a.searchAll.replace)
	}
	tt.Frame()
	if !tt.HasText(locales.ReplaceSearchConfirm("2", "1", "scope")) && !tt.HasText(locales.ReplaceAll()) {
		t.Errorf("confirmation is not drawn: %v", tt.Texts())
	}
}
