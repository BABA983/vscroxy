// Package watchdog detects a main (UI) thread that stops answering, after the
// window unresponsive sampling in VS Code's
// src/vs/platform/windows/electron-main/windowImpl.ts. A worker pings the main
// thread once a second with MyGo's RunOnMain. A ping unanswered for half a
// second records a goroutine profile in the app log; recovery, and how long the
// thread was stuck, are logged when a ping finally comes back.
package watchdog

import (
	"bytes"
	"fmt"
	"log/slog"
	"runtime/pprof"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/async"
)

const (
	defaultInterval     = time.Second
	defaultAnswerTime   = 500 * time.Millisecond
	defaultDumpInterval = time.Minute
	defaultMaxDumpBytes = 256 << 10
)

// RunMain schedules fn on the main thread. It must return promptly, even when
// the main thread is stuck: MyGo's RunOnMain blocks until fn has run, so the
// app's adapter calls it on its own goroutine.
type RunMain func(fn func())

// Option configures a Watchdog.
type Option func(*Watchdog)

// WithClock injects the clock used by the timers and the hang durations.
func WithClock(clock async.Clock) Option {
	return func(w *Watchdog) { w.clock = clock }
}

// WithLogger sends the watchdog's messages to logger. The default is the
// current slog default.
func WithLogger(logger *slog.Logger) Option {
	return func(w *Watchdog) { w.logger = logger }
}

// WithInterval sets how often the main thread is pinged.
func WithInterval(interval time.Duration) Option {
	return func(w *Watchdog) { w.interval = interval }
}

// WithAnswerTime sets how long a ping may go unanswered before a dump.
func WithAnswerTime(answerTime time.Duration) Option {
	return func(w *Watchdog) { w.answerTime = answerTime }
}

// WithDumpInterval sets the shortest gap between two dumps.
func WithDumpInterval(interval time.Duration) Option {
	return func(w *Watchdog) { w.dumpInterval = interval }
}

// WithMaxDumpBytes bounds how much of a stack dump reaches the log.
func WithMaxDumpBytes(max int) Option {
	return func(w *Watchdog) { w.maxDumpBytes = max }
}

// WithStackDump replaces the goroutine profile. Tests use it to avoid dumping
// the test runner's own stacks; the profile still passes through the size cap.
func WithStackDump(dump func() []byte) Option {
	return func(w *Watchdog) { w.dump = dump }
}

type probe struct {
	id      uint64
	started time.Time
	hung    bool
	timer   *async.TimeoutTimer
}

// Watchdog owns one interval timer and, while a ping is outstanding, one
// answer timer. It starts no goroutine of its own: runMain decides how a ping
// reaches the main thread. Both timers stop on Dispose.
type Watchdog struct {
	clock        async.Clock
	runMain      RunMain
	logger       *slog.Logger
	interval     time.Duration
	answerTime   time.Duration
	dumpInterval time.Duration
	maxDumpBytes int
	dump         func() []byte

	mu       sync.Mutex
	ticks    *async.IntervalTimer
	nextID   uint64
	probing  *probe
	lastDump time.Time
	disposed bool
}

// New constructs a stopped watchdog. Dispose is safe to call more than once.
func New(runMain RunMain, opts ...Option) *Watchdog {
	if runMain == nil {
		panic("watchdog: nil main-thread runner")
	}
	w := &Watchdog{
		clock:        async.RealClock(),
		runMain:      runMain,
		logger:       slog.Default(),
		interval:     defaultInterval,
		answerTime:   defaultAnswerTime,
		dumpInterval: defaultDumpInterval,
		maxDumpBytes: defaultMaxDumpBytes,
		dump:         goroutineStacks,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Start begins sampling. It is a no-op after Dispose or a second Start.
func (w *Watchdog) Start() {
	w.mu.Lock()
	if w.disposed || w.ticks != nil {
		w.mu.Unlock()
		return
	}
	w.ticks = async.NewIntervalTimer(func() error {
		w.tick()
		return nil
	}, w.interval, async.WithClock(w.clock))
	w.mu.Unlock()
}

// Dispose stops sampling and the outstanding answer timer. A callback already
// running on the main thread cannot be cancelled, but it only reports an answer
// for a probe no longer tracked, so it does no work.
func (w *Watchdog) Dispose() {
	w.mu.Lock()
	if w.disposed {
		w.mu.Unlock()
		return
	}
	w.disposed = true
	ticks, probing := w.ticks, w.probing
	w.ticks, w.probing = nil, nil
	w.mu.Unlock()
	if ticks != nil {
		ticks.Dispose()
	}
	if probing != nil {
		probing.timer.Dispose()
	}
}

func (w *Watchdog) tick() {
	w.mu.Lock()
	if w.disposed || w.probing != nil {
		w.mu.Unlock()
		// One hang is one probe: piling up pings would dump the same stall
		// again and leave a queue of callbacks on a thread already stuck.
		return
	}
	w.nextID++
	id := w.nextID
	p := &probe{id: id, started: w.clock.Now()}
	p.timer = async.NewTimeoutTimer(func() error {
		w.timeout(id)
		return nil
	}, w.answerTime, async.WithClock(w.clock))
	w.probing = p
	w.mu.Unlock()
	w.runMain(func() { w.answer(id) })
}

func (w *Watchdog) answer(id uint64) {
	w.mu.Lock()
	p := w.probing
	if p == nil || p.id != id || w.disposed {
		w.mu.Unlock()
		return
	}
	w.probing = nil
	p.timer.Dispose()
	if !p.hung {
		w.mu.Unlock()
		return
	}
	duration := w.clock.Now().Sub(p.started)
	w.mu.Unlock()
	w.logger.Info("main thread recovered", "hang", duration)
}

func (w *Watchdog) timeout(id uint64) {
	w.mu.Lock()
	p := w.probing
	if p == nil || p.id != id || w.disposed {
		w.mu.Unlock()
		return
	}
	p.hung = true
	now := w.clock.Now()
	canDump := now.Sub(w.lastDump) >= w.dumpInterval
	if canDump {
		w.lastDump = now
	}
	started := p.started
	w.mu.Unlock()
	if !canDump {
		return
	}
	w.logger.Error("main thread unresponsive; goroutine stacks",
		"hang", now.Sub(started), "stacks", capped(w.dump(), w.maxDumpBytes))
}

// capped bounds a dump without cutting the marker that says it was cut.
func capped(dump []byte, max int) string {
	if max <= 0 || len(dump) <= max {
		return string(dump)
	}
	const marker = "\n[stack dump truncated]\n"
	if max < len(marker) {
		return string(dump[:max])
	}
	return string(dump[:max-len(marker)]) + marker
}

func goroutineStacks() []byte {
	var buf bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&buf, 2); err != nil {
		return []byte(fmt.Sprintf("watchdog: goroutine profile unavailable: %v", err))
	}
	return buf.Bytes()
}
