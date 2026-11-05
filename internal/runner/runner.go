// Package runner executes a collection without a window. Desktop and headless
// callers supply the same requests, inheritance, proxy and cookie jar.
package runner

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"
	"uuid"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
)

type Step struct {
	Request request.Request
	Context request.Context
}

type Options struct {
	Iterations int
	// Timeout is the caller's per-request cap; zero uses the desktop default
	// of 30 seconds and lets a request's own timeout raise or remove it. A
	// non-zero caller cap always wins over a request's longer or unlimited
	// setting.
	Timeout       time.Duration
	MaxSteps      int // zero is unlimited; counts script jumps too
	StopOnFailure bool
	Data          []map[string]string
	Vars          map[string]string
	Cookies       http.CookieJar
}

// Result is one completed attempt, including an in-flight attempt cancelled by
// its caller. Index addresses the input steps; Iteration starts at one.
type Result struct {
	Index, Iteration int
	RunKey           string
	Before           map[string]string
	Execution        request.Execution
	Console          []request.ConsoleMessage // execution output plus runner diagnostics
	Duration         time.Duration
}

type Summary struct {
	Completed, Failed, Skipped int
	Stopped                    bool // setNextRequest ended the run
}

var ErrStepLimit = errors.New("maximum number of request attempts reached")

// Run visits steps in order, following setNextRequest and carrying environment,
// local, global and collection writes across steps and iterations. Data repeats
// its last row when there are more iterations. Inputs are not changed. emit is
// synchronous; an error stops the run, so consumers control delivery/backpressure.
// Script writes are returned, never persisted by the engine.
func Run(ctx context.Context, srv *capture.Server, steps []Step, opts Options, emit func(Result) error) (Summary, error) {
	return run(ctx, srv, steps, opts, false, nil, emit)
}

// repeat is the load engine's unbounded iteration mode; reserve bounds total
// attempts across users before any scripts or network operations begin.
func run(ctx context.Context, srv *capture.Server, steps []Step, opts Options, repeat bool, reserve func() error, emit func(Result) error) (Summary, error) {
	var summary Summary
	if opts.Iterations < 1 || opts.Iterations > request.MaxDataRows {
		return summary, fmt.Errorf("iterations must be between 1 and %d", request.MaxDataRows)
	}
	if len(steps) == 0 {
		return summary, fmt.Errorf("choose at least one request")
	}
	if srv == nil || !srv.Running() {
		return summary, fmt.Errorf("start the local proxy first")
	}
	if opts.Timeout < 0 {
		return summary, fmt.Errorf("request timeout must be positive")
	}
	if opts.MaxSteps < 0 {
		return summary, fmt.Errorf("maximum steps must not be negative")
	}
	// Keep whether the caller supplied a cap before applying the desktop
	// default. Only the desktop Runner may let a request raise or remove it.
	callerTimeout := opts.Timeout
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	steps = slices.Clone(steps)
	vars := maps.Clone(opts.Vars)
	var locals map[string]string
	for iteration := 1; repeat || iteration <= opts.Iterations; iteration++ {
		for i := 0; i < len(steps); {
			if err := ctx.Err(); err != nil {
				return summary, err
			}
			if opts.MaxSteps > 0 && summary.Completed >= opts.MaxSteps {
				return summary, ErrStepLimit
			}
			if reserve != nil {
				if err := reserve(); err != nil {
					return summary, err
				}
			}
			step := steps[i]
			in := step.Context
			in.Iteration, in.Iterations, in.Locals = iteration-1, opts.Iterations, locals
			if repeat {
				in.Iterations = 0 // duration-limited: no predetermined iteration count
			}
			if len(opts.Data) > 0 {
				in.Data = opts.Data[min(iteration, len(opts.Data))-1]
			}
			before := maps.Clone(vars)
			// A request's own timeout may exceed the desktop default; leave
			// it a second to report its deadline before the runner's. A caller
			// cap (CLI, MCP or load) always wins: a request can shorten it,
			// while a request timeout of 0 means no extra limit, not "ignore
			// the caller". The desktop's explicit 0 removes its budget.
			timeout := opts.Timeout
			limit := step.Request.Timeout(in)
			if callerTimeout == 0 {
				if step.Request.TimeoutUnlimited(in) {
					timeout = 0
				} else if limit > 0 && limit+time.Second > timeout {
					timeout = limit + time.Second
				}
			} else if limit > 0 && limit+time.Second < timeout {
				timeout = limit + time.Second
			}
			var cancel context.CancelFunc
			var stepCtx context.Context
			if timeout > 0 {
				stepCtx, cancel = context.WithTimeout(ctx, timeout)
			} else {
				stepCtx, cancel = context.WithCancel(ctx)
			}
			key := uuid.New().String()
			start := time.Now()
			got := step.Request.Execute(request.WithRunKey(stepCtx, key), srv, in, vars, opts.Cookies)
			cancel()
			duration := time.Since(start)
			vars, locals = got.Vars, got.Locals
			for j := range steps {
				steps[j].Context = steps[j].Context.Apply(got.Writes, steps[j].Request.Collection)
			}
			next := nextStep(steps, i, got.Next)
			console := got.Console
			if got.Next != nil && next == len(steps) {
				summary.Stopped = true
				text := "pm.execution.setNextRequest(null) stopped the run"
				if *got.Next != "" {
					text = fmt.Sprintf("setNextRequest(%q): no such request in this run, so it stopped", *got.Next)
				}
				console = append(slices.Clone(console), request.ConsoleMessage{Phase: "runner", Kind: "warning", Text: text})
			}
			summary.Completed++
			if got.Failed() {
				summary.Failed++
			}
			if got.Skipped {
				summary.Skipped++
			}
			if err := emit(Result{Index: i, Iteration: iteration, RunKey: key, Before: before, Execution: got, Console: console, Duration: duration}); err != nil {
				return summary, err
			}
			if err := ctx.Err(); err != nil {
				return summary, err
			}
			if opts.StopOnFailure && got.Failed() || summary.Stopped {
				return summary, nil
			}
			i = next
		}
	}
	return summary, nil
}

func nextStep(steps []Step, i int, next *string) int {
	if next == nil {
		return i + 1
	}
	if at := slices.IndexFunc(steps, func(s Step) bool { return s.Request.Name == *next || s.Request.ID == *next }); at >= 0 && *next != "" {
		return at
	}
	return len(steps)
}
