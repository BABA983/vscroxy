package app

import (
	"cmp"
	"runtime"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// An action is one command. The menu bar, context and ⋯ menus, the command
// palette, shortcuts and buttons are all built from actions, so a command has
// one label, one shortcut and one rule for when it can run, wherever it shows.
//
// Actions are built with the state they act on and read once, in the same
// build pass: enabled is decided when the action is made.
type action struct {
	// id is stable, "area.verb", and is how the menu bar, and later the
	// command line and MCP, ask for the command. Commands that differ only by
	// their target, such as one column of many, share an id.
	id       string
	label    string
	group    string // menus draw a line between groups
	shortcut shortcut
	enabled  bool
	reason   string // why it cannot run, when not enabled
	check    check
	items    []action // a submenu's actions
	run      func()
}

// check is how a menu marks an action that shows a setting.
type check uint8

const (
	noCheck check = iota
	checkOff
	checkOn
	radioOff
	radioOn
)

func checkbox(on bool) check {
	if on {
		return checkOn
	}
	return checkOff
}

func radio(on bool) check {
	if on {
		return radioOn
	}
	return radioOff
}

// do makes an action that can run.
func do(id, label string, run func()) action {
	return action{id: id, label: label, enabled: true, run: run}
}

// note is a menu line that explains rather than runs.
func note(label string) action {
	return action{label: label}
}

// submenu groups actions under label.
func submenu(label string, items ...action) action {
	return action{label: label, enabled: true, items: items}
}

// when makes the action unavailable unless ok, saying why for the palette
// and tooltips.
func (x action) when(ok bool, reason string) action {
	x.enabled = x.enabled && ok
	if !ok && x.reason == "" {
		x.reason = reason
	}
	return x
}

func (x action) keys(mods ui.Modifiers, key ui.Key) action {
	x.shortcut = shortcut{mods, key}
	return x
}

func (x action) in(group string) action {
	x.group = group
	return x
}

func (x action) checked(c check) action {
	x.check = c
	return x
}

// as relabels an action for one menu, where its surroundings say the rest:
// "cURL" under Copy.
func (x action) as(label string) action {
	x.label = label
	return x
}

func (x action) why() string {
	return cmp.Or(x.reason, "Unavailable for what is shown or selected")
}

func (x action) hint() string {
	text := x.label
	if key := x.shortcut.String(); key != "" {
		text += " · " + key
	}
	if !x.enabled {
		text += " · " + x.why()
	}
	return text
}

// pick finds the action with id, or none.
func pick(actions []action, id string) action {
	for _, x := range actions {
		if x.id == id {
			return x
		}
	}
	return action{}
}

// pickActions finds the global actions with ids, in that order.
func (a *app) pickActions(c *ui.Context, ids ...string) []action {
	all := a.actions(c)
	picked := make([]action, 0, len(ids))
	for _, id := range ids {
		if x := pick(all, id); x.id != "" {
			picked = append(picked, x)
		}
	}
	return picked
}

// actionMenu fills a menu with actions, a line between groups. Unavailable
// actions stay, disabled, so a menu keeps its shape.
func actionMenu(m *ui.Menu, actions []action) {
	for i, x := range actions {
		if x.label == "" {
			continue
		}
		if i > 0 && x.group != actions[i-1].group {
			m.Separator()
		}
		if x.items != nil {
			m.Submenu(x.label, func(m *ui.Menu) { actionMenu(m, x.items) })
			continue
		}
		item := m.Item(x.label).Shortcut(x.shortcut.mods, x.shortcut.key).Disabled(!x.enabled)
		switch x.check {
		case checkOff, checkOn:
			item.Checked(x.check == checkOn)
		case radioOff, radioOn:
			item.Radio(x.check == radioOn)
		}
		if item.Chosen() && x.run != nil {
			x.run()
		}
	}
}

// actionButton is a button for the global action id, labelled for its place.
func (a *app) actionButton(c *ui.Context, id, label string) ui.Element {
	x := pick(a.actions(c), id)
	if x.id == "" {
		return ui.Element{}
	}
	b := uikit.SecondaryButton(c, label).Disabled(!x.enabled).Tooltip(x.hint())
	if b.Clicked() {
		x.run()
	}
	return b
}

func (a *app) actionShortcuts(c *ui.Context) {
	for _, x := range a.actions(c) {
		// Browsing traffic never sends: in LIVE, Send's shortcut works from
		// inside the inspector only.
		if x.id == "request.send" && a.page.capture() && !a.workbench.inspector.FocusWithin(c) {
			continue
		}
		if x.enabled && x.shortcut.key != 0 {
			c.OnShortcut(x.shortcut.mods, x.shortcut.key, x.run)
		}
	}
}

// Native menu callbacks enter the next build, rather than retaining a Context
// or invoking commands behind a modal. Modal focus is outside content.
func (a *app) nativeMenuActions(c *ui.Context) {
	w := &a.workbench
	pending := w.menuAction
	w.menuAction = ""
	_, toolsSave := a.profileToSave()
	for _, x := range a.actions(c) {
		available := x.enabled && w.content.FocusWithin(c)
		if a.toolsWin != nil && a.toolsFocused {
			// Tools in front has one menu command of its own: Save.
			available = x.id == "request.save" && toolsSave
		}
		if item := w.menuItems[x.id]; item != nil && item.IsEnabled() != available {
			item.SetEnabled(available)
		}
		if pending == x.id && available {
			x.run()
			c.Invalidate()
		}
	}
}

type shortcut struct {
	mods ui.Modifiers
	key  ui.Key
}

// accelerator is the spelling of native accelerators; String, of visible
// shortcut hints.
func (s shortcut) accelerator() string {
	if s.key == 0 {
		return ""
	}
	text := ""
	if s.mods&ui.Cmd != 0 {
		text += "CmdOrCtrl+"
	} else if s.mods&ui.Ctrl != 0 {
		text += "Ctrl+"
	}
	if s.mods&ui.Alt != 0 {
		text += "Alt+"
	}
	if s.mods&ui.Shift != 0 {
		text += "Shift+"
	}
	switch {
	case s.key >= ui.KeyA && s.key <= ui.KeyZ:
		return text + string('A'+rune(s.key-ui.KeyA))
	case s.key >= ui.Key0 && s.key <= ui.Key9:
		return text + string('0'+rune(s.key-ui.Key0))
	case s.key == ui.KeyEnter:
		return text + "Enter"
	case s.key == ui.KeyTab:
		return text + "Tab"
	case s.key == ui.KeyComma:
		return text + ","
	case s.key == ui.KeyBracketLeft:
		return text + "["
	}
	return ""
}

func (s shortcut) String() string {
	modifier := "Ctrl"
	if runtime.GOOS == "darwin" {
		modifier = "Cmd"
	}
	return strings.ReplaceAll(s.accelerator(), "CmdOrCtrl", modifier)
}
