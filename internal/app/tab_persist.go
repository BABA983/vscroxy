package app

import (
	"encoding/json/v2"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/locales"
)

// Open tabs survive a restart as VS Code's editors do: the strip's order,
// which tab was pinned or the preview, and which was active. Only tabs that
// name something on disk are kept: a saved request by its ID and a contract
// by its spec ID. A never-saved draft has nothing to reopen, and session tabs
// come back with the last session, which owns their files.

const tabsFile = "tabs.json"

type savedTabs struct {
	Version int        `json:"version"`
	Tabs    []savedTab `json:"tabs"`
	// Active indexes Tabs; -1 is a tab that is not kept, such as Live.
	Active int `json:"active"`
}

type savedTab struct {
	Request string `json:"request,omitempty"` // a saved request's ID
	API     string `json:"api,omitempty"`     // a contract's spec ID
	Pinned  bool   `json:"pinned,omitzero"`
	Preview bool   `json:"preview,omitzero"`
}

// openTabs is the strip as it would be saved now.
func (a *app) openTabs() savedTabs {
	doc := savedTabs{Version: store.Version, Tabs: []savedTab{}, Active: -1}
	if a.tabs == nil {
		return doc
	}
	g := a.tabsGroup()
	active := g.active()
	for _, e := range g.tabs() {
		var entry savedTab
		switch e.kind {
		case pageRequest:
			// A saved request that was edited reopens as saved: the edit was
			// never written, so the file is what there is to reopen.
			d := a.documentForTab(e.id())
			if d == nil || d.assetID == "" {
				continue
			}
			entry.Request = d.assetID
		case pageAPI:
			entry.API = strings.TrimPrefix(e.key, "api:")
			if entry.API == "" {
				continue
			}
		default:
			continue
		}
		entry.Pinned, entry.Preview = g.isPinned(e.id()), g.isPreview(e.id())
		if e.id() == active {
			doc.Active = len(doc.Tabs)
		}
		doc.Tabs = append(doc.Tabs, entry)
	}
	return doc
}

// saveTabs writes the strip as the app quits. No backups: the file is
// rewritten on every quit, and an old order is not worth keeping.
func (a *app) saveTabs() {
	if a.tabsPath == "" {
		return
	}
	data, err := json.Marshal(a.openTabs())
	if err == nil {
		err = store.WriteFileVersioned(a.tabsPath, store.Tabs, append(data, '\n'), "")
	}
	if err != nil {
		a.fail(locales.OpenTabsNotSaved(), err)
	}
}

// restoreTabs reopens the tabs the last quit saved, after the library and
// the contracts are read. What has since gone, such as a deleted request, is
// skipped; the rest keep their order.
func (a *app) restoreTabs() {
	if a.tabsPath == "" {
		return
	}
	data, err := store.ReadFile(a.tabsPath, store.Tabs)
	if err == nil && data == nil {
		return
	}
	var doc savedTabs
	if err == nil {
		err = json.Unmarshal(data, &doc)
	}
	if err != nil {
		a.fail(locales.OpenTabsNotRestored(), err)
		return
	}
	g := a.tabsGroup()
	active := tabID{kind: pageLive, key: liveTabKey}
	for i, e := range doc.Tabs {
		opts := tabOpen{pinned: e.Pinned, preview: e.Preview && !e.Pinned}
		switch {
		case e.Request != "":
			key := savedRequest{ID: e.Request}.key()
			if !slices.ContainsFunc(a.requests, func(r savedRequest) bool { return r.ID == e.Request }) {
				continue
			}
			a.openSavedTab(key, opts)
		case e.API != "":
			if !slices.ContainsFunc(a.apis, func(d *apiDocument) bool { return d.spec.ID == e.API }) {
				continue
			}
			a.apiChoice = e.API
			a.openPageTab(pageAPI, opts)
		default:
			continue
		}
		if i == doc.Active {
			active = g.active()
		}
	}
	a.activateTab(active)
	if k := active.kind; k == pageRequest || k == pageAPI {
		a.sidebarMode = sidebarCollections
	}
}
