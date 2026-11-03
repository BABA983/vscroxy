package app

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/cli"
	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
)

func recordingApp(t *testing.T) (*app, *control.Client) {
	t.Helper()
	a := libraryApp(t)
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	a.envs, a.activeEnv = []environment{{Name: "Local"}, {Name: "Other"}}, "Other"
	var err error
	a.srv, err = capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.srv.Close() })
	client := desktopClient(t, a)
	return a, client
}

func startRecording(t *testing.T, client *control.Client, extra map[string]any) {
	t.Helper()
	p := map[string]any{"collection": "Recorded", "environment": "Local", "confirm": true}
	for k, v := range extra {
		p[k] = v
	}
	if v := desktopCall(t, client, "collection-recording.start", p); !strings.Contains(string(v), `"active":true`) {
		t.Fatalf("start: %s", v)
	}
}

func recordingFlow(path string) flow {
	return flow{live: true, id: 10, at: time.Now(), method: "GET", scheme: "https", host: "api.example.com", path: path, url: "https://api.example.com" + path, status: 200, process: "Browser",
		reqHeader:  http.Header{"Authorization": {"Bearer recording-secret"}, "Cookie": {"session=private"}, "X-Api-Key": {"private-key"}, "Accept": {"application/json"}},
		respHeader: http.Header{"Content-Type": {"application/json"}, "Set-Cookie": {"response-private"}}, resBody: `{"id":42}`}
}

func TestDesktopControlCollectionRecordingPersistsNewTrafficAndPreservesAuthoredRequests(t *testing.T) {
	a, client := recordingApp(t)
	startRecording(t, client, map[string]any{"hosts": []string{"API.EXAMPLE.COM"}, "apps": []string{"Browser"}})
	f := recordingFlow("/v1/users/42?expand=profile")
	a.recordCollection([]flow{f})
	list, _, _, err := loadLibrary(a.library.root)
	if err != nil || len(list) != 1 {
		t.Fatalf("library %d: %v, status %s", len(list), err, desktopCall(t, client, "collection-recording.get", nil))
	}
	r := list[0]
	if r.Collection != "Recorded/users" || r.Name != "Get user" || len(r.Examples) != 1 || r.Examples[0].Request.URL != f.url || r.Examples[0].Body != f.resBody {
		t.Fatalf("recorded %+v", r)
	}
	env, err := loadEnvs(&store.File{Path: a.envsPath})
	if err != nil || env.Active != "Other" {
		t.Fatalf("environment switched or unreadable: %+v %v", env, err)
	}
	p, err := r.Request.Prepare(env.Envs[0].values())
	if err != nil || p.URL != f.url || p.Header.Get("Authorization") != "Bearer recording-secret" || p.Header.Get("Cookie") != "session=private" || p.Header.Get("X-Api-Key") != "private-key" {
		t.Fatalf("recorded request not replayable: %+v %v", p, err)
	}
	for _, file := range libraryTree(t, a.library.root) {
		data, _ := os.ReadFile(filepath.Join(a.library.root, filepath.FromSlash(file)))
		for _, secret := range []string{"recording-secret", "session=private", "private-key", "response-private"} {
			if strings.Contains(string(data), secret) {
				t.Fatalf("credential leaked into %s", file)
			}
		}
	}
	sharedBefore := len(a.envs[0].Vars)
	// A reader's edits and examples are not the recorder's to replace.
	r.Name, r.Pre, r.Post, r.Body = "Manually edited", "pm.test('pre',()=>{})", "pm.test('post',()=>{})", "authored body"
	r.Auth = request.Auth{Type: "Bearer", Token: "{{manual_token}}"}
	r.Examples = append(r.Examples, request.Example{ID: "manual", Name: "Do not replace", Status: 200, Body: "manual example"})
	if err := a.persistRequests([]savedRequest{r}); err != nil {
		t.Fatal(err)
	}
	a.openSaved(r.key())
	d := a.requestDocument
	d.composeURL += "&draft=true"
	draftURL := d.composeURL
	f.url, f.path, f.resBody, f.at = "https://api.example.com/v1/users/7?expand=none", "/v1/users/7?expand=none", `{"id":7}`, time.Now()
	a.recordCollection([]flow{f})
	if len(a.requests) != 1 || len(a.requests[0].Examples) != 2 || a.requests[0].Examples[0].Body != `{"id":7}` || a.requests[0].Examples[1].Body != "manual example" {
		t.Fatalf("duplicate endpoint/example: %+v", a.requests)
	}
	got, want := a.requests[0], r
	got.Examples, want.Examples = nil, nil
	if !reflect.DeepEqual(got, want) || d.composeURL != draftURL || d.examples[0].Body != `{"id":7}` || !d.dirty() || len(a.envs[0].Vars) != sharedBefore {
		t.Fatal("recording rewrote authored content, dirty draft, or initial URL variables")
	}
	f.status, f.resBody = 404, `{"error":"missing"}`
	a.recordCollection([]flow{f})
	if len(a.requests[0].Examples) != 3 || a.requests[0].Examples[2].Status != 404 {
		t.Fatal("HTTP error example not retained separately")
	}
	desktopCall(t, client, "collection-recording.stop", map[string]bool{"confirm": true})
	before := libraryTree(t, a.library.root)
	f.url = "https://api.example.com/v1/orders/8"
	a.recordCollection([]flow{f})
	if !slices.Equal(before, libraryTree(t, a.library.root)) || len(a.requests) != 1 {
		t.Fatal("stopped recorder wrote a request")
	}
	// A fresh session matches the saved template and replaces only its own
	// deterministic example, not a duplicate or a manual response.
	startRecording(t, client, nil)
	f = recordingFlow("/v1/users/19")
	f.resBody = `{"id":19}`
	a.recordCollection([]flow{f})
	if len(a.requests) != 1 || len(a.requests[0].Examples) != 3 || a.requests[0].Examples[0].Body != f.resBody {
		t.Fatal("restart duplicated the endpoint or its example")
	}
}

func TestDesktopControlCollectionRecordingFiltersAndCompleteBodyContract(t *testing.T) {
	a, client := recordingApp(t)
	startRecording(t, client, map[string]any{"hosts": []string{"api.example.com:8443"}, "apps": []string{"Browser"}})
	for _, change := range []func(*flow){
		func(f *flow) {}, // a hostname-only URL must not match a port-specific filter
		func(f *flow) { f.url = "https://api.example.com:8443/users/42"; f.process = "Other" },
		func(f *flow) { f.url = "https://api.example.com:8443/app.js" },
		func(f *flow) { f.kind = "websocket" },
		func(f *flow) { f.status = 0 },
		func(f *flow) { f.reqError = "private diagnostic" },
		func(f *flow) { f.at = a.collectionRecording.StartedAt.Add(-time.Nanosecond) },
		func(f *flow) { f.live = false },
		func(f *flow) { f.debugRun = "runner" },
		func(f *flow) { f.replayOf = 1 },
	} {
		f := recordingFlow("/users/42")
		change(&f)
		a.recordCollection([]flow{f})
	}
	if len(a.requests) != 0 {
		t.Fatal("filtered or old traffic was recorded")
	}
	desktopCall(t, client, "collection-recording.stop", map[string]bool{"confirm": true})
	startRecording(t, client, nil)
	for _, change := range []func(*flow){
		func(f *flow) { f.resError = "secret-error" },
		func(f *flow) { f.resBody = "\xff\x00" },
		func(f *flow) { f.reqBody = "\xff" },
		func(f *flow) { f.reqSize = 10 },
		func(f *flow) { f.resSize = 10 },
		func(f *flow) { f.reqFile = filepath.Join(t.TempDir(), "missing") },
		func(f *flow) { f.resFile = filepath.Join(t.TempDir(), "missing"); f.resSize = 99 },
		func(f *flow) { f.resSize = request.MaxExampleBody + 1 },
		func(f *flow) { f.reqTrailer = http.Header{"Digest": {"secret"}} },
		func(f *flow) { f.url = "https://username:password@api.example.com/users/42" },
	} {
		f := recordingFlow("/users/42")
		change(&f)
		a.recordCollection([]flow{f})
	}
	if len(a.requests) != 0 || a.collectionRecording.Skipped != 10 || !a.collectionRecording.Active {
		t.Fatalf("partial/binary capture was saved: %+v", a.collectionRecording)
	}
	f := recordingFlow("/users/550e8400-e29b-41d4-a716-446655440000")
	f.reqFile, f.resFile = filepath.Join(t.TempDir(), "request"), filepath.Join(t.TempDir(), "response")
	f.reqBody, f.resBody, f.reqSize, f.resSize = "preview", "preview", 12, 11
	if err := os.WriteFile(f.reqFile, []byte(`{"input":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.resFile, []byte(`{"out":420}`), 0600); err != nil {
		t.Fatal(err)
	}
	a.recordCollection([]flow{f})
	if len(a.requests) != 1 || a.requests[0].Body != `{"input":42}` || a.requests[0].Examples[0].Body != `{"out":420}` {
		t.Fatalf("spill body lost or preview saved: %+v", a.requests)
	}
}

func TestDesktopControlCollectionRecordingFailsClosedAndDoesNotLeakDiagnostics(t *testing.T) {
	a, client := recordingApp(t)
	for _, p := range []any{
		map[string]any{"collection": "Recorded", "environment": "Local"},
		map[string]any{"collection": "", "environment": "Local", "confirm": true},
		map[string]any{"collection": "Recorded", "environment": "Missing", "confirm": true},
		map[string]any{"collection": "Recorded", "environment": "Local", "confirm": true, "path": "/tmp/elsewhere"},
		map[string]any{"collection": "Recorded", "environment": "Local", "confirm": true, "hosts": []string{"https://api.example.com"}},
		map[string]any{"collection": "Recorded", "environment": "Local", "confirm": true, "hosts": []string{"*.example.com"}},
		map[string]any{"collection": "Recorded", "environment": "Local", "confirm": true, "apps": []string{""}},
	} {
		if v := desktopCall(t, client, "collection-recording.start", p); !strings.Contains(string(v), `"error":`) {
			t.Fatalf("invalid start accepted: %s", v)
		}
	}
	startRecording(t, client, nil)
	f := recordingFlow("/users/42")
	a.recordCollection([]flow{f})
	files := libraryTree(t, a.library.root)
	var target string
	for _, file := range files {
		if strings.HasSuffix(file, requestFileSuffix) {
			target = filepath.Join(a.library.root, filepath.FromSlash(file))
		}
	}
	if err := os.WriteFile(target, []byte("external-secret-edit"), 0600); err != nil {
		t.Fatal(err)
	}
	f.resBody = `{"new":true}`
	a.recordCollection([]flow{f})
	data, _ := os.ReadFile(target)
	status := desktopCall(t, client, "collection-recording.get", nil)
	if a.collectionRecording.Active || string(data) != "external-secret-edit" || !strings.Contains(string(status), "conflict") || strings.Contains(string(status), "external-secret-edit") || strings.Contains(a.message, "recording-secret") {
		t.Fatalf("conflict not protected/sanitized: %s, disk=%s", status, data)
	}
	// Queue overflow stops explicitly, never silently drops a gap and keeps
	// claiming to record. Stop is idempotent and discards already accepted work.
	a.loadRequestLibrary()
	a.requestsProblem = ""
	startRecording(t, client, nil)
	a.collectionRecording.Busy = true // a worker owns the first accepted flow
	for range collectionRecordingQueue + 1 {
		a.recordCollection([]flow{recordingFlow("/orders/1")})
	}
	if a.collectionRecording.Active || a.collectionRecording.Discarded != collectionRecordingQueue+1 {
		t.Fatalf("overload: %+v", a.collectionRecording)
	}
	desktopCall(t, client, "collection-recording.stop", map[string]bool{"confirm": true})
	if v := desktopCall(t, client, "collection-recording.start", map[string]any{"collection": "Recorded", "environment": "Local", "confirm": true}); !strings.Contains(string(v), `"error":`) {
		t.Fatal("new worker started while old conversion is outstanding")
	}
}

func TestDesktopControlCollectionRecordingThroughCLIAndRealProxy(t *testing.T) {
	a, _ := recordingApp(t)
	// Exercise CLI.Run through the same authenticated dispatcher used by the
	// desktop, with real capture events from a real HTTP proxy, not flow fixtures.
	path := filepath.Join(t.TempDir(), "cli-control.json")
	server, err := control.Start(path, func(ctx context.Context, c control.Command) (any, error) {
		return a.dispatchControl(ctx, c, func(fn func()) { fn() })
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	var out, stderr bytes.Buffer
	if code := cli.Run(t.Context(), []string{"control", "--file", path, "--params", `{"collection":"CLI","environment":"Local"}`, "--confirm", "collection-recording.start"}, &out, &stderr); code != 0 {
		t.Fatalf("CLI start %d: %s %s", code, &out, &stderr)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"path":%q}`, r.URL.Path)
	}))
	defer upstream.Close()
	proxy, _ := url.Parse("http://" + a.srv.Addr())
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	for _, p := range []string{"/orders/18", "/orders/36"} {
		res, err := httpClient.Get(upstream.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		select {
		case f := <-a.srv.Flows():
			a.pending = append(a.pending, f)
			a.drain()
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
	if len(a.requests) != 1 || len(a.requests[0].Examples) != 1 || a.requests[0].Examples[0].Body != `{"path":"/orders/36"}` {
		t.Fatalf("real capture not consolidated: %+v", a.requests)
	}
	var status collectionRecording
	out.Reset()
	if code := cli.Run(t.Context(), []string{"control", "--file", path, "collection-recording.get"}, &out, &stderr); code != 0 || json.Unmarshal(out.Bytes(), &status) != nil || status.Added != 1 || status.Updated != 1 {
		t.Fatalf("CLI status %d: %s %s", code, &out, &stderr)
	}
	// Capture's own channel can lose traffic before the recorder sees it.
	// Saturate that real channel without draining: the recorder must stop,
	// even though its separate conversion queue was never overloaded.
	for a.srv.Dropped() == 0 {
		res, err := httpClient.Get(upstream.URL + "/orders/99")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	a.pending = append(a.pending, <-a.srv.Flows())
	a.drain()
	if a.collectionRecording.Active || !strings.Contains(a.collectionRecording.Problem, "dropped traffic") || a.collectionRecording.Updated != 1 {
		t.Fatalf("capture loss did not stop before further writes: %+v", a.collectionRecording)
	}
}

func TestDesktopControlCollectionRecordingUsesCaptureTimeAndPreservesVariableLayers(t *testing.T) {
	a, client := recordingApp(t)
	startRecording(t, client, nil)
	f := recordingFlow("/users/42")
	a.recordCollection([]flow{f})
	first := a.requests[0]
	var pathVariable string
	for k, v := range a.envs[0].Vars {
		if v == "42" {
			pathVariable = k
		}
	}
	if pathVariable == "" {
		t.Fatal("path value was not extracted")
	}
	a.envs[0].Current[pathVariable] = "7"
	desktopCall(t, client, "collection-recording.stop", map[string]bool{"confirm": true})
	startRecording(t, client, map[string]any{"collection": "Second"})
	f = recordingFlow("/users/7")
	a.recordCollection([]flow{f})
	if a.envs[0].Vars[pathVariable] != "42" || a.envs[0].Current[pathVariable] != "7" || len(a.requests) != 2 {
		t.Fatal("Current override authorized overwriting an authored shared value")
	}
	for _, tc := range []struct{ newest, older int }{{201, 200}, {404, 404}} {
		newer := recordingFlow("/users/19")
		newer.status, newer.at, newer.resBody = tc.newest, time.Now().Add(time.Second), "newest"
		older := newer
		older.status, older.at, older.resBody = tc.older, time.Now(), "older"
		a.recordCollection([]flow{newer, older})
		r := a.requests[1]
		if ex := r.Examples[len(r.Examples)-1]; ex.Body != "newest" || ex.Status != tc.newest {
			t.Fatalf("older delivery replaced newest response: %+v", ex)
		}
	}
	if a.requests[0].URL != first.URL {
		t.Fatal("recording a second destination rewrote the first request")
	}
	// Another environment's draft cannot be renamed over the target either:
	// its full-map save would delete dependencies it never snapshotted.
	a.editEnvironment("Other")
	a.varsOpen, a.varsName = true, "Local"
	a.saveVars()
	if a.varsProblem == "" || a.envs[0].Vars[pathVariable] != "42" || !a.collectionRecording.Active {
		t.Fatal("renamed environment draft overwrote recorded dependencies")
	}
	// Snapshotting the target environment must stop first, or saving the
	// complete editor map later could remove newly recorded credentials.
	a.editEnvironment("Local")
	a.varsOpen = true
	if a.collectionRecording.Active {
		t.Fatal("environment editor left recorder running")
	}
	if v := desktopCall(t, client, "collection-recording.start", map[string]any{"collection": "Third", "environment": "Local", "confirm": true}); !strings.Contains(string(v), `"error":`) {
		t.Fatal("recording started against a stale environment draft")
	}
}

func TestDesktopControlCollectionRecordingOwnsQueuedSpillsThroughRetention(t *testing.T) {
	a, client := recordingApp(t)
	startRecording(t, client, nil)
	// The capture engine owns this real spill, so retention normally removes
	// it. Hold the first worker to deterministically queue it before eviction.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, strings.Repeat("retained-", 32<<10))
	}))
	defer upstream.Close()
	proxy, _ := url.Parse("http://" + a.srv.Addr())
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	defer transport.CloseIdleConnections()
	res, err := (&http.Client{Transport: transport, Timeout: 10 * time.Second}).Get(upstream.URL + "/large/42")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	var wire capture.Flow
	select {
	case wire = <-a.srv.Flows():
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	if wire.ResponseBodyFile == "" {
		t.Fatal("fixture did not produce a spill")
	}
	a.collectionRecording.Busy = true
	a.flowLimit = 1
	a.pending = []capture.Flow{wire, {ID: wire.ID + 1, At: time.Now(), Kind: "connect", Method: "CONNECT"}}
	a.drain()
	if _, err := os.Stat(wire.ResponseBodyFile); err != nil {
		t.Fatalf("retention deleted queued body: %v", err)
	}
	a.collectionRecording.Busy = false
	a.nextCollectionRecording()
	if len(a.requests) != 1 || a.requests[0].Examples[0].Body != strings.Repeat("retained-", 32<<10) {
		t.Fatalf("evicted capture lost: %s", desktopCall(t, client, "collection-recording.get", nil))
	}
	if _, err := os.Stat(wire.ResponseBodyFile); !os.IsNotExist(err) {
		t.Fatalf("evicted spill was not released after conversion: %v", err)
	}
}
