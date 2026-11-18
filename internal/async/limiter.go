package async

import (
	"context"
	"sync"
)

// Limiter corresponds to VS Code's Limiter<T>: it runs queued tasks with at
// most maxDegreeOfParalellism in flight, keeping FIFO order. Size, Clear,
// Dispose, onDrained and whenIdle correspond to the VS Code members of the same
// names. Go channels replace the void promise/event: OnDrained returns a
// channel that closes on each drain, and WhenIdle returns a channel that closes
// once no work remains.
type Limiter[T any] struct {
	mu          sync.Mutex
	max         int
	lifetime    context.Context
	cancelAll   context.CancelFunc
	size        int
	running     int
	outstanding []*limiterItem[T]
	drained     chan struct{}
	disposed    bool
}

type limiterItem[T any] struct {
	task     Task[T]
	ctx      context.Context
	release  context.CancelFunc
	delivery *delivery[T]
}

// NewLimiter constructs a Limiter with a positive degree of parallelism.
func NewLimiter[T any](maxDegreeOfParalellism int, opts ...Option) *Limiter[T] {
	_ = resolveClock(opts)
	if maxDegreeOfParalellism <= 0 {
		panic("async: NewLimiter: maxDegreeOfParalellism must be positive")
	}
	lifetime, cancelAll := context.WithCancel(context.Background())
	return &Limiter[T]{
		max:       maxDegreeOfParalellism,
		lifetime:  lifetime,
		cancelAll: cancelAll,
		drained:   make(chan struct{}),
	}
}

// Queue corresponds to Limiter.queue. The task runs on a worker goroutine;
// canceling ctx cancels that task. Queued work dropped by Clear or Dispose
// settles with ErrCanceled or ErrDisposed respectively.
func (l *Limiter[T]) Queue(ctx context.Context, task Task[T]) <-chan Result[T] {
	wait := newDelivery[T]()
	if err := ctx.Err(); err != nil {
		wait.settle(Result[T]{Err: err})
		return wait.ch
	}

	l.mu.Lock()
	if l.disposed {
		l.mu.Unlock()
		wait.settle(Result[T]{Err: ErrDisposed})
		return wait.ch
	}
	runCtx, release := withLifetime(ctx, l.lifetime)
	l.outstanding = append(l.outstanding, &limiterItem[T]{
		task:     task,
		ctx:      runCtx,
		release:  release,
		delivery: wait,
	})
	l.size++
	toStart := l.takeStartableLocked()
	l.mu.Unlock()

	for _, item := range toStart {
		go l.run(item)
	}
	return wait.ch
}

func (l *Limiter[T]) takeStartableLocked() []*limiterItem[T] {
	var items []*limiterItem[T]
	for l.running < l.max && len(l.outstanding) > 0 {
		item := l.outstanding[0]
		l.outstanding[0] = nil
		l.outstanding = l.outstanding[1:]
		l.running++
		items = append(items, item)
	}
	return items
}

func (l *Limiter[T]) run(item *limiterItem[T]) {
	result := invokeResult(item.ctx, item.task)
	item.release()

	l.mu.Lock()
	var toStart []*limiterItem[T]
	if !l.disposed {
		l.running--
		l.size--
		if l.size == 0 {
			close(l.drained)
			l.drained = make(chan struct{})
		}
		toStart = l.takeStartableLocked()
	}
	l.mu.Unlock()

	item.delivery.settle(result)
	for _, next := range toStart {
		go l.run(next)
	}
}

// Size corresponds to Limiter.size: the number of running and queued tasks.
func (l *Limiter[T]) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.size
}

// OnDrained corresponds to Limiter.onDrained. The returned channel closes
// once when the limiter becomes idle; call OnDrained again for the next drain.
func (l *Limiter[T]) OnDrained() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.drained
}

// WhenIdle corresponds to Limiter.whenIdle. It resolves immediately when
// there is no work, otherwise on the next drain.
func (l *Limiter[T]) WhenIdle() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size == 0 {
		return closedChannel
	}
	return l.drained
}

// Clear corresponds to Limiter.clear. Running tasks continue; queued tasks
// settle with ErrCanceled.
func (l *Limiter[T]) Clear() {
	l.mu.Lock()
	dropped := l.takeOutstandingLocked()
	l.size = l.running
	l.mu.Unlock()
	for _, item := range dropped {
		item.release()
		item.delivery.settle(Result[T]{Err: ErrCanceled})
	}
}

func (l *Limiter[T]) takeOutstandingLocked() []*limiterItem[T] {
	dropped := l.outstanding
	l.outstanding = nil
	return dropped
}

// Dispose corresponds to Limiter.dispose. Running tasks have their contexts
// canceled; queued tasks and later Queue calls settle with ErrDisposed.
func (l *Limiter[T]) Dispose() {
	l.mu.Lock()
	if l.disposed {
		l.mu.Unlock()
		return
	}
	l.disposed = true
	dropped := l.takeOutstandingLocked()
	l.size = 0
	close(l.drained)
	l.drained = closedChannel
	l.mu.Unlock()

	l.cancelAll()
	for _, item := range dropped {
		item.release()
		item.delivery.settle(Result[T]{Err: ErrDisposed})
	}
}

// Queue corresponds to VS Code's Queue<T>: a Limiter with a parallelism of one.
type Queue[T any] struct {
	*Limiter[T]
}

// NewQueue constructs a Queue.
func NewQueue[T any](opts ...Option) *Queue[T] {
	return &Queue[T]{Limiter: NewLimiter[T](1, opts...)}
}
