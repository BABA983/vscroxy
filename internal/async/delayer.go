package async

import (
	"context"
	"sync"
	"time"
)

// Delayer corresponds to VS Code's Delayer<T>: it debounces a task so that a
// burst of Trigger calls only runs the most recently supplied task once the
// delay elapses. All callers in a burst receive that task's result. Flush is an
// addition used to join pending work during shutdown. The context passed to a
// task is canceled when that task returns; work that outlives it needs its own
// context.
type Delayer[T any] struct {
	mu           sync.Mutex
	clock        Clock
	defaultDelay time.Duration
	pending      *delayerPending[T]
}

type delayerPending[T any] struct {
	task    Task[T]
	ctx     context.Context
	cancel  context.CancelFunc
	timer   Timer
	callers []*caller[T]
}

// NewDelayer constructs a Delayer with defaultDelay as its default wait.
func NewDelayer[T any](defaultDelay time.Duration, opts ...Option) *Delayer[T] {
	return &Delayer[T]{clock: resolveClock(opts), defaultDelay: defaultDelay}
}

// Trigger corresponds to Delayer.trigger. Without delay the default wait is
// used. Canceling one caller's ctx settles only that caller with ctx.Err();
// the shared task's context is canceled by Cancel, Dispose, or when the last
// waiting caller's context is canceled.
func (d *Delayer[T]) Trigger(ctx context.Context, task Task[T], delay ...time.Duration) <-chan Result[T] {
	if err := ctx.Err(); err != nil {
		immediate := newDelivery[T]()
		immediate.settle(Result[T]{Err: err})
		return immediate.ch
	}
	when := d.defaultDelay
	if len(delay) > 0 {
		when = delay[0]
	}

	d.mu.Lock()
	pending := d.pending
	if pending == nil {
		shared, cancel := context.WithCancel(context.Background())
		pending = &delayerPending[T]{ctx: shared, cancel: cancel}
		d.pending = pending
	}
	pending.task = task
	if pending.timer != nil {
		pending.timer.Stop()
	}
	pending.timer = d.clock.AfterFunc(when, func() { d.fire(pending) })
	waiting := newCaller(ctx, func(c *caller[T], err error) {
		d.cancelCaller(pending, c, err)
	})
	pending.callers = append(pending.callers, waiting)
	d.mu.Unlock()
	return waiting.delivery.ch
}

// IsTriggered corresponds to Delayer.isTriggered: it reports whether a task is
// still waiting for its timer, not whether a task is currently running.
func (d *Delayer[T]) IsTriggered() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pending != nil
}

// Cancel corresponds to Delayer.cancel. Every waiting caller receives
// ErrCanceled, the timer is stopped, and the task's context is canceled.
func (d *Delayer[T]) Cancel() {
	d.mu.Lock()
	pending := d.pending
	if pending == nil {
		d.mu.Unlock()
		return
	}
	d.pending = nil
	if pending.timer != nil {
		pending.timer.Stop()
		pending.timer = nil
	}
	callers := pending.callers
	pending.cancel()
	d.mu.Unlock()
	settleCallers(callers, Result[T]{Err: ErrCanceled})
}

// Flush runs a pending task immediately instead of waiting for its timer. The
// task runs synchronously on the calling goroutine, and every waiting caller
// receives its result. Without a pending task Flush returns ErrNotTriggered.
func (d *Delayer[T]) Flush() (T, error) {
	d.mu.Lock()
	pending := d.pending
	if pending == nil {
		d.mu.Unlock()
		var zero T
		return zero, ErrNotTriggered
	}
	d.pending = nil
	if pending.timer != nil {
		pending.timer.Stop()
		pending.timer = nil
	}
	task, ctx, callers := pending.task, pending.ctx, pending.callers
	d.mu.Unlock()

	value, err := invoke(ctx, task)
	settleCallers(callers, Result[T]{Value: value, Err: err})
	return value, err
}

// Dispose corresponds to Delayer.dispose, which is the same as cancel in VS
// Code. Like VS Code, a disposed Delayer accepts new triggers.
func (d *Delayer[T]) Dispose() {
	d.Cancel()
}

func (d *Delayer[T]) fire(pending *delayerPending[T]) {
	d.mu.Lock()
	if d.pending != pending || pending.timer == nil {
		d.mu.Unlock()
		return
	}
	pending.timer = nil
	d.pending = nil
	task, ctx, callers := pending.task, pending.ctx, pending.callers
	d.mu.Unlock()

	result := invokeResult(ctx, task)
	settleCallers(callers, result)
}

func (d *Delayer[T]) cancelCaller(pending *delayerPending[T], c *caller[T], err error) {
	d.mu.Lock()
	pending.callers = removeCaller(pending.callers, c)
	if len(pending.callers) == 0 {
		if d.pending == pending {
			d.pending = nil
			if pending.timer != nil {
				pending.timer.Stop()
				pending.timer = nil
			}
		}
		pending.cancel()
	}
	d.mu.Unlock()
	c.delivery.settle(Result[T]{Err: err})
}

// ThrottledDelayer corresponds to VS Code's ThrottledDelayer<T>: a Delayer in
// front of a Throttler, so rapid triggers both debounce and never overlap.
type ThrottledDelayer[T any] struct {
	mu        sync.Mutex
	delayer   *Delayer[<-chan Result[T]]
	throttler *Throttler[T]
	disposed  bool
}

// NewThrottledDelayer constructs a ThrottledDelayer with the given default wait.
func NewThrottledDelayer[T any](defaultDelay time.Duration, opts ...Option) *ThrottledDelayer[T] {
	clock := resolveClock(opts)
	return &ThrottledDelayer[T]{
		delayer:   NewDelayer[<-chan Result[T]](defaultDelay, WithClock(clock)),
		throttler: NewThrottler[T](),
	}
}

// Trigger corresponds to ThrottledDelayer.trigger. The returned channel
// carries the result of the throttled task; after Dispose it carries
// ErrDisposed. Each caller's context cancels only that caller; the delayed task
// uses the Delayer's shared context, which survives until every caller leaves.
func (td *ThrottledDelayer[T]) Trigger(ctx context.Context, task Task[T], delay ...time.Duration) <-chan Result[T] {
	out := newDelivery[T]()
	td.mu.Lock()
	disposed := td.disposed
	td.mu.Unlock()
	if disposed {
		out.settle(Result[T]{Err: ErrDisposed})
		return out.ch
	}

	delayed := td.delayer.Trigger(ctx, func(ctx context.Context) (<-chan Result[T], error) {
		return td.throttler.Queue(ctx, task), nil
	}, delay...)
	go func() {
		settled := <-delayed
		if settled.Err != nil {
			out.settle(Result[T]{Err: settled.Err})
			return
		}
		out.settle(<-settled.Value)
	}()
	return out.ch
}

// IsTriggered, Cancel and Dispose correspond to their VS Code counterparts.
func (td *ThrottledDelayer[T]) IsTriggered() bool { return td.delayer.IsTriggered() }

func (td *ThrottledDelayer[T]) Cancel() { td.delayer.Cancel() }

func (td *ThrottledDelayer[T]) Dispose() {
	td.mu.Lock()
	td.disposed = true
	td.mu.Unlock()
	td.delayer.Dispose()
	td.throttler.Dispose()
}
