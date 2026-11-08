package app

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

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
	var branch func(string)
	branch = func(parent string) {
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
			item := ui.TreeItem(c.Key("folder:"+path), "", open, func() { branch(path) }).Height(uikit.HeightControl).
				Label("Collection: " + path).PopoverMenu(menu)
			item.Children(func() {
				uikit.Glyph(c, lucide.Folder())
				uikit.Body(c, name).SingleLine().Grow(1)
				action := uikit.IconButton(c, "Collection actions: "+path, locales.CollectionActions(), moreIcon).PopoverMenuButton(menu).
					Absolute().Right(0).Top(2).Background(uikit.Colors(c.Theme()).Panel)
				if !item.Hovered() && !item.FocusWithin() {
					action.Opacity(0)
				}
			})
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
			item := ui.TreeItem(c.Key(r.key()), "", nil, nil).Label(r.Name).Height(uikit.HeightControl).
				Selected(chosen).Tooltip(method + " " + r.URL).
				OnClick(func() { a.openSaved(r.key()) }).PopoverMenu(menu)
			if chosen {
				uikit.Selected(c, item, !a.windowBehind)
			}
			item.Children(func() {
				ui.Row(c).Grow(1).FillHeight().Gap(c.Theme().Space(2)).AlignItems(ui.Center).Label(method + "  " + r.Name).Children(func() {
					uikit.MethodLabel(c, method).FontSize(uikit.TextMeta).MinWidth(38).Shrink(0).Height(16)
					uikit.Body(c, r.Name).SingleLine().Grow(1)
					if edited {
						unsavedDot(c)
					}
					action := uikit.IconButton(c, "Request actions: "+r.Name, locales.RequestActions(), moreIcon).PopoverMenuButton(menu).
						Absolute().Right(0).Top(2).Background(uikit.Colors(c.Theme()).Panel)
					if !item.Hovered() && !item.FocusWithin() {
						action.Opacity(0)
					}
				})
			})
		}
	}
	ui.Tree(c, func() {
		a.unsavedRequests(c, query)
		branch("")
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
func (a *app) unsavedRequests(c *ui.Context, query string) {
	t := c.Theme()
	var unsaved []*requestDocument
	for _, d := range a.documents {
		if d.savedKey == "" && (query == "" || strings.Contains(strings.ToLower(d.title()+" "+d.composeURL+" "+cmp.Or(d.protocol, d.composeMethod)), query)) {
			unsaved = append(unsaved, d)
		}
	}
	if len(unsaved) == 0 {
		return
	}
	group := ui.TreeItem(c.Key("unsaved"), "", a.collectionShown(unsavedGroup), func() {
		for _, d := range unsaved {
			method := cmp.Or(d.protocol, d.composeMethod)
			chosen := a.page == pageRequest && a.requestDocument == d
			item := ui.TreeItem(c.Key(fmt.Sprint("unsaved:", d.id)), "", nil, nil).Label("Unsaved: " + d.title()).Height(uikit.HeightControl).
				Selected(chosen).Tooltip(method + " " + d.composeURL).
				OnClick(func() { a.requestDocument, a.requestChoice, a.page = d, "", pageRequest })
			if chosen {
				uikit.Selected(c, item, !a.windowBehind)
			}
			item.Children(func() {
				ui.Row(c).Grow(1).FillHeight().Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					uikit.MethodLabel(c, method).FontSize(uikit.TextMeta).MinWidth(38).Shrink(0).Height(16)
					uikit.Body(c, d.title()).SingleLine().Grow(1)
				})
			})
		}
	}).Height(uikit.HeightControl).Label(locales.UnsavedRequests())
	group.Children(func() {
		uikit.Glyph(c, lucide.FilePen())
		uikit.Body(c, locales.Unsaved()).SingleLine().Grow(1)
		uikit.Meta(c, fmt.Sprint(len(unsaved))).FontFeatures("tnum")
	})
}

// unsavedDot marks a request whose open document has changes to save.
func unsavedDot(c *ui.Context) {
	ui.Box(c).Size(6, 6).Radius(uikit.RadiusControl).Background(c.Theme().Accent).Label(locales.UnsavedChanges())
}
