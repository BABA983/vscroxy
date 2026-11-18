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

func vetoError(ids []string) error {
	return fmt.Errorf("%w: %s", ErrShutdownVetoed, strings.Join(ids, ", "))
}

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

// BeforeShutdownEvent is delivered to participants that can stop a shutdown.
type BeforeShutdownEvent struct {
	Reason ShutdownReason

	mu      sync.Mutex
	vetoed  bool
	vetoIDs []string
	vetoes  []vetoRecord
}

// Veto is the boolean case of VS Code's
// veto(value: boolean | Promise<boolean>, id: string): it records an immediate
// decision and returns when value is false. The id identifies the decision in
// logs. A true value short-circuits async checks and the final veto; use
// VetoAsync for the Promise<boolean> case.
func (e *BeforeShutdownEvent) Veto(value bool, id string) {
	if !value {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vetoed = true
	e.vetoIDs = append(e.vetoIDs, id)
}

// VetoAsync is the Promise<boolean> case of VS Code's
// veto(value: boolean | Promise<boolean>, id: string). The check runs on a
// package-owned goroutine; checks run in parallel and settle before the final
// veto. An error or a panic counts as a veto. A nil fn registers nothing.
func (e *BeforeShutdownEvent) VetoAsync(fn VetoFunc, id string) {
	if fn == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vetoes = append(e.vetoes, vetoRecord{fn: fn, id: id})
}

// hasVeto reports a synchronous veto without starting any registered async
// checks. A synchronous veto short-circuits the before-shutdown phase.
func (e *BeforeShutdownEvent) hasVeto() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.vetoed
}

// settle waits for all async vetoes registered so far and returns the veto
// state and ids in registration order. A synchronous veto skips the async
// checks entirely, like VS Code's handleVetos.
func (e *BeforeShutdownEvent) settle(ctx context.Context) (bool, []string) {
	e.mu.Lock()
	vetoed := e.vetoed
	ids := slices.Clone(e.vetoIDs)
	vetoes := e.vetoes
	e.vetoes = nil
	e.mu.Unlock()
	if vetoed || len(vetoes) == 0 {
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

// FinalVeto is the Go form of VS Code's
// finalVeto(value: () => boolean | Promise<boolean>, id: string): the check
// runs after every ordinary veto has settled, on a package-owned goroutine. It
// panics when a final veto is already defined, like VS Code's slot. A nil fn
// registers nothing.
func (e *InternalBeforeShutdownEvent) FinalVeto(fn VetoFunc, id string) {
	if fn == nil {
		return
	}
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

// WillShutdown reports whether shutdown passed the veto phase. It stays true
// after a failed join phase; use ShutdownSucceeded to know whether the app can
// safely exit.
func (l *Lifecycle) WillShutdown() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.willShutdown
}

// ShuttingDown reports whether a shutdown is currently in progress.
func (l *Lifecycle) ShuttingDown() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active != nil
}

// ShutdownSucceeded reports whether the last shutdown finished without vetoes
// or errors. A failed shutdown is not cached, so another attempt can run.
func (l *Lifecycle) ShutdownSucceeded() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.completed != nil
}

// ShutdownAsync starts a shutdown and returns once the synchronous
// OnBeforeShutdown callbacks have run. Waiting for async vetoes and joiners,
// plus the later OnFinalVeto registration and OnWillShutdown callbacks,
// happens on a package-owned goroutine; done is also called there with the
// result, so the caller must marshal any UI work back to the UI thread.
//
// A concurrent call waits for the in-flight shutdown and gets its result. A
// call after a successful shutdown gets the same result; a vetoed or failed
// one can be retried. A nil done discards the result.
func (l *Lifecycle) ShutdownAsync(reason ShutdownReason, done func(error)) {
	l.mu.Lock()
	run := l.active
	if run == nil {
		run = l.completed
	}
	if run != nil {
		l.mu.Unlock()
		l.notifyWhenDone(run, done)
		return
	}
	run = &shutdownRun{done: make(chan struct{})}
	l.active = run
	before := slices.Clone(l.before)
	final := l.finalVeto
	will := slices.Clone(l.will)
	l.mu.Unlock()

	beforeCtx, cancelBefore := context.WithCancel(context.Background())
	beforeEvent := &BeforeShutdownEvent{Reason: reason}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		for _, fn := range before {
			fn(beforeEvent)
		}
	}()
	if recovered != nil {
		cancelBefore()
		l.finishShutdown(run, panicError("shutdown", recovered), false, done)
		return
	}
	if beforeEvent.hasVeto() {
		_, ids := beforeEvent.settle(beforeCtx)
		cancelBefore()
		l.finishShutdown(run, vetoError(ids), false, done)
		return
	}

	go func() {
		defer cancelBefore()
		err, reachedWill := l.finishPhases(reason, beforeEvent, beforeCtx, final, will)
		l.finishShutdown(run, err, reachedWill, done)
	}()
}

func (l *Lifecycle) notifyWhenDone(run *shutdownRun, done func(error)) {
	if done == nil {
		return
	}
	go func() {
		<-run.done
		done(run.err)
	}()
}

func (l *Lifecycle) finishShutdown(run *shutdownRun, err error, reachedWill bool, done func(error)) {
	run.err = err
	l.mu.Lock()
	l.active = nil
	if reachedWill && err == nil {
		l.completed = run
	}
	l.mu.Unlock()
	close(run.done)
	if done != nil {
		go done(err)
	}
}

func (l *Lifecycle) finishPhases(reason ShutdownReason, before *BeforeShutdownEvent, beforeCtx context.Context, final func(*InternalBeforeShutdownEvent), will []func(*WillShutdownEvent)) (err error, reachedWill bool) {
	defer func() {
		if r := recover(); r != nil {
			err = panicError("shutdown", r)
		}
	}()
	if vetoed, ids := before.settle(beforeCtx); vetoed {
		return vetoError(ids), false
	}
	if final != nil {
		internal := &InternalBeforeShutdownEvent{BeforeShutdownEvent: before}
		final(internal)
		internal.runFinal(beforeCtx)
		if vetoed, ids := before.settle(beforeCtx); vetoed {
			return vetoError(ids), false
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
	return errors.Join(errs...), true
}

// Shutdown runs the veto phase and then, unless vetoed, the join phase. It
// returns ErrShutdownVetoed when a participant stopped the shutdown, or joins
// the errors returned by joiners. It blocks until shutdown settles, so callers
// on the UI thread should use ShutdownAsync when a veto or joiner needs the UI
// thread to keep running. Concurrent and later calls get the same result.
func (l *Lifecycle) Shutdown(reason ShutdownReason) error {
	result := make(chan error, 1)
	l.ShutdownAsync(reason, func(err error) { result <- err })
	return <-result
}
