package app

import (
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/locales"
)

// The app's side of the tab strip. The model in tabs.go knows order, preview,
// pinned state, MRU and the reopen order; this file maps its descriptors to
// the app's pages and documents, and keeps the old single-document fields in
// step with whichever tab is active.

// ensureTabs builds the strip on first use and returns its one group.
func (a *app) ensureTabs() *tabGroup {
	if a.tabs == nil {
		a.tabs = newTabState(locales.Live())
	}
	if a.tabDocuments == nil {
		a.tabDocuments = make(map[tabID]*requestDocument)
	}
	if a.closedDocs == nil {
		a.closedDocs = make(map[tabID]closedDocument)
	}
	return a.tabs.group()
}

func (a *app) tabsGroup() *tabGroup { return a.ensureTabs() }

// requestTabID is a request document's stable tab identity. It is the
// document's own ID, so saving, renaming or duplicating a request never moves
// or merges its tab.
func requestTabID(d *requestDocument) tabID {
	return tabID{kind: pageRequest, key: "draft:" + strconv.Itoa(d.id)}
}

// documentForTab finds the document a request tab names.
func (a *app) documentForTab(id tabID) *requestDocument {
	if id.kind != pageRequest {
		return nil
	}
	if d := a.tabDocuments[id]; d != nil {
		return d
	}
	for _, d := range a.documents {
		if requestTabID(d) == id {
			a.tabDocuments[id] = d
			return d
		}
	}
	return nil
}

// requestTab is a document's descriptor as the strip shows it.
func requestTabDescriptor(d *requestDocument) tab {
	return tab{
		kind: pageRequest, key: requestTabID(d).key, title: d.title(),
		dirty: d.dirty(), saved: d.savedKey != "",
	}
}

// syncRequestTab writes the document's current label and flags into its tab.
func syncRequestTab(g *tabGroup, d *requestDocument) {
	id := requestTabID(d)
	g.setTitle(id, d.title())
	g.setDirty(id, d.dirty())
	g.setSaved(id, d.savedKey != "")
	// A send keeps the tab, so opening the next preview does not discard
	// its response. A send can finish before the next frame, so a result
	// counts as well as one in flight.
	if d.composeWaiting || d.composeResult != "" {
		g.keep(id)
	}
}

// syncDocumentOrder keeps documents in the strip's visual order. The tab is
// the source of order; documents is the registry the old screens read.
func (a *app) syncDocumentOrder() {
	g := a.tabsGroup()
	ordered := make([]*requestDocument, 0, len(a.documents))
	for _, e := range g.tabs() {
		if e.kind != pageRequest {
			continue
		}
		if d := a.documentForTab(e.id()); d != nil {
			ordered = append(ordered, d)
		}
	}
	for _, d := range a.documents {
		if !slices.Contains(ordered, d) {
			ordered = append(ordered, d)
		}
	}
	a.documents = ordered
}

// openRequestTab adds d to the open documents and opens or activates its tab.
func (a *app) openRequestTab(d *requestDocument, opts tabOpen) tabID {
	if d == nil {
		return tabID{}
	}
	// Leaving the current tab commits an inline name edit before the
	// breadcrumb that owns it disappears.
	if a.titleEdit.id != 0 && (a.requestDocument == nil || a.requestDocument.id != d.id) {
		a.commitRequestRename()
	}
	if !slices.Contains(a.documents, d) {
		a.documents = append(a.documents, d)
	}
	g := a.tabsGroup()
	// A request page with no document holds a placeholder tab. Opening a
	// real request replaces it rather than leaving a second, empty tab.
	placeholder := tabID{kind: pageRequest, key: "draft:0"}
	if g.indexOf(placeholder) >= 0 && a.documentForTab(placeholder) == nil {
		g.drop(placeholder)
	}
	id := requestTabID(d)
	// A preview replaces the one before it in place. The old preview is not
	// a closed tab: forget its document so the next frame does not adopt it
	// again as a background tab.
	// An already open tab is only activated, so the preview stays.
	if opts.preview && g.indexOf(id) < 0 {
		if old := g.preview(); old.kind != 0 && old != id {
			if oldDoc := a.documentForTab(old); oldDoc != nil {
				a.forgetDocument(oldDoc)
			}
		}
	}
	a.tabDocuments[id] = d
	if g.indexOf(id) >= 0 {
		syncRequestTab(g, d)
		g.activate(id)
	} else {
		g.open(requestTabDescriptor(d), opts)
	}
	a.syncDocumentOrder()
	a.applyActiveTab()
	a.focusActiveTab()
	return id
}

// pageTab names the tab for a page that is not a request document. A session
// and a contract get one tab each; the other pages are singletons.
func (a *app) pageTab(kind page) tab {
	switch kind {
	case pageLive:
		return tab{kind: pageLive, key: liveTabKey, title: locales.Live(), saved: true}
	case pageSession:
		title := locales.Sessions()
		if item := a.sessions.current(); item != nil && item.data.Title != "" {
			title = item.data.Title
		}
		return tab{kind: pageSession, key: "session:" + a.sessions.active, title: title, saved: true}
	case pageAPI:
		title := locales.ApiContracts()
		if d := a.activeAPI(); d != nil {
			title = d.title()
		}
		return tab{kind: pageAPI, key: "api:" + a.apiChoice, title: title, saved: true}
	case pageRuns:
		return tab{kind: pageRuns, key: "runs", title: locales.RunsPage(), saved: true}
	case pageDiagnostics:
		return tab{kind: pageDiagnostics, key: "diagnostics", title: locales.Diagnostics(), saved: true}
	case pageOverview:
		return tab{kind: pageOverview, key: "overview", title: locales.Overview(), saved: true}
	default:
		return tab{kind: kind, key: strconv.Itoa(int(kind)), title: "", saved: true}
	}
}

// openPageTab opens or activates the tab for a page and makes it active.
func (a *app) openPageTab(kind page, opts tabOpen) tabID {
	t := a.pageTab(kind)
	id := a.tabsGroup().open(t, opts)
	a.applyActiveTab()
	return id
}

// promoteTab makes a preview a normal tab without pinning it: a double click
// on the tab, as a double click in Collections does for its request.
func (a *app) promoteTab(id tabID) {
	if a.tabsGroup().keep(id) {
		a.activateTab(id)
	}
}

// toggleTabPin pins a tab to the front of the scrolling tabs, or unpins it.
// Live is fixed and takes no pin.
func (a *app) toggleTabPin(id tabID) {
	g := a.tabsGroup()
	if g.isFixed(id) {
		return
	}
	if g.isPinned(id) {
		g.unpin(id)
	} else {
		g.pin(id)
	}
	a.applyActiveTab()
}

// revealDocumentInSidebar shows the request in Collections without changing
// the active tab: its folder opens, the sidebar comes back and any tree
// search that would hide it clears.
func (a *app) revealDocumentInSidebar(d *requestDocument) {
	if d == nil {
		return
	}
	a.workbench.sidebarHidden = false
	a.sidebarMode = sidebarCollections
	*a.regionOpen("collections") = true
	a.requestQuery = ""
	if d.savedKey != "" {
		for _, prefix := range pathPrefixes(d.composeCollection) {
			*a.collectionShown(prefix) = true
		}
		a.requestChoice = d.savedKey
		return
	}
	*a.collectionShown(unsavedGroup) = true
}

// showRequestWorkspace brings COLLECTIONS to the front: its most recently
// used request tab, or the empty workspace when no request is open. The empty
// workspace is a placeholder tab, so page and strip still agree.
func (a *app) showRequestWorkspace() {
	if id, ok := a.mostRecentTab(pageRequest); ok {
		a.activateTab(id)
		return
	}
	id := tabID{kind: pageRequest, key: "draft:0"}
	g := a.tabsGroup()
	if g.indexOf(id) < 0 {
		g.open(tab{kind: pageRequest, key: id.key, title: locales.Collections()}, tabOpen{})
	} else {
		g.activate(id)
	}
	a.applyActiveTab()
}

// openAPITab opens one contract's tab.
func (a *app) openAPITab(id string) {
	a.apiChoice = id
	a.openPageTab(pageAPI, tabOpen{})
}

// activateTab makes an open tab active and points the old fields at it.
func (a *app) activateTab(id tabID) bool {
	g := a.tabsGroup()
	if !g.activate(id) {
		return false
	}
	a.applyActiveTab()
	a.focusActiveTab()
	return true
}

// showTab selects a tab without changing the MRU order, for Ctrl/Cmd+Tab's
// quick switch while the chord is still held.
func (a *app) showTab(id tabID) bool {
	g := a.tabsGroup()
	if !g.show(id) {
		return false
	}
	a.applyActiveTab()
	a.focusActiveTab()
	return true
}

// focusActiveTab puts the caret where typing belongs when a request tab
// becomes active. Background synchronization does not call it, so a frame
// never steals the reader's focus.
func (a *app) focusActiveTab() {
	if a.page == pageRequest && a.requestDocument != nil {
		a.requestDocument.urlInput.Focus()
	}
}

// applyActiveTab copies the active tab into page, requestDocument,
// requestChoice and the page-specific choice. These fields stay for the many
// screens that read them, but the tab is what decides their values.
func (a *app) applyActiveTab() {
	g := a.tabsGroup()
	id := g.active()
	a.page = id.kind
	switch id.kind {
	case pageRequest:
		d := a.documentForTab(id)
		a.requestDocument = d
		if d != nil {
			a.requestChoice = d.savedKey
		} else {
			a.requestChoice = ""
		}
	case pageLive:
		a.requestDocument = nil
		a.requestChoice = ""
	case pageAPI:
		a.apiChoice = strings.TrimPrefix(id.key, "api:")
	case pageSession:
		a.sessions.active = strings.TrimPrefix(id.key, "session:")
	}
}

// syncTabs makes the strip agree with the app before a frame draws. The app
// still has callers that set page or requestDocument directly; a change they
// made opens or activates the tab that now owns it. Documents that other code
// put in documents get a tab without stealing the reader's place.
func (a *app) syncTabs() {
	g := a.tabsGroup()
	// A request tab whose document left the open registry is a ghost left
	// by an undo or a bulk replace. Drop it without putting it on the
	// reopen stack; the documents slice is the registry of real ones.
	for _, e := range g.tabs() {
		if e.kind != pageRequest || e.key == "draft:0" {
			continue
		}
		d := a.documentForTab(e.id())
		if d == nil || !slices.Contains(a.documents, d) {
			g.drop(e.id())
			delete(a.tabDocuments, e.id())
		}
	}
	for _, d := range a.documents {
		if d == nil {
			continue
		}
		id := requestTabID(d)
		a.tabDocuments[id] = d
		if g.indexOf(id) < 0 {
			g.add(requestTabDescriptor(d))
		}
	}

	switch {
	case a.page == pageRequest && a.requestDocument != nil &&
		(slices.Contains(a.documents, a.requestDocument) || g.active().kind == pageRequest):
		id := requestTabID(a.requestDocument)
		if g.indexOf(id) < 0 {
			a.tabDocuments[id] = a.requestDocument
			g.add(requestTabDescriptor(a.requestDocument))
		}
		if g.active() != id {
			g.activate(id)
		}
	case a.page == pageRequest:
		var found tabID
		for _, id := range g.mru() {
			if id.kind == pageRequest {
				found = id
				break
			}
		}
		if found.kind == pageRequest {
			g.activate(found)
			break
		}
		if a.requestChoice != "" {
			if r, ok := a.savedRequest(a.requestChoice); ok {
				a.openRequestTab(a.newDocument(r, r.key()), tabOpen{})
				break
			}
		}
		id := tabID{kind: pageRequest, key: "draft:0"}
		g.open(tab{kind: pageRequest, key: id.key, title: locales.Collections()}, tabOpen{})
	case a.page != pageLive && a.page != g.active().kind:
		a.openPageTab(a.page, tabOpen{})
	case a.page == pageAPI && g.active().kind == pageAPI && g.active().key != "api:"+a.apiChoice:
		a.openPageTab(pageAPI, tabOpen{})
	case a.page == pageSession && g.active().kind == pageSession && g.active().key != "session:"+a.sessions.active:
		a.openPageTab(pageSession, tabOpen{})
	case a.page == pageLive && g.active().kind != pageLive:
		a.openPageTab(pageLive, tabOpen{})
	}

	for _, e := range g.tabs() {
		if e.kind != pageRequest {
			continue
		}
		if d := a.documentForTab(e.id()); d != nil {
			syncRequestTab(g, d)
		}
	}
	a.syncDocumentOrder()
	a.applyActiveTab()
}

// requestTabFor finds the open tab for a document, or the zero ID.
func (a *app) requestTabFor(d *requestDocument) tabID {
	if d == nil {
		return tabID{kind: pageLive, key: liveTabKey}
	}
	return requestTabID(d)
}

// mostRecentTab is the first tab of a kind in recency order.
func (a *app) mostRecentTab(kind page) (tabID, bool) {
	for _, id := range a.tabsGroup().mru() {
		if id.kind == kind {
			return id, true
		}
	}
	return tabID{}, false
}
