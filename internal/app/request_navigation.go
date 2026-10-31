package app

import (
	"github.com/baba983/vscroxy/internal/uikit"
	"slices"
	"strings"

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
		if query == "" || strings.Contains(strings.ToLower(r.Collection+" "+r.Name+" "+r.URL+" "+r.Method), query) {
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
				a.folderSettingsItems(m, path)
				if m.Item("Delete “" + path + "”…").Chosen() {
					a.confirmDeleteFolder(path)
				}
			}
			item := ui.TreeItem(c.Key("folder:"+path), "", open, func() { branch(path) }).Height(uikit.HeightControl).
				Label("Collection: " + path).PopoverMenu(menu)
			item.Children(func() {
				ui.Icon(c, lucide.Folder()).Size(uikit.IconInline, uikit.IconInline).TextColor(c.Theme().TextMuted)
				uikit.Body(c, name).SingleLine().Grow(1)
				action := uikit.IconButton(c, "Collection actions: "+path, "Collection actions", moreIcon).PopoverMenuButton(menu).
					Absolute().Right(0).Top(2).Background(uikit.Colors(c.Theme()).Panel)
				if !item.Hovered() && !item.FocusWithin() {
					action.Opacity(0)
				}
			})
		}
		for _, r := range a.requests {
			if r.Collection != parent || query != "" && !strings.Contains(strings.ToLower(r.Collection+" "+r.Name+" "+r.URL+" "+r.Method), query) {
				continue
			}
			menu := func(m *ui.Menu) {
				if m.Item("Delete request…").Chosen() {
					a.confirmDeleteRequest(r.key(), r.Name)
				}
				m.Separator()
				a.folderSettingsItems(m, r.Collection)
			}
			// The sidebar has one choice: a request only while it is shown.
			chosen := a.page == pageRequest && a.requestChoice == r.key()
			item := ui.TreeItem(c.Key(r.key()), "", nil, nil).Label(r.Name).Height(uikit.HeightControl).
				Selected(chosen).Tooltip(r.Method + " " + r.URL).
				OnClick(func() { a.openSaved(r.key()) }).PopoverMenu(menu)
			if chosen {
				uikit.Selected(c, item, true)
			}
			item.Children(func() {
				ui.Row(c).Grow(1).FillHeight().Gap(c.Theme().Space(2)).AlignItems(ui.Center).Label(r.Method + "  " + r.Name).Children(func() {
					uikit.MethodLabel(c, r.Method).FontSize(uikit.TextMeta).Width(38).Height(16)
					uikit.Body(c, r.Name).SingleLine().Grow(1)
					action := uikit.IconButton(c, "Request actions: "+r.Name, "Request actions", moreIcon).PopoverMenuButton(menu).
						Absolute().Right(0).Top(2).Background(uikit.Colors(c.Theme()).Panel)
					if !item.Hovered() && !item.FocusWithin() {
						action.Opacity(0)
					}
				})
			})
		}
	}
	ui.Tree(c, func() { branch("") })
	if len(a.requests) == 0 && len(paths) == 0 {
		ui.Text(c, "Save a request here, or edit a captured flow to start.").FontSize(uikit.TextBody).TextColor(c.Theme().TextMuted).Padding(c.Theme().Space(2))
	}
}
