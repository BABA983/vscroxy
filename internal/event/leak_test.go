package event

import "testing"

func TestDisposedSubscriptionsLeaveNoSlots(t *testing.T) {
	var e Emitter[int]
	for range 1000 {
		e.Event(func(int) {}).Dispose()
	}
	if len(e.listeners) != 0 {
		t.Fatalf("%d listener slots remain after 1000 subscribe/dispose cycles", len(e.listeners))
	}
	if e.HasListeners() {
		t.Fatal("an emitter with no listeners reports listeners")
	}
}
