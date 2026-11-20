package app

import (
	"testing"

	"github.com/baba983/vscroxy/internal/leakcheck"
)

func TestMain(m *testing.M) {
	// esbuild starts a one-shot stale-build cleanup after a module compile.
	// It sleeps for a fixed 250 ms and returns on its own; it is not work
	// the app or a test left running.
	leakcheck.Run(m, "github.com/evanw/esbuild/pkg/api.(*internalContext).rebuild.func1")
}
