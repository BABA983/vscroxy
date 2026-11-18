package async

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunOnceSchedulerScheduleCancelFlush(t *testing.T) {
	clock := NewFakeClock()
	var calls atomic.Int32
	scheduler := NewRunOnceScheduler(func() error {
		calls.Add(1)
		return nil
	}, 10*time.Millisecond, WithClock(clock))
	if scheduler.IsScheduled() {
		t.Fatal("fresh scheduler is scheduled")
	}
	if scheduler.Delay() != 10*time.Millisecond {
		t.Fatalf("delay = %v", scheduler.Delay())
	}

	first := scheduler.Schedule()
	if !scheduler.IsScheduled() {
		t.Fatal("scheduler should be scheduled")
	}
	clock.Advance(5 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("runner ran early")
	}
	clock.Advance(5 * time.Millisecond)
	if calls.Load() != 1 || scheduler.IsScheduled() {
		t.Fatalf("calls=%d scheduled=%v", calls.Load(), scheduler.IsScheduled())
	}
	if err := takeError(t, first); err != nil {
		t.Fatalf("runner error = %v", err)
	}

	second := scheduler.Schedule()
	scheduler.Cancel()
	assertCanceled(t, takeError(t, second))
	clock.Advance(time.Hour)
	if calls.Load() != 1 {
		t.Fatalf("canceled runner ran %d times", calls.Load())
	}

	third := scheduler.Schedule(2 * time.Millisecond)
	clock.Advance(2 * time.Millisecond)
	if err := takeError(t, third); err != nil || calls.Load() != 2 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}

	scheduler.SetDelay(3 * time.Millisecond)
	fourth := scheduler.Schedule()
	clock.Advance(2 * time.Millisecond)
	if calls.Load() != 2 {
		t.Fatal("runner ignored the new delay")
	}
	clock.Advance(time.Millisecond)
	if err := takeError(t, fourth); err != nil || calls.Load() != 3 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}

	fifth := scheduler.Schedule(time.Hour)
	if err := scheduler.Flush(); err != nil || calls.Load() != 4 {
		t.Fatalf("flush: calls=%d err=%v", calls.Load(), err)
	}
	if err := takeError(t, fifth); err != nil {
		t.Fatalf("flushed schedule error = %v", err)
	}
	if err := scheduler.Flush(); !errors.Is(err, ErrNotTriggered) {
		t.Fatalf("flush without schedule: %v", err)
	}

	scheduler.Dispose()
	if err := takeError(t, scheduler.Schedule()); !errors.Is(err, ErrDisposed) {
		t.Fatalf("schedule after dispose: %v", err)
	}
	if err := scheduler.Flush(); !errors.Is(err, ErrDisposed) {
		t.Fatalf("flush after dispose: %v", err)
	}
}

func TestRunOnceSchedulerSupersedingSchedule(t *testing.T) {
	clock := NewFakeClock()
	var calls atomic.Int32
	scheduler := NewRunOnceScheduler(func() error {
		calls.Add(1)
		return nil
	}, time.Millisecond, WithClock(clock))
	first := scheduler.Schedule()
	second := scheduler.Schedule()
	assertCanceled(t, takeError(t, first))
	clock.Advance(time.Millisecond)
	if err := takeError(t, second); err != nil || calls.Load() != 1 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}

func TestRunOnceSchedulerRecoversPanic(t *testing.T) {
	clock := NewFakeClock()
	scheduler := NewRunOnceScheduler(func() error {
		panic("scheduler boom")
	}, time.Millisecond, WithClock(clock))
	timed := scheduler.Schedule()
	clock.Advance(time.Millisecond)
	assertPanic(t, takeError(t, timed), "scheduler boom")

	flushed := scheduler.Schedule()
	assertPanic(t, scheduler.Flush(), "scheduler boom")
	assertPanic(t, takeError(t, flushed), "scheduler boom")
}

func TestTimeoutTimer(t *testing.T) {
	clock := NewFakeClock()
	var firstCalls, secondCalls atomic.Int32
	timer := NewTimeoutTimer(func() error {
		firstCalls.Add(1)
		return nil
	}, 10*time.Millisecond, WithClock(clock))

	clock.Advance(9 * time.Millisecond)
	if firstCalls.Load() != 0 {
		t.Fatal("runner ran early")
	}
	clock.Advance(time.Millisecond)
	if err := takeError(t, timer.Errors()); err != nil || firstCalls.Load() != 1 {
		t.Fatalf("calls=%d err=%v", firstCalls.Load(), err)
	}

	armed := timer.CancelAndSet(func() error {
		secondCalls.Add(1)
		return nil
	}, 20*time.Millisecond)
	if kept := timer.SetIfNotSet(func() error {
		firstCalls.Add(1)
		return nil
	}, time.Millisecond); kept != armed {
		t.Fatal("SetIfNotSet replaced an armed timer")
	}
	clock.Advance(20 * time.Millisecond)
	if err := takeError(t, armed); err != nil {
		t.Fatalf("armed timer error = %v", err)
	}
	if secondCalls.Load() != 1 || firstCalls.Load() != 1 {
		t.Fatalf("first=%d second=%d", firstCalls.Load(), secondCalls.Load())
	}

	canceled := timer.CancelAndSet(func() error {
		secondCalls.Add(1)
		return nil
	}, 5*time.Millisecond)
	timer.Cancel()
	assertCanceled(t, takeError(t, canceled))
	clock.Advance(time.Hour)
	if secondCalls.Load() != 1 {
		t.Fatalf("canceled runner ran %d times", secondCalls.Load())
	}

	timer.Dispose()
	if err := takeError(t, timer.CancelAndSet(func() error { return nil }, 0)); !errors.Is(err, ErrDisposed) {
		t.Fatalf("CancelAndSet after dispose: %v", err)
	}
}

func TestTimeoutTimerRecoversPanic(t *testing.T) {
	clock := NewFakeClock()
	timer := NewTimeoutTimer(func() error { return nil }, time.Hour, WithClock(clock))
	result := timer.CancelAndSet(func() error {
		panic(fmt.Errorf("timeout boom"))
	}, time.Millisecond)
	clock.Advance(time.Millisecond)
	assertPanic(t, takeError(t, result), "timeout boom")
}

func TestIntervalTimer(t *testing.T) {
	clock := NewFakeClock()
	var ticks atomic.Int32
	runner := func() error {
		ticks.Add(1)
		return nil
	}
	timer := NewIntervalTimer(runner, 5*time.Millisecond, WithClock(clock))
	errs := timer.CancelAndSet(runner, 5*time.Millisecond)

	clock.Advance(14 * time.Millisecond)
	if ticks.Load() != 2 {
		t.Fatalf("ticks after 14ms = %d, want 2", ticks.Load())
	}
	clock.Advance(2 * time.Millisecond)
	if ticks.Load() != 3 {
		t.Fatalf("ticks after 16ms = %d, want 3", ticks.Load())
	}
	timer.Cancel()
	clock.Advance(time.Hour)
	if ticks.Load() != 3 {
		t.Fatalf("ticks after cancel = %d", ticks.Load())
	}
	assertCanceled(t, takeError(t, errs))
}

func TestIntervalTimerCancelAndSetReplacesRunner(t *testing.T) {
	clock := NewFakeClock()
	var oldCalls, newCalls atomic.Int32
	timer := NewIntervalTimer(func() error {
		oldCalls.Add(1)
		return nil
	}, 5*time.Millisecond, WithClock(clock))
	timer.CancelAndSet(func() error {
		newCalls.Add(1)
		return nil
	}, 3*time.Millisecond)
	clock.Advance(9 * time.Millisecond)
	if oldCalls.Load() != 0 || newCalls.Load() != 3 {
		t.Fatalf("old=%d new=%d", oldCalls.Load(), newCalls.Load())
	}
	timer.Dispose()
}

func TestIntervalTimerRecoversPanic(t *testing.T) {
	clock := NewFakeClock()
	timer := NewIntervalTimer(func() error {
		panic("interval boom")
	}, time.Millisecond, WithClock(clock))
	result := timer.CancelAndSet(func() error {
		panic("interval boom")
	}, time.Millisecond)
	clock.Advance(time.Millisecond)
	assertPanic(t, takeError(t, result), "interval boom")
	timer.Dispose()
}
