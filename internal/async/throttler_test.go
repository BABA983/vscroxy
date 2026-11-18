package async

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func delayedTask(clock *FakeClock, delay time.Duration, value int, ready chan<- struct{}) Task[int] {
	return func(ctx context.Context) (int, error) {
		done := make(chan struct{})
		clock.AfterFunc(delay, func() { close(done) })
		ready <- struct{}{}
		select {
		case <-done:
			return value, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

// Port of Throttler > non async.
func TestThrottlerNonAsync(t *testing.T) {
	clock := NewFakeClock()
	throttler := NewThrottler[int](WithClock(clock))
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	factory := func() Task[int] {
		return func(context.Context) (int, error) {
			n := int(calls.Add(1))
			started <- struct{}{}
			<-release
			return n, nil
		}
	}

	channels := []<-chan Result[int]{
		throttler.Queue(t.Context(), factory()),
		throttler.Queue(t.Context(), factory()),
		throttler.Queue(t.Context(), factory()),
		throttler.Queue(t.Context(), factory()),
		throttler.Queue(t.Context(), factory()),
	}
	waitSignal(t, started) // the first task is active
	close(release)
	for i, want := range []int{1, 2, 2, 2, 2} {
		assertValue(t, takeResult(t, channels[i]), want)
	}
	if calls.Load() != 2 {
		t.Fatalf("factory called %d times, want 2", calls.Load())
	}
}

// Port of Throttler > async.
func TestThrottlerAsync(t *testing.T) {
	clock := NewFakeClock()
	throttler := NewThrottler[int](WithClock(clock))
	factory := func(n int, ready chan struct{}) Task[int] { return delayedTask(clock, 0, n, ready) }
	expectAll := func(values ...int) {
		t.Helper()
		ready := make(chan struct{}, len(values)+1)
		channels := make([]<-chan Result[int], len(values))
		for i, n := range values {
			channels[i] = throttler.Queue(t.Context(), factory(n, ready))
		}
		waitSignal(t, ready) // active task registered its timer
		clock.Advance(0)
		waitSignal(t, ready) // queued task registered its timer
		clock.Advance(0)
		for i, want := range values {
			assertValue(t, takeResult(t, channels[i]), want)
		}
	}
	expectAll(1, 2, 2, 2, 2)
	expectAll(3, 4, 4, 4, 4)
}

// Port of Throttler > last factory should be the one getting called.
func TestThrottlerLastFactoryWins(t *testing.T) {
	clock := NewFakeClock()
	throttler := NewThrottler[int](WithClock(clock))
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	active := func(context.Context) (int, error) {
		started <- struct{}{}
		<-release
		return 1, nil
	}
	queued := func(n int) Task[int] {
		return func(context.Context) (int, error) { return n, nil }
	}
	first := throttler.Queue(t.Context(), active)
	second := throttler.Queue(t.Context(), queued(2))
	third := throttler.Queue(t.Context(), queued(3))
	waitSignal(t, started)
	close(release)

	assertValue(t, takeResult(t, first), 1)
	assertValue(t, takeResult(t, second), 3)
	assertValue(t, takeResult(t, third), 3)
}

// Port of Throttler > propagates queued errors after the active task fails or
// succeeds. Both queued callers get the replacement task's error, and the
// throttler recovers afterwards.
func TestThrottlerPropagatesQueuedErrors(t *testing.T) {
	for _, activeRejects := range []bool{false, true} {
		name := "success"
		if activeRejects {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			throttler := NewThrottler[int]()
			type settlement struct {
				value int
				err   error
			}
			gate := make(chan settlement, 1)
			var queuedCalls atomic.Int32
			queuedError := errors.New("queued task failed")
			queued := func(context.Context) (int, error) {
				queuedCalls.Add(1)
				return 0, queuedError
			}
			active := throttler.Queue(t.Context(), func(context.Context) (int, error) {
				settled := <-gate
				return settled.value, settled.err
			})
			first := throttler.Queue(t.Context(), queued)
			second := throttler.Queue(t.Context(), queued)
			activeError := errors.New("active task failed")
			if activeRejects {
				gate <- settlement{err: activeError}
			} else {
				gate <- settlement{value: 1}
			}

			activeResult := takeResult(t, active)
			if activeRejects {
				if !errors.Is(activeResult.Err, activeError) {
					t.Fatalf("active error = %v", activeResult.Err)
				}
			} else {
				assertValue(t, activeResult, 1)
			}
			if got := takeResult(t, first).Err; !errors.Is(got, queuedError) {
				t.Fatalf("first queued error = %v", got)
			}
			if got := takeResult(t, second).Err; !errors.Is(got, queuedError) {
				t.Fatalf("second queued error = %v", got)
			}
			if queuedCalls.Load() != 1 {
				t.Fatalf("queued factory called %d times", queuedCalls.Load())
			}
			recovered := throttler.Queue(t.Context(), func(context.Context) (int, error) { return 2, nil })
			assertValue(t, takeResult(t, recovered), 2)
		})
	}
}

// Port of Throttler > disposal after queueing and disposal releases the queued
// factory.
func TestThrottlerDisposeAfterQueueing(t *testing.T) {
	throttler := NewThrottler[int]()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	factory := func(context.Context) (int, error) {
		calls.Add(1)
		close(started)
		<-release
		return 1, nil
	}
	active := throttler.Queue(t.Context(), factory)
	queued := throttler.Queue(t.Context(), func(context.Context) (int, error) {
		calls.Add(1)
		return 2, nil
	})
	waitSignal(t, started)
	throttler.Dispose()
	close(release)

	assertValue(t, takeResult(t, active), 1)
	if !errors.Is(takeResult(t, queued).Err, ErrDisposed) {
		t.Fatal("queued task should be released on dispose")
	}
	if calls.Load() != 1 {
		t.Fatalf("factory called %d times", calls.Load())
	}
}

// Port of Throttler > disposal before queueing.
func TestThrottlerDisposeBeforeQueueing(t *testing.T) {
	throttler := NewThrottler[int]()
	var calls atomic.Int32
	throttler.Dispose()
	result := throttler.Queue(t.Context(), func(context.Context) (int, error) {
		calls.Add(1)
		return 1, nil
	})
	if !errors.Is(takeResult(t, result).Err, ErrDisposed) {
		t.Fatal("queue after dispose should fail")
	}
	if calls.Load() != 0 {
		t.Fatalf("factory called %d times", calls.Load())
	}
}

// Context cancellation, the Go form of cancelling a CancellationToken.
func TestThrottlerContextCancellation(t *testing.T) {
	throttler := NewThrottler[int]()
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	active := throttler.Queue(ctx, func(ctx context.Context) (int, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	})
	waitSignal(t, started)
	cancel()
	assertCanceled(t, takeResult(t, active).Err)

	var queuedCalls atomic.Int32
	queued := throttler.Queue(t.Context(), func(context.Context) (int, error) {
		queuedCalls.Add(1)
		return 2, nil
	})
	assertValue(t, takeResult(t, queued), 2)
	if queuedCalls.Load() != 1 {
		t.Fatalf("queued task called %d times", queuedCalls.Load())
	}
}

func TestThrottlerQueuedContextCancellation(t *testing.T) {
	throttler := NewThrottler[int]()
	started := make(chan struct{})
	release := make(chan struct{})
	active := throttler.Queue(t.Context(), func(context.Context) (int, error) {
		close(started)
		<-release
		return 1, nil
	})
	waitSignal(t, started)

	ctx, cancel := context.WithCancel(t.Context())
	var queuedCalls atomic.Int32
	queued := throttler.Queue(ctx, func(context.Context) (int, error) {
		queuedCalls.Add(1)
		return 2, nil
	})
	cancel()
	assertCanceled(t, takeResult(t, queued).Err)
	close(release)
	assertValue(t, takeResult(t, active), 1)
	if queuedCalls.Load() != 0 {
		t.Fatalf("canceled queued task ran %d times", queuedCalls.Load())
	}
}

func TestThrottlerRecoversPanic(t *testing.T) {
	throttler := NewThrottler[int]()
	result := throttler.Queue(t.Context(), func(context.Context) (int, error) {
		panic(fmt.Errorf("throttler boom"))
	})
	assertPanic(t, takeResult(t, result).Err, "throttler boom")
}
