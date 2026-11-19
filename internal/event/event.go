package event

import (
	"slices"
	"sync"

	"github.com/baba983/vscroxy/internal/lifecycle"
)

// Event registers a listener and returns a disposable subscription, as in
// VS Code's Event<T>. A nil listener registers nothing.
type Event[T any] func(listener func(T)) lifecycle.Disposable

type subscription[T any] struct {
	id       uint64
	listener func(T)
}

// Emitter owns the listeners of one event. Its zero value is ready to use.
type Emitter[T any] struct {
	mu        sync.RWMutex
	listeners []subscription[T]
	nextID    uint64
	disposed  bool
}

// NewEmitter returns a ready emitter, matching VS Code's `new Emitter<T>()`.
func NewEmitter[T any]() *Emitter[T] { return new(Emitter[T]) }

// Event is the registration side of the emitter, separated from the ability
// to fire it. Keep it as a field or method result where only subscribing is
// allowed.
func (e *Emitter[T]) Event(listener func(T)) lifecycle.Disposable {
	if listener == nil {
		return lifecycle.ToDisposable(nil)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.disposed {
		return lifecycle.ToDisposable(nil)
	}
	e.nextID++
	id := e.nextID
	e.listeners = append(e.listeners, subscription[T]{id: id, listener: listener})
	return lifecycle.ToDisposable(func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		at := slices.IndexFunc(e.listeners, func(s subscription[T]) bool { return s.id == id })
		if at >= 0 {
			e.listeners = slices.Delete(e.listeners, at, at+1)
		}
	})
}

// Fire delivers value to the listeners registered at the time of the call.
func (e *Emitter[T]) Fire(value T) {
	e.mu.RLock()
	if e.disposed || len(e.listeners) == 0 {
		e.mu.RUnlock()
		return
	}
	listeners := slices.Clone(e.listeners)
	e.mu.RUnlock()
	for _, sub := range listeners {
		sub.listener(value)
	}
}

// HasListeners reports whether anyone is subscribed.
func (e *Emitter[T]) HasListeners() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.listeners) > 0
}

// Dispose releases every subscription and refuses new ones, like VS Code's
// Emitter.dispose.
func (e *Emitter[T]) Dispose() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.disposed = true
	e.listeners = nil
}
