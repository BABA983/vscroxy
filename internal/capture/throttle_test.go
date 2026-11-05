package capture

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

func TestThrottledReaderPacesReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		payload := bytes.Repeat([]byte{0, 255, 42, 7}, 4096)
		r := throttleReader(t.Context(), nil, bytes.NewReader(payload), 64<<10)
		// Construction and unrelated waiting must not grant a burst budget.
		time.Sleep(time.Second)
		start := time.Now()
		got, err := io.ReadAll(r)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("read: %d bytes, %v", len(got), err)
		}
		if elapsed := time.Since(start); elapsed != 250*time.Millisecond {
			t.Fatalf("read took %v, want 250ms", elapsed)
		}
	})
}

func TestNetworkWaitAndBodyCancellation(t *testing.T) {
	for _, kind := range []string{"context", "shutdown", "close"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				shutdown := make(chan struct{})
				source, writer := io.Pipe()
				r := throttleReader(ctx, shutdown, source, 1).(io.ReadCloser)
				defer r.Close()
				defer writer.Close()
				go func() { _, _ = writer.Write([]byte("x")) }()
				result := make(chan error, 1)
				go func() { _, err := io.ReadAll(r); result <- err }()
				synctest.Wait() // the byte is read, but its one-second wait is pending
				want := context.Canceled
				switch kind {
				case "context":
					cancel()
				case "shutdown":
					close(shutdown)
				case "close":
					want = io.ErrClosedPipe
					if err := r.Close(); err != nil {
						t.Fatal(err)
					}
				}
				if err := <-result; !errors.Is(err, want) {
					t.Fatalf("read error %v, want %v", err, want)
				}
				if kind == "close" {
					if _, err := writer.Write([]byte("y")); !errors.Is(err, io.ErrClosedPipe) {
						t.Fatalf("source was not closed: %v", err)
					}
				} else if err := networkWait(ctx, shutdown, nil, time.Minute); !errors.Is(err, context.Canceled) {
					t.Fatalf("latency wait error: %v", err)
				}
			})
		})
	}
}

func TestThrottleOffLeavesReader(t *testing.T) {
	in := bytes.NewReader([]byte("hi"))
	if got := throttleReader(t.Context(), nil, in, 0); got != in {
		t.Fatal("an off throttle wrapped the reader")
	}
}

func TestNetworkConditionsValidateBeforeApply(t *testing.T) {
	s := &Server{}
	valid := Throttle{Name: "custom", UpPerSec: 1234, Latency: time.Second, FailurePercent: 7}
	if err := s.SetThrottle(valid); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Throttle{
		{DownPerSec: -1}, {UpPerSec: -1}, {DownPerSec: 1<<30 + 1}, {UpPerSec: 1<<30 + 1},
		{Latency: -1}, {Latency: time.Minute + 1}, {FailurePercent: -1}, {FailurePercent: 101},
	} {
		if err := s.SetThrottle(invalid); err == nil || s.Throttle() != valid {
			t.Fatalf("invalid setting changed conditions: %+v, %v", invalid, err)
		}
	}
}

func TestNetworkConditionsThroughProxy(t *testing.T) {
	payload := strings.Repeat("01234567", 64)
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != r.Header.Get("X-Expected") {
			t.Errorf("upstream body %q: %v", body, err)
		}
		if r.URL.Path == "/empty" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/sse" {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: "+payload+"\n\n")
			return
		}
		io.WriteString(w, payload)
	}))
	defer upstream.Close()
	s, _ := startServer(t)
	client := clientFor(t, s, nil)
	defer client.CloseIdleConnections()
	for _, tc := range []struct {
		name, path, body string
		conditions       Throttle
		minimum          time.Duration
	}{
		{"empty latency", "/empty", "", Throttle{Latency: 100 * time.Millisecond}, 90 * time.Millisecond},
		{"download", "/body", "", Throttle{DownPerSec: 2048}, 230 * time.Millisecond},
		{"upload", "/empty", payload, Throttle{UpPerSec: 2048}, 230 * time.Millisecond},
		{"sse", "/sse", "", Throttle{DownPerSec: 2048}, 230 * time.Millisecond},
		{"off", "/body", payload, Throttle{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.SetThrottle(tc.conditions); err != nil {
				t.Fatal(err)
			}
			req, _ := http.NewRequestWithContext(t.Context(), "POST", upstream.URL+tc.path, strings.NewReader(tc.body))
			req.Header.Set("X-Expected", tc.body)
			start := time.Now()
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			want := payload
			if tc.path == "/empty" {
				want = ""
			} else if tc.path == "/sse" {
				want = "data: " + payload + "\n\n"
			}
			if err != nil || string(got) != want || time.Since(start) < tc.minimum {
				t.Fatalf("got %d bytes, %v, elapsed %v (minimum %v)", len(got), err, time.Since(start), tc.minimum)
			}
		})
	}
	before := hits.Load()
	if err := s.SetThrottle(Throttle{Name: "disconnect", FailurePercent: 100}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(t.Context(), "POST", upstream.URL+"/fail", nil)
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
		t.Fatalf("100%% failure returned HTTP %d", resp.StatusCode)
	}
	if hits.Load() != before {
		t.Fatal("failed request reached upstream")
	}
	var failure Flow
	for range 6 {
		select {
		case f := <-s.Flows():
			if strings.HasSuffix(f.URL, "/fail") {
				failure = f
			}
		case <-time.After(time.Second):
			t.Fatal("missing captured exchange")
		}
	}
	if failure.Conn.Error != errNetworkFailure.Error() || !slices.Contains(failure.Actions, "Network · disconnect") {
		t.Fatalf("failure evidence: %q, %v", failure.Conn.Error, failure.Actions)
	}
}

func TestNetworkConditionsSnapshotSurvivesSettingsChange(t *testing.T) {
	arrived, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-release
		io.WriteString(w, strings.Repeat("x", 256))
	}))
	defer upstream.Close()
	s, _ := startServer(t)
	client := clientFor(t, s, nil)
	defer client.CloseIdleConnections()
	if err := s.SetThrottle(Throttle{DownPerSec: 1024}); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		resp, err := client.Get(upstream.URL)
		if err == nil {
			defer resp.Body.Close()
			var body []byte
			body, err = io.ReadAll(resp.Body)
			if err == nil && len(body) != 256 {
				err = errors.New("changed download body")
			}
		}
		result <- err
	}()
	<-arrived
	if err := s.SetThrottle(Throttle{FailurePercent: 100}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	close(release)
	if err := <-result; err != nil || time.Since(start) < 230*time.Millisecond {
		t.Fatalf("in-flight conditions changed: %v, %v", err, time.Since(start))
	}
}

func TestNetworkConditionsLocalAndHTTPS(t *testing.T) {
	payload := strings.Repeat("x", 256)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, payload)
	}))
	defer upstream.Close()
	s, _ := startServer(t)
	s.roots.AddCert(upstream.Certificate())
	s.SetSSL(SSLConfig{All: true})
	file := filepath.Join(t.TempDir(), "response.txt")
	if err := os.WriteFile(file, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRules([]Rule{{Type: RuleMapLocal, Match: "/local", Target: file}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetThrottle(Throttle{DownPerSec: 1024}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/upstream", "/local"} {
		start := time.Now()
		reply, err := s.Replay(t.Context(), Flow{Method: "GET", URL: upstream.URL + path})
		if err != nil || reply.ResponseBody != payload || reply.Status != 200 || time.Since(start) < 230*time.Millisecond {
			t.Fatalf("%s: status %d, body %d, error %v, time %v", path, reply.Status, len(reply.ResponseBody), err, time.Since(start))
		}
	}
}

func TestNetworkRuleOperationUsesSnapshotAndCompleteBody(t *testing.T) {
	s, _ := startServer(t) // deliberately has no live operation rules
	list := []Rule{{Type: RuleNetwork, Match: "/graphql", Operation: "Search", Network: new(Throttle)}}
	const selected = `{"query":"query Search { item }"}`
	for _, tc := range []struct {
		name, method, query, body string
		stream, gzip, matches     bool
	}{
		{name: "buffered", method: "POST", body: selected, matches: true},
		{name: "compressed", method: "POST", body: selected, gzip: true, matches: true},
		{name: "GET", method: "GET", query: "?query=" + url.QueryEscape("query Search { item }"), stream: true, matches: true},
		{name: "streamed prefix", method: "POST", body: selected, stream: true},
		{name: "missing body", method: "POST"},
		{name: "ambiguous", method: "POST", body: `{"query":"query Search { item } query Other { item }"}`},
		{name: "persisted name only", method: "POST", body: `{"operationName":"Search"}`},
		{name: "oversize", method: "POST", body: selected + strings.Repeat(" ", 1<<20)},
		{name: "oversize decompressed", method: "POST", body: selected + strings.Repeat(" ", 1<<20), gzip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse("http://example.test/graphql" + tc.query)
			if err != nil {
				t.Fatal(err)
			}
			var body []byte
			if tc.body != "" {
				body = []byte(tc.body)
			}
			h := http.Header{"Content-Type": {"application/json"}}
			if tc.gzip {
				var b bytes.Buffer
				zw := gzip.NewWriter(&b)
				if _, err := zw.Write(body); err != nil {
					t.Fatal(err)
				}
				if err := zw.Close(); err != nil {
					t.Fatal(err)
				}
				body = b.Bytes()
				h.Set("Content-Encoding", "gzip")
			}
			f := &proxy.Flow{Stream: tc.stream, Request: &proxy.Request{URL: u, Method: tc.method, Header: h, Body: body}}
			before := bytes.Clone(body)
			req := s.networkRuleRequest(f)
			if got := firstNetworkRule(list, req); (got == 0) != tc.matches {
				t.Fatalf("selected %d for operations %v, want matching=%v", got, req.Operations, tc.matches)
			}
			if !bytes.Equal(before, f.Request.Body) {
				t.Fatal("operation inspection mutated request bytes")
			}
		})
	}
}

func TestNetworkRuleSnapshotSurvivesScenarioSwitch(t *testing.T) {
	arrived, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-release
		io.WriteString(w, strings.Repeat("x", 256))
	}))
	defer upstream.Close()
	defer close(release)
	s, _ := startServer(t)
	client := clientFor(t, s, nil)
	defer client.CloseIdleConnections()
	rule := Rule{Name: "Selected", Type: RuleNetwork, Match: upstream.URL, Scenario: "Slow", ScenarioActive: true, Network: new(Throttle{DownPerSec: 1024})}
	if err := s.SetRules([]Rule{rule}); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		resp, err := client.Get(upstream.URL)
		if err == nil {
			defer resp.Body.Close()
			var body []byte
			body, err = io.ReadAll(resp.Body)
			if err == nil && len(body) != 256 {
				err = errors.New("changed download body")
			}
		}
		result <- err
	}()
	select {
	case <-arrived:
	case err := <-result:
		t.Fatalf("request ended before upstream: %v", err)
	}
	next, err := SelectScenario(s.Rules(), "")
	if err != nil {
		t.Fatal(err)
	}
	next[0].Network.DownPerSec = 0
	if err := s.SetRules(next); err != nil {
		t.Fatal(err)
	}
	if err := s.SetThrottle(Throttle{FailurePercent: 100}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	release <- struct{}{}
	if err := <-result; err != nil || time.Since(start) < 230*time.Millisecond {
		t.Fatalf("in-flight conditions changed: %v, %v", err, time.Since(start))
	}
	if s.RuleHits(rule) != 1 || s.RuleHits(next[0]) != 0 {
		t.Fatal("in-flight exchange lost its rule identity")
	}
	if f := waitFlow(t, s); !slices.Contains(f.Actions, "Scenario · Slow") || !slices.Contains(f.Actions, "Network conditions · down 1024 B/s · up 0 B/s · latency 0s · failure 0%") {
		t.Fatalf("in-flight exchange lost selected evidence: %v", f.Actions)
	}
}

func TestNetworkRuleRatesHTTPSLocalAndSSE(t *testing.T) {
	payload := strings.Repeat("x", 256)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != payload {
			t.Errorf("upload bytes changed: %d, %v", len(body), err)
		}
		if r.URL.Path == "/sse" {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: "+payload+"\n\n")
			return
		}
		io.WriteString(w, payload)
	}))
	defer upstream.Close()
	s, ca := startServer(t)
	s.roots.AddCert(upstream.Certificate())
	s.SetSSL(SSLConfig{All: true})
	client := clientFor(t, s, sslTestRoots(t, ca))
	defer client.CloseIdleConnections()
	file := filepath.Join(t.TempDir(), "response.txt")
	if err := os.WriteFile(file, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	rule := Rule{Type: RuleNetwork, Match: upstream.URL, Network: new(Throttle{DownPerSec: 1024, UpPerSec: 1024, Latency: 20 * time.Millisecond})}
	if err := s.SetRules([]Rule{rule, {Type: RuleMapLocal, Match: "/local", Target: file}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetThrottle(Throttle{FailurePercent: 100}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/upstream", "/local", "/sse"} {
		req, _ := http.NewRequestWithContext(t.Context(), "POST", upstream.URL+path, strings.NewReader(payload))
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		want := payload
		minimum := 480 * time.Millisecond
		if path == "/sse" {
			want = "data: " + payload + "\n\n"
		} else if path == "/local" {
			// A local response never forwards an upload, so only its download is paced.
			minimum = 250 * time.Millisecond
		}
		if err != nil || resp.StatusCode != 200 || string(body) != want || time.Since(start) < minimum {
			t.Fatalf("%s: HTTP %d body %d, %v, time %v (minimum %v)", path, resp.StatusCode, len(body), err, time.Since(start), minimum)
		}
	}
	if s.RuleHits(rule) != 3 {
		t.Fatalf("network counted %d hits for three exchanges", s.RuleHits(rule))
	}
}

func TestNetworkRuleOversizedUploadSkipsBodyMatching(t *testing.T) {
	payload := `{"query":"query Search { item }"}` + strings.Repeat(" ", 8<<20)
	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != payload {
			t.Errorf("streamed upload changed: %d, %v", len(body), err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	s, _ := startServer(t)
	client := clientFor(t, s, nil)
	defer client.CloseIdleConnections()
	list := []Rule{
		{Type: RuleNetwork, Match: upstream.URL, Operation: "Search", Network: new(Throttle{FailurePercent: 100})},
		{Type: RuleNetwork, Match: upstream.URL, Network: new(Throttle)},
	}
	if err := s.SetRules(list); err != nil {
		t.Fatal(err)
	}
	if err := s.SetThrottle(Throttle{FailurePercent: 100}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(t.Context(), "POST", upstream.URL, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 204 || upstreamHits.Load() != 1 || s.RuleHits(list[0]) != 0 || s.RuleHits(list[1]) != 1 {
		t.Fatalf("streamed selection: HTTP %d, upstream %d, hits %d/%d", resp.StatusCode, upstreamHits.Load(), s.RuleHits(list[0]), s.RuleHits(list[1]))
	}
}
