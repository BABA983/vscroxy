package runner

import (
	"context"
	"errors"
	"maps"
	"net/http/cookiejar"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"golang.org/x/net/publicsuffix"
)

// LoadOptions describes a bounded, closed workload, not an arrival-rate target.
// Each user repeats the same saved steps, retaining its own state between loops.
// Zero timeout and attempt budget use 30 seconds and 100,000 respectively.
type LoadOptions struct {
	Users         int
	Duration      time.Duration
	Timeout       time.Duration
	MaxAttempts   int
	StopOnFailure bool
	Vars          map[string]string
}

func (o LoadOptions) Validate() error {
	if o.Users < 1 || o.Users > 64 || o.Duration < time.Second || o.Duration > 10*time.Minute || o.Timeout < 0 || o.Timeout > time.Minute || o.MaxAttempts < 0 || o.MaxAttempts > 1000000 {
		return errors.New("use 1–64 users, 1s–10m duration, up to 1m request timeout and 1–1000000 attempts")
	}
	return nil
}

// LoadReport contains only aggregates and saved request IDs, never execution
// objects, URLs, scripts, console messages, variables, headers or bodies.
type LoadReport struct {
	Version        int           `json:"version"`
	StartedAt      time.Time     `json:"startedAt"`
	DurationMillis int64         `json:"durationMillis"`
	Users          int           `json:"users"`
	Done           bool          `json:"done"`
	Reason         string        `json:"reason"`
	Total          LoadMetrics   `json:"total"`
	Requests       []LoadRequest `json:"requests"`
}

type LoadRequest struct {
	Index int         `json:"index"`
	ID    string      `json:"id"`
	Stats LoadMetrics `json:"stats"`
}

// Completed excludes skipped and load-interrupted attempts. Failed includes
// setup/network/script/check errors and unexpected HTTP errors, as Runner does.
// Errors counts Execution.Err, not failed assertions. Bytes/HTTPResponses refer
// only to the primary response, not OAuth, redirects or pm.sendRequest traffic.
type LoadMetrics struct {
	Attempts        int64   `json:"attempts"`
	Completed       int64   `json:"completed"`
	Failed          int64   `json:"failed"`
	Skipped         int64   `json:"skipped"`
	Interrupted     int64   `json:"interrupted"`
	Errors          int64   `json:"errors"`
	HTTPResponses   int64   `json:"httpResponses"`
	ResponseBytes   int64   `json:"responseBytes"`
	RequestsPerSec  float64 `json:"requestsPerSecond"`
	FailureRate     float64 `json:"failureRate"`
	LatencySamples  int64   `json:"latencySamples"`
	LatencyOverflow int64   `json:"latencyOverflow"`
	MeanMillis      float64 `json:"meanMillis"`
	P50Millis       float64 `json:"p50Millis"`
	P90Millis       float64 `json:"p90Millis"`
	P95Millis       float64 `json:"p95Millis"`
	P99Millis       float64 `json:"p99Millis"`
	MaxMillis       float64 `json:"maxMillis"`
}

type loadMeter struct {
	stats LoadMetrics
	hist  *hdrhistogram.Histogram
}

func newLoadMeter() loadMeter {
	return loadMeter{hist: hdrhistogram.New(1, time.Hour.Microseconds(), 3)}
}

func (m *loadMeter) record(r Result, interrupted bool) {
	m.stats.Attempts++
	switch {
	case r.Execution.Skipped:
		m.stats.Skipped++
		return
	case interrupted:
		m.stats.Interrupted++
		return
	}
	m.stats.Completed++
	if r.Execution.Failed() {
		m.stats.Failed++
	}
	if r.Execution.Err != nil {
		m.stats.Errors++
	}
	if response := r.Execution.Response; response != nil {
		m.stats.HTTPResponses++
		m.stats.ResponseBytes += int64(response.Bytes)
	}
	if err := m.hist.RecordValue(max(1, r.Duration.Microseconds())); err != nil {
		m.stats.LatencyOverflow++
	}
}

func (m *loadMeter) snapshot(elapsed time.Duration) LoadMetrics {
	s := m.stats
	if elapsed > 0 {
		s.RequestsPerSec = float64(s.Completed) / elapsed.Seconds()
	}
	if s.Completed > 0 {
		s.FailureRate = float64(s.Failed) / float64(s.Completed)
	}
	s.LatencySamples = m.hist.TotalCount()
	s.MeanMillis = m.hist.Mean() / 1000
	s.P50Millis = float64(m.hist.ValueAtQuantile(50)) / 1000
	s.P90Millis = float64(m.hist.ValueAtQuantile(90)) / 1000
	s.P95Millis = float64(m.hist.ValueAtQuantile(95)) / 1000
	s.P99Millis = float64(m.hist.ValueAtQuantile(99)) / 1000
	s.MaxMillis = float64(m.hist.Max()) / 1000
	return s
}

var errLoadDuration = errors.New("load duration reached")
var errLoadFailure = errors.New("load stopped on failure")
var errLoadBranch = errors.New("unknown next request")

// RunLoad reuses Runner, including inheritance, pm scripts/checks, auth and
// branching. Only HTTP(S) steps are supported. Inputs must remain immutable
// until return. State writes are per-user and never persisted; Vault script
// access and caller cookie/token caches are deliberately not inherited.
//
// emit receives serialized aggregate snapshots at most four times per second
// plus a final snapshot. It must return promptly and must not call back into the
// engine. Memory is bounded by selected steps/users, not attempt count.
// Duration completion returns nil; parent cancellation/deadline, exhausted
// attempt budget and invalid branching return an error with a controlled reason.
func RunLoad(ctx context.Context, srv *capture.Server, steps []Step, opts LoadOptions, emit func(LoadReport)) (LoadReport, error) {
	if err := opts.Validate(); err != nil {
		return LoadReport{}, err
	}
	if len(steps) < 1 || len(steps) > 200 || srv == nil || !srv.Running() {
		return LoadReport{}, errors.New("choose 1–200 HTTP requests and start the local proxy first")
	}
	for _, step := range steps {
		// Resolve variable-based schemes inside Execute; explicit WebSocket
		// requests have different lifetimes and are not a load scenario.
		if step.Request.Method == "WS" || step.Request.Method == "WSS" || strings.HasPrefix(strings.ToLower(step.Request.URL), "ws:") || strings.HasPrefix(strings.ToLower(step.Request.URL), "wss:") {
			return LoadReport{}, errors.New("load runs support HTTP(S) requests only")
		}
	}
	if opts.MaxAttempts == 0 {
		opts.MaxAttempts = 100000
	}
	started := time.Now()
	durationCtx, stopDuration := context.WithTimeoutCause(ctx, opts.Duration, errLoadDuration)
	defer stopDuration()
	loadCtx, stop := context.WithCancelCause(durationCtx)
	defer stop(nil)
	loadCtx = capture.WithoutRecording(loadCtx)
	// A per-step child may observe the shared deadline just before the
	// parent's timer callback runs. Classify by that deadline as well as Err.
	loadCause := func() error {
		if cause := context.Cause(loadCtx); cause != nil {
			return cause
		}
		if deadline, ok := loadCtx.Deadline(); ok && !time.Now().Before(deadline) {
			if parentDeadline, ok := ctx.Deadline(); ok && !parentDeadline.After(deadline) {
				return context.DeadlineExceeded
			}
			return errLoadDuration
		}
		return nil
	}
	total := newLoadMeter()
	meters := make([]loadMeter, len(steps))
	for i := range meters {
		meters[i] = newLoadMeter()
	}
	snapshot := func(done bool, reason string) LoadReport {
		elapsed := time.Since(started)
		r := LoadReport{Version: 1, StartedAt: started.UTC(), DurationMillis: elapsed.Milliseconds(), Users: opts.Users, Done: done, Reason: reason, Total: total.snapshot(elapsed), Requests: make([]LoadRequest, len(steps))}
		for i := range steps {
			r.Requests[i] = LoadRequest{Index: i, ID: steps[i].Request.ID, Stats: meters[i].snapshot(elapsed)}
		}
		return r
	}
	var reserved atomic.Int64
	reserve := func() error {
		if reserved.Add(1) > int64(opts.MaxAttempts) {
			stop(ErrStepLimit)
			return ErrStepLimit
		}
		return nil
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	lastEmit := started
	for range opts.Users {
		wg.Go(func() {
			jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
			tokens := &request.Tokens{}
			userSteps := slices.Clone(steps)
			for i := range userSteps {
				userSteps[i].Context.Tokens = tokens
				userSteps[i].Context.ScriptVault = false
				userSteps[i].Context.HTTPOnly = true
			}
			_, err := run(loadCtx, srv, userSteps, Options{Iterations: 1, Timeout: opts.Timeout, Vars: maps.Clone(opts.Vars), Cookies: jar, StopOnFailure: opts.StopOnFailure}, true, reserve, func(r Result) error {
				cause := loadCause()
				interrupted := cause != nil && (errors.Is(r.Execution.Err, cause) || errors.Is(r.Execution.Err, context.Canceled) || errors.Is(r.Execution.Err, context.DeadlineExceeded))
				mu.Lock()
				total.record(r, interrupted)
				meters[r.Index].record(r, interrupted)
				if emit != nil && time.Since(lastEmit) >= 250*time.Millisecond {
					emit(snapshot(false, "running"))
					lastEmit = time.Now()
				}
				mu.Unlock()
				if !interrupted && opts.StopOnFailure && r.Execution.Failed() {
					stop(errLoadFailure)
				}
				if next := r.Execution.Next; next != nil && *next != "" && nextStep(userSteps, r.Index, next) == len(userSteps) {
					return errLoadBranch
				}
				return nil
			})
			if err != nil && loadCtx.Err() == nil {
				if cause := loadCause(); cause != nil {
					stop(cause)
				} else {
					stop(err)
				}
			}
		})
	}
	wg.Wait()
	err := loadCause()
	reason := "finished" // all users explicitly stopped via setNextRequest(null)
	switch {
	case errors.Is(err, errLoadDuration):
		reason, err = "duration", nil
	case errors.Is(err, errLoadFailure):
		reason, err = "failure", nil
	case errors.Is(err, ErrStepLimit):
		reason = "attempt-limit"
	case errors.Is(err, context.Canceled):
		reason = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		reason = "timed-out"
	case err != nil:
		reason = "execution-error"
	}
	report := snapshot(true, reason)
	if emit != nil {
		emit(report)
	}
	return report, err
}
