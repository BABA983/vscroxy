package app

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// tabBar draws the window's document strip. The model supplies order, active,
// preview and dirty state; this maps each descriptor to the label, method and
// icon its page shows.
func (a *app) tabBar(c *ui.Context, t *ui.Theme) {
	g := a.tabsGroup()
	entries := g.tabs()
	active := g.active()
	items := make([]uikit.DocumentTab, len(entries))
	ids := make([]tabID, len(entries))
	for i, e := range entries {
		id := e.id()
		ids[i] = id
		item := uikit.DocumentTab{
			Title:    cmp.Or(e.title, locales.UntitledRequest()),
			Dirty:    e.dirty,
			Preview:  e.preview,
			Active:   id == active,
			Fixed:    e.fixed,
			Closable: !e.fixed,
			Pinned:   e.pinned,
			Reveal:   id == active && id != a.tabShown,
		}
		if id.kind == pageRequest {
			if d := a.documentForTab(id); d != nil {
				item.Method = cmp.Or(d.protocol, d.composeMethod)
			}
		} else {
			item.Icon = tabIcon(id.kind)
		}
		items[i] = item
	}
	opts := uikit.DocumentTabsOptions{
		Tabs:          items,
		NewLabel:      locales.NewRequest(),
		OverflowLabel: locales.SearchOpenTabs(),
		OnActivate: func(i int) {
			// Choosing Live is choosing the traffic; Live after a close is not.
			if ids[i].kind == pageLive {
				a.sidebarMode = sidebarTraffic
			}
			a.activateTab(ids[i])
		},
		OnClose:    func(i int) { a.closeTab(ids[i], false) },
		OnPin:      func(i int) { a.promoteTab(ids[i]) },
		OnNew:      a.openCompose,
		OnOverflow: func(anchor ui.Element) { a.openTabsPicker(c, t, anchor) },
		TabMenu:    func(i int) func(*ui.Menu) { return a.tabContextMenu(c, ids[i]) },
	}
	// The environment belongs to the request being edited, so it appears
	// with a request tab and steps aside for Live, a session or a contract.
	if active.kind == pageRequest {
		opts.Trailing = func() { a.environmentSelect(c, t) }
	}
	uikit.DocumentTabs(c, opts)
	// The strip takes room from the list. When LIVE becomes active, a list
	// that follows traffic scrolls to its new end, as it would after a resize.
	if active.kind == pageLive && a.tabShown != active && a.autoSelect {
		if view := a.views.activeView(); view != nil {
			view.list.ScrollToEnd()
		}
	}
	a.tabShown = active
}

// environmentSelect is the environment a request resolves its variables in,
// at the document strip's right end. It reads as chrome rather than a field:
// the name and chevron take a face only under the pointer, as Postman's
// environment switcher does.
func (a *app) environmentSelect(c *ui.Context, t *ui.Theme) {
	if names := a.envNames(); len(names) > 0 {
		env := ui.SelectBase(c.Key("active-environment"), &a.activeEnv)
		trigger := uikit.Tip(c, env.Trigger.Label(locales.ActiveEnvironment()), locales.ActiveEnvironment())
		trigger.MaxWidth(uikit.WidthWide).Height(uikit.HeightControlSmall).
			Padding(0, t.Space(uikit.SpaceS), 0, t.Space(uikit.SpaceCompact)).Gap(t.Space(uikit.SpaceXS)).
			Radius(uikit.RadiusControl).AlignItems(ui.Center).Background(ui.Color{})
		if trigger.Hovered() {
			// The selector is chrome, not a field: its face appears under the
			// pointer, as Postman's environment switcher does.
			trigger.Background(t.Border)
		}
		trigger.Children(func() {
			uikit.Caption(c, cmp.Or(a.activeEnv, locales.NoEnvironment())).
				TextColor(t.TextMuted).SingleLine().MinWidth(0).Grow(1)
			ui.Icon(c, chevronIcon).Size(uikit.IconLarge, uikit.IconLarge).TextColor(t.TextMuted)
		})
		env.Popup(func(panel ui.Element) {
			uikit.PopoverPanel(c, panel).Padding(t.Space(uikit.SpaceXS))
			for _, name := range names {
				item := env.Item(name).Label(name).Height(uikit.HeightRow).Padding(0, t.Space(uikit.SpaceM)).
					Gap(t.Space(uikit.SpaceM)).Radius(uikit.RadiusControl).AlignItems(ui.Center)
				if item.Highlighted() {
					item.Background(t.SurfaceHover)
				}
				item.Children(func() {
					color := t.TextMuted
					if name == a.activeEnv {
						color = t.Accent
					}
					ui.Box(c).Size(uikit.DotSize, uikit.DotSize).Radius(uikit.DotSize / 2).Background(color)
					uikit.Body(c, name)
				})
			}
		})
		return
	}
	uikit.ToolbarButton(c, locales.ManageEnvironments2(), locales.AddAnEnvironmentForRequest(), false).
		Height(uikit.HeightControlSmall).TextColor(t.TextMuted).Children(func() {
		uikit.Caption(c, locales.NoEnvironment())
	}).OnClick(a.openVars)
}

func tabIcon(kind page) *ui.SVG {
	switch kind {
	case pageLive:
		return captureIcon
	case pageSession:
		return clockIcon
	case pageRuns:
		return resumeIcon
	case pageDiagnostics:
		return lucide.ShieldCheck()
	case pageOverview:
		return overviewIcon
	case pageAPI:
		return apiIcon
	default:
		return requestIcon
	}
}

// tabPickerState is the strip's searchable list: its popover, query and
// highlighted row. It is small, in-memory state, like the palette's.
type tabPickerState struct {
	open  bool
	state uikit.QuickPickState
}

// tabPickPrefix tells the two groups' ids apart: an open tab is activated,
// a closed one is restored.
const (
	tabPickOpen   = "open:"
	tabPickClosed = "closed:"
)

func tabPickID(prefix string, id tabID) string {
	return prefix + strconv.Itoa(int(id.kind)) + "\x00" + id.key
}

func parseTabPickID(value string) (tabID, bool) {
	prefix, rest, ok := strings.Cut(value, ":")
	if !ok || prefix != "open" && prefix != "closed" {
		return tabID{}, false
	}
	kind, key, ok := strings.Cut(rest, "\x00")
	if !ok {
		return tabID{}, false
	}
	n, err := strconv.Atoi(kind)
	if err != nil {
		return tabID{}, false
	}
	return tabID{kind: page(n), key: key}, true
}

// openTabsPicker is the strip's ⌄: a searchable list of the open tabs in
// recency order and the tabs closed before, as VS Code's open-editor quick
// pick. The popover is anchored to the strip's own button.
func (a *app) openTabsPicker(c *ui.Context, t *ui.Theme, anchor ui.Element) {
	s := &a.tabPicker
	uikit.QuickPick(c, anchor, uikit.QuickPickOptions{
		Open:        &s.open,
		State:       &s.state,
		Placeholder: locales.SearchTabs(),
		Empty:       locales.NoMatchingTabs(),
		Groups:      a.tabPickGroups,
		OnActivate: func(value string) {
			id, ok := parseTabPickID(value)
			if !ok {
				return
			}
			if strings.HasPrefix(value, tabPickClosed) {
				a.reopenClosedDocumentID(id)
				return
			}
			if id.kind == pageLive {
				a.sidebarMode = sidebarTraffic
			}
			a.activateTab(id)
		},
		OnClose: func(value string) {
			if id, ok := parseTabPickID(value); ok && strings.HasPrefix(value, tabPickOpen) {
				a.closeTab(id, false)
			}
		},
	})
}

// tabPickGroups lists all open tabs by recency, then the closed tabs newest
// first. A request row carries its method, unsaved mark and collection; the
// picker highlights the query on the title.
func (a *app) tabPickGroups() []uikit.QuickPickGroup {
	g := a.tabsGroup()
	entries := g.tabs()
	open := uikit.QuickPickGroup{Title: locales.OpenTabsGroup()}
	for _, id := range g.mru() {
		at := slices.IndexFunc(entries, func(e tabEntry) bool { return e.id() == id })
		if at < 0 {
			continue
		}
		e := entries[at]
		item := uikit.QuickPickItem{
			ID: tabPickID(tabPickOpen, id), Label: cmp.Or(e.title, locales.UntitledRequest()),
			Dirty: e.dirty, Closable: !e.fixed,
		}
		a.fillTabPickItem(&item, id, e.tab)
		open.Items = append(open.Items, item)
	}
	groups := []uikit.QuickPickGroup{open}
	closed := uikit.QuickPickGroup{Title: locales.RecentlyClosedGroup()}
	for _, c := range g.recentlyClosed() {
		id := c.tab.id()
		item := uikit.QuickPickItem{ID: tabPickID(tabPickClosed, id), Label: cmp.Or(c.tab.title, locales.UntitledRequest())}
		a.fillTabPickItem(&item, id, c.tab)
		closed.Items = append(closed.Items, item)
	}
	if len(closed.Items) > 0 {
		groups = append(groups, closed)
	}
	return slices.DeleteFunc(groups, func(group uikit.QuickPickGroup) bool { return len(group.Items) == 0 })
}

// fillTabPickItem gives a row its method or icon and the facts its document
// carries: the URL the query matches and the collection path at its end.
func (a *app) fillTabPickItem(item *uikit.QuickPickItem, id tabID, e tab) {
	if id.kind == pageRequest {
		if d := a.documentForTab(id); d != nil {
			item.Method = cmp.Or(d.protocol, d.composeMethod)
			item.Search = d.composeURL
			item.Detail = d.composeCollection
			return
		}
		if entry, ok := a.closedDocs[id]; ok && entry.doc != nil {
			d := entry.doc
			item.Method = cmp.Or(d.protocol, d.composeMethod)
			item.Search = d.composeURL
			item.Detail = d.composeCollection
			return
		}
	}
	item.Icon = tabIcon(id.kind)
}

// tabContextMenu is a tab's own right-click menu, as VS Code's editor tab
// menu: closes, the pin, and the request's address and place in the sidebar.
// Fixed tabs such as Live get only the closes that keep them.
func (a *app) tabContextMenu(c *ui.Context, id tabID) func(*ui.Menu) {
	return func(m *ui.Menu) {
		g := a.tabsGroup()
		e, ok := g.find(id)
		if !ok {
			return
		}
		entries := g.tabs()
		at := g.indexOf(id)
		closable, right := 0, 0
		for i, other := range entries {
			if other.id() == id || other.fixed || other.pinned {
				continue
			}
			closable++
			if i > at {
				right++
			}
		}
		var items []action
		if !e.fixed {
			items = append(items, do("tab.close", locales.Close(), func() { a.closeTab(id, false) }).
				keys(ui.Cmd, ui.KeyW).in("close"))
		}
		items = append(items,
			do("tab.close-others", locales.CloseOtherTabs(), func() { a.closeOtherTabs(id) }).
				when(closable > 0, "").in("close"),
			do("tab.close-right", locales.CloseTabsToTheRight(), func() { a.closeTabsToRight(id) }).
				when(right > 0, "").in("close"),
			do("tab.close-all", locales.CloseAllTabs(), a.closeAllDocuments).
				when(closable > 0, "").in("close"),
		)
		if !e.fixed {
			label := locales.PinTab()
			if e.pinned {
				label = locales.UnpinTab()
			}
			items = append(items, do("tab.pin", label, func() { a.toggleTabPin(id) }).in("pin"))
		}
		if id.kind == pageRequest {
			if d := a.documentForTab(id); d != nil {
				if d.composeURL != "" {
					items = append(items, do("tab.copy-url", locales.CopyUrl(), func() {
						c.Services().WriteClipboard(d.composeURL)
						a.done(locales.CopiedUrl())
					}).in("nav"))
				}
				items = append(items, do("tab.reveal", locales.RevealInSidebar(), func() {
					a.revealDocumentInSidebar(d)
				}).in("nav"))
			}
		}
		actionMenu(m, items)
	}
}
