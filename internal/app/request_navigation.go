package app

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

const requestRailWidth = 72

// The rail chooses resources, not documents. Switching it never replaces a draft.
func (a *app) requestRail(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(requestRailWidth).FillHeight().Padding(t.Space(1)).Gap(t.Space(1)).
		Background(t.Surface).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
		for _, item := range []struct {
			label string
			icon  *ui.SVG
			side  int
		}{{"Collections", layersIcon, 0}, {"Envs", domainIcon, 2}, {"Runs", sendHistoryIcon, 1}, {"APIs", apiIcon, 3}} {
			button := ui.ButtonBase(c.Key(item.label)).Label(item.label).Tooltip(item.label).Height(56).FillWidth().Radius(radiusControl).
				Column().Justify(ui.Center).AlignItems(ui.Center).Gap(t.Space(1)).TextColor(t.TextMuted)
			if a.requestSide == item.side {
				button.Background(t.Accent.Alpha(0.1)).TextColor(t.Accent)
			} else if button.Hovered() {
				button.Background(t.SurfaceHover)
			}
			button.Children(func() {
				ui.Icon(c, item.icon).Size(iconLarge, iconLarge)
				ui.Text(c, item.label).FontSize(textMeta)
			})
			button.OnClick(func() {
				a.requestSide, a.requestQuery = item.side, ""
				a.workbench.requestSidebarHidden = false
				switch item.side {
				case 0, 2:
					a.requestsSection = 0
				case 1:
					a.requestsSection = 2
					a.experiments.scope, a.experiments.draftKey = nil, ""
				case 3:
					a.requestsSection = 1
				}
			})
		}
		ui.Spacer(c)
		label := "Hide request sidebar"
		if a.workbench.requestSidebarHidden {
			label = "Show request sidebar"
		}
		ui.ButtonBase(c).Label(label).Tooltip(label).Height(heightLarge).FillWidth().Radius(radiusControl).
			Disabled(a.workbench.sidebarHidden || a.requestsSection == 2).Center().
			Children(func() { ui.Icon(c, lucide.PanelLeft()).Size(iconLarge, iconLarge).TextColor(t.TextMuted) }).
			OnClick(func() { a.workbench.requestSidebarHidden = !a.workbench.requestSidebarHidden })
	})
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
			item := ui.TreeItem(c.Key("folder:"+path), "", open, func() { branch(path) }).Height(heightControl).
				Label("Collection: " + path).PopoverMenu(menu)
			item.Children(func() {
				ui.Icon(c, lucide.Folder()).Size(iconInline, iconInline).TextColor(c.Theme().TextMuted)
				ui.Text(c, name).FontSize(textBody).SingleLine().Grow(1)
				action := iconButton(c, "Collection actions: "+path, "Collection actions", moreIcon).PopoverMenuButton(menu).
					Absolute().Right(0).Top(2).Background(c.Theme().Surface)
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
			item := ui.TreeItem(c.Key(r.key()), "", nil, nil).Label(r.Name).Height(heightControl).
				Selected(a.requestChoice == r.key()).Tooltip(r.Method + " " + r.URL).
				OnClick(func() { a.openSaved(r.key()) }).PopoverMenu(menu)
			if a.requestChoice == r.key() {
				item.Background(c.Theme().Accent.Alpha(0.10)).TextColor(c.Theme().Text)
			}
			item.Children(func() {
				ui.Row(c).Grow(1).FillHeight().Gap(c.Theme().Space(2)).AlignItems(ui.Center).Label(r.Method + "  " + r.Name).Children(func() {
					methodLabel(c, r.Method).FontSize(textMeta).Width(38).Height(16)
					ui.Text(c, r.Name).FontSize(textBody).SingleLine().Grow(1)
					action := iconButton(c, "Request actions: "+r.Name, "Request actions", moreIcon).PopoverMenuButton(menu).
						Absolute().Right(0).Top(2).Background(c.Theme().Surface)
					if !item.Hovered() && !item.FocusWithin() {
						action.Opacity(0)
					}
				})
			})
		}
	}
	ui.Tree(c, func() { branch("") })
	if len(a.requests) == 0 && len(paths) == 0 {
		ui.Text(c, "Save a request here, or edit a captured flow to start.").FontSize(textBody).TextColor(c.Theme().TextMuted).Padding(c.Theme().Space(2))
	}
}

func (a *app) environmentSidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Surface).Children(func() {
		ui.Row(c).Height(40).Padding(0, t.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Environments").FontSize(textBody).FontWeight(600).Grow(1)
			if iconButton(c, "Manage environments…", "Manage environments", plusIcon).Clicked() {
				a.openVars()
			}
		})
		ui.Scroll(c).Grow(1).Padding(t.Space(2)).Children(func() {
			ui.Tree(c, func() {
				for _, name := range a.envNames() {
					item := ui.TreeItem(c.Key(name), "", nil, nil).Label(cmp.Or(name, "No environment")).Height(heightControl).
						Selected(name == a.activeEnv).OnClick(func() { a.editEnvironment(name) })
					if name == a.activeEnv {
						item.Background(t.Accent.Alpha(0.1))
					}
					item.Children(func() {
						ui.Box(c).Size(6, 6).Radius(radiusControl).Background(t.Accent)
						ui.Text(c, cmp.Or(name, "No environment")).FontSize(textBody).SingleLine()
					})
				}
				ui.TreeItem(c, "Globals", nil, nil).Height(heightControl).FontSize(textBody).OnClick(a.editGlobals)
			})
		})
		if toolbarButton(c, "Vault…", "Manage secrets", false).FillWidth().Children(func() {
			ui.Icon(c, lucide.Lock()).Size(iconInline, iconInline)
			ui.Text(c, "Vault…")
		}).Clicked() {
			a.openVault()
		}
	})
}
