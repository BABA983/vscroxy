package app

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"maps"
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
	body := `{"padding":"` + strings.Repeat("x", 96<<10) + `","access_token":"retained-token-123456"}`
	requestBody := strings.Repeat("request payload ", 8<<10)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	defer upstream.Close()
	proxy, _ := url.Parse("http://" + a.srv.Addr())
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(t.Context(), "POST", upstream.URL+"/large/42", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer raw-source-secret")
	res, err := (&http.Client{Transport: transport, Timeout: 10 * time.Second}).Do(req)
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
	if wire.ResponseBodyFile == "" || wire.RequestBodyFile == "" {
		t.Fatal("fixture did not produce a spill")
	}
	a.collectionRecording.Busy = true
	a.flowLimit = 1
	a.pending = []capture.Flow{wire, {ID: wire.ID + 1, At: time.Now(), Kind: "connect", Method: "CONNECT"}}
	a.drain()
	if _, err := os.Stat(wire.ResponseBodyFile); err != nil {
		t.Fatalf("retention deleted queued body: %v", err)
	}
	if len(a.collectionRecording.evidence) != 0 {
		t.Fatal("unsaved queued traffic became evidence")
	}
	a.collectionRecording.Busy = false
	a.nextCollectionRecording()
	if len(a.requests) != 1 || a.requests[0].Examples[0].Body != body {
		t.Fatalf("evicted capture lost: %s", desktopCall(t, client, "collection-recording.get", nil))
	}
	for _, file := range []string{wire.RequestBodyFile, wire.ResponseBodyFile} {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("evicted spill was not released after conversion: %v", err)
		}
	}
	s := a.collectionRecording
	if len(s.evidence) != 1 || !slices.Equal(s.EvidenceIDs, []int{wire.ID}) {
		t.Fatal("saved capture evidence missing")
	}
	e := s.evidence[0]
	if e.resBody != body || e.reqHeader.Get("Authorization") != "Bearer raw-source-secret" || e.reqBody != "" || e.reqFile != "" || e.resFile != "" || e.reqTrailer != nil || e.resTrailer != nil {
		t.Fatal("evidence is incomplete, substituted, or retains request payloads/files/trailers")
	}
	// The token sits beyond the capture's display preview. Analysis must still
	// find it after both engine spills and the source capture row are gone.
	consumer := recordingFlow("/profile")
	consumer.id, consumer.url, consumer.at = wire.ID+2, upstream.URL+"/profile", wire.At.Add(time.Second)
	consumer.reqHeader.Set("Authorization", "Bearer retained-token-123456")
	a.recordCollection([]flow{consumer})
	a.stopCollectionRecording("")
	analysis, err := recognizeTokenLinks(t.Context(), s.evidence)
	if err != nil || len(analysis.Links) != 1 || analysis.Links[0].Source != wire.ID || !slices.Equal(analysis.Links[0].Consumers, []int{consumer.id}) {
		t.Fatalf("retained evidence cannot infer link: %+v, %v", analysis, err)
	}
	status := desktopCall(t, client, "collection-recording.get", nil)
	for _, secret := range []string{"raw-source-secret", "retained-token-123456", "padding", "reqHeader", "resBody", "evidenceBytes"} {
		if strings.Contains(string(status), secret) {
			t.Fatalf("raw evidence disclosed in status: %s", secret)
		}
	}
	var exported collectionRecording
	if err := json.Unmarshal(status, &exported); err != nil || !slices.Equal(exported.EvidenceIDs, []int{wire.ID, consumer.id}) {
		t.Fatalf("evidence IDs not exported in order: %s, %v", status, err)
	}
	startRecording(t, client, nil)
	if len(a.collectionRecording.evidence) != 0 || len(a.collectionRecording.EvidenceIDs) != 0 || a.collectionRecording.evidenceBytes != 0 {
		t.Fatal("new recording retained previous session evidence")
	}
}

func TestCollectionRecordingEvidenceRejectsUnsavedAndPostStopResults(t *testing.T) {
	for _, reason := range []string{"stop", "replaced", "paused", "conversion", "variables", "library", "older"} {
		t.Run(reason, func(t *testing.T) {
			a, client := recordingApp(t)
			startRecording(t, client, nil)
			f := recordingFlow("/users/42")
			a.recordCollection([]flow{f})
			s := a.collectionRecording
			f.id++
			f.at = f.at.Add(time.Second)
			f.resBody = `{"changed":true}`
			before := a.envs[0]
			result, err := prepareRecordedCapture(f, s.collectionRecordingConfig, before, map[string]string{})
			if err != nil {
				t.Fatal(err)
			}
			result.before.Vars, result.before.Current = maps.Clone(before.Vars), maps.Clone(before.Current)
			if len(s.evidence) != 1 {
				t.Fatal("worker published evidence before a save")
			}
			s.Busy = true
			switch reason {
			case "stop":
				a.stopCollectionRecording("")
			case "replaced":
				a.collectionRecording = &collectionRecording{}
			case "paused":
				a.recordingPaused = true
			case "conversion":
				err = fmt.Errorf("private conversion error")
			case "variables":
				for name := range result.current {
					a.envs[0].Current[name] = "changed while preparing"
				}
			case "library":
				for _, file := range libraryTree(t, a.library.root) {
					if strings.HasSuffix(file, requestFileSuffix) {
						if err := os.WriteFile(filepath.Join(a.library.root, filepath.FromSlash(file)), []byte("external edit"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
			case "older":
				f.at = s.latest[result.example.ID].Add(-time.Nanosecond)
			}
			a.finishRecordedCapture(s, f, result, err)
			if s.Busy || len(s.evidence) != 1 || !slices.Equal(s.EvidenceIDs, []int{10}) || s.evidence[0].id != 10 {
				t.Fatal("unsaved/stopped work changed retained evidence")
			}
		})
	}
}

func TestCollectionRecordingEvidenceBudgets(t *testing.T) {
	// Exercise byte/count bounds without hundreds of filesystem saves. The
	// production retain path accepts this worker-prepared minimal snapshot.
	f := recordingFlow("/login")
	f.reqBody, f.resTrailer = "must not retain request body", http.Header{"Trailer": {"must not retain"}}
	result, err := prepareRecordedCapture(f, collectionRecordingConfig{Collection: "Recorded"}, environment{}, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	f = result.evidence
	for _, tc := range []struct {
		name     string
		body     int
		metadata int
		count    int
	}{
		{"count", 2, 0, tokenSelectionLimit + 3},
		{"bytes including metadata", tokenResponseLimit, 0, 18},
		{"metadata contributes to eviction", 1, 60 << 10, 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &collectionRecording{}
			f.resBody = strings.Repeat("x", tc.body)
			f.reqHeaders = strings.Repeat("m", tc.metadata)
			size := recordedEvidenceBytes(f)
			for i := range tc.count {
				f.id = i + 1
				s.retainEvidence(f)
			}
			want := min(tc.count, tokenSelectionLimit, tokenReadLimit/size)
			if len(s.evidence) != want || s.evidenceBytes != want*size || s.evidenceBytes > tokenReadLimit || s.EvidenceIDs[0] != tc.count-want+1 || s.EvidenceIDs[want-1] != tc.count {
				t.Fatalf("wrong oldest-first eviction: count=%d bytes=%d IDs=%v", len(s.evidence), s.evidenceBytes, s.EvidenceIDs)
			}
		})
	}
	t.Run("inclusive limits and ordered IDs", func(t *testing.T) {
		s := &collectionRecording{}
		f := flow{id: 2, url: strings.Repeat("m", tokenMetadataLimit), resBody: strings.Repeat("b", tokenResponseLimit)}
		s.retainEvidence(f)
		f.id = 1
		s.retainEvidence(f)
		if len(s.evidence) != 2 || !slices.Equal(s.EvidenceIDs, []int{1, 2}) || s.evidenceBytes != 2*(tokenMetadataLimit+tokenResponseLimit) {
			t.Fatal("inclusive limits rejected or capture IDs unordered")
		}
	})
	for _, tc := range []struct {
		name   string
		change func(*flow)
	}{
		{"response", func(f *flow) { f.resBody = strings.Repeat("x", tokenResponseLimit+1) }},
		{"metadata", func(f *flow) { f.reqHeaders = strings.Repeat("x", tokenMetadataLimit+1) }},
		{"header framing", func(f *flow) { f.reqHeader = http.Header{"": make([]string, tokenMetadataLimit/2+1)} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &collectionRecording{}
			f := result.evidence
			tc.change(&f)
			s.retainEvidence(f)
			if len(s.evidence) != 0 || s.evidenceBytes != 0 || len(s.EvidenceIDs) != 0 {
				t.Fatal("oversized evidence retained")
			}
		})
	}
}
