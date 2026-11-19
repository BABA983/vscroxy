package app

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/lifecycle"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
)

// startQuitForTest puts an app into the in-progress quit state without a MyGo
// event loop. The returned function releases the blocking participant.
func startQuitForTest(t *testing.T, a *app) func() {
	t.Helper()
	if a.lifecycle == nil {
		a.lifecycle = &lifecycle.Lifecycle{}
	}
	started := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	a.lifecycle.OnBeforeShutdown(func(*lifecycle.BeforeShutdownEvent) {
		close(started)
		<-release
	})
	go func() {
		a.lifecycle.ShutdownAsync(lifecycle.ShutdownReasonQuit, func(err error) { result <- err })
	}()
	<-started
	var once sync.Once
	finish := func() {
		once.Do(func() {
			close(release)
			if err := <-result; err != nil {
				t.Errorf("test shutdown: %v", err)
			}
		})
	}
	t.Cleanup(finish)
	return finish
}

func TestQuitProxyReleaseFailureCanBeRetried(t *testing.T) {
	useTempHome(t, t.TempDir())
	a := newApp()
	t.Cleanup(func() { _ = a.srv.Close() })
	a.installShutdown()
	a.quitOnMain = func(fn func()) { fn() }
	quits := make(chan struct{}, 2)
	a.quitApp = func() { quits <- struct{}{} }
	failures := make(chan error, 2)
	a.quitError = func(err error) { failures <- err }

	denied := errors.New("authorization denied")
	clears := 0
	a.systemProxy.clear = func(string) error {
		clears++
		return denied
	}
	a.systemProxy.mu.Lock()
	a.systemProxy.ownedTarget = "127.0.0.1:7890"
	a.systemProxy.ownedNow.Store(true)
	a.systemProxy.mu.Unlock()

	event := &mygo.QuitEvent{}
	a.requestQuit(event)
	if !event.DefaultPrevented() {
		t.Fatal("the first quit request should be prevented")
	}
	err := <-failures
	if !strings.Contains(err.Error(), denied.Error()) {
		t.Fatalf("quit error = %v, want the proxy failure", err)
	}
	if a.quitting() || a.lifecycle.WillShutdown() || a.lifecycle.ShutdownSucceeded() {
		t.Fatal("a failed release should leave the app running and retryable")
	}
	if clears != 1 {
		t.Fatalf("proxy clear ran %d times, want 1", clears)
	}
	if a.experiments.closed {
		t.Fatal("other cleanup ran after the proxy release failed")
	}
	select {
	case <-quits:
		t.Fatal("a failed proxy release still quit the app")
	default:
	}

	a.systemProxy.clear = func(string) error {
		clears++
		return nil
	}
	a.requestQuit(&mygo.QuitEvent{})
	select {
	case <-quits:
	case <-time.After(5 * time.Second):
		t.Fatal("a second quit attempt did not succeed")
	}
	if clears != 2 {
		t.Fatalf("proxy clear ran %d times after the retry, want 2", clears)
	}
	if !a.lifecycle.ShutdownSucceeded() {
		t.Fatal("successful retry should be cached as ready")
	}
	if !a.experiments.closed {
		t.Fatal("other cleanup did not run after the successful retry")
	}
	if !strings.Contains(a.message, locales.QuitCancelledSystemProxyCleanup()) {
		t.Fatalf("status message = %q, want the quit-cancelled notice", a.message)
	}
}

func TestQuitLastJoinerFailureStillQuits(t *testing.T) {
	useTempHome(t, t.TempDir())
	a := newApp()
	t.Cleanup(func() { _ = a.srv.Close() })
	a.installShutdown()
	a.quitOnMain = func(fn func()) { fn() }
	quits := make(chan struct{}, 1)
	a.quitApp = func() { quits <- struct{}{} }
	failures := make(chan error, 1)
	a.quitError = func(err error) { failures <- err }
	a.systemProxy.clear = func(string) error { return nil }
	a.systemProxy.mu.Lock()
	a.systemProxy.ownedTarget = "127.0.0.1:7890"
	a.systemProxy.ownedNow.Store(true)
	a.systemProxy.mu.Unlock()

	lastErr := errors.New("settings flush failed")
	a.lifecycle.OnWillShutdown(func(e *lifecycle.WillShutdownEvent) {
		e.Join(func() error { return lastErr }, lifecycle.WillShutdownEventJoiner{
			ID: "last-failure", Label: "Saving settings", Order: lifecycle.WillShutdownJoinerOrderLast,
		})
	})

	a.requestQuit(&mygo.QuitEvent{})
	select {
	case <-quits:
	case <-time.After(5 * time.Second):
		t.Fatal("a Last joiner error did not quit")
	}
	select {
	case err := <-failures:
		t.Fatalf("Last joiner error showed the proxy failure dialog: %v", err)
	default:
	}
	if !a.lifecycle.WillShutdown() || !a.lifecycle.ShutdownSucceeded() {
		t.Fatal("a Last joiner error should still complete shutdown")
	}
	if strings.Contains(a.message, locales.QuitCancelledSystemProxyCleanup()) {
		t.Fatalf("status message = %q, want no quit-cancelled notice", a.message)
	}
}
