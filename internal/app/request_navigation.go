package app

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// treeRow gives one row of the sidebar's tree the rows' metrics: a control
// high, inset like the sidebar's other rows, indented by its depth, with the
// quiet face under the pointer. A branch puts its arrow in the row's slot; a
// leaf starts with what it holds, so a request's name follows its method with
// no empty slot between them.
func treeRow(c *ui.Context, item ui.TreeItemParts, depth int) ui.Element {
	t := c.Theme()
	row := item.Row.Height(uikit.HeightControl).Margin(0, t.Space(1.5)).Padding(0, t.Space(1.5)).
		PaddingStart(t.Space(1.5 + 2*float32(depth))).Gap(t.Space(2)).Radius(uikit.RadiusControl).AlignItems(ui.Center)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	return row
}

// methodCell is a request row's method, right-aligned in the tree's column: the
// name that follows starts at the same place on every row, and the gap between
// the two is the row's gap alone.
func methodCell(c *ui.Context, method string, slot float32) {
	ui.Row(c).Width(slot).Shrink(0).Justify(ui.End).Children(func() {
		uikit.MethodLabel(c, method).FontSize(uikit.TextMeta).Height(16)
	})
}

// methodAdvance is how wide one character of a method is at the meta size in
// the monospace face, which advances every character the same.
const methodAdvance = uikit.TextMeta * 0.68

// treeMethodSlot is the width of the tree's method column: the widest method
// it shows, so every request's name starts on one line and follows its method
// by the row's own gap rather than by a column of slack.
func (a *app) treeMethodSlot() float32 {
	longest := 3 // GET, the shortest method there is
	for _, r := range a.requests {
		longest = max(longest, utf8.RuneCountInString(cmp.Or(r.Protocol, r.Method)))
	}
	for _, d := range a.documents {
		if d.savedKey == "" {
			longest = max(longest, utf8.RuneCountInString(cmp.Or(d.protocol, d.composeMethod)))
		}
	}
	return float32(longest)*methodAdvance + 2
}

// branchArrow is a branch row's disclosure: it says whether the row is open
// and toggles it. A leaf has none, so its own content starts the row.
func branchArrow(c *ui.Context, open *bool) {
	icon := statusMoreIcon
	if *open {
		icon = chevronIcon
	}
	arrow := ui.Box(c).Size(uikit.HeightControl-8, uikit.HeightControl-8).Shrink(0).OnClick(func() { *open = !*open })
	arrow.Children(func() {
		ui.Icon(c, icon).Size(uikit.IconInline, uikit.IconInline).TextColor(c.Theme().TextMuted)
	})
}

// sessionsTree is the last row of the collections tree: the sessions kept
// beyond live traffic, each opening the requests it holds. It folds away like
// any other branch, so the tree stays one list rather than gaining a section.
func (a *app) sessionsTree(c *ui.Context, tree ui.TreeParts) {
	open := !a.sessionsClosed
	item := tree.Item(&open)
	row := treeRow(c, item, 0).Label(locales.Sessions())
	if a.revealGroup == "sessions" {
		row.ScrollIntoView()
	}
	row.Children(func() {
		branchArrow(c, &open)
		uikit.Body(c, locales.Sessions()).SingleLine().Grow(1)
		if n := len(a.sessions.items); n > 0 {
			uikit.Meta(c, fmt.Sprint(n)).FontFeatures("tnum")
		}
	})
	item.Group(func() {
		for _, s := range a.sessions.items {
			leaf := tree.Item(nil)
			session := uikit.Tip(c, treeRow(c, leaf, 1).Label(s.data.Title), s.data.Title).
				OnClick(func() { a.goTo("session:" + s.key) }).
				PopoverMenu(func(m *ui.Menu) {
					actionMenu(m, []action{do("session.delete", locales.DeleteSession(), func() { a.confirmDeleteSession(s) })})
				})
			if a.sidebarChoice == "session:"+s.key {
				uikit.Selected(c, session, !a.windowBehind)
			}
			session.Children(func() {
				uikit.Glyph(c, clockIcon)
				uikit.Body(c, s.data.Title).SingleLine().Grow(1)
				uikit.Meta(c, fmt.Sprint(s.count)).FontFeatures("tnum")
			})
		}
		opening := tree.Item(nil)
		openSession := treeRow(c, opening, 1).Label(locales.OpenSession()).
			Tooltip(locales.OpenHistoricalSessionFilesWithout()).OnClick(a.openSession)
		openSession.Children(func() {
			uikit.Glyph(c, clockIcon)
			uikit.Body(c, locales.OpenSession()).SingleLine().Grow(1)
		})
	})
	a.sessionsClosed = !open
}

// collectionTree uses full paths for identity and short names for presentation.
// Search opens matching ancestors without rewriting the user's collapsed state.
func (a *app) collectionTree(c *ui.Context) {
	query := strings.ToLower(strings.TrimSpace(a.requestQuery))
	paths := map[string]bool{}
	addPath := func(path string) {
		for path != "" {
			paths[path] = true
			parent, _, found := strings.CutLast(path, "/")
			if !found {
				break
			}
			path = parent
		}
	}
	for _, r := range a.requests {
		if query == "" || strings.Contains(strings.ToLower(r.Collection+" "+r.Name+" "+r.URL+" "+cmp.Or(r.Protocol, r.Method)), query) {
			addPath(r.Collection)
		}
	}
	for _, f := range a.folders {
		if query == "" || strings.Contains(strings.ToLower(f.Path), query) {
			addPath(f.Path)
		}
	}
	tree := ui.TreeBase(c)
	var branch func(string, int)
	branch = func(parent string, depth int) {
		var children []string
		for path := range paths {
			prefix, _, found := strings.CutLast(path, "/")
			if !found {
				prefix = ""
			}
			if prefix == parent {
				children = append(children, path)
			}
		}
		slices.Sort(children)
		for _, path := range children {
			_, name, found := strings.CutLast(path, "/")
			if !found {
				name = path
			}
			open := a.collectionShown(path)
			if query != "" {
				open = new(true)
			}
			menu := func(m *ui.Menu) {
				mock := "Mock “" + path + "” from examples"
				if slices.ContainsFunc(a.rules, func(r capture.Rule) bool { return r.Scenario == path }) {
					mock = "Rebuild scenario “" + path + "” from examples"
				}
				actions := append([]action{do("collection.docs", locales.ViewDocumentation(), func() { a.openDocs(path) }).in("context")}, a.folderSettingsActions(path)...)
				actionMenu(m, append(actions,
					do("collection.mock", mock, func() { a.scenarioFromExamples(path) }).
						when(a.examplesUnder(path), "Save a response as an example first").in("mock"),
					do("collection.delete", "Delete “"+path+"”…", func() { a.confirmDeleteFolder(path) }).in("export")))
			}
			parts := tree.Item(open)
			row := treeRow(c, parts, depth).Label(locales.CollectionPath(path)).PopoverMenu(menu)
			row.Children(func() {
				branchArrow(c, open)
				uikit.Body(c, name).SingleLine().Grow(1)
				action := uikit.IconButton(c, locales.CollectionActionsPrefix(path), locales.CollectionActions(), moreIcon).PopoverMenuButton(menu).
					Absolute().Right(0).Top(2).Background(uikit.Colors(c.Theme()).Chrome)
				if !row.Hovered() && !row.FocusWithin() {
					action.Opacity(0)
				}
			})
			parts.Group(func() { branch(path, depth+1) })
		}
		for _, r := range a.requests {
			method := cmp.Or(r.Protocol, r.Method)
			if r.Collection != parent || query != "" && !strings.Contains(strings.ToLower(r.Collection+" "+r.Name+" "+r.URL+" "+method), query) {
				continue
			}
			menu := func(m *ui.Menu) {
				actionMenu(m, append([]action{
					do("request.delete", locales.DeleteRequest(), func() { a.confirmDeleteRequest(r.key(), r.Name) }).in("delete"),
				}, a.folderSettingsActions(r.Collection)...))
			}
			// The sidebar has one choice: a request only while it is shown.
			chosen := a.page == pageRequest && a.requestChoice == r.key()
			edited := slices.ContainsFunc(a.documents, func(d *requestDocument) bool { return d.savedKey == r.key() && d.dirty() })
			row := uikit.Tip(c, treeRow(c, tree.Item(nil), depth).Label(r.Name).
				Selected(chosen), method+" "+r.URL).
				OnClick(func() { a.openSaved(r.key()) }).PopoverMenu(menu)
			if chosen {
				uikit.Selected(c, row, !a.windowBehind)
			}
			row.Children(func() {
				ui.Row(c).Grow(1).FillHeight().Gap(c.Theme().Space(2)).AlignItems(ui.Center).Label(method + "  " + r.Name).Children(func() {
					methodCell(c, method, a.treeMethodSlot())
					uikit.Body(c, r.Name).SingleLine().Grow(1)
					if edited {
						unsavedDot(c)
					}
					action := uikit.IconButton(c, locales.RequestActionsPrefix(r.Name), locales.RequestActions(), moreIcon).PopoverMenuButton(menu).
						Absolute().Right(0).Top(2).Background(uikit.Colors(c.Theme()).Chrome)
					if !row.Hovered() && !row.FocusWithin() {
						action.Opacity(0)
					}
				})
			})
		}
	}
	tree.Root.Gap(0).Children(func() {
		a.unsavedRequests(c, tree, query)
		branch("", 0)
		a.sessionsTree(c, tree)
	})
	if len(a.requests) == 0 && len(paths) == 0 {
		uikit.Hint(c, locales.SaveARequestHereOr()).Padding(c.Theme().Space(2))
	}
}

// unsavedGroup keys the Unsaved group's open state among the collections'.
const unsavedGroup = "\x00unsaved"

// unsavedRequests lists the open requests no collection holds yet, above the
// collections: with one request on screen at a time, this is where a new
// one waits while another is shown.
func (a *app) unsavedRequests(c *ui.Context, tree ui.TreeParts, query string) {
	var unsaved []*requestDocument
	for _, d := range a.documents {
		if d.savedKey == "" && (query == "" || strings.Contains(strings.ToLower(d.title()+" "+d.composeURL+" "+cmp.Or(d.protocol, d.composeMethod)), query)) {
			unsaved = append(unsaved, d)
		}
	}
	if len(unsaved) == 0 {
		return
	}
	open := a.collectionShown(unsavedGroup)
	group := tree.Item(open)
	row := treeRow(c, group, 0).Label(locales.UnsavedRequests())
	row.Children(func() {
		branchArrow(c, open)
		uikit.Body(c, locales.Unsaved()).SingleLine().Grow(1)
		uikit.Meta(c, fmt.Sprint(len(unsaved))).FontFeatures("tnum")
	})
	group.Group(func() {
		for _, d := range unsaved {
			method := cmp.Or(d.protocol, d.composeMethod)
			chosen := a.page == pageRequest && a.requestDocument == d
			item := uikit.Tip(c, treeRow(c, tree.Item(nil), 1).Label(locales.UnsavedPrefix(d.title())).
				Selected(chosen), method+" "+d.composeURL).
				OnClick(func() { a.requestDocument, a.requestChoice, a.page = d, "", pageRequest })
			if chosen {
				uikit.Selected(c, item, !a.windowBehind)
			}
			item.Children(func() {
				methodCell(c, method, a.treeMethodSlot())
				uikit.Body(c, d.title()).SingleLine().Grow(1)
			})
		}
	})
}

// unsavedDot marks a request whose open document has changes to save.
func unsavedDot(c *ui.Context) {
	ui.Box(c).Size(6, 6).Radius(uikit.RadiusControl).Background(c.Theme().Accent).Label(locales.UnsavedChanges())
}
