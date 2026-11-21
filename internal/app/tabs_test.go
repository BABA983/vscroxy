package app

import (
	"fmt"
	"slices"
	"testing"
)

func testTabGroup() *tabGroup { return newTabGroup("Live") }

func requestTab(key, title string) tab {
	return tab{kind: pageRequest, key: key, title: title}
}

func liveTabID(g *tabGroup) tabID { return g.tabs()[0].id() }

func tabTitles(g *tabGroup) []string {
	titles := make([]string, 0, g.len())
	for _, e := range g.tabs() {
		titles = append(titles, e.title)
	}
	return titles
}

func tabKeys(tabs []tab) []string {
	keys := make([]string, len(tabs))
	for i, t := range tabs {
		keys[i] = t.key
	}
	return keys
}

func wantTitles(t *testing.T, g *tabGroup, want ...string) {
	t.Helper()
	if got := tabTitles(g); !slices.Equal(got, want) {
		t.Fatalf("tabs = %v, want %v", got, want)
	}
}

// wantPinnedPrefix checks the group's invariant that every pinned tab comes
// before every unpinned one.
func wantPinnedPrefix(t *testing.T, g *tabGroup) {
	t.Helper()
	unpinned := false
	for _, e := range g.tabs() {
		if e.fixed {
			continue
		}
		if e.pinned && unpinned {
			t.Fatalf("pinned tab %q came after an unpinned tab in %v", e.title, tabTitles(g))
		}
		if !e.pinned {
			unpinned = true
		}
	}
}

func TestTabStateStartsWithOneGroupAndLive(t *testing.T) {
	s := newTabState("Live")
	if len(s.groups) != 1 || s.active != 0 {
		t.Fatalf("state = %d groups, active %d", len(s.groups), s.active)
	}
	g := s.group()
	if g.len() != 1 || g.active() != liveTabID(g) || !g.isFixed(liveTabID(g)) {
		t.Fatal("the group does not start on fixed Live")
	}
}

// TestTabPreviewReplacesInPlace follows VS Code's single preview slot: a new
// preview takes the old one's place, even when an explicit index asks for a
// different one, and the replaced preview is not reopenable.
func TestTabPreviewReplacesInPlace(t *testing.T) {
	g := testTabGroup()
	a := g.open(requestTab("saved:a", "A"), tabOpen{})
	g.open(requestTab("saved:b", "B"), tabOpen{})
	if !g.activate(a) {
		t.Fatal("could not activate A")
	}

	first := g.open(requestTab("flow:1", "P1"), tabOpen{preview: true})
	wantTitles(t, g, "Live", "A", "P1", "B")
	if g.preview() != first || g.active() != first {
		t.Fatalf("preview = %v, active = %v, want %v", g.preview(), g.active(), first)
	}

	second := g.open(requestTab("flow:2", "P2"), tabOpen{preview: true, at: new(1)})
	wantTitles(t, g, "Live", "A", "P2", "B")
	if g.preview() != second || g.active() != second {
		t.Fatalf("preview = %v, active = %v, want %v", g.preview(), g.active(), second)
	}
	if g.indexOf(first) != -1 {
		t.Fatal("the replaced preview is still open")
	}
	if g.canReopen() {
		t.Fatal("replacing a preview entered the reopen stack")
	}

	// Replacing an inactive preview still keeps its slot.
	if !g.activate(a) {
		t.Fatal("could not reactivate A")
	}
	third := g.open(requestTab("flow:3", "P3"), tabOpen{preview: true})
	wantTitles(t, g, "Live", "A", "P3", "B")
	if g.indexOf(second) != -1 || g.indexOf(third) != 2 {
		t.Fatalf("P3 at %d, replaced P2 still at %d", g.indexOf(third), g.indexOf(second))
	}
}

func TestTabPreviewBecomesNormalWhenDirtyOrPinned(t *testing.T) {
	t.Run("opened dirty", func(t *testing.T) {
		g := testTabGroup()
		p := g.open(tab{kind: pageRequest, key: "flow:1", title: "P", dirty: true}, tabOpen{preview: true})
		if g.isPreview(p) {
			t.Fatal("a dirty tab opened as preview")
		}
	})

	t.Run("dirty", func(t *testing.T) {
		g := testTabGroup()
		p := g.open(requestTab("flow:1", "P"), tabOpen{preview: true})
		if !g.setDirty(p, true) {
			t.Fatal("could not mark the preview dirty")
		}
		if g.isPreview(p) {
			t.Fatal("a dirty tab stayed a preview")
		}
		r := g.close(p)
		if len(r.needsSave) != 1 || len(r.closed) != 0 || g.indexOf(p) < 0 {
			t.Fatalf("dirty close = %+v, still open at %d", r, g.indexOf(p))
		}
		if !g.setDirty(p, false) {
			t.Fatal("could not clear the dirty flag")
		}
		if r := g.close(p); len(r.closed) != 1 {
			t.Fatalf("clean close = %+v", r)
		}
	})

	t.Run("pinned", func(t *testing.T) {
		g := testTabGroup()
		g.open(requestTab("saved:a", "A"), tabOpen{})
		p := g.open(requestTab("flow:1", "P"), tabOpen{preview: true})
		if !g.pin(p) {
			t.Fatal("could not pin the preview")
		}
		if g.isPreview(p) || !g.isPinned(p) {
			t.Fatalf("pinned preview: preview=%v pinned=%v", g.isPreview(p), g.isPinned(p))
		}
		wantTitles(t, g, "Live", "P", "A")
		if !g.unpin(p) {
			t.Fatal("could not unpin P")
		}
		if g.isPinned(p) {
			t.Fatal("P stayed pinned after unpin")
		}
		wantTitles(t, g, "Live", "P", "A")
		wantPinnedPrefix(t, g)
	})
}

// TestTabCloseActivatesMostRecentlyUsed follows VS Code's default after a
// close: the second MRU entry takes over, not the visual neighbour.
func TestTabCloseActivatesMostRecentlyUsed(t *testing.T) {
	g := testTabGroup()
	a := g.open(requestTab("a", "A"), tabOpen{})
	b := g.open(requestTab("b", "B"), tabOpen{})
	c := g.open(requestTab("c", "C"), tabOpen{})
	if !g.activate(a) {
		t.Fatal("could not activate A")
	}
	if got, want := g.mru(), []tabID{a, c, b, liveTabID(g)}; !slices.Equal(got, want) {
		t.Fatalf("MRU = %v, want %v", got, want)
	}

	if r := g.close(a); len(r.closed) != 1 || r.closed[0].key != "a" {
		t.Fatalf("close A = %+v", r)
	}
	if g.active() != c {
		t.Fatalf("active = %v, want C", g.active())
	}
	wantTitles(t, g, "Live", "B", "C")

	if r := g.close(c); len(r.closed) != 1 {
		t.Fatalf("close C = %+v", r)
	}
	if g.active() != b {
		t.Fatalf("active = %v, want B", g.active())
	}
	wantTitles(t, g, "Live", "B")
}

func TestTabOpenFindsExistingTab(t *testing.T) {
	g := testTabGroup()
	a := g.open(requestTab("saved:a", "A"), tabOpen{})
	g.open(requestTab("saved:b", "B"), tabOpen{})
	if !g.activate(a) {
		t.Fatal("could not activate A")
	}

	again := g.open(requestTab("saved:a", "A"), tabOpen{})
	if again != a || g.len() != 3 || g.active() != a {
		t.Fatalf("duplicate open: id=%v len=%d active=%v", again, g.len(), g.active())
	}
	if g.isPreview(a) {
		t.Fatal("opening a normal tab again made it a preview")
	}

	// A preview open of an already open normal tab must not create a second
	// preview or a duplicate.
	g.open(requestTab("saved:a", "A"), tabOpen{preview: true})
	if g.isPreview(a) || g.len() != 3 {
		t.Fatalf("preview duplicate: preview=%v len=%d", g.isPreview(a), g.len())
	}

	p := g.open(requestTab("flow:1", "P"), tabOpen{preview: true})
	if !g.isPreview(p) {
		t.Fatal("P did not open as preview")
	}
	g.open(requestTab("flow:1", "P"), tabOpen{})
	if g.isPreview(p) || g.len() != 4 {
		t.Fatalf("normal reopen of P: preview=%v len=%d", g.isPreview(p), g.len())
	}
}

func TestTabOpenGoesRightOfActive(t *testing.T) {
	g := testTabGroup()
	a := g.open(requestTab("a", "A"), tabOpen{})
	g.open(requestTab("b", "B"), tabOpen{})
	g.open(requestTab("c", "C"), tabOpen{})
	if !g.activate(a) {
		t.Fatal("could not activate A")
	}

	d := g.open(requestTab("d", "D"), tabOpen{})
	wantTitles(t, g, "Live", "A", "D", "B", "C")
	if g.active() != d {
		t.Fatalf("active = %v, want D", g.active())
	}

	// A pinned tab joins the pinned range instead of the active tab's right.
	e := g.open(requestTab("e", "E"), tabOpen{pinned: true})
	wantTitles(t, g, "Live", "E", "A", "D", "B", "C")
	if !g.isPinned(e) {
		t.Fatal("E did not open pinned")
	}

	// An explicit index is honored for an unpinned tab, after the pinned
	// range.
	f := g.open(requestTab("f", "F"), tabOpen{at: new(2)})
	wantTitles(t, g, "Live", "E", "F", "A", "D", "B", "C")
	if g.indexOf(f) != 2 {
		t.Fatalf("F is at %d, want 2", g.indexOf(f))
	}
}

func TestTabCloseOthersSkipsFixedAndPinned(t *testing.T) {
	g := testTabGroup()
	a := g.open(requestTab("a", "A"), tabOpen{})
	b := g.open(requestTab("b", "B"), tabOpen{})
	c := g.open(requestTab("c", "C"), tabOpen{})
	g.open(requestTab("d", "D"), tabOpen{})
	g.open(requestTab("flow:1", "P"), tabOpen{preview: true})
	if !g.pin(b) {
		t.Fatal("could not pin B")
	}
	if !g.setDirty(c, true) {
		t.Fatal("could not mark C dirty")
	}
	if !g.activate(a) {
		t.Fatal("could not activate A")
	}
	wantTitles(t, g, "Live", "B", "A", "C", "D", "P")

	r := g.closeOthers(a)
	if len(r.needsSave) != 1 || r.needsSave[0].key != "c" {
		t.Fatalf("needs save = %v, want C", tabKeys(r.needsSave))
	}
	if got, want := tabKeys(r.closed), []string{"d", "flow:1"}; !slices.Equal(got, want) {
		t.Fatalf("closed = %v, want %v", got, want)
	}
	wantTitles(t, g, "Live", "B", "A", "C")
	if g.active() != a {
		t.Fatalf("active = %v, want A", g.active())
	}
	if !g.isPinned(b) || !g.isFixed(liveTabID(g)) {
		t.Fatal("close others disturbed a pinned or fixed tab")
	}

	if r := g.close(liveTabID(g)); len(r.refused) != 1 || g.indexOf(liveTabID(g)) != 0 {
		t.Fatalf("closing Live = %+v", r)
	}
	if r := g.forceClose(c); len(r.closed) != 1 {
		t.Fatalf("force close C = %+v", r)
	}
	wantTitles(t, g, "Live", "B", "A")
}

func TestTabCloseToRightSkipsPinnedAndFallsBack(t *testing.T) {
	g := testTabGroup()
	a := g.open(requestTab("a", "A"), tabOpen{})
	g.open(requestTab("b", "B"), tabOpen{})
	c := g.open(requestTab("c", "C"), tabOpen{})
	d := g.open(requestTab("d", "D"), tabOpen{})
	if !g.pin(a) || !g.pin(c) {
		t.Fatal("could not pin A and C")
	}
	wantTitles(t, g, "Live", "A", "C", "B", "D")
	if !g.activate(d) {
		t.Fatal("could not activate D")
	}

	r := g.closeToRight(a)
	if got, want := tabKeys(r.closed), []string{"b", "d"}; !slices.Equal(got, want) {
		t.Fatalf("closed = %v, want %v", got, want)
	}
	if len(r.needsSave) != 0 {
		t.Fatalf("needs save = %v", tabKeys(r.needsSave))
	}
	wantTitles(t, g, "Live", "A", "C")
	if g.active() != a {
		t.Fatalf("active = %v, want A", g.active())
	}
}

func TestTabCloseSavedAndAllSkipFixedAndPinned(t *testing.T) {
	t.Run("saved", func(t *testing.T) {
		g := testTabGroup()
		a := g.open(requestTab("a", "A"), tabOpen{})
		g.open(requestTab("b", "B"), tabOpen{})
		c := g.open(requestTab("c", "C"), tabOpen{})
		if !g.pin(c) {
			t.Fatal("could not pin C")
		}
		if !g.setDirty(a, true) {
			t.Fatal("could not mark A dirty")
		}
		// [Live, C, A, B], with A dirty.
		r := g.closeSaved()
		if got, want := tabKeys(r.closed), []string{"b"}; !slices.Equal(got, want) {
			t.Fatalf("closed = %v, want %v", got, want)
		}
		if len(r.needsSave) != 0 {
			t.Fatalf("close saved asked for a confirmation: %v", tabKeys(r.needsSave))
		}
		wantTitles(t, g, "Live", "C", "A")
		if g.active() != c {
			t.Fatalf("active = %v, want C", g.active())
		}
	})

	t.Run("all", func(t *testing.T) {
		g := testTabGroup()
		a := g.open(requestTab("a", "A"), tabOpen{})
		b := g.open(requestTab("b", "B"), tabOpen{})
		c := g.open(requestTab("c", "C"), tabOpen{})
		g.open(requestTab("d", "D"), tabOpen{})
		if !g.pin(c) {
			t.Fatal("could not pin C")
		}
		if !g.setDirty(b, true) {
			t.Fatal("could not mark B dirty")
		}
		if !g.activate(a) {
			t.Fatal("could not activate A")
		}
		// [Live, C, A, B, D], with B dirty and A active.
		r := g.closeAll()
		if got, want := tabKeys(r.closed), []string{"a", "d"}; !slices.Equal(got, want) {
			t.Fatalf("closed = %v, want %v", got, want)
		}
		if len(r.needsSave) != 1 || r.needsSave[0].key != "b" {
			t.Fatalf("needs save = %v, want B", tabKeys(r.needsSave))
		}
		wantTitles(t, g, "Live", "C", "B")
		if g.active() != c {
			t.Fatalf("active = %v, want C", g.active())
		}
	})
}

func TestTabReopenRestoresIndex(t *testing.T) {
	g := testTabGroup()
	g.open(requestTab("a", "A"), tabOpen{})
	b := g.open(requestTab("b", "B"), tabOpen{})
	g.open(requestTab("c", "C"), tabOpen{})
	if r := g.close(b); len(r.closed) != 1 {
		t.Fatalf("close B = %+v", r)
	}
	wantTitles(t, g, "Live", "A", "C")

	g.open(requestTab("d", "D"), tabOpen{})
	wantTitles(t, g, "Live", "A", "C", "D")
	if !g.reopen() {
		t.Fatal("nothing to reopen")
	}
	wantTitles(t, g, "Live", "A", "B", "C", "D")
	if g.indexOf(b) != 2 {
		t.Fatalf("B reopened at %d, want 2", g.indexOf(b))
	}
	if g.active() != b {
		t.Fatalf("active = %v, want B", g.active())
	}
	if g.reopen() {
		t.Fatal("reopened an empty stack")
	}
}

func TestTabReopenRestoresPinned(t *testing.T) {
	g := testTabGroup()
	a := g.open(requestTab("a", "A"), tabOpen{})
	g.open(requestTab("b", "B"), tabOpen{})
	if !g.pin(a) {
		t.Fatal("could not pin A")
	}
	if r := g.close(a); len(r.closed) != 1 {
		t.Fatalf("close A = %+v", r)
	}
	c := g.open(requestTab("c", "C"), tabOpen{})
	if !g.pin(c) {
		t.Fatal("could not pin C")
	}
	wantTitles(t, g, "Live", "C", "B")

	if !g.reopen() {
		t.Fatal("nothing to reopen")
	}
	wantTitles(t, g, "Live", "A", "C", "B")
	if g.indexOf(a) != 1 || !g.isPinned(a) {
		t.Fatalf("A reopened at %d, pinned=%v", g.indexOf(a), g.isPinned(a))
	}
}

func TestTabMoveAcrossPinnedBoundary(t *testing.T) {
	g := testTabGroup()
	a := g.open(requestTab("a", "A"), tabOpen{})
	b := g.open(requestTab("b", "B"), tabOpen{})
	c := g.open(requestTab("c", "C"), tabOpen{})
	d := g.open(requestTab("d", "D"), tabOpen{})
	if !g.pin(a) || !g.pin(b) {
		t.Fatal("could not pin A and B")
	}
	wantTitles(t, g, "Live", "A", "B", "C", "D")
	wantPinnedPrefix(t, g)

	// An unpinned tab dragged into the pinned range joins it.
	if !g.move(d, 1) {
		t.Fatal("could not move D to the pinned range")
	}
	wantTitles(t, g, "Live", "D", "A", "B", "C")
	if !g.isPinned(d) {
		t.Fatal("D stayed unpinned inside the pinned range")
	}
	wantPinnedPrefix(t, g)

	// Dragging it past the last pinned tab unpins it.
	if !g.move(d, 4) {
		t.Fatal("could not move D out of the pinned range")
	}
	wantTitles(t, g, "Live", "A", "B", "C", "D")
	if g.isPinned(d) {
		t.Fatal("D stayed pinned after leaving the pinned range")
	}
	wantPinnedPrefix(t, g)

	// An unpinned tab can land between pinned tabs.
	if !g.move(c, 2) {
		t.Fatal("could not move C between pinned tabs")
	}
	wantTitles(t, g, "Live", "A", "C", "B", "D")
	if !g.isPinned(c) {
		t.Fatal("C stayed unpinned between pinned tabs")
	}
	wantPinnedPrefix(t, g)

	// A pinned tab dragged below the boundary leaves the pinned range.
	if !g.move(a, 4) {
		t.Fatal("could not move A out of the pinned range")
	}
	wantTitles(t, g, "Live", "C", "B", "D", "A")
	if g.isPinned(a) {
		t.Fatal("A stayed pinned after leaving the pinned range")
	}
	wantPinnedPrefix(t, g)

	// Live cannot move, and no other tab can be placed before it.
	if g.move(liveTabID(g), 3) {
		t.Fatal("Live moved")
	}
	if g.indexOf(liveTabID(g)) != 0 {
		t.Fatal("Live left index 0")
	}
	if !g.move(d, 0) {
		t.Fatal("could not move D to index 0")
	}
	wantTitles(t, g, "Live", "D", "C", "B", "A")
	if g.indexOf(d) != 1 || !g.isPinned(d) {
		t.Fatalf("D at %d, pinned=%v, want 1 and pinned", g.indexOf(d), g.isPinned(d))
	}
	wantPinnedPrefix(t, g)
}

func TestTabDirtyCloseNeedsConfirmation(t *testing.T) {
	g := testTabGroup()
	a := g.open(requestTab("a", "A"), tabOpen{})
	if !g.setDirty(a, true) {
		t.Fatal("could not mark A dirty")
	}
	r := g.close(a)
	if len(r.needsSave) != 1 || len(r.closed) != 0 || g.indexOf(a) < 0 {
		t.Fatalf("dirty close = %+v, still open at %d", r, g.indexOf(a))
	}
	if g.active() != a {
		t.Fatalf("active = %v, want A", g.active())
	}

	if r := g.forceClose(a); len(r.closed) != 1 || g.indexOf(a) != -1 {
		t.Fatalf("force close = %+v, index %d", r, g.indexOf(a))
	}
	if !g.reopen() {
		t.Fatal("nothing to reopen")
	}
	wantTitles(t, g, "Live", "A")
	if !g.tabs()[1].dirty {
		t.Fatal("the reopened tab lost its dirty flag")
	}
	if g.active() != a {
		t.Fatalf("active = %v, want A", g.active())
	}
}

func TestTabReopenStackIsBounded(t *testing.T) {
	g := testTabGroup()
	var ids []tabID
	for i := range 25 {
		ids = append(ids, g.open(requestTab(fmt.Sprintf("r%d", i), fmt.Sprintf("R%d", i)), tabOpen{}))
	}
	for _, id := range ids {
		if r := g.close(id); len(r.closed) != 1 {
			t.Fatalf("close %v = %+v", id, r)
		}
	}
	wantTitles(t, g, "Live")
	for range tabReopenLimit {
		if !g.reopen() {
			t.Fatal("ran out of reopen entries early")
		}
	}
	if g.canReopen() {
		t.Fatal("the reopen stack kept more than its bound")
	}
	if got := g.len(); got != tabReopenLimit+1 {
		t.Fatalf("%d tabs reopened, want %d", got-1, tabReopenLimit)
	}
	for i := range 5 {
		old := tabID{kind: pageRequest, key: fmt.Sprintf("r%d", i)}
		if g.indexOf(old) != -1 {
			t.Fatalf("old tab %d was not dropped", i)
		}
	}
}
