package app

import (
	"cmp"
	"fmt"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
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
	inspector     ui.Handle
	content       ui.Handle
	menuAction    string
	menuItems     map[string]*mygo.MenuItem
	// listFocusAsked is set once the traffic list took the keys for this
	// visit to LIVE, so Tab can leave it again.
	listFocusAsked bool
}

func (w *desktopWorkbench) openPalette() {
	w.paletteOpen, w.query, w.command = true, "", 0
	w.commands.ScrollTo(0, ui.Start)
}

// contentWidth is the room the main pane has beside the sidebar.
func (a *app) contentWidth(c *ui.Context) float32 {
	width, _ := c.Size()
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
		default:
			ui.Column(c).Fill().Children(func() {
				if w.focused {
					ui.Row(c).Height(uikit.HeightLarge).Padding(0, c.Theme().Space(3)).AlignItems(ui.Center).Children(func() {
						a.actionButton(c, "view.focus-inspector", "Back to list")
						uikit.Meta(c, locales.InspectorFocusCtrlCmdShift())
					})
					a.detail(c)
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
							ui.Row(c).Height(uikit.HeightLarge).Padding(0, c.Theme().Space(2)).Gap(c.Theme().Space(2)).AlignItems(ui.Center).Children(func() {
								ui.Text(c, locales.SelectedCount(a.chosen.Len())).FontSize(uikit.Scaled(uikit.TextMeta)).FontWeight(500).Grow(1)
								a.actionButton(c, "flow.replay", "Replay…")
								a.actionButton(c, "capture.export-chosen", "Export HAR…")
								a.actionButton(c, "capture.export-chosen-csv", "Export CSV…")
								if uikit.CompactButton(c, locales.ClearSelection()).Clicked() {
									a.chosen.Clear()
								}
							})
						}
						a.table(c)
					})
				}
				// Keep the plot and its pointer capture in the same split while
				// a brush passes through an interval with no matching requests.
				if a.guide {
					a.setupGuide(c)
				} else if len(a.visible) == 0 && !a.activity.active() {
					a.syncDetail()
					// The table's own empty state says why; an inspector
					// with nothing to show would only repeat it.
					table()
				} else if w.sideBySide {
					// The room under the toolbar, not the column's whole
					// height (Fill), which ran past the footer.
					ui.SplitPercent(c.Key("right-inspector"), &w.sidePercent, table, func() { a.detail(c) }).SplitReset(64).Grow(1).FillWidth().MinHeight(0)
				} else {
					ui.SplitVerticalPercent(c.Key("bottom-inspector"), &a.tablePercent, table, func() { a.detail(c) }).SplitReset(44).Grow(1).FillWidth().MinHeight(0)
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
	ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).
		Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
		box := ui.Row(c).Grow(1).MinWidth(160).Height(uikit.HeightControl).Gap(t.Space(1)).Padding(0, t.Space(1)).AlignItems(ui.Center)
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
			cb := ui.ComboboxBase(c.Key("capture-search"), &a.query)
			search := cb.Input.Bind(&a.search).Label(locales.SearchHostOrPath()).Placeholder(placeholder).
				Tooltip(cmp.Or(a.queryProblem, a.searchProblem, "Search host and path (including query) · key:value chips narrow it · /regex/ and * wildcards match the whole field · Ctrl/Cmd+F")).
				Height(uikit.HeightControl).Padding(0, t.Space(1)).FontSize(uikit.Scaled(uikit.TextBody)).Grow(1).MinWidth(min(200, width/5)).FocusRing(false).
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
				uikit.PopoverPanel(c, panel).Padding(t.Space(1)).Margin(t.Space(1.5), 0, 0, 0)
				for _, s := range suggestions[:min(len(suggestions), maxQuerySuggestions)] {
					item := cb.Item(s.text).Label(s.text).Height(uikit.HeightControl).Padding(0, t.Space(2)).Gap(t.Space(3)).Radius(uikit.RadiusControl).AlignItems(ui.Center)
					if item.Highlighted() {
						item.Background(t.SurfaceHover)
						first = first || s == suggestions[0]
					}
					item.Children(func() {
						ui.Text(c, s.text).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextData)).TextColor(t.Text).SingleLine().Grow(1)
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
				if uikit.IconButton(c, locales.ResetAll(), locales.ResetAllConditions(), closeIcon).Clicked() {
					a.views.resetFilters(a)
				}
			}
			// The conditions the field cannot spell -- the dialog's own
			// fields, the time range -- open from the field's end, so the
			// search is the one way to narrow the list.
			if uikit.IconButton(c, locales.Filters(), locales.StatusHeaderBodyMethodDuration(), filterIcon).Clicked() {
				a.openFilter()
			}
		})
		ui.ScrollHorizontal(c).Width(protocolWidth).Height(uikit.HeightControl).Children(func() {
			// Images and HTML live in the menu but remain valid selections.
			// The kinds read as tabs over the list: the chosen one is the
			// tabs' tinted chip, with no track behind them.
			p := uikit.Colors(t)
			segments := ui.SegmentedBase(c, &a.chip, len(captureKinds))
			segments.Track.Label(locales.ProtocolAndContentTypeLabel()).Height(uikit.HeightControl).Gap(t.Space(0.5)).Children(func() {
				for i, name := range captureProtocols {
					label := optionLabel(name)
					if name == "WebSocket" {
						label = "WS"
					}
					segment := segments.Segment(i).Label(locales.ProtocolAndContentType(name)).Height(uikit.HeightControl).
						Padding(0, t.Space(1.5)).Radius(uikit.RadiusControl).AlignItems(ui.Center).TextColor(t.TextMuted)
					switch {
					case a.chip == i:
						// A filter's chosen tab is a neutral face: green is
						// only for what is selected as data, primary or on.
						segment.Background(p.Fill).TextColor(t.Text)
					case segment.Pressed():
						segment.Background(t.SurfacePressed)
					case segment.Hovered():
						segment.Background(t.SurfaceHover)
					}
					segment.Children(func() {
						ui.Text(c, label).FontSize(uikit.Scaled(uikit.TextMeta))
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
		uikit.MoreMenu(c, locales.MoreCaptureOptions(), func(m *ui.Menu) { actionMenu(m, a.captureActions()) })
	})
	ui.Divider(c)
}

// Conditions are chips inside the search field; overflow scrolls rather
// than adding another toolbar row. Content fields use the view's AND/OR mode;
// scope conditions always narrow. A chip the query syntax can write turns back
// into text at the end of the field, so it can be changed and committed again.
func (a *app) captureConditions(c *ui.Context) {
	t := c.Theme()
	note := "All conditions must match"
	if a.filterAny {
		note = "Any content field may match (OR); search, sidebar, protocol and time range still narrow"
	}
	uikit.Tip(c, ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center), note).Children(func() {
		changed := func() {
			a.views.capture(a)
			a.applyFilter()
		}
		chip := func(label string, clear func()) {
			uikit.QueryChip(c, label, nil, func() { clear(); changed() })
		}
		if a.filterAny {
			uikit.QueryChip(c, "Content: OR", a.openFilter, func() { a.filterAny = false; changed() })
		}
		if a.filter != "all" {
			label := "Traffic: " + a.filter
			if host, ok := strings.CutPrefix(a.filter, "host:"); ok {
				label = "Host: " + host
			} else if client, ok := strings.CutPrefix(a.filter, "client:"); ok {
				label = "App: " + client
			}
			chip(label, func() { a.filter = "all" })
		}
		if a.chip != 0 {
			chip(captureKinds[a.chip], func() { a.chip = 0 })
		}
		if a.activity.active() {
			chip("Time: "+a.activity.label(), func() { a.activity = activityRange{} })
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

// captureActions are the capture toolbar's ⋯ menu.
func (a *app) captureActions() []action {
	actions := []action{
		do("capture.stop", locales.StopCapture(), a.stopCapture).when(a.srv != nil && a.srv.Running(), "Capture is not running").keys(ui.Cmd, ui.KeyPeriod),
		do("capture.start", locales.StartCapture(), func() { go a.startCapture() }).when(a.srv != nil && !a.srv.Running(), "Capture is already running").keys(ui.Cmd, ui.KeyPeriod),
		do("tools.rules", locales.DebuggingRules2(), a.openRules),
		do("collection.record", locales.RecordIntoCollection(), func() { a.openCollectionRecording("", "", "") }),
		do("tokens.analyze", locales.RecognizeTokenLinks(), func() { a.openTokenLinks(a.tokenLinkScope()) }),
		do("capture.saved-filters", locales.SaveFilter(), func() { a.views.openSaved(a) }),
	}
	for index := 6; index < len(captureKinds); index++ {
		actions = append(actions, do("capture.kind", optionLabel(captureKinds[index]), func() {
			a.chip = index
			a.applyFilter()
		}).checked(checkbox(a.chip == index)).in("kinds"))
	}
	actions = append(actions, do("capture.filter", locales.Filter(), a.openFilter).in("filter"))
	if a.filtersActive() {
		actions = append(actions, do("capture.reset-filters", locales.ResetAllConditions(), func() { a.views.resetFilters(a) }).in("filter"))
	}
	return append(actions,
		do("capture.add-column", locales.AddColumn(), a.openColumn).in("columns"),
		do("capture.clear", locales.ClearUnpinnedRequests2(), func() { a.clearOpen = true }).in("clear"),
		do("flow.undo-delete", locales.Undo(), a.undoDelete).when(len(a.deletions) > 0, "Nothing to undo").keys(ui.Cmd, ui.KeyZ),
	)
}

const inspectOne = "Inspect one HTTP request; bulk selections, tunnels and streams are unavailable"

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
			when(current && len(selected) > 0, "Select captured requests first"),
		do("tokens.from-capture", locales.RecognizeTokenLinks(), func() { a.openTokenLinks(selected) }).
			when(current && len(selected) > 1, "Select a source response and subsequent Bearer requests"),
		do("flow.edit", locales.EditRequest(), func() { a.editCapture(f) }).
			when(current && !bulk && f.kind == "", inspectOne).keys(ui.Cmd|ui.Alt, ui.KeyEnter),
		do("flow.replay", locales.ReplaySelected(), func() { a.openReplay(selected) }).
			when(replayable, "Choose HTTP requests only; tunnels and streams cannot be replayed").keys(ui.Cmd, ui.KeyR),
	}
	for _, item := range a.requestCopyActions(c, f, draft) {
		if item.id == "flow.copy-curl" {
			item = item.keys(ui.Cmd|ui.Alt, ui.KeyC)
		}
		actions = append(actions, item.when(copyable, inspectOne))
	}
	for _, item := range a.captureCopyActions(c, selected) {
		actions = append(actions, item.when(current && a.page.capture(), "Inspect captured requests first"))
	}
	actions = append(actions, do("flow.delete", locales.Delete(), func() { a.queueDeleteFlows(selected) }).
		when(current && len(selected) > 0, "Select captured requests first").in("danger").keys(ui.Cmd, flowDeleteKey()))
	return actions
}

// actions are the global commands: the palette lists them, the menu bar and
// shortcuts run them by id, and menus and buttons reuse them with pick.
func (a *app) actions(c *ui.Context) []action {
	w := &a.workbench
	f, current := a.current()
	capturing := a.page.capture()
	proxyReady := a.srv != nil && a.srv.Running()
	d := a.requestDocument
	// A captured request in LIVE is a document too; tabs are COLLECTIONS'.
	editing := d != nil && (a.page == pageLive || a.page == pageRequest)
	tab := editing && !a.page.capture()
	focus := "Focus inspector"
	if w.focused {
		focus = "Back to list"
	}
	recording := locales.PauseCapture()
	if a.recordingPaused {
		recording = locales.ResumeCapture()
	}
	primary := "Focus request list"
	if tab {
		primary = "Focus request URL"
	}
	search := locales.SearchHostOrPath()
	find := func() { a.search.Focus(c) }
	for side := range 2 {
		if c != nil && capturing && a.bodyText[side] != nil && a.bodyReader[side].FocusWithin(c) {
			search = "Find in body"
			find = func() { a.bodyText[side].Run(editor.Find) }
		}
	}
	draft := "Open a request draft first"
	actions := a.requestActions(c, f, capturing && current)
	actions = append(actions, []action{
		do("app.palette", locales.Commands(), w.openPalette).keys(ui.Cmd, ui.KeyK),
		do("view.search", search, find).when(capturing, "").keys(ui.Cmd, ui.KeyF),
		do("view.primary", primary, func() {
			if tab {
				d.urlInput.Focus(c)
			} else {
				a.focusTraffic(c)
			}
		}).when(capturing && len(a.visible) > 0 || editing, "").keys(ui.Cmd, ui.KeyL),
		do("request.send", locales.SendRequest(), a.sendComposed).
			when(editing && !d.composeWaiting && (d.protocol == "gRPC" || d.protocol == "MQTT" || d.protocol == "MCP" || a.srv != nil && a.srv.Running()), "Open a request draft; HTTP sends require capture running").keys(ui.Cmd, ui.KeyEnter),
		do("request.cancel-send", locales.CancelSend(), func() { d.cancelSend() }).
			when(editing && d.composeWaiting && d.sendCancel != nil && !d.sendCancelling, ""),
		do("request.duplicate", locales.DuplicateAsNewRequest(), func() { a.duplicateDocument(d) }).
			when(editing && !d.composeWaiting, draft).keys(ui.Cmd, ui.KeyD),
		do("request.save", locales.SaveRequest(), func() {
			if a.page.capture() && d.savedKey == "" {
				// A capture has no name or collection yet: ask for both.
				a.renameOpen, a.saveAfterRename = true, true
				return
			}
			a.saveComposed()
		}).when(editing, draft).keys(ui.Cmd, ui.KeyS),
		do("request.compare-source", locales.CompareWithSource(), func() {
			d.comparing = true
			if !tab {
				// The comparison replaces the editor that had the focus; keep
				// it in the inspector, where Escape returns to the list.
				a.workbench.inspector.Focus(c)
			}
		}).when(editing && d.source != nil && d.response != nil && !d.composeWaiting, "Send a draft opened from Capture before comparing it with its source").keys(ui.Cmd|ui.Shift, ui.KeyD),
		do("request.close", locales.CloseRequest(), func() { a.requestClose(d) }).
			when(tab && !d.composeWaiting, "Open a request draft; wait for its send to finish before closing").keys(ui.Cmd, ui.KeyW),
		do("request.next-tab", locales.NextOpenRequest(), func() { a.cycleDocument(1) }).
			when(len(a.documents) > 0, "Open a request first").keys(ui.Ctrl, ui.KeyTab),
		do("request.previous-tab", locales.PreviousOpenRequest(), func() { a.cycleDocument(-1) }).
			when(len(a.documents) > 0, "Open a request first").keys(ui.Ctrl|ui.Shift, ui.KeyTab),
		do("capture.export-chosen", locales.ExportSelectedHar(), a.exportChosen).when(capturing && a.chosen.Len() > 0, ""),
		do("capture.export-csv", locales.ExportCsv(), a.exportCSV).when(capturing && len(a.visible) > 0, "No visible requests to export"),
		do("capture.export-chosen-csv", locales.ExportChosenCsv(), a.exportChosenCSV).when(capturing && a.chosen.Len() > 0, "Choose requests first"),
		do("view.focus-inspector", focus, func() {
			w.focused = !w.focused
			if w.focused {
				w.inspector.Focus(c)
			} else {
				a.focusTraffic(c)
			}
		}).when(capturing && current, "").keys(ui.Cmd|ui.Shift, ui.KeyEnter),
		do("view.follow-end", locales.FollowListEnd(), func() {
			w.focused = false
			a.views.activeView().list.ScrollToEnd()
		}).when(capturing && len(a.visible) > 0, ""),
		do("capture.filter", locales.EditCaptureFilters(), a.openFilter).when(capturing, ""),
		do("capture.saved-filters", locales.ManageSavedFilters(), func() { a.views.openSaved(a) }).when(capturing, ""),
		do("capture.new-view", locales.NewCaptureView(), func() { a.views.capture(a); a.views.newView(a) }).when(capturing, ""),
		do("capture.reset-filters", locales.ResetAllCaptureConditions(), func() { a.views.resetFilters(a) }).when(capturing, ""),
		do("capture.break", fmt.Sprintf("Break requests · %t", a.breakOn), func() {
			a.breakOn = !a.breakOn
			a.srv.SetBreak(a.breakOn)
		}).when(a.srv != nil, ""),
		do("capture.stop", locales.StopCapture(), a.stopCapture).when(proxyReady, "Capture is not running").keys(ui.Cmd, ui.KeyPeriod),
		do("capture.start", locales.StartCapture(), func() { go a.startCapture() }).when(a.srv != nil && !proxyReady, "Capture is already running").keys(ui.Cmd, ui.KeyPeriod),
		do("capture.recording", recording, func() { a.recordingPaused = !a.recordingPaused }).
			when(a.srv != nil && a.srv.Running(), ""),
		do("collection.record", locales.RecordIntoCollection(), func() { a.openCollectionRecording("", "", "") }),
		do("view.layout-right", locales.LayoutSideBySide(), func() { w.layout, w.focused = "right", false }).
			checked(checkbox(w.layout == "right")),
		do("view.layout-bottom", locales.LayoutStacked(), func() { w.layout, w.focused = "bottom", false }).
			checked(checkbox(w.layout == "bottom")),
		do("view.sidebar", locales.ToggleSidebar(), func() { w.sidebarHidden = !w.sidebarHidden }).keys(ui.Cmd, ui.KeyB),
		do("flow.undo-delete", locales.Undo(), a.undoDelete).when(len(a.deletions) > 0, "Nothing to undo").keys(ui.Cmd, ui.KeyZ),
		do("view.layout-reset", locales.ResetDesktopLayout(), func() {
			w.layout, w.sidebarHidden, w.focused, w.sidePercent = "right", false, false, 64
			a.sidebarWidth, a.tablePercent = 220, 44
			a.setColumns(ui.TableLayout{})
		}),
		do("settings.https", locales.HttpsCertificateDecryptionSetup(), func() { a.openSettings(2) }),
		do("settings.certificates", locales.ClientCertificatesRoots(), func() { a.openSettings(6) }),
		do("network.diagnostics", locales.ConnectionDiagnostics(), func() { a.openDiagnostics(nil, diagnoseMac) }),
		do("app.setup-guide", locales.ShowSetupGuide(), a.openGuide).when(!a.guide, ""),
		do("history.open", locales.History2(), func() {
			a.experiments.scope, a.experiments.draftKey, a.experiments.open = nil, "", true
			a.experiments.assetID, a.experiments.status, a.experiments.sourceFilter = "", "", ""
		}),
		do("network.websocket", locales.WebsocketSession(), a.openSocket),
		do("network.sse", locales.SseSession(), a.openSSE),
		do("session.open", locales.OpenSession(), a.openSession),
		do("session.save", locales.SaveCaptureToSessions(), a.saveSession).when(capturing, ""),
		do("network.client", locales.SetUpALocalClient(), a.openClientSetup).when(a.srv != nil, ""),
		do("network.terminal", locales.OpenAProxiedTerminal(), a.openProxiedTerminal).when(proxyReady, "Start capture first"),
		do("network.chrome", locales.OpenProxiedChrome(), a.openProxiedChrome).when(proxyReady, "Start capture first"),
		do("network.firefox", locales.OpenProxiedFirefox(), a.openProxiedFirefox).when(proxyReady, "Start capture first"),
		do("network.android", locales.SetUpAnAndroidEmulator(), func() { a.openClientSetupTarget(clientTargetAndroid) }),
		do("network.ios", locales.InstallCaInIosSimulator(), func() { a.openClientSetupTarget(clientTargetIOS) }).
			when(runtime.GOOS == "darwin", "iOS simulators are available only on macOS"),
		do("network.java", locales.ImportCaIntoJavaTruststore(), func() { a.openClientSetupTarget(clientTargetJava) }),
		do("network.electron", locales.LaunchAnElectronApp(), func() { a.openClientSetupTarget(clientTargetElectron) }),
		do("network.devices", locales.ConnectADevice(), a.openDevices).when(a.srv != nil, ""),
		do("tools.rules", locales.ManageDebuggingRules(), a.openRules),
	}...)
	actions = append(actions, a.hostsActions()...)
	actions = append(actions, []action{
		do("vault.open", locales.Vault(), a.openVault),
		do("environment.manage", locales.ManageEnvironments(), a.openVars),
		do("request.code", locales.GenerateCode(), a.openCode).when(editing, draft),
		do("collection.settings", locales.CollectionSettings(), func() { a.openFolder(d.composeCollection) }).
			when(editing && d.composeCollection != "", ""),
		do("request.new", locales.NewRequest(), a.openCompose).keys(ui.Cmd, ui.KeyN),
		do("request.import-curl", locales.ImportCurl(), a.openCurlImport),
		do("collection.reload", locales.ReloadCollectionsFromDisk(), func() {
			a.loadRequestAssets()
			a.done(locales.ReloadedRequests(len(a.requests), a.library.root))
		}),
		do("capture.import-session", locales.ImportCaptureSession(), a.importCaptureSession),
		do("capture.import-proxyman-folder", locales.ImportProxymanLogFolder(), a.importCaptureFolder),
		do("rules.import-charles", locales.ImportCharlesSettings(), a.importCharlesSettings),
		do("collection.import", locales.ImportText(), a.importRequests),
		do("collection.import-folder", locales.ImportCollectionFolder(), a.importCollectionFolder),
		do("collection.export-postman", locales.ExportCollectionAsPostman(), func() { a.exportPostman(collectionRoot(d.composeCollection)) }).
			when(editing && d.composeCollection != "", ""),
		do("app.settings", locales.OpenSettings(), func() { a.openSettings(-1) }).keys(ui.Cmd, ui.KeyComma),
		do("go.live", locales.GoToLiveTraffic(), func() {
			a.page = pageLive
			a.revealGroup = "live"
			*a.regionOpen("traffic") = true
			a.focusTraffic(c)
		}).keys(ui.Cmd, ui.Key1),
		do("go.collections", locales.GoToCollections(), func() {
			a.page = pageRequest
			a.revealGroup = "collections"
			*a.regionOpen("collections") = true
			if a.requestDocument != nil {
				a.urlInput.Focus(c)
			}
		}).keys(ui.Cmd, ui.Key2),
		do("go.sessions", locales.GoToSessions(), func() {
			a.page = pageSession
			a.revealGroup = "sessions"
		}).keys(ui.Cmd, ui.Key3),
		do("go.apis", locales.ApiContracts(), func() { a.page = pageAPI }).
			when(len(a.apis) > 0, "Create or import an API contract first"),
		do("api.new", locales.NewApiContract(), func() {
			a.newAPI()
			a.page = pageAPI
		}),
		do("api.import-openapi", locales.ImportOpenapi(), a.importOpenAPI),
		do("history.show", locales.ShowHistory(), a.showRunRecords),
		do("flow.save-evidence", locales.SaveEvidenceToSessions(), func() { a.saveEvidence(f) }).when(capturing && current, ""),
	}...)
	actions = append(actions, a.grpcActions(d, editing)...)
	actions = append(actions, a.socketIOActions(d, editing)...)
	actions = append(actions, a.mqttActions(d, editing)...)
	actions = append(actions, a.mcpActions(d, editing)...)
	return actions
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
	if !w.paletteOpen {
		return
	}
	uikit.Modal(c, &w.paletteOpen, func() {
		width, _ := c.Size()
		t := c.Theme()
		ui.Column(c).Width(min(560, width-48)).Gap(t.Space(3)).Children(func() {
			uikit.DialogTitle(c, locales.Commands2(
			// Modal focuses its first input after remembering the opener. An
			// explicit AutoFocus here would replace that remembered focus.
			))

			input := uikit.Input(c, &w.query).Label(locales.FindCommand()).Placeholder(locales.SearchCommandsRequestsEnvironments()).
				Height(uikit.HeightLarge).FontSize(uikit.Scaled(uikit.TextBody)).FillWidth()
			if input.Changed() {
				w.command = 0
				w.commands.ScrollTo(0, ui.Start)
			}
			// An empty query lists what can run now. A search also finds
			// unavailable commands, after the available ones, so asking for
			// one explains why it cannot run instead of hiding it. Saved
			// collections, requests and variables follow the available
			// commands; their index is rebuilt only when the library changes.
			query := strings.ToLower(strings.TrimSpace(w.query))
			var matches, unavailable []paletteEntry
			for _, action := range a.actions(c) {
				switch {
				case action.id == "app.palette" || !strings.Contains(strings.ToLower(action.label), query):
				case action.enabled:
					matches = append(matches, paletteEntry{
						label: action.label, detail: action.hint(), meta: action.shortcut.String(),
						enabled: true, action: true, run: action.run,
					})
				case query != "":
					unavailable = append(unavailable, paletteEntry{
						label: action.label, detail: action.hint(), meta: action.shortcut.String(),
						enabled: false, why: action.why(), action: true, run: action.run,
					})
				}
			}
			assets := a.paletteSearch(query)
			matches = append(matches, assets.entries...)
			matches = append(matches, unavailable...)
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
			list := ui.List(c, &w.commands, len(matches), func(i int) {
				item := matches[i]
				b := uikit.Tip(c, uikit.RowButton(c, item.label).Disabled(!item.enabled).Height(uikit.HeightLarge).Padding(0, t.Space(3)), item.detail)
				// The list's highlight shows through; RowButton saw the row
				// before Disabled, so an unavailable command drops its hover too.
				if !item.enabled || i == w.command && !b.Pressed() {
					b.Background(ui.Transparent)
				}
				b.Children(func() {
					color := t.Text
					if !item.enabled {
						color = uikit.Colors(t).TextFaint
					}
					label := item.label
					if !item.enabled {
						label += " (unavailable)"
					}
					ui.Text(c, label).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(color)
					ui.Spacer(c)
					ui.Text(c, item.meta).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(color)
				})
				if b.Clicked() {
					w.command = i
					run()
				}
			}).Height(9 * uikit.HeightLarge).FillWidth()
			c.SetTheme(t)
			if list.Submitted() {
				run()
			}
			if len(matches) == 0 {
				uikit.Hint(c, locales.NoMatchingCommandsRequestsOr())
			} else if w.command >= 0 && w.command < len(matches) {
				item := matches[w.command]
				message := ""
				switch {
				case !item.enabled:
					message = item.why
				case !item.action:
					message = item.detail
				case a.page.capture():
					if a.chosen.Len() > 1 {
						message = fmt.Sprintf("Capture · %d selected requests", a.chosen.Len())
					} else if f, ok := a.current(); ok {
						message = fmt.Sprintf("Capture #%d · %s %s", f.id, f.method, f.path)
					}
				case !a.page.capture() && a.requestDocument != nil:
					message = "Draft · " + a.title()
				}
				uikit.Tip(c, uikit.Meta(c, message).SingleLine(), message)
			}
			if assets.more > 0 {
				uikit.Meta(c, locales.MoreResults(assets.more)).SingleLine()
			}
			uikit.Meta(c, locales.ChooseEnterRunEscClose())
		})
	})
}
