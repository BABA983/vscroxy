// Package lifecycle owns resource cleanup and the two phases of shutdown:
// before-shutdown participants may veto, then will-shutdown participants join
// work that has to finish before the application exits.
//
// The package deliberately does not import MyGo or provide a service locator.
// The app owns one Lifecycle value and passes it to the few components that
// participate in shutdown. Disposables are idempotent and dispose in
// registration order so cleanup can be registered once and closed from one
// place.
package lifecycle
