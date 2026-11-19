package watchdog_test

import (
	"bytes"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/async"
	"github.com/baba983/vscroxy/internal/watchdog"
)

// fakeMain records the callbacks the watchdog posts to the main thread. It
// returns immediately when a thread is "hung", leaving the callback unanswered
// until the test lets it run, and starts no goroutine of its own.
type fakeMain struct {
	mu         sync.Mutex
	queued     []func()
	dispatches int
}

func (m *fakeMain) run(fn func()) {
	m.mu.Lock()
	m.queued = append(m.queued, fn)
	m.dispatches++
	m.mu.Unlock()
}

func (m *fakeMain) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dispatches
}

func (m *fakeMain) answer() bool {
	m.mu.Lock()
	if len(m.queued) == 0 {
		m.mu.Unlock()
		return false
	}
	fn := m.queued[0]
	m.queued = m.queued[1:]
	m.mu.Unlock()
	fn()
	return true
}

func (m *fakeMain) answerAll() {
	for m.answer() {
	}
}

func fixture(t *testing.T, opts ...watchdog.Option) (*async.FakeClock, *fakeMain, *bytes.Buffer, *watchdog.Watchdog, *int) {
	t.Helper()
	clock := async.NewFakeClock()
	main := &fakeMain{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	dumps := new(int)
	all := []watchdog.Option{
		watchdog.WithClock(clock),
		watchdog.WithLogger(logger),
		watchdog.WithStackDump(func() []byte {
			(*dumps)++
			return []byte("goroutine 1 [running]\nmain.fake()\n")
		}),
	}
	w := watchdog.New(main.run, append(all, opts...)...)
	t.Cleanup(w.Dispose)
	return clock, main, &logs, w, dumps
}

func TestWatchdogDumpsOnceForOneHangAndLogsRecovery(t *testing.T) {
	clock, main, logs, w, dumps := fixture(t)
	w.Start()

	clock.Advance(time.Second)            // first ping
	clock.Advance(500 * time.Millisecond) // unanswered long enough
	if *dumps != 1 {
		t.Fatalf("dumps = %d, want 1", *dumps)
	}
	if text := logs.String(); !strings.Contains(text, "unresponsive") || !strings.Contains(text, "goroutine 1 [running]") {
		t.Fatalf("stack dump not in log: %q", text)
	}

	// More unanswered pings are suppressed: one hang gets one dump.
	clock.Advance(3 * time.Second)
	if *dumps != 1 {
		t.Fatalf("dumps after repeated ticks = %d, want 1", *dumps)
	}
	if !main.answer() {
		t.Fatal("no ping waiting to be answered")
	}
	if text := logs.String(); !strings.Contains(text, "recovered") || !strings.Contains(text, "hang=3.5s") {
		t.Fatalf("recovery not in log: %q", text)
	}
}

func TestWatchdogRateLimitsDumpsInsideTheWindow(t *testing.T) {
	// Production uses one minute; a short injected window drives the same
	// path with a few fake seconds.
	const window = 3 * time.Second
	clock, main, logs, w, dumps := fixture(t, watchdog.WithDumpInterval(window))
	w.Start()

	// First hang: dump at 1.5s, recovery at 2s.
	clock.Advance(1500 * time.Millisecond)
	if *dumps != 1 {
		t.Fatalf("first hang: dumps = %d, want 1", *dumps)
	}
	clock.Advance(500 * time.Millisecond)
	main.answerAll()

	// Second hang inside the window: detected and recovered, but not dumped.
	clock.Advance(1500 * time.Millisecond)
	clock.Advance(500 * time.Millisecond)
	if *dumps != 1 {
		t.Fatalf("second hang inside window: dumps = %d, want 1", *dumps)
	}
	main.answerAll()
	if n := strings.Count(logs.String(), "recovered"); n < 2 {
		t.Fatalf("recovery lines = %d, want at least 2: %q", n, logs.String())
	}

	// Once the window passes, a new hang can dump again.
	clock.Advance(4 * time.Second)
	if *dumps != 2 {
		t.Fatalf("hang after window: dumps = %d, want 2", *dumps)
	}
}

func TestWatchdogCapsDumpSize(t *testing.T) {
	clock, _, logs, _, _ := fixture(t)
	w := watchdog.New(func(func()) {},
		watchdog.WithClock(clock),
		watchdog.WithLogger(slog.New(slog.NewTextHandler(logs, nil))),
		watchdog.WithMaxDumpBytes(64),
		watchdog.WithInterval(time.Millisecond),
		watchdog.WithAnswerTime(time.Millisecond),
		watchdog.WithStackDump(func() []byte { return bytes.Repeat([]byte("x"), 4096) }),
	)
	w.Start()
	clock.Advance(2 * time.Millisecond) // ping at 1ms, unanswered at 2ms
	if n := strings.Count(logs.String(), "x"); n > 64 || n == 0 {
		t.Fatalf("dump has %d bytes of stacks, want 1..64: %q", n, logs.String())
	}
	if !strings.Contains(logs.String(), "stack dump truncated") {
		t.Fatalf("no truncation marker: %q", logs.String())
	}
}

func TestWatchdogDisposeStopsPingsAndLeavesNoGoroutine(t *testing.T) {
	before := runtime.NumGoroutine()
	clock, main, logs, w, dumps := fixture(t)
	w.Start()
	clock.Advance(time.Second)
	w.Dispose()
	w.Dispose() // dispose is idempotent
	clock.Advance(10 * time.Second)
	if got := main.count(); got != 1 {
		t.Fatalf("pings after dispose = %d, want 1", got)
	}
	if *dumps != 0 {
		t.Fatalf("dumps after dispose = %d, want 0", *dumps)
	}
	// A callback that was already dispatched only reports a stale answer.
	main.answer()
	if logs.Len() != 0 {
		t.Fatalf("log after dispose: %q", logs.String())
	}
	for range 100 {
		if runtime.NumGoroutine() <= before {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("goroutines before = %d, after = %d", before, runtime.NumGoroutine())
}
