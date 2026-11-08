package capture

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A load run's session keeps its socket link until the proxy sees the
// connection close, exactly as ReplayWithPolicy does, so canceling the run
// cannot let its traffic into Capture.
func TestReplaySessionSuppressedLinksOutliveClose(t *testing.T) {
	srv, _ := startServer(t)
	received := make(chan struct{}, 1)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case received <- struct{}{}:
		default:
		}
		<-release
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(WithoutRecording(t.Context()))
	defer cancel()
	session, err := srv.ReplaySession(ctx, Flow{DebugRun: "load"}, TransferPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", upstream.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		res, err := session.RoundTrip(req)
		if err == nil {
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
		}
		done <- err
	}()
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("the upstream saw no request")
	}
	waitLinks := func(want int) bool {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			srv.mu.Lock()
			got := len(srv.replayClients)
			srv.mu.Unlock()
			if got == want {
				return true
			}
			time.Sleep(10 * time.Millisecond)
		}
		return false
	}
	if !waitLinks(1) {
		t.Fatal("the session did not register its socket")
	}
	session.Close()
	srv.mu.Lock()
	kept := len(srv.replayClients)
	srv.mu.Unlock()
	if kept != 1 {
		t.Fatalf("suppressed links after Close = %d, want kept until disconnect", kept)
	}

	cancel()
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	if !waitLinks(0) {
		t.Fatal("the suppressed link survived the connection's disconnect")
	}
}
