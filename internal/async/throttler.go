package async

import (
	"context"
	"sync"
)

// Throttler corresponds to VS Code's Throttler: the first queued task runs
// immediately, tasks queued while it runs coalesce to the last one, and that
// last task runs once the active task settles. Every coalesced caller receives
// the same result.
type Throttler[T any] struct {
	mu        sync.Mutex
	lifetime  context.Context
	cancelAll context.CancelFunc
	active    *throttlerActive[T]
	queued    *throttlerQueued[T]
	disposed  bool
}

type throttlerActive[T any] struct {
	task       Task[T]
	ctx        context.Context
	release    context.CancelFunc
	deliveries []*delivery[T]
}

type throttlerQueued[T any] struct {
	task       Task[T]
	ctx        context.Context
	release    context.CancelFunc
	stopWatch  func() bool
	deliveries []*delivery[T]
}

// NewThrottler constructs a Throttler. The clock option is accepted for a
// uniform constructor signature; a Throttler itself has no timers.
func NewThrottler[T any](opts ...Option) *Throttler[T] {
	_ = resolveClock(opts)
	lifetime, cancelAll := context.WithCancel(context.Background())
	return &Throttler[T]{lifetime: lifetime, cancelAll: cancelAll}
}

// Queue corresponds to Throttler.queue. A task runs on a worker goroutine with
// ctx; canceling ctx cancels that task and cancels a queued group before it
// starts. After Dispose the returned channel carries ErrDisposed.
func (t *Throttler[T]) Queue(ctx context.Context, task Task[T]) <-chan Result[T] {
	wait := newDelivery[T]()
	if err := ctx.Err(); err != nil {
		wait.settle(Result[T]{Err: err})
		return wait.ch
	}

	t.mu.Lock()
	if t.disposed {
		t.mu.Unlock()
		wait.settle(Result[T]{Err: ErrDisposed})
		return wait.ch
	}
	if t.active != nil {
		if t.queued == nil {
			t.queued = t.newQueuedLocked(ctx, task)
		} else {
			// VS Code replaces the queued factory; earlier callers still
			// settle with the result of the replacement.
			t.queued.task = task
		}
		t.queued.deliveries = append(t.queued.deliveries, wait)
		t.mu.Unlock()
		return wait.ch
	}

	runCtx, release := withLifetime(ctx, t.lifetime)
	active := &throttlerActive[T]{
		task:       task,
		ctx:        runCtx,
		release:    release,
		deliveries: []*delivery[T]{wait},
	}
	t.active = active
	t.mu.Unlock()
	t.launch(active)
	return wait.ch
}

func (t *Throttler[T]) newQueuedLocked(ctx context.Context, task Task[T]) *throttlerQueued[T] {
	runCtx, release := withLifetime(ctx, t.lifetime)
	queued := &throttlerQueued[T]{task: task, ctx: runCtx, release: release}
	queued.stopWatch = context.AfterFunc(runCtx, func() {
		t.cancelQueued(queued, runCtx.Err())
	})
	return queued
}

func (t *Throttler[T]) launch(active *throttlerActive[T]) {
	go func() {
		result := invokeResult(active.ctx, active.task)
		active.release()
		t.finish(active, result)
	}()
}

func (t *Throttler[T]) finish(active *throttlerActive[T], result Result[T]) {
	t.mu.Lock()
	if t.active == active {
		t.active = nil
	}
	var next *throttlerActive[T]
	var canceled []*delivery[T]
	var cancelErr error
	if queued := t.queued; queued != nil && !t.disposed {
		t.queued = nil
		queued.stopWatch()
		if err := queued.ctx.Err(); err != nil {
			canceled = queued.deliveries
			cancelErr = err
			queued.release()
		} else {
			next = &throttlerActive[T]{
				task:       queued.task,
				ctx:        queued.ctx,
				release:    queued.release,
				deliveries: queued.deliveries,
			}
			t.active = next
		}
	}
	t.mu.Unlock()

	settleAll(active.deliveries, result)
	settleAll(canceled, Result[T]{Err: cancelErr})
	if next != nil {
		t.launch(next)
	}
}

func (t *Throttler[T]) cancelQueued(queued *throttlerQueued[T], err error) {
	t.mu.Lock()
	if t.queued != queued {
		t.mu.Unlock()
		return
	}
	t.queued = nil
	queued.stopWatch()
	queued.release()
	t.mu.Unlock()
	if err == nil {
		err = ErrCanceled
	}
	settleAll(queued.deliveries, Result[T]{Err: err})
}

// Dispose corresponds to Throttler.dispose. The active task's context is
// canceled, queued work is dropped, and later Queue calls fail with ErrDisposed.
func (t *Throttler[T]) Dispose() {
	t.mu.Lock()
	t.disposed = true
	active := t.active
	t.active = nil
	queued := t.queued
	t.queued = nil
	t.mu.Unlock()

	t.cancelAll()
	if active != nil {
		active.release()
	}
	if queued != nil {
		queued.stopWatch()
		queued.release()
		settleAll(queued.deliveries, Result[T]{Err: ErrDisposed})
	}
}

func settleAll[T any](deliveries []*delivery[T], result Result[T]) {
	for _, d := range deliveries {
		d.settle(result)
	}
}
