package app

import (
	"slices"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// A collection request's right-click menu and its ⋯ menu are one menu: the
// same actions, in the same order, wherever the pointer finds them.
func TestCollectionRequestMenusMatch(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1440, 800)
	showCollections(t, tt)
	tt.Frame()
	row := "List projects"
	more := locales.RequestActionsPrefix(row)
	if err := tt.RightClick(row); err != nil {
		t.Fatalf("right-click: %v", err)
	}
	want := tt.Menu()
	if len(want) == 0 {
		t.Fatal("the request's right-click menu is empty")
	}
	for _, label := range []string{locales.RenameRequest(), locales.DuplicateAsNewRequest()} {
		if !slices.Contains(want, label) {
			t.Fatalf("the request's right-click menu is missing %q: %q", label, want)
		}
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if err := tt.Click(more); err != nil {
		t.Fatalf("open ⋯: %v", err)
	}
	got := tt.Menu()
	if !slices.Equal(got, want) {
		t.Fatalf("⋯ menu %q, right-click menu %q", got, want)
	}
}

// The collection tree's folder menu offers the Postman-like creation and
// rename actions, not only the settings and export actions.
func TestCollectionFolderMenuCreatesRequestsAndFolders(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("en-US")
	showCollections(t, tt)
	tt.Frame()
	if err := tt.RightClick("API"); err != nil {
		t.Fatalf("right-click the API folder: %v", err)
	}
	menu := tt.Menu()
	for _, label := range []string{locales.NewRequest(), locales.NewFolder(), locales.RenameNamed("API")} {
		if !slices.Contains(menu, label) {
			t.Fatalf("the folder menu is missing %q: %q", label, menu)
		}
	}
	if err := tt.ChooseMenuItem(locales.NewRequest()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if d := a.requestDocument; d == nil || d.composeCollection != "Workspace/API" {
		t.Fatalf("New request opened %+v, want the API folder", d)
	}

	// A new folder opens its editor on a free path; nothing is written until
	// the editor is saved.
	a = workspaceApp()
	tt = ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("en-US")
	showCollections(t, tt)
	tt.Frame()
	if err := tt.RightClick("API"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(locales.NewFolder()); err != nil {
		t.Fatal(err)
	}
	if !a.folderUI.open || a.folderUI.draft.Path != "Workspace/API/New folder" {
		t.Fatalf("New folder opened %+v, want Workspace/API/New folder", a.folderUI)
	}
}

// An API contract's row has its own actions: opening it, validating and
// saving its source, and exporting or creating a collection from it.
func TestAPIContractRowMenu(t *testing.T) {
	a := workspaceApp()
	a.addAPI(apiSpec{ID: "menu-api", Source: `{"openapi":"3.0.3","info":{"title":"Menu API","version":"1"},"paths":{}}`}, true)
	tt := ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("en-US")
	showCollections(t, tt)
	tt.Frame()
	if err := tt.RightClick(locales.ApiTitle("Menu API")); err != nil {
		t.Fatalf("right-click the API contract: %v", err)
	}
	menu := tt.Menu()
	for _, label := range []string{locales.Open(), locales.Validate(), locales.SaveApi(), locales.ExportOpenapi(), locales.CreateCollectionFromOpenapi()} {
		if !slices.Contains(menu, label) {
			t.Fatalf("the API contract menu is missing %q: %q", label, menu)
		}
	}
}

// A collection or folder's name edits in place: Rename in its menu shows an
// input on the row, and Enter writes the name without opening the settings
// dialog.
func TestCollectionFolderRenamesInline(t *testing.T) {
	a := libraryApp(t)
	if err := a.writeLibrary(nil, []request.Folder{{Path: "Shop"}}); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("en-US")
	showCollections(t, tt)
	tt.Frame()
	if err := tt.RightClick("Shop"); err != nil {
		t.Fatalf("right-click the folder name: %v", err)
	}
	if err := tt.ChooseMenuItem(locales.RenameNamed("Shop")); err != nil {
		t.Fatalf("choose Rename: %v", err)
	}
	tt.Frame()
	if _, ok := tt.Find("Rename Shop"); !ok {
		t.Fatalf("the folder name did not become an input: %q", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Renamed")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if len(a.folders) != 1 || a.folders[0].Path != "Renamed" {
		t.Fatalf("folders after inline rename = %+v", a.folders)
	}
	if _, ok := tt.Find("Rename Shop"); ok {
		t.Fatal("the inline editor stayed open after Enter")
	}
}

// The table row's menu is the exchange menu the inspector offers: the
// inspector adds its own pane choices, but every row action is there in the
// same order.
func TestTableRowMenuIsInTheInspectorMenu(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1440, 800)
	tt.Frame()
	if err := tt.Click("api.example.com/projects"); err != nil {
		t.Fatalf("select the row: %v", err)
	}
	tt.Frame()
	if err := tt.RightClick("api.example.com/projects"); err != nil {
		t.Fatalf("right-click: %v", err)
	}
	row := tt.Menu()
	if len(row) == 0 {
		t.Fatal("the row's menu is empty")
	}
	items := slices.DeleteFunc(slices.Clone(row), func(label string) bool { return label == "-" })
	index := func(label string) int { return slices.Index(items, label) }
	if len(items) == 0 || items[0] != "Replay" {
		t.Fatalf("the row's menu starts with %q, want Replay", items)
	}
	for _, pair := range [][2]string{
		{"Pin to the top", "Mark…"},
		{"Mark…", "Filter captures"},
		{"Save as request", "Save as regression…"},
		{"Save as regression…", "Save evidence to Sessions"},
		{"Save evidence to Sessions", "Create collection…"},
		{"Create collection…", "Copy"},
		{"Copy", "Tools"},
	} {
		if index(pair[0]) < 0 || index(pair[1]) < 0 || index(pair[0]) >= index(pair[1]) {
			t.Fatalf("%q does not come before %q: %q", pair[0], pair[1], items)
		}
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if err := tt.Click(locales.InspectorOptions()); err != nil {
		t.Fatalf("open the inspector menu: %v", err)
	}
	inspector := tt.Menu()
	next := 0
	for _, label := range inspector {
		if next < len(row) && label == row[next] {
			next++
		}
	}
	if next != len(row) {
		t.Fatalf("the inspector menu is missing row actions from %q: %q", row[next:], inspector)
	}
}

// A header's right-click menu changes the table: choosing a column hides it.
func TestTableHeaderMenuChoosesAColumn(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1440, 800)
	tt.Frame()
	if !a.columnShown("status") {
		t.Fatal("the status column starts hidden")
	}
	if err := tt.RightClick("Status"); err != nil {
		t.Fatalf("right-click the header: %v", err)
	}
	if err := tt.ChooseMenuItem("Status"); err != nil {
		t.Fatalf("choose the column: %v", err)
	}
	tt.Frame()
	if a.columnShown("status") {
		t.Fatal("choosing Status in the header menu left the column shown")
	}
}
