package capture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func assertNoRecording(t *testing.T, s *Server) {
	t.Helper()
	select {
	case f := <-s.Flows():
		t.Fatalf("suppressed exchange published: %s %s", f.Method, f.URL)
	case f := <-s.Frames():
		t.Fatalf("suppressed frame published for flow %d", f.FlowID)
	case <-time.After(50 * time.Millisecond):
	}
	files, err := os.ReadDir(s.bodyDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("suppressed exchange created spill files: %v", files)
	}
	if s.Dropped() != 0 || s.FrameDropped() != 0 {
		t.Fatal("suppressed traffic counted as dropped capture")
	}
}

// Public sends exercise the real proxy; inspect its maps only to prove that
// completion/disconnection also releases metadata, not merely the output row.
func awaitRecordingCleanup(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		left := len(s.started) + len(s.firstByte) + len(s.original) + len(s.dnsHits) +
			len(s.trailers) + len(s.streams) + len(s.replayClients) + len(s.frameSeq)
		s.mu.Unlock()
		if left == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d metadata entries survived completion/disconnection", left)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWithoutRecordingBodies(t *testing.T) {
	for _, n := range []int{128, 128 << 10, 9 << 20} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			payload := strings.Repeat("x", n)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, err := io.ReadAll(r.Body)
				if err != nil || string(got) != payload {
					t.Errorf("upload: %d bytes, error %v", len(got), err)
				}
				w.Header().Set("Trailer", "X-Complete")
				io.WriteString(w, payload)
				w.Header().Set("X-Complete", "yes")
			}))
			defer upstream.Close()
			s, _ := startServer(t)
			ctx, cancel := context.WithTimeout(WithoutRecording(t.Context()), 10*time.Second)
			defer cancel()
			// No ID or DebugRun is necessary to carry the capability. A derived
			// context, as used by auxiliary sends, inherits it too.
			reply, err := s.Replay(ctx, Flow{Method: "POST", URL: upstream.URL, RequestBody: payload})
			if err != nil {
				t.Fatal(err)
			}
			if reply.Status != 200 || reply.RequestBodySize != n || reply.ResponseBodySize != n ||
				reply.ResponseBody != payload[:min(n, 8<<20)] || reply.ResponseTrailer.Get("X-Complete") != "yes" {
				t.Fatalf("wrong replay response: status %d, upload %d, download %d, preview %d, trailers %v",
					reply.Status, reply.RequestBodySize, reply.ResponseBodySize, len(reply.ResponseBody), reply.ResponseTrailer)
			}
			awaitRecordingCleanup(t, s)
			assertNoRecording(t, s)
		})
	}
}

func TestWithoutRecordingStreamDoesNotSpillWhileActive(t *testing.T) {
	for _, upload := range []bool{false, true} {
		t.Run(fmt.Sprint("upload=", upload), func(t *testing.T) {
			payload := strings.Repeat("s", 9<<20)
			ready, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if upload {
					if n, err := io.Copy(io.Discard, r.Body); err != nil || n != int64(len(payload)) {
						t.Errorf("upload: %d, %v", n, err)
					}
				} else {
					io.WriteString(w, payload)
					w.(http.Flusher).Flush()
				}
				close(ready)
				<-release
				io.WriteString(w, "end")
			}))
			defer upstream.Close()
			// Unblock before httptest.Close even when an assertion fails.
			defer unblock()
			s, _ := startServer(t)
			result := make(chan error, 1)
			go func() {
				f := Flow{Method: "POST", URL: upstream.URL}
				if upload {
					f.RequestBody = payload
				}
				_, err := s.Replay(WithoutRecording(t.Context()), f)
				result <- err
			}()
			select {
			case <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("stream did not reach upstream")
			}
			assertNoRecording(t, s)
			unblock()
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			awaitRecordingCleanup(t, s)
			assertNoRecording(t, s)
		})
	}
}

func TestWithoutRecordingHTTPSRedirectsAndCookies(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "kept" {
			t.Error("redirect lost the cookie jar")
		}
		for key := range r.Header {
			if strings.Contains(strings.ToLower(key), "record") || strings.Contains(strings.ToLower(key), "capture") {
				t.Errorf("unexpected internal header %q", key)
			}
		}
		fmt.Fprint(w, "secure result")
	}))
	defer target.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "kept", Path: "/"})
		http.Redirect(w, r, target.URL+"/finish", http.StatusFound)
	}))
	defer redirect.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirect.URL+"/start", http.StatusFound)
	}))
	defer plain.Close()
	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	jar, _ := cookiejar.New(nil)
	ctx := WithoutRecording(t.Context())
	f := Flow{ID: 17, DebugRun: "suppressed-run", Method: "GET", URL: plain.URL}
	reply, err := s.ReplayWithCookies(ctx, f, jar, true)
	if err != nil || reply.Status != 200 || reply.ResponseBody != "secure result" {
		t.Fatalf("redirect response: %d %q, %v", reply.Status, reply.ResponseBody, err)
	}
	reply, err = s.ReplayWithCookies(ctx, f, jar, false)
	if err != nil || reply.Status != http.StatusFound {
		t.Fatalf("no-follow response: %d, %v", reply.Status, err)
	}
	awaitRecordingCleanup(t, s)
	assertNoRecording(t, s)
}

func TestWithoutRecordingLeavesConcurrentOrdinaryCapture(t *testing.T) {
	ready, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/load" {
			close(ready)
			<-release
		}
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	defer unblock()
	s, _ := startServer(t)
	result := make(chan error, 1)
	go func() {
		_, err := s.Replay(WithoutRecording(t.Context()), Flow{Method: "GET", URL: upstream.URL + "/load"})
		result <- err
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("load request did not start")
	}
	// An ordinary replay and an external client remain capturable. Header
	// strings resembling the capability cannot acquire it.
	spoof := http.Header{"X-Vscroxy-Without-Recording": {"true"}, "X-Vscroxy-Capture": {"off"}}
	_, err := s.Replay(t.Context(), Flow{ID: 42, Method: "GET", URL: upstream.URL + "/ordinary", RequestHeader: spoof})
	if err != nil {
		t.Fatal(err)
	}
	ordinary := waitFlow(t, s)
	if ordinary.ReplayOf != 42 || ordinary.Path != "/ordinary" {
		t.Fatalf("ordinary replay lost its capture/lineage: %+v", ordinary)
	}
	client := clientFor(t, s, nil)
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(t.Context(), "GET", upstream.URL+"/external", nil)
	req.Header = spoof
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if external := waitFlow(t, s); external.Path != "/external" || external.ReplayOf != 0 {
		t.Fatalf("external capture changed: %+v", external)
	}
	unblock()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	awaitRecordingCleanup(t, s)
	assertNoRecording(t, s)
}

func TestWithoutRecordingCancellation(t *testing.T) {
	for _, tc := range []struct {
		name           string
		secure, upload bool
	}{
		{name: "http-download"},
		{name: "https-download", secure: true},
		{name: "http-upload", upload: true},
		{name: "https-upload", secure: true, upload: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := strings.Repeat("c", 9<<20)
			ready, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.upload {
					var first [1]byte
					if _, err := io.ReadFull(r.Body, first[:]); err != nil {
						t.Errorf("upload did not start: %v", err)
					}
				} else {
					io.WriteString(w, payload)
					w.(http.Flusher).Flush()
				}
				close(ready)
				<-release
				if tc.upload {
					io.Copy(io.Discard, r.Body)
				}
			})
			var upstream *httptest.Server
			if tc.secure {
				upstream = httptest.NewTLSServer(handler)
			} else {
				upstream = httptest.NewServer(handler)
			}
			defer upstream.Close()
			defer unblock()
			s, _ := startServer(t)
			s.SetSSL(SSLConfig{All: true})
			ctx, cancel := context.WithCancel(WithoutRecording(t.Context()))
			defer cancel()
			result := make(chan error, 1)
			go func() {
				f := Flow{Method: "GET", URL: upstream.URL}
				if tc.upload {
					f.Method, f.RequestBody = "POST", payload
				}
				_, err := s.Replay(ctx, f)
				result <- err
			}()
			select {
			case <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("transfer did not start")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled replay returned %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("canceled replay did not return")
			}
			unblock()
			awaitRecordingCleanup(t, s)
			assertNoRecording(t, s)
		})
	}
}

func TestWithoutRecordingSSE(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", strings.Repeat("event", 2000))
	}))
	defer upstream.Close()
	s, _ := startServer(t)
	reply, err := s.Replay(WithoutRecording(t.Context()), Flow{Method: "GET", URL: upstream.URL})
	if err != nil || !strings.HasPrefix(reply.ResponseBody, "data: event") {
		t.Fatalf("SSE replay failed: %v", err)
	}
	awaitRecordingCleanup(t, s)
	assertNoRecording(t, s)
}

func TestWithoutRecordingUndecryptedHTTPS(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "tunneled")
	}))
	defer upstream.Close()
	s, _ := startServer(t)
	// Trust the fixture's self-signed certificate while leaving interception
	// disabled. Only the CONNECT envelope is visible to the proxy in this case.
	s.roots.AddCert(upstream.Certificate())
	reply, err := s.Replay(WithoutRecording(t.Context()), Flow{Method: "GET", URL: upstream.URL})
	if err != nil || reply.ResponseBody != "tunneled" {
		t.Fatalf("tunneled response: %q, %v", reply.ResponseBody, err)
	}
	awaitRecordingCleanup(t, s)
	assertNoRecording(t, s)
}

func TestWithoutRecordingPreservesRulesScriptsAndThrottles(t *testing.T) {
	payload := strings.Repeat("p", 12<<10)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/actual" || r.Header.Get("X-Script") != "yes" {
			t.Errorf("mapping/request script not applied: %s %v", r.URL.Path, r.Header)
		}
		if body, err := io.ReadAll(r.Body); err != nil || string(body) != payload {
			t.Errorf("request body changed: %d bytes, %v", len(body), err)
		}
		io.WriteString(w, payload)
	}))
	defer upstream.Close()
	s := startServerWithScript(t, `
function onRequest(req) { req.headers["X-Script"] = "yes" }
function onResponse(res) { res.body = "changed:" + res.body; res.status = 201 }
`)
	local := filepath.Join(t.TempDir(), "local.txt")
	localBody := strings.Repeat("local", 20<<10)
	if err := os.WriteFile(local, []byte(localBody), 0o600); err != nil {
		t.Fatal(err)
	}
	rules := []Rule{
		{Type: RuleMapRemote, Match: "/mapped", Target: upstream.URL, PathFrom: "/mapped", PathTo: "/actual"},
		{Type: RuleMapLocal, Match: "/local", Target: local},
		{Type: RuleBlock, Match: "/blocked"},
	}
	if err := s.SetRules(rules); err != nil {
		t.Fatal(err)
	}
	s.SetThrottle(Throttle{UpPerSec: 64 << 10, DownPerSec: 64 << 10})
	ctx := WithoutRecording(t.Context())
	started := time.Now()
	reply, err := s.Replay(ctx, Flow{Method: "POST", URL: upstream.URL + "/mapped", RequestBody: payload})
	if err != nil || reply.Status != 201 || reply.ResponseBody != "changed:"+payload {
		t.Fatalf("mapped/scripted response: %d, %v", reply.Status, err)
	}
	// Each direction carries 12 KiB at 64 KiB/s, about 375 ms in total.
	if time.Since(started) < 300*time.Millisecond {
		t.Fatal("suppressed send bypassed upload/download throttles")
	}
	s.SetThrottle(Throttle{})
	s.SetScript(nil)
	reply, err = s.Replay(ctx, Flow{Method: "GET", URL: upstream.URL + "/local"})
	if err != nil || reply.Status != 200 || reply.ResponseBody != localBody {
		t.Fatalf("local response: %d, %v", reply.Status, err)
	}
	reply, err = s.Replay(ctx, Flow{Method: "GET", URL: upstream.URL + "/blocked"})
	if err != nil || reply.Status != http.StatusForbidden {
		t.Fatalf("blocked response: %d, %v", reply.Status, err)
	}
	for _, rule := range rules {
		if hits := s.RuleHits(rule); hits != 1 {
			t.Errorf("rule %s hits = %d, want 1", rule.Type, hits)
		}
	}
	awaitRecordingCleanup(t, s)
	assertNoRecording(t, s)
}
