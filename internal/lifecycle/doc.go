// Package lifecycle owns resource cleanup and the two phases of shutdown:
// before-shutdown participants may veto, then will-shutdown participants join
// work that has to finish before the application exits.
//
// The package deliberately does not import MyGo or provide a service locator.
// The app owns one Lifecycle value and passes it to the few components that
// participate in shutdown. Disposables are idempotent and dispose in
// registration order so cleanup can be registered once and closed from one
// place. Disposing a store or map recovers each item's panic, disposes all the
// remaining items, and then panics with the collected errors.
//
// # Threading model
//
// ShutdownAsync starts a shutdown without blocking the UI thread. Its
// OnBeforeShutdown callbacks run synchronously on the calling goroutine before
// it returns and may read UI state. After that, a goroutine owned by this
// package settles the async vetoes, then runs the OnFinalVeto registration
// callback and the OnWillShutdown callbacks, since those phases can only start
// after earlier phases settle. Those later callbacks therefore also run on the
// package goroutine, not the UI thread: they must capture the state they need
// or marshal UI access themselves. done is called on that goroutine too, and
// the caller marshals its result back to the UI thread. Long work belongs in
// VetoFunc and Join functions, never in the participant callbacks.
//
// Shutdown is the blocking form built on ShutdownAsync: it waits for the done
// result instead of taking one. The same goroutine rules apply; do not call it
// on the UI thread when async work needs that thread to keep running, such as
// showing an "unsaved requests?" dialog.
//
// Async veto functions passed to VetoAsync or FinalVeto, and join work, run on
// package-owned goroutines. Veto functions run in parallel; join work also
// runs in parallel within each phase. A synchronous Veto(true) skips the async
// checks and the final veto.
// A joiner that ignores its canceled Token may keep running after Force;
// Shutdown stops waiting for it instead of killing it.
//
// # Failed shutdowns
//
// In VS Code the will-shutdown phase is irreversible. Here a failing default
// joiner aborts the shutdown: the error wraps ErrShutdownAborted, Last joiners
// are skipped, WillShutdown returns false again, and a later Shutdown runs
// both phases from the start. This matters when a default joiner releases an
// OS setting such as the system proxy, which can fail and leave the app
// running with capture still active.
//
// Once the defaults succeed, the will phase is complete. An error from a Last
// joiner (or Force ending the wait) is returned but does not abort: the run is
// cached, WillShutdown stays true, and later callers get the same result
// instead of repeating teardown. Callers distinguish the two with errors.Is.
package lifecycle
