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

// navigationLocation is one stop: the page and the finer choice inside it.
// Flow ids survive row rebuilds, request keys survive edits, and the API
// choice names a contract. Unsaved request tabs carry a document id instead
// of a key, since several of them may have the empty key.
type navigationLocation struct {
	page       page
	flowID     int
	requestKey string
	requestID  int
	apiChoice  string
}

func currentNavigationLocation(a *app) navigationLocation {
	loc := navigationLocation{page: a.page}
	switch {
	case a.page.capture():
		loc.flowID = a.pickedID
	case a.page == pageRequest:
		if d := a.requestDocument; d != nil {
			loc.requestKey = d.savedKey
			if d.savedKey == "" {
				loc.requestID = d.id
			}
		}
	case a.page == pageAPI:
		loc.apiChoice = a.apiChoice
	}
	return loc
}

func (loc navigationLocation) openDocument(a *app) *requestDocument {
	for _, d := range a.documents {
		switch {
		case loc.requestKey != "" && d.savedKey == loc.requestKey,
			loc.requestKey == "" && d.id == loc.requestID:
			return d
		}
	}
	return nil
}

// exists is the skip rule: a flow that was deleted and a request tab that was
// closed have no location to return to. VS Code drops disposed editors from
// its stack; here validity is also checked at navigation time, in case the
// app changed between two frames.
func (loc navigationLocation) exists(a *app) bool {
	switch {
	case loc.page.capture() && loc.flowID != 0:
		return slices.ContainsFunc(a.flows, func(f flow) bool { return f.id == loc.flowID })
	case loc.page == pageRequest:
		return loc.openDocument(a) != nil
	case loc.page == pageAPI && loc.apiChoice != "":
		return a.activeAPI() != nil
	default:
		return true
	}
}

func (loc navigationLocation) navigate(a *app) {
	switch {
	case loc.page.capture() && loc.flowID != 0 && loc.exists(a):
		a.inspectCapture(loc.flowID)
	case loc.page == pageRequest:
		if d := loc.openDocument(a); d != nil {
			a.requestDocument, a.requestChoice, a.page = d, d.savedKey, pageRequest
		}
	case loc.page == pageAPI && loc.apiChoice != "":
		a.apiChoice, a.page = loc.apiChoice, pageAPI
	default:
		a.page = loc.page
	}
}

// samePlace compares the coarse part of two stops, as VS Code compares the
// editor input: the same page, request tab and contract with a different
// inspected flow is still one place at different positions.
func samePlace(x, y navigationLocation) bool {
	return x.page == y.page && x.requestKey == y.requestKey &&
		x.requestID == y.requestID && x.apiChoice == y.apiChoice
}

// shouldMerge is VS Code's shouldReplaceStackEntry: without a previous
// position a new stop replaces the old one; an unspecific new stop keeps the
// specific one already recorded; equal positions merge.
func shouldMerge(previous, next navigationLocation) bool {
	if !samePlace(previous, next) {
		return false
	}
	if previous.flowID == 0 {
		return true
	}
	if next.flowID == 0 {
		return false
	}
	return previous.flowID == next.flowID
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
		if len(kept) > 0 && shouldMerge(kept[len(kept)-1], loc) {
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
	if s.index >= 0 && s.index < len(s.entries) && shouldMerge(s.entries[s.index], loc) {
		s.entries[s.index] = loc
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

// navigationCommandLabel keeps the palette and the native menu on the same
// VS Code wording. The app's message catalog has no navigation entries yet;
// the catalog is owned outside this change.
func navigationCommandLabel(id string) string {
	switch id {
	case "navigation.back":
		return "Go Back"
	case "navigation.forward":
		return "Go Forward"
	default:
		return ""
	}
}

// navigationAccelerator spells arrow and minus bindings for the native menu,
// whose accelerator grammar accepts their names but shortcut.accelerator's
// keycap-oriented switch does not.
func navigationAccelerator(s shortcut) string {
	if accel := s.accelerator(); accel != "" {
		return accel
	}
	name := ""
	switch s.key {
	case ui.KeyLeft:
		name = "Left"
	case ui.KeyRight:
		name = "Right"
	case ui.KeyMinus:
		name = "-"
	default:
		return ""
	}
	// ui.Cmd is ui.Ctrl on non-macOS, so the first branch also spells the
	// platform's primary modifier as the other menu accelerators do.
	mods := ""
	switch {
	case s.mods&ui.Cmd != 0 && runtime.GOOS != "darwin":
		mods += "CmdOrCtrl+"
	case s.mods&ui.Cmd != 0:
		mods += "Cmd+"
	case s.mods&ui.Ctrl != 0:
		mods += "Ctrl+"
	}
	if s.mods&ui.Alt != 0 {
		mods += "Alt+"
	}
	if s.mods&ui.Shift != 0 {
		mods += "Shift+"
	}
	return mods + name
}

// navigationKeycap is the same spelling in the palette, using platform glyphs
// on macOS as the other keycaps do.
func navigationKeycap(s shortcut) string {
	if s.key != ui.KeyLeft && s.key != ui.KeyRight && s.key != ui.KeyMinus {
		return s.keycap()
	}
	text := navigationAccelerator(s)
	if runtime.GOOS != "darwin" {
		return strings.NewReplacer(
			"CmdOrCtrl", "Ctrl", "Left", "←", "Right", "→",
		).Replace(text)
	}
	return strings.NewReplacer(
		"Ctrl+", "⌃", "Alt+", "⌥", "Shift+", "⇧", "Cmd+", "⌘",
	).Replace(text)
}
