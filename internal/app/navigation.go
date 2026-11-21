package app

import (
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

// navigationStack is VS Code's EditorNavigationStack: a bounded list of
// places the reader has been and an index into it. A jump into a new place
// drops the forward half, a jump back does not record itself, and revisiting
// the same place moves the current entry instead of duplicating it.
const navigationStackLimit = 50

// navigationLocation is one stop: the tab it belongs to and, for LIVE, the
// capture being inspected. The tab ID already names a request document, an
// API contract, a session or a singleton page, so it is the whole identity of
// the stop; the flow id is a position inside LIVE.
type navigationLocation struct {
	tab    tabID
	flowID int
}

func currentNavigationLocation(a *app) navigationLocation {
	loc := navigationLocation{tab: a.tabsGroup().active()}
	if loc.tab.kind == pageLive {
		loc.flowID = a.pickedID
	}
	return loc
}

// exists is the skip rule: a flow that was deleted, a tab that was closed and
// an API contract that was removed have no location to return to. VS Code
// drops disposed editors from its stack; here validity is also checked at
// navigation time, in case the app changed between two frames.
func (loc navigationLocation) exists(a *app) bool {
	if a.tabsGroup().indexOf(loc.tab) < 0 {
		return false
	}
	if loc.tab.kind == pageLive && loc.flowID != 0 {
		return slices.ContainsFunc(a.flows, func(f flow) bool { return f.id == loc.flowID })
	}
	if loc.tab.kind == pageAPI {
		id := strings.TrimPrefix(loc.tab.key, "api:")
		return slices.ContainsFunc(a.apis, func(d *apiDocument) bool { return d.spec.ID == id })
	}
	return true
}

func (loc navigationLocation) navigate(a *app) {
	if !loc.exists(a) {
		return
	}
	a.activateTab(loc.tab)
	// Activating LIVE restores the tab, then the row inside it. A request,
	// API or session tab carries its document in its key.
	if loc.tab.kind == pageLive && loc.flowID != 0 {
		a.inspectCapture(loc.flowID)
	}
}

// samePlace compares two stops. Moving the traffic list's selection stays
// inside one stop, as it does in VS Code: a tab is what gets pushed, while a
// flow id is a position inside the LIVE tab.
func samePlace(x, y navigationLocation) bool {
	return x.tab == y.tab
}

// preferSpecific keeps a flow choice when merging stops: a stop with no flow
// selected must not overwrite one that names the capture being inspected.
func preferSpecific(previous, next navigationLocation) navigationLocation {
	if next.flowID != 0 {
		return next
	}
	return previous
}

type navigationStack struct {
	entries    []navigationLocation
	index      int
	navigating bool
}

// prune drops stops that have gone away and flattens duplicates left behind,
// as VS Code does when an editor is disposed. The index keeps pointing at the
// same stop when it survives.
func (s *navigationStack) prune(a *app) {
	old := s.index
	kept := make([]navigationLocation, 0, len(s.entries))
	index := -1
	for i, loc := range s.entries {
		if !loc.exists(a) {
			continue
		}
		last := len(kept) - 1
		if last >= 0 && samePlace(kept[last], loc) {
			kept[last] = preferSpecific(kept[last], loc)
			if i <= old {
				index = last
			}
			continue
		}
		if i <= old {
			index = len(kept)
		}
		kept = append(kept, loc)
	}
	s.entries, s.index = kept, index
}

func (s *navigationStack) canGo(a *app, step int) bool {
	if s.navigating {
		return false
	}
	start := s.index
	if start < 0 {
		start = 0
		if step > 0 {
			return false
		}
	}
	for i := start + step; i >= 0 && i < len(s.entries); i += step {
		if s.entries[i].exists(a) {
			return true
		}
	}
	return false
}

// observe records the location a frame committed. It is called once per view
// pass, after the app reconciled its documents, so no other screen has to
// know the history exists.
func (s *navigationStack) observe(a *app) {
	if s.navigating {
		return
	}
	s.prune(a)
	loc := currentNavigationLocation(a)
	if s.index >= 0 && s.index < len(s.entries) && samePlace(s.entries[s.index], loc) {
		s.entries[s.index] = preferSpecific(s.entries[s.index], loc)
		return
	}
	if s.index+1 < len(s.entries) {
		s.entries = slices.Delete(s.entries, s.index+1, len(s.entries))
	}
	s.entries = append(s.entries, loc)
	if len(s.entries) > navigationStackLimit {
		s.entries = s.entries[len(s.entries)-navigationStackLimit:]
	}
	s.index = len(s.entries) - 1
}

// go moves one stop in VS Code's direction: -1 back, +1 forward. The stack
// records no jump of its own; the next frame's observe sees the same place
// and replaces the entry instead of clearing the forward half.
func (s *navigationStack) step(a *app, step int) bool {
	if s.navigating || !s.canGo(a, step) {
		return false
	}
	for next := s.index + step; next >= 0 && next < len(s.entries); next += step {
		if !s.entries[next].exists(a) {
			continue
		}
		s.index = next
		s.navigating = true
		s.entries[next].navigate(a)
		s.navigating = false
		return true
	}
	return false
}

// navigationKeys are VS Code's Go Back / Go Forward bindings: Alt+← / → on
// Windows, Ctrl+- / Ctrl+Shift+- on macOS, and Ctrl+Alt+- / Ctrl+Shift+- on
// Linux. The registry takes one binding per command; these are chosen once.
func navigationKeys() (shortcut, shortcut) {
	switch runtime.GOOS {
	case "darwin":
		return shortcut{ui.Ctrl, ui.KeyMinus}, shortcut{ui.Ctrl | ui.Shift, ui.KeyMinus}
	case "linux":
		return shortcut{ui.Ctrl | ui.Alt, ui.KeyMinus}, shortcut{ui.Ctrl | ui.Shift, ui.KeyMinus}
	default:
		return shortcut{ui.Alt, ui.KeyLeft}, shortcut{ui.Alt, ui.KeyRight}
	}
}

var navigationBackKey, navigationForwardKey = navigationKeys()
