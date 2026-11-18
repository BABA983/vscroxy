package async

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// takeResult waits only on the test's lifetime channel, never on wall-clock
// time. All scheduling delays in these tests come from a FakeClock.
func takeResult[T any](t *testing.T, ch <-chan Result[T]) Result[T] {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-t.Context().Done():
		t.Fatal("timed out waiting for result")
		var zero Result[T]
		return zero
	}
}

func takeError(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-t.Context().Done():
		t.Fatal("timed out waiting for error")
		return nil
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-t.Context().Done():
		t.Fatal("timed out waiting for signal")
	}
}

func assertValue[T comparable](t *testing.T, result Result[T], want T) {
	t.Helper()
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.Value != want {
		t.Fatalf("got %v, want %v", result.Value, want)
	}
}

func assertCanceled(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got error %v, want cancellation", err)
	}
}

func assertPanic(t *testing.T, err error, message string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), message) {
		t.Fatalf("got error %v, want panic containing %q", err, message)
	}
}

func TestFakeClockAdvanceRunsDueTimersInOrder(t *testing.T) {
	clock := NewFakeClock()
	var order []int
	clock.AfterFunc(20*time.Millisecond, func() { order = append(order, 3) })
	clock.AfterFunc(5*time.Millisecond, func() {
		order = append(order, 2)
		clock.AfterFunc(0, func() { order = append(order, 4) })
	})
	clock.AfterFunc(5*time.Millisecond, func() { order = append(order, 1) })

	clock.Advance(5 * time.Millisecond)
	if !clock.Now().Equal(time.Unix(0, 0).UTC().Add(5 * time.Millisecond)) {
		t.Fatalf("clock now = %v", clock.Now())
	}
	if len(order) != 3 || order[0] != 2 || order[1] != 1 || order[2] != 4 {
		t.Fatalf("order after first advance = %v", order)
	}
	clock.Advance(15 * time.Millisecond)
	if len(order) != 4 || order[3] != 3 {
		t.Fatalf("order after second advance = %v", order)
	}
}

func TestFakeClockStop(t *testing.T) {
	clock := NewFakeClock()
	called := false
	timer := clock.AfterFunc(time.Millisecond, func() { called = true })
	if !timer.Stop() || timer.Stop() {
		t.Fatal("Stop should succeed once")
	}
	clock.Advance(time.Hour)
	if called {
		t.Fatal("stopped timer fired")
	}
}
