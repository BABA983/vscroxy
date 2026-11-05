package runner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
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
