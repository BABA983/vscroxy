package main

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
}

func (w *desktopWorkbench) openPalette() {
	w.paletteOpen, w.query, w.command = true, "", 0
	w.commands.ScrollTo(0, ui.Start)
}

func (a *app) desktopContent(c *ui.Context) {
	w := &a.workbench
	width, _ := c.Size()
	available := width
	if !w.sidebarHidden {
		available -= a.sidebarWidth + 6
	}
	// Even an explicitly requested side inspector yields on small windows.
	// It returns, with its own divider position, when there is room again.
	w.sideBySide = (available >= 1160 || w.layout == "right" && available >= 1000) && w.layout != "bottom"
	if w.sidePercent == 0 {
		w.sidePercent = 54
	}
	content := func() {
		switch a.workspace {
		case 1:
			a.composeView(c, c.Theme())
		case 2:
			a.apiView(c, c.Theme())
		default:
			ui.Column(c).Fill().Children(func() {
				if w.focused {
					ui.Row(c).Height(32).Padding(0, 12).AlignItems(ui.Center).Children(func() {
						a.desktopButton(c, "focus", "Back to list")
						ui.Text(c, "Inspector focus · Ctrl/Cmd+Shift+Enter").FontSize(11).TextColor(c.Theme().TextMuted)
					})
					a.detail(c)
					return
				}
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
				if w.sideBySide {
					ui.SplitPercent(c.Key("right-inspector"), &w.sidePercent, table, func() { a.detail(c) }).Fill()
				} else {
					ui.SplitVerticalPercent(c.Key("bottom-inspector"), &a.tablePercent, table, func() { a.detail(c) }).Fill()
				}
			})
		}
	}
	if w.sidebarHidden || w.focused && a.workspace == 0 {
		ui.Column(c).Grow(1).FillWidth().Children(content)
	} else {
		ui.Split(c, &a.sidebarWidth, func() { a.sidebar(c) }, content).Grow(1)
	}
}

var captureKinds = []string{"All", "HTTP", "HTTPS", "WebSocket", "SSE", "JSON", "Images", "HTML"}

func (a *app) captureToolbar(c *ui.Context, t *ui.Theme) {
	ui.Row(c).Height(44).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		label := "Capture stopped"
		if a.srv != nil && a.srv.Running() {
			label = "Live · " + a.srv.Addr()
		}
		if ui.Button(c, label).FontSize(11).SingleLine().Width(156).Tooltip("Open Proxy settings · " + label).Clicked() {
			a.settingsTab, a.settingsOpen = 1, true
		}
		search := ui.TextInput(c, &a.query).Bind(&a.search).Label("Search host or path").Placeholder("Search host or path…").Tooltip("URL host and path only · Ctrl/Cmd+F · Enter or ↓ moves to requests. Filters search headers and full bodies.").Grow(1)
		search.OnShortcut(0, ui.KeyDown, func() { a.focusTraffic(c) })
		if search.Changed() {
			a.applyFilter()
		}
		if search.Submitted() {
			a.focusTraffic(c)
		}
		ui.MenuButton(c, captureKinds[a.chip], func(m *ui.Menu) {
			for i, kind := range captureKinds {
				if m.Item(kind).Checked(a.chip == i).Chosen() {
					a.chip = i
					a.applyFilter()
				}
			}
		}).Label("Protocol and content type").FontSize(11).Height(28)
		b := ui.Button(c, "Filters…").Height(28).FontSize(11)
		if a.filtersActive() {
			b.Background(t.Accent.Alpha(0.12)).TextColor(t.Accent)
		}
		if b.Clicked() {
			a.openFilter()
		}
		follow := "Following · Latest"
		if view := a.views.activeView(); view != nil && !view.list.AtEnd() && len(a.visible) > 0 {
			follow = "Paused · Latest"
		}
		a.desktopButton(c, "latest", follow).Label("Latest").Tooltip("Scroll to the end of the current sort order and resume following. Does not change the inspected request.")
		ui.Text(c, "Break").FontSize(11).TextColor(t.TextMuted)
		if ui.Switch(c, &a.breakOn).Label("Break requests").Changed() && a.srv != nil {
			a.srv.SetBreak(a.breakOn)
		}
		if ui.Button(c, "Clear").Height(28).FontSize(11).Clicked() {
			a.clearOpen = true
		}
	})
	ui.Divider(c)
	if a.query == "" && a.filter == "all" && a.chip == 0 && !a.filtersActive() {
		return
	}
	// Every condition is visible and removable without opening a dialog.
	ui.Row(c).Height(32).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.ScrollHorizontal(c).Grow(1).FillHeight().Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				token := func(label string, clear func()) {
					if ui.Button(c, label+" ×").FontSize(10).Height(24).TextColor(t.Accent).Tooltip("Remove condition: " + label).Clicked() {
						clear()
						a.views.capture(a)
						a.applyFilter()
					}
				}
				if a.query != "" {
					token("URL: "+a.query, func() { a.query = "" })
				}
				if a.filter != "all" {
					token(a.filter, func() { a.filter = "all" })
				}
				if a.chip != 0 {
					token(captureKinds[a.chip], func() { a.chip = 0 })
				}
				for _, field := range []struct {
					label string
					value *string
				}{
					{"Status", &a.filterStatus}, {"Header", &a.filterHeader}, {"Body", &a.views.activeView().body},
					{"Method", &a.filterTriage.Method}, {"Duration ms", &a.filterTriage.Duration},
					{"Response bytes", &a.filterTriage.Size}, {"Color", &a.filterTriage.Color}, {"Note", &a.filterTriage.Note},
				} {
					if *field.value != "" {
						token(field.label+": "+*field.value, func() { *field.value = "" })
					}
				}
			})
		})
		a.desktopButton(c, "clear-filters", "Reset all")
	})
	ui.Divider(c)
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
		copyRequest = func() {
			req, err := prepareRequest(d.value(), nil)
			if err != nil {
				a.setMessage("Copy cURL: " + err.Error())
				return
			}
			a.copyCurl(c, flow{method: req.Method, url: req.URL, reqHeader: req.header, reqBody: req.Body})
		}
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
	if c != nil && capturing && a.tab == 1 && a.bodyText[a.bodySide] != nil && a.bodyReader[a.bodySide].FocusWithin(c) {
		search = "Find in body"
		find = func() { a.bodyText[a.bodySide].Run(editor.Find) }
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
		{"save-request", "Save request", editing, ui.Cmd, ui.KeyS, func() { a.saveComposed() }},
		{"compare-source", "Compare source", editing && d.source != nil && d.response != nil && !d.composeWaiting, ui.Cmd | ui.Shift, ui.KeyD, func() { d.responseTab = 3 }},
		{"back", "Back to traffic", editing, ui.Cmd, ui.KeyBracketLeft, func() { a.backToTraffic(c) }},
		{"close-request", "Close request tab", editing && !d.composeWaiting, ui.Cmd, ui.KeyW, func() { a.requestClose(d) }},
		{"next-tab", "Next debugging tab", a.workspace != 2 && len(a.documents) > 0, ui.Ctrl, ui.KeyTab, func() { a.cycleDocument(1) }},
		{"previous-tab", "Previous debugging tab", a.workspace != 2 && len(a.documents) > 0, ui.Ctrl | ui.Shift, ui.KeyTab, func() { a.cycleDocument(-1) }},
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
			a.query, a.filter, a.chip = "", "all", 0
			a.views.capture(a)
			a.clearFilters()
		}},
		{"break", fmt.Sprintf("Break requests · %t", a.breakOn), a.srv != nil, 0, 0, func() {
			a.breakOn = !a.breakOn
			a.srv.SetBreak(a.breakOn)
		}},
		{"layout-auto", "Inspector: Adaptive", capturing, 0, 0, func() { w.layout, w.focused = "", false }},
		{"layout-right", "Inspector: Right (wide windows)", capturing, 0, 0, func() { w.layout, w.focused = "right", false }},
		{"layout-bottom", "Inspector: Bottom", capturing, 0, 0, func() { w.layout, w.focused = "bottom", false }},
		{"sidebar", "Toggle sidebar", true, ui.Cmd, ui.KeyB, func() { w.sidebarHidden = !w.sidebarHidden }},
		{"layout-reset", "Reset desktop layout", true, 0, 0, func() {
			w.layout, w.sidebarHidden, w.focused, w.sidePercent = "", false, false, 54
			a.sidebarWidth, a.tablePercent = 220, 44
		}},
		{"https", "HTTPS certificate & decryption setup…", true, 0, 0, func() { a.settingsTab, a.settingsOpen = 2, true }},
		{"diagnostics", "Capture diagnostics…", true, 0, 0, a.openDiagnostics},
		{"experiments", "Experiment workbench…", true, 0, 0, func() { a.experiments.open = true }},
		{"websocket", "WebSocket session…", true, 0, 0, a.openSocket},
		{"client", "Set up a local client…", a.srv != nil, 0, 0, a.openClientSetup},
		{"devices", "Connect a device…", a.srv != nil, 0, 0, a.openDevices},
		{"rules", "Manage debugging rules…", true, 0, 0, a.openRules},
		{"new-request", "New request", true, ui.Cmd, ui.KeyN, a.openCompose},
		{"import-curl", "Import cURL…", true, 0, 0, a.openCurlImport},
		{"settings", "Open settings…", true, ui.Cmd, ui.KeyComma, func() { a.settingsOpen = true }},
		{"capture-workspace", "Capture", true, ui.Cmd, ui.Key1, func() { a.workspace = 0; a.focusTraffic(c) }},
		{"requests-workspace", "Requests", true, ui.Cmd, ui.Key2, func() {
			if a.requestDocument == nil {
				a.openCompose()
			}
			a.workspace = 1
			a.urlInput.Focus(c)
		}},
		{"apis-workspace", "APIs", true, ui.Cmd, ui.Key3, func() { a.workspace = 2 }},
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
	a.workspace = 0
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
	ui.Modal(c, &w.paletteOpen, func() {
		width, _ := c.Size()
		ui.Column(c).Width(min(560, width-48)).Gap(10).Children(func() {
			ui.Text(c, "Commands").FontSize(15).Bold()
			// Modal focuses its first input after remembering the opener. An
			// explicit AutoFocus here would replace that remembered focus.
			input := ui.TextInput(c, &w.query).Label("Find command").Placeholder("Search actions…").FillWidth()
			if input.Changed() {
				w.command = 0
				w.commands.ScrollTo(0, ui.Start)
			}
			var matches []desktopAction
			for _, action := range a.desktopActions(c) {
				if action.id != "palette" && strings.Contains(strings.ToLower(action.label), strings.ToLower(strings.TrimSpace(w.query))) {
					matches = append(matches, action)
				}
			}
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
			list := ui.List(c, &w.commands, len(matches), func(i int) {
				action := matches[i]
				b := ui.ButtonBase(c).Label(action.label).Height(34).FillWidth().Padding(0, 10).Gap(8).Radius(5).AlignItems(ui.Center).Disabled(!action.enabled).Tooltip(action.hint())
				if i == w.command {
					b.Background(c.Theme().Accent.Alpha(0.12))
				}
				b.Children(func() {
					color := c.Theme().Text
					if i == w.command {
						color = c.Theme().AccentText
					} else if !action.enabled {
						color = c.Theme().TextMuted
					}
					label := action.label
					if !action.enabled {
						label += " (unavailable)"
					}
					ui.Text(c, label).FontSize(12).TextColor(color)
					ui.Spacer(c)
					ui.Text(c, action.shortcut()).FontSize(10).TextColor(color)
				})
				if b.Clicked() {
					w.command = i
					run()
				}
			}).Height(300).FillWidth()
			if list.Submitted() {
				run()
			}
			if len(matches) == 0 {
				ui.Text(c, "No matching commands").FontSize(12).TextColor(c.Theme().TextMuted)
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
				ui.Text(c, message).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine().Tooltip(message)
			}
			ui.Text(c, "↑ ↓ Choose · Enter Run · Esc Close · Unavailable actions are dimmed").FontSize(11).TextColor(c.Theme().TextMuted)
		})
	})
}
