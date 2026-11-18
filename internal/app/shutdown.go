package app

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/baba983/vscroxy/internal/lifecycle"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
)

// shutdownCleanup is the work shared by the lifecycle joiners and the native
// termination hooks. Each function runs at most once, so a graceful shutdown
// followed by OnQuit does not write the same files or stop the same jobs twice.
type shutdownCleanup struct {
	flushSettings    func()
	flushCookies     func()
	stopRecording    func()
	cancelRequests   func()
	closeExperiments func()
	closeSessions    func()
}

// installShutdown registers the graceful cleanup work and returns the same
// idempotent functions for the fallback hooks. Call it on the UI thread before
// the app runs.
func (a *app) installShutdown() shutdownCleanup {
	if a.lifecycle == nil {
		a.lifecycle = &lifecycle.Lifecycle{}
	}
	c := shutdownCleanup{
		flushSettings:    sync.OnceFunc(a.flushSettings),
		flushCookies:     sync.OnceFunc(a.flushCookies),
		stopRecording:    sync.OnceFunc(func() { a.stopCollectionRecording("") }),
		cancelRequests:   sync.OnceFunc(a.cancelQuitRequests),
		closeExperiments: sync.OnceFunc(a.experiments.close),
		closeSessions:    sync.OnceFunc(a.sessions.close),
	}
	a.lifecycle.OnWillShutdown(func(e *lifecycle.WillShutdownEvent) {
		// The proxy owns an OS setting, so it releases first. If that fails,
		// the app keeps running and none of the other cleanup may run: capture
		// and its workspaces must stay exactly as they were.
		released := new(atomic.Bool)
		released.Store(true)
		e.Join(func() error {
			err := a.systemProxy.close()
			if err != nil {
				released.Store(false)
			}
			return err
		}, lifecycle.WillShutdownEventJoiner{ID: "system-proxy", Label: locales.ReleaseSystemProxy()})

		joinMain := func(id, label string, fn func()) {
			e.Join(func() error {
				if released.Load() {
					a.onMain(fn)
				}
				return nil
			}, lifecycle.WillShutdownEventJoiner{ID: id, Label: label, Order: lifecycle.WillShutdownJoinerOrderLast})
		}
		joinMain("settings", "Saving settings", c.flushSettings)
		joinMain("recording", "Stopping collection recording", c.stopRecording)
		joinMain("cookies", "Saving cookies", c.flushCookies)
		joinMain("requests", "Cancelling requests", c.cancelRequests)
		joinMain("experiments", "Closing experiments", c.closeExperiments)
		joinMain("sessions", "Closing sessions", c.closeSessions)
	})
	return c
}

// cancelQuitRequests stops work that can still publish into the closing views.
func (a *app) cancelQuitRequests() {
	a.columnRead.stop()
	a.structured[0].stop()
	a.structured[1].stop()
	a.stopDocuments()
}

// quitting is the single guard for controls that must not run while the app is
// leaving, whether cleanup is in progress or already succeeded.
func (a *app) quitting() bool {
	return a.lifecycle != nil && (a.lifecycle.ShuttingDown() || a.lifecycle.ShutdownSucceeded())
}

// requestQuit is the OnBeforeQuit/OnClose entry. It never blocks the UI thread:
// lifecycle runs the join phase on a goroutine and reports back on the main
// thread. Once cleanup succeeded, the next quit is allowed through natively.
func (a *app) requestQuit(e *mygo.QuitEvent) {
	if e.DefaultPrevented() || (a.lifecycle != nil && a.lifecycle.ShutdownSucceeded()) {
		return
	}
	e.PreventDefault()
	if a.lifecycle == nil || a.lifecycle.ShuttingDown() {
		return
	}
	a.lifecycle.ShutdownAsync(lifecycle.ShutdownReasonQuit, func(err error) {
		a.onMain(func() { a.finishQuit(err) })
	})
}

func (a *app) finishQuit(err error) {
	if err == nil {
		a.quit()
		return
	}
	if errors.Is(err, lifecycle.ErrShutdownVetoed) {
		a.warn(err.Error())
		return
	}
	a.fail(locales.QuitCancelledSystemProxyCleanup(), err)
	a.reportQuitError(err)
}

func (a *app) onMain(fn func()) {
	if a.quitOnMain != nil {
		a.quitOnMain(fn)
		return
	}
	mygo.RunOnMain(fn)
}

func (a *app) quit() {
	if a.quitApp != nil {
		a.quitApp()
		return
	}
	mygo.App.Quit()
}

func (a *app) reportQuitError(err error) {
	if a.quitError != nil {
		a.quitError(err)
		return
	}
	go mygo.Dialog.Error(locales.CouldNotReleaseSystemProxy(), "Capture is still running to keep your network working. Disable the system proxy in Settings and retry quitting.\n\n"+err.Error())
}
