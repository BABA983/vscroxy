package app

import (
	"cmp"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

var collectionSteps = []string{"Scope", "Endpoints", "Variables", "Structure", "Examples", "Done"}

func (a *app) collectionWizardModal(c *ui.Context, t *ui.Theme) {
	w := &a.collectionUI
	if !w.open {
		return
	}
	_, height := c.Size()
	dialog := uikit.Modal(c, &w.open, func() {
		c.OnShortcut(0, ui.KeyEscape, func() { w.open = false })
		uikit.DialogColumn(c, 1080).Height(min(620, height-100)).Gap(t.Space(3)).Children(func() {
			uikit.DialogTitle(c, "Create collection")
			ui.Row(c).Gap(t.Space(2)).FillWidth().Children(func() {
				for i, label := range collectionSteps {
					ui.Row(c.Key(label)).Grow(1).Gap(t.Space(2)).Height(36).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
						mark := strconv.Itoa(i + 1)
						if i < w.step {
							mark = "✓"
						}
						color, background := t.TextMuted, t.Surface
						if i <= w.step {
							color, background = t.AccentText, t.Accent
						}
						ui.Box(c).Size(22, 22).Radius(11).Background(background).Border(1, t.Border).Center().Children(func() { ui.Text(c, mark).FontSize(uikit.TextMeta).TextColor(color) })
						text := uikit.Body(c, label).SingleLine()
						if i == w.step {
							text.TextColor(t.Accent).FontWeight(600)
						}
					}).BorderColor(func() ui.Color {
						if i == w.step {
							return t.Accent
						}
						return t.Border
					}())
				}
			})
			ui.Row(c).Grow(1).MinHeight(0).Gap(t.Space(4)).AlignItems(ui.Stretch).Disabled(w.building != nil).Children(func() {
				ui.Scroll(c.Key("settings-" + strconv.Itoa(w.step))).Basis(0).Grow(1).MinWidth(0).MinHeight(0).Gap(t.Space(3)).Children(func() {
					switch w.step {
					case 0:
						a.collectionScopeEditor(c, t)
					case 1:
						a.collectionEndpointsEditor(c, t)
					case 2:
						a.collectionVariablesEditor(c, t)
					case 3:
						a.collectionStructureEditor(c, t)
					case 4:
						a.collectionExamplesEditor(c, t)
					case 5:
						uikit.DialogTitle(c, "Ready to create")
						n, examples := 0, 0
						for _, g := range w.groups {
							if g.include {
								n++
								for _, i := range g.flows {
									if w.examples[i] {
										examples++
									}
								}
							}
						}
						uikit.Body(c, fmt.Sprintf("%d requests · %d examples", n, examples))
						uikit.Body(c, "Collection: "+cmp.Or(w.destination, w.name))
						uikit.Body(c, "Environment: "+w.environmentLabel())
						ui.Checkbox(c, &w.generateAPI, "Also generate OpenAPI draft").FontSize(uikit.TextBody)
						uikit.Hint(c, "The API draft opens in APIs for review; it is not a verified contract.")
						uikit.Hint(c, "Requests and examples may contain sensitive query and body values. Review them before sharing.")
					}
				})
				ui.Column(c.Key("preview-"+strconv.Itoa(w.step))).Basis(0).Grow(1.15).MinWidth(0).MinHeight(0).Padding(t.Space(3)).Gap(t.Space(3)).Background(uikit.Colors(t).Panel).Border(1, t.Border).Radius(uikit.RadiusPanel).Children(func() {
					if w.step == 3 || w.step == 5 {
						a.collectionWizardTree(c, t)
						return
					}
					if w.step == 0 {
						uikit.DialogTitle(c, "Hosts in scope")
						ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(2)).Children(func() {
							for _, h := range w.hosts {
								uikit.Mono(c, h.origin).SingleLine()
							}
						})
						return
					}
					if len(w.groups) == 0 {
						uikit.Hint(c, "No endpoints in this scope")
						return
					}
					w.active = min(w.active, len(w.groups)-1)
					g := w.groups[w.active]
					uikit.DialogTitle(c, g.method+" "+g.template).Font(uikit.FontMono).SingleLine()
					uikit.Meta(c, fmt.Sprintf("%s · %d captured requests", g.origin, len(g.flows))).SingleLine()
					if w.step == 1 || w.step == 4 {
						ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(2)).Children(func() {
							for _, i := range g.flows {
								f := w.flows[i]
								ui.Row(c.Key(i)).Gap(t.Space(2)).Height(uikit.HeightLarge).Children(func() {
									uikit.Mono(c, cmp.Or(f.path, f.url)).Grow(1).MinWidth(0).SingleLine().Tooltip(f.url)
									ui.Text(c, strconv.Itoa(f.status)).Font(uikit.FontMono).FontSize(uikit.TextData).TextColor(uikit.StatusColor(f.status, t)).Width(32)
									uikit.Meta(c, fmt.Sprintf("%d ms", f.millis)).Width(52)
								})
							}
						})
						return
					}
					// Never read a spilled request just to paint a preview.
					f := w.latest(g)
					if f.reqFile != "" {
						uikit.Hint(c, "Request body is on disk; it will be read when you create the collection.")
						return
					}
					r, err := w.requestFor(g)
					if err != nil {
						uikit.Hint(c, err.Error())
						return
					}
					preview := r.Method + " " + r.URL + "\n\n" + r.Headers
					// Do not echo credentials even when Leave in requests is selected.
					for _, credential := range w.credentials {
						preview = strings.ReplaceAll(preview, credential.value, "••••")
					}
					ui.Scroll(c).Grow(1).MinHeight(0).Children(func() { uikit.Mono(c, preview+"\n\n"+r.Body).Selectable() })
					uikit.Meta(c, "Environment: "+w.environmentLabel())
				})
			})
			if w.problem != "" {
				ui.Text(c, w.problem).FontSize(uikit.TextMeta).TextColor(t.Danger)
			}
			uikit.DialogFooter(c).Children(func() {
				message := "Nothing is saved until Create."
				if w.building != nil {
					message = "Preparing requests and examples…"
				}
				uikit.Meta(c, message).Grow(1)
				uikit.SecondaryButton(c, "Cancel").OnClick(func() { w.open = false })
				if w.step > 0 {
					uikit.SecondaryButton(c, "Back").Disabled(w.building != nil).OnClick(func() { w.step--; w.problem = "" })
				}
				if w.step < 5 {
					uikit.PrimaryButton(c, "Next: "+collectionSteps[w.step+1]).Disabled(len(w.groups) == 0).OnClick(func() { w.step++; w.problem = "" })
				} else {
					uikit.PrimaryButton(c, "Create").Disabled(w.building != nil).OnClick(a.createCollection)
				}
			})
		})
	})
	// Outside clicks must not discard six steps of draft edits. Escape still
	// cancels through the explicit command inside the dialog.
	if dialog.Dismissed() {
		w.open = true
	}
}

func (w *collectionWizard) environmentLabel() string {
	if w.environment == "New environment" {
		return w.environmentName
	}
	return w.environment
}

func (a *app) collectionScopeEditor(c *ui.Context, t *ui.Theme) {
	w := &a.collectionUI
	uikit.DialogTitle(c, "Choose scope")
	uikit.Body(c, fmt.Sprintf("%d captured requests", len(w.flows)))
	ui.Checkbox(c, &w.static, "Exclude static resources").FontSize(uikit.TextBody).OnChange(w.regroup)
	ui.Checkbox(c, &w.tunnels, "Exclude CONNECT tunnels").FontSize(uikit.TextBody).OnChange(w.regroup)
	ui.Checkbox(c, &w.failed, "Exclude network failures").FontSize(uikit.TextBody).OnChange(w.regroup)
	uikit.Hint(c, "HTTP 4xx and 5xx responses are kept for error examples. Streams and non-HTTP traffic cannot become requests.")
	uikit.Body(c, fmt.Sprintf("%d included · %d omitted · %d endpoints", len(w.scope()), len(w.flows)-len(w.scope()), len(w.groups)))
}

func (a *app) collectionEndpointsEditor(c *ui.Context, t *ui.Theme) {
	w := &a.collectionUI
	uikit.DialogTitle(c, "Merge endpoints")
	uikit.Hint(c, "Same method, host and path template become one request.")
	for i, g := range w.groups {
		ui.Row(c.Key(g.id)).Gap(t.Space(2)).Height(uikit.HeightLarge).Children(func() {
			ui.Checkbox(c, &g.include, "").Label("Include " + g.method + " " + g.template)
			uikit.ToolbarButton(c, "Endpoint "+g.method+" "+g.template, g.origin, i == w.active).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, g.method).Font(uikit.FontMono).FontSize(uikit.TextData).TextColor(uikit.MethodColor(g.method, t)).Width(46)
				uikit.Mono(c, g.template).Grow(1).MinWidth(0).SingleLine()
				uikit.Meta(c, fmt.Sprintf("×%d", len(g.flows)))
			}).OnClick(func() { w.active = i })
		})
	}
	if len(w.groups) == 0 {
		return
	}
	g := w.groups[min(w.active, len(w.groups)-1)]
	ui.Divider(c)
	uikit.Body(c, "Path template")
	uikit.Input(c, &g.template).Label("Path template").Font(uikit.FontMono)
	uikit.Hint(c, "Use :id for a whole path segment. Rename parameters here; literal segments are kept unchanged.")
	ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
		uikit.SecondaryButton(c, "Split").Disabled(len(g.flows) < 2).OnClick(func() { w.split(w.active) })
		uikit.MenuSelect(c, "Merge with…", func(m *ui.Menu) {
			for i, other := range w.groups {
				if other != g && other.origin == g.origin && other.method == g.method {
					if m.Item(other.template).Chosen() {
						if err := w.merge(w.active, i); err != nil {
							w.problem = err.Error()
						}
						return
					}
				}
			}
		})
	})
}

func (a *app) collectionVariablesEditor(c *ui.Context, t *ui.Theme) {
	w := &a.collectionUI
	uikit.DialogTitle(c, "Hosts → environment")
	uikit.Select(c, &w.environment, append([]string{"New environment"}, a.envNames()...)).Label("Target environment")
	if w.environment == "New environment" {
		uikit.Input(c, &w.environmentName).Label("New environment name")
	}
	for i := range w.hosts {
		h := &w.hosts[i]
		ui.Row(c.Key(h.origin)).Gap(t.Space(2)).Children(func() {
			uikit.Input(c, &h.variable).Label("Host variable " + h.origin).Width(140).Font(uikit.FontMono)
			uikit.Mono(c, h.origin).SingleLine().Grow(1).MinWidth(0).Tooltip(h.origin)
		})
	}
	ui.Divider(c)
	uikit.DialogTitle(c, "Credentials")
	uikit.Hint(c, "Matching Authorization is inherited from the collection. Cookie and API-key headers use private variable references.")
	for i := range w.credentials {
		credential := &w.credentials[i]
		ui.Column(c.Key(i)).Gap(t.Space(2)).Padding(0, 0, t.Space(2), 0).Children(func() {
			uikit.Body(c, fmt.Sprintf("%s · •••• · %d requests", credential.header, credential.count))
			uikit.Input(c, &credential.variable).Label(fmt.Sprintf("Credential variable %d", i+1)).Font(uikit.FontMono)
			uikit.Select(c, &credential.storage, []string{credentialCurrent, credentialVault, credentialKeep}).Label(fmt.Sprintf("Store credential %d", i+1))
			if credential.storage == credentialKeep {
				ui.Text(c, "Original credential will be written into request files.").FontSize(uikit.TextMeta).TextColor(t.Warning)
			}
		})
	}
	if len(w.credentials) == 0 {
		uikit.Meta(c, "No Authorization, Cookie or API-key headers found.")
	}
	uikit.Hint(c, "Current values stay local and are excluded from environment exports. Vault values are encrypted and host-restricted.")
	ui.Divider(c)
	uikit.DialogTitle(c, "Path parameters")
	seen := map[string]bool{}
	for _, g := range w.groups {
		if !g.include {
			continue
		}
		u, _ := url.Parse(w.latest(g).url)
		actual := strings.Split(u.EscapedPath(), "/")
		for i, part := range strings.Split(g.template, "/") {
			if name, ok := strings.CutPrefix(part, ":"); ok && !seen[name] && i < len(actual) {
				uikit.Mono(c, "{{"+name+"}} = "+actual[i]).SingleLine()
				seen[name] = true
			}
		}
	}
	uikit.Hint(c, "Same-named parameters share an initial value. Rename them in Endpoints to keep different IDs.")
}

func (a *app) collectionStructureEditor(c *ui.Context, t *ui.Theme) {
	w := &a.collectionUI
	uikit.Body(c, "Collection name")
	uikit.Input(c, &w.name).Label("Collection name").Disabled(w.destination != "")
	uikit.Body(c, "Save to")
	uikit.MenuSelect(c, cmp.Or(w.destination, "New collection"), func(m *ui.Menu) {
		if m.Item("New collection").Chosen() {
			w.destination = ""
		}
		for _, name := range a.collectionNames() {
			if m.Item(name).Chosen() {
				w.destination = name
			}
		}
	}).Label("Save to")
	uikit.Body(c, "Group by")
	uikit.Segmented(c, &w.grouping, "Host", "Path prefix", "Flat").OnChange(w.organize)
	uikit.Hint(c, "Drag onto a folder to move, or onto a request to reorder. Select a request to edit its name and folder.")
	if len(w.groups) == 0 {
		return
	}
	g := w.groups[min(w.active, len(w.groups)-1)]
	uikit.Body(c, "Selected request")
	uikit.Mono(c, g.method+" "+g.template).SingleLine()
	uikit.Input(c, &g.name).Label("Request name")
	uikit.Input(c, &g.folder).Label("Request folder").Placeholder("Collection root")
	ui.Row(c).Gap(t.Space(2)).Children(func() {
		uikit.SecondaryButton(c, "Move up").Disabled(w.active == 0).OnClick(func() { i := w.active; w.groups[i-1], w.groups[i] = w.groups[i], w.groups[i-1]; w.active-- })
		uikit.SecondaryButton(c, "Move down").Disabled(w.active == len(w.groups)-1).OnClick(func() { i := w.active; w.groups[i+1], w.groups[i] = w.groups[i], w.groups[i+1]; w.active++ })
	})
}

func (a *app) collectionExamplesEditor(c *ui.Context, t *ui.Theme) {
	w := &a.collectionUI
	uikit.DialogTitle(c, "Choose examples")
	uikit.Hint(c, "Default: latest successful response and one response per error status. Deselect incomplete or binary responses.")
	for index, g := range w.groups {
		if !g.include {
			continue
		}
		ui.Column(c.Key(g.id)).Gap(t.Space(2)).Children(func() {
			uikit.ToolbarButton(c, "Examples for "+g.name, g.template, w.active == index).FillWidth().Children(func() { uikit.Mono(c, g.method+" "+g.template).SingleLine() }).OnClick(func() { w.active = index })
			for _, i := range g.flows {
				f := w.flows[i]
				selected := w.examples[i]
				ui.Checkbox(c.Key(i), &selected, fmt.Sprintf("%d · %s · %s", f.status, f.at.Format("15:04:05"), cmp.Or(f.path, "/"))).FontSize(uikit.TextMeta).
					Label(fmt.Sprintf("Example capture %d", i)).Disabled(f.status < 200 || f.status > 599 || f.conn.error != "").OnClick(func() { w.examples[i] = selected })
			}
		})
	}
}

func (a *app) collectionWizardTree(c *ui.Context, t *ui.Theme) {
	w := &a.collectionUI
	uikit.DialogTitle(c, cmp.Or(w.destination, w.name, "Collection"))
	folders := []string{""}
	for _, g := range w.groups {
		if g.include && !slices.Contains(folders, g.folder) {
			folders = append(folders, g.folder)
		}
	}
	moveID, targetID, folder := "", "", ""
	ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(1)).Children(func() {
		for _, name := range folders {
			heading := ui.Row(c.Key("folder-" + name)).Gap(t.Space(2)).Height(uikit.HeightLarge).AlignItems(ui.Center)
			if _, over := ui.DragOver[string](heading); over {
				heading.Background(t.SurfaceHover)
			}
			if id, dropped := ui.Drop[string](heading); dropped {
				moveID, folder = id, name
			}
			heading.Children(func() {
				ui.Icon(c, lucide.Folder()).Size(uikit.IconInline, uikit.IconInline)
				uikit.Body(c, cmp.Or(name, "Collection root"))
			})
			for i, g := range w.groups {
				if !g.include || g.folder != name {
					continue
				}
				row := uikit.ToolbarButton(c.Key(g.id), "Organize "+g.name, g.method+" "+g.template, i == w.active).Height(32).FillWidth().Drag(g.id)
				if _, over := ui.DragOver[string](row); over {
					row.BorderWidth(2, 0, 0, 0).BorderColor(t.Accent)
				}
				if id, dropped := ui.Drop[string](row); dropped {
					moveID, targetID, folder = id, g.id, name
				}
				row.Children(func() {
					ui.Text(c, "⠿").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
					ui.Text(c, g.method).Font(uikit.FontMono).FontSize(uikit.TextData).TextColor(uikit.MethodColor(g.method, t)).Width(46)
					uikit.Body(c, g.name).Grow(1).MinWidth(0).SingleLine()
					n := 0
					for _, j := range g.flows {
						if w.examples[j] {
							n++
						}
					}
					uikit.Meta(c, fmt.Sprintf("%d examples", n))
				}).OnClick(func() { w.active = i })
			}
		}
	})
	if moveID != "" && moveID != targetID {
		from := slices.IndexFunc(w.groups, func(g *collectionEndpoint) bool { return g.id == moveID })
		if from >= 0 {
			g := w.groups[from]
			g.folder = folder
			if targetID != "" {
				w.groups = slices.Delete(w.groups, from, from+1)
				to := slices.IndexFunc(w.groups, func(g *collectionEndpoint) bool { return g.id == targetID })
				w.groups = slices.Insert(w.groups, to, g)
			}
			w.active = slices.Index(w.groups, g)
		}
	}
}
