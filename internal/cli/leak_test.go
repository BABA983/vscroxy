package cli_test

import (
	"testing"

	"github.com/baba983/vscroxy/internal/leakcheck"
)

// Headless commands own proxy, mock and watch goroutines; every test must
// leave them joined, including when a run or mock is cancelled.
func TestMain(m *testing.M) {
	leakcheck.Run(m)
}
