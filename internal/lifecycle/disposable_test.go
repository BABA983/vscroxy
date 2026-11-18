package lifecycle

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type disposeCounter struct {
	n atomic.Int32
}

func (c *disposeCounter) Dispose() { c.n.Add(1) }

func (c *disposeCounter) calls() int32 { return c.n.Load() }

type sliceDisposable struct {
	data  []int
	calls *atomic.Int32
}

func (d sliceDisposable) Dispose() { d.calls.Add(1) }

type panicDisposable struct {
	err   error
	calls *atomic.Int32
}

func (d panicDisposable) Dispose() {
	d.calls.Add(1)
	panic(d.err)
}

func capturePanic(fn func()) (recovered any) {
	defer func() { recovered = recover() }()
	fn()
	return recovered
}

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
	if got := late.calls(); got != 1 {
		t.Fatalf("a set on a disposed holder disposed the value %d times, want 1", got)
	}
	if m.Value() != nil {
		t.Fatal("Value should be nil after Dispose")
	}
}

func TestDisposableMapReleasesReplacedKeys(t *testing.T) {
	var m DisposableMap[string, *disposeCounter]
	first, second := &disposeCounter{}, &disposeCounter{}
	m.Set("k", first)
	m.Set("k", second)
	if got := first.calls(); got != 1 {
		t.Fatalf("first disposed %d times, want 1", got)
	}
	if got, ok := m.Get("k"); !ok || got != second {
		t.Fatalf("Get(k) = %v, %v, want second, true", got, ok)
	}

	m.Set("k", second)
	if got := second.calls(); got != 0 {
		t.Fatalf("second disposed %d times after setting it again, want 0", got)
	}
	if !m.Has("k") {
		t.Fatal("Has(k) should be true")
	}

	leaked := m.DeleteAndLeak("k")
	if leaked != second {
		t.Fatalf("DeleteAndLeak(k) = %v, want second", leaked)
	}
	if got := leaked.calls(); got != 0 {
		t.Fatalf("leaked value disposed %d times, want 0", got)
	}
	if m.Has("k") {
		t.Fatal("key should be gone")
	}
	if got, ok := m.Get("k"); ok || got != nil {
		t.Fatalf("Get(k) = %v, %v, want nil, false", got, ok)
	}
	if got := m.DeleteAndLeak("missing"); got != nil {
		t.Fatalf("DeleteAndLeak(missing) = %v, want nil", got)
	}

	third := &disposeCounter{}
	m.Set("k", third)
	if keys := m.Keys(); len(keys) != 1 || keys[0] != "k" {
		t.Fatalf("Keys() = %v, want [k]", keys)
	}
	m.DeleteAndDispose("k")
	if got := third.calls(); got != 1 {
		t.Fatalf("third disposed %d times, want 1", got)
	}
	if len(m.Keys()) != 0 {
		t.Fatal("Keys() should be empty after deleting the key")
	}

	last := &disposeCounter{}
	m.Set("k", last)
	m.Dispose()
	if got := last.calls(); got != 1 {
		t.Fatalf("last disposed %d times, want 1", got)
	}
	late := &disposeCounter{}
	m.Set("k", late)
	if got := late.calls(); got != 1 {
		t.Fatalf("late disposable disposed %d times, want 1", got)
	}
}

func TestDisposableStoreAcceptsNonComparableValues(t *testing.T) {
	var calls atomic.Int32
	d := sliceDisposable{data: []int{1}, calls: &calls}
	var store DisposableStore
	store.Add(d)
	store.Add(d)
	store.Dispose()
	if got := calls.Load(); got != 2 {
		t.Fatalf("non-comparable value disposed %d times, want 2", got)
	}
}

func TestDisposableStoreDeleteAndLeak(t *testing.T) {
	var store DisposableStore
	first, second := &disposeCounter{}, &disposeCounter{}
	store.Add(first)
	store.Add(second)
	store.DeleteAndLeak(first)
	if got := first.calls(); got != 0 {
		t.Fatalf("leaked value disposed %d times, want 0", got)
	}
	store.Dispose()
	if got := first.calls(); got != 0 {
		t.Fatalf("leaked value disposed %d times after Dispose, want 0", got)
	}
	if got := second.calls(); got != 1 {
		t.Fatalf("second disposed %d times, want 1", got)
	}

	var other DisposableStore
	third, neverAdded := &disposeCounter{}, &disposeCounter{}
	other.Add(third)
	other.Delete(third)
	if got := third.calls(); got != 1 {
		t.Fatalf("deleted value disposed %d times, want 1", got)
	}
	other.Dispose()
	if got := third.calls(); got != 1 {
		t.Fatalf("deleted value disposed %d times after Dispose, want 1", got)
	}
	other.Delete(neverAdded)
	if got := neverAdded.calls(); got != 1 {
		t.Fatalf("Delete of an unregistered value disposed it %d times, want 1", got)
	}
}

func TestDisposePanicsAreAggregatedAfterAllItems(t *testing.T) {
	firstPanic, secondPanic := errors.New("first panicked"), errors.New("second panicked")
	var firstCalls, secondCalls, lastCalls atomic.Int32
	var store DisposableStore
	store.Add(panicDisposable{err: firstPanic, calls: &firstCalls})
	store.Add(panicDisposable{err: secondPanic, calls: &secondCalls})
	store.Add(ToDisposable(func() { lastCalls.Add(1) }))

	recovered := capturePanic(store.Dispose)
	err, ok := recovered.(error)
	if !ok {
		t.Fatalf("Dispose panic = %v, want an error", recovered)
	}
	if !errors.Is(err, firstPanic) || !errors.Is(err, secondPanic) {
		t.Fatalf("Dispose panic = %v, want both panic errors", err)
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 || lastCalls.Load() != 1 {
		t.Fatalf("dispose counts = %d, %d, %d, want 1, 1, 1", firstCalls.Load(), secondCalls.Load(), lastCalls.Load())
	}
	if !store.IsDisposed() {
		t.Fatal("store should be disposed even after panics")
	}
	if got := capturePanic(store.Dispose); got != nil {
		t.Fatalf("second Dispose panicked with %v", got)
	}
}

func TestDisposableMapAggregatesPanicsAndStaysUsableAfterClear(t *testing.T) {
	firstPanic, secondPanic := errors.New("first panicked"), errors.New("second panicked")
	var firstCalls, secondCalls atomic.Int32
	var m DisposableMap[string, panicDisposable]
	m.Set("a", panicDisposable{err: firstPanic, calls: &firstCalls})
	m.Set("b", panicDisposable{err: secondPanic, calls: &secondCalls})

	recovered := capturePanic(m.Clear)
	err, ok := recovered.(error)
	if !ok {
		t.Fatalf("Clear panic = %v, want an error", recovered)
	}
	if !errors.Is(err, firstPanic) || !errors.Is(err, secondPanic) {
		t.Fatalf("Clear panic = %v, want both panic errors", err)
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("dispose counts = %d, %d, want 1, 1", firstCalls.Load(), secondCalls.Load())
	}
	if len(m.Keys()) != 0 || m.IsDisposed() {
		t.Fatal("Clear should empty the map without closing it")
	}
	if got := capturePanic(m.Dispose); got != nil {
		t.Fatalf("disposing an emptied map panicked with %v", got)
	}
}
