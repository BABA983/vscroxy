package leakcheck

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// The production settle window stays in place; only the gap between dumps
// shrinks so these tests finish quickly. Every handoff below uses channels.
func fastSettle(t *testing.T) {
	t.Helper()
	old := settleDelay
	settleDelay = 100 * time.Microsecond
	t.Cleanup(func() { settleDelay = old })
}

func TestSettleWaitsForGoroutineThatExits(t *testing.T) {
	fastSettle(t)
	check := Start()

	release := make(chan struct{})
	exited := make(chan struct{})
	exitingStarted := make(chan struct{})
	go func() {
		close(exitingStarted)
		<-release
		close(exited)
	}()
	<-exitingStarted

	// A second goroutine releases the first only after the initial dump has
	// seen it, so a check with no retries would report it as a leak.
	letGo := make(chan struct{})
	releaserStarted := make(chan struct{})
	releaserDone := make(chan struct{})
	go func() {
		close(releaserStarted)
		defer close(releaserDone)
		<-letGo
		close(release)
	}()
	<-releaserStarted

	dumps := 0
	check.take = func() snapshot {
		dumps++
		s := takeSnapshot()
		if dumps == 1 {
			close(letGo)
		} else {
			<-exited
			<-releaserDone
		}
		return s
	}
	if leaks := check.diff(nil); len(leaks) != 0 {
		t.Fatalf("goroutine that exited while settling was reported: %+v", leaks)
	}
	if dumps < 2 {
		t.Fatalf("diff took %d dump(s), want a recheck", dumps)
	}
}

func TestSettleReportsGoroutineStillBlocked(t *testing.T) {
	fastSettle(t)
	check := Start()

	release := make(chan struct{})
	started := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		close(started)
		<-release
		close(exited)
	}()
	<-started
	t.Cleanup(func() {
		close(release)
		<-exited
	})

	dumps := 0
	check.take = func() snapshot {
		dumps++
		return takeSnapshot()
	}
	leaks := check.diff(nil)
	if !slices.ContainsFunc(leaks, func(l leak) bool {
		return strings.Contains(l.name, "TestSettleReportsGoroutineStillBlocked")
	}) {
		t.Fatalf("goroutine still blocked at the deadline was not reported: %+v", leaks)
	}
	if dumps < 2 {
		t.Fatalf("diff took %d dump(s), want the settle window", dumps)
	}
}

func TestDiffReturnsOnFirstCleanDump(t *testing.T) {
	check := Start()
	dumps := 0
	check.take = func() snapshot {
		dumps++
		return snapshot{}
	}
	if leaks := check.diff(nil); len(leaks) != 0 {
		t.Fatalf("clean diff reported leaks: %+v", leaks)
	}
	if dumps != 1 {
		t.Fatalf("clean diff took %d dump(s), want 1", dumps)
	}
}
