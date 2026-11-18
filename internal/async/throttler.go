package async

import (
	"context"
	"sync"
)

// Throttler corresponds to VS Code's Throttler: the first queued task runs
// immediately, tasks queued while it runs coalesce to the last one, and that
// last task runs once the active task settles. Every coalesced caller receives
// the same result. Canceling one caller's context settles only that caller;
// the shared task's context is canceled by Dispose or by the last caller
// leaving the group.
type Throttler[T any] struct {
	mu        sync.Mutex
	lifetime  context.Context
	cancelAll context.CancelFunc
	active    *throttlerGroup[T]
	queued    *throttlerGroup[T]
	disposed  bool
}

type throttlerGroup[T any] struct {
	task    Task[T]
	ctx     context.Context
	release context.CancelFunc
	callers []*caller[T]
}

// NewThrottler constructs a Throttler.
func NewThrottler[T any]() *Throttler[T] {
	lifetime, cancelAll := context.WithCancel(context.Background())
	return &Throttler[T]{lifetime: lifetime, cancelAll: cancelAll}
}

// Queue corresponds to Throttler.queue. A task runs on a worker goroutine.
// After Dispose the returned channel carries ErrDisposed.
func (t *Throttler[T]) Queue(ctx context.Context, task Task[T]) <-chan Result[T] {
	if err := ctx.Err(); err != nil {
		immediate := newDelivery[T]()
		immediate.settle(Result[T]{Err: err})
		return immediate.ch
	}

	t.mu.Lock()
	if t.disposed {
		t.mu.Unlock()
		immediate := newDelivery[T]()
		immediate.settle(Result[T]{Err: ErrDisposed})
		return immediate.ch
	}

	var group, toLaunch *throttlerGroup[T]
	if t.active == nil {
		group = t.newGroup(task)
		t.active = group
		toLaunch = group
	} else {
		if t.queued == nil {
			t.queued = t.newGroup(task)
		} else {
			// VS Code replaces the queued factory; all queued callers still
			// settle with the replacement's result.
			t.queued.task = task
		}
		group = t.queued
	}
	waiting := newCaller(ctx, func(c *caller[T], err error) {
		t.cancelCaller(group, c, err)
	})
	group.callers = append(group.callers, waiting)
	t.mu.Unlock()

	if toLaunch != nil {
		t.launch(toLaunch)
	}
	return waiting.delivery.ch
}

func (t *Throttler[T]) newGroup(task Task[T]) *throttlerGroup[T] {
	// No caller's context is the parent: a group lives until it is disposed,
	// dropped, or all of its callers cancel.
	shared, cancel := context.WithCancel(context.Background())
	return &throttlerGroup[T]{task: task, ctx: shared, release: cancel}
}

func (t *Throttler[T]) launch(group *throttlerGroup[T]) {
	go func() {
		result := invokeResult(group.ctx, group.task)
		t.finish(group, result)
	}()
}

func (t *Throttler[T]) finish(group *throttlerGroup[T], result Result[T]) {
	t.mu.Lock()
	if t.active == group {
		t.active = nil
	}
	var next *throttlerGroup[T]
	if queued := t.queued; queued != nil && !t.disposed {
		t.queued = nil
		t.active = queued
		next = queued
	}
	t.mu.Unlock()

	settleCallers(group.callers, result)
	if next != nil {
		t.launch(next)
	}
}

func (t *Throttler[T]) cancelCaller(group *throttlerGroup[T], c *caller[T], err error) {
	t.mu.Lock()
	group.callers = removeCaller(group.callers, c)
	if len(group.callers) == 0 {
		// An abandoned queued group never runs. An abandoned active group
		// keeps its slot until its worker settles, so later calls still queue.
		if t.queued == group {
			t.queued = nil
		}
		group.release()
	}
	t.mu.Unlock()
	c.delivery.settle(Result[T]{Err: err})
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
		queued.release()
		settleCallers(queued.callers, Result[T]{Err: ErrDisposed})
	}
}
