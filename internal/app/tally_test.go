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

// The table reads in the order its numbers were handed out. A request is
// numbered when it starts and arrives when it ends, so a slow one lands after
// faster ones that started behind it: it has to fall back to where its number
// says, or the # column steps backwards.
func TestRowsReadByTheirNumberNotByArrival(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	a := newApp()
	defer a.views.stopSearch()
	if a.srv != nil {
		defer a.srv.Close()
	}
	if a.sort.Column != "id" || a.sort.Descending {
		t.Fatalf("default sort = %+v, want the number ascending", a.sort)
	}
	a.flows = []flow{
		{id: 4, method: "GET", scheme: "https", host: "slow.test", path: "/", status: 200},
		{id: 1, method: "GET", scheme: "https", host: "fast.test", path: "/", status: 200},
		{id: 3, method: "GET", scheme: "https", host: "fast.test", path: "/", status: 200},
		{id: 2, method: "GET", scheme: "https", host: "slow.test", path: "/", status: 200},
	}
	a.recount()
	a.applyFilter()
	ids := make([]int, 0, len(a.visible))
	for _, at := range a.visible {
		ids = append(ids, a.flows[at].id)
	}
	if !slices.Equal(ids, []int{1, 2, 3, 4}) {
		t.Fatalf("rows read %v, want the numbers in order", ids)
	}
}
