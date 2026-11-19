package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func TestMoveRequestIntoFolderUpdatesDiskAndTabs(t *testing.T) {
	a := libraryApp(t)
	requests := []savedRequest{
		{Request: request.Request{ID: "one", Name: "One", Method: "GET"}},
		{Request: request.Request{ID: "two", Name: "Two", Method: "GET"}},
	}
	if err := a.writeLibrary(requests, []request.Folder{{Path: "Shop", Description: "settings"}}); err != nil {
		t.Fatal(err)
	}
	a.addDocument(a.requests[0], a.requests[0].key())
	opened := a.requestDocument
	closed := a.newDocument(a.requests[0], a.requests[0].key())
	a.documents = append(a.documents, closed)
	a.rememberClosedDocument(closed)
	a.documents = a.documents[:1]
	key := opened.savedKey
	closedKey := closed.savedKey

	if !a.moveLibrary(libraryMove{item: libraryDrag{kind: requestItem, id: key}, to: libraryDrop{parent: "Shop"}}) {
		t.Fatal("move was refused")
	}
	moved, ok := a.savedRequest(key)
	if !ok || moved.Collection != "Shop" {
		t.Fatalf("moved request = %+v, found %v", moved, ok)
	}
	onDisk, _, _, err := loadLibrary(a.library.root)
	if err != nil || len(onDisk) != 2 || onDisk[1].ID != "one" || onDisk[1].Collection != "Shop" {
		t.Fatalf("disk after move = %+v, %v", onDisk, err)
	}
	if opened.composeCollection != "Shop" || opened.baseline.Collection != "Shop" || opened.dirty() {
		t.Errorf("open tab did not follow: compose %q, baseline %q, dirty %v", opened.composeCollection, opened.baseline.Collection, opened.dirty())
	}
	entry, ok := a.closedDocuments().Get(closed.id)
	if !ok || entry.savedKey != closedKey || entry.baseline.Collection != "Shop" || entry.draft.Collection != "Shop" ||
		entry.doc.composeCollection != "Shop" || !a.savedSourceStillThere(entry) {
		t.Fatalf("closed tab did not follow: %+v, found %v", entry, ok)
	}
	if !a.reopenClosedDocument() {
		t.Fatal("the moved closed tab could not be reopened")
	}
	if a.requestDocument.savedKey != closedKey {
		t.Errorf("reopened tab lost its saved identity: %q", a.requestDocument.savedKey)
	}
	// The success notice carries the same undo as the existing delete actions.
	undo := a.notices[len(a.notices)-1].run
	if undo == nil {
		t.Fatal("the move notice has no Undo")
	}
	undo()
	if got := a.requests[0].Collection; got != "" {
		t.Fatalf("undo left the request in %q", got)
	}
	onDisk, _, _, err = loadLibrary(a.library.root)
	if err != nil || len(onDisk) != 2 || onDisk[0].Collection != "" {
		t.Fatalf("disk after undo = %+v, %v", onDisk, err)
	}
}

func TestReorderRequestPersistsSavedOrder(t *testing.T) {
	a := libraryApp(t)
	requests := []savedRequest{
		{Request: request.Request{ID: "two", Name: "req2", Collection: "Shop", Method: "GET"}},
		{Request: request.Request{ID: "ten", Name: "req10", Collection: "Shop", Method: "GET"}},
	}
	if err := a.writeLibrary(requests, []request.Folder{{Path: "Shop", Description: "settings"}}); err != nil {
		t.Fatal(err)
	}
	move := libraryMove{
		item: libraryDrag{kind: requestItem, id: "saved:ten"},
		to:   libraryDrop{parent: "Shop", before: libraryDrag{kind: requestItem, id: "saved:two"}},
	}
	if !a.moveLibrary(move) {
		t.Fatal("reorder refused")
	}
	var order []string
	for _, r := range a.requests {
		order = append(order, r.ID)
	}
	if !slices.Equal(order, []string{"ten", "two"}) {
		t.Fatalf("order in memory = %q, want the dropped order", order)
	}
	onDisk, _, _, err := loadLibrary(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	order = nil
	for _, r := range onDisk {
		order = append(order, r.ID)
	}
	if !slices.Equal(order, []string{"ten", "two"}) {
		t.Fatalf("order on disk = %q, want the dropped order", order)
	}
}

func TestFolderMoveIntoFolderAndRefuseDescendant(t *testing.T) {
	a := libraryApp(t)
	requests := []savedRequest{
		{Request: request.Request{ID: "one", Name: "One", Collection: "Shop/Orders", Method: "GET"}},
		{Request: request.Request{ID: "two", Name: "Two", Collection: "Shop", Method: "GET"}},
	}
	folders := []request.Folder{
		{Path: "Shop", Description: "shop"}, {Path: "Shop/Orders", Pre: "saved script"},
		{Path: "Archive", Description: "archive"},
	}
	if err := a.writeLibrary(requests, folders); err != nil {
		t.Fatal(err)
	}
	a.addDocument(a.requests[0], a.requests[0].key())
	opened := a.requestDocument
	a.collectionShown("Shop/Orders")
	closed := a.newDocument(a.requests[0], a.requests[0].key())
	a.documents = append(a.documents, closed)
	a.rememberClosedDocument(closed)
	a.documents = a.documents[:1]

	cycle := libraryMove{item: libraryDrag{kind: folderItem, id: "Shop"}, to: libraryDrop{parent: "Shop/Orders"}}
	if a.moveLibrary(cycle) {
		t.Fatal("moving a folder into its descendant was accepted")
	}
	if _, _, _, err := loadLibrary(a.library.root); err != nil {
		t.Fatal(err)
	}
	if got := a.requests[0].Collection; got != "Shop/Orders" {
		t.Fatalf("refused move changed the library: %q", got)
	}

	move := libraryMove{item: libraryDrag{kind: folderItem, id: "Shop/Orders"}, to: libraryDrop{parent: "Archive"}}
	if !a.moveLibrary(move) {
		t.Fatal("folder move was refused")
	}
	if got := a.requests[0].Collection; got != "Archive/Orders" {
		t.Fatalf("moved request collection = %q", got)
	}
	onDisk, loadedFolders, _, err := loadLibrary(a.library.root)
	if err != nil || onDisk[0].Collection != "Archive/Orders" {
		t.Fatalf("disk after folder move = %+v, %v", onDisk, err)
	}
	if !slices.ContainsFunc(loadedFolders, func(f request.Folder) bool { return f.Path == "Archive/Orders" && f.Pre == "saved script" }) {
		t.Fatalf("moved folder settings = %+v", loadedFolders)
	}
	if opened.composeCollection != "Archive/Orders" || opened.baseline.Collection != "Archive/Orders" {
		t.Errorf("open tab did not follow the folder: %+v", opened.composeCollection)
	}
	if _, ok := a.collectionOpen["Archive/Orders"]; !ok {
		t.Error("the folder's open state was lost")
	}
	entry, _ := a.closedDocuments().Get(closed.id)
	if entry.baseline.Collection != "Archive/Orders" || entry.draft.Collection != "Archive/Orders" {
		t.Errorf("closed tab did not follow: %+v", entry)
	}
	if !slices.Contains(a.library.folderOrder["Archive"], "Orders") {
		t.Errorf("the folder's new sibling order was not saved: %v", a.library.folderOrder)
	}
	var files []string
	for _, file := range libraryTree(t, a.library.root) {
		if strings.Contains(file, "Orders") {
			files = append(files, file)
		}
	}
	if !slices.Equal(files, []string{"Archive/Orders/One.request.json", "Archive/Orders/folder.json"}) {
		t.Fatalf("files under the moved folder = %q", files)
	}
}

// dragRows presses the first row and moves over the second, with a stop
// between them so MyGo starts a value drag rather than treating it as a click.
func dragRows(tt *ui.Tester, from, to ui.Rect) {
	x0, y0 := from.X+from.W/2, from.Y+from.H/2
	x1, y1 := to.X+to.W/2, to.Y+to.H/2
	tt.Press(x0, y0)
	tt.Move((x0+x1)/2, (y0+y1)/2)
	tt.Move(x1, y1)
}

func TestTreeDropQueuesMove(t *testing.T) {
	a := libraryApp(t)
	a.page = pageRequest
	if err := a.writeLibrary([]savedRequest{
		{Request: request.Request{ID: "one", Name: "One", Collection: "Shop", Method: "GET"}},
	}, []request.Folder{{Path: "Shop", Description: "shop"}, {Path: "Archive", Description: "archive"}}); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1100, 700)
	from, ok := tt.Find(locales.CollectionPath("Shop"))
	if !ok {
		t.Fatal("no Shop row")
	}
	to, ok := tt.Find(locales.CollectionPath("Archive"))
	if !ok {
		t.Fatal("no Archive row")
	}
	dragRows(tt, from, to)
	x, y := to.X+to.W/2, to.Y+to.H/2
	tt.Release(x, y)
	if got := a.requests[0].Collection; got == "Archive/Shop" {
		return // the release already ran the next build and applied the move
	}
	if a.pendingMove == nil || a.pendingMove.to.parent != "Archive" {
		t.Fatalf("drop did not queue a move into Archive: %+v, requests %+v", a.pendingMove, a.requests)
	}
	tt.Frame()
	if got := a.requests[0].Collection; got != "Archive/Shop" {
		t.Fatalf("applied drop left the request in %q", got)
	}
}

func TestEscapeCancelsTreeDrag(t *testing.T) {
	a := libraryApp(t)
	a.page = pageRequest
	if err := a.writeLibrary([]savedRequest{
		{Request: request.Request{ID: "one", Name: "One", Collection: "Shop", Method: "GET"}},
	}, []request.Folder{{Path: "Shop", Description: "shop"}, {Path: "Archive", Description: "archive"}}); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1100, 700)
	from, ok := tt.Find(locales.CollectionPath("Shop"))
	if !ok {
		t.Fatal("no Shop row")
	}
	to, ok := tt.Find(locales.CollectionPath("Archive"))
	if !ok {
		t.Fatal("no Archive row")
	}
	dragRows(tt, from, to)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.pendingMove != nil {
		t.Fatalf("Escape left a queued move: %+v", a.pendingMove)
	}
	if got := a.requests[0].Collection; got != "Shop" {
		t.Errorf("Escape moved the request to %q", got)
	}
	onDisk, _, _, err := loadLibrary(a.library.root)
	if err != nil || len(onDisk) != 1 || onDisk[0].Collection != "Shop" {
		t.Fatalf("Escape changed disk: %+v, %v", onDisk, err)
	}
}

func TestMoveToCommandOpensDialog(t *testing.T) {
	a := libraryApp(t)
	a.page = pageRequest
	a.requestChoice = "saved:one"
	if err := a.writeLibrary([]savedRequest{
		{Request: request.Request{ID: "one", Name: "One", Collection: "Shop", Method: "GET"}},
	}, []request.Folder{{Path: "Shop", Description: "shop"}, {Path: "Archive", Description: "archive"}}); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1100, 700)
	if err := tt.RightClick("One"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, locales.MoveToEllipsis()); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText(locales.MoveDestination()) || !tt.HasText("Archive") {
		t.Fatalf("move dialog missing: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.moveTo.open {
		t.Fatal("Escape did not close Move to…")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyM)
	tt.Frame()
	if !a.moveTo.open {
		t.Fatal("the command shortcut did not open Move to…")
	}
}
