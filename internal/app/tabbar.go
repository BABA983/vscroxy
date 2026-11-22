package app

import (
	"cmp"
	"slices"

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
		OverflowLabel: locales.AllOpenTabs(),
		OverflowTip:   locales.OpenTabsMostRecentlyUsed(),
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
		OnOverflow: a.openTabsMenu,
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
// at the document strip's right end. It is a chip with a status dot rather
// than a field: the choice belongs to the request being edited, not to the
// window's chrome.
func (a *app) environmentSelect(c *ui.Context, t *ui.Theme) {
	if names := a.envNames(); len(names) > 0 {
		env := ui.SelectBase(c.Key("active-environment"), &a.activeEnv)
		uikit.Tip(c, env.Trigger.Label(locales.ActiveEnvironment()), locales.ActiveEnvironment()).
			Width(uikit.WidthPicker).Height(uikit.HeightTabs).
			Padding(0, t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceM)).Radius(uikit.RadiusControl).
			Background(uikit.Colors(t).Background).Border(1, t.Border).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(uikit.DotSize, uikit.DotSize).Radius(uikit.DotSize / 2).Background(t.Accent)
			uikit.Body(c, cmp.Or(a.activeEnv, locales.NoEnvironment())).SingleLine().Grow(1)
			uikit.Glyph(c, chevronIcon)
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
		Height(uikit.HeightTabs).Children(func() { uikit.Body(c, locales.NoEnvironment()) }).OnClick(a.openVars)
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
	case pageSearch:
		return searchIcon
	case pageAPI:
		return apiIcon
	default:
		return requestIcon
	}
}

// openTabsMenu lists every open tab in recency order, as the overflow button
// promises. A checked row is the active tab; a subtitle marks unsaved work.
func (a *app) openTabsMenu(m *ui.Menu) {
	g := a.tabsGroup()
	entries := g.tabs()
	active := g.active()
	for _, id := range g.mru() {
		at := slices.IndexFunc(entries, func(e tabEntry) bool { return e.id() == id })
		if at < 0 {
			continue
		}
		e := entries[at]
		label := e.title
		if id.kind == pageRequest {
			if d := a.documentForTab(id); d != nil {
				label = cmp.Or(d.protocol, d.composeMethod) + "  " + e.title
			}
		}
		item := m.Item(label).Checked(id == active)
		if e.dirty {
			item.Subtitle(locales.UnsavedChanges())
		}
		if item.Chosen() {
			a.activateTab(id)
		}
	}
}
