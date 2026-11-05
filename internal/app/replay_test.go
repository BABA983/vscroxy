package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
)

func TestReplaySettingsLimits(t *testing.T) {
	cases := []struct {
		name        string
		flows       int
		repeat      int
		concurrency int
		intervalMS  int
		want        string
	}{
		{name: "default", flows: 2, repeat: 1, concurrency: 1, intervalMS: 0},
		{name: "largest allowed batch", flows: 5, repeat: 100, concurrency: 8, intervalMS: 60_000},
		{name: "no requests", flows: 0, repeat: 1, concurrency: 1, intervalMS: 0, want: "Select at least one"},
		{name: "repeat low", flows: 1, repeat: 0, concurrency: 1, intervalMS: 0, want: "1–100 repeats"},
		{name: "repeat high", flows: 1, repeat: 101, concurrency: 1, intervalMS: 0, want: "1–100 repeats"},
		{name: "concurrency low", flows: 1, repeat: 1, concurrency: 0, intervalMS: 0, want: "1–8 concurrent"},
		{name: "concurrency high", flows: 1, repeat: 1, concurrency: 9, intervalMS: 0, want: "1–8 concurrent"},
		{name: "interval negative", flows: 1, repeat: 1, concurrency: 1, intervalMS: -1, want: "0–60000 ms"},
		{name: "interval high", flows: 1, repeat: 1, concurrency: 1, intervalMS: 60_001, want: "0–60000 ms"},
		{name: "batch over limit", flows: 167, repeat: 3, concurrency: 1, intervalMS: 0, want: "batch limit is 500"},
		{name: "batch exactly at limit", flows: 100, repeat: 5, concurrency: 1, intervalMS: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := replaySettings{
				repeat:      tc.repeat,
				concurrency: tc.concurrency,
				interval:    time.Duration(tc.intervalMS) * time.Millisecond,
			}.problem(tc.flows)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("unexpected problem: %s", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("problem %q does not contain %q", got, tc.want)
			}
		})
	}
}

func replayTestApp(t *testing.T, handler http.HandlerFunc) (*app, []flow) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	a := workspaceApp()
	a.srv = srv
	flows := []flow{
		{id: 1, method: "GET", scheme: "http", host: "upstream.test", path: "/one", url: upstream.URL + "/one"},
		{id: 2, method: "POST", scheme: "http", host: "upstream.test", path: "/two", url: upstream.URL + "/two"},
	}
	a.flows = flows
	a.recount()
	a.applyFilter()
	return a, flows
}

func waitReplay(t *testing.T, a *app, tt *ui.Tester) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for a.replayUI.updates != nil {
		if time.Now().After(deadline) {
			t.Fatal("replay did not finish")
		}
		time.Sleep(time.Millisecond)
		tt.Frame()
	}
}

func TestAdvancedReplayRunsConcurrentlyAndSummarizesStatuses(t *testing.T) {
	var inFlight, peak atomic.Int32
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	handler := func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		<-release
		inFlight.Add(-1)
		if r.URL.Path == "/two" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
	a, flows := replayTestApp(t, handler)
	flows = append(flows, flow{id: 3, method: "GET", scheme: "http", host: "upstream.test", path: "/three", url: flows[0].url})
	a.flows = flows
	a.recount()
	a.applyFilter()
	a.openReplay(flows)
	a.replayUI.timeout, a.replayUI.repeat = "0", "2"
	a.replayUI.concurrency, a.replayUI.interval = "3", "0"
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Send replays"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent sends did not reach the server")
		}
	}
	if got := peak.Load(); got != 3 {
		t.Fatalf("peak concurrency = %d, want 3", got)
	}
	close(release)
	waitReplay(t, a, tt)
	if a.replayUI.done != 6 || a.replayUI.failed != 0 {
		t.Fatalf("done=%d failed=%d, want 6 and 0", a.replayUI.done, a.replayUI.failed)
	}
	if a.replayUI.statuses[200] != 4 || a.replayUI.statuses[404] != 2 {
		t.Fatalf("status distribution = %v, want 200×4 and 404×2", a.replayUI.statuses)
	}
	summary := a.replayUI.summary()
	for _, want := range []string{"6/6 started", "200 × 4", "404 × 2"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q lacks %q", summary, want)
		}
	}
	if !tt.HasText("6/6 started") {
		t.Fatal("the dialog did not show the completed batch")
	}
}

func TestAdvancedReplayCountsCompletedAndCanceledSends(t *testing.T) {
	var calls atomic.Int32
	started := make(chan int, 10)
	release := make(chan struct{})
	a, flows := replayTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		started <- n
		if n > 1 {
			<-release
		}
		w.WriteHeader(http.StatusOK)
	})
	a.openReplay(flows[:1])
	a.replayUI.timeout, a.replayUI.repeat = "0", "10"
	a.replayUI.concurrency, a.replayUI.interval = "1", "0"
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Send replays"); err != nil {
		t.Fatal(err)
	}
	for want := 1; want <= 2; want++ {
		select {
		case got := <-started:
			if got != want {
				t.Fatalf("start %d, want %d", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("send %d did not reach the server", want)
		}
	}
	if err := tt.Click("Stop replay"); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitReplay(t, a, tt)
	if !a.replayUI.stopped || !a.replayUI.finished {
		t.Fatalf("stopped=%t finished=%t", a.replayUI.stopped, a.replayUI.finished)
	}
	if a.replayUI.done != 2 || a.replayUI.failed != 0 || a.replayUI.canceled != 1 {
		t.Fatalf("done=%d failed=%d canceled=%d, want 2, 0 and 1", a.replayUI.done, a.replayUI.failed, a.replayUI.canceled)
	}
	if a.replayUI.statuses[200] != 1 {
		t.Fatalf("status distribution = %v, want 200×1", a.replayUI.statuses)
	}
	summary := a.replayUI.summary()
	for _, want := range []string{"2/10 started", "1 succeeded", "0 failed", "1 canceled", "200 × 1"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q lacks %q", summary, want)
		}
	}
	if !tt.HasText("1 canceled") {
		t.Fatal("the dialog did not show the canceled count")
	}
}

func TestAdvancedReplayReportsElapsedWithFinalOutcome(t *testing.T) {
	a, flows := replayTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})
	a.openReplay(flows[:1])
	a.replayUI.timeout = "0"
	a.startReplay(0)
	var final replayOutcome
	for outcome := range a.replayUI.updates {
		if outcome.final {
			final = outcome
		}
	}
	if !final.final || final.elapsed <= 0 {
		t.Fatalf("final outcome = %+v, want elapsed time", final)
	}
}

func TestReplayRecordsOnlySingleSendResult(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flows int
		want  int
	}{{"single send", 1, 1}, {"batch", 2, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			a, flows := replayTestApp(t, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, "ok")
			})
			a.openReplay(flows[:tc.flows])
			a.replayUI.timeout = "0"
			a.startReplay(0)
			for range a.replayUI.updates {
			}
			a.mu.Lock()
			got := len(a.replayResults)
			a.mu.Unlock()
			if got != tc.want {
				t.Fatalf("recorded results = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestReplayDialogSingleSendRestoresInspectNotification(t *testing.T) {
	a, flows := replayTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})
	a.openReplay(flows[:1])
	a.replayUI.timeout = "0"
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Send replays"); err != nil {
		t.Fatal(err)
	}
	waitReplay(t, a, tt)
	tt.Frame()
	if !tt.HasText("Replayed Capture #1") || !tt.HasText("Inspect replay") {
		t.Fatalf("missing replay result notification: %v", tt.Texts())
	}
	if a.replayUI.done != 1 || a.replayUI.failed != 0 || a.replayUI.canceled != 0 {
		t.Fatalf("done=%d failed=%d canceled=%d", a.replayUI.done, a.replayUI.failed, a.replayUI.canceled)
	}
}

func TestReplaySettingsRejectsHugeIntervalBeforeDurationConversion(t *testing.T) {
	for _, text := range []string{"60001", "9223372036854775807", "999999999999999999999"} {
		r := replaySetup{flows: []flow{{}}, repeat: "1", concurrency: "1", interval: text}
		settings, problem := r.settings()
		if !strings.Contains(problem, "0–60000 ms") {
			t.Fatalf("interval %q problem = %q", text, problem)
		}
		if settings.interval != -time.Millisecond {
			t.Fatalf("interval %q converted to %v before validation", text, settings.interval)
		}
	}
	r := replaySetup{flows: []flow{{}}, repeat: "1", concurrency: "1", interval: "60000"}
	settings, problem := r.settings()
	if problem != "" || settings.interval != time.Minute {
		t.Fatalf("60000 ms = %v, problem %q", settings.interval, problem)
	}
}

func TestAdvancedReplayIntervalSpacesStarts(t *testing.T) {
	times := make(chan time.Time, 3)
	a, flows := replayTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		times <- time.Now()
		io.WriteString(w, "ok")
	})
	a.openReplay(flows[:1])
	a.replayUI.timeout, a.replayUI.repeat = "0", "3"
	a.replayUI.concurrency, a.replayUI.interval = "1", "80"
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Send replays"); err != nil {
		t.Fatal(err)
	}
	waitReplay(t, a, tt)
	first, second, third := <-times, <-times, <-times
	if second.Sub(first) < 60*time.Millisecond || third.Sub(second) < 60*time.Millisecond {
		t.Fatalf("starts %v apart, want about 80 ms", []time.Duration{second.Sub(first), third.Sub(second)})
	}
	if a.replayUI.done != 3 {
		t.Fatalf("done=%d, want 3", a.replayUI.done)
	}
}
