package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/request"
)

func replayCaptureApp(t *testing.T, handler http.HandlerFunc) (*app, *httptest.Server) {
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
	return a, upstream
}

type replayJobJSON struct {
	ID         string `json:"id"`
	Done       bool   `json:"done"`
	Kind       string `json:"kind"`
	SourceID   int    `json:"sourceId"`
	FlowID     int    `json:"flowId"`
	Status     int    `json:"status"`
	DurationMs int    `json:"durationMs"`
	Error      string `json:"error"`
}

func waitReplayJob(t *testing.T, a *app, client *control.Client, id string) replayJobJSON {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var state replayJobJSON
		if err := json.Unmarshal(desktopCall(t, client, "runs.get", map[string]any{"id": id}), &state); err != nil {
			t.Fatal(err)
		}
		if state.Done {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatalf("replay job %s did not finish: %+v, jobs=%d", id, state, len(a.control.jobs))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func startReplay(t *testing.T, client *control.Client, params map[string]any) string {
	t.Helper()
	var started struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(desktopCall(t, client, "flows.replay", params), &started); err != nil || started.ID == "" {
		t.Fatalf("flows.replay = %+v, %v", started, err)
	}
	return started.ID
}

func TestFlowsReplayHonorsOverridesAndReportsRuns(t *testing.T) {
	type seen struct {
		method string
		path   string
		header http.Header
		body   string
	}
	received := make(chan seen, 4)
	a, upstream := replayCaptureApp(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- seen{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: string(body)}
		w.WriteHeader(http.StatusCreated)
	})
	a.flows = []flow{{
		id: 7, live: true, method: "POST", scheme: "http", host: "upstream.test", path: "/original",
		url: upstream.URL + "/original", at: time.Now(),
		reqHeader: http.Header{"X-Keep": {"one"}, "X-Remove": {"two"}, "Content-Type": {"application/json"}},
		reqBody:   `{"original":true}`,
	}}
	a.recount()
	client := desktopClient(t, a)

	firstID := startReplay(t, client, map[string]any{"id": 7, "confirm": true})
	select {
	case got := <-received:
		if got.method != "POST" || got.path != "/original" || got.header.Get("X-Keep") != "one" || got.header.Get("X-Remove") != "two" || got.body != `{"original":true}` {
			t.Fatalf("unchanged replay sent %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unchanged replay did not reach the upstream")
	}
	first := waitReplayJob(t, a, client, firstID)
	if first.Kind != "replay" || first.SourceID != 7 || first.Status != 201 || first.Error != "" || first.DurationMs < 0 {
		t.Fatalf("unchanged replay result = %+v", first)
	}
	// The proxy records the replay like any other exchange. Once the desktop
	// table shows it, runs.get names its flow ID.
	var captured capture.Flow
	select {
	case captured = <-a.srv.Flows():
	case <-time.After(5 * time.Second):
		t.Fatal("replay was not captured")
	}
	if captured.ReplayOf != 7 || captured.Status != 201 {
		t.Fatalf("captured replay = %+v", captured)
	}
	a.flows = append(a.flows, flow{id: 900, live: true, replayOf: captured.ReplayOf, at: captured.At})
	if again := waitReplayJob(t, a, client, firstID); again.FlowID != 900 {
		t.Fatalf("replay flowId = %d; want the captured replay 900", again.FlowID)
	}

	secondID := startReplay(t, client, map[string]any{
		"id": 7, "confirm": true,
		"method": "PUT", "url": upstream.URL + "/changed",
		"headers": map[string]any{"X-Keep": "", "X-New": "value"},
		"body":    "changed body", "timeoutSeconds": 5,
	})
	select {
	case got := <-received:
		if got.method != "PUT" || got.path != "/changed" || got.header.Get("X-Keep") != "" || got.header.Get("X-New") != "value" {
			t.Fatalf("overridden replay sent %+v", got)
		}
		if got.header.Get("Content-Type") != "application/json" || got.body != "changed body" {
			t.Fatalf("overridden replay lost captured headers or kept the old body: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("overridden replay did not reach the upstream")
	}
	second := waitReplayJob(t, a, client, secondID)
	if second.Kind != "replay" || second.SourceID != 7 || second.Status != 201 || second.Error != "" {
		t.Fatalf("overridden replay result = %+v", second)
	}
	if first.ID == second.ID {
		t.Fatal("two replays shared one job ID")
	}
}

func TestFlowsReplayRejectsUnconfirmedAndNonHTTPFlows(t *testing.T) {
	a, upstream := replayCaptureApp(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	a.flows = []flow{
		{id: 1, live: true, method: "GET", scheme: "http", host: "upstream.test", path: "/ok", url: upstream.URL + "/ok", at: time.Now()},
		{id: 2, live: true, method: "GET", scheme: "ws", host: "upstream.test", path: "/socket", url: "ws://upstream.test/socket", kind: "ws", at: time.Now()},
		{id: 3, live: true, method: "GET", at: time.Now()},
	}
	a.recount()
	client := desktopClient(t, a)
	for _, params := range []map[string]any{
		{"id": 1},
		{"id": 1, "confirm": false},
		{"id": 2, "confirm": true},
		{"id": 3, "confirm": true},
		{"id": 99, "confirm": true},
	} {
		if value := string(desktopCall(t, client, "flows.replay", params)); !strings.Contains(value, `"error":`) {
			t.Fatalf("flows.replay accepted %v: %s", params, value)
		}
	}
	if len(a.control.jobs) != 0 {
		t.Fatalf("rejected replays created %d jobs", len(a.control.jobs))
	}
}

func TestRequestsSaveIntoCollectionAndReadBack(t *testing.T) {
	a := workspaceApp()
	a.requests = []savedRequest{{Request: request.Request{ID: "orders", Name: "Orders", Collection: "Shop/Orders", Method: "GET", URL: "http://api.test/orders"}}}
	a.flows = []flow{{
		id: 9, live: true, method: "POST", scheme: "http", host: "api.test", path: "/login",
		url: "http://api.test/login", at: time.Now(),
		reqHeader: http.Header{"Content-Type": {"application/json"}},
		reqBody:   `{"user":"alice"}`,
	}}
	client := desktopClient(t, a)
	var saved struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Collection string `json:"collection"`
	}
	if err := json.Unmarshal(desktopCall(t, client, "requests.save", map[string]any{
		"flowId": 9, "confirm": true, "name": "Login", "collection": "Shop/Orders",
	}), &saved); err != nil || saved.ID == "" || saved.Name != "Login" || saved.Collection != "Shop/Orders" {
		t.Fatalf("requests.save = %+v, %v", saved, err)
	}
	var got struct {
		Name       string `json:"name"`
		Collection string `json:"collection"`
		Method     string `json:"method"`
		URL        string `json:"url"`
		Body       string `json:"body"`
	}
	if err := json.Unmarshal(desktopCall(t, client, "requests.get", map[string]any{"id": saved.ID, "details": true}), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Login" || got.Collection != "Shop/Orders" || got.Method != "POST" || got.URL != "http://api.test/login" || got.Body != `{"user":"alice"}` {
		t.Fatalf("saved request read back as %+v", got)
	}
	listed := string(desktopCall(t, client, "requests.list", map[string]any{"collection": "Shop/Orders"}))
	if !strings.Contains(listed, saved.ID) || !strings.Contains(listed, `"name":"Login"`) {
		t.Fatalf("saved request missing from its collection: %s", listed)
	}

	// Omitted name and collection use the desktop's own capture label and root.
	var defaulted struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Collection string `json:"collection"`
	}
	if err := json.Unmarshal(desktopCall(t, client, "requests.save", map[string]any{"flowId": 9, "confirm": true}), &defaulted); err != nil ||
		defaulted.Name != "POST api.test/login" || defaulted.Collection != "" {
		t.Fatalf("defaulted requests.save = %+v, %v", defaulted, err)
	}
}

func TestRequestsSaveRejectsUnknownCollectionAndNonHTTP(t *testing.T) {
	a := workspaceApp()
	a.requests = []savedRequest{{Request: request.Request{ID: "orders", Name: "Orders", Collection: "Shop/Orders", Method: "GET", URL: "http://api.test/orders"}}}
	a.flows = []flow{
		{id: 9, live: true, method: "POST", scheme: "http", host: "api.test", path: "/login", url: "http://api.test/login", at: time.Now(),
			reqBody: `{}`, reqSize: 2},
		{id: 10, live: true, method: "GET", scheme: "ws", host: "api.test", path: "/socket", url: "ws://api.test/socket", kind: "ws", at: time.Now()},
	}
	client := desktopClient(t, a)
	before := len(a.requests)
	for _, params := range []map[string]any{
		{"flowId": 9, "confirm": true, "collection": "Missing"},
		{"flowId": 10, "confirm": true},
		{"flowId": 99, "confirm": true},
		{"flowId": 9},
	} {
		if value := string(desktopCall(t, client, "requests.save", params)); !strings.Contains(value, `"error":`) {
			t.Fatalf("requests.save accepted %v: %s", params, value)
		}
	}
	if len(a.requests) != before {
		t.Fatalf("refused saves wrote %d requests", len(a.requests)-before)
	}
	if listed := string(desktopCall(t, client, "requests.list", nil)); strings.Contains(listed, "Missing") {
		t.Fatalf("refused save created a collection: %s", listed)
	}
}
