// Package perf records startup marks, after VS Code's
// src/vs/base/common/performance.ts mark() and getMarks(). Marks are times on
// the monotonic clock, kept in the order they were recorded; Marks returns a
// copy sorted by time, so a mark taken on a worker does not reorder the ones
// taken on the UI thread.
package perf

import (
	"slices"
	"sync"
	"time"
)

// Event is one recorded mark. At carries a monotonic reading, so subtracting
// two events is safe across clock changes.
type Event struct {
	Name string
	At   time.Time
}

var (
	mu    sync.Mutex
	marks []Event
)

// Mark records a mark now. Mark is safe for concurrent use.
func Mark(name string) {
	mu.Lock()
	marks = append(marks, Event{Name: name, At: time.Now()})
	mu.Unlock()
}

// Marks returns the recorded marks sorted by time. The result is a copy: the
// caller may hold on to it while more marks arrive. Events recorded at the
// same instant keep the order they were marked in.
func Marks() []Event {
	mu.Lock()
	out := slices.Clone(marks)
	mu.Unlock()
	slices.SortStableFunc(out, func(a, b Event) int { return a.At.Compare(b.At) })
	return out
}
