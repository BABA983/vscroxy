package lifecycle

import (
	"sync"
	"sync/atomic"
	"testing"
)

type disposeCounter struct {
	n atomic.Int32
}

func (c *disposeCounter) Dispose() { c.n.Add(1) }

func (c *disposeCounter) calls() int32 { return c.n.Load() }

func TestToDisposableRunsOnce(t *testing.T) {
	calls := 0
	d := ToDisposable(func() { calls++ })
	d.Dispose()
	d.Dispose()
	if calls != 1 {
		t.Fatalf("fn ran %d times, want 1", calls)
	}
	ToDisposable(nil).Dispose()
}

func TestDisposableStoreDisposesInRegistrationOrder(t *testing.T) {
	var (
		mu    sync.Mutex
		order []int
	)
	store := NewDisposableStore()
	for i := range 3 {
		store.Add(ToDisposable(func() {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, i)
		}))
	}
	store.Dispose()
	store.Dispose()
	if got := len(order); got != 3 || order[0] != 0 || order[1] != 1 || order[2] != 2 {
		t.Fatalf("dispose order = %v, want [0 1 2]", order)
	}
	if !store.IsDisposed() {
		t.Fatal("store should be disposed")
	}
}

func TestDisposableStoreDeduplicatesAndDisposesLateAdd(t *testing.T) {
	store := NewDisposableStore()
	first := &disposeCounter{}
	store.Add(first)
	store.Add(first)
	store.Dispose()
	if got := first.calls(); got != 1 {
		t.Fatalf("first disposed %d times, want 1", got)
	}

	late := &disposeCounter{}
	store.Add(late)
	if got := late.calls(); got != 1 {
		t.Fatalf("late disposable disposed %d times, want 1", got)
	}
}

func TestDisposableStoreClearKeepsItUsable(t *testing.T) {
	store := NewDisposableStore()
	first := &disposeCounter{}
	second := &disposeCounter{}
	store.Add(first)
	store.Clear()
	if got := first.calls(); got != 1 {
		t.Fatalf("first disposed %d times, want 1", got)
	}
	if store.IsDisposed() {
		t.Fatal("Clear must not close the store")
	}
	store.Add(second)
	store.Dispose()
	if got := second.calls(); got != 1 {
		t.Fatalf("second disposed %d times, want 1", got)
	}
}

func TestMutableDisposableFollowsVSCodeSetBehavior(t *testing.T) {
	var m MutableDisposable
	first, second := &disposeCounter{}, &disposeCounter{}
	m.Set(first)
	m.Set(second)
	m.Set(second)
	if got := first.calls(); got != 1 {
		t.Fatalf("first disposed %d times, want 1", got)
	}
	if got := second.calls(); got != 0 {
		t.Fatalf("second disposed %d times after setting it again, want 0", got)
	}
	if m.Value() != second {
		t.Fatal("Value should return the current disposable")
	}

	m.Dispose()
	m.Dispose()
	if got := second.calls(); got != 1 {
		t.Fatalf("second disposed %d times, want 1", got)
	}
	late := &disposeCounter{}
	m.Set(late)
	if got := late.calls(); got != 0 {
		t.Fatalf("a set on a disposed holder disposed the value %d times, want 0", got)
	}
	if m.Value() != nil {
		t.Fatal("Value should be nil after Dispose")
	}
}

func TestDisposableMapReleasesReplacedKeys(t *testing.T) {
	var m DisposableMap[string]
	first, second := &disposeCounter{}, &disposeCounter{}
	m.Set("k", first)
	m.Set("k", second)
	if got := first.calls(); got != 1 {
		t.Fatalf("first disposed %d times, want 1", got)
	}
	if got, ok := m.Get("k"); !ok || got != second {
		t.Fatalf("Get(k) = %v, %v, want second, true", got, ok)
	}

	m.DeleteAndDispose("k")
	if got := second.calls(); got != 1 {
		t.Fatalf("second disposed %d times, want 1", got)
	}
	if _, ok := m.Get("k"); ok {
		t.Fatal("key should be gone")
	}

	third := &disposeCounter{}
	m.Set("k", third)
	m.Dispose()
	if got := third.calls(); got != 1 {
		t.Fatalf("third disposed %d times, want 1", got)
	}
	late := &disposeCounter{}
	m.Set("k", late)
	if got := late.calls(); got != 1 {
		t.Fatalf("late disposable disposed %d times, want 1", got)
	}
}
