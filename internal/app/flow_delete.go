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
	flows     []flow
	positions []int
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
		if _, gone := ids[f.id]; gone {
			deletion.flows = append(deletion.flows, f)
			deletion.positions = append(deletion.positions, i)
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
	a.recount()
	a.pickedID = nextID
	a.applyFilter()
	if nextID != 0 {
		a.choosePicked()
	}
	a.deletions = append(a.deletions, deletion)
	if len(a.deletions) > maxFlowDeletions {
		a.deletions = slices.Delete(a.deletions, 0, len(a.deletions)-maxFlowDeletions)
	}
	a.notify(notice{level: noticeSuccess, text: locales.DeletedRequests(len(deletion.flows)), action: locales.Undo(), run: a.undoDelete})
}

// undoDelete restores the most recent deletion. The rows go back at their old
// positions, keep their #, and the first one is selected.
func (a *app) undoDelete() {
	if len(a.deletions) == 0 {
		return
	}
	d := a.deletions[len(a.deletions)-1]
	a.deletions = a.deletions[:len(a.deletions)-1]
	for i, f := range d.flows {
		at := d.positions[i]
		if at > len(a.flows) {
			at = len(a.flows)
		}
		a.flows = slices.Insert(a.flows, at, f)
	}
	a.recount()
	a.pickedID = d.flows[0].id
	a.applyFilter()
	a.choosePicked()
}

// deleteTargets is what Delete acts on: the whole chosen set when the row is
// part of it, otherwise just that row.
func (a *app) deleteTargets(f flow) []flow {
	if a.chosen.Len() > 1 && a.chosen.Has(f.id) {
		return a.selectedFlows()
	}
	return []flow{f}
}
