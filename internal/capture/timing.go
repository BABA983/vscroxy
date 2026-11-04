package capture

import (
	"cmp"
	"context"
	"crypto/tls"
	"net/http/httptrace"
	"slices"
	"sync"
	"time"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// Timing is a proxy-observed request timeline, from request headers to capture
// publication. Setup before this request and downstream delivery are excluded.
// Phases may overlap (connection acquisition contains DNS/TCP/TLS; a duplex
// upload can continue after the first response byte). Never sum them as a total.
type Timing struct {
	TotalNS   int64         `json:"totalNS"`
	Phases    []TimingPhase `json:"phases"`
	Reused    bool          `json:"reused,omitzero"`
	Truncated bool          `json:"truncated,omitzero"`
}

// TimingPhase retains sub-millisecond precision and its actual start offset.
// Incomplete means the phase had not ended when this snapshot was published.
type TimingPhase struct {
	Name       string `json:"name"`
	StartNS    int64  `json:"startNS"`
	DurationNS int64  `json:"durationNS"`
	Detail     string `json:"detail,omitempty"`
	Failed     bool   `json:"failed,omitzero"`
	Incomplete bool   `json:"incomplete,omitzero"`
}

type timingKey struct{}

type timedPhase struct {
	TimingPhase
	key string
}

// httptrace callbacks can run concurrently, and late dial callbacks can outlive
// the request. Only immutable copies cross the capture boundary.
type timingRecorder struct {
	mu        sync.Mutex
	start     time.Time
	phases    []timedPhase
	first     bool
	reused    bool
	truncated bool
}

func startTiming(f *proxy.Flow, start time.Time) {
	raw := f.Request.Raw()
	if raw == nil {
		return
	}
	r := &timingRecorder{start: start}
	ctx := context.WithValue(raw.Context(), timingKey{}, r)
	*raw = *raw.WithContext(httptrace.WithClientTrace(ctx, r.trace(f)))
}

func (r *timingRecorder) begin(name, key, detail string) {
	if len(r.phases) == 128 {
		r.truncated = true
		return
	}
	r.phases = append(r.phases, timedPhase{TimingPhase: TimingPhase{
		Name: name, StartNS: time.Since(r.start).Nanoseconds(), Detail: detail, Incomplete: true,
	}, key: key})
}

func (r *timingRecorder) end(name, key string, err error) {
	for i := len(r.phases) - 1; i >= 0; i-- {
		p := &r.phases[i]
		if p.Name == name && p.key == key && p.Incomplete {
			p.DurationNS, p.Incomplete, p.Failed = time.Since(r.start).Nanoseconds()-p.StartNS, false, err != nil
			return
		}
	}
}

func (r *timingRecorder) trace(f *proxy.Flow) *httptrace.ClientTrace {
	preTLS := f.ConnContext != nil && f.ConnContext.ServerConn != nil && f.ConnContext.ServerConn.TlsState() != nil
	return &httptrace.ClientTrace{
		GetConn: func(string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.begin("Connection", "", "Transport acquisition; may overlap DNS, TCP and TLS")
		},
		GotConn: func(info httptrace.GotConnInfo) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.reused = info.Reused
			r.end("Connection", "", nil)
			r.begin("Send", "", "Writing the upstream request")
		},
		DNSStart: func(info httptrace.DNSStartInfo) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.begin("DNS", "", info.Host)
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.end("DNS", "", info.Err)
		},
		ConnectStart: func(network, addr string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.begin("TCP", network+" "+addr, network+" "+addr)
		},
		ConnectDone: func(network, addr string, err error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.end("TCP", network+" "+addr, err)
		},
		TLSHandshakeStart: func() {
			// net/http calls HandshakeContext even on the already-handshaken
			// TLS connection returned by the MITM dialer. That no-op is not
			// this request paying for TLS. A rewritten target uses a real dial.
			if preTLS && !f.UseSeparateClient {
				return
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			r.begin("TLS", "", "Upstream TLS handshake")
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.end("TLS", "", err)
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.end("Send", "", info.Err)
			if !r.first && info.Err == nil {
				r.begin("Wait", "", "Request written to first response byte")
			}
		},
		GotFirstResponseByte: func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if !r.first {
				r.first = true
				r.end("Wait", "", nil)
				r.begin("Receive", "", "First response byte to capture publication")
			}
		},
	}
}

func snapshotTiming(f *proxy.Flow, now time.Time, complete bool) *Timing {
	if f.Request.Raw() == nil {
		return nil
	}
	r, _ := f.Request.Raw().Context().Value(timingKey{}).(*timingRecorder)
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	timing := &Timing{TotalNS: max(0, now.Sub(r.start).Nanoseconds()), Reused: r.reused, Truncated: r.truncated}
	first := timing.TotalNS
	for _, recorded := range r.phases {
		p := recorded.TimingPhase
		p.StartNS = min(p.StartNS, timing.TotalNS)
		first = min(first, p.StartNS)
		if p.Incomplete {
			p.DurationNS = timing.TotalNS - p.StartNS
			p.Failed = f.Error != nil
			if p.Name == "Receive" && complete {
				p.Incomplete = false
			}
		} else {
			p.DurationNS = min(p.DurationNS, timing.TotalNS-p.StartNS)
		}
		timing.Phases = append(timing.Phases, p)
	}
	timing.Phases = append(timing.Phases, TimingPhase{Name: "Proxy", DurationNS: first,
		Detail: "Client upload, proxy rules and buffering before upstream activity"})
	slices.SortStableFunc(timing.Phases, func(a, b TimingPhase) int { return cmp.Compare(a.StartNS, b.StartNS) })
	return timing
}
