package runner

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
)

func TestRequestTimeoutAppliesInRunner(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	srv, err := capture.New(addr, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

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

// An explicit 0 on the request removes the runner's per-request budget; the
// run's own context can still stop it.
func TestExplicitZeroTimeoutRemovesTheRunnerBudget(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(300 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	srv, err := capture.New(addr, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	zero := 0
	var got request.Execution
	summary, err := Run(t.Context(), srv, []Step{{
		Request: request.Request{Method: "GET", URL: slow.URL, Settings: request.Settings{TimeoutSeconds: &zero}},
	}}, Options{Iterations: 1, Timeout: 100 * time.Millisecond}, func(result Result) error {
		got = result.Execution
		return nil
	})
	if err != nil || summary.Completed != 1 {
		t.Fatalf("run = %+v, %v", summary, err)
	}
	if got.Err != nil || got.Response == nil || got.Response.Status != 200 {
		t.Fatalf("runner step = %+v, want the 300ms response without the 100ms budget", got)
	}
}
