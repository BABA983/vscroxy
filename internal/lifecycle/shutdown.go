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

// BeforeShutdownEvent is delivered to participants that can stop a shutdown.
type BeforeShutdownEvent struct {
	Reason ShutdownReason

	mu      sync.Mutex
	vetoed  bool
	vetoIDs []string
}

// Veto stops the shutdown. The id identifies a long-running veto in logs.
func (e *BeforeShutdownEvent) Veto(value bool, id string) {
	if !value {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vetoed = true
	e.vetoIDs = append(e.vetoIDs, id)
}

func (e *BeforeShutdownEvent) result() (bool, []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.vetoed, slices.Clone(e.vetoIDs)
}

// InternalBeforeShutdownEvent is the final veto slot. It is registered with
// Lifecycle.OnFinalVeto and runs after every ordinary before-shutdown
// participant, so the app can ask about unsaved documents once all other
// participants have decided.
type InternalBeforeShutdownEvent struct {
	*BeforeShutdownEvent

	mu  sync.Mutex
	set bool
	fn  func() bool
	id  string
}

// FinalVeto installs the last check. Calling it twice is a programming error.
func (e *InternalBeforeShutdownEvent) FinalVeto(fn func() bool, id string) {
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

func (e *InternalBeforeShutdownEvent) runFinal() {
	e.mu.Lock()
	fn, id := e.fn, e.id
	e.mu.Unlock()
	if fn != nil && fn() {
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

// Force cancels Token. Joiners decide whether to stop early.
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
			if err := job.work(); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s (%s): %w", job.joiner.ID, job.joiner.Label, err))
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return errs
}

// Lifecycle coordinates shutdown. The app owns one value; components register
// with OnBeforeShutdown or OnWillShutdown. The zero value is usable.
type Lifecycle struct {
	mu           sync.Mutex
	before       []func(*BeforeShutdownEvent)
	finalVeto    func(*InternalBeforeShutdownEvent)
	will         []func(*WillShutdownEvent)
	shuttingDown bool
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
// after every OnBeforeShutdown participant.
func (l *Lifecycle) OnFinalVeto(fn func(*InternalBeforeShutdownEvent)) {
	if fn == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
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
// the errors returned by joiners. Call it off the UI thread when joiners can
// take time. A second call while shutdown is in progress or complete does
// nothing.
func (l *Lifecycle) Shutdown(reason ShutdownReason) error {
	l.mu.Lock()
	if l.shuttingDown || l.willShutdown {
		l.mu.Unlock()
		return nil
	}
	l.shuttingDown = true
	before := slices.Clone(l.before)
	final := l.finalVeto
	will := slices.Clone(l.will)
	l.mu.Unlock()

	beforeEvent := &BeforeShutdownEvent{Reason: reason}
	for _, fn := range before {
		fn(beforeEvent)
	}
	if vetoed, ids := beforeEvent.result(); vetoed {
		l.reset()
		return fmt.Errorf("%w: %s", ErrShutdownVetoed, strings.Join(ids, ", "))
	}
	if final != nil {
		internal := &InternalBeforeShutdownEvent{BeforeShutdownEvent: beforeEvent}
		final(internal)
		internal.runFinal()
		if vetoed, ids := beforeEvent.result(); vetoed {
			l.reset()
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

func (l *Lifecycle) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.shuttingDown = false
	l.willShutdown = false
}
