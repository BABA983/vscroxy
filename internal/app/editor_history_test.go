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

// TestTabCloseAsksBeforeDiscarding checks the strip's own close button: it
// goes through the same dirty confirmation as the document header's.
func TestTabCloseAsksBeforeDiscarding(t *testing.T) {
	a := workspaceApp()
	draft := openCleanTab(a, "Draft", "https://example.test/draft")
	draft.composeURL = "https://example.test/edited"
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if err := tt.Click(locales.CloseTabNamed("Draft")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.closingDocument != draft || !tt.HasText(locales.SaveChanges()) {
		t.Fatalf("tab close did not ask: %v", tt.Texts())
	}
	if err := tt.Click(locales.Discard()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if slices.Contains(a.documents, draft) || a.closingDocument != nil {
		t.Fatal("discarding from the tab prompt left the draft open")
	}
}

// TestTabContextMenuClosesOthers checks the strip's right-click menu: a tab
// offers VS Code's closes, and Close Others keeps the tab it opened on while
// skipping pinned and fixed tabs.
func TestTabContextMenuClosesOthers(t *testing.T) {
	a := workspaceApp()
	openCleanTab(a, "First", "https://example.test/first")
	keep := openCleanTab(a, "Keep", "https://example.test/keep")
	openCleanTab(a, "Last", "https://example.test/last")
	tt := ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	tab, ok := tt.AccessByRole(ui.RoleButton, "Keep")
	if !ok {
		t.Fatalf("the Keep tab is not drawn: %v", tt.Texts())
	}
	tt.RightClickAt(tab.Bounds.X+20, tab.Bounds.Y+tab.Bounds.H/2)
	tt.Frame()
	menu := tt.Menu()
	for _, want := range []string{locales.Close(), locales.CloseOtherTabs(), locales.CloseTabsToTheRight(), locales.CloseAllTabs(), locales.PinTab(), locales.CopyUrl(), locales.RevealInSidebar()} {
		if !slices.Contains(menu, want) {
			t.Fatalf("tab menu lacks %q: %v", want, menu)
		}
	}
	if err := tt.ChooseMenuItem(locales.CloseOtherTabs()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !slices.Equal(a.documents, []*requestDocument{keep}) {
		t.Fatalf("tabs after Close Others: %v", a.documents)
	}
	if a.requestDocument != keep {
		t.Fatal("Close Others did not keep the tab it opened on active")
	}
	// Live is fixed: its menu keeps the closes that can run and offers no
	// Close or Pin of its own.
	liveTab, ok := tt.AccessByRole(ui.RoleButton, locales.Live())
	if !ok {
		t.Fatal("the fixed Live tab is not drawn")
	}
	tt.RightClickAt(liveTab.Bounds.X+20, liveTab.Bounds.Y+liveTab.Bounds.H/2)
	tt.Frame()
	live := tt.Menu()
	if slices.Contains(live, locales.Close()) || slices.Contains(live, locales.PinTab()) {
		t.Fatalf("the fixed Live tab offers Close or Pin: %v", live)
	}
	if !slices.Contains(live, locales.CloseAllTabs()) {
		t.Fatalf("the fixed Live tab lost Close All: %v", live)
	}
}

// TestPreviewReplacementForgetsTheOldDocument checks that the single preview
// slot is truly one document: the next frame must not adopt the replaced
// preview as a background tab.
func TestPreviewReplacementForgetsTheOldDocument(t *testing.T) {
	a := workspaceApp()
	a.requests = append(a.requests, savedRequest{Name: "Other", Collection: "Workspace/API", Method: "POST", URL: "https://example.test/other"})
	a.previewSaved(a.requests[0].key())
	first := a.requestDocument
	a.previewSaved(a.requests[1].key())
	second := a.requestDocument
	if first == nil || second == nil || first == second {
		t.Fatal("preview did not open two different documents")
	}
	if slices.Contains(a.documents, first) {
		t.Fatal("the replaced preview document stayed open")
	}
	if _, ok := a.tabDocuments[requestTabID(first)]; ok {
		t.Fatal("the replaced preview document kept its tab mapping")
	}
	g := a.tabsGroup()
	if g.preview() != requestTabID(second) {
		t.Fatalf("preview = %v, want the second document", g.preview())
	}
	if got := len(g.tabs()); got != 2 {
		t.Fatalf("strip has %d tabs, want Live and one preview", got)
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
	if len(a.documents) != 0 || a.closedCount() != 3 {
		t.Fatalf("closed %d tabs, stack has %d", len(a.documents), a.closedCount())
	}
	for _, want := range []string{"three", "two", "one"} {
		if !a.reopenClosedDocument() {
			t.Fatalf("reopening %s failed with an empty stack message", want)
		}
		if got := a.requestDocument.title(); got != want {
			t.Fatalf("reopened %q, want %q", got, want)
		}
	}
	if a.reopenClosedDocument() || a.closedCount() != 0 {
		t.Fatal("the stack still holds entries after three reopens")
	}
}

func TestReopenStackKeepsOnlyTheLastTwentyCloses(t *testing.T) {
	a := workspaceApp()
	names := make([]string, 0, tabReopenLimit+2)
	for i := range tabReopenLimit + 2 {
		name := "draft-" + string(rune('a'+i))
		names = append(names, name)
		openCleanTab(a, name, "https://example.test/"+name)
	}
	for _, d := range slices.Clone(a.documents) {
		a.closeDocument(d)
	}
	if got := a.closedCount(); got != tabReopenLimit {
		t.Fatalf("stack has %d entries, want %d", got, tabReopenLimit)
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
	if a.closedCount() != 3 {
		t.Fatalf("the three closed tabs were not put on the reopen stack: %d", a.closedCount())
	}
	foundClean := false
	for _, id := range a.tabsGroup().closedIDs() {
		e, ok := a.closedDocs[id]
		if ok && e.doc.title() == clean.title() {
			foundClean = true
		}
	}
	if !foundClean {
		t.Fatal("the clean closed tab lost its document")
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
	if a.closedCount() != 1 {
		t.Fatalf("close-saved put %d tabs on the stack, want 1", a.closedCount())
	}
	if !a.reopenClosedDocument() || a.documents[0].title() != cleanSaved.title() ||
		a.documents[0].savedKey != cleanSaved.savedKey {
		t.Fatal("the closed saved tab did not return to its old position")
	}
}

// A Ctrl+Tab pick is provisional, as in VS Code: two presses with no other
// input walk the MRU order, but the second press must not reorder it.
func TestMRUQuickSwitchWalksUntilTheChordEnds(t *testing.T) {
	a := mruTestApp(t)
	tt := ui.NewTester(a.view, 1280, 800)
	tt.HoldModifiers(ui.Ctrl)
	tt.Frame()
	for _, want := range []string{"B", "C"} {
		tt.Key(ui.Ctrl, ui.KeyTab)
		tt.Frame()
		if got := a.requestDocument.title(); got != want {
			t.Fatalf("Ctrl+Tab switched to %q, want %q", got, want)
		}
	}
	// Releasing Ctrl commits C, so the next separate chord starts there.
	tt.HoldModifiers(0)
	tt.Frame()
	if a.mruPending {
		t.Fatal("the quick-switch pick was not committed on Ctrl release")
	}
	tt.HoldModifiers(ui.Ctrl)
	tt.Key(ui.Ctrl, ui.KeyTab)
	tt.Frame()
	if got := a.requestDocument.title(); got != "A" {
		t.Fatalf("Ctrl+Tab after Ctrl release switched to %q, want A", got)
	}
}

// Typing commits the pick before the next chord. From [A B C], stepping to B
// and editing there makes B most recent, so the next Ctrl+Tab returns to A.
func TestMRUQuickSwitchCommitsPickOnEdit(t *testing.T) {
	a := mruTestApp(t)
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	tt.Key(ui.Ctrl, ui.KeyTab)
	tt.Frame()
	b := a.requestDocument
	if b.title() != "B" {
		t.Fatalf("Ctrl+Tab switched to %q, want B", b.title())
	}
	before := b.composeURL
	tt.Type("?edited=1")
	tt.Frame()
	if b.composeURL == before {
		t.Fatal("the test did not edit the picked tab")
	}
	tt.Key(ui.Ctrl, ui.KeyTab)
	tt.Frame()
	if got := a.requestDocument.title(); got != "A" {
		t.Fatalf("Ctrl+Tab after editing B switched to %q, want A", got)
	}
}

// LIVE is one entry in the same order: when it is most recently active, the
// first forward press lands on the newest request tab rather than nothing.
func TestMRUQuickSwitchFromLiveStepsPastLive(t *testing.T) {
	a := mruTestApp(t)
	a.page, a.requestDocument = pageLive, nil
	a.activateDocument(nil)
	want := a.documents[0]
	if want.title() != "A" {
		t.Fatalf("the newest request tab is %q", want.title())
	}
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	tt.Key(ui.Ctrl, ui.KeyTab)
	tt.Frame()
	if a.page != pageRequest || a.requestDocument != want {
		t.Fatalf("Ctrl+Tab from LIVE opened %q on page %v, want %q", a.requestDocument.title(), a.page, want.title())
	}
}

// Activating LIVE is an MRU visit like activating a request tab. From
// [A B C], visiting LIVE and then B puts B ahead of LIVE, so Ctrl+Tab must
// return to LIVE rather than to A.
func TestMRUQuickSwitchReturnsToActivatedLive(t *testing.T) {
	a := mruTestApp(t)
	a.activateDocument(nil)
	b := a.documents[1]
	a.activateDocument(b)
	if a.page != pageRequest || a.requestDocument != b {
		t.Fatalf("activation left %q on page %v, want B", a.requestDocument.title(), a.page)
	}

	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	tt.Key(ui.Ctrl, ui.KeyTab)
	tt.Frame()
	// LIVE can hold an inspected capture as a document, but it must not be B.
	if a.page != pageLive || a.requestDocument == b {
		t.Fatalf("Ctrl+Tab opened page %v with B selected %v, want LIVE", a.page, a.requestDocument == b)
	}
}

// mruTestApp opens A, B, C and activates them in the order C, B, A, leaving
// the recency order [A B C] and A on screen.
func mruTestApp(t *testing.T) *app {
	t.Helper()
	a := workspaceApp()
	for _, name := range []string{"A", "B", "C"} {
		a.addDocument(savedRequest{Name: name, Method: "GET", URL: "https://example.test/" + name}, "")
	}
	a.activateDocument(a.documents[2])
	a.activateDocument(a.documents[1])
	a.activateDocument(a.documents[0])
	return a
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
