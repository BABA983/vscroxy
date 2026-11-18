package async

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// Port of async.test.ts: sequence > simple. Each value is produced in FIFO
// order even though every task goes to a worker goroutine.
func TestSequencerSimple(t *testing.T) {
	sequencer := NewSequencer[int]()
	channels := make([]<-chan Result[int], 0, 5)
	for n := range 5 {
		channels = append(channels, sequencer.Queue(t.Context(), func(context.Context) (int, error) {
			return n + 1, nil
		}))
	}
	for i, ch := range channels {
		assertValue(t, takeResult(t, ch), i+1)
	}
}

// A failing task must not stop the queue, as in VS Code's Sequencer.
func TestSequencerErrorDoesNotStopQueue(t *testing.T) {
	sequencer := NewSequencer[int]()
	first := sequencer.Queue(t.Context(), func(context.Context) (int, error) {
		return 0, errors.New("failed")
	})
	second := sequencer.Queue(t.Context(), func(context.Context) (int, error) { return 2, nil })
	if takeResult(t, first).Err == nil {
		t.Fatal("first task should fail")
	}
	assertValue(t, takeResult(t, second), 2)
}

func TestSequencerContextCancellation(t *testing.T) {
	sequencer := NewSequencer[int]()
	gate := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	first := sequencer.Queue(t.Context(), func(context.Context) (int, error) {
		<-gate
		return 1, nil
	})
	second := sequencer.Queue(ctx, func(context.Context) (int, error) { return 2, nil })
	cancel()
	assertCanceled(t, takeResult(t, second).Err)
	close(gate)
	assertValue(t, takeResult(t, first), 1)
}

func TestSequencerRecoversPanic(t *testing.T) {
	sequencer := NewSequencer[int]()
	result := sequencer.Queue(t.Context(), func(context.Context) (int, error) {
		panic("sequencer boom")
	})
	assertPanic(t, takeResult(t, result).Err, "sequencer boom")
}

// Port of async.test.ts: SequencerByKey.
func TestSequencerByKeyPort(t *testing.T) {
	sequencer := NewSequencerByKey[string, string]()
	hello := sequencer.Queue(t.Context(), "key1", func(context.Context) (string, error) {
		return "hello", nil
	})
	assertValue(t, takeResult(t, hello), "hello")

	failed := sequencer.Queue(t.Context(), "key2", func(context.Context) (string, error) {
		return "", errors.New("failed")
	})
	result := takeResult(t, failed)
	if result.Err == nil || result.Err.Error() != "failed" {
		t.Fatalf("error = %v", result.Err)
	}

	// A rejected task must not break later tasks on the same key.
	again := sequencer.Queue(t.Context(), "key2", func(context.Context) (string, error) {
		return "hello", nil
	})
	assertValue(t, takeResult(t, again), "hello")
}

// Tasks on different keys are independent; tasks on one key stay ordered.
func TestSequencerByKeyOrdering(t *testing.T) {
	sequencer := NewSequencerByKey[string, int]()
	gate := make(chan struct{})
	sameKeyFirst := sequencer.Queue(t.Context(), "a", func(context.Context) (int, error) {
		<-gate
		return 1, nil
	})
	sameKeySecond := sequencer.Queue(t.Context(), "a", func(context.Context) (int, error) { return 2, nil })
	otherKey := sequencer.Queue(t.Context(), "b", func(context.Context) (int, error) { return 3, nil })

	assertValue(t, takeResult(t, otherKey), 3)
	if _, ok := sequencer.Peek("a"); !ok {
		t.Fatal("key a should have queued work")
	}
	if keys := sequencer.Keys(); len(keys) != 1 || keys[0] != "a" {
		t.Fatalf("keys = %v", keys)
	}
	close(gate)
	assertValue(t, takeResult(t, sameKeyFirst), 1)
	assertValue(t, takeResult(t, sameKeySecond), 2)

	// Peek and Keys report an idle key once its task has settled.
	waitForIdleKey(t, sequencer)
}

func waitForIdleKey(t *testing.T, sequencer *SequencerByKey[string, int]) {
	t.Helper()
	// The completion channel of the last task closes when its cleanup runs.
	// Take a fresh peek after the results were delivered: cleanup happens in a
	// defer in the worker goroutine, so wait for the channel to close.
	ch, ok := sequencer.Peek("a")
	if ok {
		select {
		case <-ch:
		case <-t.Context().Done():
			t.Fatal("timed out waiting for key cleanup")
		}
	}
	if _, ok := sequencer.Peek("a"); ok || len(sequencer.Keys()) != 0 {
		t.Fatal("idle key should not be listed")
	}
}

func TestSequencerByKeyRecoversPanic(t *testing.T) {
	sequencer := NewSequencerByKey[string, int]()
	result := sequencer.Queue(t.Context(), "key", func(context.Context) (int, error) {
		panic(fmt.Errorf("keyed boom"))
	})
	assertPanic(t, takeResult(t, result).Err, "keyed boom")
	after := sequencer.Queue(t.Context(), "key", func(context.Context) (int, error) { return 9, nil })
	assertValue(t, takeResult(t, after), 9)
}
