package perf_test

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/perf"
)

var testSeq atomic.Uint64

func TestMarksKeepOrderAndDurationsUnderConcurrentMarking(t *testing.T) {
	prefix := fmt.Sprintf("perf-test-%d/", testSeq.Add(1))
	perf.Mark(prefix + "first")
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Go(func() { perf.Mark(fmt.Sprintf("%sworker-%02d", prefix, i)) })
	}
	wg.Wait()
	time.Sleep(time.Millisecond) // a later, distinct monotonic reading
	perf.Mark(prefix + "last")

	var got []perf.Event
	for _, event := range perf.Marks() {
		if strings.HasPrefix(event.Name, prefix) {
			got = append(got, event)
		}
	}
	if len(got) != 66 {
		t.Fatalf("got %d marks, want 66: %v", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if duration := got[i].At.Sub(got[i-1].At); duration < 0 {
			t.Fatalf("marks out of order at %d: %s follows %s by %s", i, got[i].Name, got[i-1].Name, duration)
		}
	}
	if got[0].Name != prefix+"first" || got[len(got)-1].Name != prefix+"last" {
		t.Fatalf("first = %q, last = %q", got[0].Name, got[len(got)-1].Name)
	}
	if duration := got[len(got)-1].At.Sub(got[0].At); duration <= 0 {
		t.Fatalf("startup duration = %s, want positive", duration)
	}
	snapshot := perf.Marks()
	perf.Mark(prefix + "later")
	if len(perf.Marks()) == len(snapshot) {
		t.Fatal("Marks returned live state rather than a copy")
	}
}
