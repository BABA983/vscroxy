package app

import "slices"

// This file is VS Code's EditorGroupModel reduced to what vscroxy's tab strip
// needs. It owns descriptors only: a requestDocument, a session, an API spec
// or a capture stays where the app keeps documents. It imports no MyGo, so
// the model is tested without a window.
//
// The tab kinds are the app's pages. Live is the fixed first tab; the rest
// are opened from the sidebar and the library.

// tabKind is the page a tab shows. Aliasing page keeps the strip's kinds and
// the content area's pages the same list instead of two that can drift.
type tabKind = page

// liveTabKey is Live's identity. A non-empty key leaves the zero tabID free
// to mean "no tab".
const liveTabKey = "live"

// tab is one tab's descriptor: what it shows, which document it names, its
// label and whether that document has unsaved changes. The key is opaque to
// the model; the app uses a saved request's key, a flow ID, a session file or
// an unsaved document's ID, and keeps keys unique within a kind.
type tab struct {
	kind  tabKind
	key   string
	title string
	dirty bool
}

func (t tab) id() tabID { return tabID{kind: t.kind, key: t.key} }

// tabID identifies a tab across open, close, move and reopen calls.
type tabID struct {
	kind tabKind
	key  string
}

// tabEntry is a tab's place in the strip. Pinned entries stay before
// unpinned ones, fixed is Live at index 0, and at most one entry is preview.
type tabEntry struct {
	tab
	pinned  bool
	fixed   bool
	preview bool
}

// tabOpen says how to open a tab. The zero value opens a normal tab right of
// the active one. at, when set, asks for a visual index; the model keeps a
// pinned tab inside the pinned range and an unpinned one after it.
type tabOpen struct {
	preview bool
	pinned  bool
	at      *int
}

// tabCloseResult reports what a close request did. needsSave holds dirty tabs
// the model left open; the caller asks about them and calls forceClose for
// each one the reader discards. refused holds fixed tabs, which never close.
type tabCloseResult struct {
	closed    []tab
	needsSave []tab
	refused   []tab
}

// tabReopenLimit is VS Code's bound on Reopen Closed Editor.
const tabReopenLimit = 20

// closedTab is one entry on the reopen stack: where the tab sat and enough
// state to put it back. A dirty tab stays dirty, so reopening a discarded
// draft restores its edits.
type closedTab struct {
	tab    tab
	index  int
	pinned bool
}

// tabGroup is one editor group. Live is created fixed at index 0 and is the
// only tab that always exists.
type tabGroup struct {
	entries  []tabEntry
	recent   []tabID // most recently used first; the active tab is first
	activeID tabID
	closed   []closedTab // oldest first
}

func newTabGroup(liveTitle string) *tabGroup {
	live := tabEntry{kind: pageLive, key: liveTabKey, title: liveTitle, fixed: true}
	return &tabGroup{
		entries:  []tabEntry{live},
		recent:   []tabID{live.id()},
		activeID: live.id(),
	}
}

// tabState is the window's tabs: a list of groups so splits can be added
// later without changing the shape. One group is open for now.
type tabState struct {
	groups []*tabGroup
	active int
}

func newTabState(liveTitle string) *tabState {
	return &tabState{groups: []*tabGroup{newTabGroup(liveTitle)}}
}

// group is the active group.
func (s *tabState) group() *tabGroup { return s.groups[s.active] }

// len is the number of open tabs, including Live.
func (g *tabGroup) len() int { return len(g.entries) }

// tabs is a snapshot in visual order.
func (g *tabGroup) tabs() []tabEntry { return slices.Clone(g.entries) }

// active is the tab the content area shows.
func (g *tabGroup) active() tabID { return g.activeID }

// mru is the recency order, most recently used first.
func (g *tabGroup) mru() []tabID { return slices.Clone(g.recent) }

// preview is the preview tab, or the zero tabID when there is none.
func (g *tabGroup) preview() tabID {
	if at := g.previewIndex(); at >= 0 {
		return g.entries[at].id()
	}
	return tabID{}
}

// indexOf is the tab's visual position, or -1.
func (g *tabGroup) indexOf(id tabID) int {
	return slices.IndexFunc(g.entries, func(e tabEntry) bool { return e.id() == id })
}

func (g *tabGroup) find(id tabID) (tabEntry, bool) {
	at := g.indexOf(id)
	if at < 0 {
		return tabEntry{}, false
	}
	return g.entries[at], true
}

// isPinned reports whether the tab is pinned and therefore skipped by the
// bulk closes.
func (g *tabGroup) isPinned(id tabID) bool {
	e, ok := g.find(id)
	return ok && e.pinned
}

// isFixed reports whether the tab is Live, which cannot close or move.
func (g *tabGroup) isFixed(id tabID) bool {
	e, ok := g.find(id)
	return ok && e.fixed
}

// isPreview reports whether the tab is the one replaceable preview.
func (g *tabGroup) isPreview(id tabID) bool {
	e, ok := g.find(id)
	return ok && e.preview
}

// activate shows an open tab and makes it the most recently used one.
func (g *tabGroup) activate(id tabID) bool {
	if g.indexOf(id) < 0 {
		return false
	}
	g.activeID = id
	g.touch(id)
	return true
}

func (g *tabGroup) touch(id tabID) {
	g.recent = slices.DeleteFunc(g.recent, func(x tabID) bool { return x == id })
	g.recent = slices.Insert(g.recent, 0, id)
}

// setTitle relabels an open tab. Opening an existing tab again does not
// rewrite its title, since a draft may have been renamed after it opened.
func (g *tabGroup) setTitle(id tabID, title string) bool {
	at := g.indexOf(id)
	if at < 0 {
		return false
	}
	g.entries[at].title = title
	return true
}

// setDirty records unsaved changes. A dirty preview becomes a normal tab in
// place, so it can no longer be replaced by the next preview.
func (g *tabGroup) setDirty(id tabID, dirty bool) bool {
	at := g.indexOf(id)
	if at < 0 {
		return false
	}
	g.entries[at].dirty = dirty
	if dirty {
		g.entries[at].preview = false
	}
	return true
}

// open shows a tab and makes it active. An existing tab with the same kind
// and key is activated instead of duplicated. A new tab goes right of the
// active one, except that a pinned tab stays in the pinned range and an
// unpinned one stays after it. Opening as preview reuses the preview slot:
// the old preview is replaced in place and is not put on the reopen stack.
func (g *tabGroup) open(t tab, opts tabOpen) tabID {
	id := t.id()
	if at := g.indexOf(id); at >= 0 {
		if !opts.preview {
			g.entries[at].preview = false
		}
		if opts.pinned {
			g.pin(id)
		}
		if opts.at != nil {
			g.move(id, *opts.at)
		}
		g.activate(id)
		return id
	}

	preview := opts.preview && !opts.pinned && !t.dirty
	if preview {
		if at := g.previewIndex(); at >= 0 {
			g.removeAt(at)
			g.entries = slices.Insert(g.entries, at, tabEntry{
				kind: t.kind, key: t.key, title: t.title, preview: true,
			})
			g.activate(id)
			return id
		}
	}

	at := g.insertIndex(opts.at, opts.pinned)
	g.entries = slices.Insert(g.entries, at, tabEntry{
		kind: t.kind, key: t.key, title: t.title, dirty: t.dirty,
		pinned: opts.pinned, preview: preview,
	})
	g.activate(id)
	return id
}

// insertIndex is where a new tab goes: after the active one by default, or
// at the requested index. Pinned tabs stay at the front and unpinned tabs
// stay behind them, as VS Code keeps sticky editors before the rest.
func (g *tabGroup) insertIndex(at *int, pinned bool) int {
	fixed, end := g.fixedCount(), g.pinnedEnd()
	active := g.indexOf(g.activeID)
	var idx int
	switch {
	case at != nil:
		idx = min(max(*at, fixed), len(g.entries))
	case active >= 0:
		idx = active + 1
	default:
		idx = len(g.entries)
	}
	if pinned {
		return min(max(idx, fixed), end)
	}
	return min(max(idx, end), len(g.entries))
}

// close removes a clean tab. A dirty tab is reported in needsSave and left
// open; the caller confirms and then calls forceClose. Live is reported in
// refused and never closes.
func (g *tabGroup) close(id tabID) tabCloseResult {
	e, ok := g.find(id)
	if !ok {
		return tabCloseResult{}
	}
	if e.fixed {
		return tabCloseResult{refused: []tab{e.tab}}
	}
	if e.dirty {
		return tabCloseResult{needsSave: []tab{e.tab}}
	}
	return g.removeTabs([]tabID{id}, tabID{})
}

// forceClose closes a tab after the caller has settled its unsaved changes.
// Fixed tabs still refuse.
func (g *tabGroup) forceClose(id tabID) tabCloseResult {
	e, ok := g.find(id)
	if !ok {
		return tabCloseResult{}
	}
	if e.fixed {
		return tabCloseResult{refused: []tab{e.tab}}
	}
	return g.removeTabs([]tabID{id}, tabID{})
}

// closeOthers closes every unpinned, non-fixed tab except keep. Dirty tabs
// are reported and stay open; pinned and fixed tabs are never candidates.
func (g *tabGroup) closeOthers(keep tabID) tabCloseResult {
	if g.indexOf(keep) < 0 {
		return tabCloseResult{}
	}
	var ids []tabID
	for _, e := range g.entries {
		if e.id() == keep || e.fixed || e.pinned {
			continue
		}
		ids = append(ids, e.id())
	}
	return g.closeCandidates(ids, keep)
}

// closeToRight closes unpinned, non-fixed tabs after of. If the active tab
// closes, of becomes active.
func (g *tabGroup) closeToRight(of tabID) tabCloseResult {
	at := g.indexOf(of)
	if at < 0 {
		return tabCloseResult{}
	}
	var ids []tabID
	for _, e := range g.entries[at+1:] {
		if e.fixed || e.pinned {
			continue
		}
		ids = append(ids, e.id())
	}
	return g.closeCandidates(ids, of)
}

// closeSaved closes every clean unpinned tab, as VS Code's Close Saved
// Editors does. Dirty tabs stay for a save confirmation.
func (g *tabGroup) closeSaved() tabCloseResult {
	var ids []tabID
	for _, e := range g.entries {
		if e.fixed || e.pinned || e.dirty {
			continue
		}
		ids = append(ids, e.id())
	}
	return g.removeTabs(ids, tabID{})
}

// closeAll closes every unpinned, non-fixed tab. Dirty tabs are reported and
// stay open; pinned tabs and Live stay.
func (g *tabGroup) closeAll() tabCloseResult {
	var ids []tabID
	for _, e := range g.entries {
		if e.fixed || e.pinned {
			continue
		}
		ids = append(ids, e.id())
	}
	return g.closeCandidates(ids, tabID{})
}

// closeCandidates partitions the candidates into tabs that can close now and
// dirty tabs the caller must confirm. It never removes a pinned or fixed tab.
func (g *tabGroup) closeCandidates(ids []tabID, fallback tabID) tabCloseResult {
	var clean []tabID
	var result tabCloseResult
	for _, id := range ids {
		e, ok := g.find(id)
		if !ok || e.fixed || e.pinned {
			continue
		}
		if e.dirty {
			result.needsSave = append(result.needsSave, e.tab)
			continue
		}
		clean = append(clean, id)
	}
	if len(clean) == 0 {
		return result
	}
	result.closed = g.removeTabs(clean, fallback).closed
	return result
}

// removeTabs takes ids in visual order and closes them from the right, so
// each remembered index is still the one it had. When the active tab closes,
// fallback takes over, or the most recently used remaining tab if there is
// no fallback.
func (g *tabGroup) removeTabs(ids []tabID, fallback tabID) tabCloseResult {
	if len(ids) == 0 {
		return tabCloseResult{}
	}
	activeClosed := slices.Contains(ids, g.activeID)
	closed := make([]tab, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		at := g.indexOf(ids[i])
		if at < 0 {
			continue
		}
		e := g.removeAt(at)
		g.rememberClosed(e, at)
		closed = append(closed, e.tab)
	}
	slices.Reverse(closed)
	if activeClosed {
		switch {
		case g.indexOf(fallback) >= 0:
			g.activate(fallback)
		case len(g.recent) > 0:
			g.activate(g.recent[0])
		}
	}
	return tabCloseResult{closed: closed}
}

func (g *tabGroup) removeAt(at int) tabEntry {
	e := g.entries[at]
	g.entries = slices.Delete(g.entries, at, at+1)
	g.forget(e.id())
	return e
}

func (g *tabGroup) forget(id tabID) {
	g.recent = slices.DeleteFunc(g.recent, func(x tabID) bool { return x == id })
}

// rememberClosed puts a tab on the reopen stack. Closing the same identity
// again replaces its older entry, and the oldest entry falls off at the
// bound, as VS Code's history service does.
func (g *tabGroup) rememberClosed(e tabEntry, at int) {
	id := e.id()
	g.closed = slices.DeleteFunc(g.closed, func(c closedTab) bool { return c.tab.id() == id })
	g.closed = append(g.closed, closedTab{tab: e.tab, index: at, pinned: e.pinned})
	if len(g.closed) > tabReopenLimit {
		g.closed = g.closed[len(g.closed)-tabReopenLimit:]
	}
}

// canReopen reports whether Reopen Closed Editor has a tab to restore.
func (g *tabGroup) canReopen() bool { return len(g.closed) > 0 }

// reopen restores the most recently closed tab as a normal tab at its old
// index, pinned if it was pinned, and makes it active. A tab already open
// again is only activated. Reopened tabs are never previews.
func (g *tabGroup) reopen() bool {
	if len(g.closed) == 0 {
		return false
	}
	last := len(g.closed) - 1
	c := g.closed[last]
	g.closed = g.closed[:last]
	id := c.tab.id()
	if g.indexOf(id) >= 0 {
		g.activate(id)
		return true
	}
	at := min(max(c.index, g.fixedCount()), len(g.entries))
	if c.pinned {
		at = min(max(at, g.fixedCount()), g.pinnedEnd())
	} else {
		at = max(at, g.pinnedEnd())
	}
	g.entries = slices.Insert(g.entries, at, tabEntry{
		kind: c.tab.kind, key: c.tab.key, title: c.tab.title, dirty: c.tab.dirty,
		pinned: c.pinned,
	})
	g.activate(id)
	return true
}

// move puts a tab at a visual index. A tab dragged into the pinned range
// becomes pinned; one dragged out becomes unpinned. Live never moves.
func (g *tabGroup) move(id tabID, to int) bool {
	from := g.indexOf(id)
	if from < 0 || g.entries[from].fixed {
		return false
	}
	to = min(max(to, g.fixedCount()), len(g.entries)-1)
	if to == from {
		return false
	}
	pinned := to < g.pinnedEnd()
	e := g.takeAt(from)
	e.pinned = pinned
	if pinned {
		e.preview = false
	}
	g.entries = slices.Insert(g.entries, to, e)
	return true
}

// pin makes a tab permanent: it leaves the preview slot and, when it was
// not already pinned, moves to the end of the pinned range. Pinned tabs are
// skipped by the bulk closes. Live is already fixed.
func (g *tabGroup) pin(id tabID) bool {
	at := g.indexOf(id)
	if at < 0 || g.entries[at].fixed {
		return false
	}
	if g.entries[at].pinned {
		g.entries[at].preview = false
		return true
	}
	e := g.takeAt(at)
	e.pinned, e.preview = true, false
	g.entries = slices.Insert(g.entries, g.pinnedEnd(), e)
	return true
}

// unpin moves a pinned tab to the first unpinned place.
func (g *tabGroup) unpin(id tabID) bool {
	at := g.indexOf(id)
	if at < 0 || g.entries[at].fixed {
		return false
	}
	if !g.entries[at].pinned {
		return true
	}
	e := g.takeAt(at)
	e.pinned = false
	g.entries = slices.Insert(g.entries, g.pinnedEnd(), e)
	return true
}

// takeAt removes an entry without touching the active tab or the recency
// order, for callers that put it back or handle the active tab themselves.
func (g *tabGroup) takeAt(at int) tabEntry {
	e := g.entries[at]
	g.entries = slices.Delete(g.entries, at, at+1)
	return e
}

func (g *tabGroup) previewIndex() int {
	return slices.IndexFunc(g.entries, func(e tabEntry) bool { return e.preview })
}

// fixedCount is the index of the first tab that may move: Live occupies the
// fixed prefix at index 0.
func (g *tabGroup) fixedCount() int {
	if at := slices.IndexFunc(g.entries, func(e tabEntry) bool { return !e.fixed }); at >= 0 {
		return at
	}
	return len(g.entries)
}

// pinnedEnd is the first unpinned index. Pinned tabs are kept contiguous
// after the fixed prefix.
func (g *tabGroup) pinnedEnd() int {
	at := g.fixedCount()
	for at < len(g.entries) && g.entries[at].pinned {
		at++
	}
	return at
}
