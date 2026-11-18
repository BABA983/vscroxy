package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// receive waits on a channel without sleeping: ordering comes from the channel
// itself, and the timer only fails a regression that stops making progress.
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case value := <-ch:
		return value
	case <-timer.C:
		t.Fatal("timed out waiting for a test signal")
		var zero T
		return zero
	}
}

func TestVetoStopsShutdownAndRetryWorks(t *testing.T) {
	var (
		veto   atomic.Bool
		joined atomic.Bool
	)
	var lc Lifecycle
	lc.OnBeforeShutdown(func(e *BeforeShutdownEvent) {
		if veto.Load() {
			e.Veto(true, "unsaved-requests")
		}
	})
	lc.OnWillShutdown(func(e *WillShutdownEvent) { joined.Store(true) })

	veto.Store(true)
	err := lc.Shutdown(ShutdownReasonQuit)
	if !errors.Is(err, ErrShutdownVetoed) {
		t.Fatalf("Shutdown error = %v, want ErrShutdownVetoed", err)
	}
	if !strings.Contains(err.Error(), "unsaved-requests") {
		t.Fatalf("Shutdown error = %v, want the veto id", err)
	}
	if joined.Load() || lc.WillShutdown() {
		t.Fatal("a veto must skip the will-shutdown phase")
	}

	veto.Store(false)
	if err := lc.Shutdown(ShutdownReasonQuit); err != nil {
		t.Fatalf("Shutdown after saving: %v", err)
	}
	if !joined.Load() || !lc.WillShutdown() {
		t.Fatal("the second shutdown should run the join phase")
	}
	if err := lc.Shutdown(ShutdownReasonQuit); err != nil {
		t.Fatalf("repeated Shutdown: %v", err)
	}
}

func TestFinalVetoRunsAfterOrdinaryVetos(t *testing.T) {
	var order []string
	var lc Lifecycle
	lc.OnBeforeShutdown(func(e *BeforeShutdownEvent) {
		order = append(order, "ordinary")
		e.Veto(false, "")
	})
	lc.OnFinalVeto(func(e *InternalBeforeShutdownEvent) {
		order = append(order, "final")
		e.FinalVeto(func(context.Context) (bool, error) { return true, nil }, "confirm-unsaved")
	})

	err := lc.Shutdown(ShutdownReasonClose)
	if !errors.Is(err, ErrShutdownVetoed) || !strings.Contains(err.Error(), "confirm-unsaved") {
		t.Fatalf("Shutdown error = %v, want the final veto id", err)
	}
	if len(order) != 2 || order[0] != "ordinary" || order[1] != "final" {
		t.Fatalf("phase order = %v, want [ordinary final]", order)
	}
}

func TestLastJoinersWaitForDefaultJoiners(t *testing.T) {
	var (
		defaultsDone atomic.Int32
		lastRan      atomic.Bool
	)
	var lc Lifecycle
	lc.OnWillShutdown(func(e *WillShutdownEvent) {
		if e.Reason != ShutdownReasonReload || e.Token == nil {
			t.Errorf("event reason = %v, token present = %v", e.Reason, e.Token != nil)
		}
		for i := range 2 {
			e.Join(func() error {
				defaultsDone.Add(1)
				return nil
			}, WillShutdownEventJoiner{ID: fmt.Sprintf("default-%d", i), Label: "Default"})
		}
	})
	lc.OnWillShutdown(func(e *WillShutdownEvent) {
		e.Join(func() error {
			if got := defaultsDone.Load(); got != 2 {
				return fmt.Errorf("last joiner started with %d defaults done", got)
			}
			if got := len(e.Joiners()); got != 1 {
				t.Errorf("pending joiners = %d, want only the last one", got)
			}
			lastRan.Store(true)
			return nil
		}, WillShutdownEventJoiner{ID: "last", Label: "Last", Order: WillShutdownJoinerOrderLast})
	})

	if err := lc.Shutdown(ShutdownReasonReload); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !lastRan.Load() {
		t.Fatal("last joiner did not run")
	}
}

func TestJoinErrorsAreReportedWithIDs(t *testing.T) {
	sentinel := errors.New("disk busy")
	var lc Lifecycle
	lc.OnWillShutdown(func(e *WillShutdownEvent) {
		e.Join(func() error { return sentinel }, WillShutdownEventJoiner{ID: "settings", Label: "Saving settings"})
	})
	err := lc.Shutdown(ShutdownReasonQuit)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Shutdown error = %v, want disk busy", err)
	}
	if !strings.Contains(err.Error(), "settings") || !strings.Contains(err.Error(), "Saving settings") {
		t.Fatalf("Shutdown error = %v, want the joiner id and label", err)
	}
}

func TestForceStopsWaitingForJoinerIgnoringToken(t *testing.T) {
	events := make(chan *WillShutdownEvent, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	done := make(chan error, 1)
	var lc Lifecycle
	lc.OnWillShutdown(func(e *WillShutdownEvent) {
		events <- e
		e.Join(func() error {
			close(started)
			<-release
			return nil
		}, WillShutdownEventJoiner{ID: "wait"})
	})

	go func() { done <- lc.Shutdown(ShutdownReasonClose) }()
	e := receive(t, events)
	receive(t, started)
	e.Force()
	select {
	case <-e.Token.Done():
	default:
		t.Fatal("Force did not cancel Token")
	}

	if err := receive(t, done); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestForceStopsWaitingForLastJoiner(t *testing.T) {
	events := make(chan *WillShutdownEvent, 1)
	lastStarted := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	done := make(chan error, 1)
	var lc Lifecycle
	lc.OnWillShutdown(func(e *WillShutdownEvent) {
		events <- e
		e.Join(func() error { return nil }, WillShutdownEventJoiner{ID: "default", Label: "Default"})
		e.Join(func() error {
			close(lastStarted)
			<-release
			return nil
		}, WillShutdownEventJoiner{ID: "last", Label: "Last", Order: WillShutdownJoinerOrderLast})
	})

	go func() { done <- lc.Shutdown(ShutdownReasonQuit) }()
	e := receive(t, events)
	receive(t, lastStarted)
	e.Force()
	if err := receive(t, done); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestAsyncVetoesRunInParallelAndBeforeFinalVeto(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan bool, 2)
	defer close(release)
	var settled, finalRanEarly atomic.Int32
	var joined atomic.Bool
	var lc Lifecycle
	lc.OnBeforeShutdown(func(e *BeforeShutdownEvent) {
		for i := range 2 {
			e.VetoAsync(func(ctx context.Context) (bool, error) {
				started <- struct{}{}
				veto := <-release
				settled.Add(1)
				return veto, nil
			}, fmt.Sprintf("async-%d", i))
		}
	})
	lc.OnFinalVeto(func(e *InternalBeforeShutdownEvent) {
		e.FinalVeto(func(ctx context.Context) (bool, error) {
			if settled.Load() != 2 {
				finalRanEarly.Store(1)
			}
			return false, nil
		}, "final")
	})
	lc.OnWillShutdown(func(e *WillShutdownEvent) { joined.Store(true) })

	done := make(chan error, 1)
	go func() { done <- lc.Shutdown(ShutdownReasonQuit) }()
	receive(t, started)
	receive(t, started)
	release <- true
	release <- true

	err := receive(t, done)
	if !errors.Is(err, ErrShutdownVetoed) {
		t.Fatalf("Shutdown error = %v, want ErrShutdownVetoed", err)
	}
	for _, id := range []string{"async-0", "async-1"} {
		if !strings.Contains(err.Error(), id) {
			t.Fatalf("Shutdown error = %v, want the %s veto id", err, id)
		}
	}
	if finalRanEarly.Load() != 0 {
		t.Fatal("final veto ran before the async vetoes settled")
	}
	if joined.Load() || lc.WillShutdown() {
		t.Fatal("an async veto must skip the will-shutdown phase")
	}
}

func TestAsyncVetoSettlesOnlyOnceWhenFinalVetoRuns(t *testing.T) {
	var checks atomic.Int32
	var lc Lifecycle
	lc.OnBeforeShutdown(func(e *BeforeShutdownEvent) {
		e.VetoAsync(func(ctx context.Context) (bool, error) {
			checks.Add(1)
			return false, nil
		}, "ordinary")
	})
	lc.OnFinalVeto(func(e *InternalBeforeShutdownEvent) {
		e.FinalVeto(func(context.Context) (bool, error) { return true, nil }, "final")
	})

	err := lc.Shutdown(ShutdownReasonClose)
	if !errors.Is(err, ErrShutdownVetoed) || !strings.Contains(err.Error(), "final") {
		t.Fatalf("Shutdown error = %v, want the final veto", err)
	}
	if got := checks.Load(); got != 1 {
		t.Fatalf("ordinary async veto ran %d times, want 1", got)
	}
}

func TestVetoErrorsVeto(t *testing.T) {
	tests := []struct {
		name     string
		register func(*Lifecycle)
		id       string
	}{
		{
			name: "async veto",
			register: func(lc *Lifecycle) {
				lc.OnBeforeShutdown(func(e *BeforeShutdownEvent) {
					e.VetoAsync(func(ctx context.Context) (bool, error) {
						return false, errors.New("proxy still busy")
					}, "async-error")
				})
			},
			id: "async-error",
		},
		{
			name: "async final veto",
			register: func(lc *Lifecycle) {
				lc.OnFinalVeto(func(e *InternalBeforeShutdownEvent) {
					e.FinalVeto(func(ctx context.Context) (bool, error) {
						return false, errors.New("unsaved document")
					}, "final-error")
				})
			},
			id: "final-error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lc Lifecycle
			tt.register(&lc)
			err := lc.Shutdown(ShutdownReasonClose)
			if !errors.Is(err, ErrShutdownVetoed) {
				t.Fatalf("Shutdown error = %v, want ErrShutdownVetoed", err)
			}
			if !strings.Contains(err.Error(), tt.id) {
				t.Fatalf("Shutdown error = %v, want the %s veto id", err, tt.id)
			}
		})
	}
}

func TestConcurrentShutdownWaitsForInFlightResult(t *testing.T) {
	busy := errors.New("disk busy")
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var lc Lifecycle
	lc.OnBeforeShutdown(func(e *BeforeShutdownEvent) {
		close(started)
		<-release
	})
	lc.OnWillShutdown(func(e *WillShutdownEvent) {
		e.Join(func() error { return busy }, WillShutdownEventJoiner{ID: "settings", Label: "Saving settings"})
	})

	first := make(chan error, 1)
	go func() { first <- lc.Shutdown(ShutdownReasonQuit) }()
	receive(t, started)

	entering := make(chan struct{})
	second := make(chan error, 1)
	go func() {
		close(entering)
		second <- lc.Shutdown(ShutdownReasonQuit)
	}()
	receive(t, entering)
	unblock()

	if err := receive(t, first); !errors.Is(err, busy) {
		t.Fatalf("first Shutdown error = %v, want busy", err)
	}
	if err := receive(t, second); !errors.Is(err, busy) {
		t.Fatalf("concurrent Shutdown error = %v, want the first shutdown's busy error", err)
	}
}

func TestDefaultJoinerPanicBecomesErrorAndStopsLastPhase(t *testing.T) {
	defaultPanic := errors.New("default joiner panicked")
	var goodDefault, lastRan atomic.Int32
	var lc Lifecycle
	lc.OnWillShutdown(func(e *WillShutdownEvent) {
		e.Join(func() error { panic(defaultPanic) }, WillShutdownEventJoiner{ID: "bad-default", Label: "Default"})
		e.Join(func() error {
			goodDefault.Add(1)
			return nil
		}, WillShutdownEventJoiner{ID: "good-default", Label: "Default"})
		e.Join(func() error {
			lastRan.Add(1)
			return nil
		}, WillShutdownEventJoiner{ID: "last", Label: "Last", Order: WillShutdownJoinerOrderLast})
	})

	err := lc.Shutdown(ShutdownReasonClose)
	if !errors.Is(err, defaultPanic) || !strings.Contains(err.Error(), "bad-default") {
		t.Fatalf("Shutdown error = %v, want the default joiner's panic", err)
	}
	if strings.Contains(err.Error(), "last") {
		t.Fatalf("Shutdown error = %v, want no last-phase error", err)
	}
	if goodDefault.Load() != 1 {
		t.Fatalf("non-panicking default joiner ran %d times, want 1", goodDefault.Load())
	}
	if lastRan.Load() != 0 {
		t.Fatalf("last joiner ran %d times after a default panic, want 0", lastRan.Load())
	}
}

func TestLastJoinerPanicBecomesErrorAndOtherLastJoinersRun(t *testing.T) {
	var goodLast atomic.Int32
	var lc Lifecycle
	lc.OnWillShutdown(func(e *WillShutdownEvent) {
		e.Join(func() error { panic("last joiner panicked") }, WillShutdownEventJoiner{ID: "bad-last", Label: "Last", Order: WillShutdownJoinerOrderLast})
		e.Join(func() error {
			goodLast.Add(1)
			return nil
		}, WillShutdownEventJoiner{ID: "good-last", Label: "Last", Order: WillShutdownJoinerOrderLast})
	})

	err := lc.Shutdown(ShutdownReasonClose)
	for _, want := range []string{"bad-last", "last joiner panicked"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Shutdown error = %v, want it to contain %q", err, want)
		}
	}
	if goodLast.Load() != 1 {
		t.Fatalf("non-panicking last joiner ran %d times, want 1", goodLast.Load())
	}
}

func TestFinalVetoCannotBeDefinedTwice(t *testing.T) {
	var lc Lifecycle
	lc.OnFinalVeto(func(e *InternalBeforeShutdownEvent) {})
	if got := capturePanic(func() {
		lc.OnFinalVeto(func(e *InternalBeforeShutdownEvent) {})
	}); got == nil {
		t.Fatal("a second OnFinalVeto registration did not panic")
	}

	var event InternalBeforeShutdownEvent
	event.FinalVeto(func(context.Context) (bool, error) { return false, nil }, "first")
	if got := capturePanic(func() {
		event.FinalVeto(func(context.Context) (bool, error) { return true, nil }, "second")
	}); got == nil {
		t.Fatal("a second FinalVeto slot did not panic")
	}
}

func TestFailedDefaultJoinerAbortsAndRetries(t *testing.T) {
	busy := errors.New("busy")
	var attempts, defaults, lasts atomic.Int32
	var lc Lifecycle
	lc.OnWillShutdown(func(e *WillShutdownEvent) {
		attempt := attempts.Add(1)
		err := error(nil)
		if attempt == 1 {
			err = busy
		}
		e.Join(func() error {
			defaults.Add(1)
			return err
		}, WillShutdownEventJoiner{ID: "system-proxy", Label: "Releasing system proxy"})
		e.Join(func() error {
			lasts.Add(1)
			return nil
		}, WillShutdownEventJoiner{ID: "sessions", Label: "Closing sessions", Order: WillShutdownJoinerOrderLast})
	})

	if err := lc.Shutdown(ShutdownReasonQuit); !errors.Is(err, busy) {
		t.Fatalf("first Shutdown = %v, want busy", err)
	}
	if lc.WillShutdown() {
		t.Fatal("a failed join phase must clear WillShutdown")
	}
	if lc.ShutdownSucceeded() {
		t.Fatal("a failed shutdown must not be cached as ready")
	}
	if got := lasts.Load(); got != 0 {
		t.Fatalf("last joiners ran %d times after a default failure, want 0", got)
	}

	if err := lc.Shutdown(ShutdownReasonQuit); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	if !lc.WillShutdown() || !lc.ShutdownSucceeded() {
		t.Fatal("successful retry should report will-shutdown and success")
	}
	if got := defaults.Load(); got != 2 {
		t.Fatalf("default joiners ran %d times, want 2", got)
	}
	if got := lasts.Load(); got != 1 {
		t.Fatalf("last joiners ran %d times after success, want 1", got)
	}
}

func TestLastJoinerFailureCompletesShutdown(t *testing.T) {
	busy := errors.New("settings busy")
	var defaults, lasts atomic.Int32
	var lc Lifecycle
	lc.OnWillShutdown(func(e *WillShutdownEvent) {
		e.Join(func() error {
			defaults.Add(1)
			return nil
		}, WillShutdownEventJoiner{ID: "system-proxy", Label: "Releasing system proxy"})
		e.Join(func() error {
			lasts.Add(1)
			return busy
		}, WillShutdownEventJoiner{ID: "settings", Label: "Saving settings", Order: WillShutdownJoinerOrderLast})
	})

	err := lc.Shutdown(ShutdownReasonQuit)
	if !errors.Is(err, busy) || errors.Is(err, ErrShutdownAborted) {
		t.Fatalf("Shutdown error = %v, want the Last joiner's error without an abort", err)
	}
	if !lc.WillShutdown() || !lc.ShutdownSucceeded() {
		t.Fatal("a Last joiner error must still complete the shutdown")
	}
	if err := lc.Shutdown(ShutdownReasonQuit); !errors.Is(err, busy) {
		t.Fatalf("later Shutdown = %v, want the cached Last joiner error", err)
	}
	if defaults.Load() != 1 || lasts.Load() != 1 {
		t.Fatalf("joiners ran %d defaults and %d lasts, want 1 and 1", defaults.Load(), lasts.Load())
	}
}

func TestShutdownAsyncRunsBeforeCallbacksOnCallerThenWaits(t *testing.T) {
	beforeStarted := make(chan struct{})
	beforeRelease := make(chan struct{})
	vetoStarted := make(chan struct{})
	releaseVeto := make(chan bool, 1)
	returned := make(chan struct{})
	done := make(chan error, 1)
	var willRan atomic.Bool
	var lc Lifecycle
	lc.OnBeforeShutdown(func(e *BeforeShutdownEvent) {
		close(beforeStarted)
		<-beforeRelease
		e.VetoAsync(func(ctx context.Context) (bool, error) {
			close(vetoStarted)
			return <-releaseVeto, nil
		}, "dialog")
	})
	lc.OnWillShutdown(func(e *WillShutdownEvent) { willRan.Store(true) })

	go func() {
		lc.ShutdownAsync(ShutdownReasonQuit, func(err error) { done <- err })
		close(returned)
	}()
	receive(t, beforeStarted)
	select {
	case <-returned:
		t.Fatal("ShutdownAsync returned before the before-shutdown callback finished")
	default:
	}
	close(beforeRelease)
	receive(t, vetoStarted)
	receive(t, returned)
	if willRan.Load() {
		t.Fatal("will-shutdown participant ran before the async veto settled")
	}

	releaseVeto <- true
	if err := receive(t, done); !errors.Is(err, ErrShutdownVetoed) {
		t.Fatalf("ShutdownAsync error = %v, want ErrShutdownVetoed", err)
	}
	if willRan.Load() {
		t.Fatal("will-shutdown participant ran after a veto")
	}
}

func TestSyncVetoSkipsAsyncAndFinalVeto(t *testing.T) {
	var asyncRan, finalRegistered, finalRan, willRan atomic.Int32
	var lc Lifecycle
	lc.OnBeforeShutdown(func(e *BeforeShutdownEvent) {
		e.VetoAsync(func(ctx context.Context) (bool, error) {
			asyncRan.Add(1)
			return true, nil
		}, "async")
		e.Veto(true, "sync")
	})
	lc.OnFinalVeto(func(e *InternalBeforeShutdownEvent) {
		finalRegistered.Add(1)
		e.FinalVeto(func(context.Context) (bool, error) {
			finalRan.Add(1)
			return true, nil
		}, "final")
	})
	lc.OnWillShutdown(func(e *WillShutdownEvent) { willRan.Add(1) })

	err := lc.Shutdown(ShutdownReasonQuit)
	if !errors.Is(err, ErrShutdownVetoed) || !strings.Contains(err.Error(), "sync") {
		t.Fatalf("Shutdown error = %v, want the sync veto", err)
	}
	if strings.Contains(err.Error(), "async") {
		t.Fatalf("Shutdown error = %v, want no async veto id", err)
	}
	if asyncRan.Load() != 0 || finalRegistered.Load() != 0 || finalRan.Load() != 0 || willRan.Load() != 0 {
		t.Fatalf("after a sync veto: async=%d final registration=%d final=%d will=%d, want all 0",
			asyncRan.Load(), finalRegistered.Load(), finalRan.Load(), willRan.Load())
	}
}

func TestShutdownAsyncConcurrentCallersGetSameResult(t *testing.T) {
	busy := errors.New("disk busy")
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var lc Lifecycle
	lc.OnBeforeShutdown(func(e *BeforeShutdownEvent) {
		close(started)
		<-release
	})
	lc.OnWillShutdown(func(e *WillShutdownEvent) {
		e.Join(func() error { return busy }, WillShutdownEventJoiner{ID: "settings", Label: "Saving settings"})
	})

	first := make(chan error, 1)
	go func() {
		lc.ShutdownAsync(ShutdownReasonQuit, func(err error) { first <- err })
	}()
	receive(t, started)
	second := make(chan error, 1)
	lc.ShutdownAsync(ShutdownReasonQuit, func(err error) { second <- err })
	unblock()

	for _, result := range []<-chan error{first, second} {
		if err := receive(t, result); !errors.Is(err, busy) {
			t.Fatalf("ShutdownAsync error = %v, want busy", err)
		}
	}
}
