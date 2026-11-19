package app

import (
	"context"

	"github.com/baba983/vscroxy/internal/lifecycle"
)

// newCaptureSessionStore owns the workers that live with one loaded capture.
// The first capture and every replacement get the same three stops, in the
// order replaceSession has always released them.
func newCaptureSessionStore(a *app) *lifecycle.DisposableStore {
	store := lifecycle.NewDisposableStore()
	store.Add(lifecycle.ToDisposable(a.views.stopSearch))
	store.Add(lifecycle.ToDisposable(a.messageUI.stopSearch))
	store.Add(lifecycle.ToDisposable(a.protobuf.stop))
	return store
}

// newSessionLifetime puts a session's cancellation function under lifecycle's
// idempotent ownership, so a document close, a modal dismissal and quit can
// all ask the same session to stop without racing to cancel it.
//
// A session's done channel is deliberately not added: it reports that the
// worker has finished, not that someone asked it to stop. Closing it at stop
// would let waiters mistake a still-running stream for a finished one and
// change the message the session records for a local close. The worker closes
// it when it actually returns.
func newSessionLifetime(cancel context.CancelFunc) *lifecycle.DisposableStore {
	store := lifecycle.NewDisposableStore()
	store.Add(lifecycle.ToDisposable(cancel))
	return store
}

// stopSessionLifetime cancels a session through its store. Fixtures and
// saved-session snapshots can carry a bare cancel function, so that still
// works when there is no store.
func stopSessionLifetime(store *lifecycle.DisposableStore, cancel context.CancelFunc) {
	if store != nil {
		store.Dispose()
		return
	}
	if cancel != nil {
		cancel()
	}
}
