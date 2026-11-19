package app

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// openCleanTab gives a tab a baseline equal to its value without touching the
// library, so closing it needs no confirmation.
func openCleanTab(a *app, name, url string) *requestDocument {
	a.addDocument(savedRequest{Name: name, Method: "GET", URL: url}, "")
	d := a.requestDocument
	d.baseline = d.value()
	return d
}

// TestReopenDiscardedDraftRestoresExactDraftAtItsPosition covers the central
// promise of Reopen Closed Editor: Discard must not lose the draft when the
// tab is brought back with the keyboard.
func TestReopenDiscardedDraftRestoresExactDraftAtItsPosition(t *testing.T) {
	a := workspaceApp()
	first := openCleanTab(a, "First", "https://example.test/first")
	draft := openCleanTab(a, "Draft", "https://example.test/original")
	draft.composeURL = "https://example.test/edited?t=1"
	draft.composeBody = `{"kept":true}`
	draft.composeHeaders = "X-Kept: yes"
	last := openCleanTab(a, "Last", "https://example.test/last")
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if !draft.dirty() {
		t.Fatal("the draft to close is not dirty")
	}
	a.requestClose(draft)
	tt.Frame()
	if !tt.HasText(locales.SaveChanges()) {
		t.Fatalf("closing a dirty draft did not ask: %v", tt.Texts())
	}
	// With the prompt up, the tab still holds the draft Discard is about to
	// close; compare the reopened document against that value.
	want := draft.value()
	wantKey, wantBaseline := draft.savedKey, draft.baseline
	if err := tt.Click(locales.Discard()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if slices.Contains(a.documents, draft) {
		t.Fatal("the discarded tab stayed open")
	}
	if got := a.documents[0]; got != first {
		t.Fatalf("first tab changed while closing: %q", got.title())
	}
	if got := a.documents[len(a.documents)-1]; got != last {
		t.Fatalf("last tab changed while closing: %q", got.title())
	}

	tt.Key(ui.Cmd|ui.Shift, ui.KeyT)
	tt.Frame()
	if len(a.documents) != 3 {
		t.Fatalf("reopen left %d tabs, want 3", len(a.documents))
	}
	got := a.documents[1]
	if got == draft || got.title() != "Draft" {
		t.Fatalf("reopen got %q, want the closed draft", got.title())
	}
	if !reflect.DeepEqual(got.value(), want) {
		t.Fatalf("draft changed:\n got %+v\nwant %+v", got.value(), want)
	}
	if got.savedKey != wantKey || !reflect.DeepEqual(got.baseline, wantBaseline) {
		t.Fatalf("key/baseline changed: %q, %+v", got.savedKey, got.baseline)
	}
	if !got.dirty() {
		t.Fatal("the restored draft lost its unsaved state")
	}
	if a.requestDocument != got {
		t.Fatal("the reopened tab was not made active")
	}
}

func TestReopenClosedTabsIsLIFO(t *testing.T) {
	a := workspaceApp()
	for _, name := range []string{"one", "two", "three"} {
		openCleanTab(a, name, "https://example.test/"+name)
	}
	for _, d := range slices.Clone(a.documents) {
		a.closeDocument(d)
	}
	if len(a.documents) != 0 || a.closedDocuments().Size() != 3 {
		t.Fatalf("closed %d tabs, stack has %d", len(a.documents), a.closedDocuments().Size())
	}
	for _, want := range []string{"three", "two", "one"} {
		if !a.reopenClosedDocument() {
			t.Fatalf("reopening %s failed with an empty stack message", want)
		}
		if got := a.requestDocument.title(); got != want {
			t.Fatalf("reopened %q, want %q", got, want)
		}
	}
	if a.reopenClosedDocument() || a.closedDocuments().Size() != 0 {
		t.Fatal("the stack still holds entries after three reopens")
	}
}

func TestReopenStackKeepsOnlyTheLastTwentyCloses(t *testing.T) {
	a := workspaceApp()
	names := make([]string, 0, recentlyClosedLimit+2)
	for i := range recentlyClosedLimit + 2 {
		name := "draft-" + string(rune('a'+i))
		names = append(names, name)
		openCleanTab(a, name, "https://example.test/"+name)
	}
	for _, d := range slices.Clone(a.documents) {
		a.closeDocument(d)
	}
	if got := a.closedDocuments().Size(); got != recentlyClosedLimit {
		t.Fatalf("stack has %d entries, want %d", got, recentlyClosedLimit)
	}
	kept := names[2:]
	for i := len(kept) - 1; i >= 0; i-- {
		want := kept[i]
		if !a.reopenClosedDocument() {
			t.Fatalf("reopen %d failed", i)
		}
		if got := a.requestDocument.title(); got != want {
			t.Fatalf("reopen %d got %q, want %q", i, got, want)
		}
	}
	if a.reopenClosedDocument() {
		t.Fatal("the dropped first closes came back")
	}
}

// TestCloseOthersPromptsEachDirtyTabAndKeepsTheActiveOne checks both the
// confirmations and the stack: a batch close must not quietly discard edits.
func TestCloseOthersPromptsEachDirtyTabAndKeepsTheActiveOne(t *testing.T) {
	a := workspaceApp()
	keep := openCleanTab(a, "Keep", "https://example.test/keep")
	first := openCleanTab(a, "Dirty one", "https://example.test/one")
	first.composeBody = "first edit"
	clean := openCleanTab(a, "Clean", "https://example.test/clean")
	second := openCleanTab(a, "Dirty two", "https://example.test/two")
	second.composeBody = "second edit"

	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	a.closeOtherDocuments(keep)
	tt.Frame()
	if a.closingDocument != first || !tt.HasText(locales.DraftUnsavedChanges(first.title())) {
		t.Fatalf("first prompt was for %v: %v", a.closingDocument, tt.Texts())
	}
	if err := tt.Click(locales.Discard()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.closingDocument != second || !tt.HasText(locales.DraftUnsavedChanges(second.title())) {
		t.Fatalf("second prompt was for %v: %v", a.closingDocument, tt.Texts())
	}
	if err := tt.Click(locales.Discard()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !slices.Equal(a.documents, []*requestDocument{keep}) {
		t.Fatalf("tabs after close-others: %v", a.documents)
	}
	if a.requestDocument != keep || a.closingDocument != nil || a.closingReturnTo != nil {
		t.Fatal("the tab close-others kept did not get the focus back")
	}
	if a.closedDocuments().Size() != 3 {
		t.Fatalf("the three closed tabs were not put on the reopen stack: %d", a.closedDocuments().Size())
	}
	foundClean := false
	for _, e := range a.closedDocuments().All() {
		if e.doc.title() == clean.title() && e.position == 2 {
			foundClean = true
		}
	}
	if !foundClean {
		t.Fatal("the clean closed tab lost its position")
	}
}

func TestCloseSavedLeavesDirtyAndUnsavedTabsOpen(t *testing.T) {
	a := workspaceApp()
	saved := savedRequest{Name: "Saved", Method: "GET", URL: "https://example.test/saved"}
	a.requests = append(a.requests, saved)
	a.addDocument(saved, saved.key())
	cleanSaved := a.requestDocument
	dirtySaved := a.newDocument(saved, saved.key())
	a.documents = append(a.documents, dirtySaved)
	dirtySaved.composeURL = "https://example.test/dirty-saved"
	unsaved := openCleanTab(a, "Unsaved", "https://example.test/unsaved")
	dirtyUnsaved := openCleanTab(a, "Dirty unsaved", "https://example.test/dirty-unsaved")
	dirtyUnsaved.composeBody = "edit"

	a.closeSavedDocuments()
	if slices.Contains(a.documents, cleanSaved) {
		t.Fatal("a clean saved tab was closed but reopening must still save it")
	}
	for _, d := range []*requestDocument{dirtySaved, unsaved, dirtyUnsaved} {
		if !slices.Contains(a.documents, d) {
			t.Fatalf("%q should have stayed open", d.title())
		}
	}
	if a.closedDocuments().Size() != 1 {
		t.Fatalf("close-saved put %d tabs on the stack, want 1", a.closedDocuments().Size())
	}
	if !a.reopenClosedDocument() || a.documents[0].title() != cleanSaved.title() ||
		a.documents[0].savedKey != cleanSaved.savedKey {
		t.Fatal("the closed saved tab did not return to its old position")
	}
}

// TestMRUQuickSwitchVisitsRecentTabs exercises the keys, not just the order:
// after activating B, C, A, Ctrl+Tab must skip back to C, then B.
func TestMRUQuickSwitchVisitsRecentTabs(t *testing.T) {
	a := workspaceApp()
	a.addDocument(savedRequest{Name: "A", Method: "GET", URL: "https://example.test/a"}, "")
	a.addDocument(savedRequest{Name: "B", Method: "GET", URL: "https://example.test/b"}, "")
	third := openCleanTab(a, "C", "https://example.test/c")
	b, aTab := a.documents[1], a.documents[0]
	a.activateDocument(b)
	a.activateDocument(third)
	a.activateDocument(aTab)
	if got := a.requestDocument.title(); got != "A" {
		t.Fatalf("the test started at %q", got)
	}

	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	for _, want := range []string{"C", "B"} {
		tt.Key(ui.Ctrl, ui.KeyTab)
		tt.Frame()
		if got := a.requestDocument.title(); got != want {
			t.Fatalf("Ctrl+Tab switched to %q, want %q", got, want)
		}
	}
	// Backwards walks the same MRU order: B → C → A.
	for _, want := range []string{"C", "A"} {
		tt.Key(ui.Ctrl|ui.Shift, ui.KeyTab)
		tt.Frame()
		if got := a.requestDocument.title(); got != want {
			t.Fatalf("Ctrl+Shift+Tab switched to %q, want %q", got, want)
		}
	}
	// Next/previous tab stay in positional order when chosen from a menu.
	at := slices.Index(a.documents, a.requestDocument)
	a.cycleDocument(1)
	if a.requestDocument != a.documents[(at+1)%len(a.documents)] {
		t.Fatal("next-tab no longer follows the tab strip")
	}
}

// TestReopenMissingSourceBecomesUnsaved covers a saved request deleted or
// renamed while its closed draft waited on the stack.
func TestReopenMissingSourceBecomesUnsaved(t *testing.T) {
	for _, change := range []string{"deleted", "renamed"} {
		t.Run(change, func(t *testing.T) {
			a := workspaceApp()
			saved := savedRequest{Name: "Source", Method: "GET", URL: "https://example.test/source"}
			a.requests = append(a.requests, saved)
			a.addDocument(saved, saved.key())
			d := a.requestDocument
			d.composeBody = "unsaved edit"
			want := d.value()
			a.closeDocument(d)
			switch change {
			case "deleted":
				a.requests = nil
			case "renamed":
				a.requests[len(a.requests)-1].Name = "New name"
			}
			if !a.reopenClosedDocument() {
				t.Fatal("reopen failed after the saved source changed")
			}
			got := a.requestDocument
			if got.savedKey != "" || got.assetID != "" {
				t.Fatalf("the reopened draft still points at %q", got.savedKey)
			}
			if !reflect.DeepEqual(got.value(), want) || !got.dirty() {
				t.Fatal("the reopened draft lost its edits or dirty state")
			}
		})
	}
}

// TestTabContextMenuScreenshots exports 2x captures of the tab's ⋯ menu in
// both themes. Set VSCROXY_EDITOR_HISTORY_SCREENSHOTS to an output directory.
func TestTabContextMenuScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_EDITOR_HISTORY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_EDITOR_HISTORY_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run(appearance, func(t *testing.T) {
			a := workspaceApp()
			a.appearance = appearance
			a.openSaved(a.requests[0].key())
			saved := a.requestDocument
			a.addDocument(savedRequest{Name: "Draft", Method: "POST", URL: "https://example.test/draft"}, "")
			a.addDocument(savedRequest{Name: "Closed", Method: "GET", URL: "https://example.test/closed"}, "")
			a.closeDocument(a.requestDocument)
			a.activateDocument(saved)

			tt := ui.NewTester(a.view, 1280, 800)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			if err := tt.Click(locales.RequestOptions()); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			for _, label := range []string{locales.CloseOtherRequests(), locales.CloseSavedRequests(), locales.ReopenClosedRequest()} {
				if !tt.HasText(label) {
					t.Fatalf("the tab menu is missing %q: %v", label, tt.Texts())
				}
			}
			var b bytes.Buffer
			if err := png.Encode(&b, tt.Image()); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "tab-context-menu-"+appearance+".png")
			if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		})
	}
}
