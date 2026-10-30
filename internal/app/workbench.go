package app

import (
	"fmt"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// desktopWorkbench owns presentation, not the capture view's filters or choice.
// Focusing an inspector is temporary; only the normal layout is persisted.
type desktopWorkbench struct {
	layout               string
	sidePercent          float32
	sidebarHidden        bool
	focused              bool
	sideBySide           bool
	paletteOpen          bool
	query                string
	command              int
	commands             ui.ListState
	inspector            ui.Handle
	content              ui.Handle
	menuAction           string
	menuItems            map[string]*mygo.MenuItem
	requestSidebarHidden bool
}

func (w *desktopWorkbench) openPalette() {
	w.paletteOpen, w.query, w.command = true, "", 0
	w.commands.ScrollTo(0, ui.Start)
}

// contentWidth is the room the main pane has beside the sidebar.
func (a *app) contentWidth(c *ui.Context) float32 {
	width, _ := c.Size()
	if !a.workbench.sidebarHidden && !(a.workspace == 1 && (a.workbench.requestSidebarHidden || a.requestsSection == 2)) {
		width -= a.sidebarWidth + 6
	}
	if a.workspace == 1 {
		width -= requestRailWidth
	}
	return width
}

func (a *app) desktopContent(c *ui.Context) {
	w := &a.workbench
	available := a.contentWidth(c)
	// Even an explicitly requested side inspector yields on small windows.
	// It returns, with its own divider position, when there is room again.
	w.sideBySide = w.layout == "right" && available >= 1000 || w.layout == "adaptive" && available >= 1160
	if w.sidePercent == 0 {
		w.sidePercent = 64
	}
	content := func() {
		switch a.workspace {
		case 1:
			if a.requestsSection == 2 {
				if !a.experiments.open {
					a.experimentsView(c, c.Theme(), true)
				}
				return
			}
			if a.requestsSection == 1 {
				a.apiView(c, c.Theme())
				return
			}
			ui.Column(c).Fill().Children(func() {
				a.documentTabs(c, c.Theme())
				a.composeView(c, c.Theme())
			})
		default:
			if a.captureSection == 1 {
				a.boardView(c, c.Theme())
				return
			}
			ui.Column(c).Fill().Children(func() {
				if w.focused {
					ui.Row(c).Height(32).Padding(0, 12).AlignItems(ui.Center).Children(func() {
						a.desktopButton(c, "focus", "Back to list")
						ui.Text(c, "Inspector focus · Ctrl/Cmd+Shift+Enter").FontSize(11).TextColor(c.Theme().TextMuted)
					})
					a.detail(c)
					return
				}
				a.captureToolbar(c, c.Theme())
				table := func() {
					ui.Column(c).Fill().Children(func() {
						if a.chosen.Len() > 1 {
							ui.Row(c).Height(34).Padding(0, 10).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, fmt.Sprintf("%d selected", a.chosen.Len())).FontSize(11).Bold().Grow(1)
								a.desktopButton(c, "replay", "Replay…")
								a.desktopButton(c, "export-selection", "Export HAR…")
								if ui.Button(c, "Clear selection").FontSize(11).Height(24).Clicked() {
									a.chosen.Clear()
								}
							})
						}
						a.table(c)
					})
				}
				if len(a.visible) == 0 {
					a.syncDetail()
					table()
					ui.Row(c).Height(heightLarge).Padding(0, 12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "No request chosen").FontSize(textBody).TextColor(c.Theme().TextMuted)
					})
				} else if w.sideBySide {
					ui.SplitPercent(c.Key("right-inspector"), &w.sidePercent, table, func() { a.detail(c) }).Fill()
				} else {
					ui.SplitVerticalPercent(c.Key("bottom-inspector"), &a.tablePercent, table, func() { a.detail(c) }).Fill()
				}
			})
		}
	}
	ui.Row(c).Grow(1).FillWidth().Children(func() {
		if a.workspace == 1 {
			a.requestRail(c)
		}
		if w.sidebarHidden || w.focused && a.workspace == 0 || a.workspace == 1 && (w.requestSidebarHidden || a.requestsSection == 2) {
			ui.Column(c).Grow(1).FillHeight().Children(content)
		} else {
			ui.Split(c, &a.sidebarWidth, func() { a.sidebar(c) }, content).Grow(1).FillHeight()
		}
	})
}

var captureKinds = []string{"All", "HTTP", "HTTPS", "WebSocket", "SSE", "JSON", "Images", "HTML"}

// captureProtocols are the filters the toolbar shows; the rest live in its
// overflow menu.
var captureProtocols = captureKinds[:6]

func (a *app) captureToolbar(c *ui.Context, t *ui.Theme) {
	width := a.contentWidth(c)
	ui.Row(c).Height(44).Padding(0, 12).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Grow(1).MinWidth(160).Height(heightControl).Gap(4).Padding(0, 4).
			Border(1, t.Border).Radius(radiusControl).AlignItems(ui.Center).Children(func() {
			search := ui.TextInputBase(c, &a.query).Bind(&a.search).Label("Search host or path").Placeholder("Search host or path…").
				Tooltip("URL host and path only · Ctrl/Cmd+F · Enter or ↓ moves to requests. Filters search headers and full bodies.").
				Height(heightCompact).Padding(0, 8).FontSize(textBody).Grow(1).MinWidth(72)
			search.OnShortcut(0, ui.KeyDown, func() { a.focusTraffic(c) })
			if search.Changed() {
				a.applyFilter()
			}
			if search.Submitted() {
				a.focusTraffic(c)
			}
			if a.filtersActive() {
				ui.ScrollHorizontal(c).Width(min(260, width/4)).FillHeight().Children(func() {
					a.captureConditions(c)
				})
				if iconButton(c, "Reset all", "Reset all conditions", closeIcon).Clicked() {
					a.views.resetFilters(a)
				}
			}
		})
		ui.ScrollHorizontal(c).Width(min(302, width*0.36)).Height(heightControl).Children(func() {
			// Images and HTML live in the menu but remain valid selections.
			segments := ui.SegmentedBase(c, &a.chip, len(captureKinds))
			segments.Track.Label("Protocol and content type").Height(heightControl).Padding(2).Gap(1).Radius(radiusControl).Background(t.Surface).Children(func() {
				for i, name := range captureProtocols {
					label := name
					if name == "WebSocket" {
						label = "WS"
					}
					segment := segments.Segment(i).Label("Protocol and content type: "+name).Height(heightCompact).
						Padding(0, 7).Radius(radiusControl).AlignItems(ui.Center).TextColor(t.TextMuted)
					if a.chip == i {
						segment.Background(t.Background).TextColor(t.Text)
					} else if segment.Hovered() {
						segment.Background(t.SurfaceHover)
					}
					segment.Children(func() { ui.Text(c, label).FontSize(textMeta) })
				}
			})
			if segments.Track.Changed() {
				a.applyFilter()
			}
		})
		if toolbarButton(c, "Filters…", "Status, header, body, method, duration and notes", a.filtersActive()).Width(heightControl).Padding(0).Justify(ui.Center).Children(func() {
			ui.Icon(c, filterIcon).Size(iconInline, iconInline)
		}).Clicked() {
			a.openFilter()
		}
		breakButton := toolbarButton(c, "Breakpoints", "Pause each matching request or response before it continues", a.breakOn).
			Width(heightControl).Padding(0).Justify(ui.Center)
		if breakButton.Clicked() && a.srv != nil {
			a.breakOn = !a.breakOn
			a.srv.SetBreak(a.breakOn)
		}
		breakButton.Children(func() { ui.Icon(c, breakIcon).Size(iconInline, iconInline) })
		ui.PopoverMenuButton(c, "Layout", func(m *ui.Menu) {
			for _, id := range []string{"layout-bottom", "layout-right", "layout-auto", "focus"} {
				a.desktopMenuItem(c, m, id, "")
			}
		}).Label("Inspector layout").Tooltip("Inspector layout").Height(heightControl).Padding(0, 6).
			FontSize(textMeta).TextColor(t.TextMuted).Border(0, t.Border)
		ui.PopoverMenuButton(c, "⋯", func(m *ui.Menu) {
			if m.Item("Debugging rules…").Chosen() {
				a.openRules()
			}
			if m.Item("Save filter…").Chosen() {
				a.views.openSaved(a)
			}
			m.Separator()
			for _, index := range []int{6, 7} {
				if m.Item(captureKinds[index]).Checked(a.chip == index).Chosen() {
					a.chip = index
					a.applyFilter()
				}
			}
			m.Separator()
			if m.Item("Filter…").Chosen() {
				a.openFilter()
			}
			if a.filtersActive() && m.Item("Reset all conditions").Chosen() {
				a.views.resetFilters(a)
			}
			m.Separator()
			if m.Item("Add column…").Chosen() {
				a.openColumn()
			}
			m.Separator()
			if m.Item("Clear unpinned requests…").Chosen() {
				a.clearOpen = true
			}
		}).
			Label("More capture options").Tooltip("More capture options").Height(heightCompact).Padding(0, t.Space(2)).FontSize(textBody).TextColor(t.TextMuted).Border(0, t.Border)
	})
	ui.Divider(c)
}

// Conditions stay removable inside the search field; overflow scrolls rather
// than adding another toolbar row. Their AND/OR semantics are unchanged.
func (a *app) captureConditions(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(4).AlignItems(ui.Center).Tooltip("All conditions must match").Children(func() {
		token := func(label string, clear func()) {
			if secondaryButton(c, clamp(label, 30)+" ×").Label(label+" ×").Height(22).Padding(0, 6).FontSize(textMeta).TextColor(t.Accent).Tooltip("Remove condition: " + label).Clicked() {
				clear()
				a.views.capture(a)
				a.applyFilter()
			}
		}
		if a.query != "" {
			token("URL: "+a.query, func() { a.query = "" })
		}
		if a.filter != "all" {
			label := "Traffic: " + a.filter
			if host, ok := strings.CutPrefix(a.filter, "host:"); ok {
				label = "Host: " + host
			} else if client, ok := strings.CutPrefix(a.filter, "client:"); ok {
				label = "App: " + client
			}
			token(label, func() { a.filter = "all" })
		}
		if a.chip != 0 {
			token(captureKinds[a.chip], func() { a.chip = 0 })
		}
		for _, field := range []struct {
			label string
			value *string
		}{
			{"App", &a.filterTriage.Client}, {"Host", &a.filterTriage.Host},
			{"Status", &a.filterStatus}, {"Header", &a.filterHeader}, {"Body", &a.views.activeView().body},
			{"Method", &a.filterTriage.Method}, {"Duration ms", &a.filterTriage.Duration},
			{"Response bytes", &a.filterTriage.Size}, {"Color", &a.filterTriage.Color}, {"Note", &a.filterTriage.Note},
		} {
			if *field.value != "" {
				token(field.label+": "+*field.value, func() { *field.value = "" })
			}
		}
	})
}

// desktopAction is shared by the palette, shortcuts and contextual buttons:
// unavailable actions are never given a different meaning by another entry.
type desktopAction struct {
	id, label string
	enabled   bool
	mods      ui.Modifiers
	key       ui.Key
	run       func()
}

// Row menus and global commands share these target rules. Single-row actions
// use the inspected/clicked request; a bulk replay always uses the whole choice.
func (a *app) requestActions(c *ui.Context, f flow, current bool) []desktopAction {
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
	copyRequest := func() { a.copyCurl(c, f) }
	if d := a.requestDocument; a.workspace == 1 && d != nil {
		copyable = true
		copyRequest = func() { a.copyDraftCurl(c, d) }
	}
	return []desktopAction{
		{"edit", "Edit & replay", current && !bulk && f.kind == "", ui.Cmd | ui.Alt, ui.KeyEnter, func() { a.composeFrom(f) }},
		{"replay", "Replay selected…", replayable, ui.Cmd, ui.KeyR, func() { a.openReplay(selected) }},
		{"copy", "Copy as cURL", copyable, ui.Cmd | ui.Alt, ui.KeyC, copyRequest},
	}
}

func (a *app) desktopActions(c *ui.Context) []desktopAction {
	w := &a.workbench
	f, current := a.current()
	capturing := a.workspace == 0
	d := a.requestDocument
	editing := a.workspace == 1 && d != nil
	focus := "Focus inspector"
	if w.focused {
		focus = "Back to list"
	}
	primary := "Focus request list"
	if editing {
		primary = "Focus request URL"
	}
	search := "Search host or path"
	find := func() { a.search.Focus(c) }
	for side := range 2 {
		if c != nil && capturing && a.bodyText[side] != nil && a.bodyReader[side].FocusWithin(c) {
			search = "Find in body"
			find = func() { a.bodyText[side].Run(editor.Find) }
		}
	}
	actions := a.requestActions(c, f, capturing && current)
	actions = append(actions, []desktopAction{
		{"palette", "Commands…", true, ui.Cmd, ui.KeyK, w.openPalette},
		{"search", search, capturing, ui.Cmd, ui.KeyF, find},
		{"primary", primary, capturing && len(a.visible) > 0 || editing, ui.Cmd, ui.KeyL, func() {
			if editing {
				d.urlInput.Focus(c)
			} else {
				a.focusTraffic(c)
			}
		}},
		{"send", "Send request", editing && !d.composeWaiting && a.srv != nil && a.srv.Running(), ui.Cmd, ui.KeyEnter, a.sendComposed},
		{"cancel-send", "Cancel send", editing && d.composeWaiting && d.sendCancel != nil && !d.sendCancelling, 0, 0, func() { d.cancelSend() }},
		{"save-request", "Save request", editing, ui.Cmd, ui.KeyS, func() { a.saveComposed() }},
		{"compare-source", "Compare with source", editing && d.source != nil && d.response != nil && !d.composeWaiting, ui.Cmd | ui.Shift, ui.KeyD, func() { d.comparing = true }},
		{"back", "Back to traffic", editing, ui.Cmd, ui.KeyBracketLeft, func() { a.backToTraffic(c) }},
		{"close-request", "Close request tab", editing && !d.composeWaiting, ui.Cmd, ui.KeyW, func() { a.requestClose(d) }},
		{"next-tab", "Next debugging tab", len(a.documents) > 0, ui.Ctrl, ui.KeyTab, func() { a.cycleDocument(1) }},
		{"previous-tab", "Previous debugging tab", len(a.documents) > 0, ui.Ctrl | ui.Shift, ui.KeyTab, func() { a.cycleDocument(-1) }},
		{"export-selection", "Export selected HAR…", capturing && a.chosen.Len() > 0, 0, 0, a.exportChosen},
		{"focus", focus, capturing && current, ui.Cmd | ui.Shift, ui.KeyEnter, func() {
			w.focused = !w.focused
			if w.focused {
				w.inspector.Focus(c)
			} else {
				a.focusTraffic(c)
			}
		}},
		{"latest", "Follow list end", capturing && len(a.visible) > 0, 0, 0, func() {
			w.focused = false
			a.views.activeView().list.ScrollToEnd()
		}},
		{"filters", "Edit capture filters…", capturing, 0, 0, a.openFilter},
		{"saved", "Manage saved filters…", capturing, 0, 0, func() { a.views.openSaved(a) }},
		{"new-view", "New capture view", capturing, 0, 0, func() { a.views.capture(a); a.views.newView(a) }},
		{"clear-filters", "Reset all capture conditions", capturing, 0, 0, func() {
			a.views.resetFilters(a)
		}},
		{"break", fmt.Sprintf("Break requests · %t", a.breakOn), a.srv != nil, 0, 0, func() {
			a.breakOn = !a.breakOn
			a.srv.SetBreak(a.breakOn)
		}},
		{"layout-auto", "Inspector: Adaptive", capturing, 0, 0, func() { w.layout, w.focused = "adaptive", false }},
		{"layout-right", "Inspector: Right (wide windows)", capturing, 0, 0, func() { w.layout, w.focused = "right", false }},
		{"layout-bottom", "Inspector: Bottom", capturing, 0, 0, func() { w.layout, w.focused = "bottom", false }},
		{"sidebar", "Toggle sidebar", true, ui.Cmd, ui.KeyB, func() { w.sidebarHidden = !w.sidebarHidden }},
		{"layout-reset", "Reset desktop layout", true, 0, 0, func() {
			w.layout, w.sidebarHidden, w.focused, w.sidePercent = "bottom", false, false, 64
			a.sidebarWidth, a.tablePercent = 220, 44
			a.columns = ui.TableLayout{}
		}},
		{"https", "HTTPS certificate & decryption setup…", true, 0, 0, func() { a.settingsTab, a.settingsOpen = 2, true }},
		{"diagnostics", "Capture diagnostics…", true, 0, 0, a.openDiagnostics},
		{"experiments", "Run records…", true, 0, 0, func() {
			a.experiments.scope, a.experiments.draftKey, a.experiments.open = nil, "", true
		}},
		{"websocket", "WebSocket session…", true, 0, 0, a.openSocket},
		{"investigations", "Historical sessions & evidence…", true, 0, 0, a.investigations.show},
		{"client", "Set up a local client…", a.srv != nil, 0, 0, a.openClientSetup},
		{"devices", "Connect a device…", a.srv != nil, 0, 0, a.openDevices},
		{"rules", "Manage debugging rules…", true, 0, 0, a.openRules},
		{"vault", "Vault…", true, 0, 0, a.openVault},
		{"environments", "Manage environments…", true, 0, 0, a.openVars},
		{"code", "Generate code…", editing, 0, 0, a.openCode},
		{"collection", "Collection settings…", editing && d.composeCollection != "", 0, 0, func() { a.openFolder(d.composeCollection) }},
		{"new-request", "New request", true, ui.Cmd, ui.KeyN, a.openCompose},
		{"import-curl", "Import cURL…", true, 0, 0, a.openCurlImport},
		{"reload-library", "Reload collections from disk", true, 0, 0, func() {
			a.loadRequestAssets()
			a.setMessage(fmt.Sprintf("Reloaded %d %s from %s", len(a.requests), plural(len(a.requests), "request", "requests"), a.library.root))
		}},
		{"import-postman", "Import…", true, 0, 0, a.importRequests},
		{"export-postman", "Export collection as Postman…", editing && d.composeCollection != "", 0, 0, func() { a.exportPostman(collectionRoot(d.composeCollection)) }},
		{"settings", "Open settings…", true, ui.Cmd, ui.KeyComma, func() { a.settingsOpen = true }},
		{"capture-workspace", "Capture", true, ui.Cmd, ui.Key1, func() {
			a.workspace, a.captureSection = 0, 0
			a.sidebarChoice = cmpOr(a.filter, "all")
			a.focusTraffic(c)
		}},
		{"requests-workspace", "Requests", true, ui.Cmd, ui.Key2, func() {
			a.workspace, a.requestsSection = 1, 0
			if a.requestDocument != nil {
				a.urlInput.Focus(c)
			}
		}},
		{"apis-workspace", "API contracts", true, ui.Cmd, ui.Key3, func() {
			a.workspace, a.requestsSection = 1, 1
			a.requestSide = 3
			if a.apiChoice == "" && len(a.apis) > 0 {
				a.apiChoice = a.apis[0].spec.ID
			}
		}},
		{"investigate-workspace", "Investigations", true, ui.Cmd, ui.Key4, func() {
			a.workspace, a.captureSection = 0, 1
		}},
		{"add-investigation", "Add inspected request to investigation", capturing && current && f.kind == "", 0, 0, func() {
			a.addToInvestigation(f)
		}},
	}...)
	return actions
}

func (a *app) focusTraffic(c *ui.Context) {
	a.workbench.focused = false
	if view := a.views.activeView(); view != nil && len(a.visible) > 0 {
		view.list.Handle.Focus(c)
	}
}

func (a *app) desktopShortcuts(c *ui.Context) {
	for _, action := range a.desktopActions(c) {
		if action.enabled && action.key != 0 {
			c.OnShortcut(action.mods, action.key, action.run)
		}
	}
}

func (a *app) desktopButton(c *ui.Context, id, label string) ui.Element {
	for _, action := range a.desktopActions(c) {
		if action.id == id {
			b := ui.Button(c, label).Height(28).FontSize(11).Disabled(!action.enabled).Tooltip(action.hint())
			if b.Clicked() {
				action.run()
			}
			return b
		}
	}
	return ui.Element{}
}

func (action desktopAction) unavailable() string {
	switch action.id {
	case "edit", "copy":
		return "Inspect one HTTP request; bulk selections, tunnels and streams are unavailable"
	case "replay":
		return "Choose HTTP requests only; tunnels and streams cannot be replayed"
	case "send":
		return "Open a request draft with capture running; wait for any send in progress"
	case "compare-source":
		return "Send a draft opened from Capture before comparing it with its source"
	case "close-request":
		return "Open a request draft; wait for its send to finish before closing"
	case "save-request", "back":
		return "Open a request draft first"
	case "next-tab", "previous-tab":
		return "Open a request tab in Capture or Requests"
	default:
		return "Unavailable in the current workspace or selection"
	}
}

func (action desktopAction) hint() string {
	text := action.label
	if key := action.shortcut(); key != "" {
		text += " · " + key
	}
	if !action.enabled {
		text += " · " + action.unavailable()
	}
	return text
}

// The same spelling feeds native accelerators and visible shortcut hints.
func (action desktopAction) accelerator() string {
	if action.key == 0 {
		return ""
	}
	text := ""
	if action.mods&ui.Cmd != 0 {
		text += "CmdOrCtrl+"
	} else if action.mods&ui.Ctrl != 0 {
		text += "Ctrl+"
	}
	if action.mods&ui.Alt != 0 {
		text += "Alt+"
	}
	if action.mods&ui.Shift != 0 {
		text += "Shift+"
	}
	switch {
	case action.key >= ui.KeyA && action.key <= ui.KeyZ:
		return text + string('A'+rune(action.key-ui.KeyA))
	case action.key >= ui.Key0 && action.key <= ui.Key9:
		return text + string('0'+rune(action.key-ui.Key0))
	case action.key == ui.KeyEnter:
		return text + "Enter"
	case action.key == ui.KeyTab:
		return text + "Tab"
	case action.key == ui.KeyComma:
		return text + ","
	case action.key == ui.KeyBracketLeft:
		return text + "["
	}
	return ""
}

func (action desktopAction) shortcut() string {
	modifier := "Ctrl"
	if runtime.GOOS == "darwin" {
		modifier = "Cmd"
	}
	return strings.ReplaceAll(action.accelerator(), "CmdOrCtrl", modifier)
}

func (a *app) desktopMenuItem(c *ui.Context, m *ui.Menu, id, label string) {
	desktopMenuItem(m, a.desktopActions(c), id, label)
}

func desktopMenuItem(m *ui.Menu, actions []desktopAction, id, label string) {
	for _, action := range actions {
		if action.id == id {
			if label == "" {
				label = action.label
			}
			if m.Item(label).Shortcut(action.mods, action.key).Disabled(!action.enabled).Chosen() {
				action.run()
			}
			return
		}
	}
}

func (a *app) backToTraffic(c *ui.Context) {
	d := a.requestDocument
	a.views.capture(a)
	if d != nil && d.sourceView != nil {
		if i := slices.Index(a.views.views, d.sourceView); i >= 0 {
			a.views.activate(a, i)
		} else {
			a.setMessage("Original capture view is no longer open; returned to the current traffic view")
		}
	}
	a.workspace, a.captureSection = 0, 0
	a.sidebarChoice = cmpOr(a.filter, "all")
	a.focusTraffic(c)
}

// Native menu callbacks enter the next build, rather than retaining a Context
// or invoking commands behind a modal. Modal focus is outside content.
func (a *app) desktopNativeMenu(c *ui.Context) {
	w := &a.workbench
	pending := w.menuAction
	w.menuAction = ""
	for _, action := range a.desktopActions(c) {
		available := action.enabled && w.content.FocusWithin(c)
		if item := w.menuItems[action.id]; item != nil && item.IsEnabled() != available {
			item.SetEnabled(available)
		}
		if pending == action.id && available {
			action.run()
			c.Invalidate()
		}
	}
}

func (a *app) commandPalette(c *ui.Context) {
	w := &a.workbench
	if !w.paletteOpen {
		return
	}
	appModal(c, &w.paletteOpen, func() {
		width, _ := c.Size()
		t := c.Theme()
		ui.Column(c).Width(min(560, width-48)).Gap(t.Space(3)).Children(func() {
			dialogTitle(c, "Commands")
			// Modal focuses its first input after remembering the opener. An
			// explicit AutoFocus here would replace that remembered focus.
			input := ui.TextInput(c, &w.query).Label("Find command").Placeholder("Search actions…").
				Height(heightLarge).FontSize(textBody).FillWidth()
			if input.Changed() {
				w.command = 0
				w.commands.ScrollTo(0, ui.Start)
			}
			// An empty query lists what can run now. A search also finds
			// unavailable commands, after the available ones, so asking for
			// one explains why it cannot run instead of hiding it.
			query := strings.ToLower(strings.TrimSpace(w.query))
			var matches, unavailable []desktopAction
			for _, action := range a.desktopActions(c) {
				switch {
				case action.id == "palette" || !strings.Contains(strings.ToLower(action.label), query):
				case action.enabled:
					matches = append(matches, action)
				case query != "":
					unavailable = append(unavailable, action)
				}
			}
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
			selectionTheme := *t
			if !focused {
				selectionTheme.Accent, selectionTheme.AccentText = t.Accent.Alpha(0.12), t.Text
			}
			c.SetTheme(&selectionTheme)
			list := ui.List(c, &w.commands, len(matches), func(i int) {
				action := matches[i]
				b := ui.ButtonBase(c).Label(action.label).Height(heightLarge).FillWidth().Padding(0, t.Space(3)).
					Gap(t.Space(2)).Radius(radiusControl).AlignItems(ui.Center).Disabled(!action.enabled).Tooltip(action.hint())
				if action.enabled {
					if b.Pressed() {
						b.Background(t.SurfacePressed)
					} else if b.Hovered() && i != w.command {
						b.Background(t.SurfaceHover)
					}
				}
				b.Children(func() {
					color := t.Text
					if i == w.command && focused {
						color = t.AccentText
					} else if !action.enabled {
						color = appColors(t).TextFaint
					}
					label := action.label
					if !action.enabled {
						label += " (unavailable)"
					}
					ui.Text(c, label).FontSize(textBody).TextColor(color)
					ui.Spacer(c)
					ui.Text(c, action.shortcut()).FontSize(textMeta).TextColor(color)
				})
				if b.Clicked() {
					w.command = i
					run()
				}
			}).Height(9 * heightLarge).FillWidth()
			c.SetTheme(t)
			if list.Submitted() {
				run()
			}
			if len(matches) == 0 {
				ui.Text(c, "No matching commands").FontSize(textBody).TextColor(t.TextMuted)
			} else if w.command >= 0 && w.command < len(matches) {
				action := matches[w.command]
				message := ""
				if !action.enabled {
					message = action.unavailable()
				} else if a.workspace == 0 {
					if a.chosen.Len() > 1 {
						message = fmt.Sprintf("Capture · %d selected requests", a.chosen.Len())
					} else if f, ok := a.current(); ok {
						message = fmt.Sprintf("Capture #%d · %s %s", f.id, f.method, f.path)
					}
				} else if a.workspace == 1 && a.requestDocument != nil {
					message = "Draft · " + a.title()
				}
				ui.Text(c, message).FontSize(textMeta).TextColor(t.TextMuted).SingleLine().Tooltip(message)
			}
			ui.Text(c, "↑ ↓ Choose · Enter Run · Esc Close").FontSize(textMeta).TextColor(t.TextMuted)
		})
	})
}
