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
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
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
		{name: "requests-body", setup: func(a *app) { draft(a); a.composeTab = composeTabBody }, want: "Request body"},
		{name: "requests-response", setup: func(a *app) {
			draft(a)
			a.sent, a.sentSet, a.sentAt = a.draft(), true, snapshotTime
			a.response = &capture.Flow{Status: 201, Millis: 47, Bytes: 43, ResponseHeader: http.Header{"Content-Type": {"application/json"}}, ResponseBody: `{"id":73,"name":"Demo project","active":true}`}
			a.sendOutcome = "Response received"
			a.responseArrived()
		}, want: "201 Created"},
		{name: "settings", setup: func(a *app) { a.settingsOpen = true }, want: "Dark appearance"},
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
			if state.name == "capture-selected" || state.name == "capture-ws" || state.name == "capture-connect" || state.name == "requests-editing" || state.name == "requests-response" || state.name == "active-tools" || state.name == "requests-active-tools" {
				widths = append(widths, 960)
			}
			for _, width := range widths {
				t.Run(fmt.Sprintf("%s/%s-%d", theme, state.name, width), func(t *testing.T) {
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
					}, width, 800)
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
					base := filepath.Join(theme, fmt.Sprintf("%s-%d", state.name, width))
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
