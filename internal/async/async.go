// Package async ports the timing and concurrency primitives of VS Code's
// src/vs/base/common/async.ts (commit 972b66fdc541e10689adfd90ade9549dea1b18bc)
// to Go.
//
// Type and method names follow VS Code. CancellationToken becomes
// context.Context, Promise<T> becomes Task[T], a func(context.Context) (T,
// error) whose single result is delivered on the channel returned by the
// scheduling method, and IDisposable becomes a Dispose method compatible with
// internal/lifecycle.Disposable. JS-only unions and microtasks are not ported.
//
// Every type is safe for concurrent use. Task callbacks run on worker
// goroutines started by the primitive; timer callbacks run on the goroutine
// that fires the timer (the standard library's timer goroutine with the real
// clock, or the caller of FakeClock.Advance with the fake clock). Callbacks are
// never sent to a UI thread and this package does not import MyGo: callers that
// need to touch the UI must marshal there themselves. Panics in callbacks are
// recovered and returned as errors.
package async

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// ErrCanceled corresponds to VS Code's CancellationError. Cancellation from a
// Cancel call and cancellation from a context both produce this error.
var ErrCanceled = context.Canceled

// ErrDisposed is returned by primitives that refuse work after Dispose.
// VS Code throws "Object has been disposed" in those cases.
var ErrDisposed = errors.New("async: disposed")

// ErrNotTriggered is returned by Flush when there is no pending task.
var ErrNotTriggered = errors.New("async: not triggered")

// Task is the Go form of VS Code's ITask<Promise<T>>: it receives the caller's
// cancellation context and returns the value a promise would have resolved to.
type Task[T any] func(ctx context.Context) (T, error)

// Action is the Go form of a VS Code callback that returns void, such as the
// runner of RunOnceScheduler or TimeoutTimer. Returning an error keeps a
// callback failure observable through the returned error channel.
type Action func() error

// Result is one promise settlement delivered on a returned channel.
type Result[T any] struct {
	Value T
	Err   error
}

// Timer is the stoppable handle returned by Clock.AfterFunc.
type Timer interface {
	Stop() bool
}

// Clock supplies the time source used by every timer in this package. The zero
// option in every constructor is a real clock backed by the standard library.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// Option configures a primitive's time source.
type Option func(*options)

type options struct {
	clock Clock
}

// WithClock injects a Clock. Without it, primitives use a real clock; tests
// use NewFakeClock so no test waits on wall-clock time.
func WithClock(clock Clock) Option {
	return func(o *options) {
		o.clock = clock
	}
}

func resolveClock(opts []Option) Clock {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	if o.clock == nil {
		return realClock{}
	}
	return o.clock
}

// FakeClock is the Go analog of VS Code's runWithFakedTimers: time advances
// only when Advance is called, and due timers fire in deadline order. It is
// safe for concurrent use. Timer callbacks run synchronously on the goroutine
// calling Advance, so a test must have registered its timers before advancing.
type FakeClock struct {
	mu        sync.Mutex
	advanceMu sync.Mutex
	now       time.Time
	seq       uint64
	timers    []*fakeTimer
}

// NewFakeClock returns a clock whose time starts at the Unix epoch.
func NewFakeClock() *FakeClock {
	return &FakeClock{now: time.Unix(0, 0).UTC()}
}

// Now reports the fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// AfterFunc schedules f after d of fake time.
func (c *FakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d < 0 {
		d = 0
	}
	c.seq++
	timer := &fakeTimer{
		clock:    c,
		deadline: c.now.Add(d),
		seq:      c.seq,
		fn:       f,
	}
	c.timers = append(c.timers, timer)
	return timer
}

// Advance moves fake time forward and runs every timer due at or before the
// target, in deadline order. Timers scheduled by those callbacks are included.
func (c *FakeClock) Advance(d time.Duration) {
	if d < 0 {
		panic("async: FakeClock.Advance: negative duration")
	}
	c.advanceMu.Lock()
	defer c.advanceMu.Unlock()
	c.mu.Lock()
	target := c.now.Add(d)
	for {
		next := -1
		for i, timer := range c.timers {
			if timer.deadline.After(target) {
				continue
			}
			if next < 0 || timer.deadline.Before(c.timers[next].deadline) ||
				(timer.deadline.Equal(c.timers[next].deadline) && timer.seq < c.timers[next].seq) {
				next = i
			}
		}
		if next < 0 {
			break
		}
		timer := c.timers[next]
		c.timers = slices.Delete(c.timers, next, next+1)
		if timer.state != fakeTimerPending {
			continue
		}
		timer.state = fakeTimerFired
		c.now = timer.deadline
		c.mu.Unlock()
		timer.fn()
		c.mu.Lock()
	}
	c.now = target
	c.mu.Unlock()
}

type fakeTimerState uint8

const (
	fakeTimerPending fakeTimerState = iota
	fakeTimerFired
	fakeTimerStopped
)

type fakeTimer struct {
	clock    *FakeClock
	deadline time.Time
	seq      uint64
	fn       func()
	state    fakeTimerState
}

// Stop cancels the timer. Like time.Timer.Stop, it reports whether the timer
// had not already fired or been stopped.
func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.state != fakeTimerPending {
		return false
	}
	t.state = fakeTimerStopped
	t.clock.timers = slices.DeleteFunc(t.clock.timers, func(other *fakeTimer) bool {
		return other == t
	})
	return true
}

// caller is one caller's stake in a shared group. A context cancels only this
// caller's delivery; the group's task context is canceled only by the group's
// own Cancel/Dispose or when the last caller leaves.
type caller[T any] struct {
	ctx      context.Context
	delivery *delivery[T]
	stop     func() bool
}

func newCaller[T any](ctx context.Context, onCancel func(*caller[T], error)) *caller[T] {
	c := &caller[T]{ctx: ctx, delivery: newDelivery[T]()}
	c.stop = context.AfterFunc(ctx, func() { onCancel(c, ctx.Err()) })
	return c
}

// callerGroup owns the callers waiting on one shared task. Once the group is
// detached for settlement, leave stops mutating the slice being iterated and
// only tracks whether the last caller left, so the task context still gets
// canceled when a running task's callers all disappear.
type callerGroup[T any] struct {
	callers   []*caller[T]
	remaining int
	settling  bool
}

func (g *callerGroup[T]) add(c *caller[T]) {
	g.callers = append(g.callers, c)
	g.remaining++
}

// detach transfers ownership of the callers slice to the settling goroutine.
func (g *callerGroup[T]) detach() []*caller[T] {
	g.settling = true
	callers := g.callers
	g.callers = nil
	return callers
}

// leave reports whether this was the last caller in the group.
func (g *callerGroup[T]) leave(c *caller[T]) bool {
	if !g.settling {
		g.callers = removeCaller(g.callers, c)
	}
	if g.remaining > 0 {
		g.remaining--
	}
	return g.remaining == 0
}

func settleCallers[T any](callers []*caller[T], result Result[T]) {
	for _, c := range callers {
		c.stop()
		if err := c.ctx.Err(); err != nil {
			c.delivery.settle(Result[T]{Err: err})
			continue
		}
		c.delivery.settle(result)
	}
}

func removeCaller[T any](callers []*caller[T], target *caller[T]) []*caller[T] {
	if i := slices.Index(callers, target); i >= 0 {
		return slices.Delete(callers, i, i+1)
	}
	return callers
}

// delivery is the one-shot channel behind a returned result channel. Several
// callers can share the same underlying promise and each still gets one result.
type delivery[T any] struct {
	once sync.Once
	ch   chan Result[T]
}

func newDelivery[T any]() *delivery[T] {
	return &delivery[T]{ch: make(chan Result[T], 1)}
}

func (d *delivery[T]) settle(result Result[T]) {
	d.once.Do(func() { d.ch <- result })
}

// errorDelivery is the one-shot form used by callbacks that return no value.
type errorDelivery struct {
	once sync.Once
	ch   chan error
}

func newErrorDelivery() *errorDelivery {
	return &errorDelivery{ch: make(chan error, 1)}
}

func (d *errorDelivery) settle(err error) {
	d.once.Do(func() { d.ch <- err })
}

func invokeResult[T any](ctx context.Context, task Task[T]) Result[T] {
	value, err := invoke(ctx, task)
	return Result[T]{Value: value, Err: err}
}

func invoke[T any](ctx context.Context, task Task[T]) (result T, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	defer func() {
		if value := recover(); value != nil {
			err = panicError(value)
		}
	}()
	return task(ctx)
}

func invokeAction(action Action) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = panicError(value)
		}
	}()
	return action()
}

func panicError(value any) error {
	err, ok := value.(error)
	if !ok {
		return fmt.Errorf("async: callback panicked: %v", value)
	}
	return fmt.Errorf("async: callback panicked: %w", err)
}

// withLifetime derives a context from the caller's context that is also
// canceled when the owning primitive is disposed. The returned function stops
// that association and cancels the derived context.
func withLifetime(ctx, lifetime context.Context) (context.Context, context.CancelFunc) {
	derived, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lifetime, cancel)
	return derived, func() {
		stop()
		cancel()
	}
}

var closedChannel = func() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}()
