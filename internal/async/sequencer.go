package async

import (
	"context"
	"sync"
)

// Sequencer corresponds to VS Code's Sequencer: queued tasks run one at a
// time in FIFO order, and a failure does not stop the following tasks.
type Sequencer[T any] struct {
	mu   sync.Mutex
	tail chan struct{}
}

// NewSequencer constructs a Sequencer.
func NewSequencer[T any]() *Sequencer[T] { return &Sequencer[T]{} }

// Queue corresponds to Sequencer.queue. The task runs on a worker goroutine
// after all earlier tasks settle. Canceling ctx before then skips the task and
// settles the returned channel with the context error.
func (s *Sequencer[T]) Queue(ctx context.Context, task Task[T]) <-chan Result[T] {
	wait := newDelivery[T]()
	s.mu.Lock()
	previous := s.tail
	if previous == nil {
		previous = closedChannel
	}
	completed := make(chan struct{})
	s.tail = completed
	s.mu.Unlock()

	go func() {
		defer close(completed)
		select {
		case <-previous:
		case <-ctx.Done():
			wait.settle(Result[T]{Err: ctx.Err()})
			return
		}
		result := invokeResult(ctx, task)
		wait.settle(result)
	}()
	return wait.ch
}

// SequencerByKey corresponds to VS Code's SequencerByKey<TKey>: tasks with
// different keys run independently, while tasks with the same key are ordered.
type SequencerByKey[K comparable, T any] struct {
	mu    sync.Mutex
	tails map[K]chan struct{}
}

// NewSequencerByKey constructs a SequencerByKey.
func NewSequencerByKey[K comparable, T any]() *SequencerByKey[K, T] {
	return &SequencerByKey[K, T]{tails: make(map[K]chan struct{})}
}

// Queue corresponds to SequencerByKey.queue. It behaves like Sequencer.Queue
// for tasks sharing key.
func (s *SequencerByKey[K, T]) Queue(ctx context.Context, key K, task Task[T]) <-chan Result[T] {
	wait := newDelivery[T]()
	s.mu.Lock()
	previous := s.tails[key]
	if previous == nil {
		previous = closedChannel
	}
	completed := make(chan struct{})
	s.tails[key] = completed
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			if s.tails[key] == completed {
				delete(s.tails, key)
			}
			s.mu.Unlock()
			close(completed)
		}()
		select {
		case <-previous:
		case <-ctx.Done():
			wait.settle(Result[T]{Err: ctx.Err()})
			return
		}
		result := invokeResult(ctx, task)
		wait.settle(result)
	}()
	return wait.ch
}

// Peek corresponds to SequencerByKey.peek: it returns the channel that closes
// when the last queued task for key settles, or false when key is idle.
func (s *SequencerByKey[K, T]) Peek(key K) (<-chan struct{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	completed, ok := s.tails[key]
	return completed, ok
}

// Keys corresponds to SequencerByKey.keys and returns the keys with queued work.
func (s *SequencerByKey[K, T]) Keys() []K {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]K, 0, len(s.tails))
	for key := range s.tails {
		keys = append(keys, key)
	}
	return keys
}
