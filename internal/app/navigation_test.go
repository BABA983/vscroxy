package app

import (
	"runtime"
	"slices"
	"strings"
	"testing"
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
		a.pickedID, a.page = 2, pageLive
		s.observe(a)
		a.pickedID = 3
		s.observe(a)
		a.flows = slices.DeleteFunc(a.flows, func(f flow) bool { return f.id == 2 })
		a.applyFilter()

		if !s.canGo(a, -1) || !s.step(a, -1) {
			t.Fatal("back did not skip the deleted flow")
		}
		if a.page != pageRequest || a.requestDocument.savedKey != "saved:one" {
			t.Fatalf("back stopped at %v / %+v", a.page, a.requestDocument)
		}
		// The deleted stop is pruned; another back goes to the first capture.
		if !s.step(a, -1) || a.page != pageLive || a.pickedID != 1 {
			t.Fatalf("second back stopped at %v / %d", a.page, a.pickedID)
		}
		if s.canGo(a, -1) {
			t.Fatal("back went past the start of the stack")
		}
	})
	t.Run("closed tab", func(t *testing.T) {
		a := navigationTestApp(t)
		s := &a.workbench.navigation
		a.pickedID, a.page = 1, pageLive
		s.observe(a)
		navigationOpenRequest(a, "saved:closed")
		s.observe(a)
		a.pickedID, a.page = 2, pageLive
		s.observe(a)
		a.documents = nil
		a.requestDocument = nil

		if !s.step(a, -1) || a.page != pageLive || a.pickedID != 1 {
			t.Fatalf("back did not skip the closed tab: %v / %d", a.page, a.pickedID)
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
	a.pickedID = 2
	s.observe(a)
	a.pickedID = 3
	s.observe(a)

	if !s.step(a, -1) || !s.canGo(a, 1) {
		t.Fatal("back lost the forward stack")
	}
	if !s.step(a, -1) || !s.step(a, 1) || a.pickedID != 2 {
		t.Fatalf("forward did not return to flow 2: %d", a.pickedID)
	}
	a.pickedID = 7
	s.observe(a)
	if s.canGo(a, 1) || !s.canGo(a, -1) {
		t.Fatalf("new jump kept a forward stop: %+v", s.entries)
	}
	if !s.step(a, -1) || a.pickedID != 2 {
		t.Fatalf("back after the new jump went to %d", a.pickedID)
	}
}

// TestNavigationMergesConsecutiveNearDuplicates mirrors shouldReplaceStackEntry:
// revisiting the same place replaces its stop, while a different flow in the
// same place is a genuine new stop.
func TestNavigationMergesConsecutiveNearDuplicates(t *testing.T) {
	a := navigationTestApp(t)
	s := &a.workbench.navigation
	a.pickedID, a.page = 1, pageLive
	s.observe(a)
	s.observe(a)
	if len(s.entries) != 1 {
		t.Fatalf("the same capture was pushed %d times", len(s.entries))
	}
	a.pickedID = 2
	s.observe(a)
	if len(s.entries) != 2 {
		t.Fatalf("a new flow made %d entries", len(s.entries))
	}
	a.pickedID = 1
	s.observe(a)
	a.pickedID = 3
	s.observe(a)
	a.pickedID = 1
	s.observe(a)
	s.observe(a)
	if len(s.entries) != 5 || s.index != 4 {
		t.Fatalf("revisits were not merged: %d entries, index %d", len(s.entries), s.index)
	}
	// A new jump after stepping back still discards the forward half.
	if !s.step(a, -1) || !s.step(a, -1) {
		t.Fatal("back through merged stops failed")
	}
	a.pickedID = 4
	s.observe(a)
	if len(s.entries) != 4 || s.canGo(a, 1) {
		t.Fatalf("jump after back kept %d entries: %+v", len(s.entries), s.entries)
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
// palette keycap: arrow and minus keys are outside shortcut's usual table.
func TestNavigationKeysAreSpelled(t *testing.T) {
	for id, s := range map[string]shortcut{
		"navigation.back": navigationBackKey, "navigation.forward": navigationForwardKey,
	} {
		accel := navigationAccelerator(s)
		cap := paletteKeycap(s)
		if accel == "" || cap == "" {
			t.Fatalf("%s has no accelerator or keycap: %q / %q", id, accel, cap)
		}
		if strings.Contains(accel, "Cmd") && !strings.Contains(accel, "CmdOrCtrl") && runtime.GOOS != "darwin" {
			t.Fatalf("%s accelerator = %q, want the platform's Ctrl key", id, accel)
		}
		if navigationCommandLabel(id) == "" {
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
	if back.enabled || forward.enabled {
		t.Fatal("navigation was enabled with no history")
	}
	a.pickedID, a.page = 1, pageLive
	a.workbench.navigation.observe(a)
	a.pickedID = 2
	a.workbench.navigation.observe(a)
	a.workbench.navigation.step(a, -1)
	a.builds++
	if again := a.actions(c); !pick(again, "navigation.forward").enabled {
		t.Fatal("Go Forward stayed disabled with a forward stop")
	}
}
