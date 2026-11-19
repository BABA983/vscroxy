package app

import "testing"

type countingTitleWindow struct {
	titles int
	edits  int
	title  string
	edited bool
}

func (w *countingTitleWindow) SetTitle(title string) {
	w.titles++
	w.title = title
}

func (w *countingTitleWindow) SetDocumentEdited(edited bool) {
	w.edits++
	w.edited = edited
}

func titleTestDocument(a *app, name, collection string) *requestDocument {
	r := savedRequest{Name: name, Collection: collection, Method: "POST", URL: "https://example.com/orders"}
	return a.newDocument(r, "saved:"+name)
}

func TestWindowTitleNamesEachPage(t *testing.T) {
	a := workspaceApp()
	a.page, a.requestDocument = pageRequest, titleTestDocument(a, "Create order", "Shop/Orders")
	cases := []struct {
		page page
		want string
	}{
		{pageRequest, "Create order — Shop/Orders — vscroxy"},
		{pageLive, "Live — vscroxy"},
		{pageSession, "Sessions — vscroxy"},
		{pageAPI, "API contracts — vscroxy"},
		{pageRuns, "Runs — vscroxy"},
		{pageDiagnostics, "Diagnostics — vscroxy"},
		{pageOverview, "Overview — vscroxy"},
		{pageSearch, "Search — vscroxy"},
	}
	for _, tc := range cases {
		a.page = tc.page
		if got := windowTitleText(windowName(a), false, false); got != tc.want {
			t.Errorf("page %d title = %q, want %q", tc.page, got, tc.want)
		}
	}
}

func TestWindowTitleDirtyTabAndNativeMarker(t *testing.T) {
	a := workspaceApp()
	clean := titleTestDocument(a, "List products", "Shop")
	dirty := titleTestDocument(a, "Create order", "Shop/Orders")
	a.documents = []*requestDocument{clean, dirty}
	a.requestDocument, a.page = dirty, pageRequest
	if anyDocumentEdited(a) {
		t.Fatal("a newly loaded tab is already edited")
	}
	dirty.composeName = "Create order (draft)"
	if !anyDocumentEdited(a) {
		t.Fatal("renaming a tab did not mark the window edited")
	}
	want := "● Create order (draft) — Shop/Orders — vscroxy"
	if got := windowTitleText(windowName(a), true, false); got != want {
		t.Fatalf("title without a native marker = %q, want %q", got, want)
	}
	// macOS shows the dot in the close button instead of in the text.
	if got := windowTitleText(windowName(a), true, true); got != "Create order (draft) — Shop/Orders — vscroxy" {
		t.Fatalf("title with a native marker = %q", got)
	}
	// A clean active tab does not hide another tab's unsaved work.
	a.requestDocument, a.page = clean, pageRequest
	if !anyDocumentEdited(a) {
		t.Fatal("switching to a clean tab lost the dirty flag")
	}
	if got := windowName(a); got != "List products — Shop" {
		t.Fatalf("clean tab name = %q", got)
	}
}

func TestWindowTitleAndEditedFlagUpdateOnlyOnChange(t *testing.T) {
	a := workspaceApp()
	d := titleTestDocument(a, "Create order", "Shop/Orders")
	a.documents = []*requestDocument{d}
	a.requestDocument, a.page = d, pageRequest
	w := &countingTitleWindow{}
	a.titleWindow = w
	a.syncTitleFor(true)
	if w.titles != 1 || w.edits != 1 || w.edited {
		t.Fatalf("first sync = %+v", w)
	}
	for range 4 {
		a.syncTitleFor(true)
	}
	if w.titles != 1 || w.edits != 1 {
		t.Fatalf("unchanged frames rewrote the native window: %+v", w)
	}
	a.page = pageLive
	a.syncTitleFor(true)
	if w.titles != 2 || w.title != "Live — vscroxy" || w.edits != 1 {
		t.Fatalf("page change = %+v", w)
	}
	d.composeBody = "// draft only"
	a.syncTitleFor(true)
	if w.titles != 2 || w.edits != 2 || !w.edited {
		t.Fatalf("dirty flag across tabs = %+v", w)
	}
	a.syncTitleFor(true)
	if w.titles != 2 || w.edits != 2 {
		t.Fatalf("repeat after edit = %+v", w)
	}
	d.composeBody = ""
	a.syncTitleFor(true)
	if w.edits != 3 || w.edited {
		t.Fatalf("saved document = %+v", w)
	}
}

func TestWindowTitleWithoutNativeMarkerUsesDot(t *testing.T) {
	a := workspaceApp()
	d := titleTestDocument(a, "Create order", "Shop/Orders")
	a.documents = []*requestDocument{d}
	a.requestDocument, a.page = d, pageRequest
	w := &countingTitleWindow{}
	a.titleWindow = w
	a.syncTitleFor(false)
	if w.titles != 1 || w.edits != 1 {
		t.Fatalf("first sync = %+v", w)
	}
	d.composeBody = "// draft only"
	a.syncTitleFor(false)
	if w.titles != 2 || w.title != "● Create order — Shop/Orders — vscroxy" {
		t.Fatalf("dirty title = %q", w.title)
	}
	if w.edits != 1 || w.edited {
		t.Fatalf("document flag should stay off where the title has the dot: %+v", w)
	}
}
