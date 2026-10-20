package capture

import (
	"io"
	"sync"
	"time"
)

// Throttle slows the proxy's traffic down, as a slow network would. A zero
// rate leaves that direction alone.
type Throttle struct {
	Name       string
	DownPerSec int
	UpPerSec   int
	Latency    time.Duration
}

// Presets are the network conditions the UI offers.
var Presets = []Throttle{
	{Name: "Off"},
	{Name: "3G", DownPerSec: 780_000, UpPerSec: 330_000, Latency: 100 * time.Millisecond},
	{Name: "4G", DownPerSec: 4_000_000, UpPerSec: 1_000_000, Latency: 20 * time.Millisecond},
	{Name: "DSL", DownPerSec: 2_000_000, UpPerSec: 256_000, Latency: 5 * time.Millisecond},
}

// SetThrottle replaces the network conditions.
func (s *Server) SetThrottle(t Throttle) {
	s.throttleMu.Lock()
	s.throttle = t
	s.throttleMu.Unlock()
}

// Throttle returns the conditions in force.
func (s *Server) Throttle() Throttle {
	s.throttleMu.RLock()
	defer s.throttleMu.RUnlock()
	return s.throttle
}

func (s *Server) throttleDown(in io.Reader) io.Reader {
	return throttleReader(in, s.Throttle(), true)
}

func (s *Server) throttleUp(in io.Reader) io.Reader {
	return throttleReader(in, s.Throttle(), false)
}

func throttleReader(in io.Reader, t Throttle, down bool) io.Reader {
	rate := t.UpPerSec
	if down {
		rate = t.DownPerSec
	}
	if in == nil || (rate <= 0 && t.Latency <= 0) {
		return in
	}
	return &throttledReader{in: in, rate: rate, latency: t.Latency, start: time.Now()}
}

// throttledReader paces reads to an average rate: after each read it sleeps
// until the bytes so far would have taken that long. The first read also
// waits for the latency.
type throttledReader struct {
	in      io.Reader
	rate    int
	latency time.Duration
	start   time.Time
	read    int64
	once    sync.Once
}

func (r *throttledReader) Read(p []byte) (int, error) {
	r.once.Do(func() {
		if r.latency > 0 {
			time.Sleep(r.latency)
		}
	})
	n, err := r.in.Read(p)
	r.read += int64(n)
	if r.rate > 0 {
		want := time.Duration(float64(r.read) / float64(r.rate) * float64(time.Second))
		if elapsed := time.Since(r.start); want > elapsed {
			time.Sleep(want - elapsed)
		}
	}
	return n, err
}
