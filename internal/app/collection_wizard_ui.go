package app

import (
	"cmp"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
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
	dialog := uikit.Modal(c, &w.open, locales.CreateCollection2(), func() {
		c.OnShortcut(0, ui.KeyEscape, func() { w.open = false })
		uikit.DialogColumn(c, 1080).Height(min(620, height-100)).Gap(t.Space(uikit.SpaceL)).Children(func() {
			uikit.DialogTitle(c, locales.CreateCollection2())
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).FillWidth().Children(func() {
				for i, label := range collectionSteps {
					ui.Row(c.Key(label)).Grow(1).Gap(t.Space(uikit.SpaceM)).Height(36).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
						mark := strconv.Itoa(i + 1)
						if i < w.step {
							mark = "✓"
						}
						color, background := t.TextMuted, t.Surface
						if i < w.step {
							// Passed steps are the only green in the wizard;
							// the step you are on is ink, like the accent.
							color, background = t.AccentText, uikit.Colors(t).Success
						} else if i == w.step {
							color, background = t.AccentText, t.Accent
						}
						ui.Box(c).Size(22, 22).Radius(11).Background(background).Border(1, t.Border).Center().Children(func() { uikit.Caption(c, mark).TextColor(color) })
						text := uikit.Body(c, label).SingleLine()
						if i == w.step {
							text.TextColor(t.Accent).FontWeight(500)
						}
					}).BorderColor(func() ui.Color {
						if i == w.step {
							return t.Accent
						}
						return t.Border
					}())
				}
			})
			ui.Row(c).Grow(1).MinHeight(0).Gap(t.Space(uikit.SpaceXL)).AlignItems(ui.Stretch).Disabled(w.building != nil).Children(func() {
				ui.Scroll(c.Key("settings-" + strconv.Itoa(w.step))).Basis(0).Grow(1).MinWidth(0).MinHeight(0).Gap(t.Space(uikit.SpaceL)).Children(func() {
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
						uikit.DialogTitle(c, locales.ReadyToCreate())
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
						uikit.Body(c, locales.RequestsAndExamples(n, examples))
						uikit.Body(c, locales.CollectionLabel(cmp.Or(w.destination, w.name)))
						uikit.Body(c, locales.EnvironmentLabel(w.environmentLabel()))
						uikit.Checkbox(c, &w.generateAPI, locales.AlsoGenerateOpenApiDraft())
						uikit.Hint(c, locales.TheApiDraftOpensIn())
						uikit.Hint(c, locales.RequestsAndExamplesMayContain())
					}
				})
				ui.Column(c.Key("preview-"+strconv.Itoa(w.step))).Basis(0).Grow(1.15).MinWidth(0).MinHeight(0).Padding(t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceL)).Background(uikit.Colors(t).Chrome).Border(1, t.Border).Radius(uikit.RadiusPanel).Children(func() {
					if w.step == 3 || w.step == 5 {
						a.collectionWizardTree(c, t)
						return
					}
					if w.step == 0 {
						uikit.DialogTitle(c, locales.HostsInScope())
						ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(uikit.SpaceM)).Children(func() {
							for _, h := range w.hosts {
								uikit.Mono(c, h.origin).SingleLine()
							}
						})
						return
					}
					if len(w.groups) == 0 {
						uikit.Hint(c, locales.NoEndpointsInThisScope())
						return
					}
					w.active = min(w.active, len(w.groups)-1)
					g := w.groups[w.active]
					uikit.DialogTitle(c, g.method+" "+g.template).Font(uikit.FontMono).SingleLine()
					uikit.Meta(c, locales.OriginCapturedRequests(g.origin, len(g.flows))).SingleLine()
					if w.step == 4 {
						ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(uikit.SpaceM)).Children(func() {
							for _, i := range g.flows {
								f := w.flows[i]
								ui.Row(c.Key(i)).Gap(t.Space(uikit.SpaceM)).Height(uikit.HeightControl).Children(func() {
									uikit.Tip(c, uikit.Mono(c, cmp.Or(f.path, f.url)).Grow(1).MinWidth(0).SingleLine(), f.url)
									uikit.Mono(c, strconv.Itoa(f.status)).TextColor(uikit.StatusColor(f.status, t)).Width(32)
									uikit.Meta(c, fmt.Sprintf("%d ms", f.millis)).Width(52)
								})
							}
						})
						return
					}
					// Never read a spilled request just to paint a preview.
					f := w.latest(g)
					if f.reqFile != "" {
						uikit.Hint(c, locales.RequestBodyIsOnDisk())
						return
					}
					r, err := w.requestFor(g)
					if err != nil {
						uikit.Hint(c, err.Error())
						return
					}
					uikit.Meta(c, r.Name+" · "+r.Collection).SingleLine()
					preview := r.Method + " " + r.URL + "\n\n" + r.Headers
					// Do not echo credentials even when Leave in requests is selected.
					for _, credential := range w.credentials {
						preview = strings.ReplaceAll(preview, credential.value, "••••")
					}
					ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
						uikit.Mono(c, preview+"\n\n"+r.Body).Selectable()
						if body, notice := f.bodyPreview(true); body != "" {
							ui.Divider(c)
							uikit.Meta(c, locales.CapturedResponseDetail(f.status, cmp.Or(f.contentType(true), locales.UnknownType()))).SingleLine()
							uikit.Mono(c, body+notice).Selectable().MaxLines(12)
						}
					})
					uikit.Meta(c, locales.EnvironmentLabel(w.environmentLabel()))
				})
			})
			if w.problem != "" {
				uikit.ErrorText(c, w.problem)
			}
			uikit.DialogFooter(c).Children(func() {
				message := locales.NothingSavedUntilCreate()
				if w.building != nil {
					message = locales.PreparingRequests()
				}
				uikit.Meta(c, message).Grow(1)
				uikit.SecondaryButton(c, locales.Cancel()).OnClick(func() { w.open = false })
				if w.step > 0 {
					uikit.SecondaryButton(c, locales.Back()).Disabled(w.building != nil).OnClick(func() { w.step--; w.problem = "" })
				}
				if w.step < 5 {
					uikit.PrimaryButton(c, locales.NextStep(collectionSteps[w.step+1])).Disabled(len(w.groups) == 0).OnClick(func() { w.step++; w.problem = "" })
				} else {
					uikit.PrimaryButton(c, locales.Create()).Disabled(w.building != nil).OnClick(a.createCollection)
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
	uikit.DialogTitle(c, locales.ChooseScope())
	uikit.Body(c, locales.CapturedRequestsCount(len(w.flows)))
	uikit.Checkbox(c, &w.static, locales.ExcludeStaticResources()).OnChange(w.regroup)
	uikit.Checkbox(c, &w.tunnels, locales.ExcludeConnectTunnels()).OnChange(w.regroup)
	uikit.Checkbox(c, &w.failed, locales.ExcludeNetworkFailures()).OnChange(w.regroup)
	uikit.Hint(c, locales.Http4xxAnd5xxResponses())
	uikit.Body(c, locales.ScopeSummary(len(w.scope()), len(w.flows)-len(w.scope()), len(w.groups)))
}

func (a *app) collectionEndpointsEditor(c *ui.Context, t *ui.Theme) {
	w := &a.collectionUI
	uikit.DialogTitle(c, locales.MergeEndpoints())
	uikit.Hint(c, locales.SameMethodHostAndPath())
	for i, g := range w.groups {
		ui.Row(c.Key(g.id)).Gap(t.Space(uikit.SpaceM)).Height(uikit.HeightControl).Children(func() {
			uikit.Checkbox(c, &g.include, "").Label(locales.IncludeRequest(g.method, g.template))
			uikit.ToolbarButton(c, locales.EndpointNamed(g.method+" "+g.template), g.origin, i == w.active).Grow(1).MinWidth(0).Children(func() {
				uikit.Mono(c, g.method).TextColor(uikit.MethodColor(g.method, t)).Width(46)
				uikit.Mono(c, g.template).Grow(1).MinWidth(0).SingleLine()
				uikit.Meta(c, locales.TimesCount(len(g.flows)))
			}).OnClick(func() { w.active = i })
		})
	}
	if len(w.groups) == 0 {
		return
	}
	g := w.groups[min(w.active, len(w.groups)-1)]
	ui.Divider(c)
	uikit.Body(c, locales.PathTemplate())
	uikit.Input(c, &g.template).Label(locales.PathTemplate()).Font(uikit.FontMono)
	uikit.Hint(c, locales.UseIdForAWhole())
	ui.Row(c).Gap(t.Space(uikit.SpaceM)).Wrap().Children(func() {
		uikit.SecondaryButton(c, locales.Split()).Disabled(len(g.flows) < 2).OnClick(func() { w.split(w.active) })
		uikit.MenuSelect(c, locales.MergeWith(), func(m *ui.Menu) {
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
	uikit.DialogTitle(c, locales.HostsEnvironment())
	uikit.SelectLabels(c, &w.environment, append([]string{"New environment"}, a.envNames()...), optionLabel).Label(locales.TargetEnvironment())
	if w.environment == "New environment" {
		uikit.Input(c, &w.environmentName).Label(locales.NewEnvironmentName())
	}
	for i := range w.hosts {
		h := &w.hosts[i]
		ui.Row(c.Key(h.origin)).Gap(t.Space(uikit.SpaceM)).Children(func() {
			uikit.Input(c, &h.variable).Label(locales.HostVariable(h.origin)).Width(uikit.WidthPicker).Font(uikit.FontMono)
			uikit.Tip(c, uikit.Mono(c, h.origin).SingleLine().Grow(1).MinWidth(0), h.origin)
		})
	}
	ui.Divider(c)
	uikit.DialogTitle(c, locales.Credentials())
	uikit.Hint(c, locales.MatchingAuthorizationIsInheritedFrom())
	for i := range w.credentials {
		credential := &w.credentials[i]
		ui.Column(c.Key(i)).Gap(t.Space(uikit.SpaceM)).Padding(0, 0, t.Space(uikit.SpaceM), 0).Children(func() {
			uikit.Body(c, locales.CredentialSummary(credential.header, credential.count))
			uikit.Input(c, &credential.variable).Label(locales.CredentialVariable(i + 1)).Font(uikit.FontMono)
			uikit.SelectLabels(c, &credential.storage, []string{credentialCurrent, credentialVault, credentialKeep}, optionLabel).Label(locales.StoreCredential(i + 1))
			if credential.storage == credentialKeep {
				uikit.Caption(c, locales.OriginalCredentialWillBeWritten()).TextColor(t.Warning)
			}
		})
	}
	if len(w.credentials) == 0 {
		uikit.Meta(c, locales.NoAuthorizationCookieOrApi())
	}
	uikit.Hint(c, locales.CurrentValuesStayLocalAnd())
	ui.Divider(c)
	uikit.DialogTitle(c, locales.PathParameters())
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
	uikit.Hint(c, locales.SameNamedParametersShareAn())
}

func (a *app) collectionStructureEditor(c *ui.Context, t *ui.Theme) {
	w := &a.collectionUI
	uikit.Body(c, locales.CollectionName())
	uikit.Input(c, &w.name).Label(locales.CollectionName()).Disabled(w.destination != "")
	uikit.Body(c, locales.SaveTo())
	uikit.MenuSelect(c, cmp.Or(w.destination, locales.NewCollectionMenu()), func(m *ui.Menu) {
		if m.Item(locales.NewCollectionMenu()).Chosen() {
			w.destination = ""
		}
		for _, name := range a.collectionNames() {
			if m.Item(name).Chosen() {
				w.destination = name
			}
		}
	}).Label(locales.SaveTo())
	uikit.Body(c, locales.GroupBy())
	uikit.Segmented(c, &w.grouping, locales.Host(), locales.PathPrefix(), locales.Flat()).OnChange(w.organize)
	uikit.Hint(c, locales.DragOntoAFolderTo())
	if len(w.groups) == 0 {
		return
	}
	g := w.groups[min(w.active, len(w.groups)-1)]
	uikit.Body(c, locales.SelectedRequest())
	uikit.Mono(c, g.method+" "+g.template).SingleLine()
	uikit.Input(c, &g.name).Label(locales.RequestName())
	uikit.Input(c, &g.folder).Label(locales.RequestFolder()).Placeholder(locales.CollectionRoot())
	ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
		uikit.SecondaryButton(c, locales.MoveUp()).Disabled(w.active == 0).OnClick(func() { i := w.active; w.groups[i-1], w.groups[i] = w.groups[i], w.groups[i-1]; w.active-- })
		uikit.SecondaryButton(c, locales.MoveDown()).Disabled(w.active == len(w.groups)-1).OnClick(func() { i := w.active; w.groups[i+1], w.groups[i] = w.groups[i], w.groups[i+1]; w.active++ })
	})
}

func (a *app) collectionExamplesEditor(c *ui.Context, t *ui.Theme) {
	w := &a.collectionUI
	uikit.DialogTitle(c, locales.ChooseExamples())
	uikit.Hint(c, locales.DefaultLatestSuccessfulResponseAnd())
	for index, g := range w.groups {
		if !g.include {
			continue
		}
		ui.Column(c.Key(g.id)).Gap(t.Space(uikit.SpaceM)).Children(func() {
			uikit.ToolbarButton(c, locales.ExamplesFor(g.name), g.template, w.active == index).FillWidth().Children(func() { uikit.Mono(c, g.method+" "+g.template).SingleLine() }).OnClick(func() { w.active = index })
			for _, i := range g.flows {
				f := w.flows[i]
				selected := w.examples[i]
				uikit.Checkbox(c.Key(i), &selected, locales.StatusTimePath(f.status, f.at.Format("15:04:05"), cmp.Or(f.path, "/"))).
					Label(locales.ExampleCapture(i)).Disabled(f.status < 200 || f.status > 599 || f.conn.error != "").OnClick(func() { w.examples[i] = selected })
			}
		})
	}
}

func (a *app) collectionWizardTree(c *ui.Context, t *ui.Theme) {
	w := &a.collectionUI
	uikit.DialogTitle(c, cmp.Or(w.destination, w.name, locales.Collection()))
	folders := []string{""}
	for _, g := range w.groups {
		if g.include && !slices.Contains(folders, g.folder) {
			folders = append(folders, g.folder)
		}
	}
	moveID, targetID, folder := "", "", ""
	ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(uikit.SpaceXS)).Children(func() {
		for _, name := range folders {
			heading := ui.Row(c.Key("folder-" + name)).Gap(t.Space(uikit.SpaceM)).Height(uikit.HeightControl).AlignItems(ui.Center)
			if _, over := ui.DragOver[string](heading); over {
				heading.Background(t.SurfaceHover)
			}
			if id, dropped := ui.Drop[string](heading); dropped {
				moveID, folder = id, name
			}
			heading.Children(func() {
				ui.Icon(c, lucide.Folder()).Size(uikit.IconInline, uikit.IconInline)
				uikit.Body(c, cmp.Or(name, locales.CollectionRoot()))
			})
			for i, g := range w.groups {
				if !g.include || g.folder != name {
					continue
				}
				row := uikit.ToolbarButton(c.Key(g.id), locales.OrganizeNamed(g.name), g.method+" "+g.template, i == w.active).FillWidth().Drag(g.id)
				if _, over := ui.DragOver[string](row); over {
					row.BorderWidth(2, 0, 0, 0).BorderColor(t.Accent)
				}
				if id, dropped := ui.Drop[string](row); dropped {
					moveID, targetID, folder = id, g.id, name
				}
				row.Children(func() {
					uikit.Meta(c, "⠿")
					uikit.Mono(c, g.method).TextColor(uikit.MethodColor(g.method, t)).Width(46)
					uikit.Body(c, g.name).Grow(1).MinWidth(0).SingleLine()
					n := 0
					for _, j := range g.flows {
						if w.examples[j] {
							n++
						}
					}
					uikit.Meta(c, locales.ExamplesCount(n))
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
