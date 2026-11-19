package app

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/system"
	"github.com/egoist/mygo/ui"
)

// fakeSleep records the platform assertion instead of touching the machine.
type fakeSleep struct {
	holds, releases int
	err             error
	logs            []error
	released        chan struct{}
	once            sync.Once
}

func (f *fakeSleep) hold(reason string) (func(), error) {
	f.holds++
	if f.err != nil {
		return nil, f.err
	}
	return func() {
		f.releases++
		if f.released != nil {
			f.once.Do(func() { close(f.released) })
		}
	}, nil
}

func (f *fakeSleep) log(err error) { f.logs = append(f.logs, err) }

func TestSleepGuardSharesOneAssertionForOverlappingRuns(t *testing.T) {
	fake := &fakeSleep{}
	g := &sleepGuard{hold: fake.hold, log: fake.log}
	first, second, third := g.holdRun(), g.holdRun(), g.holdRun()
	if fake.holds != 1 {
		t.Fatalf("three overlapping runs acquired %d assertions", fake.holds)
	}
	first()
	first() // a run can release more than once
	second()
	if fake.releases != 0 {
		t.Fatalf("the last run was still held: %d releases", fake.releases)
	}
	third()
	if fake.releases != 1 {
		t.Fatalf("the last release dropped %d assertions", fake.releases)
	}

	fourth := g.holdRun()
	if fake.holds != 2 || fake.releases != 1 {
		t.Fatalf("a new run did not start a new assertion: holds=%d releases=%d", fake.holds, fake.releases)
	}
	fourth()
	if fake.releases != 2 {
		t.Fatalf("the final release did not run: %d", fake.releases)
	}
}

func TestSleepGuardFailureLogsOnceAndDoesNotBlockTheRun(t *testing.T) {
	fake := &fakeSleep{err: errors.New("platform refused")}
	g := &sleepGuard{hold: fake.hold, log: fake.log}
	first, second := g.holdRun(), g.holdRun()
	first()
	second()
	if fake.holds != 1 || fake.releases != 0 {
		t.Fatalf("a failed acquisition changed the assertion: holds=%d releases=%d", fake.holds, fake.releases)
	}
	if len(fake.logs) != 1 || !errors.Is(fake.logs[0], fake.err) {
		t.Fatalf("logs = %v, want one failure", fake.logs)
	}
	if g.refs != 0 {
		t.Fatalf("a failed acquisition left %d holders", g.refs)
	}
}

// gatedSleep holds the first platform release until the test lets it go,
// standing in for MyGo's onMain waiting for the UI thread.
type gatedSleep struct {
	active   atomic.Int32
	acquires chan struct{}
	releases chan struct{}
	gate     chan struct{}
	once     sync.Once
}

func newGatedSleep() *gatedSleep {
	return &gatedSleep{acquires: make(chan struct{}, 2), releases: make(chan struct{}, 2), gate: make(chan struct{})}
}

func (f *gatedSleep) hold(string) (func(), error) {
	f.active.Add(1)
	f.acquires <- struct{}{}
	return func() {
		f.once.Do(func() {
			f.releases <- struct{}{}
			<-f.gate
		})
		f.active.Add(-1)
	}, nil
}

func TestSleepGuardDoesNotBlockOnAReleaseWaitingForMainThread(t *testing.T) {
	fake := newGatedSleep()
	g := &sleepGuard{hold: fake.hold, log: func(err error) { t.Errorf("unexpected failure: %v", err) }}
	first := g.holdRun()
	<-fake.acquires

	firstDone := make(chan struct{})
	go func() { first(); close(firstDone) }()
	<-fake.releases // the platform release is blocked, as onMain would be

	returned := make(chan func(), 1)
	go func() { returned <- g.holdRun() }()
	var second func()
	select {
	case second = <-returned:
	case <-t.Context().Done():
		t.Fatal("holdRun blocked behind a release waiting for the main thread")
	}
	<-fake.acquires
	if n := fake.active.Load(); n != 2 {
		t.Fatalf("active assertions = %d with a release still pending, want 2", n)
	}
	close(fake.gate)
	<-firstDone
	if n := fake.active.Load(); n != 1 {
		t.Fatalf("active assertions = %d after the first release, want 1", n)
	}
	second()
	if n := fake.active.Load(); n != 0 {
		t.Fatalf("active assertions = %d after the last release, want 0", n)
	}
}

// A run can be cancelled while its first platform acquire is still waiting
// for the main thread. Its token is only returned once the acquire finishes,
// so the test performs the token's drop while the fake hold is blocked.
func TestSleepGuardReleasesAnAcquireThatBecameStale(t *testing.T) {
	entered := make(chan struct{})
	unblock := make(chan struct{})
	var active atomic.Int32
	released := make(chan struct{}, 1)
	hold := func(string) (func(), error) {
		active.Add(1)
		entered <- struct{}{}
		<-unblock
		return func() {
			active.Add(-1)
			released <- struct{}{}
		}, nil
	}
	g := &sleepGuard{hold: hold, log: func(err error) { t.Errorf("unexpected failure: %v", err) }}
	returned := make(chan func(), 1)
	go func() { returned <- g.holdRun() }()
	<-entered
	g.dropRun() // the only holder leaves before the acquire returns
	close(unblock)
	select {
	case <-released:
	case <-t.Context().Done():
		t.Fatal("the stale assertion was not released")
	}
	<-returned
	if n := active.Load(); n != 0 {
		t.Fatalf("active assertions = %d after a stale acquire, want 0", n)
	}
}

// A failed check in an ordinary run still releases on the run goroutine,
// before it closes the channel the UI drains.
func TestRunnerReleasesSleepAssertionOnFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.requests = []savedRequest{{Name: "fails", Method: "GET", URL: upstream.URL, Checks: request.Checks{Status: "200"}}}
	fake := &fakeSleep{}
	a.sleep.hold, a.sleep.log = fake.hold, fake.log
	a.openRunner()
	a.runner.stopOnFailure = true
	a.startRunner()
	for range a.runner.events {
	}
	if a.runner.outcome == nil || a.runner.outcome.summary.Failed != 1 {
		t.Fatalf("the run did not fail: %+v", a.runner.outcome)
	}
	if fake.holds != 1 || fake.releases != 1 {
		t.Fatalf("sleep assertion: holds=%d releases=%d", fake.holds, fake.releases)
	}
}

// Stopping a load test cancels its context; the worker releases the assertion
// before it closes done, which is also what the quit joiner waits on.
func TestLoadRunnerReleasesSleepAssertionOnCancel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.requests = []savedRequest{{Name: "loads", Method: "GET", URL: upstream.URL}}
	fake := &fakeSleep{released: make(chan struct{})}
	a.sleep.hold, a.sleep.log = fake.hold, fake.log
	a.openLoadRunner()
	a.loadRunner.duration = "10m"
	a.startLoadRunner()
	a.stopLoadRunner()
	select {
	case <-a.loadRunner.done:
	case <-t.Context().Done():
		t.Fatal("cancelled load worker did not finish")
	}
	if fake.holds != 1 || fake.releases != 1 {
		t.Fatalf("sleep assertion: holds=%d releases=%d", fake.holds, fake.releases)
	}
}

// A wake goes through the one system-proxy check path. Ordinary frames do not
// recheck: MyGo supplies the wake event, there is no clock poll to trigger one.
func TestWakeRechecksProxyOnceAndTicksDoNot(t *testing.T) {
	fake := &fakeSystemProxy{check: system.ProxyCheck{Status: system.ProxyMatches}}
	a := proxyTestApp(t, fake)
	tt := ui.NewTester(a.view, 1280, 800)
	var wake func()
	a.resumeWatch.listen = func(fn func()) func() { wake = fn; return func() {} }
	a.resumeWatch.start(func() { a.checkSystemProxy(true) })
	if wake == nil {
		t.Fatal("the wake notification was not installed")
	}
	wake()
	settleProxy(t, a, tt)
	if fake.reads != 1 || fake.writes != 0 {
		t.Fatalf("wake: reads=%d writes=%d, want one read", fake.reads, fake.writes)
	}
	for range 3 {
		tt.Frame()
	}
	if fake.reads != 1 {
		t.Fatalf("ordinary frames rechecked the OS %d times", fake.reads)
	}
	wake()
	settleProxy(t, a, tt)
	if fake.reads != 2 || fake.writes != 0 {
		t.Fatalf("second wake: reads=%d writes=%d", fake.reads, fake.writes)
	}
}
