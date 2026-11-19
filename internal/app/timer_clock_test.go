package app

import (
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/async"
)

type clockProbe struct {
	*async.FakeClock
	armed chan time.Duration
}

func (c *clockProbe) AfterFunc(d time.Duration, f func()) async.Timer {
	select {
	case c.armed <- d:
	default:
	}
	return c.FakeClock.AfterFunc(d, f)
}

// TestTickRateUsesAppClock drives the recording ticker with a fake clock: no
// wall clock passes before the callback runs and arms the next tick.
func TestTickRateUsesAppClock(t *testing.T) {
	a := workspaceApp()
	clock := &clockProbe{FakeClock: async.NewFakeClock(), armed: make(chan time.Duration, 2)}
	a.clock = clock
	a.lastFlow.Store(-int64(24 * time.Hour / time.Second)) // no UI redraw from this tick

	done := make(chan struct{})
	finished := make(chan struct{})
	go guarded(func() {
		defer close(finished)
		a.tickRate(done)
	})

	for range 2 {
		clock.Advance(time.Second)
		select {
		case d := <-clock.armed:
			if d != time.Second {
				t.Fatalf("tick interval = %v, want 1s", d)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the injected clock did not arm the ticker")
		}
	}
	close(done)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("tickRate did not stop")
	}
}
