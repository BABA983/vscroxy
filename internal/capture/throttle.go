package capture

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/graphql"
	"github.com/baba983/vscroxy/internal/rules"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// Throttle simulates conditions for HTTP, inspected HTTPS and SSE. Rates
// limit each body's wire bytes, not the connection's aggregate bandwidth.
// Latency is paid once before forwarding, including for empty bodies.
// FailurePercent disconnects requests, not TCP packets. Zero rates are unlimited.
// Keep zero conditions in JSON so an explicit Off is not an empty object
// omitted by Rule.Network's omitempty tag.
type Throttle struct {
	Name           string        `json:"name,omitempty"`
	DownPerSec     int           `json:"downPerSec"`
	UpPerSec       int           `json:"upPerSec"`
	Latency        time.Duration `json:"latency"`
	FailurePercent int           `json:"failurePercent"`
}

// JSON durations are lossless nanoseconds, including programmatically imported
// values finer than the editor's whole-millisecond granularity.
type throttleJSON struct {
	Name           string `json:"name,omitempty"`
	DownPerSec     int    `json:"downPerSec"`
	UpPerSec       int    `json:"upPerSec"`
	Latency        int64  `json:"latency"`
	FailurePercent int    `json:"failurePercent"`
}

func (t Throttle) MarshalJSON() ([]byte, error) {
	return json.Marshal(throttleJSON{t.Name, t.DownPerSec, t.UpPerSec, int64(t.Latency), t.FailurePercent})
}

func (t *Throttle) UnmarshalJSON(data []byte) error {
	var wire throttleJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*t = Throttle{wire.Name, wire.DownPerSec, wire.UpPerSec, time.Duration(wire.Latency), wire.FailurePercent}
	return nil
}

// Presets are the network conditions the UI offers.
var Presets = []Throttle{
	{Name: "Off"},
	{Name: "3G", DownPerSec: 780_000, UpPerSec: 330_000, Latency: 100 * time.Millisecond},
	{Name: "4G", DownPerSec: 4_000_000, UpPerSec: 1_000_000, Latency: 20 * time.Millisecond},
	{Name: "DSL", DownPerSec: 2_000_000, UpPerSec: 256_000, Latency: 5 * time.Millisecond},
}

// Validate bounds both editor input and programmatically supplied conditions.
func (t Throttle) Validate() error {
	if t.DownPerSec < 0 || t.DownPerSec > 1<<30 || t.UpPerSec < 0 || t.UpPerSec > 1<<30 {
		return errors.New("network rates must be from 0 to 1073741824 bytes/s")
	}
	if t.Latency < 0 || t.Latency > time.Minute {
		return errors.New("network latency must be from 0 to 60 seconds")
	}
	if t.FailurePercent < 0 || t.FailurePercent > 100 {
		return errors.New("network failure must be from 0 to 100 percent")
	}
	return nil
}

// SetThrottle replaces the global conditions for subsequent exchanges. Each
// exchange retains its selected conditions until its body has finished.
func (s *Server) SetThrottle(t Throttle) error {
	if err := t.Validate(); err != nil {
		return err
	}
	s.throttleMu.Lock()
	s.throttle = t
	s.throttleMu.Unlock()
	return nil
}

// Throttle returns the conditions in force.
func (s *Server) Throttle() Throttle {
	s.throttleMu.RLock()
	defer s.throttleMu.RUnlock()
	return s.throttle
}

func (s *Server) flowThrottle(f *proxy.Flow) Throttle {
	if d := debugOf(f); d != nil && d.network != nil {
		return *d.network
	}
	return Throttle{}
}

var errNetworkFailure = errors.New("simulated network request disconnect")

// applyNetwork runs once, after buffered bodies are available for matching
// and before scripts/routing change the request. Large bodies reach it from
// the request modifier; pre-marked streaming requests reach it at headers.
func (s *Server) applyNetwork(f *proxy.Flow) bool {
	d := debugOf(f)
	if d == nil || d.network != nil || isWebSocket(f.Request.URL) {
		return false
	}
	t := s.Throttle()
	list := s.Rules()
	var req rules.Request
	if slices.ContainsFunc(list, func(r Rule) bool {
		return r.Type == RuleNetwork && r.Operation != "" && !r.Disabled && (r.Scenario == "" || r.ScenarioActive)
	}) {
		req = s.networkRuleRequest(f)
	} else {
		req = s.ruleRequest(f)
	}
	i := firstNetworkRule(list, req)
	if i >= 0 {
		r := list[i]
		t = *r.Network // including explicit Off; never merge with the global profile
		s.hit(r)
		if r.Scenario != "" {
			recordAction(f, "Scenario · "+r.Scenario)
		}
		recordAction(f, "Network rule · "+r.Name+" · "+r.Match)
	}
	d.network = &t
	if i >= 0 || t.DownPerSec != 0 || t.UpPerSec != 0 || t.Latency != 0 || t.FailurePercent != 0 {
		recordAction(f, "Network · "+t.Name)
		recordAction(f, fmt.Sprintf("Network conditions · down %d B/s · up %d B/s · latency %s · failure %d%%", t.DownPerSec, t.UpPerSec, t.Latency, t.FailurePercent))
	}
	err := networkWait(f.Request.Raw().Context(), s.closed, nil, t.Latency)
	if err == nil && t.FailurePercent > 0 && rand.IntN(100) < t.FailurePercent {
		err = errNetworkFailure
	}
	if err == nil {
		return false
	}
	f.Error, d.drop = err, true
	if f.ConnContext != nil {
		f.ConnContext.RecordEvent("Network", "Request disconnected", err)
	}
	s.answer(f, &proxy.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
		Body:       []byte("vscroxy: " + err.Error() + "\n"),
	})
	return true
}

// Unlike later rule hooks, network selection supplies exactly one snapshot:
// parsing GraphQL must not reread the live rules. Never treat a streamed
// prefix as a body.
func (s *Server) networkRuleRequest(f *proxy.Flow) rules.Request {
	req := rules.Request{URL: f.Request.URL.String(), Method: f.Request.Method, Host: f.Request.URL.Hostname(),
		App: s.clientProcess(f).Name, Header: f.Request.Header}
	var body []byte
	if req.Method == http.MethodPost {
		if f.Stream || f.Request.Body == nil || len(f.Request.Body) > 1<<20 {
			return req
		}
		decoder, err := newDecoder(req.Header.Get("Content-Encoding"), bytes.NewReader(f.Request.Body))
		if err != nil {
			return req
		}
		defer decoder.Close()
		body, err = io.ReadAll(io.LimitReader(decoder, 1<<20+1))
		if err != nil || len(body) > 1<<20 {
			return req
		}
	}
	operations, err := graphql.RequestOperations(req.Method, req.URL, req.Header.Get("Content-Type"), string(body))
	if err == nil {
		for _, op := range operations {
			if op.Name != "" {
				req.Operations = append(req.Operations, op.Name)
			}
		}
	}
	return req
}

func (s *Server) throttleDown(f *proxy.Flow, in io.Reader) io.Reader {
	rate := s.flowThrottle(f).DownPerSec
	if rate == 0 {
		return in
	}
	// Buffered responses reach this hook with a nil reader. Move their bytes
	// to the paced reader so reply cannot bypass it or write the body twice.
	if rate > 0 && in == nil && f.Response != nil && len(f.Response.Body) > 0 {
		in = bytes.NewReader(f.Response.Body)
		f.Response.Body = nil
	}
	return throttleReader(f.Request.Raw().Context(), s.closed, in, rate)
}

func (s *Server) throttleUp(f *proxy.Flow, in io.Reader) io.Reader {
	rate := s.flowThrottle(f).UpPerSec
	if rate == 0 {
		return in
	}
	return throttleReader(f.Request.Raw().Context(), s.closed, in, rate)
}

func throttleReader(ctx context.Context, shutdown <-chan struct{}, in io.Reader, rate int) io.Reader {
	if in == nil || rate == 0 {
		return in
	}
	return &throttledReader{in: in, rate: rate, ctx: ctx, shutdown: shutdown, closed: make(chan struct{})}
}

// Limit each read to at most a tenth of a second's allowance (and 32 KiB).
// Pace from the first read, not wrapper construction: response buffering or
// script/breakpoint time must not grant an unbounded initial burst.
type throttledReader struct {
	in       io.Reader
	rate     int
	ctx      context.Context
	shutdown <-chan struct{}
	closed   chan struct{}
	close    sync.Once
	closeErr error
	start    time.Time
	read     int64
}

func (r *throttledReader) Read(p []byte) (int, error) {
	if err := networkWait(r.ctx, r.shutdown, r.closed, 0); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.start.IsZero() {
		r.start = time.Now()
	}
	n, err := r.in.Read(p[:min(len(p), max(1, min(32<<10, r.rate/10)))])
	r.read += int64(n)
	want := time.Duration(float64(r.read) / float64(r.rate) * float64(time.Second))
	if waitErr := networkWait(r.ctx, r.shutdown, r.closed, want-time.Since(r.start)); waitErr != nil {
		return 0, waitErr
	}
	return n, err
}

func (r *throttledReader) Close() error {
	r.close.Do(func() {
		close(r.closed)
		if closer, ok := r.in.(io.Closer); ok {
			r.closeErr = closer.Close()
		}
	})
	return r.closeErr
}

func networkWait(ctx context.Context, shutdown, closed <-chan struct{}, delay time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-shutdown:
		return context.Canceled
	case <-closed:
		return io.ErrClosedPipe
	default:
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-shutdown:
		return context.Canceled
	case <-closed:
		return io.ErrClosedPipe
	}
}
