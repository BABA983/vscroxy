package app

import (
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/locales"
)

func navigationTestApp(t *testing.T) *app {
	t.Helper()
	a := workspaceApp()
	for id := 3; id <= 60; id++ {
		a.flows = append(a.flows, flow{id: id, method: "GET", host: "api.example.com", path: "/f", url: "https://api.example.com/f"})
	}
	a.applyFilter()
	return a
}

func navigationOpenRequest(a *app, key string) {
	d := &requestDocument{savedKey: key}
	a.documents = []*requestDocument{d}
	a.requestDocument, a.page = d, pageRequest
}

// TestNavigationBackSkipsDeletedFlowAndClosedTab follows VS Code's removal of
// disposed editors: a stop naming a flow that was deleted or a tab that was
// closed is never shown again.
func TestNavigationBackSkipsDeletedFlowAndClosedTab(t *testing.T) {
	t.Run("deleted flow", func(t *testing.T) {
		a := navigationTestApp(t)
		s := &a.workbench.navigation
		a.pickedID, a.page = 1, pageLive
		s.observe(a)
		navigationOpenRequest(a, "saved:one")
		s.observe(a)
		a.page = pageSession
		s.observe(a)
		a.flows = slices.DeleteFunc(a.flows, func(f flow) bool { return f.id == 1 })
		a.applyFilter()

		if !s.step(a, -1) || a.page != pageRequest || a.requestDocument.savedKey != "saved:one" {
			t.Fatalf("back did not reach the request: page=%v doc=%+v", a.page, a.requestDocument)
		}
		if s.canGo(a, -1) || s.step(a, -1) {
			t.Fatal("back walked into the deleted flow")
		}
	})
	t.Run("closed tab", func(t *testing.T) {
		a := navigationTestApp(t)
		s := &a.workbench.navigation
		a.pickedID, a.page = 1, pageLive
		s.observe(a)
		navigationOpenRequest(a, "saved:closed")
		s.observe(a)
		navigationOpenRequest(a, "saved:other")
		s.observe(a)
		a.documents = []*requestDocument{a.requestDocument}

		if !s.step(a, -1) || a.page != pageLive || a.pickedID != 1 {
			t.Fatalf("back did not skip the closed tab: page=%v flow=%d", a.page, a.pickedID)
		}
	})
}

// TestNavigationForwardClearedByNewJump is VS Code's addOrReplace truncation:
// stepping back keeps the forward half until a new place is recorded.
func TestNavigationForwardClearedByNewJump(t *testing.T) {
	a := navigationTestApp(t)
	s := &a.workbench.navigation
	a.pickedID, a.page = 1, pageLive
	s.observe(a)
	navigationOpenRequest(a, "saved:one")
	s.observe(a)
	a.page = pageSession
	s.observe(a)

	if !s.step(a, -1) || !s.canGo(a, 1) {
		t.Fatal("back lost the forward stack")
	}
	if !s.step(a, -1) || a.page != pageLive || a.pickedID != 1 {
		t.Fatalf("back did not reach the capture: page=%v flow=%d", a.page, a.pickedID)
	}
	if !s.step(a, 1) || a.page != pageRequest {
		t.Fatal("forward did not return to the request tab")
	}
	a.page = pageRuns
	s.observe(a)
	if s.canGo(a, 1) || !s.canGo(a, -1) {
		t.Fatalf("new jump kept a forward stop: %+v", s.entries)
	}
	if !s.step(a, -1) || a.page != pageRequest {
		t.Fatalf("back after the new jump went to %v", a.page)
	}
}

// TestNavigationSelectionMovesReplaceStop is the list-selection contract from
// the review: arrowing through ten captures replaces one LIVE stop instead of
// filling the history. Back restores the jump to a request tab, not each row.
func TestNavigationSelectionMovesReplaceStop(t *testing.T) {
	a := navigationTestApp(t)
	s := &a.workbench.navigation
	for id := 1; id <= 10; id++ {
		a.pickedID, a.page = id, pageLive
		s.observe(a)
	}
	if len(s.entries) != 1 || s.entries[0].flowID != 10 {
		t.Fatalf("ten selections made %d stops: %+v", len(s.entries), s.entries)
	}
	navigationOpenRequest(a, "saved:one")
	s.observe(a)
	if len(s.entries) != 2 {
		t.Fatalf("opening a request made %d stops", len(s.entries))
	}

	if !s.step(a, -1) || a.page != pageLive || a.pickedID != 10 {
		t.Fatalf("back did not restore the last selected capture: %v / %d", a.page, a.pickedID)
	}
	if s.canGo(a, -1) || s.step(a, -1) || a.pickedID != 10 {
		t.Fatal("back walked through the ten list selections")
	}
	// An unspecific LIVE stop must not erase the selected flow either.
	a.pickedID = 0
	s.observe(a)
	if s.entries[s.index].flowID != 10 {
		t.Fatalf("an unspecific stop overwrote the capture: %+v", s.entries[s.index])
	}
}

// TestNavigationPushesOnlyOnPlaceChange covers the other coarse coordinates:
// a different request tab or API contract is a new stop, a move inside one is
// not.
func TestNavigationPushesOnlyOnPlaceChange(t *testing.T) {
	a := navigationTestApp(t)
	s := &a.workbench.navigation
	a.apis = []*apiDocument{{spec: apiSpec{ID: "one"}}, {spec: apiSpec{ID: "two"}}}
	a.apiChoice, a.page = "one", pageAPI
	s.observe(a)
	a.activeAPI().tab = 1 // a move inside the contract
	s.observe(a)
	if len(s.entries) != 1 {
		t.Fatalf("a pane move inside one contract made %d stops", len(s.entries))
	}
	a.apiChoice = "two"
	s.observe(a)
	a.page = pageRuns
	s.observe(a)
	if len(s.entries) != 3 || s.index != 2 {
		t.Fatalf("page and contract changes made %d stops", len(s.entries))
	}
}

func TestNavigationStackIsBounded(t *testing.T) {
	a := navigationTestApp(t)
	s := &a.workbench.navigation
	for range navigationStackLimit + 12 {
		a.page = pageRuns
		if a.pickedID%2 == 0 {
			a.page = pageSession
		}
		a.pickedID++
		s.observe(a)
	}
	if len(s.entries) != navigationStackLimit || s.index != navigationStackLimit-1 {
		t.Fatalf("stack = %d entries, index %d", len(s.entries), s.index)
	}
	for range navigationStackLimit - 1 {
		if !s.step(a, -1) {
			t.Fatal("back stopped before the oldest kept stop")
		}
	}
	if s.canGo(a, -1) || !s.canGo(a, 1) {
		t.Fatal("the stack lost its forward half at the oldest kept stop")
	}
}

// TestNavigationKeysAreSpelled checks the native menu accelerator and the
// palette keycap: arrow and minus keys need shortcut's own spelling.
func TestNavigationKeysAreSpelled(t *testing.T) {
	for id, item := range map[string]struct {
		s     shortcut
		label string
	}{
		"navigation.back":    {navigationBackKey, locales.GoBack()},
		"navigation.forward": {navigationForwardKey, locales.GoForward()},
	} {
		accel, cap := item.s.accelerator(), item.s.keycap()
		if accel == "" || cap == "" {
			t.Fatalf("%s has no accelerator or keycap: %q / %q", id, accel, cap)
		}
		if strings.Contains(cap, "CmdOrCtrl") {
			t.Fatalf("%s keycap = %q, want a platform key", id, cap)
		}
		if runtime.GOOS != "darwin" && !strings.Contains(accel, "CmdOrCtrl") {
			t.Fatalf("%s accelerator = %q, want the platform's Ctrl key", id, accel)
		}
		if item.label == "" {
			t.Fatalf("%s has no menu label", id)
		}
	}
}

// TestNavigationCommandsFollowTheStack checks the ids the palette and native
// menu share with the registry, and that their keys do not collide.
func TestNavigationCommandsFollowTheStack(t *testing.T) {
	a, c := commandTestApp(t, commandTestStates()[0])
	back, forward := pick(a.actions(c), "navigation.back"), pick(a.actions(c), "navigation.forward")
	if back.id == "" || forward.id == "" || back.shortcut == forward.shortcut {
		t.Fatalf("navigation commands or keys missing: %+v / %+v", back, forward)
	}
	if back.label != locales.GoBack() || forward.label != locales.GoForward() {
		t.Fatalf("navigation labels = %q / %q", back.label, forward.label)
	}
	if back.enabled || forward.enabled {
		t.Fatal("navigation was enabled with no history")
	}
	a.pickedID, a.page = 1, pageLive
	a.workbench.navigation.observe(a)
	navigationOpenRequest(a, "saved:one")
	a.workbench.navigation.observe(a)
	a.workbench.navigation.step(a, -1)
	a.builds++
	if again := a.actions(c); !pick(again, "navigation.forward").enabled {
		t.Fatal("Go Forward stayed disabled with a forward stop")
	}
}
