package app

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
)

// sleepGuard holds one idle-sleep assertion for any number of active runs.
// MyGo already maps KeepAwake to the platform's assertion; counting here makes
// overlapping runs share it and drops it with the last release.
type sleepGuard struct {
	mu      sync.Mutex
	refs    int
	epoch   uint64
	release func()

	// hold is the platform call. Run installs MyGo's KeepAwake once its event
	// loop exists, and tests inject a fake; the zero guard holds nothing, so no
	// unit test waits for a UI thread that is not being driven.
	// log stands in for the app log in tests.
	hold func(reason string) (func(), error)
	log  func(error)
}

// holdRun acquires the assertion for one run. The returned release is safe to
// call from any goroutine and more than once, including after a failed hold.
// The counter changes under the lock; the platform call runs after it, because
// MyGo's call waits for the main thread and the main thread must never wait on
// this lock. The epoch marks one acquire: if the last holder left while it was
// in flight, the acquirer drops its own assertion when it returns.
func (g *sleepGuard) holdRun() func() {
	g.mu.Lock()
	g.refs++
	acquire := g.refs == 1
	if acquire {
		g.epoch++
	}
	epoch := g.epoch
	g.mu.Unlock()

	if acquire && g.hold != nil {
		release, err := g.hold(locales.KeepAwakeReason())
		if err != nil {
			// Keep the holders until the last one drops: a run keeps working,
			// and overlapping runs still hear about one failed attempt.
			log := g.log
			if log == nil {
				log = func(err error) { slog.Error(locales.KeepAwakeFailed(), "err", err) }
			}
			log(err)
		}
		g.mu.Lock()
		stale := g.epoch != epoch
		if !stale && err == nil {
			g.release = release
		}
		g.mu.Unlock()
		if stale && err == nil {
			release()
		}
	}

	var once sync.Once
	return func() { once.Do(g.dropRun) }
}

func (g *sleepGuard) dropRun() {
	g.mu.Lock()
	g.refs--
	var release func()
	if g.refs == 0 {
		g.epoch++
		release, g.release = g.release, nil
	}
	g.mu.Unlock()
	if release != nil {
		release()
	}
}

// mygoKeepAwake asks for an idle-sleep assertion, never a display one: display
// is false. MyGo implements this with NSProcessInfo on macOS, an inhibit portal
// on Linux and PowerCreateRequest on Windows. MyGo always returns a release,
// even when the backend gives nothing; only a panic is reported as a failure.
func mygoKeepAwake(reason string) (release func(), err error) {
	defer func() {
		if r := recover(); r != nil {
			release, err = nil, fmt.Errorf("mygo KeepAwake: %v", r)
		}
	}()
	return mygo.Power.KeepAwake(reason, false), nil
}

// resumeWatch turns the system's wake notification into the existing
// system-proxy check. MyGo reports resume on every desktop platform, so there
// is no clock polling to add. MyGo fires the callback on the main thread, as
// the proxy check's UI state requires.
type resumeWatch struct {
	onWake func()
	// listen stands in for MyGo's wake notification in tests.
	listen func(func()) func()
}

func (w *resumeWatch) start(onWake func()) {
	w.onWake = onWake
	listen := w.listen
	if listen == nil {
		listen = mygo.Power.OnResume
	}
	listen(func() { w.onWake() })
}
