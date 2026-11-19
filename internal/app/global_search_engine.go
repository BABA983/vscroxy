package app

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/async"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

const (
	globalSearchDebounce  = 120 * time.Millisecond
	globalSearchBatchSize = 16
	globalSearchLimit     = 200
)

// globalSearch is the Search view's state. The runner and worker own no UI
// elements; batches are applied on the UI thread, and a generation check
// drops anything a superseded query left in the channel.
type globalSearch struct {
	input       string
	armed       searchRequest
	query       searchQuery
	handle      ui.Handle
	matchCase   bool
	wholeWord   bool
	regexpQuery bool
	results     []searchResult
	current     int
	limited     int
	running     bool
	problem     string
	reveal      searchReveal
	runner      *globalSearchRunner
}

type searchRequest struct {
	input       string
	matchCase   bool
	wholeWord   bool
	regexpQuery bool
}

func (s *globalSearch) caseOption() *bool { return &s.matchCase }
func (s *globalSearch) wordOption() *bool { return &s.wholeWord }
func (s *globalSearch) regexpOption() *bool { return &s.regexpQuery }

func (s *globalSearch) request() searchRequest {
	return searchRequest{s.input, s.matchCase, s.wholeWord, s.regexpQuery}
}

// step moves the selected result. The next and previous commands are in the
// registry and the palette, and the view offers the same two buttons.
func (s *globalSearch) step(dir int) {
	if len(s.results) == 0 {
		s.current = 0
		return
	}
	s.current = (s.current + dir + len(s.results)) % len(s.results)
}

type globalSearchRunner struct {
	delayer *async.Delayer[uint64]
	mu      sync.Mutex
	gen     uint64
	ctx     context.Context
	cancel  context.CancelFunc
	starts  chan uint64
	batches chan searchBatch
	notify  func()
}

func newGlobalSearchRunner(clock async.Clock, notify func()) *globalSearchRunner {
	return &globalSearchRunner{
		delayer: async.NewDelayer[uint64](globalSearchDebounce, async.WithClock(clock)),
		ctx:     context.Background(),
		starts:  make(chan uint64, 1),
		batches: make(chan searchBatch, globalSearchBatchSize*4),
		notify:  notify,
	}
}

// change cancels the query before it, including one still waiting in the
// debounce timer. Each query gets its own context, so a slow worker cannot
// append rows after a newer query has started.
func (r *globalSearchRunner) change() {
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	r.delayer.Cancel()
	r.gen++
	ctx, cancel := context.WithCancel(context.Background())
	r.ctx, r.cancel = ctx, cancel
	r.mu.Unlock()
}

func (r *globalSearchRunner) schedule() {
	r.mu.Lock()
	ctx, gen := r.ctx, r.gen
	r.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	delivered := r.delayer.Trigger(ctx, func(context.Context) (uint64, error) { return gen, nil })
	go func() {
		result := <-delivered
		if result.Err != nil {
			return
		}
		select {
		case r.starts <- result.Value:
		default: // an older start is still queued; the newer generation wins
			select {
			case <-r.starts:
			default:
			}
			r.starts <- result.Value
		}
		if r.notify != nil {
			r.notify()
		}
	}()
}

func (r *globalSearchRunner) contextFor(gen uint64) context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	if gen != r.gen || r.ctx.Err() != nil {
		return nil
	}
	return r.ctx
}

func (r *globalSearchRunner) stop() {
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	r.delayer.Cancel()
	r.mu.Unlock()
}

func (a *app) searchRunner() *globalSearchRunner {
	s := &a.searchAll
	if s.runner == nil {
		s.runner = newGlobalSearchRunner(a.timerClock(), a.invalidate)
	}
	return s.runner
}

// setSearchQuery is what typing into the view and tests call. The worker
// starts only after the delayer fires, so a burst of keystrokes scans once.
func (a *app) setSearchQuery(input string) {
	s := &a.searchAll
	s.input = input
	a.syncSearchQuery()
}

// syncSearchQuery notices edits to the query and to the shared find options.
// MyGo binds fields before the frame's change notices, so one comparison per
// pass is enough; option toggles reach this through the same target interface
// Alt+C/W/R uses.
func (a *app) syncSearchQuery() {
	s := &a.searchAll
	req := s.request()
	if req == s.armed {
		return
	}
	s.armed = req
	runner := a.searchRunner()
	runner.change()
	s.results, s.limited, s.current, s.problem = nil, 0, 0, ""
	s.reveal = searchReveal{}
	if req.input == "" {
		s.running, s.query = false, searchQuery{}
		return
	}
	q, err := compileSearchQuery(req.input, s)
	if err != nil {
		s.running, s.query, s.problem = false, searchQuery{}, err.Error()
		return
	}
	s.query, s.running = q, true
	runner.schedule()
}

// drainSearch is called once on the UI thread per view pass. Starting a scan
// snapshots the data there; the scanner itself reads only that copy.
func (a *app) drainSearch() {
	s := &a.searchAll
	runner := a.searchRunner()
	select {
	case gen := <-runner.starts:
		if ctx := runner.contextFor(gen); ctx != nil {
			snapshot := a.searchSnapshot(s.query)
			go guarded(func() { scanSearch(ctx, snapshot, gen, runner.batches, runner.notify) })
		}
	default:
	}
	for {
		select {
		case batch := <-runner.batches:
			a.applySearchBatch(batch)
		default:
			return
		}
	}
}

func (a *app) applySearchBatch(batch searchBatch) {
	s := &a.searchAll
	if batch.gen != s.armedGen() {
		return // stale: a newer query owns the result list
	}
	if len(batch.results) > 0 {
		s.results = append(s.results, batch.results...)
		if len(s.results) > globalSearchLimit {
			s.results = s.results[:globalSearchLimit]
			s.limited++
		}
	}
	if batch.limited > 0 {
		s.limited = batch.limited
	}
	if batch.done {
		s.running = false
	}
}

func (s *globalSearch) armedGen() uint64 {
	if s.runner == nil {
		return 0
	}
	s.runner.mu.Lock()
	defer s.runner.mu.Unlock()
	return s.runner.gen
}

// focusSearch opens the Search view and hands the keyboard to its query.
func (a *app) focusSearch(c *ui.Context) {
	a.page, a.sidebarChoice = pageSearch, "search"
	a.workbench.sidebarHidden = false
	a.invalidate()
	if c != nil {
		a.searchAll.handle.Focus(c)
	}
}

// searchReveal is the match to select after a result opens its resource.
type searchReveal struct {
	key      string
	field    string
	text     string
	line     int
	env      string
	variable string
	flowID   int
	side     int
}

func (a *app) openSearchResult(r searchResult) {
	s := &a.searchAll
	if at := slices.Index(s.results, r); at >= 0 {
		s.current = at
	}
	s.reveal = searchReveal{key: r.key, field: r.field, text: r.match(), line: r.line,
		env: r.env, variable: r.variable, flowID: r.flowID, side: r.side}
	switch r.source {
	case searchCollections:
		a.openSaved(r.key)
	case searchEnvironments:
		if r.env == "" {
			a.editGlobals()
			a.varsGlobals, a.varsOpen = true, true
		} else {
			a.openEnvironment(r.env)
		}
	case searchFlows:
		a.page, a.sidebarChoice, a.filter, a.query = pageLive, "search", "all", ""
		a.applyFilter()
		a.pickedID = r.flowID
		a.restoreSelection()
		a.choosePicked()
		if r.side >= 0 {
			a.bodyMode[r.side] = 0 // the raw text the search scanned
		}
	}
}

// revealSearchMatch puts the caret on the match once the editor for the
// resource exists. It runs at the end of a build, after opening a tab or a
// captured flow, and clears itself so it cannot fire on later frames.
func (a *app) revealSearchMatch() {
	s := &a.searchAll
	reveal := s.reveal
	if reveal.text == "" {
		return
	}
	if a.page == pageRequest {
		d := a.requestDocument
		if d == nil || d.savedKey != reveal.key {
			return
		}
		switch reveal.field {
		case locales.RequestBody(), locales.PreRequestScript(), locales.PostResponseScript():
			var state *editor.State
			switch reveal.field {
			case locales.RequestBody():
				state = d.bodyEditor
			case locales.PreRequestScript():
				d.composeTab, d.scriptTab = composeTabScripts, 0
				state = d.preEditor
			case locales.PostResponseScript():
				d.composeTab, d.scriptTab = composeTabScripts, 1
				state = d.postEditor
			}
			if state == nil {
				return // the editor for this field is not built yet
			}
			a.requestFind(state, reveal)
		case locales.Headers():
			d.composeTab = composeTabHeaders
		case locales.Description():
			d.composeTab = composeTabDocs
		}
		s.reveal = searchReveal{}
		return
	}
	if a.page == pageLive && reveal.flowID != 0 {
		if f, ok := a.current(); !ok || f.id != reveal.flowID || reveal.side < 0 || reveal.side >= 2 {
			return
		}
		state := a.bodyText[reveal.side]
		if state == nil {
			return // the body reader is built later in the frame
		}
		a.requestFind(state, reveal)
		s.reveal = searchReveal{}
	}
}

func (a *app) requestFind(state *editor.State, reveal searchReveal) {
	if state == nil {
		return
	}
	state.Find(reveal.text)
	if reveal.line > 0 {
		state.GoTo(editor.Pos{Line: reveal.line - 1, Col: 0})
	}
	state.Find(reveal.text)
}

// stopSearch cancels a running scan when the app shuts down.
func (a *app) stopSearch() {
	if a.searchAll.runner != nil {
		a.searchAll.runner.stop()
	}
}

// searchProblemText is the inline explanation for an invalid regular
// expression. A broken pattern must be readable without a notice or a crash.
func searchProblemText(problem string) string {
	if problem == "" {
		return ""
	}
	return locales.InvalidSearchPatternWith(problem)
}

// searchIsCanceled helps tests assert that a new query really ended an older
// worker without relying on sleeps or wall-clock time.
func searchIsCanceled(err error) bool {
	return errors.Is(err, context.Canceled)
}

