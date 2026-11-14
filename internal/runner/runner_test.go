package runner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/ntlmtest"
	"github.com/baba983/vscroxy/internal/request"
)

// runnerProxy starts a capture proxy on an OS-assigned loopback port.
func runnerProxy(t *testing.T) *capture.Server {
	t.Helper()
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv
}

func TestRequestTimeoutAppliesInRunner(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	srv := runnerProxy(t)

	one := 1
	var got request.Execution
	summary, err := Run(t.Context(), srv, []Step{{
		Request: request.Request{Method: "GET", URL: slow.URL, Settings: request.Settings{TimeoutSeconds: &one}},
	}}, Options{Iterations: 1}, func(result Result) error {
		got = result.Execution
		return nil
	})
	if err != nil || summary.Completed != 1 {
		t.Fatalf("run = %+v, %v", summary, err)
	}
	if !errors.Is(got.Err, context.DeadlineExceeded) || got.Outcome() != "Timed out" {
		t.Fatalf("runner step = %+v, want a timeout", got)
	}
}

// On the desktop Runner (no caller cap), an explicit 0 on the request adds no
// per-step budget; the run's own context can still stop it.
func TestExplicitZeroTimeoutDoesNotAddADesktopBudget(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(300 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	srv := runnerProxy(t)

	zero := 0
	var got request.Execution
	summary, err := Run(t.Context(), srv, []Step{{
		Request: request.Request{Method: "GET", URL: slow.URL, Settings: request.Settings{TimeoutSeconds: &zero}},
	}}, Options{Iterations: 1}, func(result Result) error {
		got = result.Execution
		return nil
	})
	if err != nil || summary.Completed != 1 {
		t.Fatalf("run = %+v, %v", summary, err)
	}
	if got.Err != nil || got.Response == nil || got.Response.Status != 200 {
		t.Fatalf("runner step = %+v, want the 300ms response", got)
	}
}

// A caller-supplied per-request cap is authoritative. A shared request file
// cannot extend it with 0 (curl -m0) or a longer timeout.
func TestCallerTimeoutCapsRequestTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(4 * time.Second):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	srv := runnerProxy(t)

	zero, long := 0, 3600
	for _, tc := range []struct {
		name     string
		settings request.Settings
	}{
		{"request unlimited", request.Settings{TimeoutSeconds: &zero}},
		{"request longer", request.Settings{TimeoutSeconds: &long}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got request.Execution
			start := time.Now()
			summary, err := Run(t.Context(), srv, []Step{{
				Request: request.Request{Method: "GET", URL: slow.URL, Settings: tc.settings},
			}}, Options{Iterations: 1, Timeout: time.Second}, func(result Result) error {
				got = result.Execution
				return nil
			})
			elapsed := time.Since(start)
			if err != nil || summary.Completed != 1 {
				t.Fatalf("run = %+v, %v", summary, err)
			}
			if !errors.Is(got.Err, context.DeadlineExceeded) || got.Outcome() != "Timed out" {
				t.Fatalf("runner step = %+v, want the caller's 1s timeout", got)
			}
			if elapsed < 900*time.Millisecond || elapsed >= 2500*time.Millisecond {
				t.Fatalf("stopped after %s, want about 1s, not the server's 4s", elapsed)
			}
		})
	}
}

func TestNTLMRequestInRunner(t *testing.T) {
	auth := &ntlmtest.Server{Username: "ann", Password: "s3" + "cret", Domain: "EXAMPLE"}
	upstream := httptest.NewUnstartedServer(auth)
	upstream.Config.ConnContext = auth.ConnContext
	upstream.Start()
	defer upstream.Close()
	srv := runnerProxy(t)

	var got request.Execution
	summary, err := Run(t.Context(), srv, []Step{{
		Request: request.Request{Method: "GET", URL: upstream.URL + "/orders", Auth: request.Auth{
			Type: request.AuthNTLM, NTLM: request.NTLMAuth{Username: "ann", Password: "s3" + "cret", Domain: "EXAMPLE"},
		}},
	}}, Options{Iterations: 1}, func(result Result) error {
		got = result.Execution
		return nil
	})
	if err != nil || summary.Completed != 1 || summary.Failed != 0 {
		t.Fatalf("run = %+v, %v", summary, err)
	}
	if got.Err != nil || got.Response == nil || got.Response.Status != http.StatusOK || got.Response.ResponseBody != "authenticated" {
		t.Fatalf("runner step = %+v", got)
	}
	if auth.Connections() != 1 || len(auth.Requests()) != 3 {
		t.Errorf("connections=%d requests=%+v, want one connection with three steps", auth.Connections(), auth.Requests())
	}
}

// Three waits separate four attempts over two iterations: none before the
// first attempt, and one before each later attempt, including the first
// attempt of the second iteration.
func TestRequestDelaySpacesAttempts(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	srv := runnerProxy(t)

	const delay = 250 * time.Millisecond
	steps := []Step{
		{Request: request.Request{Method: "GET", URL: ok.URL}},
		{Request: request.Request{Method: "GET", URL: ok.URL}},
	}
	start := time.Now()
	summary, err := Run(t.Context(), srv, steps, Options{Iterations: 2, Delay: delay}, func(Result) error { return nil })
	elapsed := time.Since(start)
	if err != nil || summary.Completed != 4 {
		t.Fatalf("run = %+v, %v", summary, err)
	}
	if elapsed < 3*delay || elapsed >= 4*delay {
		t.Fatalf("elapsed %v, want three delays of %v", elapsed, delay)
	}
}

// A cancelled run does not wait out its delay before the next attempt.
func TestRequestDelayStopsWhenRunIsCancelled(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	srv := runnerProxy(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stop := time.AfterFunc(100*time.Millisecond, cancel)
	defer stop.Stop()
	steps := []Step{
		{Request: request.Request{Method: "GET", URL: ok.URL}},
		{Request: request.Request{Method: "GET", URL: ok.URL}},
	}
	start := time.Now()
	summary, err := Run(ctx, srv, steps, Options{Iterations: 1, Delay: time.Minute}, func(Result) error { return nil })
	if !errors.Is(err, context.Canceled) || summary.Completed != 1 {
		t.Fatalf("run = %+v, %v; want one attempt then cancellation", summary, err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("cancelled run took %v; the delay ignored cancellation", elapsed)
	}
}
