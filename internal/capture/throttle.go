package capture

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// Throttle simulates conditions for HTTP, inspected HTTPS and SSE. Rates
// limit each body's wire bytes, not the connection's aggregate bandwidth.
// Latency is paid once before forwarding, including for empty bodies.
// FailurePercent disconnects requests, not TCP packets. Zero rates are unlimited.
type Throttle struct {
	Name           string
	DownPerSec     int
	UpPerSec       int
	Latency        time.Duration
	FailurePercent int
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
	d.network = &t
	if t.DownPerSec != 0 || t.UpPerSec != 0 || t.Latency != 0 || t.FailurePercent != 0 {
		recordAction(f, "Network · "+t.Name)
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
