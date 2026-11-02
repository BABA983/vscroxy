package app

// The tally is what the sidebar, the memory limits and the inspector need to
// know about the whole table: counts per host and app, the errors, slow and
// pinned flows, the memory and disk the flows take, and which flows replay
// which. Arriving and dropped flows update it, so live traffic never walks
// every flow; recount builds it again after anything else changes a.flows.

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
)

type tally struct {
	hosts, clients map[string]int
	apps           map[capture.SSLApp]int // executables, by app name and path
	errors, slow   int
	pinned         int
	memory, disk   int64            // estimates, see flow.retained and flow.spilled
	known          map[int]struct{} // the ids in a.flows
	replays        map[int][]flow   // replays by the id of their source
	live           int              // flows this capture recorded, not loaded
	latest         time.Time        // the newest flow's time; dropping flows never lowers it
}

// tallyStale reports whether a.flows changed without the tally: the full
// passes recount then, so a caller that replaced the flows is not wrong.
func (a *app) tallyStale() bool { return len(a.tally.known) != len(a.flows) }

// recount builds the tally from every flow, and the sidebar's lists from it.
func (a *app) recount() {
	a.tally = tally{}
	for i := range a.flows {
		a.count(&a.flows[i], 1)
	}
	a.listCounts()
}

// count adds a flow to the tally, or with sign -1 takes it away. A flow's
// messages count with it: those that arrive later count as addFrame keeps them.
func (a *app) count(f *flow, sign int) {
	t := &a.tally
	if t.known == nil {
		t.hosts, t.clients = make(map[string]int), make(map[string]int)
		t.apps, t.known, t.replays = make(map[capture.SSLApp]int), make(map[int]struct{}), make(map[int][]flow)
	}
	t.hosts[f.host] += sign
	if f.process != "" {
		t.clients[f.process] += sign
		if filepath.IsAbs(f.processPath) {
			t.apps[capture.SSLApp{Name: f.process, Path: f.processPath}] += sign
		}
	}
	if f.failed() {
		t.errors += sign
	}
	if f.millis >= slowMillis {
		t.slow += sign
	}
	if f.pinned {
		t.pinned += sign
	}
	if f.live {
		t.live += sign
	}
	ms := a.frames[f.id]
	t.memory += int64(sign) * (f.retained() + messagesRetained(ms))
	t.disk += int64(sign) * (f.spilled() + messagesSpilled(ms))
	if sign > 0 {
		t.known[f.id] = struct{}{}
		if f.at.After(t.latest) {
			t.latest = f.at
		}
		if f.replayOf != 0 {
			t.replays[f.replayOf] = append(t.replays[f.replayOf], *f)
		}
		return
	}
	delete(t.known, f.id)
	if f.replayOf != 0 {
		t.replays[f.replayOf] = slices.DeleteFunc(t.replays[f.replayOf], func(r flow) bool { return r.id == f.id })
	}
}

// listCounts orders the sidebar's Apps and Domains, the busiest first, and
// each app's executables by path.
func (a *app) listCounts() {
	list := func(counts map[string]int, into []domain) []domain {
		into = into[:0]
		for name, n := range counts {
			if n > 0 {
				into = append(into, domain{name: name, n: n})
			} else {
				delete(counts, name)
			}
		}
		slices.SortFunc(into, func(x, y domain) int { return cmp.Or(cmp.Compare(y.n, x.n), strings.Compare(x.name, y.name)) })
		return into
	}
	a.domains = list(a.tally.hosts, a.domains)
	a.clients = list(a.tally.clients, a.clients)
	a.clientApps = make(map[string][]capture.SSLApp)
	for app, n := range a.tally.apps {
		if n > 0 {
			a.clientApps[app.Name] = append(a.clientApps[app.Name], app)
		} else {
			delete(a.tally.apps, app)
		}
	}
	for _, apps := range a.clientApps {
		slices.SortFunc(apps, func(x, y capture.SSLApp) int { return strings.Compare(x.Path, y.Path) })
	}
}
