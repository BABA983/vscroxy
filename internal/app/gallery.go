package app

// The design gallery's window into the app: each screen is the real view,
// built from fake data, with no listener, settings file or request sent.
// internal/gallery names the screens and draws them; nothing here changes
// how a screen is built.

import (
	"context"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/hosts"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/rules"
	"github.com/baba983/vscroxy/internal/system"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"google.golang.org/grpc/metadata"
)

// galleryTime is the clock every screen reads: a fixed afternoon, so two
// exports of the same screen are the same picture.
var galleryTime = time.Date(2026, 11, 12, 15, 4, 5, 0, time.UTC)

// Gallery is one app screen built from fake data. Its View is the app's own
// view function, so what the gallery shows is what the app draws.
type Gallery struct {
	a        *app
	settings bool
	// tmp holds fixtures that need a file, such as a .proto schema; Close
	// removes it.
	tmp string
}

// NewGallery builds screen, one of the ids internal/gallery lists, or nil for
// an unknown one. The caller closes it when the screen is done.
func NewGallery(screen string) *Gallery {
	g := &Gallery{a: galleryApp()}
	if !g.setup(screen) {
		g.Close()
		return nil
	}
	return g
}

// View draws the screen. Settings screens are the Settings window's content,
// as its own window draws it; every other screen is the main window.
func (g *Gallery) View(c *ui.Context) {
	if g.settings {
		g.a.settingsView(c)
		return
	}
	g.a.view(c)
}

// SetDark chooses the appearance the screen draws in.
func (g *Gallery) SetDark(dark bool) {
	g.a.appearance = "light"
	if dark {
		g.a.appearance = "dark"
	}
}

// Close stops what the screen's documents started. It is safe to call twice.
func (g *Gallery) Close() {
	a := g.a
	a.columnRead.stop()
	a.protobuf.stop()
	a.socket.protobuf.stop()
	a.jsonQuery.job.stop()
	a.structured[0].stop()
	a.structured[1].stop()
	a.views.stopSearch()
	a.messageUI.stopSearch()
	a.stopDocuments()
	a.socket.session.disconnect()
	a.sse.session.stop()
	if g.tmp != "" {
		_ = os.RemoveAll(g.tmp)
		g.tmp = ""
	}
}

// galleryApp is the app a gallery screen draws: the same state a snapshot
// builds, with no capture server and no settings on disk.
func galleryApp() *app {
	a := &app{
		filter: "all", sidebarWidth: 220, tablePercent: 56,
		collectionOpen: make(map[string]*bool), icons: make(map[string]ui.ImageSource),
		detailFor: -1, frames: make(map[int][]frame), workbench: desktopWorkbench{layout: "right"},
		sort: ui.SortOrder{Column: "id"},
		envs: []environment{
			{Name: "Local", Vars: map[string]string{"baseUrl": "https://api.example.com"}},
			{Name: "Staging", Vars: map[string]string{"baseUrl": "https://staging.example.com"}},
		},
		activeEnv: "Local",
		flows: []flow{
			{id: 1, method: "GET", scheme: "https", host: "api.example.com", path: "/v1/projects", url: "https://api.example.com/v1/projects",
				status: 200, bytes: 83, millis: 47, wait: 32, download: 15, process: "Safari",
				reqHeader: http.Header{"Accept": {"application/json"}}, respHeader: http.Header{"Content-Type": {"application/json"}},
				reqBody: "", resBody: `{"projects":[{"id":73,"name":"Demo project","active":true}],"total":1}`},
			{id: 2, method: "POST", scheme: "https", host: "api.example.com", path: "/v1/login", url: "https://api.example.com/v1/login",
				status: 401, bytes: 41, millis: 132, process: "Safari",
				reqHeader: http.Header{"Content-Type": {"application/json"}}, respHeader: http.Header{"Content-Type": {"application/json"}},
				reqBody: `{"email":"demo@example.com"}`, resBody: `{"error":"Unauthorized","code":"AUTH_401"}`},
			{id: 3, method: "GET", scheme: "https", host: "cdn.example.com", path: "/assets/app.css", url: "https://cdn.example.com/assets/app.css",
				status: 304, bytes: 0, millis: 8, process: "Safari",
				reqHeader: http.Header{"Accept": {"text/css"}}, respHeader: http.Header{"Content-Type": {"text/css"}}},
			{id: 4, method: "PATCH", scheme: "https", host: "api.example.com", path: "/v1/projects/73", url: "https://api.example.com/v1/projects/73",
				status: 200, bytes: 118, millis: 1242, process: "curl",
				reqHeader: http.Header{"Content-Type": {"application/json"}}, respHeader: http.Header{"Content-Type": {"application/json"}},
				reqBody: `{"name":"Demo project","active":false}`, resBody: `{"id":73,"name":"Demo project","active":false}`},
			{id: 5, method: "GET", scheme: "https", host: "img.example.com", path: "/hero@2x.png", url: "https://img.example.com/hero@2x.png",
				status: 200, bytes: 48213, millis: 212, process: "Safari",
				respHeader: http.Header{"Content-Type": {"image/png"}}},
			{id: 6, method: "DELETE", scheme: "https", host: "api.example.com", path: "/v1/projects/12", url: "https://api.example.com/v1/projects/12",
				status: 500, bytes: 96, millis: 892, process: "curl",
				respHeader: http.Header{"Content-Type": {"application/json"}},
				resBody:    `{"error":"Internal Server Error","request_id":"req_73","retryable":true}`},
		},
		requests: []savedRequest{
			{Request: request.Request{Name: "List projects", Collection: "Workspace/API", Method: "GET", URL: "https://api.example.com/v1/projects",
				Headers: "Accept: application/json", Pre: `pm.request.headers.upsert("Accept", "application/json")`}},
			{Request: request.Request{Name: "Create project", Collection: "Workspace/API", Method: "POST", URL: "https://api.example.com/v1/projects",
				Headers: "Content-Type: application/json", Body: `{"name":"{{project_name}}"}`}},
			{Request: request.Request{Name: "Telemetry", Collection: "Workspace/Brokers", Protocol: "MQTT", URL: "mqtts://broker.example.com:8883"}},
			{Request: request.Request{Name: "Fetch catalog", Collection: "Workspace/Brokers", Protocol: "gRPC", URL: "https://api.example.com:443"}},
		},
	}
	for i := range a.flows {
		a.flows[i].at = galleryTime.Add(time.Duration(i) * 2 * time.Second)
	}
	a.pickedID = 1
	a.chosen.Add(1)
	a.recount()
	a.applyFilter()
	return a
}

// setup puts the fake data a screen needs in place, and reports whether the
// screen is one this app can build.
func (g *Gallery) setup(screen string) bool {
	a := g.a
	switch screen {
	case "capture-empty":
		a.flows, a.visible = nil, nil
		a.recount()
		a.applyFilter()
		a.inspect.ready = true
		a.inspect.tab[1] = resViewBody
	case "capture-traffic":
		a.inspect.ready = true
		a.inspect.tab[1] = resViewBody
	case "capture-error":
		a.flows[0].status, a.flows[0].millis, a.flows[0].bytes = 500, 1242, 1843
		a.flows[0].resBody = `{"error":"Internal Server Error","request_id":"req_73","retryable":true}`
		a.recount()
		a.applyFilter()
		a.inspect.ready = true
		a.inspect.tab[1] = resViewBody
	case "capture-tunnel":
		a.flows[0].kind, a.flows[0].method, a.flows[0].path, a.flows[0].url = "connect", "CONNECT", "", "api.example.com:443"
		a.flows[0].status, a.flows[0].bytes, a.flows[0].millis = 0, 0, 0
		a.recount()
		a.applyFilter()
		a.inspect.ready = true
	case "capture-activity":
		galleryActivity(a)
	case "editor-http-empty":
		a.addDocument(savedRequest{Request: request.Request{Name: "Untitled request", Method: "GET", URL: "https://api.example.com/v1/orders"}}, "")
	case "editor-http-response":
		a.addDocument(savedRequest{Request: request.Request{
			Name: "Create project", Collection: "Workspace/API", Method: "POST", URL: "https://api.example.com/v1/projects",
			Headers: "Content-Type: application/json", Body: `{"name":"Demo project"}`,
		}}, "")
		d := a.requestDocument
		d.response = &capture.Flow{Status: 201, Millis: 47, Bytes: 43,
			ResponseHeader: http.Header{"Content-Type": {"application/json"}, "X-Tag": {"first", "second"}},
			ResponseBody:   `{"id":73,"name":"Demo project","active":true}`}
		d.sendOutcome = "Response received"
		d.composeTests = []request.TestResult{{Name: "Created", Pass: true}, {Name: "Project active", Pass: false}}
		d.responseArrived()
		a.inspect.ready = true
		a.inspect.tab[1] = resViewBody
	case "editor-mqtt":
		galleryMQTT(a)
	case "editor-grpc":
		galleryGRPC(a)
	case "editor-websocket":
		galleryWebSocket(a)
	case "settings-general":
		g.settingsScreen(0)
	case "settings-proxy":
		g.settingsScreen(1)
	case "settings-https":
		g.settingsScreen(2)
	case "settings-appearance":
		g.settingsScreen(3)
	case "settings-capture":
		g.settingsScreen(4)
	case "settings-devices":
		g.settingsScreen(5)
	case "settings-certificates":
		g.settingsScreen(6)
	case "command-palette":
		a.workbench.openPalette()
	case "sidebar-collapsed":
		a.workbench.sidebarHidden = true
	case "tools-rules":
		galleryRules(a)
		a.srv = &capture.Server{}
		a.openRules()
	case "tools-hosts":
		galleryProfiles(a)
		a.openTools("hosts")
	case "tools-scripts":
		galleryScripts(a)
		a.rules = []capture.Rule{{Name: "Debug headers", Type: capture.RuleScript, Match: "api.example.com/v1/", Target: "Debug headers.js"}}
		a.openScript("Debug headers")
	case "tools-mocks":
		galleryRules(a)
		a.openTools("mocks")
	case "tools-scenarios":
		galleryRules(a)
		a.openTools("scenarios")
	case "client-setup":
		galleryClientSetup(a)
	case "diagnostics":
		galleryDiagnostics(a)
	case "https-rules":
		galleryHTTPSRules(a)
	case "api-endpoints":
		galleryAPI(a)
	case "runs-records":
		galleryRuns(a)
	case "runs-running":
		galleryRunner(a, true)
	case "runs-results":
		galleryRunner(a, false)
	case "sessions":
		gallerySessions(a)
	case "collection-wizard":
		galleryWizard(a)
	case "postman-import":
		galleryPostmanImport(a)
	case "variables-environment":
		galleryVariables(a)
	case "certificates-editor":
		galleryCertificates(g)
	case "protobuf-inspector":
		if !g.galleryProtobuf(a) {
			return false
		}
	case "sse-session":
		gallerySSE(a)
	case "analysis-board":
		galleryAnalysis(a)
	case "setup-guide":
		galleryGuide(a)
	case "mcp-catalog":
		galleryMCP(a)
	case "editor-socketio":
		gallerySocketIO(a)
	default:
		return false
	}
	return true
}

func (g *Gallery) settingsScreen(tab int) {
	g.settings = true
	g.a.settingsOpen = true
	g.a.settingsTab = tab
	g.a.settingsChoice = string(rune('0' + tab))
}

// galleryActivity fills the five minutes before galleryTime with traffic, then
// brushes the busy half minute, so the strip shows both a range and the rows
// it keeps.
func galleryActivity(a *app) {
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
			f.at = galleryTime.Add(-5*time.Minute + time.Duration(bucket)*5*time.Second + time.Duration(j)*time.Millisecond)
			f.path = "/v1/projects/" + strconv.Itoa(f.id)
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
	a.activity = activityRange{
		end:    galleryTime,
		from:   galleryTime.Add(-activitySpan + 22*activityStep),
		to:     galleryTime.Add(-activitySpan + 29*activityStep),
		frozen: true,
	}
	a.applyFilter()
	a.inspect.ready = true
	a.inspect.tab[1] = resViewBody
}

// galleryMQTT is the MQTT editor with a short session: a connected client,
// two subscriptions and a retained reading.
func galleryMQTT(a *app) {
	a.addDocument(savedRequest{Request: request.Request{
		Name: "Telemetry", Collection: "Workspace/Brokers", Protocol: "MQTT", URL: "mqtts://broker.example.com:8883",
		MQTT: request.MQTTConfig{
			Version: 5, ClientID: "vscroxy-demo", KeepAlive: 30, ConnectTimeoutMs: 8000,
			Properties: request.MQTTConnectProperties{SessionExpiry: 120, ReceiveMaximum: 20, MaximumPacketSize: 1 << 20,
				UserProperties: []request.MQTTProperty{{Key: "app", Value: "vscroxy"}}},
			Topics: []request.MQTTTopic{
				{Name: "sensors/+/temperature", QoS: 1, NoLocal: true, SubscriptionIdentifier: 7, UserProperties: []request.MQTTProperty{{Key: "team", Value: "telemetry"}}},
				{Name: "commands/#", QoS: 0},
			},
			Messages: []request.MQTTMessage{{Name: "Calibrate", Topic: "commands/device-7", Payload: `{"action":"calibrate"}`, Type: "json", QoS: 1,
				Properties: request.MQTTMessageProperties{ContentType: "application/json", ResponseTopic: "commands/device-7/reply", MessageExpiry: 30}}},
		},
	}}, "")
	d := a.requestDocument
	d.mqttState().session = &mqttSession{
		state: "Connected", clientID: "vscroxy-demo", seq: 5,
		subscriptions: map[string]request.MQTTSubscription{
			"sensors/+/temperature": {Topic: "sensors/+/temperature", QoS: 1},
			"commands/#":            {Topic: "commands/#", QoS: 0},
		},
		events: []mqttEvent{
			{seq: 1, at: galleryTime, direction: "System", name: "Connected", note: "MQTT 5.0 · client vscroxy-demo · clean start"},
			{seq: 2, at: galleryTime.Add(time.Second), direction: "System", name: "Subscribed", note: "sensors/+/temperature · QoS 1\ncommands/# · QoS 0"},
			{seq: 3, at: galleryTime.Add(2 * time.Second), direction: "In", name: "sensors/kitchen/temperature", qos: 1,
				properties: "Content type: application/json\nUser property: site = kitchen",
				payload:    []byte(`{"celsius":21.4,"at":"2026-11-12T15:04:07Z"}`), size: 39},
			{seq: 4, at: galleryTime.Add(3 * time.Second), direction: "In", name: "sensors/hall/temperature", qos: 1, retain: true,
				properties: "Content type: application/json", payload: []byte(`{"celsius":19.1}`), size: 16},
			{seq: 5, at: galleryTime.Add(4 * time.Second), direction: "Out", name: "commands/device-7", note: "Calibrate", qos: 1,
				properties: "Content type: application/json\nResponse topic: commands/device-7/reply",
				payload:    []byte(`{"action":"calibrate"}`), size: 21},
		},
	}
	d.mqttUI.tab = 1
	d.mqttUI.selected = 2
	d.mqttUI.list.Selected = &d.mqttUI.selected
	a.composeWaiting, a.sendCancel = true, func() {}
}

// galleryGRPC is the gRPC editor with a finished call: a request, two
// responses and the trailers.
func galleryGRPC(a *app) {
	a.addDocument(savedRequest{Request: request.Request{
		Name: "Fetch catalog", Collection: "Workspace/Brokers", Protocol: "gRPC", URL: "https://api.example.com:443",
		Body: `{"query":"headphones"}`, GRPC: request.GRPCConfig{Method: "/api.Catalog/Fetch"},
	}}, "")
	d := a.requestDocument
	d.grpcState().session = &grpcSession{state: "gRPC OK", finished: true, seq: 3, cancel: func() {},
		header:  metadata.Pairs("content-type", "application/grpc", "x-trace", "catalog-73"),
		trailer: metadata.Pairs("x-result", "complete"),
		events: []grpcEvent{
			{seq: 1, at: galleryTime, direction: "Out", text: `{"query":"headphones"}`, size: 22},
			{seq: 2, at: galleryTime.Add(time.Second), direction: "In", text: "{\n  \"title\": \"Studio headphones\",\n  \"delta\": -7,\n  \"counts\": [3, 9],\n  \"state\": \"ACTIVE\"\n}", size: 101},
			{seq: 3, at: galleryTime.Add(2 * time.Second), direction: "In", text: `{"title":"Travel headphones","delta":3}`, size: 39},
		}}
	d.grpcState().selected = 1
	a.composeWaiting, a.sendCancel = true, func() {}
}

// galleryWebSocket is the WebSocket editor, open over the capture screen with
// a short session selected.
func galleryWebSocket(a *app) {
	a.socket.open = true
	a.socket.url = "wss://api.example.com/socket"
	a.socket.headers = "Authorization: Bearer demo"
	a.socket.message = `{"subscribe":"prices"}`
	a.socket.encoding = "Text"
	a.socket.direction = "All"
	a.socket.session = &wsSession{state: "Connected", sequence: 4, cancel: func() {}, events: []wsSessionEvent{
		{Sequence: 1, At: galleryTime, Detail: "Connected · wss://api.example.com/socket"},
		{Sequence: 2, At: galleryTime.Add(time.Second), Direction: "Out", Data: []byte(`{"subscribe":"prices"}`), Size: 22},
		{Sequence: 3, At: galleryTime.Add(2 * time.Second), Direction: "In", Data: []byte(`{"symbol":"GO","price":73}`), Size: 26},
		{Sequence: 4, At: galleryTime.Add(3 * time.Second), Direction: "In", Data: []byte(`{"symbol":"GO","price":74}`), Size: 26},
	}}
	a.socket.picked = 3
	a.socket.reader = &editor.State{Buffer: editor.NewBuffer(`{"symbol":"GO","price":73}`), ReadOnly: true, NoScrollPastEnd: true, Label: "Session payload"}
}

// galleryRules is the rule list the Tools plates share: one of each action, a
// scenario with two rules, header and app conditions, and a rule that hides
// its traffic.
func galleryRules(a *app) {
	a.rules = []capture.Rule{
		{Name: "Mock user profile", Type: capture.RuleMapLocal, Match: "api.example.com/v1/users/", Method: "GET",
			Target: "/home/demo/.vscroxy/mocks/user.json", Status: 200, Scenario: "Offline demo", ScenarioActive: true},
		{Name: "Orders → staging", Type: capture.RuleMapRemote, MatchMode: "wildcard", Match: "*.example.com/v1/orders/*",
			Target: "https://staging.example.com:8443", PathFrom: "/v1/", PathTo: "/api/v1/", KeepHost: true,
			Apps: []string{"Google Chrome*"},
			When: []rules.HeaderCondition{{Name: "X-Env", Op: rules.HeaderEquals, Value: "staging"}, {Name: "Authorization", Op: rules.HeaderPresent}}},
		{Name: "Hide analytics", Type: capture.RuleBlock, Block: capture.BlockHide, Match: "analytics.example.com", Apps: []string{"Google Chrome*"}},
		{Name: "Pause user requests", Type: capture.RuleBreak, Match: "/v1/users"},
		{Name: "Inspect payments", Type: capture.RuleBreakResponse, Match: "/v1/payments", Method: "POST"},
		{Name: "Offline users", Type: capture.RuleBlock, Match: "api.example.com/v1/users", Scenario: "Offline demo", ScenarioActive: true},
	}
}

// galleryProfiles is the Host profiles of the Tools plate: two on, a
// subscription and a line that maps nothing.
func galleryProfiles(a *app) {
	a.profiles.set = hosts.Set{Profiles: []hosts.Profile{
		{Name: "Staging", On: true, Text: "# Staging API\n10.0.3.21   api.example.com auth.example.com\n10.0.3.22   cdn.example.com\nfd00::12    ws.example.com\n*.internal.test = 10.0.9.1\n10.0.3.30   pay.example.com   @ Browser\n10.0.3.4O   img.example.com\n"},
		{Name: "Default", On: true, Text: "10.1.0.8 cdn.example.com\n10.1.0.9 auth.example.com @ curl\n10.1.0.10 static.example.com\n"},
		{Name: "Local", Text: "127.0.0.1 api.example.com cdn.example.com auth.example.com pay.example.com\n"},
		{Name: "Pre-release", Text: "10.2.0.1 api.example.com\n"},
		{Name: "Team hosts", URL: "https://hosts.example.com/team.hosts", Updated: galleryTime.Add(-2 * time.Hour), Text: "10.9.0.1 team.example.com\n"},
	}}
	a.profiles.rev++
}

// galleryScripts is the script store the Scripts plate shows: one working
// script, one that does not parse, and a console with an error.
func galleryScripts(a *app) {
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
		{at: galleryTime, script: "Debug headers", rule: "Debug headers", phase: "response", kind: "log", text: "200 48 ms"},
		{at: galleryTime.Add(2 * time.Second), script: "Debug headers", rule: "Debug headers", phase: "response", kind: "log", text: "503 112 ms"},
		{at: galleryTime.Add(3 * time.Second), script: "Debug headers", rule: "Debug headers", phase: "request", kind: "error", text: "TypeError: Cannot read property 'id' of undefined at onRequest (Debug headers.js:5:58(9))"},
	}
}

// galleryClientSetup is the one-click panel for a local terminal. Capture is
// stopped, as the gallery's pill says, so the launch action is disabled.
func galleryClientSetup(a *app) {
	// No settings path: the plate must not write a settings file, so the CA
	// bundle line says it is unavailable, as on a fresh install.
	a.caPath = "/home/demo/.vscroxy/mitmproxy-ca-cert.pem"
	a.clientUI = clientSetup{open: true, tab: 0, target: clientTargetTerminal,
		addr: "127.0.0.1:7890", caPath: a.caPath}
}

// galleryDiagnostics is the check page after a launch: the OS proxy, the
// certificate and the decryption rule pass, while the listener is stopped.
// Nothing here reads this machine or starts a listener.
func galleryDiagnostics(a *app) {
	a.flows[0].live = true
	a.flows[0].reqHeaders = "Accept: application/json"
	a.sslHosts = "api.example.com\ncdn.example.com"
	a.sslApps = []capture.SSLApp{{Name: "Safari", Path: "/Applications/Safari.app/Contents/MacOS/Safari"}}
	a.systemProxy.read = func(string) system.ProxyCheck { return system.ProxyCheck{Status: system.ProxyOff} }
	a.systemProxy.checked = true
	a.systemProxy.check = system.ProxyCheck{Target: "127.0.0.1:7890", Status: system.ProxyMatches,
		Configs: []system.ProxyConfig{{Scope: "Wi-Fi", Mode: system.ProxyModeManual,
			HTTP:  system.ProxyEndpoint{Enabled: true, Host: "127.0.0.1", Port: 7890},
			HTTPS: system.ProxyEndpoint{Enabled: true, Host: "127.0.0.1", Port: 7890}}}}
	a.certificate.checked = true
	a.certificate.certificateResult = certificateResult{cert: &x509.Certificate{}}
	a.diagnostics.target = "https://api.example.com/projects"
	a.diagnostics.client = diagnoseMac
	a.page, a.sidebarChoice = pageDiagnostics, "diagnostics"
}

// galleryHTTPSRules is the decryption editor: three domains and two app
// rules, with the list itself in the draft.
func galleryHTTPSRules(a *app) {
	cfg := capture.SSLConfig{Hosts: []string{"api.example.com", "*.internal.test", "cdn.example.com"},
		Apps: []capture.SSLApp{
			{Name: "Safari", Path: "/Applications/Safari.app/Contents/MacOS/Safari"},
			{Name: "curl", Path: "/usr/bin/curl"},
		}}
	a.sslDraft = &cfg
	a.sslDraftHosts = strings.Join(cfg.Hosts, "\n")
	a.sslOpen = true
}

// galleryOpenAPI is a contract with enough paths to fill the endpoint list
// and one operation with parameters and a request body.
const galleryOpenAPI = `{
  "openapi": "3.0.3",
  "info": {"title": "Workspace API", "version": "1.4.0"},
  "servers": [{"url": "https://api.example.com/v1"}],
  "tags": [{"name": "projects"}, {"name": "auth"}],
  "paths": {
    "/projects": {
      "get": {
        "tags": ["projects"], "summary": "List projects", "operationId": "listProjects",
        "parameters": [
          {"name": "page", "in": "query", "schema": {"type": "integer"}},
          {"name": "status", "in": "query", "schema": {"type": "string", "enum": ["active", "archived"]}}
        ],
        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"type": "object", "properties": {"projects": {"type": "array", "items": {"$ref": "#/components/schemas/Project"}}, "total": {"type": "integer"}}}}}}}
      },
      "post": {
        "tags": ["projects"], "summary": "Create project", "operationId": "createProject",
        "requestBody": {"required": true, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Project"}}}},
        "responses": {"201": {"description": "created"}}
      }
    },
    "/projects/{id}": {
      "get": {
        "tags": ["projects"], "summary": "Get project",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "ok"}}
      },
      "delete": {
        "tags": ["projects"], "summary": "Delete project",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"204": {"description": "deleted"}}
      }
    },
    "/auth/token": {
      "post": {"tags": ["auth"], "summary": "Exchange credentials", "responses": {"200": {"description": "ok"}}}
    }
  },
  "components": {"schemas": {"Project": {"type": "object", "required": ["name"], "properties": {"id": {"type": "integer"}, "name": {"type": "string"}, "active": {"type": "boolean"}}}}}
}
`

// galleryAPI is the contract page on its endpoints tab.
func galleryAPI(a *app) {
	a.page, a.sidebarChoice = pageAPI, "api"
	a.addAPI(apiSpec{ID: "gallery-api", Source: galleryOpenAPI,
		Notes: []string{"Review authentication and response schemas before publishing."}}, true)
}

// galleryRuns is the retained run records page: a baseline and two attempts,
// the newer one failing its second check.
func galleryRuns(a *app) {
	a.page = pageRuns
	for i, status := range []int{201, 422} {
		body := `{"id":73,"name":"Demo project","active":true}`
		tests := []request.TestResult{{Name: "Created", Pass: true}}
		if status != 201 {
			body = `{"error":"Unprocessable Entity","field":"name"}`
			tests = []request.TestResult{{Name: "Created", Pass: false}, {Name: "Project active", Pass: false}}
		}
		draft := savedRequest{Request: request.Request{Name: "Create project", Method: "POST", URL: "https://api.example.com/projects", Body: `{"name":"Demo project"}`}}
		r := snapshotExperiment(i+1, draft, "Local", request.Execution{
			Response: &capture.Flow{Method: "POST", URL: draft.URL, Status: status, Millis: 47 + 90*i, Bytes: len(body),
				ResponseHeader: http.Header{"Content-Type": {"application/json"}}, ResponseBody: body},
			Tests:     tests,
			Console:   []request.ConsoleMessage{{Phase: "post-response", Kind: "log", Text: "Created project 73"}},
			Variables: []request.ResolvedVariable{{Name: "baseUrl", Value: "https://api.example.com", Source: "environment"}},
		})
		r.At = galleryTime.Add(time.Duration(i) * time.Minute)
		if err := a.experiments.log.retain(r); err != nil {
			panic(err)
		}
	}
	a.experiments.log.Baseline = 1
	a.experiments.pick(2)
}

// galleryRunner is the collection runner, either mid-run or finished with a
// failed check.
func galleryRunner(a *app, running bool) {
	r := &a.runner
	*r = collectionRunner{open: true, collection: "Workspace", iterations: "3", cancel: func() {}}
	r.load(a.requests)
	r.picked = 1
	if running {
		r.running, r.status, r.completed = true, "Running…", 1
		r.rows = []runnerRow{{name: "Create project", iteration: 1, summary: "201 · 43 B · 47 ms · 1/1 tests", recordID: 1,
			tests: []request.TestResult{{Name: "Created", Pass: true}}}}
		return
	}
	r.status, r.completed, r.failures = "Finished", 2, 1
	r.rows = []runnerRow{
		{name: "Create project", iteration: 1, summary: "201 · 43 B · 47 ms · 1/1 tests", recordID: 1,
			tests: []request.TestResult{{Name: "Created", Pass: true}}},
		{name: "Check project", iteration: 2, summary: "503 · 41 B · 132 ms · 0/1 tests", failed: true, recordID: 2,
			tests: []request.TestResult{{Name: "Available", Error: "expected 200, received 503"}}},
	}
}

// gallerySessions is the Sessions page with two saved files: the newer one
// selected, its exchange pinned and its response body open.
func gallerySessions(a *app) {
	a.page, a.sidebarChoice = pageSession, "sessions"
	for i, name := range []string{"before.json", "after.json"} {
		file := sessionFrom(a.flows[i:i+1], nil)
		file.Title, file.Created = name, galleryTime.Add(time.Duration(i)*time.Minute)
		a.keepSession(file)
	}
	item := a.sessions.current()
	item.data.Flows[0].Pinned = true
	item.data.Flows[0].Note = "Compare with before.json"
	item.data.Notes = "Cache behaviour after the CDN change"
	item.side, item.tab = 1, 1
}

// galleryWizard is the capture-to-collection wizard on its Endpoints step,
// with the captured hosts and a preview of the chosen endpoint.
func galleryWizard(a *app) {
	a.openCollectionWizard(a.flows)
	w := &a.collectionUI
	w.step = 1
	w.name = "Captured API"
}

// galleryPostmanImport is the review a Postman import shows when something
// has no equivalent here. The file picker is native and cannot be drawn.
func galleryPostmanImport(a *app) {
	a.importReport = importReport{
		open:  true,
		title: "Imported “Workspace API” · 14 requests",
		intro: "Everything else was imported. These parts have no equivalent here yet:",
		lines: []string{
			"Auth “OAuth 2.0 (client credentials)” was imported as a bearer token placeholder; set the token in the environment.",
			"Request “Upload avatar” uses a disabled form-data file field; the file path was not imported.",
			"Script “Refresh token” calls pm.sendRequest with a callback; it runs as a promise-only call.",
			"Collection variable “baseUrl” is now an environment variable so requests can share it.",
			"Two saved responses on “List projects” were imported as Examples.",
		},
	}
}

// galleryVariables is the environment editor with initial, current and
// credential rows. The current token is local and never exported.
func galleryVariables(a *app) {
	a.envs = []environment{
		{Name: "Local", Vars: map[string]string{
			"baseUrl": "https://api.example.com", "projectId": "73", "region": "eu-west", "pageSize": "25", "apiToken": "replace-me",
		}, Current: map[string]string{"projectId": "81", "apiToken": "local-token"},
			Credentials: map[string]bool{"apiToken": true}},
		{Name: "Staging", Vars: map[string]string{"baseUrl": "https://staging.example.com", "projectId": "12"}},
	}
	a.activeEnv = "Local"
	a.openVars()
}

// galleryCertificates is Settings → Certificates with a saved passphrase and
// an unsaved edit, which the editor's Save and Discard actions answer.
func galleryCertificates(g *Gallery) {
	g.settingsScreen(6)
	g.a.certificates = certs.Settings{
		Clients: []certs.Client{
			{ID: "pem", Host: "api.example.com", Format: certs.FormatPEM, CertFile: "/home/demo/certs/api.crt", KeyFile: "/home/demo/certs/api.key"},
			{ID: "mtls", Host: "*.internal.test:8443", Format: certs.FormatPKCS12, CertFile: "/home/demo/certs/internal.p12"},
		},
		RootCAs: []string{"/home/demo/certs/company-root.pem", "/home/demo/certs/lab-root.pem"},
		Secrets: map[string]string{"mtls": "gallery-passphrase"},
	}
	g.a.certificateUI.loaded, g.a.certificateUI.dirty = false, true
}

// galleryProtobuf writes a schema beside a decoded gRPC response, so the
// inspector shows named fields and two messages. The schema is a temporary
// file, removed by Close; nothing is read from the user's settings.
func (g *Gallery) galleryProtobuf(a *app) bool {
	dir, err := os.MkdirTemp("", "vscroxy-gallery-proto-")
	if err != nil {
		return false
	}
	g.tmp = dir
	for name, source := range map[string]string{
		"catalog.proto": `syntax = "proto3"; package api;
import "common.proto";
message Input { string query = 1; }
message Output {
  string title = 1;
  sint32 delta = 2;
  repeated int32 counts = 3;
  common.State state = 4;
  map<string, string> attrs = 5;
  oneof choice { string label = 6; int64 id = 7; }
  bytes raw = 8;
}
service Catalog { rpc Fetch(Input) returns (stream Output); }`,
		"common.proto": `syntax = "proto3"; package common; enum State { UNKNOWN = 0; ACTIVE = 1; }`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			return false
		}
	}
	schema, _, err := importProtoSchema(context.Background(), filepath.Join(dir, "catalog.proto"), nil)
	if err != nil {
		return false
	}
	first := galleryProtoOutput("Studio headphones", -7, []uint64{3, 9}, "color", "graphite", "studio", []byte{0, 255, 42}, 1763000000)
	second := galleryProtoOutput("Travel headphones", 3, []uint64{1}, "color", "sand", "travel", []byte{1, 2, 3}, 1763000060)
	f := a.flows[0]
	f.method, f.path, f.url = "POST", "/api.Catalog/Fetch", "https://api.example.com/api.Catalog/Fetch"
	f.status, f.millis = 200, 63
	f.reqHeader = http.Header{"Content-Type": {"application/grpc"}}
	f.reqBody = string(galleryProtoFrame(galleryProtoInput("headphones")))
	f.respHeader = http.Header{"Content-Type": {"application/grpc"}, "X-Trace": {"catalog-73"}}
	f.resTrailer = http.Header{"Grpc-Status": {"0"}}
	f.resBody = string(append(galleryProtoFrame(first), galleryProtoFrame(second)...))
	f.bytes = len(f.resBody)
	f.at = galleryTime
	a.flows[0] = f
	a.pickedID = 1
	a.recount()
	a.applyFilter()
	a.inspect.ready = true
	a.inspect.tab[1] = resViewBody
	a.detailFor = f.id
	a.body, a.bodyNotice[0] = f.bodyPreview(true)
	a.reqBody, a.bodyNotice[1] = f.bodyPreview(false)
	a.protoFor[0], a.bodyMode[0], a.protoOK[0] = fmt.Sprintf("%d:0", f.id), 5, true
	a.protobuf.schema, a.protobuf.name = schema, "api.Output"
	a.protobuf.result = inspectProto(context.Background(), f, true, schema, "api.Output")
	a.protobuf.key = fmt.Sprintf("%d:%t:%s:%p", f.id, true, "api.Output", schema)
	a.protobuf.selected, a.protobuf.split = 0, 200
	return true
}

// galleryProtoInput is one api.Input message.
func galleryProtoInput(query string) []byte {
	return galleryProtoBytes(1, []byte(query))
}

// galleryProtoOutput is one api.Output message with every field the schema
// names, so the inspector shows more than a title.
func galleryProtoOutput(title string, delta int64, counts []uint64, key, value, label string, raw []byte, seconds uint64) []byte {
	out := galleryProtoBytes(1, []byte(title))
	zigzag := uint64(delta<<1) ^ uint64(delta>>63)
	out = append(out, galleryProtoVarint(2, zigzag)...)
	var packed []byte
	for _, n := range counts {
		packed = binary.AppendUvarint(packed, n)
	}
	out = append(out, galleryProtoBytes(3, packed)...)
	out = append(out, galleryProtoVarint(4, 1)...)
	entry := append(galleryProtoBytes(1, []byte(key)), galleryProtoBytes(2, []byte(value))...)
	out = append(out, galleryProtoBytes(5, entry)...)
	out = append(out, galleryProtoBytes(6, []byte(label))...)
	out = append(out, galleryProtoBytes(8, raw)...)
	timestamp := galleryProtoVarint(1, seconds)
	out = append(out, galleryProtoBytes(9, timestamp)...)
	return out
}

// galleryProtoFrame is a gRPC frame: the uncompressed flag, a big-endian
// length and the message.
func galleryProtoFrame(message []byte) []byte {
	out := []byte{0}
	out = binary.BigEndian.AppendUint32(out, uint32(len(message)))
	return append(out, message...)
}

func galleryProtoVarint(field int, value uint64) []byte {
	out := binary.AppendUvarint(nil, uint64(field)<<3)
	return binary.AppendUvarint(out, value)
}

func galleryProtoBytes(field int, data []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(field)<<3|2)
	out = binary.AppendUvarint(out, uint64(len(data)))
	return append(out, data...)
}

// gallerySSE is the SSE session window with a price event selected.
func gallerySSE(a *app) {
	a.sse = sseWorkbench{open: true, url: "https://api.example.com/events", reconnect: true, picked: 2,
		session: &sseSession{state: "Connected", cancel: func() {}, events: []sseSessionEvent{
			{seq: 1, at: galleryTime, detail: "Connected · https://api.example.com/events\n\nContent-Type: text/event-stream\nX-Trace: session-73"},
			{seq: 2, at: galleryTime.Add(time.Second), eventType: "price", id: "73", data: "{\n  \"symbol\": \"DEMO\",\n  \"price\": 128.75,\n  \"change\": -2.25\n}", size: 64},
			{seq: 3, at: galleryTime.Add(2 * time.Second), eventType: "message", id: "74", data: "ready", size: 5},
		}}}
}

// galleryAnalysis is the investigation board with two requests selected: the
// summary, not the single-request inspector.
func galleryAnalysis(a *app) {
	a.chosen.Clear()
	a.chosen.Add(1)
	a.chosen.Add(2)
	a.pickedID = 1
	a.recount()
	a.applyFilter()
	a.inspect.ready = true
	a.analysis.tab = 0
}

// galleryGuide is the first-launch checklist after trust and decryption: the
// listener step is open, and the two later steps are done.
func galleryGuide(a *app) {
	a.flows = nil
	a.requests = nil
	a.folders = nil
	a.guide = true
	a.recount()
	a.applyFilter()
	a.certificate.checked = true
	a.certificate.certificateResult = certificateResult{cert: &x509.Certificate{}}
	a.systemProxy.checked = true
	a.systemProxy.check = system.ProxyCheck{Status: system.ProxyOff}
	a.sslHosts = "api.example.com\ncdn.example.com"
	a.inspect.ready = true
}

// galleryMCP is a connected MCP server: the tool catalog on the right, the
// tool call beside it, and a timeline behind the catalog tab.
func galleryMCP(a *app) {
	a.addDocument(savedRequest{Request: request.Request{Name: "MCP server", Collection: "Workspace/Agents", Protocol: "MCP",
		URL: "https://mcp.example.com/mcp", MCP: request.MCPConfig{Transport: request.MCPTransportStreamableHTTP, Tool: "echo",
			ToolArgs: "{\n  \"text\": \"hello\"\n}", TimeoutSeconds: 30}}}, "")
	d := a.requestDocument
	u := d.mcpState()
	u.session = &mcpSession{state: "Connected", connected: true, seq: 5,
		server: request.MCPServerInfo{Name: "fixture", Title: "Fixture server", Version: "1.2.3", ProtocolVersion: "2025-11-25",
			Instructions: "Use echo to repeat text.",
			Capabilities: "{\n  \"tools\": {\"listChanged\": true},\n  \"resources\": {\"listChanged\": true}\n}",
			Tools:        true, Resources: true, Prompts: true, Logging: true},
		connection: mcpConnectionInfo{Transport: "Streamable HTTP", Endpoint: "https://mcp.example.com/mcp", Timeout: "30s", Proxied: true},
		tools: []request.MCPTool{
			{Name: "echo", Title: "Echo", Description: "Echo text back", InputSchema: "{\n  \"type\": \"object\",\n  \"properties\": {\n    \"text\": {\"type\": \"string\"}\n  }\n}"},
			{Name: "fail", Title: "Fail", Description: "Fail on purpose", InputSchema: "{\n  \"type\": \"object\"\n}"},
			{Name: "image", Title: "Image", Description: "Return a small image", InputSchema: "{\n  \"type\": \"object\"\n}"},
		},
		resources: []request.MCPResource{{URI: "memo://welcome", Name: "welcome", Title: "Welcome", MIMEType: "text/plain", Description: "A short note"}},
		templates: []request.MCPResourceTemplate{{URITemplate: "memo://{name}", Name: "memo", MIMEType: "text/plain", Description: "One memo by name"}},
		prompts:   []request.MCPPrompt{{Name: "greet", Title: "Greet", Description: "A greeting", Arguments: []request.MCPPromptArgument{{Name: "name", Description: "Who to greet", Required: true}}}},
		events: []mcpEvent{
			{seq: 1, at: galleryTime, kind: "System", name: "Connected", text: "Fixture server 1.2.3\nMCP 2025-11-25 · Streamable HTTP"},
			{seq: 2, at: galleryTime.Add(time.Second), kind: "Out", name: "catalog", text: "tools/list · resources/list · resources/templates/list · prompts/list"},
			{seq: 3, at: galleryTime.Add(2 * time.Second), kind: "In", name: "catalog", text: "3 tools · 1 resource · 1 template · 1 prompt"},
			{seq: 4, at: galleryTime.Add(3 * time.Second), kind: "Out", name: "tools/call echo", text: "echo\n{\n  \"text\": \"hello\"\n}"},
			{seq: 5, at: galleryTime.Add(4 * time.Second), kind: "In", name: "tools/call echo", text: "echo: hello\n\n[structuredContent]\n{\n  \"echo\": \"hello\",\n  \"count\": 1\n}"},
		}}
	for i := range u.session.events {
		u.session.events[i].size = len(u.session.events[i].text)
	}
	u.tab, u.responseTab, u.catalogTab, u.catalogRow, u.selected = mcpTabTools, 2, 0, 0, 4
	a.composeWaiting, a.sendCancel = true, func() {}
}

// gallerySocketIO is the Socket.IO editor with a connected namespace, a
// pending ACK and a binary message selected.
func gallerySocketIO(a *app) {
	a.addDocument(savedRequest{Request: request.Request{Name: "Admin events", Collection: "Workspace/Realtime", Protocol: "Socket.IO",
		URL: "https://api.example.com/socket.io/", Body: `["answer-73"]`,
		SocketIO: request.SocketIOConfig{Event: "echo", Namespace: "/admin", WantACK: true}}}, "")
	d := a.requestDocument
	u := d.socketIOState()
	u.session = &socketIOSession{state: "Connected", conn: new(request.SocketIOConn), cancel: func() {}, owed: map[uint64]bool{7: true}, seq: 3, events: []socketIOEvent{
		{seq: 1, at: galleryTime, direction: "System", name: "Connected", text: "Namespace /admin · Socket ID local-fixture\nEngine.IO 4 · WebSocket only · No reconnect or replay", size: 102},
		{seq: 2, at: galleryTime.Add(time.Second), direction: "In", name: "question · #7", text: "[\n  \"answer me\"\n]", size: 17},
		{seq: 3, at: galleryTime.Add(2 * time.Second), direction: "In", name: "bytes-back", text: "[\n  {\n    \"nested\": [{\"_placeholder\": true, \"num\": 0}]\n  }\n]\n\nAttachment 0 · 5 B · base64\n/wBJwyg=", size: 105},
	}}
	a.composeWaiting, a.sendCancel = true, func() {}
	u.selected = 2
	u.list.Selected = &u.selected
}
