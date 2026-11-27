package app

import (
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// closedDocument is the document copy a closed request tab needs to reopen.
// The tab model owns the reopen order and position; this is only the payload
// its descriptor cannot carry.
type closedDocument struct {
	draft    savedRequest
	savedKey string
	baseline savedRequest
	doc      *requestDocument
}

// detachedCopy keeps a closed tab's fields without its running sessions:
// closeDocument has already stopped those, and the copy owns a new one if it
// is opened again.
func (d *requestDocument) detachedCopy() *requestDocument {
	snapshot := *d
	snapshot.composeWaiting, snapshot.sendCancelling = false, false
	snapshot.sendCancel = nil
	snapshot.structured = structuredInspector{}
	snapshot.grpcUI, snapshot.graphqlUI = nil, nil
	snapshot.socketIOUI, snapshot.mqttUI, snapshot.mcpUI = nil, nil, nil
	return &snapshot
}

// rememberClosedDocument stores the copy for a tab the model has just put on
// its reopen stack. Closing the same identity again replaces the older copy.
func (a *app) rememberClosedDocument(id tabID, d *requestDocument) {
	if d == nil {
		return
	}
	a.ensureTabs()
	a.closedDocs[id] = closedDocument{
		draft: d.value(), savedKey: d.savedKey, baseline: d.baseline,
		doc: d.detachedCopy(),
	}
	a.pruneClosedDocs()
}

// pruneClosedDocs drops copies whose model entry has fallen off the reopen
// bound. The model is the one list; this map only follows it.
func (a *app) pruneClosedDocs() {
	keep := make(map[tabID]bool)
	for _, id := range a.tabsGroup().closedIDs() {
		keep[id] = true
	}
	for id := range a.closedDocs {
		if !keep[id] {
			delete(a.closedDocs, id)
		}
	}
}

func (a *app) closedCount() int { return len(a.tabsGroup().closedIDs()) }

// savedSourceStillThere reports whether the saved request a closed draft came
// from is still under the same name and collection. A deleted or renamed
// asset leaves the draft in memory but no longer gives it a key to save to.
func (a *app) savedSourceStillThere(entry closedDocument) bool {
	return slices.ContainsFunc(a.requests, func(r savedRequest) bool {
		return r.key() == entry.savedKey && r.Name == entry.baseline.Name &&
			r.Collection == entry.baseline.Collection
	})
}

// reopenClosedDocument restores the model's last closed tab.
func (a *app) reopenClosedDocument() bool {
	next, ok := a.tabsGroup().nextClosed()
	if !ok {
		return false
	}
	return a.reopenClosedDocumentID(next.tab.id())
}

// reopenClosedDocumentID restores one closed tab and, for a request tab, its
// document copy. The model restores the tab clean; the document says whether
// it is still dirty, and setDirty records that.
func (a *app) reopenClosedDocumentID(id tabID) bool {
	g := a.tabsGroup()
	next, ok := g.find(id)
	if !ok {
		// A closed tab is not in the group; its descriptor lives on the
		// reopen stack, which is what the picker names.
		at := slices.IndexFunc(g.closed, func(c closedTab) bool { return c.tab.id() == id })
		if at < 0 {
			return false
		}
		next = tabEntry{tab: g.closed[at].tab}
	}
	entry, has := a.closedDocs[id]
	alreadyOpen := g.indexOf(id) >= 0
	if !g.reopenID(id) {
		return false
	}
	if id.kind == pageSession {
		// A closed session tab released its payload. Reopen the saved
		// session through the normal loader, which marks it open and reads
		// its file without blocking the frame.
		a.selectSession(strings.TrimPrefix(id.key, "session:"))
	}
	if id.kind == pageRequest && !alreadyOpen {
		d := entry.doc
		if !has || d == nil {
			d = a.newDocument(savedRequest{Name: next.tab.title, Method: "GET", URL: "https://"}, "")
		}
		if d != nil {
			if entry.savedKey != "" && !a.savedSourceStillThere(entry) {
				d.savedKey, d.assetID = "", ""
			}
			if !slices.Contains(a.documents, d) {
				a.documents = append(a.documents, d)
			}
			a.tabDocuments[id] = d
			if d.sourceFlow != 0 {
				if a.flowDrafts == nil {
					a.flowDrafts = map[int]*requestDocument{}
				}
				a.flowDrafts[d.sourceFlow] = d
			}
			syncRequestTab(g, d)
		}
	}
	delete(a.closedDocs, id)
	a.pruneClosedDocs()
	a.syncDocumentOrder()
	a.applyActiveTab()
	return true
}

// applyCloseResult takes the documents out of a closed tab, queues the dirty
// ones the model left open for confirmation, and follows the new active tab.
func (a *app) applyCloseResult(r tabCloseResult) {
	for _, t := range r.closed {
		a.cleanupClosedTab(t)
	}
	for _, t := range r.needsSave {
		if d := a.documentForTab(t.id()); d != nil {
			a.enqueueDocumentClose(d)
		}
	}
	a.pruneClosedDocs()
	a.syncDocumentOrder()
	a.applyActiveTab()
}

func (a *app) cleanupClosedTab(t tab) {
	id := t.id()
	switch id.kind {
	case pageRequest:
		if d := a.documentForTab(id); d != nil {
			a.rememberClosedDocument(id, d)
			a.forgetDocument(d)
		}
		delete(a.tabDocuments, id)
	case pageSession:
		a.sessions.closeTab(strings.TrimPrefix(id.key, "session:"))
	}
}

// closeTab closes one tab through the model. A dirty request tab is left open
// for the Save changes? prompt unless force says the reader already decided.
func (a *app) closeTab(id tabID, force bool) bool {
	a.syncTabs()
	g := a.tabsGroup()
	if _, ok := g.find(id); !ok {
		return false
	}
	if !force && id.kind == pageRequest {
		if d := a.documentForTab(id); d != nil && d.composeWaiting {
			return false
		}
	}
	var r tabCloseResult
	if force {
		r = g.forceClose(id)
	} else {
		r = g.close(id)
	}
	if len(r.closed) == 0 && len(r.needsSave) == 0 && len(r.refused) == 0 {
		return false
	}
	a.applyCloseResult(r)
	a.nextDocumentClose()
	return len(r.closed) > 0
}

// closeActiveTab closes whichever tab is active. LIVE is fixed and refuses;
// a request tab asks before discarding edits, and a session or page tab
// closes through the same model path.
func (a *app) closeActiveTab() {
	id := a.tabsGroup().active()
	if id.kind == pageRequest {
		if d := a.documentForTab(id); d != nil {
			a.requestClose(d)
			return
		}
	}
	a.closeTab(id, false)
}

func (a *app) requestClose(d *requestDocument) {
	if d == nil {
		return
	}
	a.syncTabs()
	a.closeTab(requestTabID(d), false)
}

// closeOtherDocuments keeps the tab it was opened for, even when that tab is
// dirty. Every other dirty tab still gets the Save changes? prompt; pinned
// tabs and Live are not candidates.
func (a *app) closeOtherDocuments(keep *requestDocument) {
	if keep == nil {
		return
	}
	a.syncTabs()
	g := a.tabsGroup()
	keepID := requestTabID(keep)
	if g.indexOf(keepID) < 0 {
		return
	}
	a.closingReturnTo = keep
	r := g.closeOthers(keepID)
	a.applyCloseResult(r)
	a.nextDocumentClose()
}

// closeOtherTabs keeps one tab of any kind, for the strip's own menu. The
// close path is the same as Close Others for documents: clean tabs close now
// and dirty requests queue the Save changes? prompt.
func (a *app) closeOtherTabs(keep tabID) {
	a.syncTabs()
	g := a.tabsGroup()
	if g.indexOf(keep) < 0 {
		return
	}
	a.closingReturnTo = a.documentForTab(keep)
	a.applyCloseResult(g.closeOthers(keep))
	a.nextDocumentClose()
}

// closeTabsToRight closes every closable tab after of, leaving the tab the
// menu opened on in place when it was active.
func (a *app) closeTabsToRight(of tabID) {
	a.syncTabs()
	g := a.tabsGroup()
	if g.indexOf(of) < 0 {
		return
	}
	a.closingReturnTo = a.documentForTab(of)
	a.applyCloseResult(g.closeToRight(of))
	a.nextDocumentClose()
}

// closeSavedDocuments closes saved tabs with no edits. Unsaved drafts and
// dirty requests stay open, as Close Saved Requests always has.
func (a *app) closeSavedDocuments() {
	a.syncTabs()
	r := a.tabsGroup().closeSaved()
	a.applyCloseResult(r)
	a.nextDocumentClose()
}

func (a *app) closeAllDocuments() {
	a.syncTabs()
	r := a.tabsGroup().closeAll()
	a.applyCloseResult(r)
	a.nextDocumentClose()
}

func (a *app) enqueueDocumentClose(d *requestDocument) {
	if d == nil || d.composeWaiting || !slices.Contains(a.documents, d) {
		return
	}
	if !slices.Contains(a.closingQueue, d) && d != a.closingDocument {
		a.closingQueue = append(a.closingQueue, d)
	}
}

func (a *app) queueDocumentCloses(documents ...*requestDocument) {
	for _, d := range documents {
		if d == nil || d.composeWaiting || !slices.Contains(a.documents, d) {
			continue
		}
		if d.dirty() {
			a.enqueueDocumentClose(d)
			continue
		}
		a.closeDocument(d)
	}
	a.nextDocumentClose()
}

// nextDocumentClose shows the next dirty tab's prompt, or puts the focus back
// on the tab close-others was opened for once there are no prompts left.
func (a *app) nextDocumentClose() {
	if a.closingDocument != nil {
		return
	}
	for len(a.closingQueue) > 0 {
		d := a.closingQueue[0]
		a.closingQueue = a.closingQueue[1:]
		if !slices.Contains(a.documents, d) {
			continue
		}
		a.closingDocument = d
		a.activateDocument(d)
		return
	}
	if d := a.closingReturnTo; slices.Contains(a.documents, d) {
		a.activateDocument(d)
	}
	a.closingReturnTo = nil
}

// finishDocumentClose moves on after one prompt's draft was closed, saved or
// discarded. Cancel ends the whole close, not just this tab.
func (a *app) finishDocumentClose() {
	a.closingDocument = nil
	a.nextDocumentClose()
}

func (a *app) cancelDocumentCloses() {
	a.closingDocument, a.closingQueue, a.closingReturnTo = nil, nil, nil
}

func (a *app) closeRequestModal(c *ui.Context) {
	d := a.closingDocument
	if d == nil {
		return
	}
	open := true
	switch uikit.ConfirmDialog(c, &open, uikit.ConfirmOptions{
		Title:     locales.DraftUnsavedTitle(d.title()),
		Message:   locales.DraftUnsavedDetail(),
		Severity:  uikit.SeverityWarning,
		Primary:   locales.Save(),
		Secondary: locales.DontSave(),
		Cancel:    locales.Cancel(),
	}) {
	case uikit.ConfirmCancel:
		// Cancel ends the whole close, not just this tab.
		a.cancelDocumentCloses()
	case uikit.ConfirmSecondary:
		a.closeDocument(d)
		a.finishDocumentClose()
	case uikit.ConfirmPrimary:
		a.activateDocument(d)
		if a.saveComposed() {
			a.closeDocument(d)
			a.finishDocumentClose()
		}
		// A failed save leaves the question up so the reader can retry or
		// cancel, rather than dropping the prompt with the tab unsaved.
	}
}

// commitMRUPick makes the quick switch's current tab the most recently used
// one. VS Code commits on Ctrl release; the observer settles it on any other
// input, and every other command settles it before running.
func (a *app) commitMRUPick() {
	if !a.mruPending {
		return
	}
	a.mruPending = false
	g := a.tabsGroup()
	g.activate(g.active())
}

// observeMRUInput sees input anywhere in the main column, before the focused
// element does. Ctrl+Tab presses and releases are the one chord that must
// keep walking the order; anything else commits the pick, including Ctrl's
// own release.
func (a *app) observeMRUInput(ev ui.InputEvent) {
	if !a.mruPending {
		return
	}
	if ev.Mods&ui.Ctrl != 0 && (ev.Key == ui.KeyTab || ev.Key == ui.KeyUnknown) {
		return
	}
	if ev.Kind == ui.InputKeyUp && ev.Key == ui.KeyTab {
		return
	}
	a.commitMRUPick()
}

// activateDocument records an explicit visit to a request tab, or to LIVE
// when d is nil; cycling through the MRU list does not, so repeated Ctrl+Tab
// keeps walking the same order.
func (a *app) activateDocument(d *requestDocument) {
	a.commitMRUPick()
	if d == nil {
		a.activateTab(tabID{kind: pageLive, key: liveTabKey})
		return
	}
	a.openRequestTab(d, tabOpen{})
}

// cycleDocumentMRU is Ctrl/Cmd+Tab's quick switch: next and previous walk
// the recency order rather than the positions of the tabs in the strip.
func (a *app) cycleDocumentMRU(step int) {
	g := a.tabsGroup()
	order := g.mru()
	if len(order) == 0 {
		return
	}
	at := slices.Index(order, g.active())
	if at < 0 {
		at = 0
	}
	next := order[(at+step+len(order))%len(order)]
	a.showTab(next)
	a.mruPending = true
}
