package app

import (
	"cmp"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/linkedmap"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// desktopWorkbench owns presentation, not the capture view's filters or choice.
// Focusing an inspector is temporary; only the normal layout is persisted.
type desktopWorkbench struct {
	layout        string
	sidePercent   float32
	sidebarHidden bool
	focused       bool
	sideBySide    bool
	paletteOpen   bool
	query         string
	command       int
	commands      ui.ListState
	// paletteHistory is the in-memory quick-open recency list. LRUCache
	// keeps the oldest entries at the front, so ranking walks its keys in
	// reverse to put the most recently chosen row first.
	paletteHistory *linkedmap.LRUCache[string, struct{}]
	// paletteHistoryGen changes when a row is used so cached asset rankings
	// are re-sorted as recency changes.
	paletteHistoryGen uint64
	inspector         ui.Handle
	content           ui.Handle
	menuAction        string
	menuItems         map[string]*mygo.MenuItem
	// commandList holds the commands built in one view pass; commandFrame and
	// commandFor say which pass and window context it was built for.
	commandList  []action
	commandFrame int
	commandFor   ui.Services
	// listFocusAsked is set once the traffic list took the keys for this
	// visit to LIVE, so Tab can leave it again.
	listFocusAsked bool
	// navigation is the Go Back / Go Forward history. It is recorded once a
	// frame here, after the app has reconciled its documents and selection.
	navigation navigationStack
}

func (w *desktopWorkbench) openPalette() {
	w.paletteOpen, w.query, w.command = true, "", 0
	w.commands.ScrollTo(0, ui.Start)
}

// contentWidth is the room the main pane has beside the sidebar.
func (a *app) contentWidth(c *ui.Context) float32 {
	width, _ := c.Size()
	if a.inspectorInWindow {
		return width
	}
	if a.sidebarShown() {
		width -= a.sidebarWidth + 6
	}
	return width
}

// sidebarMinWidth is the narrowest window that keeps the sidebar beside the
// content: under it the sidebar steps aside on its own, and comes back with
// the choice it was left with when the window widens again.
const sidebarMinWidth = 860

// sidebarShown: the sidebar stays beside every screen, except while it is
// hidden, the inspector has the window, the run records (three columns of
// their own, closed with their Close button) need it, or the window is too
// narrow to hold it beside what it frames.
func (a *app) sidebarShown() bool {
	return !a.workbench.sidebarHidden && !(a.workbench.focused && a.page.capture()) &&
		a.windowWidth >= sidebarMinWidth && !(a.page == pageRuns)
}

func (a *app) desktopContent(c *ui.Context) {
	w := &a.workbench
	available := a.contentWidth(c)
	// Even an explicitly requested side inspector yields on small windows.
	// It returns, with its own divider position, when there is room again.
	w.sideBySide = w.layout == "right" && available >= 1000
	if w.sidePercent == 0 {
		w.sidePercent = 64
	}
	content := func() {
		switch a.page {
		case pageRuns:
			if !a.experiments.open {
				a.experimentsView(c, c.Theme(), true)
			}
		case pageAPI:
			a.apiView(c, c.Theme())
		case pageRequest:
			ui.Column(c).Fill().Children(func() {
				a.documentHeader(c, c.Theme())
				d := a.requestDocument
				// A saved request has a history; the column leaves the editor
				// its room in a narrow window, where Send history still opens
				// it. It steps aside as soon as the request pane could not
				// show its four tabs whole beside it, and comes back when the
				// window has room again, as the sidebar does.
				if d == nil || d.assetID == "" || available < 900 ||
					!a.requestTabsFit(c, d, available-historyWidth) {
					a.composeView(c, c.Theme())
					return
				}
				ui.Row(c).Grow(1).FillWidth().MinHeight(0).AlignItems(ui.Stretch).Children(func() {
					a.historyColumn(c, c.Theme(), d)
					ui.Column(c).Grow(1).MinWidth(0).Children(func() { a.composeView(c, c.Theme()) })
				})
			})
		case pageSession:
			a.sessionsView(c, c.Theme())
		case pageDiagnostics:
			a.diagnosticsView(c, c.Theme())
		case pageOverview:
			a.overviewView(c, c.Theme())
		default:
			ui.Column(c).Fill().Children(func() {
				if w.focused && !a.inspectorDetached {
					ui.Row(c).Height(uikit.HeightControl).Padding(0, c.Theme().Space(uikit.SpaceL)).AlignItems(ui.Center).Children(func() {
						a.actionButton(c, "view.focus-inspector", locales.BackToList())
						uikit.Meta(c, locales.InspectorFocusCtrlCmdShift())
					})
					a.detail(c)
					return
				}
				// The guide is the whole page while it is open: no search,
				// filters or list frame behind it.
				if a.guide {
					a.setupGuide(c)
					return
				}
				a.captureToolbar(c, c.Theme())
				a.certificateBanner(c, c.Theme())
				table := func() {
					ui.Column(c).Fill().Children(func() {
						if a.hadFlows {
							a.activityStrip(c)
						}
						if a.chosen.Len() > 1 {
							ui.Row(c).Height(uikit.HeightControl).Padding(0, c.Theme().Space(uikit.SpaceM)).Gap(c.Theme().Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
								uikit.Caption(c, locales.SelectedCount(a.chosen.Len())).FontWeight(500).Grow(1)
								replay := pick(a.actions(c), "flow.replay")
								if uikit.PrimaryButton(c, locales.ReplaySelected()).Disabled(!replay.enabled).Clicked() {
									replay.run()
								}
								uikit.MoreMenu(c, locales.Export(), func(m *ui.Menu) {
									actionMenu(m, []action{
										pick(a.actions(c), "capture.export-chosen"),
										pick(a.actions(c), "capture.export-chosen-csv"),
									})
								})
								if uikit.LinkButton(c, locales.ClearSelection()).Clicked() {
									a.chosen.Clear()
								}
							})
						}
						a.table(c)
					})
				}
				// Keep the plot and its pointer capture in the same split while
				// a brush passes through an interval with no matching requests.
				if len(a.visible) == 0 && !a.activity.active() {
					a.syncDetail()
					// The table's own empty state says why; an inspector
					// with nothing to show would only repeat it.
					table()
				} else if a.inspectorDetached {
					// The inspector has its own window; the table takes the
					// room the split would have given it.
					table()
				} else if w.sideBySide {
					// The room under the toolbar, not the column's whole
					// height (Fill), which ran past the footer.
					ui.SplitPercent(c.Key("right-inspector"), &w.sidePercent, table, func() { a.detail(c) }).SplitReset(64).Grow(1).FillWidth().MinHeight(0)
				} else {
					percent, reset := &a.tablePercent, float32(44)
					if a.bodyMode[0] == 5 {
						if a.protobufTablePercent == 0 {
							a.protobufTablePercent = 34
						}
						percent, reset = &a.protobufTablePercent, 34
					}
					ui.SplitVerticalPercent(c.Key("bottom-inspector"), percent, table, func() { a.detail(c) }).SplitReset(reset).Grow(1).FillWidth().MinHeight(0)
				}
			})
		}
	}
	ui.Column(c).Grow(1).FillWidth().MinHeight(0).Children(content)
}

var captureKinds = []string{"All", "HTTP", "HTTPS", "WebSocket", "SSE", "JSON", "Images", "HTML", "Form", "XML", "JS", "CSS", "Media", "h1", "h2"}

// captureProtocols are the filters the toolbar shows; the rest live in its
// overflow menu.
var captureProtocols = captureKinds[:6]

// searchPlaceholder is what the search field says it searches. The whole
// sentence names the operators the field takes; a field too narrow for it says
// only what it searches, because a cut-off word reads as a broken field.
func (a *app) searchPlaceholder(c *ui.Context) string {
	placeholder := locales.SearchPlaceholder()
	if w := a.search.Bounds(c).W; w > 0 && paneTextWidth(c, placeholder, uikit.TextBody, 400) > w-12 {
		placeholder = locales.SearchHostOrPath()
	}
	return placeholder
}

func (a *app) captureToolbar(c *ui.Context, t *ui.Theme) {
	width := a.contentWidth(c)
	// The protocol tabs are six short labels: they keep enough room to show
	// them all down to a narrow window, and the search field keeps the rest.
	// Conditions live inside the field, so while any are set they take that
	// room first and the tabs give way.
	protocolWidth := min(302, max(240, width*0.36))
	if width < 900 && a.conditionsActive() {
		protocolWidth = 128
	}
	// A filter bar is the pane header's height: 32, with the controls at 24.
	ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).
		Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
		// The field's sides take SearchField's inset, so chips and the
		// placeholder clear its edge.
		box := ui.Row(c).Grow(1).MinWidth(uikit.WidthField).Height(uikit.HeightRow).Gap(t.Space(uikit.SpaceXS)).Padding(0, t.Space(uikit.SpaceM)).AlignItems(ui.Center)
		uikit.FieldFace(c, box, a.search.Focused(c)).Children(func() {
			// Chips first, then the input: a condition is narrowed one
			// token at a time and stays where it was put.
			if a.conditionsActive() {
				// The strip takes the room its chips need, down to what the
				// input leaves it; its thumb sits on the field's edge, under
				// the chips' text rather than over their buttons.
				ui.ScrollHorizontal(c).Shrink(1).MinWidth(0).FillHeight().ScrollbarInsets(0, 0, -4).Children(func() {
					a.captureConditions(c)
				})
			}
			placeholder := a.searchPlaceholder(c)
			// Bare: the query field carries its own tokens and completion popup, which uikit.Combobox does not model.
			cb := ui.ComboboxBase(c.Key("capture-search"), &a.query)
			search := cb.Input.Bind(&a.search).Label(locales.SearchHostOrPath()).Placeholder(placeholder).
				Tooltip(cmp.Or(a.queryProblem, a.searchProblem, locales.SearchHostAndPathIncluding())).
				Height(uikit.HeightRow).Padding(0, t.Space(uikit.SpaceXS)).Grow(1).MinWidth(min(200, width/5)).FocusRing(false).Border(0, ui.Color{}).
				OnShortcut(0, ui.KeyEscape, func() { a.clearQuery(c) })
			if a.queryProblem != "" || a.searchProblem != "" {
				search.TextColor(t.Danger)
			}
			if a.queryCaret != nil {
				search.SetTextSelection(*a.queryCaret, *a.queryCaret)
				a.queryCaret = nil
			}
			caret, _ := search.TextSelection()
			_, _, prefix := queryTokenAt(a.query, caret)
			suggestions := a.querySuggestions(prefix, !cb.Filtering())
			first := false
			cb.Popup(func(panel ui.Element) {
				uikit.PopoverPanel(c, panel).Padding(t.Space(uikit.SpaceXS)).Margin(t.Space(uikit.SpaceS), 0, 0, 0)
				for _, s := range suggestions[:min(len(suggestions), maxQuerySuggestions)] {
					item := cb.Item(s.text).Label(s.text).Height(uikit.HeightRow).Padding(0, t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceL)).Radius(uikit.RadiusControl).AlignItems(ui.Center)
					if item.Highlighted() {
						item.Background(t.SurfaceHover)
						first = first || s == suggestions[0]
					}
					item.Children(func() {
						uikit.Mono(c, s.text).TextColor(t.Text).SingleLine().Grow(1)
						uikit.Meta(c, s.hint).SingleLine()
					})
				}
			})
			_, _, value := strings.Cut(prefix, ":")
			text, chosen := cb.Chosen()
			switch {
			case chosen:
				q, at := completeQuery(a.query, caret, text)
				a.query, a.queryCaret = q, new(at)
				if !a.commitQuery(false) {
					a.applyFilter()
				}
				if strings.HasSuffix(text, ":") {
					cb.SetOpen(true) // on to the values
				} else {
					a.queryCaret = new(utf8.RuneCountInString(a.query))
				}
			// Opened by Down, not a click or typing, on its first option or
			// none: meant for the requests, as before the field suggested
			// anything. Up opens it on its last option.
			case cb.Open() && !a.queryOpen && !cb.Filtering() && !search.Clicked() && (first || len(suggestions) == 0):
				cb.SetOpen(false)
				a.focusTraffic(c)
			// Closed rather than open with nothing in it, so that Down
			// opening it again can be told apart.
			case cb.Open() && len(suggestions) == 0:
				cb.SetOpen(false)
			// Typing highlights the first option for Enter to take. A key
			// is only highlighted once asked for (Down): Enter after free
			// text such as "st" searches it, not status:.
			case !value && cb.Filtering():
				cb.SetOpen(false)
				cb.SetOpen(true)
			}
			a.queryOpen = cb.Open()
			if search.Changed() && !a.commitQuery(false) {
				a.applyFilter()
			}
			if search.Submitted() {
				a.commitQuery(true)
				if a.queryProblem == "" && a.searchProblem == "" {
					a.focusTraffic(c)
				}
			}
			if a.filtersActive() {
				if uikit.RowIconButton(c, locales.ResetAll(), locales.ResetAllConditions(), closeIcon).Clicked() {
					a.views.resetFilters(a)
				}
			}
			// The conditions the field cannot spell -- the dialog's own
			// fields, the time range -- open from the field's end, so the
			// search is the one way to narrow the list.
			if uikit.RowIconButton(c, locales.Filters(), locales.StatusHeaderBodyMethodDuration(), filterIcon).Clicked() {
				a.openFilter()
			}
		})
		ui.ScrollHorizontal(c).Width(protocolWidth).Height(uikit.HeightRow).Children(func() {
			// Images and HTML live in the menu but remain valid selections.
			// The kinds read as tabs over the list: the chosen one is the
			// tabs' tinted chip, with no track behind them.
			p := uikit.Colors(t)
			// Bare: each protocol chip has a count and a short label, which uikit.Segmented does not take.
			segments := ui.SegmentedBase(c, &a.chip, len(captureKinds))
			segments.Track.Label(locales.ProtocolAndContentTypeLabel()).Height(uikit.HeightRow).Gap(t.Space(uikit.SpaceHair)).Children(func() {
				for i, name := range captureProtocols {
					label := optionLabel(name)
					if name == "WebSocket" {
						label = "WS"
					}
					segment := segments.Segment(i).Label(locales.ProtocolAndContentType(name)).Height(uikit.HeightRow).
						Padding(0, t.Space(uikit.SpaceS)).Radius(uikit.RadiusControl).AlignItems(ui.Center).TextColor(t.TextMuted)
					switch {
					case a.chip == i:
						// A filter's chosen tab is a neutral face: green is
						// only for what is selected as data, primary or on.
						segment.Background(p.Muted).TextColor(t.Text)
					case segment.Pressed():
						segment.Background(t.SurfacePressed)
					case segment.Hovered():
						segment.Background(t.SurfaceHover)
					}
					segment.Children(func() {
						uikit.Caption(c, label)
					})
				}
			})
			if segments.Track.Changed() {
				a.applyFilter()
			}
		})
		breakButton := uikit.IconButton(c, locales.Breakpoints(), locales.PauseEachMatchingRequestOr(), breakIcon)
		if breakButton.Clicked() && a.srv != nil {
			a.breakOn = !a.breakOn
			a.srv.SetBreak(a.breakOn)
		}
		uikit.MoreButton(c, locales.MoreCaptureOptions(), locales.MoreCaptureOptions(), func(m *ui.Menu) { actionMenu(m, a.captureActions(c)) })
	})
	ui.Divider(c)
}

// Conditions are chips inside the search field; overflow scrolls rather
// than adding another toolbar row. Content fields use the view's AND/OR mode;
// scope conditions always narrow. A chip the query syntax can write turns back
// into text at the end of the field, so it can be changed and committed again.
func (a *app) captureConditions(c *ui.Context) {
	t := c.Theme()
	note := locales.ConditionsMatchAll()
	if a.filterAny {
		note = locales.ConditionsMatchAny()
	}
	uikit.Tip(c, ui.Row(c).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center), note).Children(func() {
		changed := func() {
			a.views.capture(a)
			a.applyFilter()
		}
		chip := func(label string, clear func()) {
			uikit.QueryChip(c, label, nil, func() { clear(); changed() })
		}
		if a.filterAny {
			uikit.QueryChip(c, locales.ChipContentOr(), a.openFilter, func() { a.filterAny = false; changed() })
		}
		if a.filter != "all" {
			label := locales.ChipTraffic(a.filter)
			if host, ok := strings.CutPrefix(a.filter, "host:"); ok {
				label = locales.ChipHost(host)
			} else if client, ok := strings.CutPrefix(a.filter, "client:"); ok {
				label = locales.ChipApp(client)
			}
			chip(label, func() { a.filter = "all" })
		}
		if a.chip != 0 {
			chip(captureKinds[a.chip], func() { a.chip = 0 })
		}
		if a.activity.active() {
			chip(locales.ChipTime(a.activity.label()), func() { a.activity = activityRange{} })
		}
		for _, key := range queryKeys {
			field := key.field(a)
			if field == nil || *field == "" {
				continue
			}
			if key.text {
				// Each condition of a text field is its own chip, so one can be
				// removed or edited without the others.
				for _, t := range tokenizeQuery(*field) {
					text := key.name + ":" + t.raw
					uikit.QueryChip(c, text, func() {
						*field = removeTextCondition(*field, t.raw)
						a.query = strings.TrimSpace(strings.TrimSpace(a.query) + " " + text)
						a.queryCaret = new(utf8.RuneCountInString(a.query))
						changed()
						a.search.Focus(c)
					}, func() { *field = removeTextCondition(*field, t.raw); changed() })
				}
				continue
			}
			text := queryChip(key.name, *field)
			uikit.QueryChip(c, text, func() {
				*field = ""
				a.query = strings.TrimSpace(strings.TrimSpace(a.query) + " " + text)
				a.queryCaret = new(utf8.RuneCountInString(a.query))
				changed()
				a.search.Focus(c)
			}, func() { *field = ""; changed() })
		}
	})
}

// captureActions are the capture toolbar's ⋯ menu. Commands already in the
// registry are looked up by id; only the toolbar's own view choices are built
// here. The menu keeps its shorter labels for the shared commands.
func (a *app) captureActions(c *ui.Context) []action {
	global := a.actions(c)
	actions := []action{
		pick(global, "capture.stop"),
		pick(global, "capture.start"),
		pick(global, "tools.rules").as(locales.DebuggingRules2()),
		pick(global, "collection.record"),
		do("tokens.analyze", locales.RecognizeTokenLinks(), func() { a.openTokenLinks(a.tokenLinkScope()) }),
		pick(global, "capture.saved-filters").as(locales.SaveFilter()),
	}
	for index := 6; index < len(captureKinds); index++ {
		actions = append(actions, do("capture.kind", optionLabel(captureKinds[index]), func() {
			a.chip = index
			a.applyFilter()
		}).checked(checkbox(a.chip == index)).in("kinds"))
	}
	actions = append(actions, pick(global, "capture.filter").as(locales.Filter()).in("filter"))
	if a.filtersActive() {
		actions = append(actions, pick(global, "capture.reset-filters").as(locales.ResetAllConditions()).in("filter"))
	}
	return append(actions,
		do("capture.add-column", locales.AddColumn(), a.openColumn).in("columns"),
		do("capture.clear", locales.ClearUnpinnedRequests2(), func() { a.clearOpen = true }).in("clear"),
		pick(global, "flow.undo-delete"),
	)
}

// inspectOne() is the label of the single-request action; it is read at each
// use so that it follows the current language.
func inspectOne() string { return locales.InspectOneHttpRequestBulk() }

// Row menus and global commands share these target rules. Single-row actions
// use the inspected/clicked request; a bulk replay always uses the whole choice.
func (a *app) requestActions(c *ui.Context, f flow, current bool) []action {
	selected := []flow{f}
	bulk := a.chosen.Len() > 1
	if bulk {
		selected = a.selectedFlows()
	}
	replayable := current && len(selected) > 0
	for _, flow := range selected {
		replayable = replayable && flow.kind == "" && flow.url != ""
	}
	copyable := current && !bulk && f.kind == "" && f.url != ""
	var draft *requestDocument
	if d := a.requestDocument; !a.page.capture() && d != nil {
		copyable = true
		draft = d
	}
	actions := []action{
		do("collection.from-capture", locales.CreateCollection(), func() { a.openCollectionWizard(selected) }).
			when(current && len(selected) > 0, locales.SelectCapturedRequestsFirst()),
		do("tokens.from-capture", locales.RecognizeTokenLinks(), func() { a.openTokenLinks(selected) }).
			when(current && len(selected) > 1, locales.SelectASourceResponseAnd()),
		do("flow.edit", locales.EditRequest(), func() { a.editCapture(f) }).
			when(current && !bulk && f.kind == "", inspectOne()).keys(ui.Cmd|ui.Alt, ui.KeyEnter),
		do("flow.replay", locales.ReplaySelected(), func() { a.openReplay(selected) }).
			when(replayable, locales.ChooseHttpRequestsOnlyTunnels()).keys(ui.Cmd, ui.KeyR),
	}
	for _, item := range a.requestCopyActions(c, f, draft) {
		if item.id == "flow.copy-curl" {
			item = item.keys(ui.Cmd|ui.Alt, ui.KeyC)
		}
		actions = append(actions, item.when(copyable, inspectOne()))
	}
	for _, item := range a.captureCopyActions(c, selected) {
		actions = append(actions, item.when(current && a.page.capture(), locales.InspectCapturedRequestsFirst()))
	}
	actions = append(actions, do("flow.delete", locales.Delete(), func() { a.queueDeleteFlows(selected) }).
		when(current && len(selected) > 0, locales.SelectCapturedRequestsFirst()).in("danger").keys(ui.Cmd, flowDeleteKey()))
	return actions
}

// breakActionLabel names what pressing the break command will do, not the
// current state: the palette and menus read as actions.
func (a *app) breakActionLabel() string {
	if a.breakOn {
		return locales.StopPausingRequests()
	}
	return locales.PauseOnMatchingRequests()
}

func (a *app) focusTraffic(c *ui.Context) {
	a.workbench.focused = false
	if view := a.views.activeView(); view != nil && len(a.visible) > 0 {
		view.list.Handle.Focus(c)
	}
}

// clearQuery takes back what was typed in the search field, leaving its chips,
// the view and the focus where they are: Escape in the field clears the search.
func (a *app) clearQuery(c *ui.Context) {
	if a.query == "" && a.queryProblem == "" {
		return
	}
	a.query, a.queryProblem = "", ""
	a.views.capture(a)
	a.applyFilter()
	c.Invalidate()
}

func (a *app) commandPalette(c *ui.Context) {
	w := &a.workbench
	// The one place history is recorded: every screen change has already
	// been committed by this point in the frame. navigate() suppresses its
	// own frame so restoring a stop cannot clear the forward half.
	w.navigation.observe(a)
	if !w.paletteOpen {
		return
	}
	uikit.ModalFlush(c, &w.paletteOpen, locales.Commands2(), func() {
		width, height := c.Size()
		t := c.Theme()
		ui.Column(c).Width(min(uikit.CommandPaletteWidth, width-32)).Gap(0).Children(func() {
			var input ui.Element
			ui.Row(c).Height(uikit.CommandPaletteInputRow).Padding(0, t.Space(uikit.SpaceXL)).Gap(t.Space(uikit.SpaceM)).
				AlignItems(ui.Center).Children(func() {
				ui.Icon(c, searchIcon).Size(uikit.IconLarge, uikit.IconLarge).TextColor(t.TextMuted)
				// Modal focuses its first input after remembering the opener.
				// An explicit AutoFocus here would replace that focus.
				// Bare: the palette's input is 48 points in its own row, taller than the kit's 28-point field.
				input = ui.TextInput(c, &w.query).Height(uikit.CommandPaletteInputRow).Padding(0).
					FontSize(uikit.Scaled(uikit.CommandPaletteInputSize)).Grow(1).MinWidth(0).
					FocusRing(false).Background(ui.Color{}).Border(0, ui.Color{}).
					Label(locales.FindCommand()).Placeholder(locales.SearchCommandsRequestsEnvironments())
				uikit.Kbd(c, "esc")
			})
			ui.Divider(c)
			if input.Changed() {
				w.command = 0
				w.commands.ScrollTo(0, ui.Start)
			}
			// Empty queries list what can run now. A search scores commands
			// and saved entries with VS Code's fuzzy scorer, puts recently
			// used rows first, and leaves unavailable commands at the end so
			// asking for one explains why it cannot run.
			results := a.paletteRankedResults(w.query, a.actions(c))
			matches := results.entries
			more := results.more
			run := func() {
				if w.command >= 0 && w.command < len(matches) && matches[w.command].enabled {
					w.paletteOpen = false
					matches[w.command].run()
				}
			}
			for _, key := range []ui.Key{ui.KeyDown, ui.KeyUp} {
				c.OnShortcut(0, key, func() {
					step := 1
					if key == ui.KeyUp {
						step = -1
					}
					w.command = max(0, min(len(matches)-1, w.command+step))
					w.commands.ScrollIntoView(w.command)
				})
			}
			if input.Submitted() {
				run()
			}
			w.commands.Selected = &w.command
			focused := w.commands.Handle.Focused(c)
			// The highlighted command is a menu highlight: the tint without a
			// marker, as menus have no selection to keep.
			c.SetTheme(uikit.ListTheme(t, focused))
			// Leave room for the overflow caption below the list.
			visibleRows := 9
			if more > 0 {
				visibleRows--
			}
			listHeight := min(float32(visibleRows)*uikit.CommandPaletteRow, max(float32(120), height-190))
			var list ui.Element
			if len(matches) > 0 {
				ui.Column(c).Padding(uikit.CommandPaletteRowInset).Gap(0).Children(func() {
					list = ui.List(c, &w.commands, len(matches), func(i int) {
						item := matches[i]
						b := uikit.RowButton(c, item.label).Disabled(!item.enabled).
							Height(uikit.CommandPaletteRow).Padding(0, t.Space(uikit.SpaceL))
						if !item.enabled {
							b.Background(ui.Transparent)
						}
						b.Children(func() {
							color := t.Text
							if !item.enabled {
								color = uikit.Colors(t).Faint
							}
							ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
								paletteLabel(c, item, color).
									LineHeight(20.0 / 14.0).SingleLine().MinWidth(0)
								if item.detail != "" {
									paletteDetail(c, item).SingleLine()
								}
							})
							if item.action && item.meta != "" {
								uikit.Kbd(c, item.meta)
							}
						})
						if b.Clicked() {
							w.command = i
							run()
						}
					}).Height(listHeight).FillWidth()
				})
			} else {
				ui.Row(c).Padding(t.Space(uikit.SpaceXL)).Children(func() {
					uikit.Hint(c, locales.NoMatchingCommandsRequestsOr())
				})
			}
			c.SetTheme(t)
			if len(matches) > 0 && list.Submitted() {
				run()
			}
			if more > 0 {
				ui.Row(c).Padding(0, t.Space(uikit.SpaceXL)).Children(func() {
					uikit.Meta(c, locales.MoreResults(more)).SingleLine()
				})
			}
			context := ""
			if w.command >= 0 && w.command < len(matches) {
				item := matches[w.command]
				switch {
				case !item.enabled:
					context = item.why
				case !item.action:
					context = item.detail
				case a.page.capture():
					if a.chosen.Len() > 1 {
						context = locales.CaptureSelected(a.chosen.Len())
					} else if f, ok := a.current(); ok {
						context = locales.CaptureNumberMethodURL(f.id, f.method, f.path)
					}
				case !a.page.capture() && a.requestDocument != nil:
					context = locales.DraftTitled(a.title())
				}
			}
			ui.Divider(c)
			enter := "Enter"
			if runtime.GOOS == "darwin" {
				enter = "↩"
			}
			ui.Row(c).Height(40).Padding(0, t.Space(uikit.SpaceXL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				if context != "" {
					uikit.Meta(c, context).SingleLine().MinWidth(0).Shrink(1)
				}
				ui.Spacer(c)
				uikit.Kbd(c, "↑")
				uikit.Kbd(c, "↓")
				uikit.Small(c, locales.ChooseCommand())
				uikit.Kbd(c, enter)
				uikit.Small(c, locales.Open())
				uikit.Kbd(c, "esc")
				uikit.Small(c, locales.Close())
			})
		})
	})
}
