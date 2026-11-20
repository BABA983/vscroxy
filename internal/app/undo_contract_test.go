package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// undoDiskApp has a real library on disk and a real variables file, so every
// contract test checks what a restart would read, not just memory.
func undoDiskApp(t *testing.T) *app {
	t.Helper()
	a := libraryApp(t)
	a.backupDir = filepath.Join(t.TempDir(), "backups")
	a.library.backupDir = a.backupDir
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	return a
}

func undoFixtureRequests(a *app) {
	a.requests = []savedRequest{
		{Request: request.Request{ID: "one", Name: "List", Collection: "Shop", Method: "GET", URL: "https://api.test/list"}},
		{Request: request.Request{ID: "two", Name: "Order", Collection: "Other", Method: "GET", URL: "https://api.test/order"}},
	}
	a.activeEnv, a.envs = "Dev", []environment{{Name: "Dev", Vars: map[string]string{"token": "value"}}}
	if err := a.persistRequests(a.requests); err != nil {
		panic(err)
	}
}

func undoDiskFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func saveUndoNoticeTexts(a *app) []string {
	var texts []string
	for _, n := range a.notices {
		if n.detail != "" {
			texts = append(texts, n.text+": "+n.detail)
		} else {
			texts = append(texts, n.text)
		}
	}
	return texts
}

func TestUndoRedoDeleteRestoresBothMemoryAndDisk(t *testing.T) {
	a := undoDiskApp(t)
	undoFixtureRequests(a)
	path := filepath.Join(a.library.root, "Shop", "List.request.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture did not write %s: %v", path, err)
	}
	if _, err := a.deleteAssets(deletion{key: a.requests[0].key()}); err != nil {
		t.Fatal(err)
	}
	if len(a.requests) != 1 {
		t.Fatalf("after delete: %d requests, want 1", len(a.requests))
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("after delete file is still there: %v", err)
	}
	if !a.undoHistory().CanUndo() || a.undoHistory().CanRedo() {
		t.Fatal("delete did not push one undo element")
	}

	a.undo()
	if len(a.requests) != 2 || !slices.ContainsFunc(a.requests, func(r savedRequest) bool { return r.Name == "List" }) {
		t.Fatalf("after undo: %+v", a.requests)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("undo did not restore the file: %v", err)
	}
	if !strings.Contains(string(undoDiskFile(t, path)), `"List"`) {
		t.Fatalf("restored file lost its request: %s", undoDiskFile(t, path))
	}
	if !a.undoHistory().CanRedo() {
		t.Fatal("undo did not leave a redo element")
	}

	a.redo()
	if len(a.requests) != 1 {
		t.Fatalf("after redo: %d requests, want 1", len(a.requests))
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("redo left the deleted file: %v", err)
	}
}

func TestUndoRedoMoveRestoresTheOriginalCollectionAndFile(t *testing.T) {
	a := undoDiskApp(t)
	undoFixtureRequests(a)
	from := filepath.Join(a.library.root, "Shop", "List.request.json")
	to := filepath.Join(a.library.root, "Other", "List.request.json")
	key := a.requests[0].key()
	if !a.moveLibrary(libraryMove{
		item: libraryDrag{kind: requestItem, id: key},
		to:   libraryDrop{parent: "Other"},
	}) {
		t.Fatal("move was refused")
	}
	if got := a.requests[0].Collection; got != "Other" {
		t.Fatalf("after move collection = %q, want Other", got)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) || !fileExists(to) {
		t.Fatalf("move left files at %q and %q", from, to)
	}

	a.undo()
	restored, ok := a.savedRequest("saved:one")
	if !ok || restored.Collection != "Shop" {
		t.Fatalf("after undo the moved request = %+v, want Shop", restored)
	}
	if !fileExists(from) || fileExists(to) {
		t.Fatalf("undo did not put the file back: from=%v to=%v", fileExists(from), fileExists(to))
	}

	a.redo()
	redone, ok := a.savedRequest("saved:one")
	if !ok || redone.Collection != "Other" {
		t.Fatalf("after redo the moved request = %+v, want Other", redone)
	}
	if fileExists(from) || !fileExists(to) {
		t.Fatalf("redo did not move the file again: from=%v to=%v", fileExists(from), fileExists(to))
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func undoRenameFixture(t *testing.T) (*app, variableTarget) {
	t.Helper()
	a := undoDiskApp(t)
	a.requests = []savedRequest{
		{Request: request.Request{ID: "one", Name: "List", Collection: "Shop", Method: "GET", URL: "https://api.test/{{tenant}}"}},
		{Request: request.Request{ID: "two", Name: "Order", Collection: "Other", Method: "GET", URL: "https://api.test/v2/{{tenant}}"}},
	}
	a.activeEnv, a.envs = "Dev", []environment{{Name: "Dev", Vars: map[string]string{"tenant": "shared"}}}
	if err := a.persistRequests(a.requests); err != nil {
		t.Fatal(err)
	}
	target := variableTarget{Name: "tenant", Scope: variableScope{Kind: "Environment", Path: "Dev"}, Defined: true}
	a.openVariableRename(target)
	a.variableRename.input = "owner"
	a.prepareVariableRename()
	if a.variableRename.problem != "" || len(a.variableRename.changes) < 3 {
		t.Fatalf("rename preview problem=%q changes=%+v", a.variableRename.problem, a.variableRename.changes)
	}
	if !a.applyVariableRename() {
		t.Fatal("rename was not applied")
	}
	return a, target
}

func TestUndoRedoBulkRenameAcrossTwoFiles(t *testing.T) {
	a, _ := undoRenameFixture(t)
	first := filepath.Join(a.library.root, "Shop", "List.request.json")
	second := filepath.Join(a.library.root, "Other", "Order.request.json")
	for _, path := range []string{first, second} {
		if !strings.Contains(string(undoDiskFile(t, path)), "{{owner}}") {
			t.Fatalf("%s does not hold the renamed variable", path)
		}
	}
	// One workspace element owns every resource the bulk edit touched:
	// undoing it writes once and posts one notice, not once per file.
	past := a.undoHistory().Past()
	if len(past) != 1 || len(past[0].Resources()) < 2 {
		t.Fatalf("bulk rename pushed %+v, want one workspace element over several resources", past)
	}

	a.undo()
	for _, path := range []string{first, second} {
		if strings.Contains(string(undoDiskFile(t, path)), "{{owner}}") ||
			!strings.Contains(string(undoDiskFile(t, path)), "{{tenant}}") {
			t.Fatalf("undo left the renamed variable in %s", path)
		}
	}
	if _, ok := a.envs[0].Vars["tenant"]; !ok {
		t.Fatalf("undo did not restore the definition: %v", a.envs[0].Vars)
	}
	a.redo()
	for _, path := range []string{first, second} {
		if !strings.Contains(string(undoDiskFile(t, path)), "{{owner}}") {
			t.Fatalf("redo did not reapply the rename in %s", path)
		}
	}
}

func TestUndoRefusesGroupWhenAFileChangedOnDisk(t *testing.T) {
	a, _ := undoRenameFixture(t)
	first := filepath.Join(a.library.root, "Shop", "List.request.json")
	second := filepath.Join(a.library.root, "Other", "Order.request.json")
	secondBefore := undoDiskFile(t, second)
	// Another editor appended a byte to one of the two files.
	outside := append(undoDiskFile(t, first), ' ')
	if err := os.WriteFile(first, outside, 0o600); err != nil {
		t.Fatal(err)
	}
	a.undo()
	if !slices.ContainsFunc(saveUndoNoticeTexts(a), func(text string) bool {
		return strings.HasPrefix(text, locales.Undo()+": Cannot undo")
	}) {
		t.Fatalf("refusal was not explained: %q", saveUndoNoticeTexts(a))
	}
	if got := undoDiskFile(t, first); !slices.Equal(got, outside) {
		t.Fatalf("refused undo changed the file edited outside: %q", got)
	}
	if got := undoDiskFile(t, second); !slices.Equal(got, secondBefore) {
		t.Fatalf("refused undo changed another file in the group: %q", got)
	}
	for _, r := range a.requests {
		if !strings.Contains(r.URL, "{{owner}}") {
			t.Fatalf("refused undo changed memory: %+v", a.requests)
		}
	}
	if !a.undoHistory().CanUndo() {
		t.Fatal("refusing an unchanged group consumed it")
	}
}

func TestTextFieldKeepsUndoShortcutWhileFocused(t *testing.T) {
	a := deleteTestApp()
	a.deleteFlows([]flow{a.flows[0]})
	var c *ui.Context
	var focusInFrame bool
	tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; focusInFrame = ctx.FocusTakesText(); a.view(ctx) }, 1280, 800)
	tt.Frame()
	// The palette's field is a text field with its own undo history. Its
	// focus is set by the frame itself, unlike a label a click may miss.
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if !focusInFrame || !textHistoryKey(pick(a.actions(c), "edit.undo"), focusInFrame) {
		t.Fatal("the focused field did not take ownership of the undo key")
	}
	tt.Type("typed")
	tt.Frame()
	if a.workbench.query != "typed" {
		t.Fatalf("typed text did not reach the field: %q", a.workbench.query)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if a.workbench.query != "" {
		t.Fatalf("Cmd+Z in a field did not undo the typed text: %q", a.workbench.query)
	}
	if len(a.flows) != 2 {
		t.Fatalf("Cmd+Z in a field undid the capture delete: %d flows", len(a.flows))
	}
	if !pick(a.actions(c), "edit.undo").enabled {
		t.Fatal("Undo is missing from the palette while a field has focus")
	}
	if err := tt.Click("Dismiss notification"); err == nil {
		tt.Frame()
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if focusInFrame {
		t.Fatal("the field still owns the undo key after the palette closed")
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if len(a.flows) != 3 || a.pickedID != 1 {
		t.Fatalf("Cmd+Z outside a field did not undo: %d flows, picked %d", len(a.flows), a.pickedID)
	}
}

func TestGroupedUndoWritesAndNotifiesOnce(t *testing.T) {
	a := undoDiskApp(t)
	undoFixtureRequests(a)
	if !a.moveLibrary(libraryMove{
		item: libraryDrag{kind: requestItem, id: "saved:one"},
		to:   libraryDrop{parent: "Other"},
	}) {
		t.Fatal("move was refused")
	}
	past := a.undoHistory().Past()
	if len(past) != 1 || len(past[0].Resources()) < 2 {
		t.Fatalf("move pushed %+v, want one workspace element over several files", past)
	}
	notices, writes := len(a.notices), a.undoApplyCount
	a.undo()
	if got := a.undoApplyCount - writes; got != 1 {
		t.Fatalf("grouped undo applied the state %d times, want once", got)
	}
	if got := len(a.notices) - notices; got != 1 {
		t.Fatalf("grouped undo posted %d notices, want one: %q", got, saveUndoNoticeTexts(a))
	}
	item, ok := a.savedRequest("saved:one")
	if !ok || item.Collection != "Shop" {
		t.Fatalf("grouped undo left the request at %+v", item)
	}
}
