// Package leakcheck checks that a test binary has not left its own
// goroutines behind. It diffs runtime.Stack snapshots and uses only the
// standard library; tests close their servers and sessions through the
// package's own shutdown paths before this check runs.
package leakcheck

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type stack struct {
	count int
	text  string
}

type snapshot map[string]stack

// ignored names backgrounds the runtime, the test harness and MyGo start on
// their own. They live for the whole test binary; goroutines started by
// application code are not ignored here.
var ignored = []string{
	"runtime.", // GC workers, sweeper, finalizers, timer processor
	"testing.", // the test runner and its parallel-test guards
	"github.com/egoist/mygo/internal/raster.help", // MyGo's raster helper pool
}

// Check keeps the baseline a package's tests start from.
type Check struct{ before snapshot }

// Start snapshots the goroutines alive before m.Run.
func Start() *Check { return &Check{before: takeSnapshot()} }

// Run starts a check, runs the tests, then fails the process when goroutines
// the tests started are still alive. A nonzero test exit code is kept.
func Run(m *testing.M, extra ...string) { RunWith(m, nil, nil, extra...) }

// RunWith does the same around a package's own TestMain setup and teardown.
// before runs before the baseline snapshot, after runs before the check, so a
// package's own Close paths are what let its goroutines finish.
func RunWith(m *testing.M, before, after func(), extra ...string) {
	if before != nil {
		before()
	}
	check := Start()
	code := m.Run()
	if after != nil {
		after()
	}
	if leaks := check.diff(extra); len(leaks) > 0 {
		report(leaks)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func takeSnapshot() snapshot {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	out := make(snapshot)
	for _, block := range strings.Split(string(buf), "\n\n") {
		name := identity(block)
		if name == "" || slices.ContainsFunc(ignored, func(prefix string) bool { return strings.HasPrefix(name, prefix) }) {
			continue
		}
		s := out[name]
		s.count++
		if s.text == "" {
			s.text = strings.TrimSpace(block)
		}
		out[name] = s
	}
	return out
}

// identity is the first frame from a module, not the runtime or standard
// library frame left on top of a parked stack. A module path has a dot in its
// first segment; that keeps a goroutine keyed to its owner rather than to
// runtime.chansend or internal/poll.Accept. Function names have no addresses,
// so two runs of the same goroutine share a key.
func identity(block string) string {
	for i, line := range strings.Split(block, "\n") {
		if i == 0 { // "goroutine 12 [chan receive]:"
			continue
		}
		frame := strings.TrimSpace(line)
		// The last parenthesis opens the argument list; pointer receivers
		// have an earlier one that belongs to the function's name.
		name := frame
		if at := strings.LastIndexByte(name, '('); at > 0 {
			name = name[:at]
		}
		if name == "" {
			continue
		}
		slash := strings.IndexByte(name, '/')
		if slash < 0 || !strings.Contains(name[:slash], ".") {
			continue
		}
		return name
	}
	return ""
}

type leak struct {
	name  string
	delta int
	stack string
}

func (c *Check) diff(extra []string) []leak {
	after := takeSnapshot()
	var leaks []leak
	for name, now := range after {
		delta := now.count - c.before[name].count
		if delta <= 0 || slices.Contains(extra, name) {
			continue
		}
		leaks = append(leaks, leak{name: name, delta: delta, stack: now.text})
	}
	slices.SortFunc(leaks, func(a, b leak) int { return strings.Compare(a.name, b.name) })
	return leaks
}

func report(leaks []leak) {
	fmt.Fprintf(os.Stderr, "goroutine leak check: %d group(s) still running after tests\n", len(leaks))
	for _, l := range leaks {
		fmt.Fprintf(os.Stderr, "\n%s x%d\n%s\n", l.name, l.delta, l.stack)
	}
}
