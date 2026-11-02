package app

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
)

// Live traffic updates the tally and the rows from each batch alone; after any
// mix of arrivals, trims, pins, sorts and filters they must match a full pass.
func TestIncrementalArrivalMatchesAFullRecount(t *testing.T) {
	for seed := range 40 {
		r := rand.New(rand.NewPCG(uint64(seed), 7))
		a := workspaceApp()
		a.flows = nil
		a.flowLimit = 30 + r.IntN(40)
		a.memoryLimit = 1 << 20
		a.sort = ui.SortOrder{Column: []string{"", "status", "host", "id"}[r.IntN(4)], Descending: r.IntN(2) == 0}
		a.sortApplied = a.sort
		a.filter = []string{"all", "errors", "host:h1", "pinned"}[r.IntN(4)]
		a.applyFilter()
		base := time.Now()
		next := 1
		for batch := range 25 {
			var pend []capture.Flow
			for range 1 + r.IntN(8) {
				pend = append(pend, capture.Flow{ID: next, Method: "GET", Scheme: "https", Host: fmt.Sprintf("h%d", r.IntN(3)), Path: "/", Status: []int{200, 404, 500}[r.IntN(3)], Kind: "http", At: base.Add(time.Duration(next) * time.Second), Process: fmt.Sprintf("p%d", r.IntN(2)), ReplayOf: r.IntN(next)})
				next++
			}
			a.pending = pend
			a.drain()
			if r.IntN(4) == 0 && len(a.flows) > 0 {
				a.togglePin(a.flows[r.IntN(len(a.flows))])
			}
			vis, act := slices.Clone(a.visible), slices.Clone(a.activityRows)
			got := a.tally
			a.recount()
			a.applyFilter()
			want := a.tally
			if !slices.Equal(vis, a.visible) || !slices.Equal(act, a.activityRows) {
				t.Fatalf("seed %d batch %d rows: %v want %v", seed, batch, vis, a.visible)
			}
			if got.errors != want.errors || got.slow != want.slow || got.pinned != want.pinned || got.memory != want.memory || got.disk != want.disk || got.live != want.live || len(got.known) != len(want.known) {
				t.Fatalf("seed %d batch %d tally %+v want %+v", seed, batch, got, want)
			}
			for k, v := range want.hosts {
				if got.hosts[k] != v {
					t.Fatalf("seed %d hosts %v want %v", seed, got.hosts, want.hosts)
				}
			}
			for id, rs := range want.replays {
				if len(got.replays[id]) != len(rs) {
					t.Fatalf("seed %d replays of %d: %d want %d", seed, id, len(got.replays[id]), len(rs))
				}
			}
		}
	}
}
