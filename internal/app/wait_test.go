package app

import (
	"testing"
	"time"
)

// watchApp installs the app's test signal before any worker starts. Production
// sends on it only when a test has done this.
func watchApp(a *app) {
	if a.changed == nil {
		a.changed = make(chan struct{}, 1)
	}
}

// awaitChan waits for a value the worker actually sends. The timer is only a
// safety net for a worker that never replies.
func awaitChan[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case v := <-ch:
		return v
	case <-timer.C:
		t.Fatal(what)
		var zero T
		return zero
	}
}

// waitSignal waits for a signal from a worker, rechecking a condition on each
// one. An optional done channel also wakes the wait when a session ends.
func waitSignal(t *testing.T, changed, done <-chan struct{}, check func() bool, what func() string) {
	t.Helper()
	if changed == nil {
		if check() {
			return
		}
		t.Fatal("the worker has no change signal")
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		if check() {
			return
		}
		select {
		case <-changed:
		case <-done:
		case <-timer.C:
			t.Fatal(what())
		}
	}
}

// waitAppSignal rechecks a condition only when the app signals a state change.
// settle draws the frame that moves worker results into the state done() reads.
// The timer guards against a worker that never signals; it is not the wait.
func waitAppSignal(t *testing.T, a *app, done func() bool, settle func(), what func() string) {
	t.Helper()
	watchApp(a)
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for !done() {
		select {
		case <-a.changed:
			if settle != nil {
				settle()
			}
		case <-timer.C:
			t.Fatal(what())
		}
	}
}
