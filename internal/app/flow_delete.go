package app

import (
	"runtime"
	"slices"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// flowDeleteKey is the platform's delete gesture: ⌘⌫ on macOS, Delete on the
// other platforms.
func flowDeleteKey() ui.Key {
	if runtime.GOOS == "darwin" {
		return ui.KeyBackspace
	}
	return ui.KeyDelete
}

// queueDeleteFlows defers a row deletion until the next view pass, after the
// table that offered the action has finished building.
func (a *app) queueDeleteFlows(flows []flow) {
	if len(flows) == 0 {
		return
	}
	a.pendingFlowDelete = flows
	a.invalidate()
}

// deleteFlows removes the chosen rows and pushes one in-memory undo element.
// The snapshot keeps the rows, their order and the selection, so Undo puts
// back the same rows and the same # numbers.
func (a *app) deleteFlows(flows []flow) {
	if len(flows) == 0 {
		return
	}
	before := a.snapshotCaptureUndo()
	ids := make(map[int]struct{}, len(flows))
	for _, f := range flows {
		ids[f.id] = struct{}{}
	}
	nextID := 0
	firstRow := -1
	for row, at := range a.visible {
		if _, gone := ids[a.flows[at].id]; gone {
			if firstRow < 0 {
				firstRow = row
			}
			continue
		}
	}
	if firstRow >= 0 {
		for row := firstRow; row < len(a.visible); row++ {
			if _, gone := ids[a.flows[a.visible[row]].id]; !gone {
				nextID = a.flows[a.visible[row]].id
				break
			}
		}
		if nextID == 0 {
			for row := firstRow - 1; row >= 0; row-- {
				if _, gone := ids[a.flows[a.visible[row]].id]; !gone {
					nextID = a.flows[a.visible[row]].id
					break
				}
			}
		}
	}
	kept := make([]flow, 0, len(a.flows))
	var removedFlows []flow
	var positions, removedIDs, order []int
	for i, f := range a.flows {
		order = append(order, f.id)
		if _, gone := ids[f.id]; gone {
			removedFlows = append(removedFlows, f)
			positions, removedIDs = append(positions, i), append(removedIDs, f.id)
			continue
		}
		kept = append(kept, f)
	}
	removed := len(removedFlows)
	if removed == 0 {
		return
	}
	a.flows = kept
	for id := range ids {
		a.chosen.Remove(id)
	}
	for _, f := range flows {
		a.clearCompareSelection(flowTarget(f))
	}
	a.recount()
	a.pickedID = nextID
	a.applyFilter()
	if nextID != 0 {
		a.choosePicked()
	}
	label := locales.DeletedRequests(removed)
	// A toggle of these exact rows, rather than a whole-capture snapshot:
	// undoing an older notice must leave a newer deletion in place.
	undoRows := func() error {
		for i, f := range removedFlows {
			at := flowInsertAt(a.flows, order, removedIDs, positions[i], f.id)
			a.flows = slices.Insert(a.flows, at, f)
		}
		a.recount()
		a.pickedID = removedFlows[0].id
		a.applyFilter()
		a.choosePicked()
		a.invalidate()
		return nil
	}
	redoRows := func() error {
		kept := slices.DeleteFunc(a.flows, func(f flow) bool { _, gone := ids[f.id]; return gone })
		if len(kept) == len(a.flows) {
			return nil
		}
		a.flows = kept
		for _, id := range removedIDs {
			a.chosen.Remove(id)
		}
		a.recount()
		a.applyFilter()
		if len(a.visible) > 0 {
			a.pickedID = a.flows[a.visible[0]].id
			a.choosePicked()
		}
		a.invalidate()
		return nil
	}
	handle := a.recordUndo(undoRecord{
		code: "capture.delete", label: label,
		before: before, after: a.snapshotCaptureUndo(),
		undoAction: undoRows, redoAction: redoRows,
	})
	a.undoNotice(label, handle)
}

// deleteTargets is what Delete acts on: the whole chosen set when the row is
// part of it, otherwise just that row.
func (a *app) deleteTargets(f flow) []flow {
	if a.chosen.Len() > 1 && a.chosen.Has(f.id) {
		return a.selectedFlows()
	}
	return []flow{f}
}

// flowInsertAt places a restored row among the rows still here, using the
// capture order remembered at deletion. Undoing an older notice after a newer
// one must not put its row above the newer row that was already restored.
func flowInsertAt(flows []flow, order, removed []int, fallback, id int) int {
	original := slices.Index(order, id)
	if original < 0 {
		return min(fallback, len(flows))
	}
	find := func(want int) int {
		return slices.IndexFunc(flows, func(f flow) bool { return f.id == want })
	}
	for i := original - 1; i >= 0; i-- {
		if slices.Contains(removed, order[i]) {
			continue
		}
		if at := find(order[i]); at >= 0 {
			return at + 1
		}
	}
	for i := original + 1; i < len(order); i++ {
		if slices.Contains(removed, order[i]) {
			continue
		}
		if at := find(order[i]); at >= 0 {
			return at
		}
	}
	return min(fallback, len(flows))
}
