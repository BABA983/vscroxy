package lifecycle

import "sync"

// Disposable releases one resource. Dispose must be safe to call more than
// once. Implementations added to a DisposableStore or passed to
// MutableDisposable.Set must be comparable; this package and ToDisposable
// return pointers.
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

// DisposableStore owns a group of disposables and releases them in
// registration order. Adding the same disposable twice registers it once.
// A store is safe for concurrent use.
type DisposableStore struct {
	mu       sync.Mutex
	items    map[Disposable]struct{}
	order    []Disposable
	disposed bool
}

// NewDisposableStore returns a ready store. The zero value is also usable.
func NewDisposableStore() *DisposableStore {
	return &DisposableStore{items: make(map[Disposable]struct{})}
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
	if s.items == nil {
		s.items = make(map[Disposable]struct{})
	}
	if _, ok := s.items[d]; ok {
		s.mu.Unlock()
		return d
	}
	s.items[d] = struct{}{}
	s.order = append(s.order, d)
	s.mu.Unlock()
	return d
}

// Clear releases everything in the store but leaves it usable.
func (s *DisposableStore) Clear() {
	s.mu.Lock()
	items := s.order
	s.order = nil
	clear(s.items)
	s.mu.Unlock()
	for _, d := range items {
		d.Dispose()
	}
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
	clear(s.items)
	s.mu.Unlock()
	for _, d := range items {
		d.Dispose()
	}
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
// value again is a no-op. Like VS Code's MutableDisposable.value, a set on a
// disposed holder does nothing.
func (m *MutableDisposable) Set(d Disposable) {
	m.mu.Lock()
	if m.disposed || d == m.current {
		m.mu.Unlock()
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
// and later Set calls do nothing.
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
type DisposableMap[K comparable] struct {
	mu       sync.Mutex
	values   map[K]Disposable
	disposed bool
}

// Set stores d under k, disposing any previous value. If the map is already
// disposed, d is disposed immediately instead.
func (m *DisposableMap[K]) Set(k K, d Disposable) {
	if d == nil {
		m.DeleteAndDispose(k)
		return
	}
	m.mu.Lock()
	if m.disposed {
		m.mu.Unlock()
		d.Dispose()
		return
	}
	if m.values == nil {
		m.values = make(map[K]Disposable)
	}
	old := m.values[k]
	m.values[k] = d
	m.mu.Unlock()
	if old != nil {
		old.Dispose()
	}
}

// Get returns the disposable stored under k.
func (m *DisposableMap[K]) Get(k K) (Disposable, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.values[k]
	return d, ok
}

// DeleteAndDispose removes k and releases its disposable.
func (m *DisposableMap[K]) DeleteAndDispose(k K) {
	m.mu.Lock()
	d := m.values[k]
	delete(m.values, k)
	m.mu.Unlock()
	if d != nil {
		d.Dispose()
	}
}

// Clear releases every disposable and removes every key. The map stays usable.
func (m *DisposableMap[K]) Clear() {
	m.mu.Lock()
	values := m.values
	m.values = make(map[K]Disposable)
	m.mu.Unlock()
	for _, d := range values {
		d.Dispose()
	}
}

// Dispose releases every disposable and closes the map. Later calls do
// nothing, and later Set calls dispose their argument immediately.
func (m *DisposableMap[K]) Dispose() {
	m.mu.Lock()
	if m.disposed {
		m.mu.Unlock()
		return
	}
	m.disposed = true
	values := m.values
	m.values = nil
	m.mu.Unlock()
	for _, d := range values {
		d.Dispose()
	}
}

// IsDisposed reports whether Dispose has been called.
func (m *DisposableMap[K]) IsDisposed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.disposed
}
