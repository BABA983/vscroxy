package app

import (
	"slices"

	"github.com/baba983/vscroxy/internal/linkedmap"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// VS Code keeps at most 20 recently closed editors for Reopen Closed Editor
// (historyService.ts). The same bound applies here, in memory only.
const recentlyClosedLimit = 20

// closedDocument is one tab the reader closed. draft is the complete editable
// value, including a discarded draft; doc keeps the rest of that tab, such as
// its response and pane choices. The key and baseline decide whether the
// draft still belongs to a saved request when it is reopened.
type closedDocument struct {
	draft    savedRequest
	savedKey string
	baseline savedRequest
	position int
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

func (a *app) closedDocuments() *linkedmap.LinkedMap[int, closedDocument] {
	if a.recentlyClosed == nil {
		a.recentlyClosed = linkedmap.NewLinkedMap[int, closedDocument]()
	}
	return a.recentlyClosed
}

// rememberClosedDocument is called just before a tab leaves the strip. A tab
// closed again after being reopened replaces its earlier entry, and the
// oldest entry falls off once the bound is reached.
func (a *app) rememberClosedDocument(d *requestDocument) {
	at := slices.Index(a.documents, d)
	if at < 0 {
		return
	}
	stack := a.closedDocuments()
	entry := closedDocument{
		draft: d.value(), savedKey: d.savedKey, baseline: d.baseline,
		position: at, doc: d.detachedCopy(),
	}
	stack.Delete(d.id)
	stack.Set(d.id, entry)
	for stack.Size() > recentlyClosedLimit {
		stack.Shift()
	}
}

// savedSourceStillThere reports whether the saved request a closed draft came
// from is still under the same name and collection. A deleted or renamed
// asset leaves the draft in memory but no longer gives it a key to save to.
func (a *app) savedSourceStillThere(entry closedDocument) bool {
	return slices.ContainsFunc(a.requests, func(r savedRequest) bool {
		return r.key() == entry.savedKey && r.Name == entry.baseline.Name &&
			r.Collection == entry.baseline.Collection
	})
}

// reopenClosedDocument pops the most recently closed tab back into its old
// place. It only changes open tabs: nothing is written to the library.
func (a *app) reopenClosedDocument() bool {
	stack := a.closedDocuments()
	key, ok := lastLinkedKey(stack)
	if !ok {
		return false
	}
	entry, _ := stack.Remove(key)
	d := entry.doc
	if entry.savedKey != "" && !a.savedSourceStillThere(entry) {
		d.savedKey, d.assetID = "", ""
	}
	at := min(entry.position, len(a.documents))
	a.documents = slices.Insert(a.documents, at, d)
	a.activateDocument(d)
	return true
}

func lastLinkedKey[K comparable, V any](m *linkedmap.LinkedMap[K, V]) (K, bool) {
	var last K
	found := false
	for key := range m.Keys() {
		last, found = key, true
	}
	return last, found
}

func (a *app) requestClose(d *requestDocument) {
	a.queueDocumentCloses(d)
}

// closeOtherDocuments keeps the tab it was opened for, even when that tab is
// dirty. Every other dirty tab still gets the Save changes? prompt.
func (a *app) closeOtherDocuments(keep *requestDocument) {
	if !slices.Contains(a.documents, keep) {
		return
	}
	others := slices.DeleteFunc(slices.Clone(a.documents), func(d *requestDocument) bool { return d == keep })
	a.closingReturnTo = keep
	a.queueDocumentCloses(others...)
}

// closeSavedDocuments closes only saved tabs with no edits. Unsaved drafts
// and dirty requests stay open.
func (a *app) closeSavedDocuments() {
	a.queueDocumentCloses(slices.DeleteFunc(slices.Clone(a.documents), func(d *requestDocument) bool {
		return d.savedKey == "" || d.dirty()
	})...)
}

func (a *app) queueDocumentCloses(documents ...*requestDocument) {
	for _, d := range documents {
		if d == nil || d.composeWaiting || !slices.Contains(a.documents, d) {
			continue
		}
		if d.dirty() {
			if !slices.Contains(a.closingQueue, d) && d != a.closingDocument {
				a.closingQueue = append(a.closingQueue, d)
			}
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
		a.requestDocument, a.requestChoice, a.page = d, d.savedKey, pageRequest
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

func (a *app) closeRequestModal(c *ui.Context, t *ui.Theme) {
	d := a.closingDocument
	if d == nil {
		return
	}
	open := true
	uikit.Modal(c, &open, locales.SaveChanges(), func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, locales.SaveChanges())
			uikit.Hint(c, locales.DraftUnsavedChanges(d.title()))
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					a.cancelDocumentCloses()
				}
				if uikit.SecondaryButton(c, locales.Discard()).Clicked() {
					a.closeDocument(d)
					a.finishDocumentClose()
				}
				if uikit.PrimaryButton(c, locales.SaveClose()).Clicked() {
					a.requestDocument = d
					if a.saveComposed() {
						a.closeDocument(d)
						a.finishDocumentClose()
					}
				}
			})
		})
	})
	// The next prompt may have replaced this one in the same pass.
	if !open && a.closingDocument == d {
		a.cancelDocumentCloses()
	}
}

// documentMRU is an in-memory recency order; nil is the LIVE traffic view,
// which the quick switch visits alongside request tabs.
func (a *app) documentMRU() *linkedmap.LinkedMap[*requestDocument, struct{}] {
	if a.documentRecency == nil {
		a.documentRecency = linkedmap.NewLinkedMap[*requestDocument, struct{}]()
	}
	return a.documentRecency
}

// touchDocument makes d the most recently used tab, like VS Code's editor
// group marking an editor active.
func (a *app) touchDocument(d *requestDocument) {
	a.documentMRU().Set(d, struct{}{}, linkedmap.TouchAsNew)
}

// showDocument selects d as the open tab without changing recency: the quick
// switch keeps its order while the reader steps through it.
func (a *app) showDocument(d *requestDocument) {
	if d == nil {
		a.page, a.requestDocument, a.requestChoice = pageLive, nil, ""
		if view := a.views.activeView(); view != nil {
			view.list.Handle.Focus()
		}
		return
	}
	a.requestDocument, a.requestChoice, a.page = d, d.savedKey, pageRequest
	d.urlInput.Focus()
}

// commitMRUPick makes the quick switch's current tab the most recently used
// one. VS Code commits on Ctrl release; the observer settles it on any other
// input, and every other command settles it before running.
func (a *app) commitMRUPick() {
	if !a.mruPending {
		return
	}
	a.mruPending = false
	if d := a.requestDocument; d != nil && slices.Contains(a.documents, d) {
		a.touchDocument(d)
	}
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

// activateDocument records an explicit visit to a tab, or to LIVE when d is
// nil; cycling through the MRU list does not, so repeated Ctrl+Tab keeps
// walking the same order.
func (a *app) activateDocument(d *requestDocument) {
	a.commitMRUPick()
	a.showDocument(d)
	a.touchDocument(d)
}

// mruOrder lists the quick switch's targets, most recently used first. Tabs
// opened before recency was recorded come after the live view in opening
// order; tabs closed meanwhile are dropped.
func (a *app) mruOrder() []*requestDocument {
	m := a.documentMRU()
	if !m.Has(nil) {
		m.Set(nil, struct{}{}, linkedmap.TouchAsOld)
	}
	for _, key := range slices.Collect(m.Keys()) {
		if key != nil && !slices.Contains(a.documents, key) {
			m.Delete(key)
		}
	}
	var untracked []*requestDocument
	for _, d := range a.documents {
		if !m.Has(d) {
			untracked = append(untracked, d)
		}
	}
	for i := len(untracked) - 1; i >= 0; i-- {
		m.Set(untracked[i], struct{}{}, linkedmap.TouchAsOld)
	}
	if len(untracked) > 0 {
		m.Set(nil, struct{}{}, linkedmap.TouchAsOld)
	}
	oldestFirst := slices.Collect(m.Keys())
	order := make([]*requestDocument, len(oldestFirst))
	for i, d := range oldestFirst {
		order[len(order)-1-i] = d
	}
	return order
}

// cycleDocumentMRU is Ctrl/Cmd+Tab's quick switch: next and previous walk
// the recency order rather than the positions of the tabs in the strip.
func (a *app) cycleDocumentMRU(step int) {
	order := a.mruOrder()
	if len(order) == 0 {
		return
	}
	current := a.requestDocument
	// LIVE's inspected capture is a document too, but it is not a tab; the
	// nil entry stands for the whole LIVE view, including its draft.
	if current != nil && !slices.Contains(a.documents, current) {
		current = nil
	}
	// LIVE is an ordinary entry: when it is most recent, forward stepping
	// must move past it to the newest request tab rather than stay put.
	at := slices.Index(order, current)
	if at < 0 {
		order = append([]*requestDocument{current}, order...)
		at = 0
	}
	next := order[(at+step+len(order))%len(order)]
	a.showDocument(next)
	a.mruPending = next != nil
}
