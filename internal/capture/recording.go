package capture

import (
	"context"

	"github.com/google/uuid"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

type withoutRecordingKey struct{}

// WithoutRecording marks built-in sends through Replay or ReplayWithCookies
// not to record their exchanges. Derived contexts inherit the marker, including
// those used for auxiliary sends. Rules, scripts, throttles and replay responses
// are unchanged. Other traffic through the proxy is unaffected.
//
// This is an internal context capability, not an HTTP header: it is carried to
// the proxy only by the replay's own socket identity.
func WithoutRecording(ctx context.Context) context.Context {
	return context.WithValue(ctx, withoutRecordingKey{}, true)
}

func recordingSuppressed(f *proxy.Flow) bool {
	if f.Request != nil {
		if raw := f.Request.Raw(); raw != nil {
			origin, _ := raw.Context().Value(replaySourceKey{}).(replayOrigin)
			return origin.withoutRecording
		}
	}
	return false
}

// awaitUnrecorded forgets per-exchange metadata even when the engine never
// calls a publishing hook (large bodies, canceled uploads and tunnels). Wait
// for Done, not the replay caller: cancellation may return before the proxy
// finishes processing the bytes already sent. No capture body sink is allocated.
func (s *Server) awaitUnrecorded(id uuid.UUID, done <-chan struct{}) {
	select {
	case <-done:
	case <-s.closed:
	}
	s.mu.Lock()
	delete(s.ids, id)
	delete(s.started, id)
	delete(s.firstByte, id)
	delete(s.original, id)
	delete(s.dnsHits, id)
	delete(s.trailers, id)
	delete(s.flowIDs, id)
	delete(s.frameSeq, id)
	s.mu.Unlock()
}
