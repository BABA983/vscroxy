package app

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/fuzzy"
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
	row := item.Row.Height(uikit.HeightRow).Margin(0, t.Space(uikit.SpaceS)).Padding(0, t.Space(uikit.SpaceS)).
		PaddingStart(t.Space(uikit.SpaceS) + float32(depth)*t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceM)).Radius(uikit.RadiusControl).AlignItems(ui.Center)
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
		uikit.MethodLabel(c, method).Height(16)
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
	arrow := ui.Box(c).Size(uikit.HeightRow-8, uikit.HeightRow-8).Shrink(0).OnClick(func() { *open = !*open })
	arrow.Children(func() {
		ui.Icon(c, icon).Size(uikit.IconInline, uikit.IconInline).TextColor(c.Theme().TextMuted)
	})
}

// folderRenameState is a collection or folder's name being edited in the
// tree: the path says which row, and focus asks the input to take the
// keyboard in the frame it appears.
type folderRenameState struct {
	path, name     string
	focus, focused bool
}

// startFolderRename turns a folder row's name into an input in place, as
// Postman renames a collection. Another row being edited is committed first,
// so a click on a second name does not silently discard the first.
func (a *app) startFolderRename(path string) {
	if a.folderRename.path != "" && a.folderRename.path != path {
		a.commitFolderRename(a.folderRename.path)
	}
	name := path
	if _, last, found := strings.CutLast(path, "/"); found {
		name = last
	}
	a.folderRename = folderRenameState{path: path, name: name, focus: true}
	a.invalidate()
}

// cancelFolderRename leaves the saved name untouched.
func (a *app) cancelFolderRename(path string) {
	if a.folderRename.path != path {
		return
	}
	a.folderRename = folderRenameState{}
	a.invalidate()
}

// commitFolderRename writes the name typed in the tree. An empty or unchanged
// name closes the editor; a refused rename warns and leaves the input open so
// the reader can correct it. path keeps a late blur from committing another
// row's draft after the reader has started editing it.
func (a *app) commitFolderRename(path string) {
	st := &a.folderRename
	if st.path != path {
		return
	}
	from, name := st.path, strings.TrimSpace(st.name)
	if name == "" {
		a.cancelFolderRename(path)
		return
	}
	if _, last, found := strings.CutLast(from, "/"); found && name == last || !found && name == from {
		*st = folderRenameState{}
		a.invalidate()
		return
	}
	if err := a.renameFolder(from, name); err != nil {
		a.warn(locales.NotRenamedWith(err.Error()))
		a.invalidate()
		return
	}
	*st = folderRenameState{}
	a.invalidate()
}

// folderName edits a collection's or folder's name in place when its menu's
// Rename starts it; Enter or leaving it commits, and Escape leaves the saved
// name alone. A plain click still belongs to the tree row, not to renaming.
func (a *app) folderName(c *ui.Context, path, name string, matches []fuzzy.Match) {
	t := c.Theme()
	if a.folderRename.path == path {
		// Bare: the inline rename is the tree row itself, at row height and with the accent edge, not the kit's field face.
		input := ui.TextInput(c, &a.folderRename.name).
			Label(locales.RenameNamed(name)).
			Height(uikit.HeightRow).Grow(1).MinWidth(0).
			FontSize(uikit.Scaled(uikit.TextBody)).
			Padding(0, t.Space(uikit.SpaceS)).
			Radius(uikit.RadiusInner).FocusRing(false).
			Background(t.Background).Border(uikit.HairlineWidth, t.Accent)
		if a.folderRename.focus {
			input.AutoFocus()
			a.folderRename.focus = false
		}
		input.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind == ui.InputKeyDown && ev.Key == ui.KeyEscape {
				a.cancelFolderRename(path)
				return true
			}
			return false
		})
		switch {
		case input.Submitted():
			a.commitFolderRename(path)
		case input.Focused() || input.FocusWithin():
			a.folderRename.focused = true
		case a.folderRename.focused:
			a.commitFolderRename(path)
		}
		return
	}
	fuzzyListBody(c, name, matches).SingleLine().Grow(1)
}

// newRequestIn opens an empty request already filed under collection, as a
// New Request on that collection's menu does.
func (a *app) newRequestIn(collection string) {
	a.openCompose()
	if d := a.requestDocument; d != nil {
		d.composeCollection = collection
	}
}

// newFolderIn opens the folder editor on a new path under parent. Saving it
// creates the folder; Cancel leaves the tree as it was.
func (a *app) newFolderIn(parent string) {
	base := locales.NewFolder()
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s %d", base, n)
		}
		path := name
		if parent != "" {
			path = parent + "/" + name
		}
		exists := false
		for _, f := range a.folders {
			if f.Path == path {
				exists = true
				break
			}
		}
		if !exists {
			a.openFolder(path)
			return
		}
	}
}

// duplicateSaved opens a copy of a saved request as a new unsaved draft.
func (a *app) duplicateSaved(r savedRequest) {
	before := a.snapshotDraftsUndo()
	duplicate := r
	duplicate.ID, duplicate.Name = "", uniqueRequestName(a.requests, r.Name+" copy")
	a.addDocument(duplicate, "")
	after := a.snapshotDraftsUndo()
	a.recordUndo(undoRecord{code: "draft.duplicate", label: locales.DuplicateAsNewRequest(),
		before: before, after: after})
}

// collectionTree uses full paths for identity and short names for presentation.
// Search opens matching ancestors without rewriting the user's collapsed state.
// VS Code's tree FindFilter (src/vs/base/browser/ui/tree/abstractTree.ts)
// scores in place, so matching requests and folders stay in their path order
// and only the label's matched runes are highlighted.
func (a *app) collectionTree(c *ui.Context) {
	query := strings.TrimSpace(a.requestQuery)
	source := strconv.Itoa(a.requestGen)
	requests := a.fuzzyList(query, "collection-tree-request", source, len(a.requests), func(i int) fuzzyListItem {
		r := a.requests[i]
		return fuzzyListItem{label: r.Name, detail: r.Collection + " " + r.URL + " " + cmp.Or(r.Protocol, r.Method)}
	}, false)
	folders := a.fuzzyList(query, "collection-tree-folder", source, len(a.folders), func(i int) fuzzyListItem {
		path := a.folders[i].Path
		_, name, found := strings.CutLast(path, "/")
		if !found {
			name = path
		}
		return fuzzyListItem{label: name, detail: path}
	}, false)
	requestMatches := make(map[string][]fuzzy.Match, len(requests))
	folderMatches := make(map[string][]fuzzy.Match, len(folders))
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
	for _, hit := range requests {
		requestMatches[a.requests[hit.index].key()] = hit.matches
		addPath(a.requests[hit.index].Collection)
	}
	for _, hit := range folders {
		path := a.folders[hit.index].Path
		folderMatches[path] = hit.matches
		addPath(path)
	}
	tree := ui.TreeBase(c)
	var branch func(string, int)
	branch = func(parent string, depth int) {
		children := folderOrder(parent, paths, a.library.folderOrder)
		for _, path := range children {
			_, name, found := strings.CutLast(path, "/")
			if !found {
				name = path
			}
			open := a.collectionShown(path)
			if query != "" {
				open = new(true)
			}
			folderDrag := libraryDrag{kind: folderItem, id: path}
			menu := func(m *ui.Menu) {
				mock := locales.MockFromExamples(path)
				if slices.ContainsFunc(a.rules, func(r capture.Rule) bool { return r.Scenario == path }) {
					mock = locales.RebuildScenarioFromExamples(path)
				}
				actions := append([]action{
					do("collection.new-request", locales.NewRequest(), func() { a.newRequestIn(path) }).in("create"),
					do("collection.new-folder", locales.NewFolder(), func() { a.newFolderIn(path) }).in("create"),
					do("collection.rename", locales.RenameNamed(name), func() { a.startFolderRename(path) }).in("edit"),
					do("collection.docs", locales.ViewDocumentation(), func() { a.openDocs(path) }).in("open"),
					do("collection.history", locales.LocalHistoryEllipsis(), func() { a.openFolderHistory(path) }).in("open"),
				}, a.folderSettingsActions(path)...)
				serve := do("collection.mockserve", locales.StartMockServer(), func() { a.startCollectionMock(path) }).
					when(a.examplesUnder(path), locales.SaveAResponseAsAn()).in("edit")
				if running := a.mocks[path]; running != nil {
					serve = do("collection.mockstop", locales.StopMockServerAt(running.url), func() { a.stopCollectionMock(path) }).in("edit")
				}
				actionMenu(m, append(actions,
					serve,
					do("collection.mock", mock, func() { a.scenarioFromExamples(path) }).
						when(a.examplesUnder(path), locales.SaveAResponseAsAn()).in("edit"),
					do("library.move", locales.MoveToEllipsis(), func() { a.openMoveTo(folderDrag) }).in("edit"),
					do("collection.delete", locales.DeletePathEllipsis(path), func() { a.confirmDeleteFolder(path) }).in("danger")))
			}
			parts := tree.Item(open)
			row := treeRow(c, parts, depth).Label(locales.CollectionPath(path)).
				Drag(folderDrag).PopoverMenu(menu)
			if row.Dragging() {
				row.Opacity(0.5)
			}
			a.queueTreeDrop(c, row, folderDrag, true, parent, paths)
			row.Children(func() {
				branchArrow(c, open)
				a.folderName(c, path, name, folderMatches[path])
				if a.folderRename.path == path {
					return
				}
				action := uikit.MoreMenu(c, locales.CollectionActionsPrefix(path), menu).
					Absolute().Right(uikit.RowActionInset).Top(uikit.RowActionInset).Background(uikit.Colors(c.Theme()).Window)
				if !row.Hovered() && !row.FocusWithin() {
					action.Opacity(0)
				}
			})
			parts.Group(func() { branch(path, depth+1) })
		}
		for _, r := range a.requests {
			requestMatch, shown := requestMatches[r.key()]
			method := cmp.Or(r.Protocol, r.Method)
			if r.Collection != parent || !shown {
				continue
			}
			requestDrag := libraryDrag{kind: requestItem, id: r.key()}
			menu := func(m *ui.Menu) {
				actionMenu(m, append(append([]action{
					do("request.open", locales.Open(), func() { a.openSaved(r.key()) }).in("open"),
					do("request.history", locales.LocalHistoryEllipsis(), func() { a.openRequestHistory(r.key()) }).in("open"),
					do("request.rename", locales.RenameRequest(), func() {
						a.openSaved(r.key())
						a.startRequestRename(a.requestDocument)
					}).in("edit"),
					do("request.duplicate", locales.DuplicateAsNewRequest(), func() { a.duplicateSaved(r) }).in("edit"),
				}, a.folderSettingsActions(r.Collection)...),
					do("library.move", locales.MoveToEllipsis(), func() { a.openMoveTo(requestDrag) }).in("edit"),
					do("request.delete", locales.DeleteRequest(), func() { a.confirmDeleteRequest(r.key(), r.Name) }).in("danger")))
			}
			// The sidebar has one choice: a request only while it is shown.
			chosen := a.page == pageRequest && a.requestChoice == r.key()
			edited := slices.ContainsFunc(a.documents, func(d *requestDocument) bool { return d.savedKey == r.key() && d.dirty() })
			base := treeRow(c, tree.Item(nil), depth).Label(r.Name).Selected(chosen).
				Drag(requestDrag)
			if base.Dragging() {
				base.Opacity(0.5)
			}
			a.queueTreeDrop(c, base, requestDrag, false, parent, paths)
			// The row shows the name and method; the card carries the whole
			// address and where the request lives.
			card := uikit.HoverCard{Title: r.Name, Meta: method, Rows: []uikit.HoverCardRow{{Key: "URL", Value: r.URL}}}
			if r.Collection != "" {
				card.Meta = method + " · " + r.Collection
			}
			row := uikit.CardTipBeside(c, base, card).
				OnClick(func() { a.previewSaved(r.key()) }).PopoverMenu(menu)
			// A single click browses in the preview tab; a double click pins
			// it, as opening the request from its menu does.
			if row.DoubleClicked() {
				a.openSaved(r.key())
			}
			if chosen {
				uikit.Selected(c, row, !a.windowBehind)
			}
			row.Children(func() {
				// The arrow's slot, empty: a request's method and name start
				// where a folder's name does, at every depth.
				ui.Box(c).Size(uikit.IconLarge, uikit.IconLarge).Shrink(0)
				ui.Row(c).Grow(1).FillHeight().Gap(c.Theme().Space(uikit.SpaceM)).AlignItems(ui.Center).Label(method + "  " + r.Name).Children(func() {
					methodCell(c, method, a.treeMethodSlot())
					fuzzyListBody(c, r.Name, requestMatch).SingleLine().Grow(1)
					if edited {
						unsavedDot(c)
					}
					action := uikit.MoreMenu(c, locales.RequestActionsPrefix(r.Name), menu).
						Absolute().Right(uikit.RowActionInset).Top(uikit.RowActionInset).Background(uikit.Colors(c.Theme()).Window)
					// The request being edited keeps its actions in sight; the others
					// wait for the pointer or for focus to enter the row.
					if !chosen && !base.Hovered() && !row.FocusWithin() {
						action.Opacity(0)
					}
				})
			})
		}
	}
	tree.Root.Gap(0).Children(func() {
		a.unsavedRequests(c, tree, query)
		branch("", 0)
		// Nothing saved yet: the tree offers the one thing a collection
		// needs, a request, instead of a sentence about where one would go.
		if len(a.requests) == 0 && len(paths) == 0 {
			item := tree.Item(nil)
			row := treeRow(c, item, 0).Label(locales.NewRequest()).OnClick(a.openCompose)
			row.Children(func() {
				ui.Row(c).Width(uikit.IconLarge).Shrink(0).Justify(ui.Center).Children(func() {
					ui.Icon(c, plusIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(c.Theme().TextMuted)
				})
				uikit.Body(c, locales.NewRequest()).SingleLine().Grow(1)
			})
		}
	})
}

// unsavedGroup keys the Unsaved group's open state among the collections'.
const unsavedGroup = "\x00unsaved"

// unsavedRequests lists the open requests no collection holds yet, above the
// collections: with one request on screen at a time, this is where a new
// one waits while another is shown.
func (a *app) unsavedRequests(c *ui.Context, tree ui.TreeParts, query string) {
	hits := a.fuzzyList(query, "collection-tree-unsaved", strconv.Itoa(len(a.documents))+":"+strconv.Itoa(a.requestGen), len(a.documents), func(i int) fuzzyListItem {
		d := a.documents[i]
		return fuzzyListItem{label: d.title(), detail: d.composeURL + " " + cmp.Or(d.protocol, d.composeMethod)}
	}, false)
	unsaved := make([]*requestDocument, 0, len(hits))
	matches := make(map[*requestDocument][]fuzzy.Match, len(hits))
	for _, hit := range hits {
		d := a.documents[hit.index]
		if d.savedKey != "" {
			continue
		}
		unsaved = append(unsaved, d)
		matches[d] = hit.matches
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
				OnClick(func() { a.activateDocument(d) })
			if chosen {
				uikit.Selected(c, item, !a.windowBehind)
			}
			item.Children(func() {
				methodCell(c, method, a.treeMethodSlot())
				fuzzyListBody(c, d.title(), matches[d]).SingleLine().Grow(1)
			})
		}
	})
}

// unsavedDot marks a request whose open document has changes to save.
func unsavedDot(c *ui.Context) {
	ui.Box(c).Size(6, 6).Radius(uikit.RadiusControl).Background(c.Theme().Accent).Label(locales.UnsavedChanges())
}
