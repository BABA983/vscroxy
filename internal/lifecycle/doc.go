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
// OnBeforeShutdown and OnWillShutdown participant callbacks, and the callback
// that installs FinalVeto, run synchronously on the goroutine calling
// Shutdown. Call Shutdown from the UI thread when those callbacks read app
// state; if Shutdown must be called from a worker, marshal such callbacks back
// to the UI thread. Long work does not belong in the callbacks.
//
// Async veto functions passed to Veto or FinalVeto, and join work passed to
// Join, run on goroutines owned by this package. Veto functions run in
// parallel; join work also runs in parallel within each phase. Those functions
// must not read or write UI state directly: capture the values the callback
// already gathered on the UI thread and marshal any results back. A joiner
// that ignores its canceled Token may keep running after Force; Shutdown stops
// waiting for it instead of killing it.
package lifecycle
