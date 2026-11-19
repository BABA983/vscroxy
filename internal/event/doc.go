// Package event is the Go form of VS Code's base/common/event.ts: an
// Event listens, an Emitter fires, and registering a listener returns the
// lifecycle.Disposable that VS Code's Event returns.
//
// Listeners added after an Emitter is disposed are immediately released.
// Firing copies the listener list, so a listener may subscribe or unsubscribe
// while an event is being delivered. Listeners are called on the goroutine
// that fires; services that can be changed off the UI thread must marshal
// their own UI updates.
package event
