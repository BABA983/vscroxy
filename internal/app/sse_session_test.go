package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

func TestSSESessionUILiveSearchCopyAndStop(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer sse-test" {
			t.Error("prepared auth or GET missing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Session", "live-test")
		fmt.Fprint(w, "id:17\nevent:price\ndata:first 界\ndata:second\n\ndata:third\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	defer upstream.Close()
	defer close(release)
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.addDocument(savedRequest{}, "")
	a.composeURL, a.composeMethod = upstream.URL, "POST" // The live client is GET-only.
	a.auth.Type, a.auth.Token = "Bearer", "sse-test"
	a.composePre = `throw new Error("request script must not run")`
	a.openSSE()
	if a.sse.reconnect || !strings.Contains(a.sse.headers, "Bearer sse-test") || a.sse.url != upstream.URL {
		t.Fatal("draft preparation or reconnect default wrong")
	}
	t.Cleanup(func() { a.sse.session.stop() })
	tt := ui.NewTester(a.view, 1100, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(time.Minute)
		for !ready() {
			if time.Now().After(deadline) {
				t.Fatalf("SSE did not settle: %v", tt.Texts())
			}
			runtime.Gosched()
			tt.Frame()
		}
		tt.Frame()
	}
	click("Connect SSE")
	wait(func() bool { _, _, events, _ := a.sse.session.snapshot(); return len(events) == 3 })
	if !tt.HasText("SSE session · Connected") {
		t.Fatal("live events did not arrive while upstream remains open")
	}
	click("SSE event 1")
	click("Copy shown SSE data")
	if !strings.Contains(tt.Clipboard(), "X-Session: live-test") {
		t.Fatal("response headers not inspectable")
	}
	click("Search SSE events")
	tt.Type("PRICE")
	if _, ok := tt.Find("SSE event 3"); ok {
		t.Fatal("search did not filter the default message type")
	}
	click("SSE event 2")
	click("Copy shown SSE data")
	if tt.Clipboard() != "first 界\nsecond" || !tt.HasText("Type: price · ID: 17") {
		t.Fatalf("incorrect SSE payload/metadata: %q", tt.Clipboard())
	}
	click("Copy event ID")
	if tt.Clipboard() != "17" {
		t.Fatal("incorrect event ID copy")
	}
	click("Stop SSE")
	wait(func() bool { _, done, _, _ := a.sse.session.snapshot(); return done })
	if !tt.HasText("SSE session · Stopped") || calls.Load() != 1 {
		t.Fatal("stop did not end receiving without reconnecting")
	}
	click("Stop & close")
	tt.Frame()
	tt.Frame()
	if a.sse.open {
		t.Fatal("panel stayed open")
	}
}

func TestSSESessionUIReconnectCancellationAndFailure(t *testing.T) {
	for _, mode := range []string{"connecting", "retrying", "failed"} {
		t.Run(mode, func(t *testing.T) {
			release := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "connecting":
					<-release
				case "retrying":
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "retry:300000\n\ndata:first\n\n")
				case "failed":
					http.Error(w, "unauthorized", http.StatusUnauthorized)
				}
			}))
			defer upstream.Close()
			defer close(release)
			a := workspaceApp()
			a.srv = requestProxy(t)
			a.sse = sseWorkbench{open: true, url: upstream.URL}
			t.Cleanup(func() { a.sse.session.stop() })
			tt := ui.NewTester(a.view, 960, 600)
			if err := tt.Click("Reconnect on EOF / network failure"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("Connect SSE"); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(time.Minute)
			for {
				state, finished, events, _ := a.sse.session.snapshot()
				if mode == "connecting" || mode == "retrying" && state == "Reconnecting" || mode == "failed" && finished {
					if mode == "failed" && (state != "Failed" || len(events) != 1 || !strings.Contains(events[0].detail, "401")) {
						t.Fatal("HTTP failure was hidden or retried")
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("SSE connection did not reach expected state")
				}
				runtime.Gosched()
				tt.Frame()
			}
			tt.Key(0, ui.KeyEscape)
			tt.Frame()
			tt.Frame()
			select {
			case <-a.sse.session.done:
			case <-time.After(time.Minute):
				t.Fatal("Escape did not cancel connection or retry wait")
			}
			if a.sse.open {
				t.Fatal("Escape did not dismiss SSE panel")
			}
		})
	}
}

func TestSSESessionUIRetentionAndEvictedSelection(t *testing.T) {
	release, burst := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data:first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-burst:
		case <-release:
			return
		}
		for i := range 205 {
			data := fmt.Sprint(i)
			if i == 204 {
				data = strings.Repeat("界", 30000)
			}
			fmt.Fprintf(w, "data:%s\n\n", data)
		}
	}))
	defer upstream.Close()
	defer close(release)
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.sse = sseWorkbench{open: true, url: upstream.URL}
	t.Cleanup(func() { a.sse.session.stop() })
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Connect SSE"); err != nil {
		t.Fatal(err)
	}
	guard := time.NewTimer(time.Minute)
	defer guard.Stop()
	for {
		_, _, events, _ := a.sse.session.snapshot()
		if len(events) == 2 {
			break
		}
		select {
		case <-guard.C:
			t.Fatal("no first event")
		default:
			runtime.Gosched()
			tt.Frame()
		}
	}
	tt.Frame()
	if err := tt.Click("SSE event 2"); err != nil {
		t.Fatal(err)
	}
	close(burst)
	select {
	case <-a.sse.session.done:
	case <-guard.C:
		t.Fatal("stream did not end")
	}
	tt.Frame()
	if !tt.HasText("Selected event was evicted. Select a retained event.") || a.sse.reader != nil {
		t.Fatal("evicted selection still offered a stale payload")
	}
	state, finished, events, evicted := a.sse.session.snapshot()
	if state != "Ended" || !finished || len(events) != 200 || evicted != 8 {
		t.Fatalf("state=%s finished=%v retained=%d evicted=%d", state, finished, len(events), evicted)
	}
	payload := events[len(events)-2]
	if payload.size != 90000 || !utf8.ValidString(payload.data) || !strings.HasSuffix(payload.data, "Retained 65535 of 90000 bytes") {
		t.Fatal("large Unicode event was not bounded with explicit truncation")
	}
	if err := tt.Click(fmt.Sprintf("SSE event %d", payload.seq)); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Copy shown SSE data"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != payload.data {
		t.Fatal("copied payload was not the displayed bounded preview")
	}
}
