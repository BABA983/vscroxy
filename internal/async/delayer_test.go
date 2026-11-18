package async

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// Port of async.test.ts: Delayer > simple. All triggers settle with the result
// of the last task, as in VS Code.
func TestDelayerSimple(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewDelayer[int](time.Millisecond, WithClock(clock))
	var calls atomic.Int32
	factory := func(context.Context) (int, error) { return int(calls.Add(1)), nil }

	if delayer.IsTriggered() {
		t.Fatal("fresh delayer is triggered")
	}
	first := delayer.Trigger(t.Context(), factory)
	second := delayer.Trigger(t.Context(), factory)
	third := delayer.Trigger(t.Context(), factory)
	if !delayer.IsTriggered() {
		t.Fatal("delayer should be triggered")
	}

	clock.Advance(time.Millisecond)
	assertValue(t, takeResult(t, first), 1)
	assertValue(t, takeResult(t, second), 1)
	assertValue(t, takeResult(t, third), 1)
	if delayer.IsTriggered() || calls.Load() != 1 {
		t.Fatalf("triggered=%v calls=%d", delayer.IsTriggered(), calls.Load())
	}
}

// Port of Delayer > last task should be the one getting called.
func TestDelayerLastTaskWins(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewDelayer[int](0, WithClock(clock))
	trigger := func(n int) <-chan Result[int] {
		return delayer.Trigger(t.Context(), func(context.Context) (int, error) { return n, nil })
	}
	a, b, c := trigger(1), trigger(2), trigger(3)
	clock.Advance(0)
	assertValue(t, takeResult(t, a), 3)
	assertValue(t, takeResult(t, b), 3)
	assertValue(t, takeResult(t, c), 3)
}

// Port of Delayer > simple cancel and cancel should cancel all calls to trigger.
func TestDelayerCancelAllTriggers(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewDelayer[int](0, WithClock(clock))
	var calls atomic.Int32
	factory := func(context.Context) (int, error) { return int(calls.Add(1)), nil }
	channels := []<-chan Result[int]{
		delayer.Trigger(t.Context(), factory),
		delayer.Trigger(t.Context(), factory),
		delayer.Trigger(t.Context(), factory),
	}
	if !delayer.IsTriggered() {
		t.Fatal("delayer should be triggered")
	}
	delayer.Cancel()
	if delayer.IsTriggered() {
		t.Fatal("delayer should not be triggered after cancel")
	}
	for _, ch := range channels {
		assertCanceled(t, takeResult(t, ch).Err)
	}
	clock.Advance(time.Hour)
	if calls.Load() != 0 {
		t.Fatalf("canceled task ran %d times", calls.Load())
	}
}

// Port of Delayer > trigger, cancel, then trigger again.
func TestDelayerTriggerCancelTriggerAgain(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewDelayer[int](0, WithClock(clock))
	var calls atomic.Int32
	factory := func(context.Context) (int, error) { return int(calls.Add(1)), nil }

	first := delayer.Trigger(t.Context(), factory)
	clock.Advance(0)
	assertValue(t, takeResult(t, first), 1)

	canceled := []<-chan Result[int]{
		delayer.Trigger(t.Context(), factory),
		delayer.Trigger(t.Context(), factory),
	}
	delayer.Cancel()
	for _, ch := range canceled {
		assertCanceled(t, takeResult(t, ch).Err)
	}

	again := []<-chan Result[int]{
		delayer.Trigger(t.Context(), factory),
		delayer.Trigger(t.Context(), factory),
	}
	if !delayer.IsTriggered() {
		t.Fatal("delayer should be triggered again")
	}
	clock.Advance(0)
	for _, ch := range again {
		assertValue(t, takeResult(t, ch), 2)
	}
	if delayer.IsTriggered() {
		t.Fatal("delayer still triggered")
	}
}

// Port of the cancel and dispose "releases the pending task" cases.
func TestDelayerCancelAndDisposeReleasePendingTask(t *testing.T) {
	for _, release := range []string{"cancel", "dispose"} {
		t.Run(release, func(t *testing.T) {
			clock := NewFakeClock()
			delayer := NewDelayer[int](0, WithClock(clock))
			var calls atomic.Int32
			pending := delayer.Trigger(t.Context(), func(context.Context) (int, error) {
				return int(calls.Add(1)), nil
			})
			if release == "cancel" {
				delayer.Cancel()
			} else {
				delayer.Dispose()
			}
			assertCanceled(t, takeResult(t, pending).Err)
			clock.Advance(0)
			if calls.Load() != 0 {
				t.Fatalf("pending task ran %d times", calls.Load())
			}
			// VS Code's Delayer.dispose is Cancel, so a later trigger still works.
			again := delayer.Trigger(t.Context(), func(context.Context) (int, error) { return 42, nil })
			clock.Advance(0)
			assertValue(t, takeResult(t, again), 42)
		})
	}
}

// Flush is the shutdown-join addition. It runs a pending task now and settles
// the channel already returned by Trigger.
func TestDelayerFlush(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewDelayer[int](time.Hour, WithClock(clock))
	if _, err := delayer.Flush(); !errors.Is(err, ErrNotTriggered) {
		t.Fatalf("flush without trigger: %v", err)
	}
	triggered := delayer.Trigger(t.Context(), func(context.Context) (int, error) { return 7, nil })
	value, err := delayer.Flush()
	if err != nil || value != 7 {
		t.Fatalf("flush = (%d, %v)", value, err)
	}
	assertValue(t, takeResult(t, triggered), 7)
	if delayer.IsTriggered() {
		t.Fatal("flush did not clear trigger")
	}
	clock.Advance(time.Hour) // the cancelled timer must not run the task again
	if _, err := delayer.Flush(); !errors.Is(err, ErrNotTriggered) {
		t.Fatalf("second flush: %v", err)
	}
}

// Cancellation via ctx is the Go form of VS Code's CancellationToken.
func TestDelayerContextCancellation(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewDelayer[int](time.Millisecond, WithClock(clock))
	ctx, cancel := context.WithCancel(t.Context())
	var calls atomic.Int32
	triggered := delayer.Trigger(ctx, func(context.Context) (int, error) {
		return int(calls.Add(1)), nil
	})
	cancel()
	assertCanceled(t, takeResult(t, triggered).Err)
	clock.Advance(time.Hour)
	if calls.Load() != 0 || delayer.IsTriggered() {
		t.Fatalf("calls=%d triggered=%v", calls.Load(), delayer.IsTriggered())
	}
}

func TestDelayerRecoversPanic(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewDelayer[int](0, WithClock(clock))
	triggered := delayer.Trigger(t.Context(), func(context.Context) (int, error) {
		panic("delayer boom")
	})
	clock.Advance(0)
	assertPanic(t, takeResult(t, triggered).Err, "delayer boom")
	if _, err := delayer.Flush(); !errors.Is(err, ErrNotTriggered) {
		t.Fatalf("flush after panic: %v", err)
	}
}

// Port of ThrottledDelayer > promise should resolve if disposed and
// trigger after dispose throws. Throws become an ErrDisposed settlement.
func TestThrottledDelayerDispose(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewThrottledDelayer[int](time.Millisecond, WithClock(clock))
	pending := delayer.Trigger(t.Context(), func(context.Context) (int, error) { return 1, nil })
	delayer.Dispose()
	assertCanceled(t, takeResult(t, pending).Err)

	after := delayer.Trigger(t.Context(), func(context.Context) (int, error) { return 2, nil })
	if !errors.Is(takeResult(t, after).Err, ErrDisposed) {
		t.Fatal("trigger after dispose should fail")
	}
}

// Port of ThrottledDelayer > cancel releases the pending task.
func TestThrottledDelayerCancelReleasesPendingTask(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewThrottledDelayer[int](0, WithClock(clock))
	var calls atomic.Int32
	canceled := delayer.Trigger(t.Context(), func(context.Context) (int, error) {
		return int(calls.Add(1)), nil
	})
	delayer.Cancel()
	assertCanceled(t, takeResult(t, canceled).Err)
	if delayer.IsTriggered() {
		t.Fatal("delayer still triggered after cancel")
	}

	clock.Advance(0)
	result := delayer.Trigger(t.Context(), func(context.Context) (int, error) { return 42, nil })
	clock.Advance(0)
	assertValue(t, takeResult(t, result), 42)
	if calls.Load() != 0 {
		t.Fatalf("canceled task ran %d times", calls.Load())
	}
}

func TestThrottledDelayerPanic(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewThrottledDelayer[int](0, WithClock(clock))
	triggered := delayer.Trigger(t.Context(), func(context.Context) (int, error) {
		panic(fmt.Errorf("throttled boom"))
	})
	clock.Advance(0)
	assertPanic(t, takeResult(t, triggered).Err, "throttled boom")
}

// A caller's context cancels only its own settlement. The second caller never
// canceled, so the coalesced task still runs with an uncanceled context.
func TestDelayerCallerContextIsScopedToOneCaller(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewDelayer[int](10*time.Millisecond, WithClock(clock))
	canceledCtx, cancel := context.WithCancel(t.Context())
	var calls atomic.Int32
	first := delayer.Trigger(canceledCtx, func(context.Context) (int, error) {
		calls.Add(1)
		return 1, nil
	})
	second := delayer.Trigger(context.Background(), func(ctx context.Context) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		calls.Add(1)
		return 2, nil
	})

	cancel()
	assertCanceled(t, takeResult(t, first).Err)
	clock.Advance(10 * time.Millisecond)
	assertValue(t, takeResult(t, second), 2)
	if calls.Load() != 1 {
		t.Fatalf("task ran %d times, want 1", calls.Load())
	}
}

func TestDelayerAllCallersCancelDropsTask(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewDelayer[int](10*time.Millisecond, WithClock(clock))
	firstCtx, cancelFirst := context.WithCancel(t.Context())
	secondCtx, cancelSecond := context.WithCancel(t.Context())
	var calls atomic.Int32
	task := func(context.Context) (int, error) {
		calls.Add(1)
		return 1, nil
	}
	first := delayer.Trigger(firstCtx, task)
	second := delayer.Trigger(secondCtx, task)

	cancelFirst()
	cancelSecond()
	assertCanceled(t, takeResult(t, first).Err)
	assertCanceled(t, takeResult(t, second).Err)
	clock.Advance(10 * time.Millisecond)
	if calls.Load() != 0 || delayer.IsTriggered() {
		t.Fatalf("calls=%d triggered=%v", calls.Load(), delayer.IsTriggered())
	}
}

func TestThrottledDelayerCallerContextIsScopedToOneCaller(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewThrottledDelayer[int](10*time.Millisecond, WithClock(clock))
	canceledCtx, cancel := context.WithCancel(t.Context())
	var calls atomic.Int32
	task := func(value int) Task[int] {
		return func(ctx context.Context) (int, error) {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			calls.Add(1)
			return value, nil
		}
	}
	first := delayer.Trigger(canceledCtx, task(1))
	second := delayer.Trigger(context.Background(), task(2))

	cancel()
	assertCanceled(t, takeResult(t, first).Err)
	clock.Advance(10 * time.Millisecond)
	assertValue(t, takeResult(t, second), 2)
	if calls.Load() != 1 {
		t.Fatalf("task ran %d times, want 1", calls.Load())
	}
}

func TestThrottledDelayerAllCallersCancelDropsTask(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewThrottledDelayer[int](10*time.Millisecond, WithClock(clock))
	firstCtx, cancelFirst := context.WithCancel(t.Context())
	secondCtx, cancelSecond := context.WithCancel(t.Context())
	var calls atomic.Int32
	task := func(context.Context) (int, error) {
		calls.Add(1)
		return 1, nil
	}
	first := delayer.Trigger(firstCtx, task)
	second := delayer.Trigger(secondCtx, task)

	cancelFirst()
	cancelSecond()
	assertCanceled(t, takeResult(t, first).Err)
	assertCanceled(t, takeResult(t, second).Err)
	clock.Advance(10 * time.Millisecond)

	followUp := delayer.Trigger(t.Context(), func(context.Context) (int, error) { return 9, nil })
	clock.Advance(10 * time.Millisecond)
	assertValue(t, takeResult(t, followUp), 9)
	if calls.Load() != 0 {
		t.Fatalf("abandoned task ran %d times", calls.Load())
	}
}

// The settlement snapshot must stay immutable while a caller's context
// callback leaves the group (the race reported for fire/settleCallers).
func TestDelayerCallerCancellationConcurrentWithSettlement(t *testing.T) {
	for range 50 {
		clock := NewFakeClock()
		delayer := NewDelayer[int](10*time.Millisecond, WithClock(clock))
		firstCtx, cancelFirst := context.WithCancel(context.Background())
		first := delayer.Trigger(firstCtx, func(context.Context) (int, error) { return 1, nil })
		second := delayer.Trigger(context.Background(), func(context.Context) (int, error) { return 2, nil })

		cancelFirst()
		clock.Advance(10 * time.Millisecond)
		assertCanceled(t, takeResult(t, first).Err)
		assertValue(t, takeResult(t, second), 2)
	}
}

// The last caller leaving cancels a task that has already started.
func TestDelayerLastCallerCancelsRunningTask(t *testing.T) {
	clock := NewFakeClock()
	delayer := NewDelayer[int](0, WithClock(clock))
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	started := make(chan struct{})
	task := func(ctx context.Context) (int, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	}
	first := delayer.Trigger(firstCtx, task)
	second := delayer.Trigger(secondCtx, task)
	go func() {
		<-started
		cancelFirst()
		cancelSecond()
	}()
	clock.Advance(0)
	assertCanceled(t, takeResult(t, first).Err)
	assertCanceled(t, takeResult(t, second).Err)
}

// Every coalesced caller of ThrottledDelayer must receive the shared result.
func TestThrottledDelayerCoalescedCallersAllSettle(t *testing.T) {
	for _, count := range []int{2, 5} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			clock := NewFakeClock()
			delayer := NewThrottledDelayer[int](10*time.Millisecond, WithClock(clock))
			var calls atomic.Int32
			channels := make([]<-chan Result[int], 0, count)
			for i := range count {
				channels = append(channels, delayer.Trigger(t.Context(), func(context.Context) (int, error) {
					calls.Add(1)
					return i + 1, nil
				}))
			}

			clock.Advance(10 * time.Millisecond)
			for _, ch := range channels {
				assertValue(t, takeResultWithin(t, ch, 2*time.Second), count)
			}
			if calls.Load() != 1 {
				t.Fatalf("task ran %d times, want 1", calls.Load())
			}
		})
	}
}

// One canceled caller must not drop the results of the others, even when its
// cancellation races with the delayed task's settlement.
func TestThrottledDelayerCoalescedCallersWithConcurrentCancel(t *testing.T) {
	for _, count := range []int{2, 5} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			for range 200 {
				clock := NewFakeClock()
				delayer := NewThrottledDelayer[int](10*time.Millisecond, WithClock(clock))
				firstCtx, cancelFirst := context.WithCancel(context.Background())
				channels := make([]<-chan Result[int], 0, count)
				first := delayer.Trigger(firstCtx, func(context.Context) (int, error) { return 1, nil })
				channels = append(channels, first)
				for i := 1; i < count; i++ {
					channels = append(channels, delayer.Trigger(context.Background(), func(context.Context) (int, error) {
						return i + 1, nil
					}))
				}

				canceled := make(chan struct{})
				go func() {
					cancelFirst()
					close(canceled)
				}()
				waitSignal(t, canceled)
				clock.Advance(10 * time.Millisecond)

				assertCanceled(t, takeResultWithin(t, first, 2*time.Second).Err)
				for _, ch := range channels[1:] {
					assertValue(t, takeResultWithin(t, ch, 2*time.Second), count)
				}
			}
		})
	}
}
