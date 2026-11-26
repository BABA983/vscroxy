package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// saveConflictFixture saves one request, changes its file outside the app as
// Git would, and tries to save a different draft over it. The failed save
// leaves both versions, and the notice offers Compare and Overwrite.
func saveConflictFixture(t *testing.T) (*app, string, []savedRequest, error) {
	t.Helper()
	a := libraryApp(t)
	a.backupDir = filepath.Join(t.TempDir(), "backups")
	a.library.backupDir = a.backupDir
	initial := savedRequest{Request: request.Request{ID: "one", Name: "One", Collection: "Shop", Method: "GET", URL: "https://old.example"}}
	if err := a.writeLibrary([]savedRequest{initial}, nil); err != nil {
		t.Fatal(err)
	}
	target, err := a.savedRequestPath(initial)
	if err != nil {
		t.Fatal(err)
	}
	outside := savedRequest{Request: request.Request{ID: "one", Name: "One", Method: "POST", URL: "https://outside.example"}}
	data, err := marshalFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}
	draft := slices.Clone(a.requests)
	draft[0].URL = "https://draft.example"
	err = a.persistRequests(draft)
	if err == nil {
		t.Fatal("the outside edit was silently overwritten")
	}
	a.fail(locales.Save(), err)
	notice := a.notices[len(a.notices)-1]
	if notice.action != locales.CompareWithDisk() || notice.alternate != locales.OverwriteWithDraft() {
		t.Fatalf("the conflict notice offers %q / %q, not Compare / Overwrite", notice.action, notice.alternate)
	}
	if !notice.persistent {
		t.Fatal("comparison spends the notice's other choices")
	}
	return a, target, draft, err
}

// expandNotice opens the one notice in the notification center; a row's
// detail and actions live inside its disclosure.
func expandNotice(t *testing.T, tt *ui.Tester) {
	t.Helper()
	row, ok := tt.AccessByRole(ui.RoleDisclosure, "")
	if !ok {
		t.Fatalf("no notice to expand: %q", tt.Texts())
	}
	box := row.Bounds
	tt.ClickAt(box.X+box.W/2, box.Y+box.H/2)
	// The center opens above the footer's bell, so the notice grows upward
	// while its disclosure animates: wait for it to settle, as a reader does.
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
}

// copyDiffSide reads a diff editor through its accessible name: editor text
// is drawn, not a text node, but the reader can select and copy it.
func copyDiffSide(t *testing.T, tt *ui.Tester, side string) string {
	t.Helper()
	if err := tt.Click(side); err != nil {
		t.Fatalf("focus %s: %v", side, err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	return tt.Clipboard()
}

func draftFileData(t *testing.T, a *app, draft []savedRequest) []byte {
	t.Helper()
	r := draft[0]
	r.Collection = "" // the file's directory says where the request lives
	data, err := marshalFile(savedRequest{Request: r.Request, Source: r.Source})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A plausible wrong implementation reloads from disk on Compare, or shows
// the disk on both sides. This checks that both texts are on screen and the
// draft the reader was editing is still the draft of the failed save.
func TestCompareShowsDiskAndDraftWithoutReloadingDraft(t *testing.T) {
	a, target, draft, _ := saveConflictFixture(t)
	onDisk, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	diskBefore := bytes.Clone(onDisk)
	draftBefore := draftFileData(t, a, draft)
	a.openNotifications()
	tt := ui.NewTester(a.view, 1440, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	expandNotice(t, tt)
	if err := tt.Click(locales.CompareWithDisk()); err != nil {
		t.Fatalf("compare from the notice: %v", err)
	}
	tt.Frame()
	if a.conflict == nil || !a.conflict.open {
		t.Fatal("Compare did not open the conflict view")
	}
	if a.conflict.diff == nil || !a.conflict.diff.Editable {
		t.Fatal("the draft side of the diff is not editable")
	}
	oldSide, newSide := a.conflict.diff.Old.String(), a.conflict.diff.New.String()
	if !strings.Contains(oldSide, "https://outside.example") || !strings.Contains(newSide, "https://draft.example") {
		t.Fatalf("Compare did not show both versions:\ndisk %q\ndraft %q", oldSide, newSide)
	}
	if oldSide == newSide {
		t.Fatal("Compare showed one version on both sides")
	}
	if shown := copyDiffSide(t, tt, "Old text"); !strings.Contains(shown, "https://outside.example") {
		t.Fatalf("the disk version is not shown: %q", shown)
	}
	if shown := copyDiffSide(t, tt, "New text"); !strings.Contains(shown, "https://draft.example") {
		t.Fatalf("the draft version is not shown: %q", shown)
	}
	onDisk, _ = os.ReadFile(target)
	if !bytes.Equal(onDisk, diskBefore) {
		t.Fatal("Compare changed the file on disk")
	}
	if got := a.conflict.editedDrafts()[a.conflict.files[0].Name]; !bytes.Equal(got, draftBefore) {
		t.Fatalf("Compare replaced the reader's draft: got %q, want %q", got, draftBefore)
	}
	if !bytes.Equal(draftFileData(t, a, draft), draftBefore) {
		t.Fatal("Compare changed the request list being saved")
	}
	if a.notices[len(a.notices)-1].spent {
		t.Fatal("compare spent the notice before an overwrite could be chosen")
	}
}

// A plausible wrong implementation writes on the first click or writes the
// disk copy. The first click must only open a confirmation and the second
// must put the draft on disk, with the outside version kept in history.
func TestOverwriteWritesDraftOnlyAfterExplicitSecondConfirmation(t *testing.T) {
	a, target, draft, _ := saveConflictFixture(t)
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	a.openNotifications()
	tt := ui.NewTester(a.view, 1440, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	expandNotice(t, tt)
	if !tt.HasText(locales.OverwriteWithDraft()) {
		t.Fatalf("the notice has no Overwrite action: %q", tt.Texts())
	}
	// The center's Overwrite button and its handler are the same path: the
	// button calls this handler with the notice's id.
	a.notificationAlternate(a.notices[len(a.notices)-1].id)
	tt.Frame()
	if a.conflict == nil || !a.conflict.confirm {
		t.Fatal("Overwrite skipped its confirmation")
	}
	if after, _ := os.ReadFile(target); !bytes.Equal(after, before) {
		t.Fatal("Overwrite wrote before the reader confirmed it")
	}
	if !tt.HasText(locales.OverwriteConfirmDetail()) {
		t.Fatalf("the confirmation is missing: %v", tt.Texts())
	}
	if err := tt.Click(locales.OverwriteFile()); err != nil {
		t.Fatalf("confirm the overwrite: %v", err)
	}
	tt.Frame()
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	want := draftFileData(t, a, draft)
	if !bytes.Equal(after, want) {
		t.Fatalf("the file holds %q, not the draft %q", after, want)
	}
	if len(a.requests) != 1 || a.requests[0].URL != "https://draft.example" {
		t.Fatalf("the app still holds %+v", a.requests)
	}
	entries, err := store.ListBackups(a.backupDir, target)
	if err != nil || len(entries) != 1 {
		t.Fatalf("backups of the outside version = %v, %v", entries, err)
	}
	backedUp, _ := os.ReadFile(entries[0].Path)
	if !bytes.Equal(backedUp, before) {
		t.Fatalf("the outside version was not backed up: %q", backedUp)
	}
	if a.conflict != nil {
		t.Fatal("a resolved conflict still offers its actions")
	}
}

// A plausible wrong implementation reloads the collections on Cancel, or
// writes one of the two versions while closing. Neither file changes.
func TestCancelConflictChangesNoFileAndNoDraft(t *testing.T) {
	a, target, draft, _ := saveConflictFixture(t)
	before, _ := os.ReadFile(target)
	draftBefore := draftFileData(t, a, draft)
	a.openNotifications()
	tt := ui.NewTester(a.view, 1440, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	expandNotice(t, tt)
	if err := tt.Click(locales.CompareWithDisk()); err != nil {
		t.Fatalf("compare from the notice: %v", err)
	}
	tt.Frame()
	if err := tt.Click(locales.Cancel()); err != nil {
		t.Fatalf("cancel the conflict: %v", err)
	}
	tt.Frame()
	after, _ := os.ReadFile(target)
	if !bytes.Equal(after, before) {
		t.Fatalf("Cancel changed the file from %q to %q", before, after)
	}
	if a.conflict.open {
		t.Fatal("Cancel left the conflict view open")
	}
	if !bytes.Equal(a.conflict.editedDrafts()[a.conflict.files[0].Name], draftBefore) {
		t.Fatal("Cancel replaced the draft")
	}
	entries, _ := store.ListBackups(a.backupDir, target)
	if len(entries) != 0 {
		t.Fatalf("Cancel made %d backups", len(entries))
	}
}

// A late Overwrite must apply only the refused file's draft to the library
// as it stands now. A stale whole-library snapshot would revert a later save
// to B and delete the request C the reader created while the notice waited.
func TestLateOverwriteKeepsLaterSavesAndNewRequestsWhileWritingDraft(t *testing.T) {
	a := libraryApp(t)
	a.backupDir = filepath.Join(t.TempDir(), "backups")
	a.library.backupDir = a.backupDir
	first := savedRequest{Request: request.Request{ID: "a", Name: "A", Collection: "Shop", Method: "GET", URL: "https://a-saved.example"}}
	second := savedRequest{Request: request.Request{ID: "b", Name: "B", Collection: "Shop", Method: "GET", URL: "https://b-saved.example"}}
	if err := a.writeLibrary([]savedRequest{first, second}, nil); err != nil {
		t.Fatal(err)
	}
	target, err := a.savedRequestPath(first)
	if err != nil {
		t.Fatal(err)
	}
	outside := savedRequest{Request: first.Request}
	outside.Collection = ""
	outside.URL = "https://a-outside.example"
	data, err := marshalFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}

	draft := slices.Clone(a.requests)
	draft[0].URL = "https://a-draft.example"
	saveErr := a.persistRequests(draft)
	var conflict *libraryConflictError
	if !errors.As(saveErr, &conflict) {
		t.Fatalf("the first save did not conflict: %v", saveErr)
	}
	a.fail(locales.Save(), saveErr) // the notice stays while other saves happen

	later := slices.Clone(a.requests)
	for i := range later {
		if later[i].ID == "b" {
			later[i].URL = "https://b-later.example"
		}
	}
	if err := a.persistRequests(later); err != nil {
		t.Fatalf("save B while A's notice waits: %v", err)
	}
	withC := append(slices.Clone(a.requests), savedRequest{Request: request.Request{ID: "c", Name: "C", Collection: "Shop", Method: "GET", URL: "https://c.example"}})
	if err := a.persistRequests(withC); err != nil {
		t.Fatalf("create C while A's notice waits: %v", err)
	}
	if a.notices[len(a.notices)-1].spent {
		t.Fatal("later saves spent the still-unresolved conflict's choices")
	}

	a.offerSaveOverwrite(conflict)
	a.overwriteSaveConflict()
	loaded, err := loadLibraryRequests(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range loaded {
		got[r.ID] = r.URL
	}
	if len(got) != 3 || got["a"] != "https://a-draft.example" || got["b"] != "https://b-later.example" || got["c"] != "https://c.example" {
		t.Fatalf("late overwrite left A=%q B=%q C=%q in %d requests", got["a"], got["b"], got["c"], len(got))
	}
}

// A two-file conflict must rebuild the diff for the file selected in the
// chooser. Reusing the first DiffState would let the reader edit and
// overwrite a file whose text was never shown.
func TestSwitchingConflictFilesShowsTheSelectedVersionsInsteadOfKeepingTheFirst(t *testing.T) {
	a := libraryApp(t)
	a.backupDir = filepath.Join(t.TempDir(), "backups")
	a.library.backupDir = a.backupDir
	if err := a.writeLibrary([]savedRequest{
		{Request: request.Request{ID: "a", Name: "A", Collection: "Shop", Method: "GET", URL: "https://a-saved.example"}},
		{Request: request.Request{ID: "b", Name: "B", Collection: "Shop", Method: "GET", URL: "https://b-saved.example"}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	for i, r := range a.requests {
		target, err := a.savedRequestPath(r)
		if err != nil {
			t.Fatal(err)
		}
		outside := savedRequest{Request: r.Request}
		outside.Collection = ""
		outside.URL = []string{"https://a-outside.example", "https://b-outside.example"}[i]
		data, err := marshalFile(outside)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	draft := slices.Clone(a.requests)
	draft[0].URL = "https://a-draft.example"
	draft[1].URL = "https://b-draft.example"
	saveErr := a.persistRequests(draft)
	var conflict *libraryConflictError
	if !errors.As(saveErr, &conflict) || len(conflict.Files) != 2 {
		t.Fatalf("expected a two-file conflict, got %v", saveErr)
	}
	a.reviewSaveConflict(conflict)
	tt := ui.NewTester(a.view, 1300, 820)
	tt.SetLocale("en-US")
	tt.Frame()
	if shown := copyDiffSide(t, tt, "Old text"); !strings.Contains(shown, "https://a-outside.example") {
		t.Fatalf("the first file's disk text is not shown: %q", shown)
	}
	first := a.conflict.files[0].Name
	second := a.conflict.files[1].Name
	if err := tt.Click(first); err != nil {
		t.Fatalf("open the conflict file chooser: %v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if err := tt.Click(second); err != nil {
		t.Fatalf("choose the second conflict file: %v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if got := copyDiffSide(t, tt, "Old text"); !strings.Contains(got, "https://b-outside.example") || strings.Contains(got, "a-outside") {
		t.Fatalf("the second file's disk text is not shown: %q", got)
	}
	if got := copyDiffSide(t, tt, "New text"); !strings.Contains(got, "https://b-draft.example") || strings.Contains(got, "a-draft") {
		t.Fatalf("the second file's draft is not shown: %q", got)
	}
	if a.conflict.diff == nil || !strings.Contains(a.conflict.diff.Old.String(), "b-outside") {
		t.Fatal("the DiffState still holds the first file")
	}
}
