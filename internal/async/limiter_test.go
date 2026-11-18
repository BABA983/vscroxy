package async

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func limiterTask(clock *FakeClock, delay time.Duration, value int, taskErr error, ready chan<- struct{}) Task[int] {
	return func(ctx context.Context) (int, error) {
		done := make(chan struct{})
		clock.AfterFunc(delay, func() { close(done) })
		ready <- struct{}{}
		select {
		case <-done:
			return value, taskErr
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

// Port of Limiter > assert degree of paralellism.
func TestLimiterDegreeOfParallelism(t *testing.T) {
	limiter := NewLimiter[int](5)
	var active, maximum atomic.Int32
	started := make(chan struct{}, 10)
	release := make(chan struct{})
	task := func(n int) Task[int] {
		return func(context.Context) (int, error) {
			current := active.Add(1)
			for {
				seen := maximum.Load()
				if current <= seen || maximum.CompareAndSwap(seen, current) {
					break
				}
			}
			started <- struct{}{}
			<-release
			active.Add(-1)
			return n, nil
		}
	}
	channels := make([]<-chan Result[int], 0, 10)
	for n := range 10 {
		channels = append(channels, limiter.Queue(t.Context(), task(n)))
	}
	for range 5 {
		waitSignal(t, started)
	}
	if got := maximum.Load(); got != 5 {
		t.Fatalf("maximum parallelism = %d, want 5", got)
	}
	close(release)
	for n, ch := range channels {
		assertValue(t, takeResult(t, ch), n)
	}
}

// Port of Queue > simple.
func TestQueueSimple(t *testing.T) {
	clock := NewFakeClock()
	queue := NewQueue[int](WithClock(clock))
	syncDone := false
	asyncDone := false
	ready := make(chan struct{}, 1)
	first := queue.Queue(t.Context(), func(context.Context) (int, error) {
		syncDone = true
		return 1, nil
	})
	if queue.Size() != 1 {
		t.Fatalf("size after first queue = %d", queue.Size())
	}
	second := queue.Queue(t.Context(), func(ctx context.Context) (int, error) {
		done := make(chan struct{})
		clock.AfterFunc(10*time.Millisecond, func() { close(done) })
		ready <- struct{}{}
		select {
		case <-done:
			asyncDone = true
			return 2, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})
	if queue.Size() != 2 {
		t.Fatalf("size after second queue = %d", queue.Size())
	}
	assertValue(t, takeResult(t, first), 1)
	waitSignal(t, ready)
	clock.Advance(10 * time.Millisecond)
	assertValue(t, takeResult(t, second), 2)
	if queue.Size() != 0 || !syncDone || !asyncDone {
		t.Fatalf("size=%d sync=%v async=%v", queue.Size(), syncDone, asyncDone)
	}
}

// Port of Queue > stop processing on dispose.
func TestQueueStopProcessingOnDispose(t *testing.T) {
	clock := NewFakeClock()
	queue := NewQueue[int](WithClock(clock))
	var work atomic.Int32
	ready := make(chan struct{}, 1)
	task := func(context.Context) (int, error) {
		done := make(chan struct{})
		clock.AfterFunc(0, func() { close(done) })
		ready <- struct{}{}
		<-done
		work.Add(1)
		queue.Dispose()
		return 1, nil
	}
	first := queue.Queue(t.Context(), task)
	second := queue.Queue(t.Context(), task)
	third := queue.Queue(t.Context(), task)
	if queue.Size() != 3 {
		t.Fatalf("size = %d", queue.Size())
	}
	waitSignal(t, ready)
	clock.Advance(0)
	assertValue(t, takeResult(t, first), 1)
	if !errors.Is(takeResult(t, second).Err, ErrDisposed) ||
		!errors.Is(takeResult(t, third).Err, ErrDisposed) {
		t.Fatal("queued work should be released on dispose")
	}
	if work.Load() != 1 {
		t.Fatalf("work = %d", work.Load())
	}
}

// Port of Queue > stop on clear.
func TestQueueStopOnClear(t *testing.T) {
	clock := NewFakeClock()
	queue := NewQueue[int](WithClock(clock))
	var sizeAfterClear atomic.Int32
	var work atomic.Int32
	ready := make(chan struct{}, 2)
	task := func(context.Context) (int, error) {
		done := make(chan struct{})
		clock.AfterFunc(0, func() { close(done) })
		ready <- struct{}{}
		<-done
		work.Add(1)
		queue.Clear()
		sizeAfterClear.Store(int32(queue.Size()))
		return 1, nil
	}
	first := queue.Queue(t.Context(), task)
	second := queue.Queue(t.Context(), task)
	third := queue.Queue(t.Context(), task)
	if queue.Size() != 3 {
		t.Fatalf("size = %d", queue.Size())
	}
	waitSignal(t, ready)
	clock.Advance(0)
	assertValue(t, takeResult(t, first), 1)
	assertCanceled(t, takeResult(t, second).Err)
	assertCanceled(t, takeResult(t, third).Err)
	if sizeAfterClear.Load() != 1 || queue.Size() != 0 {
		t.Fatalf("sizeAfterClear=%d size=%d", sizeAfterClear.Load(), queue.Size())
	}

	again := queue.Queue(t.Context(), task)
	waitSignal(t, ready)
	clock.Advance(0)
	assertValue(t, takeResult(t, again), 1)
	if work.Load() != 2 {
		t.Fatalf("work = %d", work.Load())
	}
}

// Port of Queue > clear and drain (1).
func TestQueueClearAndDrainWithWork(t *testing.T) {
	clock := NewFakeClock()
	queue := NewQueue[int](WithClock(clock))
	drained := queue.OnDrained()
	ready := make(chan struct{}, 1)
	result := queue.Queue(t.Context(), func(context.Context) (int, error) {
		done := make(chan struct{})
		clock.AfterFunc(0, func() { close(done) })
		ready <- struct{}{}
		<-done
		queue.Clear()
		return 1, nil
	})
	waitSignal(t, ready)
	clock.Advance(0)
	assertValue(t, takeResult(t, result), 1)
	waitSignal(t, drained)
}

// Port of Queue > clear and drain (2): no work, no drain.
func TestQueueClearWithoutWorkDoesNotDrain(t *testing.T) {
	queue := NewQueue[int]()
	drained := queue.OnDrained()
	queue.Clear()
	select {
	case <-drained:
		t.Fatal("drain fired without work")
	default:
	}
}

// Port of Queue > drain timing.
func TestQueueDrainTiming(t *testing.T) {
	clock := NewFakeClock()
	queue := NewQueue[int](WithClock(clock))
	var mu sync.Mutex
	logicalTime := 0
	tick := func() int {
		mu.Lock()
		defer mu.Unlock()
		value := logicalTime
		logicalTime++
		return value
	}
	drained := queue.OnDrained()
	var firstTick, secondTick, drainTick atomic.Int32
	first := queue.Queue(t.Context(), func(context.Context) (int, error) {
		firstTick.Store(int32(tick()))
		return 1, nil
	})
	ready := make(chan struct{}, 1)
	second := queue.Queue(t.Context(), func(ctx context.Context) (int, error) {
		done := make(chan struct{})
		clock.AfterFunc(10*time.Millisecond, func() { close(done) })
		ready <- struct{}{}
		select {
		case <-done:
			secondTick.Store(int32(tick()))
			return 2, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})
	waitSignal(t, ready)
	clock.Advance(10 * time.Millisecond)
	assertValue(t, takeResult(t, first), 1)
	assertValue(t, takeResult(t, second), 2)
	waitSignal(t, drained)
	drainTick.Store(int32(tick()))
	if firstTick.Load() != 0 || secondTick.Load() != 1 || drainTick.Load() != 2 {
		t.Fatalf("ticks: first=%d second=%d drain=%d", firstTick.Load(), secondTick.Load(), drainTick.Load())
	}
}

// Port of Queue > drain event is send only once.
func TestQueueDrainFiresOnce(t *testing.T) {
	clock := NewFakeClock()
	queue := NewQueue[int](WithClock(clock))
	idle := queue.WhenIdle()
	var drains atomic.Int32
	drainedOnce := make(chan struct{})
	go func() {
		<-idle
		drains.Add(1)
		close(drainedOnce)
	}()
	channels := make([]<-chan Result[int], 0, 4)
	ready := make(chan struct{}, 4)
	for n := range 4 {
		channels = append(channels, queue.Queue(t.Context(), limiterTask(clock, 0, n+1, nil, ready)))
		waitSignal(t, ready)
		clock.Advance(0)
	}
	for n, ch := range channels {
		assertValue(t, takeResult(t, ch), n+1)
	}
	waitSignal(t, drainedOnce)
	if drains.Load() != 1 {
		t.Fatalf("drain count = %d", drains.Load())
	}
}

// Port of Queue > order is kept.
func TestQueueOrderKept(t *testing.T) {
	clock := NewFakeClock()
	queue := NewQueue[int](WithClock(clock))
	delays := []time.Duration{0, 10 * time.Millisecond, 0, 20 * time.Millisecond, 0}
	ready := make(chan struct{}, 1)
	channels := make([]<-chan Result[int], 0, len(delays))
	for n, delay := range delays {
		channels = append(channels, queue.Queue(t.Context(), limiterTask(clock, delay, n+1, nil, ready)))
		waitSignal(t, ready)
		clock.Advance(delay)
	}
	var order []int
	for _, ch := range channels {
		order = append(order, takeResult(t, ch).Value)
	}
	for i, value := range order {
		if value != i+1 {
			t.Fatalf("order = %v", order)
		}
	}
}

// Port of Queue > errors bubble individually but not cause stop.
func TestQueueErrorsBubbleButDoNotStop(t *testing.T) {
	clock := NewFakeClock()
	queue := NewQueue[int](WithClock(clock))
	ready := make(chan struct{}, 5)
	errorTask := errors.New("error")
	tasks := []struct {
		delay time.Duration
		value int
		err   error
	}{
		{0, 1, nil},
		{10 * time.Millisecond, 2, nil},
		{0, 0, errorTask},
		{20 * time.Millisecond, 4, nil},
		{0, 5, nil},
	}
	channels := make([]<-chan Result[int], 0, len(tasks))
	for _, task := range tasks {
		channels = append(channels, queue.Queue(t.Context(), limiterTask(clock, task.delay, task.value, task.err, ready)))
		waitSignal(t, ready)
		clock.Advance(task.delay)
	}
	assertValue(t, takeResult(t, channels[0]), 1)
	assertValue(t, takeResult(t, channels[1]), 2)
	if !errors.Is(takeResult(t, channels[2]).Err, errorTask) {
		t.Fatal("task 3 should fail")
	}
	assertValue(t, takeResult(t, channels[3]), 4)
	assertValue(t, takeResult(t, channels[4]), 5)
}

// Port of Queue > order is kept (chained).
func TestQueueOrderKeptChained(t *testing.T) {
	clock := NewFakeClock()
	queue := NewQueue[int](WithClock(clock))
	previous := queue.Queue(t.Context(), func(context.Context) (int, error) { return 1, nil })
	assertValue(t, takeResult(t, previous), 1)
	for n := range 4 {
		current := n + 2
		previous = queue.Queue(t.Context(), func(context.Context) (int, error) { return current, nil })
		assertValue(t, takeResult(t, previous), current)
	}
}

// Port of Queue > events: the drained event fires after the final task, not
// after the earlier ones.
func TestQueueDrainedEventTiming(t *testing.T) {
	clock := NewFakeClock()
	queue := NewQueue[int](WithClock(clock))
	drained := queue.OnDrained()
	ready := make(chan struct{}, 3)
	first := queue.Queue(t.Context(), limiterTask(clock, 10*time.Millisecond, 2, nil, ready))
	second := queue.Queue(t.Context(), limiterTask(clock, 20*time.Millisecond, 4, nil, ready))
	third := queue.Queue(t.Context(), limiterTask(clock, 0, 5, nil, ready))
	for _, delay := range []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 0} {
		waitSignal(t, ready)
		clock.Advance(delay)
	}
	assertValue(t, takeResult(t, first), 2)
	assertValue(t, takeResult(t, second), 4)
	assertValue(t, takeResult(t, third), 5)
	select {
	case <-drained:
	default:
		t.Fatal("drain should have fired")
	}
}

func TestLimiterContextCancellation(t *testing.T) {
	clock := NewFakeClock()
	limiter := NewLimiter[int](2, WithClock(clock))
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	active := limiter.Queue(ctx, func(ctx context.Context) (int, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	})
	waitSignal(t, started)
	cancel()
	assertCanceled(t, takeResult(t, active).Err)

	queuedCtx, cancelQueued := context.WithCancel(t.Context())
	var queuedCalls atomic.Int32
	queued := limiter.Queue(queuedCtx, func(context.Context) (int, error) {
		queuedCalls.Add(1)
		return 9, nil
	})
	cancelQueued()
	assertCanceled(t, takeResult(t, queued).Err)
	if queuedCalls.Load() != 0 || limiter.Size() != 0 {
		t.Fatalf("calls=%d size=%d", queuedCalls.Load(), limiter.Size())
	}
}

func TestLimiterRecoversPanic(t *testing.T) {
	limiter := NewLimiter[int](1)
	result := limiter.Queue(t.Context(), func(context.Context) (int, error) {
		panic(fmt.Errorf("limiter boom"))
	})
	assertPanic(t, takeResult(t, result).Err, "limiter boom")
}
