package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
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
	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
	"github.com/gorilla/websocket"
)

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
		a.composeFrom(a.flows[0])
		a.composeTab = composeTabHeaders
	}
	response := func(a *app) {
		draft(a)
		a.sent, a.sentSet, a.sentAt = a.draft(), true, snapshotTime
		a.response = &capture.Flow{Status: 201, Millis: 47, Bytes: 43, ResponseHeader: http.Header{"Content-Type": {"application/json"}, "X-Tag": {"first", "second"}}, ResponseBody: `{"id":73,"name":"Demo project","active":true}`}
		a.sendOutcome = "Response received"
		a.composeTests = []request.TestResult{{Name: "Created", Pass: true}, {Name: "Project active", Pass: false}}
		a.responseArrived()
	}
	states := []snapshot{
		{name: "capture-empty", setup: func(a *app) { a.flows = nil; a.countDomains(); a.countClients(); a.applyFilter() }, want: "Capture is stopped"},
		{name: "capture-selected", want: "Request inspector"},
		{name: "capture-right", setup: func(a *app) { a.workbench.layout = "right" }, want: "Request inspector"},
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
		{name: "capture-headers", setup: func(a *app) { a.inspect.tab[1] = resTabHeaders }, want: "Content-Type"},
		{name: "capture-raw", setup: func(a *app) { a.inspect.tab[1] = resTabRaw }, want: "Raw message"},
		{name: "capture-timing", setup: func(a *app) { a.inspect.tab[1] = resTabTiming }, want: "Waiting"},
		{name: "requests-empty", setup: func(a *app) { a.workspace = 1 }, want: "Open a saved request or create a new one"},
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
		{name: "requests-response-checks", setup: func(a *app) { response(a); a.responseTab = resViewTests }, want: "Project active"},
		{name: "requests-response-headers", setup: func(a *app) { response(a); a.responseTab = resViewHeaders }, want: "X-Tag"},
		{name: "requests-variables", setup: func(a *app) { draft(a); a.openVars() }, want: "Variable name 1"},
		{name: "requests-environments", setup: func(a *app) { draft(a); a.requestSide = 2 }, want: "Environments"},
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
		{name: "secondary-runs-empty", setup: func(a *app) { a.workspace, a.requestsSection, a.requestSide = 1, 2, 1; a.history = nil }, want: "Send a request to record an experiment."},
		{name: "secondary-runs-result", setup: snapshotRuns, want: "Experiment result"},
		{name: "secondary-runs-compare", setup: func(a *app) { snapshotRuns(a); a.experiments.left, a.experiments.tab = 1, 1 }, want: "Old text"},
		{name: "secondary-runs-variables", setup: func(a *app) { snapshotRuns(a); a.experiments.tab = 5 }, want: "baseUrl"},
		{name: "secondary-runs-modal", setup: func(a *app) { snapshotRuns(a); a.workspace = 0; a.experiments.open = true }, want: "Experiment result"},
		{name: "secondary-sessions-empty", setup: func(a *app) { a.investigations.show() }, want: "Historical sessions & evidence"},
		{name: "secondary-sessions-browse", setup: snapshotSources, want: "Select source 2 exchange 2"},
		{name: "secondary-sessions-selected", setup: snapshotSources, act: func(t *testing.T, tt *ui.Tester) {
			click("Select source 1 exchange 1")(t, tt)
			click("Select source 2 exchange 2")(t, tt)
		}, want: "2 / 64 selected"},
		{name: "secondary-sessions-no-matches", setup: func(a *app) {
			snapshotSources(a)
			a.investigations.query = "no-matches"
			a.investigations.refreshSearch()
		}, want: "0 of 1 exchanges"},
		{name: "secondary-sessions-compare", setup: func(a *app) {
			snapshotSources(a)
			a.investigations.picks = []investigationPick{{0, 0}, {1, 0}}
			a.investigations.comparing = true
		}, want: "Old text"},
		{name: "secondary-api-empty", setup: func(a *app) {
			a.workspace, a.requestsSection, a.requestSide = 1, 1, 3
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
			a.workspace, a.requestsSection, a.requestSide = 1, 1, 3
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
		{name: "dialog-settings-devices", setup: func(a *app) { snapshotSettings(a, 5) }, want: "Settings"},
		{name: "dialog-upstream", setup: func(a *app) { a.upstreamOpen, a.upstreamAddr = true, "http://proxy.example.com:8080" }, want: "Upstream proxy"},
		{name: "dialog-ssl", setup: func(a *app) { a.sslOpen, a.sslHosts = true, "api.example.com\n*.example.org" }, want: "SSL proxying", short: true},
		{name: "dialog-ignore-hosts", setup: func(a *app) { a.ignoreOpen, a.ignoreText = true, "telemetry.example.com\n*.analytics.example.org" }, want: "Ignored hosts"},
		{name: "dialog-ignore-apps", setup: func(a *app) { a.ignoreAppsOpen, a.ignoreAppsText = true, "Updater\nBackground Sync" }, want: "Ignored apps"},
		{name: "dialog-dns", setup: func(a *app) { a.dnsOpen, a.dnsText = true, "api.example.com = 192.168.1.50 @ Browser" }, want: "DNS mapping", short: true},
		{name: "dialog-dns-rule", setup: func(a *app) {
			a.dnsRuleOpen, a.dnsRuleHost, a.dnsRuleAddr = true, "api.example.com", "192.168.1.50"
			a.dnsRuleApps, a.dnsRuleScope = []string{"All apps", "Browser"}, "Browser"
		}, want: "Map a host"},
		{name: "dialog-key-log", setup: func(a *app) { a.openKeyLog() }, want: "TLS key log"},
		{name: "dialog-rules", setup: func(a *app) {
			a.rules = []capture.Rule{{Name: "Development API", Type: capture.RuleMapRemote, Match: "https://api.example.com/*", Target: "http://127.0.0.1:3000"}}
			a.openRules()
		}, want: "Save rule", short: true},
		{name: "dialog-rules-error", setup: func(a *app) { a.openRules(); a.ruleUI.problem = "Enter a valid HTTP(S) target URL" }, want: "Enter a valid HTTP(S) target URL"},
		{name: "dialog-breakpoint", setup: snapshotBreakpoint, want: "Breakpoint · request paused", short: true},
		{name: "dialog-breakpoint-readonly", setup: func(a *app) {
			snapshotBreakpoint(a)
			a.bp.Phase, a.bp.BodyReadOnly, a.bpStatus = capture.PhaseResponse, true, "503"
		}, want: "Breakpoint · response paused", short: true},
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
		{name: "dialog-socket-payload", setup: func(a *app) {
			a.socket.open, a.socket.url = true, "wss://api.example.com/events"
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
			a.deviceUI = deviceSetup{open: true, ips: []string{"192.168.1.10"}, ip: "192.168.1.10", port: "7891", allowed: "192.168.1.20", target: "https://api.example.com/projects", since: snapshotTime}
		}, want: "LAN off", short: true},
		{name: "dialog-client", setup: func(a *app) {
			a.clientUI = clientSetup{open: true, runtime: "cURL", url: "https://api.example.com/projects", since: snapshotTime}
		}, want: "No target traffic observed yet.", short: true},
		{name: "dialog-diagnostics", setup: func(a *app) {
			snapshotSettings(a, 0)
			a.settingsOpen = false
			a.diagnostics = captureDiagnostics{open: true, target: "https://api.example.com/projects"}
		}, want: "Capture diagnostics", short: true},
		{name: "dialog-clear", setup: func(a *app) { a.clearOpen = true }, want: "Clear captured requests?"},
		{name: "dialog-clear-records", setup: func(a *app) { snapshotRuns(a); a.experiments.clearing = true }, want: "Clear all experiment records?"},
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
		{name: "dialog-evidence-review", setup: func(a *app) {
			a.investigations.showReview([]byte(`{"version":1,"title":"Demo raw evidence","notes":"Fake fixture only","sources":[],"exchanges":[]}`))
		}, want: "Confirm full evidence review", short: true},
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
		{name: "notification-warning", toasts: []ui.Toast{{Title: "Response body incomplete", Description: "The captured bytes are still available.", Type: "warning"}}, want: "Response body incomplete"},
		{name: "capture-menu", act: click("More capture options"), menu: "Clear unpinned requests…"},
		{name: "layout-menu", act: click("Inspector layout"), menu: "Inspector: Bottom"},
		{name: "body-menu", act: click("Response body format"), menu: "Pretty"},
		{name: "inspector-menu", act: click("Inspector options"), menu: "Replay…"},
		{name: "request-menu", setup: draft, act: click("Request options"), menu: "Copy as cURL"},
		{name: "traffic-menu", act: func(t *testing.T, tt *ui.Tester) {
			if err := tt.RightClick("api.example.com/projects"); err != nil {
				t.Fatal(err)
			}
		}, menu: "Edit & resend"},
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
			if state.name == "capture-selected" || state.name == "capture-ws" || state.name == "capture-connect" || strings.HasPrefix(state.name, "requests-") || strings.HasPrefix(state.name, "secondary-") || strings.HasPrefix(state.name, "dialog-") || state.name == "active-tools" {
				widths = append(widths, 960)
			}
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
					t.Run(theme+"/"+name, func(t *testing.T) {
						a := snapshotApp()
						a.library.root = filepath.Join(t.TempDir(), "collections")
						a.dark = dark
						t.Cleanup(a.protobuf.stop)
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
						for a.protobuf.answers != nil {
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
	openResponseBody(a)
	return a
}

func snapshotRuns(a *app) {
	a.workspace, a.requestsSection, a.requestSide = 1, 2, 1
	a.history = nil // The legacy Recent requests section is outside phase five.
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

func snapshotRunner(a *app) {
	snapshotRuns(a)
	a.requestsSection, a.requestSide = 0, 0
	a.runner = collectionRunner{open: true, collection: "Workspace", iterations: "2", completed: 2, failures: 1, status: "Finished"}
	a.runner.load(a.requests)
	a.runner.rows = []runnerRow{
		{name: "Create project", iteration: 1, summary: "201 · 43 B · 47 ms · 1/1 tests", recordID: 1, tests: []request.TestResult{{Name: "Created", Pass: true}}},
		{name: "Check project", iteration: 2, summary: "503 · 41 B · 132 ms · 0/1 tests", failed: true, recordID: 2, tests: []request.TestResult{{Name: "Available", Error: "expected 200, received 503"}}},
	}
}

func snapshotSources(a *app) {
	s := &a.investigations
	s.show()
	s.sources = []EvidenceSource{
		{Label: "before.json", Notes: "Previous release", Session: sessionFrom(a.flows[:1], nil)},
		{Label: "after.json", Notes: "Current release", Session: sessionFrom(a.flows[1:], nil)},
	}
	s.panes = [2]int{0, 1}
	s.refreshSearch()
}

func snapshotAPI(a *app) {
	a.workspace, a.requestsSection, a.requestSide = 1, 1, 3
	a.addAPI(apiSpec{ID: "snapshot-api", Source: openAPIFixture, Notes: []string{"Review authentication and response schemas before publishing."}}, true)
}
