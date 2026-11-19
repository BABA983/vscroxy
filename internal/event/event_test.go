package event_test

import (
	"slices"
	"testing"

	"github.com/baba983/vscroxy/internal/event"
	"github.com/baba983/vscroxy/internal/lifecycle"
)

func TestEmitterDeliversAndUnsubscribes(t *testing.T) {
	var e event.Emitter[int]
	store := lifecycle.NewDisposableStore()
	var got []int
	store.Add(e.Event(func(n int) { got = append(got, n) }))
	e.Fire(1)
	// A listener can drop itself while another listener still runs.
	var self lifecycle.Disposable
	self = e.Event(func(n int) {
		got = append(got, n*10)
		self.Dispose()
	})
	store.Add(self)
	store.Add(e.Event(func(n int) { got = append(got, n*100) }))
	e.Fire(2)
	e.Fire(3)
	if !slices.Equal(got, []int{1, 2, 20, 200, 3, 300}) {
		t.Fatalf("delivered = %v", got)
	}
	if !e.HasListeners() {
		t.Fatal("two subscriptions should remain")
	}
	store.Dispose()
	if e.HasListeners() {
		t.Fatal("disposing the store should release every listener")
	}
	e.Fire(4)
	if !slices.Equal(got, []int{1, 2, 20, 200, 3, 300}) {
		t.Fatalf("disposed listeners still ran: %v", got)
	}
}

func TestEmitterDispose(t *testing.T) {
	var e event.Emitter[string]
	called := false
	subscription := e.Event(func(string) { called = true })
	e.Dispose()
	subscription.Dispose()
	e.Fire("too late")
	if called {
		t.Fatal("a disposed emitter fired")
	}
	late := e.Event(func(string) { called = true })
	late.Dispose()
	e.Fire("still too late")
	if called {
		t.Fatal("a listener registered after dispose ran")
	}
}
