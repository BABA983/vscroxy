package app

import (
	"slices"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
)

// Deleting a folder takes its requests, subfolders and their settings, and
// nothing that only shares a name prefix; a clean tab closes with its
// request, an edited one stays open as a new draft.
func TestDeletingAFolderRemovesItsTreeOnly(t *testing.T) {
	a := libraryApp(t)
	a.workspace = 1
	requests := []savedRequest{
		{Request: request.Request{ID: "a", Name: "List", Collection: "Shop/Orders", Method: "GET"}},
		{Request: request.Request{ID: "b", Name: "Refund", Collection: "Shop/Orders/Refunds", Method: "POST"}},
		{Request: request.Request{ID: "c", Name: "Archive", Collection: "Shop/Orders archive", Method: "GET"}},
		{Request: request.Request{ID: "d", Name: "Home", Collection: "Shop", Method: "GET"}},
	}
	folders := []request.Folder{{Path: "Shop", Pre: "1"}, {Path: "Shop/Orders", Pre: "2"}, {Path: "Shop/Orders/Refunds", Pre: "3"}, {Path: "Shop/Orders archive", Pre: "4"}}
	if err := a.writeLibrary(requests, folders); err != nil {
		t.Fatal(err)
	}
	a.addDocument(requests[0], requests[0].key())
	a.addDocument(requests[1], requests[1].key())
	edited := a.requestDocument
	edited.composeURL = "https://shop.example/refunds"
	tt := ui.NewTester(a.view, 1280, 760)

	if err := tt.RightClick("Collection: Shop/Orders"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Delete “Shop/Orders”…"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Delete folder “Shop/Orders”?") {
		t.Fatalf("no confirmation: %q", tt.Texts())
	}
	if err := tt.Click("Delete folder"); err != nil {
		t.Fatal(err)
	}

	got, err := loadLibraryRequests(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range got {
		names = append(names, r.Collection+"/"+r.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Shop/Home", "Shop/Orders archive/Archive"}) {
		t.Fatalf("left after delete: %q", names)
	}
	var paths []string
	for _, f := range a.folders {
		paths = append(paths, f.Path)
	}
	if !slices.Equal(paths, []string{"Shop", "Shop/Orders archive"}) {
		t.Fatalf("folder settings left: %q", paths)
	}
	if len(a.documents) != 1 || a.documents[0] != edited || edited.savedKey != "" || edited.assetID != "" {
		t.Fatalf("tabs after delete: %d, edited = %q/%q", len(a.documents), edited.savedKey, edited.assetID)
	}
}
