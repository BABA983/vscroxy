package async

import (
	"context"
	"sync"
	"time"
)

// Delayer corresponds to VS Code's Delayer<T>: it debounces a task so that a
// burst of Trigger calls only runs the most recently supplied task once the
// delay elapses. All triggers share that one settlement, as they do in VS Code.
// Flush is an addition used to join pending work during shutdown. The context
// given to a task is canceled when that task returns; work that outlives the
// task must keep its own context.
type Delayer[T any] struct {
	mu           sync.Mutex
	clock        Clock
	defaultDelay time.Duration
	pending      *delayerPending[T]
	generation   uint64
}

type delayerPending[T any] struct {
	task       Task[T]
	ctx        context.Context
	cancel     context.CancelFunc
	stopCtxs   []func() bool
	timer      Timer
	deliveries []*delivery[T]
	generation uint64
}

// NewDelayer constructs a Delayer with VS Code's defaultDelay in milliseconds.
func NewDelayer[T any](defaultDelay time.Duration, opts ...Option) *Delayer[T] {
	return &Delayer[T]{clock: resolveClock(opts), defaultDelay: defaultDelay}
}

// Trigger corresponds to Delayer.trigger. Without delay the default delay is
// used. Cancelling ctx before the delay elapses cancels the pending task.
func (d *Delayer[T]) Trigger(ctx context.Context, task Task[T], delay ...time.Duration) <-chan Result[T] {
	wait := newDelivery[T]()
	when := d.defaultDelay
	if len(delay) > 0 {
		when = delay[0]
	}

	d.mu.Lock()
	if err := ctx.Err(); err != nil {
		d.mu.Unlock()
		wait.settle(Result[T]{Err: err})
		return wait.ch
	}

	// Like VS Code, only the first trigger creates the shared completion.
	// Later triggers replace the task, restart the timer and each get their own
	// channel settled from that same completion.
	if pending := d.pending; pending != nil {
		pending.timer.Stop()
		pending.task = task
		pending.deliveries = append(pending.deliveries, wait)
		d.generation++
		generation := d.generation
		pending.generation = generation
		pending.stopCtxs = append(pending.stopCtxs, context.AfterFunc(ctx, func() {
			d.cancelPending(generation, ctx.Err())
		}))
		pending.timer = d.clock.AfterFunc(when, func() { d.fire(pending, generation) })
		d.mu.Unlock()
		return wait.ch
	}

	d.generation++
	generation := d.generation
	derived, cancel := context.WithCancel(ctx)
	stopCtx := context.AfterFunc(ctx, func() {
		d.cancelPending(generation, ctx.Err())
	})
	pending := &delayerPending[T]{
		task:       task,
		ctx:        derived,
		cancel:     cancel,
		stopCtxs:   []func() bool{stopCtx},
		deliveries: []*delivery[T]{wait},
		generation: generation,
	}
	pending.timer = d.clock.AfterFunc(when, func() { d.fire(pending, generation) })
	d.pending = pending
	d.mu.Unlock()
	return wait.ch
}

// IsTriggered corresponds to Delayer.isTriggered: it reports whether a task is
// still waiting for its timer, not whether a task is currently running.
func (d *Delayer[T]) IsTriggered() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pending != nil
}

// Cancel corresponds to Delayer.cancel. Every caller waiting on the pending
// task receives ErrCanceled, and the task's context is canceled too.
func (d *Delayer[T]) Cancel() {
	d.mu.Lock()
	pending := d.takePendingLocked()
	d.mu.Unlock()
	if pending != nil {
		pending.cancel()
		settleAll(pending.deliveries, Result[T]{Err: ErrCanceled})
	}
}

// Flush runs a pending task immediately instead of waiting for its timer. It
// returns ErrNotTriggered when no task is pending. Flush settles the same
// channel Trigger returned, so callers waiting on the trigger do not hang.
func (d *Delayer[T]) Flush() (T, error) {
	d.mu.Lock()
	pending := d.takePendingLocked()
	d.mu.Unlock()
	if pending == nil {
		var zero T
		return zero, ErrNotTriggered
	}
	value, err := invoke(pending.ctx, pending.task)
	pending.cancel()
	result := Result[T]{Value: value, Err: err}
	settleAll(pending.deliveries, result)
	return value, err
}

// Dispose corresponds to Delayer.dispose, which is the same as cancel in VS
// Code. Like VS Code, a disposed Delayer accepts new triggers.
func (d *Delayer[T]) Dispose() {
	d.Cancel()
}

func (d *Delayer[T]) fire(pending *delayerPending[T], generation uint64) {
	d.mu.Lock()
	if d.pending != pending || pending.generation != generation {
		d.mu.Unlock()
		return
	}
	d.pending = nil
	d.generation++
	for _, stop := range pending.stopCtxs {
		stop()
	}
	d.mu.Unlock()

	result := invokeResult(pending.ctx, pending.task)
	pending.cancel()
	settleAll(pending.deliveries, result)
}

func (d *Delayer[T]) cancelPending(generation uint64, err error) {
	d.mu.Lock()
	pending := d.pending
	if pending == nil || pending.generation != generation {
		d.mu.Unlock()
		return
	}
	d.pending = nil
	d.generation++
	pending.timer.Stop()
	for _, stop := range pending.stopCtxs {
		stop()
	}
	pending.cancel()
	d.mu.Unlock()
	settleAll(pending.deliveries, Result[T]{Err: err})
}

// takePendingLocked detaches the pending task. The caller must settle its
// delivery after releasing the mutex.
func (d *Delayer[T]) takePendingLocked() *delayerPending[T] {
	pending := d.pending
	if pending == nil {
		return nil
	}
	d.pending = nil
	d.generation++
	pending.timer.Stop()
	for _, stop := range pending.stopCtxs {
		stop()
	}
	return pending
}

// ThrottledDelayer corresponds to VS Code's ThrottledDelayer<T>: a Delayer in
// front of a Throttler, so rapid triggers both debounce and never overlap.
type ThrottledDelayer[T any] struct {
	mu        sync.Mutex
	delayer   *Delayer[<-chan Result[T]]
	throttler *Throttler[T]
	disposed  bool
}

// NewThrottledDelayer constructs a ThrottledDelayer with the given default delay.
func NewThrottledDelayer[T any](defaultDelay time.Duration, opts ...Option) *ThrottledDelayer[T] {
	clock := resolveClock(opts)
	return &ThrottledDelayer[T]{
		delayer:   NewDelayer[<-chan Result[T]](defaultDelay, WithClock(clock)),
		throttler: NewThrottler[T](WithClock(clock)),
	}
}

// Trigger corresponds to ThrottledDelayer.trigger. The returned channel carries
// the result of the throttled task; after Dispose it carries ErrDisposed.
func (td *ThrottledDelayer[T]) Trigger(ctx context.Context, task Task[T], delay ...time.Duration) <-chan Result[T] {
	out := newDelivery[T]()
	td.mu.Lock()
	disposed := td.disposed
	td.mu.Unlock()
	if disposed {
		out.settle(Result[T]{Err: ErrDisposed})
		return out.ch
	}

	// The delayer cancels the context it passes to its task once that task
	// returns. The throttled task outlives it, so it uses the caller's ctx.
	delayed := td.delayer.Trigger(ctx, func(context.Context) (<-chan Result[T], error) {
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
