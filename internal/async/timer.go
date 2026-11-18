package async

import (
	"sync"
	"time"
)

// TimeoutTimer corresponds to VS Code's TimeoutTimer. CancelAndSet and
// SetIfNotSet return a channel carrying the callback's error, including a
// recovered panic; Cancel settles it with ErrCanceled. The callback runs on the
// clock's timer goroutine, never on a UI thread.
type TimeoutTimer struct {
	mu         sync.Mutex
	clock      Clock
	timer      Timer
	runner     Action
	errs       *errorDelivery
	generation uint64
	disposed   bool
}

// NewTimeoutTimer constructs a TimeoutTimer and arms it immediately, like the
// two-argument VS Code constructor. Use Errors to observe its result.
func NewTimeoutTimer(runner Action, timeout time.Duration, opts ...Option) *TimeoutTimer {
	timer := &TimeoutTimer{clock: resolveClock(opts)}
	timer.CancelAndSet(runner, timeout)
	return timer
}

// CancelAndSet corresponds to TimeoutTimer.cancelAndSet.
func (t *TimeoutTimer) CancelAndSet(runner Action, timeout time.Duration) <-chan error {
	errs := newErrorDelivery()
	t.mu.Lock()
	if t.disposed {
		t.mu.Unlock()
		errs.settle(ErrDisposed)
		return errs.ch
	}
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.generation++
	generation := t.generation
	t.runner = runner
	t.errs = errs
	t.timer = t.clock.AfterFunc(timeout, func() { t.fire(generation) })
	t.mu.Unlock()
	return errs.ch
}

// SetIfNotSet corresponds to TimeoutTimer.setIfNotSet. When a timer is already
// armed it returns that timer's existing result channel unchanged.
func (t *TimeoutTimer) SetIfNotSet(runner Action, timeout time.Duration) <-chan error {
	t.mu.Lock()
	armed := t.timer != nil
	current := t.errs
	t.mu.Unlock()
	if armed && current != nil {
		return current.ch
	}
	return t.CancelAndSet(runner, timeout)
}

// Cancel corresponds to TimeoutTimer.cancel.
func (t *TimeoutTimer) Cancel() {
	t.mu.Lock()
	if t.timer == nil {
		t.mu.Unlock()
		return
	}
	t.timer.Stop()
	t.timer = nil
	t.generation++
	errs := t.errs
	t.mu.Unlock()
	if errs != nil {
		errs.settle(ErrCanceled)
	}
}

// Errors returns the result channel of the most recent scheduling.
func (t *TimeoutTimer) Errors() <-chan error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.errs == nil {
		return nil
	}
	return t.errs.ch
}

// Dispose corresponds to TimeoutTimer.dispose.
func (t *TimeoutTimer) Dispose() {
	t.mu.Lock()
	t.disposed = true
	t.mu.Unlock()
	t.Cancel()
}

func (t *TimeoutTimer) fire(generation uint64) {
	t.mu.Lock()
	if t.disposed || t.generation != generation {
		t.mu.Unlock()
		return
	}
	t.timer = nil
	runner, errs := t.runner, t.errs
	t.mu.Unlock()
	errs.settle(invokeAction(runner))
}

// IntervalTimer corresponds to VS Code's IntervalTimer. CancelAndSet returns a
// buffered channel that receives each callback's error (or a recovered panic);
// errors are dropped if the channel is unread and full, so a caller that cares
// must drain it. Each tick re-arms from the previous scheduled time, so a fake
// clock jump delivers one tick per interval.
type IntervalTimer struct {
	mu         sync.Mutex
	clock      Clock
	timer      Timer
	runner     Action
	errs       chan error
	interval   time.Duration
	next       time.Time
	generation uint64
	disposed   bool
}

// NewIntervalTimer constructs an IntervalTimer and arms it immediately.
func NewIntervalTimer(runner Action, interval time.Duration, opts ...Option) *IntervalTimer {
	timer := &IntervalTimer{clock: resolveClock(opts)}
	timer.CancelAndSet(runner, interval)
	return timer
}

// CancelAndSet corresponds to IntervalTimer.cancelAndSet.
func (t *IntervalTimer) CancelAndSet(runner Action, interval time.Duration) <-chan error {
	errs := make(chan error, 1)
	t.mu.Lock()
	if t.disposed {
		t.mu.Unlock()
		errs <- ErrDisposed
		return errs
	}
	if t.timer != nil {
		t.timer.Stop()
	}
	t.generation++
	generation := t.generation
	t.interval = interval
	t.next = t.clock.Now().Add(interval)
	t.runner = runner
	t.errs = errs
	t.timer = t.clock.AfterFunc(interval, func() { t.tick(generation) })
	t.mu.Unlock()
	return errs
}

func (t *IntervalTimer) tick(generation uint64) {
	t.mu.Lock()
	if t.disposed || t.generation != generation {
		t.mu.Unlock()
		return
	}
	t.next = t.next.Add(t.interval)
	delay := max(0, t.next.Sub(t.clock.Now()))
	t.timer = t.clock.AfterFunc(delay, func() { t.tick(generation) })
	runner, errs := t.runner, t.errs
	t.mu.Unlock()

	if err := invokeAction(runner); err != nil {
		select {
		case errs <- err:
		default:
		}
	}
}

// Cancel corresponds to IntervalTimer.cancel.
func (t *IntervalTimer) Cancel() {
	t.mu.Lock()
	if t.timer == nil {
		t.mu.Unlock()
		return
	}
	t.timer.Stop()
	t.timer = nil
	t.generation++
	errs := t.errs
	t.mu.Unlock()
	if errs != nil {
		select {
		case errs <- ErrCanceled:
		default:
		}
	}
}

// Dispose corresponds to IntervalTimer.dispose.
func (t *IntervalTimer) Dispose() {
	t.mu.Lock()
	t.disposed = true
	t.mu.Unlock()
	t.Cancel()
}

// RunOnceScheduler corresponds to VS Code's RunOnceScheduler. Schedule returns
// a channel carrying the runner's error (or a recovered panic); a superseded
// schedule settles with ErrCanceled. Flush is the VS Code flush method and
// returns the runner's error directly.
type RunOnceScheduler struct {
	mu         sync.Mutex
	clock      Clock
	runner     Action
	delay      time.Duration
	timer      Timer
	errs       *errorDelivery
	generation uint64
	disposed   bool
}

// NewRunOnceScheduler constructs a RunOnceScheduler with runner and delay.
func NewRunOnceScheduler(runner Action, delay time.Duration, opts ...Option) *RunOnceScheduler {
	return &RunOnceScheduler{clock: resolveClock(opts), runner: runner, delay: delay}
}

// Schedule corresponds to RunOnceScheduler.schedule. Omitting delay uses the
// constructor's delay, which SetDelay can change.
func (s *RunOnceScheduler) Schedule(delay ...time.Duration) <-chan error {
	errs := newErrorDelivery()
	when := s.delay
	if len(delay) > 0 {
		when = delay[0]
	}

	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		errs.settle(ErrDisposed)
		return errs.ch
	}
	if s.timer != nil {
		s.timer.Stop()
		s.errs.settle(ErrCanceled)
	}
	s.generation++
	generation := s.generation
	s.errs = errs
	s.timer = s.clock.AfterFunc(when, func() { s.fire(generation) })
	s.mu.Unlock()
	return errs.ch
}

// Cancel corresponds to RunOnceScheduler.cancel.
func (s *RunOnceScheduler) Cancel() {
	s.mu.Lock()
	if s.timer == nil {
		s.mu.Unlock()
		return
	}
	s.timer.Stop()
	s.timer = nil
	s.generation++
	errs := s.errs
	s.mu.Unlock()
	errs.settle(ErrCanceled)
}

// IsScheduled corresponds to RunOnceScheduler.isScheduled.
func (s *RunOnceScheduler) IsScheduled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timer != nil
}

// Flush corresponds to RunOnceScheduler.flush: it runs a scheduled runner
// immediately instead of waiting for its timer. Without a pending schedule it
// returns ErrNotTriggered.
func (s *RunOnceScheduler) Flush() error {
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return ErrDisposed
	}
	if s.timer == nil {
		s.mu.Unlock()
		return ErrNotTriggered
	}
	s.timer.Stop()
	s.timer = nil
	s.generation++
	runner, errs := s.runner, s.errs
	s.mu.Unlock()

	err := invokeAction(runner)
	errs.settle(err)
	return err
}

// Delay corresponds to RunOnceScheduler.delay.
func (s *RunOnceScheduler) Delay() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.delay
}

// SetDelay corresponds to the delay setter of RunOnceScheduler.delay.
func (s *RunOnceScheduler) SetDelay(delay time.Duration) {
	s.mu.Lock()
	s.delay = delay
	s.mu.Unlock()
}

// Dispose corresponds to RunOnceScheduler.dispose.
func (s *RunOnceScheduler) Dispose() {
	s.mu.Lock()
	s.disposed = true
	s.mu.Unlock()
	s.Cancel()
}

func (s *RunOnceScheduler) fire(generation uint64) {
	s.mu.Lock()
	if s.disposed || s.generation != generation {
		s.mu.Unlock()
		return
	}
	s.timer = nil
	runner, errs := s.runner, s.errs
	s.mu.Unlock()
	errs.settle(invokeAction(runner))
}
