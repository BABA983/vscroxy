package runner

import (
	"context"
	"errors"
	"fmt"
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

// Load profiles shape how users join the run. Constant starts every user at
// once; ramp starts them evenly across RampSeconds; spike adds SpikeUsers for
// SpikeSeconds starting at SpikeAtSeconds.
const (
	LoadProfileConstant = "constant"
	LoadProfileRamp     = "ramp"
	LoadProfileSpike    = "spike"
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
	// Profile is constant (the default), ramp or spike.
	Profile string
	// RampSeconds is how long a ramp takes to reach Users.
	RampSeconds int
	// SpikeUsers join at SpikeAtSeconds and leave after SpikeSeconds.
	SpikeUsers, SpikeSeconds, SpikeAtSeconds int
	// Data holds one row list per user, cycled by that user's loop. A user
	// beyond the list reuses a file; each user still has its own variables.
	Data [][]map[string]string
	// Thresholds are pass conditions evaluated against the final totals.
	Thresholds []LoadThreshold
}

func (o LoadOptions) Validate() error {
	if o.Users < 1 || o.Users > 64 || o.Duration < time.Second || o.Duration > 10*time.Minute || o.Timeout < 0 || o.Timeout > time.Minute || o.MaxAttempts < 0 || o.MaxAttempts > 1000000 {
		return errors.New("use 1–64 users, 1s–10m duration, up to 1m request timeout and 1–1000000 attempts")
	}
	switch o.Profile {
	case "", LoadProfileConstant:
	case LoadProfileRamp:
		if o.RampSeconds < 0 || time.Duration(o.RampSeconds)*time.Second > o.Duration {
			return errors.New("ramp must be between 0s and the duration")
		}
	case LoadProfileSpike:
		if o.SpikeUsers < 1 || o.Users+o.SpikeUsers > 64 || o.SpikeSeconds < 1 || time.Duration(o.SpikeSeconds)*time.Second > o.Duration || o.SpikeAtSeconds < 0 || time.Duration(o.SpikeAtSeconds+o.SpikeSeconds)*time.Second > o.Duration {
			return errors.New("a spike needs 1+ users, peak 1–64 users, and a 1s+ window inside the duration")
		}
	default:
		return errors.New("profile must be constant, ramp or spike")
	}
	for _, rows := range o.Data {
		if len(rows) > request.MaxDataRows {
			return fmt.Errorf("a data file may have at most %d rows", request.MaxDataRows)
		}
	}
	return nil
}

// userPlan is when one user joins and leaves; end zero means the run's end.
type userPlan struct {
	start, end time.Duration
}

// plans lays out the profile's users. Peak users is the largest number
// running at once.
func (o LoadOptions) plans() (plans []userPlan, peak int) {
	switch o.Profile {
	case LoadProfileRamp:
		ramp := time.Duration(o.RampSeconds) * time.Second
		for i := range o.Users {
			start := time.Duration(0)
			if o.Users > 1 {
				start = ramp * time.Duration(i) / time.Duration(o.Users)
			}
			plans = append(plans, userPlan{start: start})
		}
		return plans, o.Users
	case LoadProfileSpike:
		for range o.Users {
			plans = append(plans, userPlan{})
		}
		at := time.Duration(o.SpikeAtSeconds) * time.Second
		end := at + time.Duration(o.SpikeSeconds)*time.Second
		for range o.SpikeUsers {
			plans = append(plans, userPlan{start: at, end: end})
		}
		return plans, o.Users + o.SpikeUsers
	default:
		for range o.Users {
			plans = append(plans, userPlan{})
		}
		return plans, o.Users
	}
}

// LoadReport contains only aggregates and saved request IDs, never execution
// objects, URLs, scripts, console messages, variables, headers or bodies.
type LoadReport struct {
	Version        int       `json:"version"`
	StartedAt      time.Time `json:"startedAt"`
	DurationMillis int64     `json:"durationMillis"`
	Users          int       `json:"users"`
	// PeakUsers is the most users running at once; Profile is the shape.
	PeakUsers int           `json:"peakUsers"`
	Profile   string        `json:"profile"`
	Done      bool          `json:"done"`
	Reason    string        `json:"reason"`
	Total     LoadMetrics   `json:"total"`
	Requests  []LoadRequest `json:"requests"`
	// Thresholds and Comparison are added by the caller when configured.
	Thresholds []ThresholdResult `json:"thresholds,omitempty"`
	Comparison *LoadComparison   `json:"comparison,omitempty"`
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
		// Resolve variable-based schemes inside Execute; protocol documents and
		// explicit WebSocket requests have different lifetimes and are not a
		// closed load scenario.
		if step.Request.Protocol != "" || step.Request.Method == "WS" || step.Request.Method == "WSS" || strings.HasPrefix(strings.ToLower(step.Request.URL), "ws:") || strings.HasPrefix(strings.ToLower(step.Request.URL), "wss:") {
			return LoadReport{}, errors.New("load runs support HTTP(S) requests only")
		}
	}
	if opts.MaxAttempts == 0 {
		opts.MaxAttempts = 100000
	}
	started := time.Now()
	plans, peak := opts.plans()
	if opts.Profile == "" {
		opts.Profile = LoadProfileConstant
	}
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
		r := LoadReport{Version: 1, StartedAt: started.UTC(), DurationMillis: elapsed.Milliseconds(), Users: opts.Users, PeakUsers: peak, Profile: opts.Profile, Done: done, Reason: reason, Total: total.snapshot(elapsed), Requests: make([]LoadRequest, len(steps))}
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
	for i, plan := range plans {
		wg.Go(func() {
			if plan.start > 0 {
				timer := time.NewTimer(plan.start)
				select {
				case <-timer.C:
				case <-loadCtx.Done():
					timer.Stop()
					return
				}
			}
			userCtx := loadCtx
			if plan.end > 0 {
				var cancel context.CancelFunc
				userCtx, cancel = context.WithDeadline(loadCtx, started.Add(plan.end))
				defer cancel()
			}
			jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
			tokens := &request.Tokens{}
			userSteps := slices.Clone(steps)
			for j := range userSteps {
				userSteps[j].Context.Tokens = tokens
				userSteps[j].Context.ScriptVault = false
				userSteps[j].Context.HTTPOnly = true
			}
			var rows []map[string]string
			if len(opts.Data) > 0 {
				rows = opts.Data[i%len(opts.Data)]
			}
			_, err := run(userCtx, srv, userSteps, Options{Iterations: 1, Timeout: opts.Timeout, Vars: maps.Clone(opts.Vars), Cookies: jar, StopOnFailure: opts.StopOnFailure, Data: rows, CycleData: true}, true, reserve, func(r Result) error {
				cause := loadCause()
				interrupted := cause != nil && (errors.Is(r.Execution.Err, cause) || errors.Is(r.Execution.Err, context.Canceled) || errors.Is(r.Execution.Err, context.DeadlineExceeded))
				// A spike user leaving on schedule interrupts its own attempt;
				// that is not a failed request or a whole-run error.
				if !interrupted && userCtx.Err() != nil && (errors.Is(r.Execution.Err, context.Canceled) || errors.Is(r.Execution.Err, context.DeadlineExceeded)) {
					interrupted = true
				}
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
			if err != nil && loadCtx.Err() == nil && userCtx.Err() == nil {
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
	report.Thresholds = EvaluateThresholds(opts.Thresholds, report.Total)
	if emit != nil {
		emit(report)
	}
	return report, err
}
