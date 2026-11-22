package app

import (
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

// historyFixture makes two history entries for one saved request. The backup
// names are re-stamped so their timestamps, and the labels in the list, are
// unambiguous in a fast test.
func historyFixture(t *testing.T) (*app, string, string) {
	t.Helper()
	a := libraryApp(t)
	a.backupDir = filepath.Join(t.TempDir(), "backups")
	a.library.backupDir = a.backupDir
	r := savedRequest{Request: request.Request{ID: "one", Name: "One", Collection: "Shop", Method: "GET", URL: "https://version0.example"}}
	if err := a.writeLibrary([]savedRequest{r}, nil); err != nil {
		t.Fatal(err)
	}
	target, err := a.savedRequestPath(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{1, 2} {
		time.Sleep(2 * time.Millisecond)
		changed := slices.Clone(a.requests)
		changed[0].URL = "https://version" + string(rune('0'+version)) + ".example"
		if err := a.persistRequests(changed); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := store.ListBackups(a.backupDir, target)
	if err != nil || len(entries) != 2 {
		t.Fatalf("history entries = %v, %v", entries, err)
	}
	// Re-stamping takes the two copies a known time apart without changing
	// the backup format or how the app finds them.
	// entries starts newest first: version 1 is the later copy.
	for i, stamp := range []string{"20260102T090000.000000000", "20260101T090000.000000000"} {
		dir, name := filepath.Split(entries[i].Path)
		at := strings.Index(name, requestFileSuffix)
		if at < 0 {
			t.Fatalf("unexpected backup name %q", name)
		}
		newName := name[:at+len(requestFileSuffix)] + "." + stamp
		if err := os.Rename(entries[i].Path, filepath.Join(dir, newName)); err != nil {
			t.Fatal(err)
		}
	}
	return a, target, r.key()
}

func backupData(t *testing.T, entry store.BackupEntry) string {
	t.Helper()
	data, err := os.ReadFile(entry.Path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A plausible wrong implementation takes the backup directory's order,
// which runs oldest first. The timeline must list and select the newest
// saved version first.
func TestLocalHistoryListsNewestBackupFirstRatherThanReadDirOrder(t *testing.T) {
	a, target, key := historyFixture(t)
	a.openRequestHistory(key)
	if len(a.history.entries) != 2 {
		t.Fatalf("history = %d entries, want 2", len(a.history.entries))
	}
	newest, older := backupData(t, a.history.entries[0]), backupData(t, a.history.entries[1])
	if !strings.Contains(newest, "https://version1.example") || !strings.Contains(older, "https://version0.example") {
		t.Fatalf("first entry is the older version: %q then %q", newest, older)
	}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	if !tt.HasText(locales.SavedVersions()) || !tt.HasText(locales.LocalHistory()) {
		// The title names the request, not the feature; the list heading does.
		if !tt.HasText(locales.SavedVersions()) {
			t.Fatalf("history list is missing: %v", tt.Texts())
		}
	}
	newLabel := historyEntryLabel(a.history.entries[0])
	oldLabel := historyEntryLabel(a.history.entries[1])
	texts := tt.Texts()
	text := strings.Join(texts, "\n")
	if !strings.Contains(text, newLabel) || !strings.Contains(text, oldLabel) ||
		strings.Index(text, newLabel) >= strings.Index(text, oldLabel) {
		t.Fatalf("the list does not show newest first (%q before %q): %q", newLabel, oldLabel, text)
	}
	if a.history.diff == nil || !strings.Contains(a.history.diff.Old.String(), "https://version1.example") ||
		!strings.Contains(a.history.diff.New.String(), "https://version2.example") {
		t.Fatalf("the selected version is not compared with the current file: %+v", a.history.diff)
	}
	if a.history.target != target {
		t.Fatalf("history target = %q, want %q", a.history.target, target)
	}
}

// A plausible wrong implementation writes the backup bytes directly without
// backing up the current file, leaving the restore unundoable. This checks
// the content, the app's reloaded state, and the new newest history entry.
func TestLocalHistoryRestoreWritesOldVersionAndBacksUpPresentFile(t *testing.T) {
	a, target, key := historyFixture(t)
	a.openRequestHistory(key)
	a.history.picked = 1
	current, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	chosen := backupData(t, a.history.entries[1])
	tt := ui.NewTester(a.view, 1200, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	if err := tt.Click(locales.RestoreVersionEllipsis()); err != nil {
		t.Fatalf("ask to restore: %v", err)
	}
	tt.Frame()
	if !a.history.confirm || !tt.HasText(locales.RestoreVersionDetail()) {
		t.Fatalf("restore did not ask first: %v", tt.Texts())
	}
	if got, _ := os.ReadFile(target); !strings.Contains(string(got), "https://version2.example") {
		t.Fatalf("the file changed before confirmation: %q", got)
	}
	if err := tt.Click(locales.RestoreVersion()); err != nil {
		t.Fatalf("confirm the restore: %v", err)
	}
	tt.Frame()
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != chosen || !strings.Contains(string(got), "https://version0.example") {
		t.Fatalf("the file holds %q, not the chosen version %q", got, chosen)
	}
	if len(a.requests) != 1 || a.requests[0].URL != "https://version0.example" {
		t.Fatalf("the app did not reload the restored request: %+v", a.requests)
	}
	a.openRequestHistory(key)
	entries, err := store.ListBackups(a.backupDir, target)
	if err != nil || len(entries) != 3 {
		t.Fatalf("backups after restore = %v, %v", entries, err)
	}
	if backedUp := backupData(t, entries[0]); backedUp != string(current) {
		t.Fatalf("the restored-over version was not backed up: %q", backedUp)
	}
	if a.history.entries[0].Path != entries[0].Path {
		t.Fatal("the restored-over version is not the newest history entry")
	}
}

// A plausible wrong implementation puts History on the request's menu but
// not the folder's, or makes the two menus differ between right-click and ⋯.
func TestRequestAndFolderMenusOfferLocalHistory(t *testing.T) {
	a := workspaceApp()
	a.folders = []request.Folder{{Path: "Shop"}}
	a.requests[0].Collection = "Shop"
	tt := ui.NewTester(a.view, 1200, 800)
	showCollections(t, tt)
	tt.Frame()
	if err := tt.RightClick("List projects"); err != nil {
		t.Fatalf("right-click the request: %v", err)
	}
	requestMenu := tt.Menu()
	if !slices.Contains(requestMenu, locales.LocalHistoryEllipsis()) {
		t.Fatalf("request menu has no History: %q", requestMenu)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if err := tt.RightClick("Shop"); err != nil {
		t.Fatalf("right-click the folder: %v", err)
	}
	folderMenu := tt.Menu()
	if !slices.Contains(folderMenu, locales.LocalHistoryEllipsis()) {
		t.Fatalf("folder menu has no History: %q", folderMenu)
	}
}
