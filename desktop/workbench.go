package main

import (
	"fmt"
	"strings"

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
		if ui.TextInput(c, &a.query).Bind(&a.search).Label("Search host or path").Placeholder("Search host or path…").Tooltip("URL host and path only · Ctrl/Cmd+F. Filters search headers and full bodies.").Grow(1).Changed() {
			a.applyFilter()
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

func (a *app) desktopActions(c *ui.Context) []desktopAction {
	w := &a.workbench
	f, current := a.current()
	capturing := a.workspace == 0
	selected := a.selectedFlows()
	if len(selected) == 0 && current {
		selected = []flow{f}
	}
	replayable := capturing && len(selected) > 0
	for _, flow := range selected {
		replayable = replayable && flow.kind == "" && flow.url != ""
	}
	focus := "Focus inspector"
	if w.focused {
		focus = "Back to list"
	}
	return []desktopAction{
		{"search", "Search host or path", capturing, ui.Cmd, ui.KeyF, func() { a.search.Focus(c) }},
		{"edit", "Edit & replay", capturing && current && f.kind == "", 0, 0, func() { a.composeFrom(f) }},
		{"replay", "Replay selected…", replayable, ui.Cmd, ui.KeyR, func() { a.openReplay(selected) }},
		{"copy", "Copy as cURL", capturing && current, ui.Cmd | ui.Alt, ui.KeyC, func() { a.copyCurl(c, f) }},
		{"export-selection", "Export selected HAR…", capturing && a.chosen.Len() > 0, 0, 0, a.exportChosen},
		{"focus", focus, capturing, ui.Cmd | ui.Shift, ui.KeyEnter, func() { w.focused = !w.focused }},
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
		{"client", "Set up a local client…", a.srv != nil, 0, 0, a.openClientSetup},
		{"devices", "Connect a device…", a.srv != nil, 0, 0, a.openDevices},
		{"rules", "Manage debugging rules…", true, 0, 0, a.openRules},
		{"new-request", "New request", true, 0, 0, a.openCompose},
		{"settings", "Open settings…", true, 0, 0, func() { a.settingsOpen = true }},
	}
}

func (a *app) desktopShortcuts(c *ui.Context) {
	c.OnShortcut(ui.Cmd, ui.KeyK, a.workbench.openPalette)
	for _, action := range a.desktopActions(c) {
		if action.enabled && action.key != 0 {
			c.OnShortcut(action.mods, action.key, action.run)
		}
	}
}

func (a *app) desktopButton(c *ui.Context, id, label string) ui.Element {
	for _, action := range a.desktopActions(c) {
		if action.id == id {
			b := ui.Button(c, label).Height(28).FontSize(11).Disabled(!action.enabled)
			if b.Clicked() {
				action.run()
			}
			return b
		}
	}
	return ui.Element{}
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
			input := ui.TextInput(c, &w.query).Label("Find command").Placeholder("Search actions…").FillWidth().AutoFocus()
			if input.Changed() {
				w.command = 0
				w.commands.ScrollTo(0, ui.Start)
			}
			var matches []desktopAction
			for _, action := range a.desktopActions(c) {
				if strings.Contains(strings.ToLower(action.label), strings.ToLower(strings.TrimSpace(w.query))) {
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
				b := ui.ButtonBase(c).Label(action.label).Height(34).FillWidth().Padding(0, 10).Radius(5).AlignItems(ui.Center).Disabled(!action.enabled)
				if i == w.command {
					b.Background(c.Theme().Accent.Alpha(0.12))
				}
				b.Children(func() {
					text := ui.Text(c, action.label).FontSize(12)
					if !action.enabled {
						text.TextColor(c.Theme().TextMuted)
					}
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
			}
			ui.Text(c, "↑ ↓ Choose · Enter Run · Esc Close · Unavailable actions are dimmed").FontSize(11).TextColor(c.Theme().TextMuted)
		})
	})
}
