package lifecycle

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"
)

// Disposable releases one resource. Dispose must be safe to call more than
// once. Implementations added to a DisposableStore or passed to
// MutableDisposable.Set do not need to be comparable.
type Disposable interface {
	Dispose()
}

type funcDisposable struct {
	dispose func()
}

func (d *funcDisposable) Dispose() { d.dispose() }

type noopDisposable struct{}

func (noopDisposable) Dispose() {}

// ToDisposable turns fn into an idempotent Disposable. A nil fn gives a no-op.
func ToDisposable(fn func()) Disposable {
	if fn == nil {
		return noopDisposable{}
	}
	return &funcDisposable{dispose: sync.OnceFunc(fn)}
}

// disposeAll releases every item, recovering each one so a panic cannot stop
// the rest. Like VS Code's dispose(iterable), it reports the collected panics
// after all items have been disposed.
func disposeAll(items []Disposable) {
	var errs []error
	for _, d := range items {
		if d == nil {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					errs = append(errs, panicError("disposing", r))
				}
			}()
			d.Dispose()
		}()
	}
	if len(errs) > 0 {
		panic(errors.Join(errs...))
	}
}

func panicError(what string, value any) error {
	if err, ok := value.(error); ok {
		return fmt.Errorf("lifecycle: %s: %w", what, err)
	}
	return fmt.Errorf("lifecycle: %s: %v", what, value)
}

func comparableDisposable(d Disposable) bool {
	return d != nil && reflect.ValueOf(d).Comparable()
}

func sameDisposable(a, b Disposable) (same bool) {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	aValue, bValue := reflect.ValueOf(a), reflect.ValueOf(b)
	if aValue.Type() != bValue.Type() || !aValue.Comparable() {
		return false
	}
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return a == b
}

// DisposableStore owns a group of disposables and releases them in
// registration order. Adding the same comparable disposable twice registers
// it once. A store is safe for concurrent use.
type DisposableStore struct {
	mu         sync.Mutex
	comparable map[Disposable]struct{}
	order      []Disposable
	disposed   bool
}

// NewDisposableStore returns a ready store. The zero value is also usable.
func NewDisposableStore() *DisposableStore {
	return &DisposableStore{}
}

// Add registers d and returns it. If the store is already disposed, d is
// disposed immediately instead of being retained.
func (s *DisposableStore) Add(d Disposable) Disposable {
	if d == nil {
		return nil
	}
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		d.Dispose()
		return d
	}
	if comparableDisposable(d) {
		if s.comparable == nil {
			s.comparable = make(map[Disposable]struct{})
		}
		if _, ok := s.comparable[d]; ok {
			s.mu.Unlock()
			return d
		}
		s.comparable[d] = struct{}{}
	}
	s.order = append(s.order, d)
	s.mu.Unlock()
	return d
}

// Delete removes d from the store and disposes it, even when d was never
// added. It is the counterpart of VS Code's DisposableStore.delete.
func (s *DisposableStore) Delete(d Disposable) {
	if d == nil {
		return
	}
	s.mu.Lock()
	s.remove(d)
	s.mu.Unlock()
	d.Dispose()
}

// DeleteAndLeak removes d from the store without disposing it.
func (s *DisposableStore) DeleteAndLeak(d Disposable) {
	if d == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remove(d)
}

// remove requires s.mu to be held.
func (s *DisposableStore) remove(d Disposable) {
	if comparableDisposable(d) {
		delete(s.comparable, d)
	}
	s.order = slices.DeleteFunc(s.order, func(other Disposable) bool {
		return sameDisposable(other, d)
	})
}

// Clear releases everything in the store but leaves it usable.
func (s *DisposableStore) Clear() {
	s.mu.Lock()
	items := s.order
	s.order = nil
	clear(s.comparable)
	s.mu.Unlock()
	disposeAll(items)
}

// Dispose releases everything and makes the store closed. Later calls do
// nothing, and later Add calls dispose their argument immediately.
func (s *DisposableStore) Dispose() {
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return
	}
	s.disposed = true
	items := s.order
	s.order = nil
	clear(s.comparable)
	s.mu.Unlock()
	disposeAll(items)
}

// IsDisposed reports whether Dispose has been called.
func (s *DisposableStore) IsDisposed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disposed
}

// MutableDisposable holds at most one disposable. Setting a new value releases
// the previous one; disposing the holder releases the current value.
type MutableDisposable struct {
	mu       sync.Mutex
	current  Disposable
	disposed bool
}

// Set replaces the current value, disposing the old one. Setting the current
// value again is a no-op. A set on a disposed holder disposes its argument
// immediately, like adding to a disposed DisposableStore.
func (m *MutableDisposable) Set(d Disposable) {
	m.mu.Lock()
	if sameDisposable(m.current, d) {
		m.mu.Unlock()
		return
	}
	if m.disposed {
		m.mu.Unlock()
		if d != nil {
			d.Dispose()
		}
		return
	}
	old := m.current
	m.current = d
	m.mu.Unlock()
	if old != nil {
		old.Dispose()
	}
}

// Value returns the current disposable, or nil if there is none.
func (m *MutableDisposable) Value() Disposable {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// Clear releases the current value. The holder stays usable.
func (m *MutableDisposable) Clear() { m.Set(nil) }

// Dispose releases the current value and makes the holder closed. Later calls
// do nothing, and later Set calls dispose their argument immediately.
func (m *MutableDisposable) Dispose() {
	m.mu.Lock()
	if m.disposed {
		m.mu.Unlock()
		return
	}
	m.disposed = true
	old := m.current
	m.current = nil
	m.mu.Unlock()
	if old != nil {
		old.Dispose()
	}
}

// IsDisposed reports whether Dispose has been called.
func (m *MutableDisposable) IsDisposed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.disposed
}

// DisposableMap holds one disposable per key. Setting a key releases the value
// it replaces. A map is safe for concurrent use; its zero value is usable.
type DisposableMap[K comparable, V Disposable] struct {
	mu       sync.Mutex
	values   map[K]V
	disposed bool
}

// Set stores d under k, disposing any previous value. Setting the same value
// again is a no-op. If the map is already disposed, d is disposed immediately.
func (m *DisposableMap[K, V]) Set(k K, d V) {
	var value Disposable = d
	if value == nil {
		m.DeleteAndDispose(k)
		return
	}
	m.mu.Lock()
	if m.disposed {
		m.mu.Unlock()
		value.Dispose()
		return
	}
	if m.values == nil {
		m.values = make(map[K]V)
	}
	old, exists := m.values[k]
	if exists && sameDisposable(old, value) {
		m.mu.Unlock()
		return
	}
	m.values[k] = d
	m.mu.Unlock()
	if exists {
		var disposable Disposable = old
		if disposable != nil {
			disposable.Dispose()
		}
	}
}

// Get returns the disposable stored under k and whether the key exists.
func (m *DisposableMap[K, V]) Get(k K) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.values[k]
	return d, ok
}

// Has reports whether k is stored in the map.
func (m *DisposableMap[K, V]) Has(k K) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.values[k]
	return ok
}

// Keys returns a snapshot of the stored keys in no particular order.
func (m *DisposableMap[K, V]) Keys() []K {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Collect(maps.Keys(m.values))
}

// DeleteAndDispose removes k and releases its disposable.
func (m *DisposableMap[K, V]) DeleteAndDispose(k K) {
	m.mu.Lock()
	d, ok := m.values[k]
	if ok {
		delete(m.values, k)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	var disposable Disposable = d
	if disposable != nil {
		disposable.Dispose()
	}
}

// DeleteAndLeak removes k and returns the value it held, without disposing it.
// The zero value is returned when k was not stored.
func (m *DisposableMap[K, V]) DeleteAndLeak(k K) V {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.values[k]
	if ok {
		delete(m.values, k)
	}
	return d
}

// Clear releases every disposable and removes every key. The map stays usable.
func (m *DisposableMap[K, V]) Clear() {
	m.mu.Lock()
	values := m.values
	m.values = make(map[K]V)
	m.mu.Unlock()
	disposeAll(disposables(values))
}

// Dispose releases every disposable and closes the map. Later calls do
// nothing, and later Set calls dispose their argument immediately.
func (m *DisposableMap[K, V]) Dispose() {
	m.mu.Lock()
	if m.disposed {
		m.mu.Unlock()
		return
	}
	m.disposed = true
	values := m.values
	m.values = nil
	m.mu.Unlock()
	disposeAll(disposables(values))
}

func disposables[K comparable, V Disposable](values map[K]V) []Disposable {
	out := make([]Disposable, 0, len(values))
	for _, value := range values {
		var d Disposable = value
		out = append(out, d)
	}
	return out
}

// IsDisposed reports whether Dispose has been called.
func (m *DisposableMap[K, V]) IsDisposed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.disposed
}
