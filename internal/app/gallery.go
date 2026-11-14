package app

// The design gallery's window into the app: each screen is the real view,
// built from fake data, with no listener, settings file or request sent.
// internal/gallery names the screens and draws them; nothing here changes
// how a screen is built.

import (
	"net/http"
	"strconv"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
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
}

// NewGallery builds screen, one of the ids internal/gallery lists, or nil for
// an unknown one. The caller closes it when the screen is done.
func NewGallery(screen string) *Gallery {
	g := &Gallery{a: galleryApp()}
	if !g.setup(screen) {
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
