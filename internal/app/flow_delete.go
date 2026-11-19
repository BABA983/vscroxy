package app

import (
	"runtime"
	"slices"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// flowDeletion is one deletion a person can undo. The flows keep their
// capture IDs and their positions in a.flows, so undo restores the same rows
// and the same # numbers.
type flowDeletion struct {
	id        uint64
	flows     []flow
	positions []int
	ids       []int // ids of the removed rows, in their original order
	order     []int // every id in a.flows before the removal
}

const maxFlowDeletions = 20

// flowDeleteKey is the platform's delete gesture: ⌘⌫ on macOS, Delete on the
// other platforms.
func flowDeleteKey() ui.Key {
	if runtime.GOOS == "darwin" {
		return ui.KeyBackspace
	}
	return ui.KeyDelete
}

// deleteFlows removes the rows chosen for a delete. Publication and filtering
// are rebuilt immediately; the toast's Undo puts the same flows back at their
// old positions.
// queueDeleteFlows defers a row deletion until the next view pass, after the
// table that offered the action has finished building.
func (a *app) queueDeleteFlows(flows []flow) {
	if len(flows) == 0 {
		return
	}
	a.pendingFlowDelete = flows
	a.invalidate()
}

func (a *app) deleteFlows(flows []flow) {
	if len(flows) == 0 {
		return
	}
	ids := make(map[int]struct{}, len(flows))
	for _, f := range flows {
		ids[f.id] = struct{}{}
	}
	var deletion flowDeletion
	deletion.order = make([]int, 0, len(a.flows))
	kept := make([]flow, 0, len(a.flows))
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
	for i, f := range a.flows {
		deletion.order = append(deletion.order, f.id)
		if _, gone := ids[f.id]; gone {
			deletion.flows = append(deletion.flows, f)
			deletion.positions = append(deletion.positions, i)
			deletion.ids = append(deletion.ids, f.id)
			continue
		}
		kept = append(kept, f)
	}
	if len(deletion.flows) == 0 {
		return
	}
	a.flows = kept
	for id := range ids {
		a.chosen.Remove(id)
	}
	for _, f := range deletion.flows {
		a.clearCompareSelection(flowTarget(f))
	}
	a.recount()
	a.pickedID = nextID
	a.applyFilter()
	if nextID != 0 {
		a.choosePicked()
	}
	a.nextDeletionID++
	deletion.id = a.nextDeletionID
	a.deletions = append(a.deletions, deletion)
	if len(a.deletions) > maxFlowDeletions {
		a.deletions = slices.Delete(a.deletions, 0, len(a.deletions)-maxFlowDeletions)
	}
	// A toast or a center entry keeps this deletion's id, so an Undo pressed
	// later restores these rows and not a more recent deletion.
	id := deletion.id
	a.notify(notice{level: noticeSuccess, text: locales.DeletedRequests(len(deletion.flows)), action: locales.Undo(),
		valid: func() bool { return a.hasDeletion(id) },
		run:   func() { a.undoDeletion(id) }})
}

// undoDelete restores the most recent deletion, as Cmd-Z does.
func (a *app) undoDelete() {
	if len(a.deletions) == 0 {
		return
	}
	a.undoDeletion(a.deletions[len(a.deletions)-1].id)
}

// undoDeletion restores one deletion by id, and is a no-op if that deletion
// was already undone or fell off the stack. The rows go back at their old
// positions, keep their #, and the first one is selected.
func (a *app) undoDeletion(id uint64) {
	at := slices.IndexFunc(a.deletions, func(d flowDeletion) bool { return d.id == id })
	if at < 0 {
		return
	}
	d := a.deletions[at]
	a.deletions = slices.Delete(a.deletions, at, at+1)
	for i, f := range d.flows {
		at := flowInsertAt(a.flows, d.order, d.ids, d.positions[i], f.id)
		a.flows = slices.Insert(a.flows, at, f)
	}
	a.recount()
	a.pickedID = d.flows[0].id
	a.applyFilter()
	a.choosePicked()
}

// hasDeletion says whether this undo is still possible. A notice keeps its
// action only while its deletion is on the stack; replacing the session clears
// that stack, so the action hides instead of inserting old rows into it.
func (a *app) hasDeletion(id uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.ContainsFunc(a.deletions, func(d flowDeletion) bool { return d.id == id })
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

// deleteTargets is what Delete acts on: the whole chosen set when the row is
// part of it, otherwise just that row.
func (a *app) deleteTargets(f flow) []flow {
	if a.chosen.Len() > 1 && a.chosen.Has(f.id) {
		return a.selectedFlows()
	}
	return []flow{f}
}
