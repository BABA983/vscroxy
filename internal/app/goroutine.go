package app

import "github.com/baba983/vscroxy/internal/errorsx"

func init() {
	// The handler installed by Run lives in notify.go; errorsx must not import
	// the app to reach it. All packages guard their goroutines through errorsx.
	errorsx.SetUnexpectedErrorHandler(onUnexpectedError)
}

// guarded runs one app-owned background goroutine. Other packages call
// errorsx.Guard directly.
func guarded(fn func()) { errorsx.Guard(fn) }
