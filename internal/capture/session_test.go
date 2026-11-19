package capture

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A load session sees the proxy's synthetic gateway response as a transport
// error, not as an origin's 502, so a failed certificate stays a network error.
func TestReplaySessionTurnsProxyGenerated502IntoError(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "untrusted")
	}))
	defer upstream.Close()
	srv, _ := startServer(t)
	srv.SetSSL(SSLConfig{All: true})
	session, err := srv.ReplaySession(t.Context(), Flow{DebugRun: "load"}, TransferPolicy{TLS: TLSVerify})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	req, err := http.NewRequestWithContext(t.Context(), "GET", upstream.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.RoundTrip(req); err == nil || !strings.Contains(strings.ToLower(err.Error()), "certificate") {
		t.Fatalf("load session response error = %v, want the certificate failure", err)
	}
}

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
	waitLinks := func(want int) {
		t.Helper()
		waitState(t, srv, func() bool {
			srv.mu.Lock()
			defer srv.mu.Unlock()
			return len(srv.replayClients) == want
		}, func() string {
			srv.mu.Lock()
			defer srv.mu.Unlock()
			return fmt.Sprintf("suppressed links = %d, want %d", len(srv.replayClients), want)
		})
	}
	waitLinks(1)
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
	waitLinks(0)
}
