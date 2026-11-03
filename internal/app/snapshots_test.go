package app

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/hosts"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/rules"
	"github.com/baba983/vscroxy/internal/system"
	"github.com/egoist/mygo/ui"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc/metadata"
)

// snapshotProfiles is the Host profiles of the design: two on, with a
// conflict and a line that maps nothing, and a subscription.
func snapshotProfiles(a *app) {
	a.profiles.set = hosts.Set{Profiles: []hosts.Profile{
		{Name: "Staging", On: true, Text: "# Staging API\n10.0.3.21   api.example.com auth.example.com\n10.0.3.22   cdn.example.com\nfd00::12    ws.example.com\n*.internal.test = 10.0.9.1\n10.0.3.30   pay.example.com   @ Browser\n10.0.3.4O   img.example.com\n"},
		{Name: "Default", On: true, Text: "10.1.0.8 cdn.example.com\n10.1.0.9 auth.example.com @ curl\n10.1.0.10 static.example.com\n"},
		{Name: "Local", Text: "127.0.0.1 api.example.com cdn.example.com auth.example.com pay.example.com\n"},
		{Name: "Pre-release", Text: "10.2.0.1 api.example.com\n"},
		{Name: "Team hosts", URL: "https://hosts.example.com/team.hosts", Updated: time.Now().Add(-2 * time.Hour), Text: "10.9.0.1 team.example.com\n"},
	}}
	a.profiles.rev++
}

// TestUISnapshots is opt-in: normal tests neither render the matrix nor write
// files. It uses no listener, real traffic, user settings or system proxy.
func TestUISnapshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_SNAPSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_SNAPSHOTS to an output directory")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("snapshot output directory must be empty; keep the baseline and choose a new directory")
	}
	zone := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = zone })

	type snapshot struct {
		name   string
		setup  func(*app)
		act    func(*testing.T, *ui.Tester)
		want   string
		menu   string // Drawn menu label, included in tt.Image().
		toasts []ui.Toast
		short  bool // Also inspect the modal at the minimum window height.
	}
	click := func(label string) func(*testing.T, *ui.Tester) {
		return func(t *testing.T, tt *ui.Tester) {
			t.Helper()
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
		}
	}
	draft := func(a *app) {
		openDraft(a, a.flows[0])
		a.composeTab = composeTabHeaders
	}
	sent := func(a *app) {
		a.sent, a.sentSet, a.sentAt = a.draft(), true, snapshotTime
		a.response = &capture.Flow{Status: 201, Millis: 47, Bytes: 43, ResponseHeader: http.Header{"Content-Type": {"application/json"}, "X-Tag": {"first", "second"}}, ResponseBody: `{"id":73,"name":"Demo project","active":true}`}
		a.sendOutcome = "Response received"
		a.composeTests = []request.TestResult{{Name: "Created", Pass: true}, {Name: "Project active", Pass: false}}
		a.responseArrived()
	}
	response := func(a *app) {
		draft(a)
		sent(a)
	}
	// A capture edited in place, as its draft.
	edited := func(a *app) {
		a.editCapture(a.flows[0])
		a.composeURL += "?page=2"
		a.composeTab = composeTabParams
	}
	grpcDraft := func(a *app) {
		_, schema, data := protoFixture(t)
		a.addDocument(savedRequest{Protocol: "gRPC", Name: "Fetch catalog", URL: "https://api.example.com:443",
			Body: `{"query":"headphones"}`, GRPC: request.GRPCConfig{Method: "/api.Catalog/Fetch", Schema: data}}, "")
		a.grpcState().schema, a.grpcState().loaded = schema, true
	}
	grpcStream := func(a *app) {
		grpcDraft(a)
		a.grpcState().session = &grpcSession{state: "gRPC OK", finished: true, seq: 3,
			header: metadata.Pairs("content-type", "application/grpc", "x-trace", "catalog-73"), trailer: metadata.Pairs("x-result", "complete"),
			events: []grpcEvent{
				{seq: 1, at: snapshotTime, direction: "Out", text: `{"query":"headphones"}`, size: 22},
				{seq: 2, at: snapshotTime.Add(time.Second), direction: "In", text: "{\n  \"title\": \"Studio headphones\",\n  \"delta\": -7,\n  \"counts\": [3, 9],\n  \"state\": \"ACTIVE\"\n}", size: 101},
				{seq: 3, at: snapshotTime.Add(2 * time.Second), direction: "In", text: `{"title":"Travel headphones","delta":3}`, size: 39},
			}}
		for i := range a.grpcState().session.events {
			e := &a.grpcState().session.events[i]
			e.size = len(e.text)
		}
		a.grpcState().selected = 1
	}
	states := []snapshot{
		{name: "requests-grpc-empty", setup: func(a *app) {
			a.addDocument(savedRequest{Protocol: "gRPC", Name: "Untitled gRPC", URL: "https://api.example.com"}, "")
		}, want: "No gRPC call yet"},
		{name: "requests-grpc-schema", setup: func(a *app) { grpcDraft(a); a.grpcState().tab = 3 }, want: "1 method · descriptors saved with this request"},
		{name: "requests-grpc-stream", setup: grpcStream, want: "gRPC OK"},
		{name: "requests-grpc-metadata", setup: func(a *app) { grpcStream(a); a.grpcState().responseTab = 1 }, want: "Final trailers"},
		{name: "requests-grpc-error", setup: func(a *app) {
			grpcDraft(a)
			a.grpcState().session = &grpcSession{state: "gRPC PermissionDenied", problem: "rpc error: code = PermissionDenied desc = Access to catalog denied", finished: true}
		}, want: "gRPC PermissionDenied"},
		{name: "capture-empty", setup: func(a *app) { a.flows = nil; a.recount(); a.applyFilter() }, want: "Capture is stopped"},
		{name: "capture-selected", want: "Request inspector"},
		{name: "capture-activity", setup: snapshotActivity, want: "Activity strip"},
		{name: "capture-activity-range", setup: snapshotActivity, act: func(t *testing.T, tt *ui.Tester) {
			plot, ok := tt.Find("Activity time range")
			if !ok {
				t.Fatal("activity plot missing")
			}
			tt.Press(plot.X+plot.W*0.35, plot.Y+10)
			tt.Move(plot.X+plot.W*0.6, plot.Y+10)
			tt.Release(plot.X+plot.W*0.6, plot.Y+10)
		}, want: "Clear activity range"},
		{name: "capture-response-error", setup: func(a *app) {
			a.flows[0].status, a.flows[0].millis, a.flows[0].bytes = 500, 1242, 1843
			a.flows[0].resBody = `{"error":"Internal Server Error","request_id":"req_73","retryable":true}`
		}, want: "500 Internal Server Error"},
		{name: "capture-no-response", setup: func(a *app) {
			a.flows[0].status, a.flows[0].bytes, a.flows[0].resBody = 0, 0, ""
			a.flows[0].respHeader, a.flows[0].resHeaders = nil, ""
		}, want: "No response"},
		{name: "capture-edited", setup: edited, want: "Edited"},
		{name: "capture-sent", setup: func(a *app) { edited(a); sent(a) }, want: "Last attempt"},
		{name: "capture-no-matches", act: func(t *testing.T, tt *ui.Tester) {
			click("Search host or path")(t, tt)
			tt.Type("no-match")
		}, want: "No matching requests"},
		{name: "capture-multiple", setup: func(a *app) { a.chosen.Add(2) }, want: "2 selected"},
		{name: "capture-connect", setup: func(a *app) { a.flows[0].kind, a.flows[0].method = "connect", "CONNECT" }, want: "Encrypted tunnel"},
		{name: "capture-connect-failed", setup: func(a *app) {
			a.flows[0].kind, a.flows[0].method = "connect", "CONNECT"
			a.flows[0].conn = conn{errorStage: "TLS client", error: "remote error: tls: unknown certificate authority"}
		}, want: "Copy connection log"},
		{name: "capture-ws", setup: func(a *app) {
			a.flows[0].kind, a.flows[0].status = "ws", 101
			a.frames = map[int][]frame{1: {
				{seq: 1, direction: "out", kind: "text", data: `{"subscribe":"prices"}`, size: 22, kept: 22, at: snapshotTime},
				{seq: 2, direction: "in", kind: "text", data: `{"symbol":"GO","price":73}`, size: 26, kept: 26, at: snapshotTime.Add(time.Second)},
			}}
		}, want: "Messages"},
		{name: "capture-ws-protobuf", setup: func(a *app) {
			a.flows[0].kind, a.flows[0].status = "ws", 101
			a.frames = map[int][]frame{1: {{seq: 1, direction: "in", kind: "binary", data: "\x08\x49\x12\x04Demo", size: 8, kept: 8, at: snapshotTime}}}
		}, act: func(t *testing.T, tt *ui.Tester) {
			click("Messages")(t, tt)
			click("Protobuf")(t, tt)
		}, want: "1 of 1 messages"},
		{name: "capture-sse", setup: func(a *app) {
			a.flows[0].kind = "sse"
			a.frames = map[int][]frame{1: {{seq: 1, direction: "in", kind: "event", eventName: "price", eventID: "73", data: "updated", size: 7, kept: 7, at: snapshotTime}}}
		}, want: "Messages"},
		{name: "capture-diff", setup: func(a *app) {
			previous := a.flows[0]
			previous.id, previous.resBody = 3, `{"projects":[],"total":0}`
			a.flows = append([]flow{previous}, a.flows...)
			a.applyFilter()
			a.inspect.pane = inspectorDiff
		}, act: func(t *testing.T, tt *ui.Tester) {
			click("api.example.com/projects")(t, tt)
			tt.Key(0, ui.KeyDown)
		}, want: "Previous response (left)"},
		{name: "capture-debug-history", setup: func(a *app) { a.openDebugHistory(newDebugSource(a.flows[0])) }, want: "Source snapshot"},
		{name: "capture-filtered", act: func(t *testing.T, tt *ui.Tester) {
			click("Filters…")(t, tt)
			click("Filter status")(t, tt)
			tt.Type("200")
			click("Apply")(t, tt)
		}, want: "2 captured · 1 shown"},
		{name: "capture-query", act: func(t *testing.T, tt *ui.Tester) {
			click("Search host or path")(t, tt)
			tt.Type("status:2xx app:Browser projects method:")
		}, want: "Remove condition: status:2xx"},
		{name: "capture-query-suggest", act: func(t *testing.T, tt *ui.Tester) {
			click("Search host or path")(t, tt)
			tt.Type("status:4xx st")
			tt.Frame()
		}, want: "status:"},
		{name: "capture-query-values", act: func(t *testing.T, tt *ui.Tester) {
			click("Search host or path")(t, tt)
			tt.Type("method:")
			tt.Frame()
		}, want: "method:POST"},
		{name: "capture-query-invalid", act: func(t *testing.T, tt *ui.Tester) {
			click("Search host or path")(t, tt)
			tt.Type("host:api.example.com status:abc ")
		}, want: "Remove condition: host:api.example.com"},
		{name: "capture-timing-hover", setup: func(a *app) {
			f := &a.flows[0]
			f.millis, f.wait, f.download = 300, 240, 60
			f.conn = conn{resolved: "203.0.113.7", lookupMs: 25, dnsFresh: true, flows: 1, tls: "TLS 1.3"}
		}, act: func(t *testing.T, tt *ui.Tester) {
			r, ok := tt.Find("Response result")
			if !ok {
				t.Fatal("headline missing")
			}
			tt.Move(r.X+20, r.Y+r.H-14)
			tt.Advance(time.Second)
		}, want: "DNS lookup"},
		{name: "capture-guide", setup: func(a *app) {
			a.flows, a.guide, a.certificate.checked = nil, true, true
			a.systemProxy.read = func(string) system.ProxyCheck { return system.ProxyCheck{Status: system.ProxyOff} }
			a.recount()
			a.applyFilter()
		}, want: "Setup guide"},
		{name: "capture-guide-decrypt", setup: func(a *app) {
			a.flows = []flow{
				{id: 1, live: true, kind: "connect", method: "CONNECT", host: "api.example.com:443", process: "Browser", processPath: "/Applications/Browser.app/Contents/MacOS/Browser"},
				{id: 2, live: true, kind: "connect", method: "CONNECT", host: "cdn.example.com:443", process: "Browser", processPath: "/Applications/Browser.app/Contents/MacOS/Browser"},
			}
			a.guide, a.certificate.checked, a.certificate.cert = true, true, &x509.Certificate{}
			a.systemProxy.read = func(string) system.ProxyCheck { return system.ProxyCheck{Status: system.ProxyOff} }
			a.recount()
			a.applyFilter()
		}, want: "Choose what to decrypt"},
		{name: "capture-headers", setup: func(a *app) { a.inspect.tab[1] = resTabHeaders }, want: "Content-Type"},
		{name: "capture-raw", setup: func(a *app) { a.inspect.tab[1] = resTabRaw }, want: "Raw message"},
		{name: "capture-timing", setup: func(a *app) { a.inspect.tab[1] = resTabTiming }, want: "Waiting"},
		{name: "requests-empty", setup: func(a *app) { a.page = pageRequest }, want: "Open a saved request or create a new one"},
		{name: "requests-editing", setup: draft, want: "Send a request to see its response"},
		{name: "requests-header-masked", setup: func(a *app) {
			draft(a)
			a.composeHeaders += "\nAuthorization: Bearer snapshot-only"
		}, want: "Reveal header 3"},
		{name: "requests-header-revealed", setup: func(a *app) {
			draft(a)
			a.composeHeaders += "\nAuthorization: Bearer snapshot-only"
		}, act: click("Reveal header 3"), want: "Hide header 3"},
		{name: "requests-header-focus", setup: draft, act: func(t *testing.T, tt *ui.Tester) {
			click("header value 1")(t, tt)
			tt.Key(0, ui.KeyTab)
		}, want: "Remove header 1"},
		{name: "requests-body", setup: func(a *app) { draft(a); a.composeTab = composeTabBody }, want: "Request body"},
		{name: "requests-response", setup: response, want: "201 Created"},
		{name: "requests-response-hex", setup: func(a *app) {
			response(a)
			a.response.ResponseBody = "\x00\xff\r\n{{token}}"
			a.responseView = 2
		}, want: "Save response body"},
		{name: "capture-response-save", act: click("Response pane options"), menu: "Save body…"},
		{name: "requests-history", setup: snapshotHistory, want: "Rename request"},
		// One request on screen; an unsaved draft waits in the sidebar and
		// the edited saved one is marked in its collection.
		{name: "requests-open-several", setup: func(a *app) {
			draft(a)
			a.openSaved(a.requests[0].key())
			a.composeURL += "?page=2"
		}, want: "Unsaved requests"},
		{name: "requests-history-run", setup: func(a *app) { snapshotHistory(a); a.requestDocument.historyPick = 1 }, want: "Read-only"},
		{name: "requests-response-checks", setup: func(a *app) { response(a); a.responseTab = resViewTests }, want: "Project active"},
		{name: "requests-response-headers", setup: func(a *app) { response(a); a.responseTab = resViewHeaders }, want: "X-Tag"},
		{name: "requests-variables", setup: func(a *app) { draft(a); a.openVars() }, want: "Variable name 1"},
		{name: "requests-environment-menu", setup: draft, act: click("Active environment"), want: "Staging"},
		{name: "requests-collection-menu", setup: draft, act: click("Collection options"), menu: "Import cURL…"},
		{name: "requests-method-menu", setup: draft, act: click("Method list"), want: "PATCH"},
		{name: "requests-url-variables", setup: func(a *app) {
			draft(a)
			a.composeURL = "{{baseUrl}}/projects?q={{query}}&page=1"
		}, act: click("Request editor: Headers"), want: "Request variables"},
		{name: "requests-bulk-headers", setup: func(a *app) { draft(a); a.headerBulk = true }, want: "Request headers"},
		{name: "requests-params", setup: func(a *app) {
			draft(a)
			a.composeURL += "?tag=first&tag=second&page=1"
			a.composeTab = composeTabParams
		}, want: "parameter key 3"},
		{name: "requests-multipart", setup: func(a *app) {
			draft(a)
			a.composeTab, a.requestDocument.bodyMode = composeTabBody, "Multipart"
			a.formFields = []request.Field{{Name: "name", Value: "Demo"}, {Name: "upload", Value: "/tmp/example.txt", File: true}}
		}, want: "Attach file…"},
		{name: "requests-binary-empty", setup: func(a *app) {
			draft(a)
			a.composeTab, a.requestDocument.bodyMode = composeTabBody, "Binary"
		}, want: "Choose file…"},
		{name: "requests-binary-file", setup: func(a *app) {
			draft(a)
			a.composeTab, a.requestDocument.bodyMode, a.bodyFile = composeTabBody, "Binary", "/Users/demo/Uploads/firmware release 2.bin"
		}, want: "Binary body file"},
		{name: "requests-graphql-query", setup: func(a *app) {
			draft(a)
			a.composeURL = "https://api.example.com/graphql"
			a.composeTab, a.requestDocument.bodyMode = composeTabBody, "GraphQL"
			a.graphql = request.GraphQLBody{Query: "query Search($q: String!, $limit: Int) {\n  search(query: $q, limit: $limit) {\n    id\n    name\n    description\n  }\n}", Variables: "{\n  \"q\": \"{{search_term}}\",\n  \"limit\": 10\n}"}
		}, want: "GraphQL query"},
		{name: "requests-graphql-variables", setup: func(a *app) {
			response(a)
			a.composeURL = "https://api.example.com/graphql"
			a.composeTab, a.requestDocument.bodyMode, a.graphqlTab = composeTabBody, "GraphQL", 1
			a.graphql = request.GraphQLBody{Query: "query Search($q: String!, $limit: Int) {\n  search(query: $q, limit: $limit) { id name }\n}", Variables: "{\n  \"q\": \"{{search_term}}\",\n  \"limit\": 10\n}"}
		}, want: "GraphQL variables"},
		{name: "requests-scripts", setup: func(a *app) { draft(a); a.composeTab = composeTabScripts }, want: "Pre-request script"},
		{name: "requests-checks", setup: func(a *app) {
			draft(a)
			a.composeTab = composeTabChecks
			a.checks = request.Checks{Status: "201", Headers: "Content-Type: application/json", JSON: "/id = 73\n/active = true"}
		}, want: "Expected status"},
		{name: "requests-examples", setup: func(a *app) {
			draft(a)
			a.composeTab = composeTabExamples
			a.examples = []request.Example{{ID: "snapshot-example", Name: "Created project", Status: 201, Body: `{"id":73}`, Request: request.ExampleRequest{Method: "POST", URL: "{{baseUrl}}/projects"}}}
		}, want: "Example name"},
		{name: "secondary-runner-empty", setup: func(a *app) { a.requests = nil; a.openRunner() }, want: "Collection Runner"},
		{name: "secondary-runner-ready", setup: func(a *app) { a.runner.collection = "Workspace"; a.openRunner() }, want: "Run request 1"},
		{name: "secondary-runner-results", setup: snapshotRunner, want: "Inspect retained run"},
		{name: "secondary-runner-focused", setup: snapshotRunner, act: click("2 · Check project"), want: "expected 200, received 503"},
		{name: "secondary-runner-running", setup: func(a *app) { snapshotRunner(a); a.runner.running, a.runner.status = true, "Running…" }, want: "Cancel run"},
		{name: "secondary-runner-error", setup: func(a *app) { a.openRunner(); a.runner.dataProblem = "data.csv: duplicate column name" }, want: "duplicate column name"},
		{name: "secondary-runs-empty", setup: func(a *app) { a.page = pageRuns }, want: "Send a request to record an experiment."},
		{name: "secondary-runs-result", setup: snapshotRuns, want: "Experiment result"},
		{name: "secondary-runs-filtered", setup: func(a *app) {
			snapshotHistory(a)
			a.showRunRecords()
			a.experiments.status, a.experiments.sourceFilter = "4xx", "Authored"
		}, want: "Experiment #2"},
		{name: "secondary-runs-compare", setup: func(a *app) { snapshotRuns(a); a.experiments.left, a.experiments.tab = 1, 1 }, want: "Old text"},
		{name: "secondary-runs-variables", setup: func(a *app) { snapshotRuns(a); a.experiments.tab = 5 }, want: "baseUrl"},
		{name: "secondary-runs-modal", setup: func(a *app) { snapshotRuns(a); a.page = pageLive; a.experiments.open = true }, want: "Experiment result"},
		{name: "secondary-sessions-empty", setup: func(a *app) { a.page = pageSession }, want: "Open a session file or save evidence from live traffic."},
		{name: "secondary-sessions-empty-file", setup: func(a *app) {
			a.keepSession(sessionFile{Version: 1, Title: "Empty session"})
		}, want: "This saved session contains no exchanges"},
		{name: "secondary-sessions-browse", setup: snapshotSources, want: "Historical · read-only payloads"},
		{name: "secondary-sessions-selected", setup: snapshotSources, act: click("Pin"), want: "Unpin"},
		{name: "secondary-sessions-no-matches", setup: func(a *app) {
			snapshotSources(a)
			a.sessions.current().query = "no-matches"
		}, want: "Search session"},
		{name: "secondary-sessions-compare", setup: func(a *app) {
			snapshotSources(a)
			left := a.sessions.items[1].data.Flows[0].flow()
			a.sessions.left, a.sessions.leftLabel = &left, "before.json"
			a.sessions.comparing = true
		}, want: "Old text"},
		{name: "dialog-export", setup: snapshotExport, want: "Headers, trailers and cookies", short: true},
		{name: "dialog-export-ready", setup: snapshotExport, act: click("I understand the scope and will review the exported file before sharing"), want: "Save export…", short: true},
		{name: "dialog-export-unredacted", setup: snapshotExport, act: click("Export unredacted (include original values)"), want: "Save export…", short: true},
		{name: "secondary-api-empty", setup: func(a *app) {
			a.page = pageAPI
			// Preview the empty reader without changing existing no-API routing.
			a.apiChoice = "snapshot-no-selection"
		}, want: "From traffic to an API contract"},
		{name: "secondary-api-endpoints", setup: snapshotAPI, want: "Operation schema"},
		{name: "secondary-api-generated", setup: func(a *app) {
			snapshotAPI(a)
			d := a.activeAPI()
			d.saved, d.spec.Fingerprint, d.spec.Collection = nil, "previous-source", "Workspace"
		}, want: "Source collection changed"},
		{name: "secondary-api-no-endpoints", setup: func(a *app) {
			a.page = pageAPI
			a.addAPI(apiSpec{ID: "snapshot-api", Source: `{"openapi":"3.0.3","info":{"title":"Empty contract","version":"1.0"},"paths":{}}`}, true)
		}, want: "No HTTP endpoints yet"},
		{name: "secondary-api-unvalidated", setup: func(a *app) {
			snapshotAPI(a)
			d := a.activeAPI()
			d.code.Buffer.Set(d.code.Buffer.String() + "\n")
		}, want: "Validate the edited source"},
		{name: "secondary-api-source", setup: func(a *app) { snapshotAPI(a); a.activeAPI().tab = 1 }, want: "OpenAPI source"},
		{name: "secondary-api-invalid", setup: func(a *app) {
			snapshotAPI(a)
			a.activeAPI().code.Buffer.Set("openapi: broken")
			a.activeAPI().validate()
			a.activeAPI().tab = 1
		}, want: "Validation failed"},
		{name: "secondary-api-notes", setup: func(a *app) { snapshotAPI(a); a.activeAPI().tab = 2 }, want: "Review before publishing"},
		{name: "settings", setup: func(a *app) { a.settingsOpen = true }, want: "Settings"},
		{name: "dialog-settings-proxy", setup: func(a *app) { snapshotSettings(a, 1) }, want: "Save for next launch", short: true},
		{name: "dialog-settings-https", setup: func(a *app) { snapshotSettings(a, 2) }, want: "TLS key log…", short: true},
		{name: "dialog-settings-appearance", setup: func(a *app) { snapshotSettings(a, 3) }, want: "Settings"},
		{name: "dialog-settings-capture", setup: func(a *app) { snapshotSettings(a, 4) }, want: "Settings"},
		{name: "dialog-settings-allow-list", setup: func(a *app) {
			snapshotSettings(a, 4)
			a.allowOn, a.allowText = true, "api.example.com\n.internal.test"
		}, want: "Only show allowed traffic"},
		{name: "dialog-settings-devices", setup: func(a *app) { snapshotSettings(a, 5) }, want: "Settings"},
		{name: "dialog-upstream", setup: func(a *app) { a.upstreamOpen, a.upstreamAddr = true, "http://proxy.example.com:8080" }, want: "Upstream proxy"},
		{name: "dialog-ssl", setup: func(a *app) { a.sslOpen, a.sslHosts = true, "api.example.com\n*.example.org" }, want: "SSL proxying", short: true},
		{name: "dialog-ignore-hosts", setup: func(a *app) { a.ignoreOpen, a.ignoreText = true, "telemetry.example.com\n*.analytics.example.org" }, want: "Ignored hosts"},
		{name: "dialog-ignore-apps", setup: func(a *app) { a.ignoreAppsOpen, a.ignoreAppsText = true, "Updater\nBackground Sync" }, want: "Ignored apps"},
		{name: "dialog-dns-rule", setup: func(a *app) {
			snapshotProfiles(a)
			a.openDNSRule("api.example.com", "Browser")
			a.dnsRuleAddr = "192.168.1.50"
		}, want: "Map a host"},
		{name: "hosts-menu", setup: func(a *app) { snapshotProfiles(a); a.profiles.menuOpen = true }, want: "Manage profiles…"},
		{name: "capture-hosts", setup: func(a *app) {
			snapshotProfiles(a)
			a.flows[0].dns, a.flows[0].hostProfile, a.flows[0].conn.remote = "api.example.com → 10.0.3.21", "Staging:2", "10.0.3.21"
			a.flows[1].conn.remote = "93.184.216.34"
		}, want: "Hosts: Staging + Default"},
		{name: "tools-hosts", setup: func(a *app) { snapshotProfiles(a); a.openTools("hosts") }, want: "Test a host"},
		{name: "tools-hosts-test", setup: func(a *app) {
			snapshotProfiles(a)
			a.openTools("hosts")
			a.profiles.test = "api.example.com"
		}, want: "→ 10.0.3.21 · Staging line 2"},
		{name: "tools-hosts-subscription", setup: func(a *app) {
			snapshotProfiles(a)
			a.openTools("hosts")
			a.profiles.choice = 4
		}, want: "Subscription URL"},
		{name: "tools-hosts-empty", setup: func(a *app) { a.openTools("hosts") }, want: "No Host profiles"},
		{name: "dialog-key-log", setup: func(a *app) { a.openKeyLog() }, want: "TLS key log"},
		{name: "tools-rules", setup: func(a *app) { snapshotRules(a); a.srv = &capture.Server{}; a.openRules() }, want: "Scenario: Offline demo", short: true},
		{name: "tools-rules-test", setup: func(a *app) {
			snapshotRules(a)
			a.openRules()
			a.breakOn = true
			a.ruleUI.test = "GET https://api.example.com/v1/users/42"
		}, want: "2 rules act, in the order numbered"},
		{name: "tools-rules-remote", setup: func(a *app) {
			snapshotRules(a)
			a.openRules()
			a.editRule(1, a.rules[1])
			a.ruleUI.problem = "rule 2: Map Remote needs an http(s) base URL without credentials or fragment"
		}, want: "Target base URL"},
		{name: "tools-rules-regex", setup: func(a *app) {
			snapshotRules(a)
			a.openRules()
			a.editRule(4, a.rules[4])
			a.ruleUI.draft.MatchMode, a.ruleUI.draft.Match = "regex", `/v1/payments/\d+$`
		}, want: "A Go regular expression found anywhere in the full URL."},
		{name: "tools-rules-block", setup: func(a *app) {
			snapshotRules(a)
			a.openRules()
			a.editRule(2, a.rules[2])
		}, want: "Proxies it as usual but keeps it out of the table. Rules below it do not act on it."},
		{name: "tools-rules-local-body", setup: func(a *app) {
			snapshotRules(a)
			file := filepath.Join(os.TempDir(), "vscroxy-snapshot-user.json")
			_ = os.WriteFile(file, []byte("{\n  \"id\": 42,\n  \"name\": \"Ada Lovelace\",\n  \"roles\": [\"admin\"]\n}\n"), 0o600)
			a.rules[0].Target = file
			a.openRules()
		}, want: "Save rule writes it to the file."},
		{name: "tools-rules-local-folder", setup: func(a *app) {
			snapshotRules(a)
			a.rules[0].Target, a.rules[0].PathFrom = os.TempDir(), "/v1"
			a.openRules()
		}, want: "Strip path prefix"},
		{name: "tools-rules-examples", setup: func(a *app) {
			snapshotRules(a)
			var made []capture.Rule
			for _, e := range []struct {
				name, url, example string
				status             int
				named              bool
			}{
				{"Get project", "https://api.example.com/projects/:id", "Not found", 404, true},
				{"Get project", "https://api.example.com/projects/:id", "Created", 200, false},
				{"List projects", "{{baseUrl}}/projects", "Two projects", 200, false},
			} {
				r := capture.Rule{Name: e.name + " · " + e.example, Type: capture.RuleMapLocal, MatchMode: "regex", Match: capture.MockPattern(e.url), Method: "GET",
					Status: e.status, Target: "/Users/me/.vscroxy/mocks/" + strings.ToLower(strings.ReplaceAll(e.example, " ", "-")) + ".json", Scenario: "Workspace"}
				if e.named {
					r.When = []rules.HeaderCondition{{Name: mockResponseName, Op: rules.HeaderEquals, Value: e.example}}
				}
				made = append(made, r)
			}
			for i := range a.rules {
				a.rules[i].ScenarioActive = false
			}
			a.rules = append(a.rules, made...)
			a.editRule(len(a.rules)-3, a.rules[len(a.rules)-3])
			a.openTools("rules")
		}, want: "Get project · Not found"},
		{name: "tools-rules-empty", setup: func(a *app) { a.openRules() }, want: "No rules"},
		{name: "tools-rules-script", setup: func(a *app) {
			snapshotRules(a)
			snapshotScripts(a)
			a.rules = append(a.rules, capture.Rule{Name: "Debug headers", Type: capture.RuleScript, Match: "api.example.com/v1/", Target: "Debug headers.js"})
			a.editRule(len(a.rules)-1, a.rules[len(a.rules)-1])
			a.openTools("rules")
		}, want: "Edit script"},
		{name: "tools-scripts", setup: func(a *app) {
			snapshotScripts(a)
			a.rules = []capture.Rule{{Name: "Debug headers", Type: capture.RuleScript, Match: "api.example.com/v1/", Target: "Debug headers.js"}}
			a.openScript("Debug headers")
		}, want: "Run by 1 rule"},
		{name: "tools-scripts-problem", setup: func(a *app) {
			snapshotScripts(a)
			a.openScript("Broken")
		}, want: "No rule runs it yet"},
		{name: "tools-scripts-empty", setup: func(a *app) { a.openTools("scripts") }, want: "No scripts"},
		{name: "tools-addons", setup: func(a *app) {
			snapshotScripts(a)
			a.addons = scriptStore{addon: true, names: []string{"sign"}, texts: map[string]string{"sign": newAddonText("sign")}, problems: map[string]error{}}
			a.addonsShown = true
			a.openTools("scripts")
		}, want: "Required by 1 script"},
		{name: "tools-addons-empty", setup: func(a *app) { a.addons.addon, a.addonsShown = true, true; a.openTools("scripts") }, want: "No addons"},
		{name: "tools-scenarios", setup: func(a *app) { snapshotRules(a); a.openTools("scenarios") }, want: "On: its rules apply"},
		{name: "tools-scenarios-empty", setup: func(a *app) { a.openTools("scenarios") }, want: "No scenarios"},
		{name: "tools-mocks", setup: func(a *app) { snapshotRules(a); a.openTools("mocks") }, want: "Map Local rules: answers from local files"},
		{name: "tools-mocks-empty", setup: func(a *app) { a.openTools("mocks") }, want: "No mocks"},
		{name: "dialog-config-import", setup: func(a *app) {
			snapshotRules(a)
			b, _ := a.exportConfig()
			b.Exported = snapshotTime
			b.Mocks = map[string][]byte{"user.json": []byte("{}")}
			b.Scripts = map[string]string{"Debug headers": ""}
			b.Hosts = &hosts.Set{Profiles: []hosts.Profile{{Name: "Staging"}, {Name: "Default"}}}
			a.configImport = configImport{open: true, path: "/Users/me/vscroxy-config.json", bundle: b, parts: [3]bool{true, true, false}}
		}, want: "Rules, mocks and scripts", short: true},
		{name: "dialog-breakpoint", setup: snapshotBreakpoint, want: "Breakpoint · request paused", short: true},
		{name: "dialog-ws-breakpoint-text", setup: func(a *app) {
			a.socket.open = true
			a.bp = &capture.Breakpoint{Phase: capture.PhaseMessage, URL: "wss://api.example.com/socket", Direction: "out", MessageType: "text"}
			a.bpOpen, a.bpBody = true, `{"action":"subscribe","topic":"prices"}`
		}, want: "Breakpoint · WebSocket message paused", short: true},
		{name: "dialog-ws-breakpoint-binary", setup: func(a *app) {
			a.bp = &capture.Breakpoint{Phase: capture.PhaseMessage, URL: "wss://api.example.com/socket", Direction: "in", MessageType: "binary"}
			a.bpOpen, a.bpBody = true, "AP6AAQ=="
		}, want: "Server → client · binary", short: true},
		{name: "dialog-ws-breakpoint-invalid", setup: func(a *app) {
			a.bp = &capture.Breakpoint{Phase: capture.PhaseMessage, URL: "wss://api.example.com/socket", Direction: "out", MessageType: "binary"}
			a.bpOpen, a.bpBody = true, "/wA"
		}, want: "Enter canonical padded base64 for the binary message.", short: true},
		{name: "dialog-ws-breakpoint-readonly", setup: func(a *app) {
			a.bp = &capture.Breakpoint{Phase: capture.PhaseMessage, URL: "wss://api.example.com/socket", Direction: "in", MessageType: "binary", BodyReadOnly: true}
			a.bpOpen = true
		}, want: "Message exceeds the editor limit or is invalid UTF-8. Continue unchanged or drop it.", short: true},
		{name: "dialog-breakpoint-readonly", setup: func(a *app) {
			snapshotBreakpoint(a)
			a.bp.Phase, a.bp.BodyReadOnly, a.bpStatus = capture.PhaseResponse, true, "503"
		}, want: "Breakpoint · response paused", short: true},
		{name: "dialog-breakpoint-templates", setup: func(a *app) {
			snapshotBreakpoint(a)
			body := `{"name":"Admin project","owner":"admin"}`
			a.bpTemplates = []bpTemplate{{Name: "Admin project", Phase: "request", Method: "POST", Headers: "Authorization: Bearer test-admin\nContent-Type: application/json", Body: &body}}
			a.applyBreakTemplate(a.bpTemplates[0])
		}, want: "Template: Admin project", short: true},
		{name: "dialog-breakpoint-template-save", setup: func(a *app) {
			snapshotBreakpoint(a)
			a.bpSaving, a.bpName = true, "Create project"
		}, want: "Cancel", short: true},
		{name: "dialog-vault", setup: func(a *app) {
			a.vault.open, a.vault.loaded = true, true
			a.vault.rows = []vaultRow{{name: "demo_token", value: "fake-snapshot-token", domains: "api.example.com"}}
		}, want: "Let scripts use pm.vault", short: true},
		{name: "dialog-vault-revealed", setup: func(a *app) {
			a.vault.open, a.vault.loaded = true, true
			a.vault.rows = []vaultRow{{name: "demo_token", value: "fake-snapshot-token", domains: "api.example.com", revealed: true}}
		}, want: "Hide secret 1"},
		{name: "dialog-vault-reset", setup: func(a *app) {
			a.vault.open, a.vault.loaded, a.vault.resetting = true, true, true
			a.vault.problem = "Vault key unavailable; the original encrypted file has not been replaced."
		}, want: "Delete all secrets and start over", short: true},
		{name: "dialog-cookies", setup: func(a *app) {
			a.cookieUI = cookieEditor{open: true, url: "https://api.example.com/", name: "demo", value: "fake-cookie", path: "/", secure: true, httpOnly: true}
		}, want: "Session Cookies", short: true},
		{name: "dialog-column", setup: func(a *app) { a.openColumn(); a.columnDraft.Name = "x-request-id" }, want: "Add column"},
		{name: "dialog-folder", setup: func(a *app) { a.openFolder("Workspace") }, want: "Collection · Workspace", short: true},
		{name: "dialog-code", setup: func(a *app) { draft(a); a.openCode() }, want: "Code language", short: true},
		{name: "dialog-curl-error", setup: func(a *app) {
			a.curlOpen, a.curlInput, a.curlProblem = true, "curl $(unsafe-command)", "shell substitutions are not supported; paste literal values instead"
		}, want: "Import cURL", short: true},
		{name: "dialog-import-report", setup: func(a *app) {
			a.importReport = importReport{open: true, title: "Import review", intro: "Imported requests; review unsupported fields before sending."}
			for i := range 16 {
				a.importReport.lines = append(a.importReport.lines, fmt.Sprintf("Request %d: unsupported collection option was not imported.", i+1))
			}
		}, want: "Import review", short: true},
		{name: "dialog-save-collection", setup: func(a *app) { a.openCollectionSave(a.flows) }, want: "Save 2 requests to collection"},
		{name: "dialog-replay", setup: func(a *app) { a.openReplay(a.flows) }, want: "Replay 2 requests"},
		{name: "dialog-replay-error", setup: func(a *app) { a.openReplay(a.flows); a.replayUI.timeout = "86401" }, want: "Enter 0–86400 seconds."},
		{name: "dialog-replay-busy", setup: func(a *app) {
			a.openReplay(a.flows)
			a.replayUI.updates, a.replayUI.cancel = make(chan error), func() {}
		}, want: "Stop replay"},
		{name: "dialog-protobuf", setup: func(a *app) { a.protobuf.open, a.protobuf.problem = true, "Missing import: common.proto" }, want: "Protobuf schema", short: true},
		{name: "dialog-protobuf-busy", setup: func(a *app) { a.protobuf.open, a.protobuf.loading = true, true }, want: "Compiling and linking schema…"},
		{name: "dialog-socket", setup: func(a *app) { a.socket.open, a.socket.url = true, "wss://api.example.com/events" }, want: "WebSocket session · Not connected", short: true},
		{name: "dialog-socket-protobuf", setup: func(a *app) {
			a.socket.open, a.socket.url = true, "wss://api.example.com/events"
			a.socket.session = &wsSession{state: "Disconnected", events: []wsSessionEvent{{Sequence: 1, At: snapshotTime, Direction: "In", Kind: websocket.BinaryMessage, Data: []byte("\x08\x49\x12\x04Demo"), Size: 8}}}
		}, act: func(t *testing.T, tt *ui.Tester) {
			click("Session message 1")(t, tt)
			click("Protobuf")(t, tt)
		}, want: "1 of 1 messages", short: true},
		{name: "dialog-socket-truncated", setup: func(a *app) {
			a.socket.open, a.socket.url = true, "wss://api.example.com/events"
			a.socket.session = &wsSession{state: "Disconnected", events: []wsSessionEvent{{Sequence: 1, At: snapshotTime, Direction: "In", Kind: websocket.BinaryMessage, Data: []byte("\x08\x49"), Size: 512}}}
		}, act: func(t *testing.T, tt *ui.Tester) {
			click("Session message 1")(t, tt)
			click("Protobuf")(t, tt)
		}, want: "Incomplete message: 2 of 512 bytes retained", short: true},
		{name: "dialog-socket-payload", setup: func(a *app) {
			a.socket.open, a.socket.url = true, "wss://api.example.com/events"
			a.socket.encoding, a.socket.message, a.socket.subprotocols = "Hex", "01 7f a3", "events.v2, events.v1"
			a.socket.session = &wsSession{state: "Disconnected", events: []wsSessionEvent{
				{Sequence: 1, At: snapshotTime, Direction: "In", Kind: websocket.TextMessage, Data: []byte(`{"event":"ready"}`), Size: 17},
				{Sequence: 2, At: snapshotTime.Add(time.Second), Direction: "In", Kind: websocket.BinaryMessage, Data: []byte{0x01, 0x7f, 0xa3}, Size: 512},
			}}
		}, act: func(t *testing.T, tt *ui.Tester) {
			tt.Scroll(600, 400, 0, 300)
			click("Session message 2")(t, tt)
		}, want: "Session payload", short: true},
		{name: "dialog-api-generation", setup: func(a *app) { a.apiGenerateOpen, a.apiCollection = true, "Workspace" }, want: "Generate OpenAPI draft"},
		{name: "dialog-devices", setup: func(a *app) {
			a.deviceUI = deviceSetup{open: true, ips: []string{"192.168.1.10"}, ip: "192.168.1.10", port: "7891", allowed: "192.168.1.20"}
		}, want: "LAN off", short: true},
		{name: "dialog-client", setup: func(a *app) {
			a.clientUI = clientSetup{open: true, runtime: "cURL", url: "https://api.example.com/projects"}
		}, want: "Copy client example", short: true},
		{name: "secondary-diagnostics", setup: func(a *app) {
			snapshotSettings(a, 0)
			a.settingsOpen = false
			a.diagnostics.target = "https://api.example.com/projects"
			a.openDiagnostics(nil, diagnoseMac)
		}, want: "Connection diagnostics"},
		{name: "secondary-diagnostics-failure", setup: func(a *app) {
			snapshotSettings(a, 0)
			a.settingsOpen = false
			f := a.flows[0]
			f.id, f.kind, f.method, f.url, f.host, f.path, f.status = 99, "connect", "CONNECT", "https://api.example.com:443", "api.example.com", "", 0
			f.conn.errorStage, f.conn.error = "TLS client", "remote error: tls: unknown certificate authority"
			f.live = true
			a.flows = append(a.flows, f)
			a.openDiagnostics(&a.flows[len(a.flows)-1], diagnoseMac)
		}, want: "Possible causes · not confirmed"},
		{name: "secondary-diagnostics-device", setup: func(a *app) {
			snapshotSettings(a, 0)
			a.settingsOpen = false
			a.diagnostics.target = "https://api.example.com/projects"
			a.openDiagnostics(nil, diagnoseDevice)
		}, want: "Device access is off"},
		{name: "dialog-clear", setup: func(a *app) { a.clearOpen = true }, want: "Clear captured requests?"},
		{name: "dialog-clear-records", setup: func(a *app) { snapshotRuns(a); a.experiments.clearing = true }, want: "Clear History?"},
		{name: "dialog-mark", setup: func(a *app) { a.openMark(a.flows[0]); a.markNote = "Investigate cache response" }, want: "Note"},
		{name: "dialog-mark-keyboard", setup: func(a *app) { a.openMark(a.flows[0]) }, act: func(t *testing.T, tt *ui.Tester) {
			tt.Key(0, ui.KeyTab)
			tt.Key(0, ui.KeySpace)
		}, want: "Mark Orange", short: true},
		{name: "dialog-mark-primary-hover", setup: func(a *app) { a.openMark(a.flows[0]) }, act: func(t *testing.T, tt *ui.Tester) {
			box, ok := tt.Find("Save")
			if !ok {
				t.Fatal("missing primary action")
			}
			tt.Move(box.X+box.W/2, box.Y+box.H/2)
		}, want: "Save"},
		{name: "dialog-mark-primary-pressed", setup: func(a *app) { a.openMark(a.flows[0]) }, act: func(t *testing.T, tt *ui.Tester) {
			box, ok := tt.Find("Save")
			if !ok {
				t.Fatal("missing primary action")
			}
			tt.Press(box.X+box.W/2, box.Y+box.H/2)
		}, want: "Save"},
		{name: "dialog-delete", setup: func(a *app) { a.confirmDeleteFolder("Workspace") }, want: "Delete collection"},
		{name: "dialog-close-unsaved", setup: func(a *app) { draft(a); a.closingDocument = a.requestDocument }, want: "Save changes?"},
		{name: "dialog-rename", setup: func(a *app) { draft(a); a.renameOpen = true }, want: "Rename request"},
		{name: "dialog-header-warning", setup: func(a *app) { draft(a); a.requestDocument.headerWarn = true }, want: "Bulk edit shows credentials"},
		{name: "commands", setup: func(a *app) { a.workbench.openPalette() }, want: "Find command"},
		{name: "commands-unavailable", setup: func(a *app) { a.workbench.openPalette(); a.workbench.query = "Send request" }, want: "unavailable"},
		{name: "active-tools", setup: snapshotActiveTools, want: "Disable Scenario: Offline"},
		{name: "requests-active-tools", setup: func(a *app) { snapshotActiveTools(a); draft(a) }, want: "Disable No Caching"},
		{name: "shell-menu", act: click("More"), menu: "Network"},
		{name: "tools-menu", setup: snapshotActiveTools, act: click("Tools"), menu: "No Caching"},
		{name: "network-submenu", act: func(t *testing.T, tt *ui.Tester) {
			click("More")(t, tt)
			if err := chooseMenu(tt, "Network"); err != nil {
				t.Fatal(err)
			}
		}, menu: "No Caching"},
		{name: "notifications", toasts: []ui.Toast{
			{Title: "Session exported", Type: "success"},
			{Title: "Rules apply to new requests", Type: "info"},
			{Title: "Replay failed", Description: "The upstream connection was refused.", Type: "error", Action: "Inspect request"},
		}, want: "Inspect request"},
		{name: "notification-error", setup: func(a *app) {
			a.done("Saved List projects")
			a.fail("Save session", errors.New("open /home/user/.vscroxy/sessions/1f3c.json: no space left on device"))
		}, want: "no space left on device"},
		{name: "notification-warning", toasts: []ui.Toast{{Title: "Response body incomplete", Description: "The captured bytes are still available.", Type: "warning"}}, want: "Response body incomplete"},
		{name: "capture-menu", act: click("More capture options"), menu: "Clear unpinned requests…"},
		{name: "collections-menu", act: click("Collection options"), menu: "Generate API from collection…"},
		{name: "body-menu", act: click("Response body format"), menu: "Pretty"},
		{name: "inspector-menu", act: click("Inspector options"), menu: "Replay…"},
		{name: "request-menu", setup: draft, act: click("Request options"), menu: "Copy as cURL"},
		{name: "collection-folder-menu", setup: func(a *app) {
			a.page = pageRequest
			a.requests[0].Examples = []request.Example{{ID: "e", Name: "Two projects", Status: 200, Body: "[]"}}
		}, act: func(t *testing.T, tt *ui.Tester) {
			if err := tt.RightClick("Collection: Workspace"); err != nil {
				t.Fatal(err)
			}
		}, menu: "Mock “Workspace” from examples"},
		{name: "traffic-menu", act: func(t *testing.T, tt *ui.Tester) {
			if err := tt.RightClick("api.example.com/projects"); err != nil {
				t.Fatal(err)
			}
		}, menu: "Edit request"},
	}
	for _, auth := range []string{"Bearer", "Basic", "API key", request.AuthOAuth2, request.AuthDigest, request.AuthAWS, request.AuthJWT} {
		states = append(states, snapshot{name: "requests-auth-" + auth, setup: func(a *app) {
			draft(a)
			a.composeTab, a.auth.Type = composeTabAuth, auth
		}, want: "Authorization type"})
	}
	for _, mode := range []string{"Auto", "Pretty", "JSON", "Hex", "Preview", "Protobuf"} {
		want := map[string]string{"Auto": "Body text", "Pretty": "Body text", "JSON": "projects", "Hex": "00000000", "Preview": "Rich text", "Protobuf": "1 of 1 messages"}[mode]
		states = append(states, snapshot{name: "body-" + mode, setup: func(a *app) {
			if mode == "Preview" {
				a.flows[0].respHeader.Set("Content-Type", "text/html")
				a.flows[0].resBody = "<h1>Snapshot preview</h1><p>Rendered locally, without a web view.</p>"
			}
			if mode == "Protobuf" {
				a.flows[0].respHeader.Set("Content-Type", "application/protobuf")
				a.flows[0].resBody = "\x08\x49\x12\x04Demo"
			}
		}, act: func(t *testing.T, tt *ui.Tester) { chooseBodyFormat(t, tt, "response", mode) }, want: want})
	}
	states = append(states, snapshot{name: "body-image", setup: func(a *app) {
		img := image.NewRGBA(image.Rect(0, 0, 24, 24))
		for y := range 24 {
			for x := range 24 {
				img.SetRGBA(x, y, color.RGBA{R: uint8(x * 10), G: uint8(y * 10), B: 128, A: 255})
			}
		}
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			t.Fatal(err)
		}
		a.flows[0].respHeader.Set("Content-Type", "image/png")
		a.flows[0].resBody = b.String()
	}, want: "24 × 24 px"})

	for step, label := range collectionSteps {
		states = append(states, snapshot{name: "dialog-collection-" + strings.ToLower(label), setup: func(a *app) {
			a.openCollectionWizard(collectionWizardFlows())
			a.collectionUI.step, a.collectionUI.grouping = step, 1
			a.collectionUI.name = "Example API"
			a.collectionUI.organize()
		}, want: "Nothing is saved until Create.", short: true})
	}
	states = append(states, snapshot{name: "dialog-current-values", setup: func(a *app) {
		a.envs[0].Current = map[string]string{"authorization": "Bearer local-only-fixture"}
		a.openVars()
	}, want: "Current values · local only", short: true})
	states = append(states, snapshot{name: "collection-matched", setup: func(a *app) {
		a.requests[0].Name = "Get project"
		a.requests[0].URL = "https://api.example.com/projects"
	}, act: click("Matches: Get project"), want: "Save response as example"})

	manifest := struct {
		Platform string
		Go       string
		Scale    int
		Files    map[string]string
	}{runtime.GOOS + "/" + runtime.GOARCH, runtime.Version(), 1, map[string]string{}}
	write := func(t *testing.T, name string, data []byte) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		manifest.Files[filepath.ToSlash(name)] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		for _, state := range states {
			widths := []int{1280}
			if state.name == "capture-selected" || strings.HasPrefix(state.name, "capture-activity") || state.name == "capture-response-error" || state.name == "capture-no-response" || state.name == "capture-edited" || state.name == "capture-sent" || state.name == "capture-ws" || state.name == "capture-connect" || strings.HasPrefix(state.name, "requests-") || strings.HasPrefix(state.name, "secondary-") || strings.HasPrefix(state.name, "dialog-") || state.name == "active-tools" {
				widths = append(widths, 960)
			}
			// Live traffic reads in both layouts: the inspector beside the
			// list (the default) and below it.
			layouts := []string{"right"}
			if strings.HasPrefix(state.name, "capture-") {
				layouts = append(layouts, "bottom")
			}
			for _, layout := range layouts {
				for _, width := range widths {
					heights := []int{800}
					if state.short && width == 960 {
						heights = append(heights, 600)
					}
					for _, height := range heights {
						name := fmt.Sprintf("%s-%d", state.name, width)
						if height != 800 {
							name += fmt.Sprintf("x%d", height)
						}
						if layout == "bottom" {
							name += "-stacked"
						}
						t.Run(theme+"/"+name, func(t *testing.T) {
							a := snapshotApp()
							a.library.root = filepath.Join(t.TempDir(), "collections")
							a.appearance = appearanceOf(dark)
							a.workbench.layout = layout
							t.Cleanup(a.protobuf.stop)
							t.Cleanup(a.socket.protobuf.stop)
							if state.setup != nil {
								state.setup(a)
							}
							announced := false
							tt := ui.NewTester(func(c *ui.Context) {
								if !announced {
									for _, toast := range state.toasts {
										c.AddToast(toast)
									}
									announced = true
								}
								a.view(c)
							}, width, height)
							tt.SetLocale("en-US")
							if state.act != nil {
								state.act(t, tt)
							}
							// Protobuf decoding is asynchronous. Wait for completion, not
							// an arbitrary sleep, and never capture an intermediate frame.
							deadline := time.NewTimer(time.Minute)
							defer deadline.Stop()
							for a.protobuf.answers != nil || a.socket.protobuf.answers != nil {
								select {
								case <-deadline.C:
									t.Fatal("protobuf render did not settle")
								default:
								}
								runtime.Gosched()
								tt.Frame()
							}
							tt.Advance(250 * time.Millisecond) // Complete toast entry motion.
							tt.Frame()
							if state.want != "" && !tt.HasText(state.want) {
								t.Fatalf("missing %q: %v", state.want, tt.Texts())
							}
							base := filepath.Join(theme, name)
							if state.menu != "" {
								if !tt.HasText(state.menu) {
									t.Fatalf("missing menu item %q: %v", state.menu, tt.Texts())
								}
							}
							var b bytes.Buffer
							if err := png.Encode(&b, tt.Image()); err != nil {
								t.Fatal(err)
							}
							write(t, base+".png", b.Bytes())
						})
					}
				}
			}
		}
	}
	if t.Failed() {
		return // A partial run must not publish a success manifest.
	}
	data, err := json.Marshal(manifest, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d artifacts to %s", len(manifest.Files), dir)
}

var snapshotTime = time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)

func snapshotSettings(a *app, tab int) {
	a.settingsOpen, a.settingsTab = true, tab
	// Render known state; never read or write this machine's proxy/CA setup.
	a.systemProxy.checked, a.systemProxy.shown = true, true
	a.systemProxy.check.Target = a.systemProxyTarget()
	a.certificate.checked = true
}

func snapshotBreakpoint(a *app) {
	a.bp = &capture.Breakpoint{ID: 1, Method: "POST", URL: "https://api.example.com/projects"}
	a.bpOpen, a.bpMethod, a.bpURL = true, "POST", a.bp.URL
	a.bpHeaders, a.bpBody = "Content-Type: application/json\nX-Trace: demo", `{"name":"Demo project"}`
}

func snapshotRules(a *app) {
	a.rules = []capture.Rule{
		{Name: "Mock user profile", Type: capture.RuleMapLocal, Match: "api.example.com/v1/users/", Method: "GET", Target: "/Users/me/mocks/user.json", Status: 200, Scenario: "Offline demo", ScenarioActive: true},
		{Name: "Orders → staging", Type: capture.RuleMapRemote, MatchMode: "wildcard", Match: "*.example.com/v1/orders/*", Target: "https://staging.example.com:8443", PathFrom: "/v1/", PathTo: "/api/v1/", KeepHost: true,
			Apps: []string{"Google Chrome*"}, When: []rules.HeaderCondition{{Name: "X-Env", Op: "equals", Value: "staging"}, {Name: "Authorization", Op: "present"}}},
		{Name: "Hide analytics", Type: capture.RuleBlock, Block: capture.BlockHide, Match: "analytics.example.com", Apps: []string{"Google Chrome*"}},
		{Name: "Pause user requests", Type: capture.RuleBreak, Match: "/v1/users"},
		{Name: "Inspect payments", Type: capture.RuleBreakResponse, Match: "/v1/payments", Method: "POST"},
		{Name: "Offline users", Type: capture.RuleBlock, Match: "api.example.com/v1/users", Scenario: "Offline demo", ScenarioActive: true},
	}
}

func snapshotScripts(a *app) {
	s := &a.scripts
	s.names = []string{"Broken", "Debug headers"}
	s.texts = map[string]string{
		"Debug headers": "// Tag requests to the API, and see what came back.\nimport { sign } from \"sign\";\n\nexport function onRequest() {\n  pm.request.headers.upsert(\"X-Signature\", sign(pm.request.body.raw, process.env.SIGNING_KEY));\n  pm.request.headers.upsert(\"X-Debug\", \"1\");\n  pm.variables.set(\"startedAt\", Date.now());\n}\n\nexport function onResponse() {\n  console.log(pm.response.code, Date.now() - Number(pm.variables.get(\"startedAt\")), \"ms\");\n  if (pm.response.code >= 500) pm.response.code = 200;\n}\n",
		"Broken":        "export function onRequest() {\n  pm.request.headers.upsert(\n}\n",
	}
	s.problems = map[string]error{}
	s.parsed = map[string]*request.ProxyScript{}
	for name, text := range s.texts {
		if p, err := request.ParseProxyScript(name, text); err != nil {
			s.problems[name] = err
		} else {
			s.parsed[scriptFile(name)] = p
		}
	}
	s.console = []scriptLog{
		{at: snapshotTime, script: "Debug headers", rule: "Debug headers", phase: "response", kind: "log", text: "200 48 ms"},
		{at: snapshotTime.Add(2 * time.Second), script: "Debug headers", rule: "Debug headers", phase: "response", kind: "log", text: "503 112 ms"},
		{at: snapshotTime.Add(3 * time.Second), script: "Debug headers", rule: "Debug headers", phase: "request", kind: "error", text: "TypeError: Cannot read property 'id' of undefined at onRequest (Debug headers.js:5:58(9))"},
	}
}

func snapshotActiveTools(a *app) {
	// State only: never start a listener or create user settings/CA files.
	a.srv = &capture.Server{}
	a.breakOn, a.throttle, a.upstream = true, capture.Presets[1], "http://127.0.0.1:8081"
	a.srv.SetBreak(true)
	a.srv.SetThrottle(a.throttle)
	a.srv.SetNoCaching(true)
	_ = a.srv.SetUpstream(a.upstream)
	a.rules = []capture.Rule{{Type: capture.RuleBlock, Match: "/offline", Scenario: "Offline", ScenarioActive: true}}
	_ = a.srv.SetRules(a.rules)
}

func snapshotApp() *app {
	a := workspaceApp()
	a.envs = []environment{{Name: "Local", Vars: map[string]string{"baseUrl": "https://api.example.com"}}, {Name: "Staging", Vars: map[string]string{"baseUrl": "https://staging.example.com"}}}
	a.activeEnv = "Local"
	a.flows[0].at, a.flows[1].at = snapshotTime, snapshotTime.Add(time.Second)
	a.flows[0].millis, a.flows[0].bytes = 47, 83
	a.flows[0].wait, a.flows[0].download = 32, 15
	a.flows[1].millis, a.flows[1].bytes = 132, 41
	a.flows[0].reqBody = `{"name":"Demo project"}`
	a.flows[0].reqHeader.Set("Content-Type", "application/json")
	a.flows[0].resBody = `{"projects":[{"id":73,"name":"Demo project","active":true}],"total":1}`
	a.flows[1].respHeader = http.Header{"Content-Type": {"application/json"}}
	a.flows[1].resBody = `{"error":"Unauthorized","code":"AUTH_401"}`
	a.pickedID = 1
	a.chosen.Add(1)
	a.recount() // the flows changed in place
	openResponseBody(a)
	return a
}

func snapshotActivity(a *app) {
	base := a.flows[0]
	a.flows = nil
	for bucket := range 60 {
		n := 1 + bucket*7%6
		if bucket >= 22 && bucket < 29 {
			n += 8
		}
		for j := range n {
			f := base
			f.id, f.debugKey = len(a.flows)+1, ""
			f.at = snapshotTime.Add(-5*time.Minute + time.Duration(bucket)*5*time.Second + time.Duration(j)*time.Millisecond)
			f.path = fmt.Sprintf("/v1/projects/%d", f.id)
			f.url = "https://" + f.host + f.path
			f.status = 200
			switch {
			case bucket >= 22 && bucket < 29 && j >= n-3:
				f.status, f.millis = 500, 1242
			case bucket%9 == 0 && j == 0:
				f.status = 404
			case bucket%7 == 0 && j == 1:
				f.status = 302
			}
			a.flows = append(a.flows, f)
		}
	}
	a.chosen.Clear()
	a.picked, a.pickedID = 0, 1
	a.recount()
	a.applyFilter()
}

func snapshotRuns(a *app) {
	a.page = pageRuns
	for i, body := range []string{`{"id":73,"active":false}`, `{"id":73,"active":true}`} {
		draft := savedRequest{Name: "Create project", Method: "POST", URL: "https://api.example.com/projects", Body: `{"name":"Demo project"}`}
		r := snapshotExperiment(i+1, draft, "Local", request.Execution{
			Response:  &capture.Flow{Method: "POST", URL: draft.URL, Status: 201, Millis: 47, Bytes: len(body), ResponseHeader: http.Header{"Content-Type": {"application/json"}}, ResponseBody: body},
			Tests:     []request.TestResult{{Name: "Created", Pass: true}},
			Console:   []request.ConsoleMessage{{Phase: "post-response", Kind: "log", Text: "Created project 73"}},
			Variables: []request.ResolvedVariable{{Name: "baseUrl", Value: "https://api.example.com", Source: "environment"}},
		})
		r.At = snapshotTime.Add(time.Duration(i) * time.Minute)
		if err := a.experiments.log.retain(r); err != nil {
			panic(err)
		}
	}
	a.experiments.log.Baseline = 1
	a.experiments.pick(2)
}

// snapshotHistory opens a saved request with two runs sent from it.
func snapshotHistory(a *app) {
	a.requests[0].ID = "list"
	for i, status := range []int{200, 401} {
		body := `{"projects":[{"id":73}],"total":1}`
		if status == 401 {
			body = `{"error":"Unauthorized"}`
		}
		r := snapshotExperiment(i+1, a.requests[0], "Local", request.Execution{
			Response: &capture.Flow{Method: "GET", URL: a.requests[0].URL, Status: status, Millis: 40 + 7*i, Bytes: len(body), ResponseHeader: http.Header{"Content-Type": {"application/json"}}, ResponseBody: body},
		})
		r.At = snapshotTime.Add(time.Duration(i) * time.Minute)
		if err := a.experiments.log.retain(r); err != nil {
			panic(err)
		}
	}
	a.openSaved(a.requests[0].key())
}

func snapshotRunner(a *app) {
	snapshotRuns(a)
	a.page = pageRequest
	a.runner = collectionRunner{open: true, collection: "Workspace", iterations: "2", completed: 2, failures: 1, status: "Finished"}
	a.runner.load(a.requests)
	a.runner.rows = []runnerRow{
		{name: "Create project", iteration: 1, summary: "201 · 43 B · 47 ms · 1/1 tests", recordID: 1, tests: []request.TestResult{{Name: "Created", Pass: true}}},
		{name: "Check project", iteration: 2, summary: "503 · 41 B · 132 ms · 0/1 tests", failed: true, recordID: 2, tests: []request.TestResult{{Name: "Available", Error: "expected 200, received 503"}}},
	}
}

func snapshotSources(a *app) {
	for i, name := range []string{"before.json", "after.json"} {
		file := sessionFrom(a.flows[i:i+1], nil)
		file.Title, file.Created = name, snapshotTime
		a.keepSession(file)
	}
	a.sessions.current().side, a.sessions.current().tab = 1, 1
}

func snapshotExport(a *app) {
	snapshotSources(a)
	data, _ := json.Marshal(a.sessions.current().data)
	a.reviewExport("Export session", "vscroxy-session.json", data, nil)
}

func snapshotAPI(a *app) {
	a.page = pageAPI
	a.addAPI(apiSpec{ID: "snapshot-api", Source: openAPIFixture, Notes: []string{"Review authentication and response schemas before publishing."}}, true)
}
