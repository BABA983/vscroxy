package lifecycle

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

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
		e.FinalVeto(func() bool { return true }, "confirm-unsaved")
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

func TestForceCancelsTokenAndClearsPending(t *testing.T) {
	events := make(chan *WillShutdownEvent, 1)
	done := make(chan error, 1)
	var lc Lifecycle
	lc.OnWillShutdown(func(e *WillShutdownEvent) {
		events <- e
		e.Join(func() error {
			<-e.Token.Done()
			return nil
		}, WillShutdownEventJoiner{ID: "wait"})
	})

	go func() { done <- lc.Shutdown(ShutdownReasonClose) }()
	e := <-events
	e.Force()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Force did not cancel the joiner")
	}
	if got := len(e.Joiners()); got != 0 {
		t.Fatalf("pending joiners = %d, want 0", got)
	}
}
