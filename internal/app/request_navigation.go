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
	ui.Column(c).Width(requestRailWidth).FillHeight().Padding(4).Gap(4).
		Background(t.Surface).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
		for _, item := range []struct {
			label string
			icon  *ui.SVG
			side  int
		}{{"Collections", layersIcon, 0}, {"Envs", domainIcon, 2}, {"Runs", sendHistoryIcon, 1}, {"APIs", apiIcon, 3}} {
			button := ui.ButtonBase(c.Key(item.label)).Label(item.label).Height(56).FillWidth().Radius(5).
				Column().Justify(ui.Center).AlignItems(ui.Center).Gap(5).TextColor(t.TextMuted)
			if a.requestSide == item.side {
				button.Background(t.Accent.Alpha(0.1)).TextColor(t.Accent)
			} else if button.Hovered() {
				button.Background(t.SurfaceHover)
			}
			button.Children(func() {
				ui.Icon(c, item.icon).Size(18, 18)
				ui.Text(c, item.label).FontSize(10)
			})
			button.OnClick(func() {
				a.requestSide, a.requestQuery = item.side, ""
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
			ui.TreeItem(c.Key("folder:"+path), name, open, func() { branch(path) }).Height(32).
				Label("Collection: " + path).ContextMenu(func(m *ui.Menu) {
				a.folderSettingsItems(m, path)
				if m.Item("Delete “" + path + "”…").Chosen() {
					a.confirmDeleteFolder(path)
				}
			})
		}
		for _, r := range a.requests {
			if r.Collection != parent || query != "" && !strings.Contains(strings.ToLower(r.Collection+" "+r.Name+" "+r.URL+" "+r.Method), query) {
				continue
			}
			item := ui.TreeItem(c.Key(r.key()), "", nil, nil).Label(r.Name).Height(32).
				Selected(a.requestChoice == r.key()).Tooltip(r.Method + " " + r.URL).
				OnClick(func() { a.openSaved(r.key()) }).ContextMenu(func(m *ui.Menu) {
				if m.Item("Delete request…").Chosen() {
					a.confirmDeleteRequest(r.key(), r.Name)
				}
				m.Separator()
				a.folderSettingsItems(m, r.Collection)
			})
			if a.requestChoice == r.key() {
				item.Background(c.Theme().Accent.Alpha(0.10)).TextColor(c.Theme().Text)
			}
			item.Children(func() {
				ui.Row(c).Gap(8).Label(r.Method + "  " + r.Name).Children(func() {
					ui.Text(c, r.Method).FontSize(11).Bold().TextColor(methodColor(r.Method)).Width(38).SingleLine()
					ui.Text(c, r.Name).FontSize(13).SingleLine()
				})
			})
		}
	}
	ui.Tree(c, func() { branch("") })
	if len(a.requests) == 0 && len(paths) == 0 {
		ui.Text(c, "Save a request here, or edit a captured flow to start.").FontSize(12).TextColor(c.Theme().TextMuted).Padding(8)
	}
}

func (a *app) environmentSidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Surface).Children(func() {
		ui.Row(c).Height(40).Padding(0, 10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Environments").Bold().Grow(1)
			if ui.Button(c, "+").Label("Manage environments…").Clicked() {
				a.openVars()
			}
		})
		ui.Scroll(c).Grow(1).Padding(6).Gap(4).Children(func() {
			for _, name := range a.envNames() {
				ui.Button(c, cmp.Or(name, "No environment")).FillWidth().OnClick(func() { a.editEnvironment(name) })
			}
			if ui.Button(c, "Globals").FillWidth().Clicked() {
				a.editGlobals()
			}
		})
		if ui.ButtonBase(c).Label("Vault…").Height(32).Padding(8).Gap(6).Children(func() {
			ui.Icon(c, lucide.Lock()).Size(14, 14)
			ui.Text(c, "Vault…")
		}).Clicked() {
			a.openVault()
		}
	})
}
