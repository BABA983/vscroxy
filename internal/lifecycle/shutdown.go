package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// ErrShutdownVetoed reports that a before-shutdown participant stopped the
// shutdown. Use errors.Is to recognize it; the wrapped message lists the ids.
var ErrShutdownVetoed = errors.New("shutdown vetoed")

// ShutdownReason tells participants why the application is closing.
type ShutdownReason uint8

const (
	ShutdownReasonClose ShutdownReason = iota + 1
	ShutdownReasonQuit
	ShutdownReasonReload
)

// String implements fmt.Stringer.
func (r ShutdownReason) String() string {
	switch r {
	case ShutdownReasonClose:
		return "CLOSE"
	case ShutdownReasonQuit:
		return "QUIT"
	case ShutdownReasonReload:
		return "RELOAD"
	default:
		return "UNKNOWN"
	}
}

// VetoFunc checks whether a shutdown should be stopped. ctx is canceled after
// the before-shutdown phase settles. A returned error counts as a veto.
type VetoFunc func(ctx context.Context) (bool, error)

type vetoRecord struct {
	fn VetoFunc
	id string
}

func asVetoFunc(value any) VetoFunc {
	switch v := value.(type) {
	case VetoFunc:
		return v
	case func(context.Context) (bool, error):
		return VetoFunc(v)
	case func() (bool, error):
		return func(context.Context) (bool, error) { return v() }
	case func() bool:
		return func(context.Context) (bool, error) { return v(), nil }
	default:
		panic(fmt.Sprintf("lifecycle: unsupported veto value of type %T", value))
	}
}

// BeforeShutdownEvent is delivered to participants that can stop a shutdown.
type BeforeShutdownEvent struct {
	Reason ShutdownReason

	mu      sync.Mutex
	vetoed  bool
	vetoIDs []string
	vetoes  []vetoRecord
}

// Veto stops the shutdown. The id identifies a long-running veto in logs.
// value is a bool or a VetoFunc (also written as
// func(context.Context) (bool, error), func() (bool, error), or func() bool).
// Async checks start on package-owned goroutines and run in parallel; their
// results are collected before the final veto runs. An error or a panic counts
// as a veto. Unsupported value types are a programming error.
func (e *BeforeShutdownEvent) Veto(value any, id string) {
	if v, ok := value.(bool); ok {
		if !v {
			return
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		e.vetoed = true
		e.vetoIDs = append(e.vetoIDs, id)
		return
	}
	fn := asVetoFunc(value)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vetoes = append(e.vetoes, vetoRecord{fn: fn, id: id})
}

// settle waits for all async vetoes registered so far and returns the veto
// state and ids in registration order.
func (e *BeforeShutdownEvent) settle(ctx context.Context) (bool, []string) {
	e.mu.Lock()
	vetoed := e.vetoed
	ids := slices.Clone(e.vetoIDs)
	vetoes := e.vetoes
	e.vetoes = nil
	e.mu.Unlock()
	if len(vetoes) == 0 {
		return vetoed, ids
	}

	type result struct {
		index int
		veto  bool
	}
	results := make(chan result, len(vetoes))
	for i, veto := range vetoes {
		go func() {
			vetoed, _ := callVeto(ctx, veto.fn)
			results <- result{index: i, veto: vetoed}
		}()
	}
	resolved := make([]bool, len(vetoes))
	for range vetoes {
		r := <-results
		resolved[r.index] = r.veto
	}
	for i, veto := range vetoes {
		if resolved[i] {
			vetoed = true
			ids = append(ids, veto.id)
		}
	}
	return vetoed, ids
}

func callVeto(ctx context.Context, fn VetoFunc) (veto bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicError("veto", r)
		}
	}()
	if fn == nil {
		return false, nil
	}
	veto, err = fn(ctx)
	return veto || err != nil, err
}

func callVetoAsync(ctx context.Context, fn VetoFunc) bool {
	result := make(chan bool, 1)
	go func() {
		veto, _ := callVeto(ctx, fn)
		result <- veto
	}()
	return <-result
}

// InternalBeforeShutdownEvent is the final veto slot. It is registered with
// Lifecycle.OnFinalVeto and runs after every ordinary before-shutdown
// participant, so the app can ask about unsaved documents once all other
// participants have decided.
type InternalBeforeShutdownEvent struct {
	*BeforeShutdownEvent

	mu  sync.Mutex
	set bool
	fn  VetoFunc
	id  string
}

// FinalVeto installs the last check. It accepts the same values as Veto and
// panics when a final veto is already defined, like VS Code's final veto slot.
func (e *InternalBeforeShutdownEvent) FinalVeto(value any, id string) {
	fn := asVetoFunc(value)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.set {
		panic(fmt.Sprintf("lifecycle: final veto already defined (id: %s)", e.id))
	}
	e.set, e.fn, e.id = true, fn, id
}

func (e *InternalBeforeShutdownEvent) runFinal(ctx context.Context) {
	e.mu.Lock()
	fn, id := e.fn, e.id
	e.mu.Unlock()
	if fn == nil {
		return
	}
	if callVetoAsync(ctx, fn) {
		e.BeforeShutdownEvent.Veto(true, id)
	}
}

// WillShutdownJoinerOrder controls when a joiner runs. Last joiners run after
// all default joiners have finished.
type WillShutdownJoinerOrder uint8

const (
	WillShutdownJoinerOrderDefault WillShutdownJoinerOrder = iota + 1
	WillShutdownJoinerOrderLast
)

// WillShutdownEventJoiner identifies a participant in logs when its work
// takes long or fails.
type WillShutdownEventJoiner struct {
	ID    string
	Label string
	Order WillShutdownJoinerOrder
}

// WillShutdownEvent collects work that must finish before the app exits.
// Work passed to Join should observe Token when it can be canceled.
type WillShutdownEvent struct {
	Reason ShutdownReason
	Token  context.Context

	mu       sync.Mutex
	cancel   context.CancelFunc
	nextID   int
	pending  []joinRecord
	defaults []shutdownJob
	lasts    []shutdownJob
}

type joinRecord struct {
	id     int
	joiner WillShutdownEventJoiner
}

type shutdownJob struct {
	id     int
	joiner WillShutdownEventJoiner
	work   func() error
}

// Join registers work to run during shutdown. Default joiners run in parallel;
// Last joiners wait for all defaults. Join is meant to be called from an
// OnWillShutdown participant.
func (e *WillShutdownEvent) Join(work func() error, joiner WillShutdownEventJoiner) {
	if work == nil {
		return
	}
	if joiner.Order != WillShutdownJoinerOrderLast {
		joiner.Order = WillShutdownJoinerOrderDefault
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	record := joinRecord{id: e.nextID, joiner: joiner}
	e.nextID++
	e.pending = append(e.pending, record)
	job := shutdownJob{id: record.id, joiner: joiner, work: work}
	if joiner.Order == WillShutdownJoinerOrderLast {
		e.lasts = append(e.lasts, job)
		return
	}
	e.defaults = append(e.defaults, job)
}

// Joiners returns the participants whose work has not finished yet.
func (e *WillShutdownEvent) Joiners() []WillShutdownEventJoiner {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]WillShutdownEventJoiner, 0, len(e.pending))
	for _, record := range e.pending {
		out = append(out, record.joiner)
	}
	return out
}

// Force cancels Token and stops Shutdown from waiting for either join phase.
// Joiners that ignore the token may keep running.
func (e *WillShutdownEvent) Force() {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *WillShutdownEvent) finish(id int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	i := slices.IndexFunc(e.pending, func(r joinRecord) bool { return r.id == id })
	if i >= 0 {
		e.pending = slices.Delete(e.pending, i, i+1)
	}
}

func (e *WillShutdownEvent) run(jobs []shutdownJob) []error {
	if len(jobs) == 0 {
		return nil
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, job := range jobs {
		wg.Go(func() {
			defer e.finish(job.id)
			if err := runJoiner(job); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s (%s): %w", job.joiner.ID, job.joiner.Label, err))
				mu.Unlock()
			}
		})
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// VS Code races the settled joiners against the force token. Joiners that
	// ignore the token may keep running, but Shutdown no longer waits for them.
	select {
	case <-done:
	case <-e.Token.Done():
	}
	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(errs)
}

func runJoiner(job shutdownJob) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicError("joiner", r)
		}
	}()
	return job.work()
}

// shutdownRun publishes one shutdown's result to every caller that arrives
// while it is in flight, and to later callers once it has completed.
type shutdownRun struct {
	done chan struct{}
	err  error
}

// Lifecycle coordinates shutdown. The app owns one value; components register
// with OnBeforeShutdown or OnWillShutdown. The zero value is usable.
type Lifecycle struct {
	mu           sync.Mutex
	before       []func(*BeforeShutdownEvent)
	finalVeto    func(*InternalBeforeShutdownEvent)
	will         []func(*WillShutdownEvent)
	active       *shutdownRun
	completed    *shutdownRun
	willShutdown bool
}

// OnBeforeShutdown registers a participant that may veto. Registrations live
// as long as the Lifecycle, so this method has nothing to dispose.
func (l *Lifecycle) OnBeforeShutdown(fn func(*BeforeShutdownEvent)) {
	if fn == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.before = append(l.before, fn)
}

// OnFinalVeto registers the one participant allowed to call FinalVeto. It runs
// after every OnBeforeShutdown participant. A second registration panics, like
// VS Code defining a second final veto.
func (l *Lifecycle) OnFinalVeto(fn func(*InternalBeforeShutdownEvent)) {
	if fn == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finalVeto != nil {
		panic("lifecycle: final veto participant already registered")
	}
	l.finalVeto = fn
}

// OnWillShutdown registers a participant that can Join shutdown work.
func (l *Lifecycle) OnWillShutdown(fn func(*WillShutdownEvent)) {
	if fn == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.will = append(l.will, fn)
}

// WillShutdown reports whether shutdown passed the veto phase.
func (l *Lifecycle) WillShutdown() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.willShutdown
}

// Shutdown runs the veto phase and then, unless vetoed, the join phase. It
// returns ErrShutdownVetoed when a participant stopped the shutdown, or joins
// the errors returned by joiners. A concurrent call waits for the in-flight
// shutdown and gets its result; a call after a completed shutdown gets the
// same result. A veto is not cached, so the shutdown can be retried.
func (l *Lifecycle) Shutdown(reason ShutdownReason) (err error) {
	l.mu.Lock()
	if run := l.active; run != nil {
		l.mu.Unlock()
		<-run.done
		return run.err
	}
	if run := l.completed; run != nil {
		l.mu.Unlock()
		return run.err
	}
	run := &shutdownRun{done: make(chan struct{})}
	l.active = run
	before := slices.Clone(l.before)
	final := l.finalVeto
	will := slices.Clone(l.will)
	l.mu.Unlock()

	defer func() {
		recovered := recover()
		if recovered != nil {
			err = panicError("shutdown", recovered)
		}
		run.err = err
		l.mu.Lock()
		l.active = nil
		if recovered == nil && l.willShutdown {
			l.completed = run
		}
		l.mu.Unlock()
		close(run.done)
		if recovered != nil {
			panic(recovered)
		}
	}()

	beforeCtx, cancelBefore := context.WithCancel(context.Background())
	defer cancelBefore()
	beforeEvent := &BeforeShutdownEvent{Reason: reason}
	for _, fn := range before {
		fn(beforeEvent)
	}
	if vetoed, ids := beforeEvent.settle(beforeCtx); vetoed {
		return fmt.Errorf("%w: %s", ErrShutdownVetoed, strings.Join(ids, ", "))
	}
	if final != nil {
		internal := &InternalBeforeShutdownEvent{BeforeShutdownEvent: beforeEvent}
		final(internal)
		internal.runFinal(beforeCtx)
		if vetoed, ids := beforeEvent.settle(beforeCtx); vetoed {
			return fmt.Errorf("%w: %s", ErrShutdownVetoed, strings.Join(ids, ", "))
		}
	}

	l.mu.Lock()
	l.willShutdown = true
	l.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	willEvent := &WillShutdownEvent{Reason: reason, Token: ctx, cancel: cancel}
	for _, fn := range will {
		fn(willEvent)
	}
	errs := willEvent.run(willEvent.defaults)
	errs = append(errs, willEvent.run(willEvent.lasts)...)
	return errors.Join(errs...)
}
