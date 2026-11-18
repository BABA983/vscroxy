package app

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/baba983/vscroxy/locales"
)

// guarded runs one background goroutine. A panic there would otherwise end the
// whole process with the system proxy still pointed at the dead proxy port.
// The panic value is deliberately not logged: it can hold request contents.
// Its type and the stack identify the fault without recording such data.
func guarded(fn func()) {
	defer func() {
		if v := recover(); v != nil {
			reportUnexpectedPanic(v, debug.Stack())
			if inGoTest() {
				panic(v) // a bug in a test must still fail the test
			}
		}
	}()
	fn()
}

// reportUnexpectedPanic writes the panic and its stack to the app log and
// hands a short error to the notice handler installed by Run.
func reportUnexpectedPanic(v any, stack []byte) {
	err := fmt.Errorf("background goroutine panicked (%T)", v)
	slog.Error(locales.UnexpectedBackgroundError(), "detail", err.Error(), "stack", string(stack))
	onUnexpectedError(err)
}

func inGoTest() bool {
	name := filepath.Base(os.Args[0])
	return strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".test.exe")
}
