// Package errorsx handles errors that escape the app's background goroutines.
// It is the Go form of VS Code's onUnexpectedError: a guard recovers a panic,
// reports its type and stack without the panic value (which can hold request
// contents), and hands a short error to the app's notice handler.
//
// Unlike internal/app this package is free of MyGo and UI state, so the
// capture core, request runners and local listeners can guard their own
// goroutines. A panic there would otherwise end the whole process while the
// system proxy still points at the now-dead capture port.
package errorsx

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/baba983/vscroxy/internal/applog"
	"github.com/baba983/vscroxy/locales"
)

var unexpected struct {
	sync.RWMutex
	handler func(error)
}

// SetUnexpectedErrorHandler installs the process-wide reporter. The app
// installs its notice handler at startup; until then the error still reaches
// the app log.
func SetUnexpectedErrorHandler(handler func(error)) {
	unexpected.Lock()
	unexpected.handler = handler
	unexpected.Unlock()
}

// ReportUnexpected passes an error from background work to the installed
// reporter without echoing it here: callers that saw a panic log their own
// detail and stack first.
func ReportUnexpected(err error) {
	unexpected.RLock()
	handler := unexpected.handler
	unexpected.RUnlock()
	if handler != nil {
		handler(err)
	}
}

// ReportPanic logs a recovered panic and reports it as an unexpected error.
// The value is used only for its type; its text can contain request bodies,
// headers or secrets and must not be written to the log or a notice.
func ReportPanic(value any, stack []byte) {
	err := fmt.Errorf("background goroutine panicked (%T)", value)
	applog.For("errorsx").Error(locales.UnexpectedBackgroundError(), "detail", err.Error(), "stack", string(stack))
	ReportUnexpected(err)
}

// Guard runs one background goroutine and recovers its panic. The panic is
// re-thrown under go test, where a failing assertion must still fail its test.
func Guard(fn func()) {
	defer func() {
		if value := recover(); value != nil {
			ReportPanic(value, debug.Stack())
			if repanicInTests() {
				panic(value) // a bug in a test must still fail the test
			}
		}
	}()
	fn()
}

// repanicInTests is a variable so the contract test can exercise the
// production path (report and return) inside a test binary.
var repanicInTests = inGoTest

func inGoTest() bool {
	name := filepath.Base(os.Args[0])
	return strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".test.exe")
}
