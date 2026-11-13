package app

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"image"
	"image/png"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/system"
	"github.com/baba983/vscroxy/internal/upstream"
	"github.com/egoist/mygo/ui"
	"github.com/gorilla/websocket"
)

// openResponseBody makes the response pane's body the visible inspector tab, as
// a person gets it by choosing Body.
func openResponseBody(a *app) {
	a.inspect.tab[1] = resViewBody
	a.inspect.ready = true
}

// clickLabel clicks the center of the text or label s without MyGo's
// target check. A field's label can overlap the control it names, and the
// click is the label's activation either way.
func clickLabel(tt *ui.Tester, s string) error {
	r, ok := tt.Find(s)
	if !ok {
		return fmt.Errorf("no element shows %q", s)
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	return nil
}

// App menus are drawn in the window, including each opened submenu.
// saveAsExample takes the response pane's action wherever the row puts it:
// inline while the row has room, in the pane's menu when it does not.
func saveAsExample(t *testing.T, tt *ui.Tester) {
	t.Helper()
	if _, found := tt.Find("Save as example"); found {
		if err := tt.Click("Save as example"); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := tt.Click("Response pane options"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Save as example"); err != nil {
		t.Fatal(err)
	}
}

// dismissToasts clears the notifications over the pane a test works on. A
// toast on its way out still shows its text but takes no input, so one that
// will not take the click is waited for instead.
func dismissToasts(t *testing.T, tt *ui.Tester) {
	t.Helper()
	for range 3 {
		if _, ok := tt.Find("Dismiss notification"); !ok {
			return
		}
		if err := tt.Click("Dismiss notification"); err == nil {
			tt.Advance(250 * time.Millisecond)
			continue
		}
		tt.Advance(250 * time.Millisecond)
	}
}

func chooseMenu(tt *ui.Tester, path ...string) error {
	for _, label := range path {
		if _, found := tt.Find(label); !found {
			return fmt.Errorf("menu item %q is not shown", label)
		}
		// Type selection targets the focused menu, not an identically named
		// button (for example Capture's JSON filter) behind the overlay.
		prefix := strings.TrimRight(strings.ToLower(label), "…. ")
		if strings.ContainsFunc(prefix, func(r rune) bool { return r != ' ' && (r < 'a' || r > 'z') }) {
			if err := tt.Click(label); err != nil {
				return err
			}
			continue
		}
		tt.Advance(time.Second)
		for _, r := range prefix {
			if r == ' ' {
				// Shift keeps Space in type selection rather than activating
				// the highlighted item before the full label is entered.
				tt.Key(ui.Shift, ui.KeySpace)
			} else {
				tt.Key(0, ui.KeyA+ui.Key(r-'a'))
			}
		}
		tt.Key(0, ui.KeyEnter)
	}
	return nil
}

// choosePaneView opens a pane's More menu and picks a view it keeps out of
// the tab row.
func choosePaneView(t *testing.T, tt *ui.Tester, pane, label string) {
	t.Helper()
	dismissToasts(t, tt)
	if err := tt.Click(pane + " options"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := chooseMenu(tt, menuLabel(tt, label)); err != nil {
		t.Fatal(err)
	}
}

// menuLabel is the label as the menu shows it: a view with a count of its
// own says "Examples 2".
func menuLabel(tt *ui.Tester, label string) string {
	for _, s := range tt.Texts() {
		if s == label || strings.HasPrefix(s, label+" ") {
			return s
		}
	}
	return label
}

// chooseBodyFormat picks a representation from the pane's format menu.
func chooseBodyFormat(t *testing.T, tt *ui.Tester, side, mode string) {
	t.Helper()
	label := "Response body format"
	if side == "request" {
		label = "Request body format"
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, mode); err != nil {
		t.Fatal(err)
	}
}

// The shell paints every part of the window, title bar and the gutters between
// the cards included: a clear root let the desktop's wallpaper through them,
// which read as a different color on the top of the window.
func TestShellPaintsItsOwnSurfaceOverTheWindowMaterial(t *testing.T) {
	for _, material := range []bool{false, true} {
		a := workspaceApp()
		a.macChrome = true
		tt := ui.NewTester(a.view, 1280, 800)
		tt.SetVibrancy(material)
		im := tt.Image()
		for _, at := range []struct {
			name string
			x, y int
		}{{"title bar", 640, 10}, {"sidebar", 200, 30}, {"gutter", 221, 400}, {"content", 900, 700}} {
			if alpha := im.RGBAAt(at.x, at.y).A; alpha != 255 {
				t.Fatalf("material=%t: %s alpha %d, want an opaque surface", material, at.name, alpha)
			}
		}
	}
}

func TestShellKeepsItsControlsInsideTheWindow(t *testing.T) {
	for _, mac := range []bool{false, true} {
		for _, width := range []int{960, 1280} {
			for _, scale := range []float32{1, 2} {
				for _, bar := range []ui.TitleBar{{}, {Height: 48, Left: 76}, {Height: 44, Right: 192}} {
					a := workspaceApp()
					a.macChrome = mac
					tt := ui.NewTester(a.view, width, 800)
					tt.SetScale(scale)
					tt.SetTitleBar(bar)
					// The pill leads the bar after the brand — past the
					// native insets — then the tools that are on and the
					// field that finds every command; the switches close it.
					status, ok := tt.Find("Proxy status")
					if !ok {
						t.Fatal("the recording pill is missing")
					}
					brand, _ := tt.Find("vscroxy")
					// The way to every command is a button with the
					// shortcut, not a field: it leaves the bar's room to
					// the tools that are on.
					field, ok := tt.Find("Commands")
					if !ok || !tt.HasText(commandShortcut()) {
						t.Fatalf("the command button is missing its shortcut: %v", field)
					}
					more, _ := tt.Find("More")
					// One button switches the layout; its label names the
					// layout a click goes to.
					layout, ok := tt.Find("Stacked layout")
					if !ok {
						layout, _ = tt.Find("Side by side layout")
					}
					if mac {
						// Beside the sidebar the brand is the sidebar's.
						if brand.X >= a.sidebarWidth {
							t.Fatalf("the brand left the sidebar at mac=%t: %v", mac, brand)
						}
					} else if bar.Left > 0 && brand.X < bar.Left+uikit.IconLarge+16 {
						// With controls on the left the brand keeps a gap
						// from them, as it does from its own icon.
						t.Fatalf("the brand sits on the native controls: %v", brand)
					}
					if status.X < bar.Left || status.X+status.W > field.X {
						t.Fatalf("the pill does not lead the bar at mac=%t: pill=%v field=%v", mac, status, field)
					}
					if field.X+field.W > layout.X || layout.X < status.X+status.W {
						t.Fatalf("the command field and the switches overlap at mac=%t: %v %v", mac, field, layout)
					}
					if more.X+more.W > float32(width)-bar.Right {
						t.Fatalf("the switches run past the window at width=%d: %v", width, more)
					}
					if field.W < 48 {
						t.Fatalf("the command button is squeezed to %g at width=%d", field.W, width)
					}
				}
			}
		}
	}
}

func TestActiveToolChipsChangeConfigurationInBothWorkspaces(t *testing.T) {
	for _, page := range []page{pageLive, pageRequest} {
		t.Run(fmt.Sprint(page), func(t *testing.T) {
			a := workspaceApp()
			a.page = page
			var err error
			a.srv, err = capture.New("127.0.0.1:0", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.srv.Close() })
			a.breakOn, a.throttle, a.upstream = true, capture.Presets[1], "http://127.0.0.1:8081"
			a.srv.SetBreak(true)
			a.srv.SetThrottle(a.throttle)
			a.srv.SetNoCaching(true)
			if err := a.srv.SetUpstreamConfig(upstream.Settings{Default: a.upstream}, nil); err != nil {
				t.Fatal(err)
			}
			a.rules = []capture.Rule{{Type: capture.RuleBlock, Match: "/test", Scenario: "Offline", ScenarioActive: true}}
			if err := a.srv.SetRules(a.rules); err != nil {
				t.Fatal(err)
			}
			a.rulesPath = t.TempDir() // A directory makes saving fail, on every platform.
			// Wide enough for every chip at once: the title bar scrolls
			// them rather than wrapping when more are on than fit.
			tt := ui.NewTester(a.view, 1440, 800)
			click := func(label string) {
				t.Helper()
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			click("Active tool: Breakpoints")
			if !a.toolsOpen || a.toolsPage != "rules" {
				t.Fatal("breakpoint chip did not open the rules")
			}
			click("Done")
			click("Disable Scenario: Offline")
			if capture.ActiveScenario(a.rules) != "Offline" || capture.ActiveScenario(a.srv.Rules()) != "Offline" || !tt.HasText("Could not disable scenario") {
				t.Fatal("failed save hid or deactivated the active scenario")
			}
			a.rulesPath = filepath.Join(t.TempDir(), "rules.json")
			click("Disable Scenario: Offline")
			stored, err := capture.LoadRules(a.rulesPath)
			if err != nil || capture.ActiveScenario(stored) != "" || capture.ActiveScenario(a.srv.Rules()) != "" {
				t.Fatalf("scenario chip did not persist the inactive configuration: %v", err)
			}
			for _, label := range []string{"Breakpoints", "3G", "No Caching", "Upstream"} {
				click("Disable " + label)
				if _, found := tt.Find("Active tool: " + label); found {
					t.Fatalf("disabled tool %q still appears active", label)
				}
			}
			if a.breakOn || a.srv.Breaking() || a.throttle.Name != "Off" || a.srv.Throttle().Name != "Off" || a.srv.NoCaching() || a.upstream != "" || a.srv.UpstreamConfig().Default != "" {
				t.Fatal("chip close only hid the tool instead of disabling it")
			}
			// Disappearing and returning chips retain independent identities.
			click("Tools")
			if err := chooseMenu(tt, "No Caching"); err != nil {
				t.Fatal(err)
			}
			if !a.srv.NoCaching() || !tt.HasText("Active tool: No Caching") {
				t.Fatal("Tools menu could not re-enable caching control")
			}
			click("Disable No Caching")
		})
	}
}

// openDraft opens a captured request as a new request tab linked to it, as
// a tab in COLLECTIONS rather than the capture's document in LIVE.
func openDraft(a *app, f flow) {
	r, err := f.requestDraft()
	if err != nil {
		panic(err)
	}
	a.addDocument(r, "")
	a.requestDocument.setDebugSource(newDebugSource(f))
}

// appearanceOf is the explicit appearance a test draws in, whatever the
// system's.
func appearanceOf(dark bool) string {
	if dark {
		return "dark"
	}
	return "light"
}

// openSettingsFromBar opens Settings the way the window offers it: the ⋯ menu
// at the title bar's end, which carries it beside the app menu's ⌘, rather
// than a button of its own.
func openSettingsFromBar(t *testing.T, tt *ui.Tester) {
	t.Helper()
	if err := tt.Click("More"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Open settings…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

// The title bar's end carries the palette, the layout, the tools and ⋯, and
// nothing else: the sidebar's switch sits at the bar's start, beside the
// window's controls, and Settings is in ⋯ and the app menu rather than a
// button of its own.
func TestTitleBarKeepsItsControlsToFourAndSettingsInMore(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if _, ok := tt.Find("Settings"); ok {
		t.Fatal("the title bar still carries a settings button")
	}
	toggle, ok := tt.Find("Hide sidebar")
	if !ok {
		t.Fatal("the title bar has no sidebar switch")
	}
	brand, ok := tt.Find("vscroxy")
	if !ok {
		t.Fatal("the title bar has no brand")
	}
	if toggle.X > brand.X {
		t.Fatalf("the sidebar switch sits at %v, right of the brand at %v: want it at the bar's start", toggle.X, brand.X)
	}
	if err := tt.Click("More"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Open settings…"); err != nil {
		t.Fatal(err)
	}
	if !a.settingsOpen {
		t.Fatal("⋯ does not open Settings")
	}
}

// HTTPS reads as encrypted tunnels until this system trusts the CA, and the
// capture screen says so once, under the filter bar, with the certificate to
// install. Putting it away keeps it away.
func TestCaptureSaysHttpsStaysEncryptedUntilTheCertificateIsTrusted(t *testing.T) {
	a := workspaceApp()
	a.certificate.checked, a.certificate.cert, a.certificate.err = true, nil, errors.New("not trusted")
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if !tt.HasText("HTTPS content is not decrypted") {
		t.Fatalf("no banner: %q", tt.Texts())
	}
	if err := tt.Click("Install certificate"); err != nil {
		t.Fatal(err)
	}
	if !a.settingsOpen || a.settingsTab != 2 {
		t.Fatalf("Install certificate opened settings page %d, want the HTTPS page", a.settingsTab)
	}
	a.settingsOpen = false
	tt.Frame()
	if err := tt.Click("Dismiss the certificate banner"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("HTTPS content is not decrypted") || !a.certBannerOff {
		t.Fatal("the banner came back after it was put away")
	}
	// A system that trusts the CA has nothing to say.
	trusted := workspaceApp()
	tt = ui.NewTester(trusted.view, 1280, 800)
	tt.Frame()
	if tt.HasText("HTTPS content is not decrypted") {
		t.Fatal("a trusted certificate still shows the banner")
	}
}

func workspaceApp() *app {
	a := &app{
		filter: "all", sidebarWidth: 220, tablePercent: 56,
		collectionOpen: make(map[string]*bool), icons: make(map[string]ui.ImageSource),
		detailFor: -1, frames: make(map[int][]frame), workbench: desktopWorkbench{layout: "right"},
		flows: []flow{
			{id: 1, method: "GET", scheme: "https", host: "api.example.com", path: "/projects", url: "https://api.example.com/projects", status: 200, process: "Browser", reqHeader: http.Header{"Accept": {"application/json"}}, respHeader: http.Header{"Content-Type": {"application/json"}}, resBody: `{"projects":[]}`},
			{id: 2, method: "POST", scheme: "https", host: "api.example.com", path: "/login", url: "https://api.example.com/login", status: 401, process: "Browser"},
		},
		requests: []savedRequest{{Name: "List projects", Collection: "Workspace/API", Method: "GET", URL: "https://api.example.com/projects", Pre: `pm.request.headers.upsert("Accept", "application/json")`}},
	}
	a.recount()
	a.applyFilter()
	return a
}

func TestBreakpointDialogValidatesEditsAndKeepsOriginalActionReachable(t *testing.T) {
	a := workspaceApp()
	a.bp = &capture.Breakpoint{ID: 1, Method: "POST", URL: "https://api.example.com/login"}
	a.bpOpen, a.bpMethod, a.bpURL = true, "POST", a.bp.URL
	a.bpHeaders, a.bpAbort = "Authorization: Bearer old", "502 Bad Gateway"
	a.inform("Previous breakpoint continued")
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Breakpoint headers"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Ctrl, ui.KeyA)
	tt.Type("broken header")
	if !tt.HasText("Header line 1 must be a valid Name: Value pair.") {
		t.Fatalf("missing validation: %q", tt.Texts())
	}
	if err := tt.Click("Apply & continue"); err != nil {
		t.Fatal(err)
	}
	if a.bp == nil {
		t.Fatal("invalid headers were silently applied")
	}
	tt.Key(0, ui.KeyEscape)
	if a.bp == nil || !a.bpOpen {
		t.Fatal("Escape aborted a live request")
	}
	if err := tt.Click("Continue original"); err != nil {
		t.Fatal(err)
	}
	if a.bp != nil {
		t.Fatal("could not discard invalid edits and continue original")
	}
}

func TestWebSocketBreakpointOverSessionAndPayloadValidation(t *testing.T) {
	for _, kind := range []string{"text", "binary", "read-only"} {
		t.Run(kind, func(t *testing.T) {
			a := workspaceApp()
			a.socket.open = true
			done := make(chan struct{})
			a.bp = &capture.Breakpoint{ID: 1, Phase: capture.PhaseMessage, URL: "wss://api.example.com/socket", Direction: "out", MessageType: kind, Done: done, BodyReadOnly: kind == "read-only"}
			a.bpOpen, a.bpBody = true, "original"
			tt := ui.NewTester(a.view, 960, 600)
			for _, label := range []string{"Breakpoint headers", "Abort status", "Save as template…"} {
				if _, ok := tt.Find(label); ok {
					t.Fatalf("HTTP-only control in message pause: %s", label)
				}
			}
			if kind == "read-only" {
				if _, ok := tt.Find("Message payload"); ok {
					t.Fatal("large message offered an editable payload")
				}
				if err := tt.Click("Drop message"); err != nil {
					t.Fatal(err)
				}
				if a.bp != nil || !a.socket.open {
					t.Fatal("dropping dismissed the underlying session")
				}
				return
			}
			if err := tt.Click("Message payload"); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Ctrl, ui.KeyA)
			tt.Type("AP6AAQ==")
			if a.bpBody != "AP6AAQ==" || a.breakpointError() != "" {
				t.Fatal("valid edit not accepted over session panel")
			}
			if kind == "binary" {
				tt.Key(ui.Ctrl, ui.KeyA)
				tt.Type("AA\n==")
				if !tt.HasText("Enter canonical padded base64 for the binary message.") {
					t.Fatalf("invalid base64 not explained: %v", tt.Texts())
				}
				if err := tt.Click("Apply & continue"); err != nil {
					t.Fatal(err)
				}
				if a.bp == nil {
					t.Fatal("invalid base64 continued")
				}
			}
			tt.Key(0, ui.KeyEscape)
			if a.bp == nil || !a.socket.open {
				t.Fatal("Escape closed a live pause or its underlying session")
			}
			close(done)
			tt.Frame()
			if a.bp != nil || a.bpOpen {
				t.Fatal("expired message pause remains actionable")
			}
		})
	}
}

func TestWebSocketBreakpointDirectionSavedFromEditor(t *testing.T) {
	a := workspaceApp()
	a.rulesPath = filepath.Join(t.TempDir(), "rules.json")
	a.openRules()
	a.editRule(-1, capture.Rule{Type: capture.RuleBreakMessage, Match: "/socket"})
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("Client → server"); err != nil {
		t.Fatal(err)
	}
	if a.ruleUI.draft.Direction != "out" {
		t.Fatal("direction selection lost its bound value")
	}
	if err := tt.Click("Save rule"); err != nil {
		t.Fatal(err)
	}
	loaded, err := capture.LoadRules(a.rulesPath)
	if err != nil || len(loaded) != 1 || loaded[0].Type != capture.RuleBreakMessage || loaded[0].Direction != "out" {
		t.Fatalf("saved rule: %+v %v", loaded, err)
	}
}

func TestBreakpointTemplatesSaveAMessageAndApplyItAtALaterPauseOfThatPhase(t *testing.T) {
	a := workspaceApp()
	a.bpTemplatesPath = filepath.Join(t.TempDir(), "breakpoint-templates.json")
	pause := func(bp capture.Breakpoint) {
		a.bp = &bp
		a.bpOpen, a.bpMethod, a.bpURL, a.bpBody = true, bp.Method, bp.URL, bp.Body
		a.bpHeaders = request.HeaderBlock(bp.Header)
		a.bpTemplate, a.bpSaving = "", false
	}
	pause(capture.Breakpoint{ID: 1, Method: "POST", URL: "https://api.example.com/login", Body: `{"user":"a"}`})
	a.bpMethod, a.bpHeaders, a.bpBody = "PUT", "Authorization: Bearer test\nX-Debug: 1", `{"user":"admin"}`
	tt := ui.NewTester(a.view, 960, 600)
	if tt.HasText("No template") {
		t.Fatal("template menu shown with nothing saved")
	}
	if err := tt.Click("Save as template…"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Template name"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Admin login")
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	// A fresh launch reads the template back from disk.
	a.bpTemplates = nil
	a.reloadConfig()
	if len(a.bpTemplates) != 1 || a.bpTemplates[0].Name != "Admin login" {
		t.Fatalf("templates on disk = %+v", a.bpTemplates)
	}
	// A response template must not be offered at a request pause.
	a.bpTemplates = append(a.bpTemplates, bpTemplate{Name: "Outage", Phase: "response", Status: 503})

	pause(capture.Breakpoint{ID: 2, Method: "GET", URL: "https://api.example.com/other", Body: "x", Header: http.Header{"Accept": {"*/*"}}})
	tt.Frame()
	if err := tt.Click("Breakpoint template"); err != nil {
		t.Fatal(err)
	}
	if _, found := tt.Find("Outage"); found {
		t.Fatal("response template offered at a request pause")
	}
	if err := chooseMenu(tt, "Admin login"); err != nil {
		t.Fatal(err)
	}
	if a.bpMethod != "PUT" || a.bpBody != `{"user":"admin"}` || a.bpHeaders != "Authorization: Bearer test\nX-Debug: 1" {
		t.Fatalf("applied = %q %q %q", a.bpMethod, a.bpHeaders, a.bpBody)
	}
	if a.bpURL != "https://api.example.com/other" {
		t.Fatalf("template replaced the paused URL: %q", a.bpURL)
	}
	if a.bp == nil {
		t.Fatal("applying a template continued the request")
	}
	if !tt.HasText("Template: Admin login") {
		t.Fatalf("applied template not named: %q", tt.Texts())
	}

	if err := tt.Click("Breakpoint template"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Delete", "Admin login"); err != nil {
		t.Fatal(err)
	}
	a.bpTemplates = nil
	a.reloadConfig()
	if len(a.bpTemplates) != 1 || a.bpTemplates[0].Name != "Outage" {
		t.Fatalf("after delete = %+v", a.bpTemplates)
	}
}

func TestConnectionFailureCanBeFoundAndCopiedWithoutInventingAnHTTPError(t *testing.T) {
	a := workspaceApp()
	a.flows = []flow{{id: 10, method: "CONNECT", scheme: "https", host: "api.example.com:443", url: "https://api.example.com:443", status: 200, kind: "connect", conn: conn{error: "remote error: tls: unknown certificate authority", errorStage: "TLS client", log: "CONNECT succeeded\n[TLS client] FAILED: remote error: tls: unknown certificate authority\n"}}}
	a.applyFilter()
	tt := ui.NewTester(a.view, 960, 600)
	if !tt.HasText("Failed") {
		t.Fatalf("failure hidden behind CONNECT 200: %q", tt.Texts())
	}
	if err := tt.Click("Connection"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("TLS client failed") {
		t.Fatal("missing TLS side")
	}
	if err := tt.Click("Copy connection log"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != a.flows[0].conn.log {
		t.Fatalf("clipboard = %q", tt.Clipboard())
	}
	if err := tt.Click("Inspector options"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Diagnose this connection"); err != nil {
		t.Fatal(err)
	}
	tt.Frame() // the failure scrolls into view
	if !tt.HasText("Client TLS handshake failed") || !tt.HasText("Possible causes · not confirmed") {
		t.Fatal("missing stage-specific explanation or uncertainty")
	}
	if err := tt.Click("Copy diagnostics"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CONNECT https://api.example.com:443", "Recorded status: 200", "Failed stage: TLS client", "Raw error: remote error: tls: unknown certificate authority", "CONNECT 200 acknowledges tunnel setup", "What to try", a.flows[0].conn.log} {
		if !strings.Contains(tt.Clipboard(), want) {
			t.Fatalf("diagnostics missing %q: %s", want, tt.Clipboard())
		}
	}
	if a.page != pageDiagnostics || a.diagnostics.flowID != 10 || a.diagnostics.target != "https://api.example.com:443" {
		t.Fatalf("Explain did not open diagnostics on #10: page %v, %+v", a.page, a.diagnostics)
	}
	a.pending = []capture.Flow{{ID: 11, Method: "GET", Scheme: "https", Host: "other.example.com", Path: "/gateway", Status: 502, Kind: "http"}}
	tt.Frame()
	if !tt.HasText("Client TLS handshake failed") {
		t.Fatal("new traffic replaced the explanation for the inspected request")
	}
	if err := tt.Click("Errors"); err != nil {
		t.Fatal(err)
	}
	if len(a.visible) != 2 {
		t.Fatal("TLS failure missing from Errors filter")
	}
	if err := tt.Click("11"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Client TLS handshake failed") || tt.HasText("Explain") || !tt.HasText("GET https://other.example.com/gateway") {
		t.Fatalf("a real HTTP 502 inherited the previous transport failure: %q", tt.Texts())
	}
}

func TestConnectionExplanationsUseCapturedStageWithoutNeedingALog(t *testing.T) {
	for _, tc := range []struct{ stage, title, advice string }{
		{"DNS", "DNS lookup failed", "DNS mappings"},
		{"TCP", "Could not establish a connection", "host and port"},
		{"TLS client", "Client TLS handshake failed", "specific client's CA trust"},
		{"TLS upstream", "Upstream TLS handshake failed", "client-certificate requirement"},
		{"CONNECT", "Tunnel setup failed", "tunnel permissions"},
		{"HTTP", "HTTP exchange failed", "safe to repeat"},
		{"", "Connection failed", "does not establish a specific cause"},
		{"future stage", "Connection failed", "does not establish a specific cause"},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			a := workspaceApp()
			// Identical TLS-looking text must not override the captured stage.
			const raw = "fixture failure: tls: unknown certificate authority"
			a.flows = []flow{{id: 10, method: "GET", scheme: "https", host: "example.test", path: "/failure", status: 502, conn: conn{errorStage: tc.stage, error: raw}}}
			a.applyFilter()
			tt := ui.NewTester(a.view, 960, 600)
			if err := tt.Click("Explain"); err != nil {
				t.Fatal(err)
			}
			tt.Frame() // the failure scrolls into view
			if !tt.HasText(tc.title) || !tt.HasText(raw) {
				t.Fatalf("wrong explanation for stage %q: %q", tc.stage, tt.Texts())
			}
			if box, ok := tt.Find("Possible causes · not confirmed"); !ok || box.H <= 0 {
				t.Fatal("explanation content is clipped")
			}
			if err := tt.Click("Copy diagnostics"); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"GET https://example.test/failure", raw, tc.advice, "Possible causes (not confirmed)", "No connection history was recorded."} {
				if !strings.Contains(tt.Clipboard(), want) {
					t.Fatalf("diagnostics missing %q: %s", want, tt.Clipboard())
				}
			}
		})
	}
}

func TestConnectionExplanationScrollsLongErrorsWithoutTruncatingDiagnostics(t *testing.T) {
	a := workspaceApp()
	raw := strings.Repeat("upstream.example.test: connect: connection refused; ", 60)
	a.flows = []flow{{id: 10, method: "CONNECT", scheme: "https", host: "example.test:443", status: 502, kind: "connect", conn: conn{errorStage: "TCP", error: raw}}}
	a.applyFilter()
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Inspector options"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Diagnose this connection"); err != nil {
		t.Fatal(err)
	}
	tt.Frame() // the failure scrolls into view
	box, ok := tt.Find("Observed failure · TCP")
	if !ok || box.H <= 0 {
		t.Fatal("failure evidence is not visible")
	}
	tt.Scroll(box.X+20, box.Y+20, 0, 1e6)
	if box, ok := tt.Find("What to try"); !ok || box.H <= 0 {
		t.Fatal("cannot scroll past a long error to reach the advice")
	}
	if err := tt.Click("Copy diagnostics"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tt.Clipboard(), raw) {
		t.Fatal("copied diagnostics truncated the original error")
	}
}

func TestCaptureTableScrollsAndKeepsEachViewsPlace(t *testing.T) {
	a := workspaceApp()
	a.flows = nil
	for i := range defaultFlowLimit {
		path := fmt.Sprintf("/capture/%04d", i+1)
		a.flows = append(a.flows, flow{id: i + 1, method: "GET", scheme: "https", host: "api.example.com", path: path, url: "https://api.example.com" + path, status: 200})
	}
	a.applyFilter()
	tt := ui.NewTester(a.view, 1280, 800)
	row := "api.example.com/capture/0001"
	// Side by side by default: the newest row is inside the table, not laid
	// out past the footer.
	newest, ok := tt.Find("api.example.com/capture/5000")
	table, _ := tt.Find("Captured requests")
	if !ok || newest.H == 0 || newest.Y+newest.H > table.Y+table.H {
		t.Fatalf("a capture view must start with the latest request in view: %v, table %v", newest, table)
	}
	x, y := table.X+table.W/2, table.Y+table.H/2
	tt.Scroll(x, y, 0, -1e6)
	first, _ := tt.Find("api.example.com/capture/0001")
	second, _ := tt.Find("api.example.com/capture/0002")
	tt.Scroll(x, y, 0, 20*(second.Y-first.Y))
	if !tt.HasText("api.example.com/capture/0021") {
		t.Fatalf("scrolling did not reveal later requests: %q", tt.Texts())
	}
	tt.Frame()
	if !tt.HasText("api.example.com/capture/0021") {
		t.Fatal("a redraw reset the scroll position")
	}
	if err := tt.Click("api.example.com/capture/0021"); err != nil {
		t.Fatal(err)
	}
	// Editing happens in place: the list keeps its position and follow state.
	if err := tt.Click("URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnd)
	tt.Type("?edited=1")
	if !tt.HasText("api.example.com/capture/0021") || !tt.HasText("GET https://api.example.com/capture/0021") || a.following() || !tt.HasText("Edited") {
		t.Fatal("editing the capture changed the list position, follow state or inspected request")
	}
	if err := tt.Click("New view"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("api.example.com/capture/5000") || tt.HasText("api.example.com/capture/0021") {
		t.Fatal("a new capture view must follow latest without inheriting another view's position")
	}
	if err := tt.Click("Capture view: View 1"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("api.example.com/capture/0021") {
		t.Fatal("returning to a capture view lost its scroll position")
	}
	tt.Scroll(x, y, 0, 1e6)
	if !tt.HasText("api.example.com/capture/5000") {
		t.Fatal("cannot reach the final retained request")
	}
	tt.Wheel(x, y, 0, -1e6)
	tt.Advance(time.Second)
	if !tt.HasText(row) {
		t.Fatal("mouse wheel cannot return to the first request")
	}
	appendFlow := func(id int) {
		t.Helper()
		path := fmt.Sprintf("/capture/%04d", id)
		a.flows = append(a.flows, flow{id: id, method: "GET", scheme: "https", host: "api.example.com", path: path, url: "https://api.example.com" + path, status: 200})
		a.applyFilter()
		tt.Frame()
	}
	appendFlow(5001)
	if !tt.HasText(row) || tt.HasText("api.example.com/capture/5001") {
		t.Fatal("new traffic pulled the reader away from older requests")
	}
	// Following is the footer's Auto-follow button, so its state is the
	// app's: new traffic must not have put the list back on the end.
	if a.following() {
		t.Fatalf("new traffic pulled the reader to the end: %q", tt.Texts())
	}
	if err := tt.Click(row); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Auto-follow"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("api.example.com/capture/5001") {
		t.Fatal("Latest must scroll, not only change the selection")
	}
	if f, ok := a.current(); !ok || f.id != 1 || !a.chosen.Has(1) || a.chosen.Len() != 1 {
		t.Fatal("following the list replaced the inspected request or bulk choice")
	}
	appendFlow(5002)
	if !tt.HasText("api.example.com/capture/5002") {
		t.Fatal("Latest did not resume following new traffic")
	}
	tt.Scroll(x, y, 0, -1e6)
	tt.Scroll(x, y, 0, 1e6)
	appendFlow(5003)
	if !tt.HasText("api.example.com/capture/5003") {
		t.Fatal("scrolling back to the bottom did not resume following")
	}
	// The capture cap drops the oldest row even when the row count stays fixed.
	a.flows = a.flows[1:]
	appendFlow(5004)
	if !tt.HasText("api.example.com/capture/5004") {
		t.Fatal("following stopped when an old row was evicted")
	}
}

func TestWorkspaceKeepsCaptureAndDraftIndependent(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 960, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	value := func(label string) string {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		return tt.Clipboard()
	}
	click("Errors")
	if !tt.HasText("401") || tt.HasText("200") {
		t.Fatal("Errors should show the filtered traffic")
	}
	if err := tt.RightClick("api.example.com/login"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Edit request"); err != nil {
		t.Fatal(err)
	}
	if value("URL") != "https://api.example.com/login" || !tt.HasText("Save to collection") || !tt.HasText("401") {
		t.Fatal("the captured request did not open in place as a document")
	}
	click("List projects")
	if value("URL") != "https://api.example.com/projects" || tt.HasText("Captured requests") {
		t.Fatal("a saved request should replace the traffic list with its draft")
	}
	click("URL")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("https://api.example.com/projects?limit=7")
	click("Method")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	choosePaneView(t, tt, "Request pane", "Scripts")
	click("Pre-request")
	tt.Key(ui.Cmd, ui.Key1)
	if !tt.HasText("401") || tt.HasText("200") {
		t.Fatal("switching workspaces lost the error filter")
	}
	tt.Key(ui.Cmd, ui.Key2)
	if url, script := value("URL"), value("Pre-request script"); url != "https://api.example.com/projects?limit=7" || a.composeMethod != "" || script != `pm.request.headers.upsert("Accept", "application/json")` {
		t.Fatalf("switching workspaces changed the draft: URL %q, method %q, script %q", url, a.composeMethod, script)
	}
}

func TestRequestResourcesPreserveDraftAndTreeExpansion(t *testing.T) {
	a := workspaceApp()
	a.page = pageRequest
	a.requests = append(a.requests, savedRequest{Name: "Other request", Collection: "Other/API", Method: "POST", URL: "https://other.test"})
	tt := ui.NewTester(a.view, 960, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("Collection: Workspace")
	tt.Key(0, ui.KeyLeft)
	if tt.HasText("GET  List projects") || !tt.HasText("POST  Other request") {
		t.Fatal("collapsing one collection affected the wrong branch")
	}
	click("Search collections")
	tt.Type("projects")
	if !tt.HasText("GET  List projects") || tt.HasText("POST  Other request") {
		t.Fatal("search did not reveal only matching ancestors and requests")
	}
	click("List projects")
	click("URL")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("https://api.example.com/unsent")
	tt.Key(ui.Cmd, ui.KeyB)
	if tt.HasText("GET  List projects") || tt.HasText("Live") {
		t.Fatal("sidebar remained visible")
	}
	tt.Key(ui.Cmd, ui.KeyB)
	if !tt.HasText("GET  List projects") || tt.HasText("POST  Other request") {
		t.Fatal("restoring the sidebar lost its search")
	}
	// Other places in the window never replace the draft.
	tt.Key(ui.Cmd, ui.Key1)
	click("Collection options")
	if err := chooseMenu(tt, "History"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.Key2)
	click("URL")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != "https://api.example.com/unsent" {
		t.Fatal("resource navigation discarded the unsent draft")
	}
	click("Search collections")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	if tt.HasText("GET  List projects") || !tt.HasText("POST  Other request") {
		t.Fatal("search rewrote the collection expansion state")
	}
	click("Collection: Workspace")
	tt.Key(0, ui.KeyRight)
	if !tt.HasText("GET  List projects") {
		t.Fatal("collection cannot be expanded again with the keyboard")
	}
}

func TestCaptureUIUsesOnlyTheRunningListener(t *testing.T) {
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a := workspaceApp()
	fake := &fakeSystemProxy{check: system.ProxyCheck{Status: system.ProxyOff}}
	a.systemProxy.read = fake.read
	a.srv, a.flows, a.visible = srv, nil, nil
	tt := ui.NewTester(a.view, 1240, 800)
	if !tt.HasText("Proxy stopped") || tt.HasText(srv.Addr()) {
		t.Fatal("an unstarted server must not appear live")
	}
	a.setSystemProxy(true)
	if a.message != "System proxy: start capture successfully first" {
		t.Fatal("an unstarted proxy must not change system settings")
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Listening on") || !tt.HasText(srv.Addr()) || !tt.HasText("Listening at "+srv.Addr()+" · no retained requests yet") {
		t.Fatalf("capture must show its actual bound address, texts: %q", tt.Texts())
	}
	// Paused, the proxy still forwards but keeps only what the app itself
	// sent; resuming keeps ambient traffic again.
	if err := tt.Click("Pause recording"); err != nil {
		t.Fatal(err)
	}
	a.pending = []capture.Flow{{ID: 1, Method: "GET", Host: "ambient.test"}, {ID: 2, Method: "GET", Host: "editor.test", DebugRun: "run"}}
	tt.Frame()
	if !tt.HasText("Paused") || len(a.flows) != 1 || a.flows[0].id != 2 {
		t.Fatalf("paused recording kept %d flows, want only the editor's send", len(a.flows))
	}
	if err := tt.Click("Resume recording"); err != nil {
		t.Fatal(err)
	}
	a.pending = []capture.Flow{{ID: 3, Method: "GET", Host: "ambient.test"}}
	tt.Frame()
	if !tt.HasText("Listening on") || len(a.flows) != 2 || a.flows[1].id != 3 {
		t.Fatalf("resumed recording kept %d flows, want the ambient one too", len(a.flows))
	}
	openSettingsFromBar(t, tt)
	if err := tt.Click("Proxy"); err != nil {
		t.Fatal(err)
	}
	settleProxy(t, a, tt)
	if _, found := tt.Find(srv.Addr()); !found {
		t.Fatal("settings did not use the actual bound address")
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// Match the Listener's address itself, not the System proxy explanation
	// of the address it checked, which remains valid after capture stops.
	_, listenerAddress := tt.Find(srv.Addr())
	if !tt.HasText("Capture is not running") || listenerAddress {
		t.Fatal("closed proxy must not advertise its old address")
	}
}

func TestSettingsOpensExistingEditors(t *testing.T) {
	a := workspaceApp()
	a.srv = requestProxy(t)
	tt := ui.NewTester(a.view, 960, 600)
	openSettingsFromBar(t, tt)
	if err := tt.Click("Proxy"); err != nil {
		t.Fatal(err)
	}
	tt.Scroll(120, 300, 0, 500)
	// The default upstream is edited in place and saved with the rest of the
	// upstream settings; clearing it connects directly again.
	scrollTo := func(label string) {
		t.Helper()
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("no element shows %q", label)
		}
		tt.Scroll(850, 300, 0, r.Y-300)
		tt.Frame()
	}
	upstream := func(value string) {
		t.Helper()
		scrollTo("Default upstream")
		if err := tt.Click("Default upstream"); err != nil {
			t.Fatal(err)
		}
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(0, ui.KeyBackspace)
		tt.Type(value)
		scrollTo("Save upstream")
		if err := tt.Click("Save upstream"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	upstream("http://127.0.0.1:9")
	if a.upstream != "http://127.0.0.1:9" || a.srv.UpstreamConfig().Default != a.upstream {
		t.Fatalf("upstream not applied: %q", a.upstream)
	}
	upstream("")
	if a.upstream != "" || a.srv.UpstreamConfig().Default != "" {
		t.Fatalf("clearing kept the upstream: %q", a.upstream)
	}
	if err := tt.Click("Settings category: Proxy"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown) // HTTPS category, not Capture's HTTPS filter chip.
	tt.Scroll(480, 300, 0, 500)
	if err := tt.Click("TLS key log…"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("TLS key log") {
		t.Fatal("settings did not open the TLS key log editor")
	}
}

func TestSettingsSwitchesApplyAtOnce(t *testing.T) {
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.settingsOpen = true
	tt := ui.NewTester(a.view, 1280, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	click("Settings category: Capture")
	sw, ok := tt.Find("No Caching")
	if !ok {
		t.Fatal("no No Caching switch")
	}
	click("No Caching")
	if !a.srv.NoCaching() {
		t.Fatal("No Caching did not turn on")
	}
	// The toolbar now shows a "No Caching" chip too; click the switch where it was.
	tt.ClickAt(sw.X+sw.W/2, sw.Y+sw.H/2)
	tt.Frame()
	if a.srv.NoCaching() {
		t.Fatal("No Caching did not turn off")
	}
	click("Settings category: HTTPS")
	click("Decrypt every domain")
	if !a.sslAll || !a.inspectionConfig().All {
		t.Fatal("every domain was not enabled")
	}
	click("Decrypt every domain")
	if a.sslAll {
		t.Fatal("every domain stayed enabled")
	}
}

func TestAllowListFromSettingsAndToolbar(t *testing.T) {
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.settingsOpen = true
	tt := ui.NewTester(a.view, 1280, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	click("Settings category: Capture")
	click("Only show allowed traffic")
	if l := a.srv.Allow(); !l.On || len(l.Hosts) != 0 {
		t.Fatalf("allow list = %+v, want on and empty", l)
	}
	if !tt.HasText("Nothing is shown until you add a host or an app.") {
		t.Fatal("an empty allow list was not flagged")
	}
	a.lastSave = time.Time{} // past the write debounce
	if err := a.persistSettings(); err != nil || !a.saved.AllowList {
		t.Fatalf("the allow list switch was not saved: %v", err)
	}
	click("Allowed hosts…")
	a.allowText = "api.example.com\n# a comment"
	tt.Frame()
	click("Save")
	if l := a.srv.Allow(); !l.On || len(l.Hosts) != 1 || l.Hosts[0] != "api.example.com" {
		t.Fatalf("allow list = %+v, want api.example.com", l)
	}
	click("Disable Allow list")
	if a.allowOn || a.srv.Allow().On {
		t.Fatal("the toolbar chip did not turn the allow list off")
	}
}

func TestRetentionDropsOldestUnpinnedFlowsPastEitherLimit(t *testing.T) {
	a := workspaceApp()
	body := strings.Repeat("x", 300<<10)
	a.flows = []flow{
		{id: 1, method: "GET", host: "a.test", path: "/1", resBody: body, pinned: true},
		{id: 2, method: "GET", host: "a.test", path: "/2", resBody: body},
		{id: 3, method: "GET", host: "a.test", path: "/3", resBody: body},
	}
	a.memoryLimit = 1 // MB: three bodies of 300 KiB fit, four do not
	a.applyFilter()
	tt := ui.NewTester(a.view, 1280, 800)
	a.pending = []capture.Flow{{ID: 4, Method: "GET", Scheme: "https", Host: "a.test", Path: "/4", Status: 200, Kind: "http", ResponseBody: body}}
	tt.Frame()
	ids := func() (out []int) {
		for _, f := range a.flows {
			out = append(out, f.id)
		}
		return out
	}
	if got := fmt.Sprint(ids()); got != "[1 3 4]" {
		t.Fatalf("past the memory limit kept %s, want the pinned 1 and the newest 3 4", got)
	}
	a.flowLimit = 2
	a.trimFlows()
	a.applyFilter()
	if got := fmt.Sprint(ids()); got != "[1 4]" {
		t.Fatalf("past the request limit kept %s, want the pinned 1 and the newest 4", got)
	}
	// Spilled bodies count against the disk limit at their full size, though
	// memory holds only their heads: 400 MB each, 1 GB fits two of them.
	spill := func(id int, pinned bool) flow {
		return flow{id: id, method: "GET", host: "a.test", resFile: fmt.Sprintf("res-%d.bin", id), resSize: 400 << 20, pinned: pinned}
	}
	a.flows = []flow{spill(1, true), spill(2, false), spill(3, false)}
	a.flowLimit, a.diskLimit = 0, 1
	a.trimFlows()
	if got := fmt.Sprint(ids()); got != "[1 3]" {
		t.Fatalf("past the disk limit kept %s, want the pinned 1 and the newest 3", got)
	}
	a.flows = []flow{{id: 1, method: "GET", host: "a.test", path: "/1", resBody: body, pinned: true}, {id: 4, method: "GET", host: "a.test", path: "/4", resBody: body}}
	a.flowLimit, a.diskLimit = 2, 0
	a.applyFilter()

	a.settingsOpen = true
	tt.Frame()
	for i, label := range []string{"Settings category: Capture", "2 requests", "1,000 requests"} {
		if i == 1 {
			tt.Scroll(600, 400, 0, 1000) // Memory is the last group
			tt.Frame()
		}
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	if a.flowLimit != 1000 || !tt.HasText("2 requests") {
		t.Fatalf("request limit = %d, want 1000 from Settings, with the usage shown", a.flowLimit)
	}
}

func TestSettingsCategoriesKeepNativeNavigationAndEditorRoutes(t *testing.T) {
	a := workspaceApp()
	a.settingsOpen = true
	tt := ui.NewTester(a.view, 960, 600)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("Settings category: Appearance")
	click("Dark")
	if a.appearance != "dark" || !a.dark {
		t.Fatal("Appearance did not switch to dark")
	}
	click("System")
	if a.appearance != "" || a.dark {
		t.Fatal("System did not follow the light system appearance")
	}
	click("Settings category: Appearance")
	tt.Key(0, ui.KeyEnd)
	if !tt.HasText("Client certificates and extra roots for requests and captured upstream TLS.") {
		t.Fatal("native End navigation did not select Certificates")
	}
	click("Settings category: Capture")
	tt.Scroll(600, 300, 0, 400) // the Ignored group is below the fold
	tt.Frame()
	click("Ignored hosts…")
	if !tt.HasText("Ignored hosts") || a.settingsOpen {
		t.Fatal("Capture did not hand off to the existing ignored-host editor")
	}
	click("Cancel")
	openSettingsFromBar(t, tt)
	click("Done")
	if a.settingsOpen {
		t.Fatal("the fixed Done footer was not reachable")
	}
}

func TestSettingsEscapeRestoresVisibleKeyboardFocus(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			a := workspaceApp()
			a.appearance = appearanceOf(dark)
			tt := ui.NewTester(a.view, 960, 800)
			// Focused includes ancestors; the content root is initially
			// focused, so exclude it by checking a neighboring control.
			for range 100 {
				tt.Key(0, ui.KeyTab)
				if tt.Focused("Proxy status") && !tt.Focused("Tools") {
					break
				}
			}
			hasRing := func() bool {
				bounds, ok := tt.Find("Proxy status")
				if !ok {
					return false
				}
				im, ink := tt.Image(), uikit.Theme(dark).Focus
				for y := int(bounds.Y) - 4; y < int(bounds.Y+bounds.H)+4; y++ {
					for x := int(bounds.X) - 4; x < int(bounds.X+bounds.W)+4; x++ {
						p := im.RGBAAt(x, y)
						if p.R == ink.R && p.G == ink.G && p.B == ink.B && p.A == ink.A {
							return true
						}
					}
				}
				return false
			}
			if !tt.Focused("Proxy status") || tt.Focused("Tools") || !hasRing() {
				t.Fatal("Tab did not visibly focus the Settings opener")
			}
			tt.Key(0, ui.KeyEnter)
			if !a.settingsOpen {
				t.Fatal("Enter did not open Settings")
			}
			tt.Key(0, ui.KeyTab)
			// Inspect before key release or another input can request an
			// unrelated frame and hide a missing dismissal repaint.
			tt.KeyDown(ui.KeyEscape)
			if a.settingsOpen || !tt.Focused("Proxy status") || tt.Focused("Tools") || !hasRing() {
				t.Fatal("Escape did not restore a visible focus ring to Settings")
			}
			tt.KeyUp(ui.KeyEscape)
		})
	}
}

func TestLongImportReportKeepsFooterReachable(t *testing.T) {
	a := workspaceApp()
	a.importReport = importReport{open: true, title: "Import review"}
	for i := range 40 {
		a.importReport.lines = append(a.importReport.lines, fmt.Sprintf("Request %d: unsupported option omitted", i+1))
	}
	tt := ui.NewTester(a.view, 960, 600)
	before, ok := tt.Find("OK")
	if !ok || before.Y < 0 || before.Y+before.H > 600 {
		t.Fatalf("footer is outside the short window: %v", before)
	}
	tt.Scroll(480, 300, 0, 3000)
	after, _ := tt.Find("OK")
	last, _ := tt.Find("• Request 40: unsupported option omitted")
	if after != before || last.Y < 0 || last.Y+last.H > after.Y {
		t.Fatalf("report did not scroll independently of its footer: last=%v, footer=%v → %v", last, before, after)
	}
	if err := tt.Click("OK"); err != nil || a.importReport.open {
		t.Fatalf("report footer did not dismiss the dialog: %v", err)
	}
}

func TestHTTPSSetupExplainsTunnelsAndEnablesOnlyTheSelectedDomain(t *testing.T) {
	dir := t.TempDir()
	srv, err := capture.New("127.0.0.1:0", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	a := workspaceApp()
	a.srv, a.caPath, a.sslPath = srv, filepath.Join(dir, "mitmproxy-ca-cert.pem"), filepath.Join(dir, "ssl.json")
	a.flows = []flow{{id: 1, method: "CONNECT", scheme: "https", host: "dc.dlife.cn:18443", url: "https://dc.dlife.cn:18443", kind: "connect", status: 200}}
	a.applyFilter()
	tt := ui.NewTester(a.view, 1280, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	// Wait for the real, read-only system check, not for an arbitrary delay.
	waitCertificate := func() {
		t.Helper()
		if a.certificate.checking {
			select {
			case result := <-a.certificate.pending:
				a.certificate.pending <- result
			case <-t.Context().Done():
				t.Fatal("certificate check did not complete")
			}
		}
		tt.Frame()
	}
	waitCertificate()
	if !tt.HasText("Encrypted, not decrypted — not individual requests") || !tt.HasText("Decrypt dc.dlife.cn:18443") {
		t.Fatalf("tunnel setup missing: %q", tt.Texts())
	}
	click("Connection")
	if !tt.HasText("Off for dc.dlife.cn") || !tt.HasText("Trust not verified on this system") {
		t.Fatalf("the rules that would decrypt it are not in the Connection tab, or an uninstalled CA was reported trusted: %q", tt.Texts())
	}
	click("HTTPS setup")
	click("Decrypt dc.dlife.cn:18443")
	if !tt.HasText("Encrypted, not decrypted — not individual requests") {
		t.Fatalf("enabling a domain must not make the old CONNECT appear decrypted: %q", tt.Texts())
	}
	click("Connection")
	if !tt.HasText("On for dc.dlife.cn") {
		t.Fatalf("the domain rule did not reach the Connection tab: %q", tt.Texts())
	}
	click("HTTPS setup")
	cfg, err := a.readSSL()
	if err != nil || !cfg.Decrypts("api.dc.dlife.cn") || cfg.Decrypts("other.dlife.cn") || cfg.All || !srv.SSL().Decrypts("dc.dlife.cn") || srv.SSL().All {
		t.Fatalf("domain selection was not saved with the correct scope: %+v, %v", cfg, err)
	}
	click("Install certificate")
	waitCertificate()
	if !tt.HasText("Trust not verified on this system") || !tt.HasText("dc.dlife.cn") {
		t.Fatalf("settings did not show scoped trust and domain state: %q", tt.Texts())
	}
	// A missing/replaced certificate must not leave the previous result cached.
	if err := os.Remove(a.caPath); err != nil {
		t.Fatal(err)
	}
	a.certificate.installErr = errors.New("authorization could not finish")
	tt.Frame()
	click("Check again")
	waitCertificate()
	if !tt.HasText("Local CA unavailable") || !tt.HasText("CA file not found. Start capture, then check again.") || !tt.HasText("Installation failed: authorization could not finish") {
		t.Fatalf("checking again must refresh trust without hiding an unresolved installation error: %q", tt.Texts())
	}
	click("Manage domains…")
	if !tt.HasText("SSL proxying") {
		t.Fatal("HTTPS settings did not open the domain editor")
	}
	click("Decryption domains")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("dlife.cn")
	click("Save")
	cfg, err = a.readSSL()
	if err != nil || !cfg.Decrypts("other.dlife.cn") || cfg.Decrypts("notdlife.cn") {
		t.Fatal("a parent-domain rule must enable its subdomains")
	}
}

func TestHTTPSAppRuleCanBeScopedEditedAndCancelled(t *testing.T) {
	a := workspaceApp()
	a.sslPath = filepath.Join(t.TempDir(), "ssl.json")
	a.sslHosts = "existing.test"
	a.certificate.checked = true
	path := filepath.Join(t.TempDir(), "client")
	// The desktop's icon lookup needs the running loop; the badge stands in.
	a.iconLookup = func(string) ([]byte, bool) { return nil, false }
	a.flows = []flow{
		{id: 1, method: "CONNECT", scheme: "https", host: "one.test:8443", kind: "connect", status: 200, process: "client", processPath: path},
		{id: 2, method: "CONNECT", scheme: "https", host: "two.test:443", kind: "connect", status: 200, process: "client"},
	}
	a.applyFilter()
	tt := ui.NewTester(a.view, 960, 600)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	load := func() capture.SSLConfig {
		t.Helper()
		cfg, err := a.readSSL()
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	click("Inspector options")
	if err := chooseMenu(tt, "Enable HTTPS for client"); err != nil {
		t.Fatal(err)
	}
	cfg := load()
	if cfg.All || len(cfg.Apps) != 1 || cfg.Apps[0].Path != path || !cfg.Decrypts("existing.test") || !cfg.DecryptsProcess("another.test:9443", path) || cfg.DecryptsProcess("another.test", "") {
		t.Fatalf("app rule changed domain/global scope or lost its executable: %+v", cfg)
	}
	click("Connection")
	if !tt.HasText("On for client") {
		t.Fatalf("the app rule did not reach the Connection tab: %q", tt.Texts())
	}
	click("HTTPS setup")
	if !tt.HasText("Encrypted, not decrypted — not individual requests") {
		t.Fatalf("enabling an app claimed this historical tunnel was decrypted: %q", tt.Texts())
	}
	click("Manage HTTPS rules")
	click("Decryption domains")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("edited.test")
	click("Save")
	cfg = load()
	if !cfg.Decrypts("edited.test") || cfg.Decrypts("existing.test") || len(cfg.Apps) != 1 || cfg.Apps[0].Path != path {
		t.Fatalf("editing domains lost the independent app rule: %+v", cfg)
	}
	click("Manage HTTPS rules")
	box, ok := tt.Find("Decryption domains")
	if !ok {
		t.Fatal("rule editor did not open")
	}
	tt.Scroll(box.X+10, box.Y+10, 0, 1e6)
	click("Remove HTTPS app: " + path)
	click("Cancel")
	click("Manage HTTPS rules")
	tt.Scroll(box.X+10, box.Y+10, 0, 1e6)
	if !tt.HasText(path) || len(load().Apps) != 1 {
		t.Fatal("cancelling a removal changed the enabled app rule")
	}
	click("Remove HTTPS app: " + path)
	click("Save")
	if len(load().Apps) != 0 || !load().Decrypts("edited.test") {
		t.Fatal("saving an app removal affected the independent domain rule")
	}
	click("two.test:443")
	click("Inspector options")
	if tt.HasText("Enable HTTPS for client") {
		t.Fatal("an unidentified client was offered a same-name app permission")
	}
	tt.Key(0, ui.KeyEscape)
	click("Connection")
	if !tt.HasText("No local executable was recorded") {
		t.Fatalf("the Connection tab does not say the app is unknown: %q", tt.Texts())
	}
}

func TestCaptureSearchAndFilterFailuresAreRecoverable(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 960, 600)
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Type("no-such-host")
	if !tt.HasText("No matching requests") || tt.HasText("Waiting for traffic") {
		t.Fatalf("search hid captured traffic without explaining why: %q", tt.Texts())
	}
	if err := tt.Click("Show all requests"); err != nil {
		t.Fatal(err)
	}
	if a.footerCountsText() != "" || len(a.visible) != 2 {
		t.Fatalf("reset did not restore the captured requests: %q", a.footerCountsText())
	}
	a.openFilter()
	tt.Frame()
	if err := tt.Click("Filter status"); err != nil {
		t.Fatal(err)
	}
	tt.Type("4xx, nonsense")
	if err := tt.Click("Apply"); err == nil && !a.views.filterOpen {
		t.Fatal("invalid status expression silently applied")
	}
	if !tt.HasText("Use HTTP status codes (100–599), ranges, comparisons or comma-separated alternatives.") {
		t.Fatal("invalid status expression has no visible validation")
	}
	tt.Click("Filter status")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("4xx, 5xx")
	if err := tt.Click("Apply"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("1 of 2") {
		t.Fatalf("comma-separated alternatives did not match the 401 response: %q", tt.Texts())
	}
}

func TestRequestBodyEditorKeepsTheDraftInSync(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	a.openCompose()
	d := a.requestDocument
	d.composeTab = composeTabBody
	tt.Frame()
	if err := tt.Click("Request body"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(`{"quantity": 2}`)
	if d.composeBody != `{"quantity": 2}` {
		t.Fatalf("the editor did not write the draft: %q", d.composeBody)
	}
	// A draft restored from elsewhere replaces the editor instead of being
	// overwritten by it.
	d.composeBody = `{"quantity": 0}`
	tt.Frame()
	if d.composeBody != `{"quantity": 0}` || d.bodyEditor.Buffer.String() != `{"quantity": 0}` {
		t.Fatalf("an external body change was lost: %q / %q", d.composeBody, d.bodyEditor.Buffer.String())
	}
	for _, label := range []string{"Request body type", "Binary", "Binary body file"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	tt.Type("{{upload_path}}")
	if d.value().BodyFile != "{{upload_path}}" || d.value().BodyMode != "Binary" || !tt.HasText("Choose file…") {
		t.Fatal("the binary file editor did not update the draft")
	}
	for _, mode := range []string{"Raw", "Binary"} {
		if err := tt.Click("Request body type"); err != nil {
			t.Fatal(err)
		}
		if err := tt.Click(mode); err != nil {
			t.Fatal(err)
		}
	}
	if d.composeBody != `{"quantity": 0}` || d.bodyFile != "{{upload_path}}" {
		t.Fatal("switching body types lost the raw body or binary file path")
	}
	for _, label := range []string{"Request body type", "GraphQL", "GraphQL query"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	tt.Type(`query Search($q: String!) { search(q: $q) { id } }`)
	if err := tt.Click("Variables"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("GraphQL variables"); err != nil {
		t.Fatal(err)
	}
	tt.Type(`{"q":"{{term}}","limit":3}`)
	if err := tt.Click("Format GraphQL variables"); err != nil {
		t.Fatal(err)
	}
	wantVariables := "{\n  \"q\": \"{{term}}\",\n  \"limit\": 3\n}"
	if d.value().GraphQL.Query != `query Search($q: String!) { search(q: $q) { id } }` || d.value().GraphQL.Variables != wantVariables || d.composeBody != `{"quantity": 0}` {
		t.Fatalf("GraphQL editor changed another body or lost edits: %+v", d.value())
	}
	for _, label := range []string{"Request body type", "Raw", "Request body type", "GraphQL", "Query", "GraphQL query"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(`mutation { createItem { id } }`)
	if d.graphql.Variables != wantVariables || d.value().GraphQL.Query != `mutation { createItem { id } }` {
		t.Fatal("GraphQL editors share text or switching modes discarded edits")
	}
	// Loading another draft must replace each editor's old text when shown.
	d.graphql = request.GraphQLBody{Query: `{ viewer { name } }`, Variables: `{"id":19}`}
	tt.Frame()
	if err := tt.Click("Variables"); err != nil {
		t.Fatal(err)
	}
	if d.queryEditor.Buffer.String() != `{ viewer { name } }` || d.varsEditor.Buffer.String() != `{"id":19}` {
		t.Fatal("restored GraphQL text was overwritten by stale editor buffers")
	}
	if err := tt.Click("Query"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Format GraphQL query"); err != nil {
		t.Fatal(err)
	}
	wantQuery := "query {\n  viewer {\n    name\n  }\n}\n"
	if d.value().GraphQL.Query != wantQuery || d.queryEditor.Buffer.String() != wantQuery || d.graphql.Variables != `{"id":19}` || d.composeBody != `{"quantity": 0}` {
		t.Fatal("query formatting changed variables/raw body or was overwritten by the editor: ", d.value())
	}
	// Formatting failure leaves authored templates untouched, never expands them.
	d.graphql.Query = `query { viewer(id: {{id}}) { name } }`
	tt.Frame()
	if err := tt.Click("Format GraphQL query"); err != nil {
		t.Fatal(err)
	}
	if d.value().GraphQL.Query != `query { viewer(id: {{id}}) { name } }` || !tt.HasText("Format GraphQL query") {
		t.Fatal("invalid/template query was silently changed")
	}
}

func TestCredentialsAreMaskedUntilRevealed(t *testing.T) {
	a := workspaceApp()
	a.flows[0].reqHeader.Set("Authorization", "Bearer super-secret-token")
	// The reader prints header values, so masking is visible in its text;
	// trailers keep this capture out of the editor, which masks in a field.
	a.flows[0].reqTrailer = http.Header{"X-Done": {"1"}}
	a.inspect.tab[0], a.inspect.ready = composeTabHeaders, true
	tt := ui.NewTester(a.view, 1280, 800)
	if tt.HasText("Bearer super-secret-token") || !tt.HasText("••••••••  masked") {
		t.Fatalf("a credential was readable before it was revealed: %q", tt.Texts())
	}
	if err := tt.Click("Reveal credentials"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Bearer super-secret-token") {
		t.Fatal("reveal did not show the captured header")
	}
	// Another exchange masks its own credentials again.
	a.picked = 1
	tt.Frame()
	if tt.HasText("Bearer super-secret-token") {
		t.Fatal("the previous exchange's reveal carried over")
	}
}

func TestBodyAutomaticallyChoosesAUsableMode(t *testing.T) {
	a := workspaceApp()
	// Wide enough for the panes' own rows: the format menu of a narrow
	// pane is in its ⋯ menu, which is another test.
	a.workbench.sidePercent = 40
	a.flows[0].respHeader.Set("Content-Type", "application/octet-stream")
	a.flows[0].resBody = "\x00\xffbinary"
	openResponseBody(a)
	tt := ui.NewTester(a.view, 1280, 800)
	if !tt.HasText("Hex") {
		t.Fatal("binary body must not default to text")
	}
	chooseBodyFormat(t, tt, "response", "Hex")
	a.flows[1].respHeader = http.Header{"Content-Type": {"application/json"}}
	a.flows[1].resBody = `{"answer":42}`
	a.picked = 1
	tt.Frame()
	if !tt.HasText("Pretty") || tt.HasText("Hex") {
		t.Fatal("switching requests kept an unusable body mode")
	}
}

func TestBodyTextFindKeepsContextAndCannotEditCapture(t *testing.T) {
	a := workspaceApp()
	a.tablePercent = 25
	openResponseBody(a)
	body := "before\nneedle first\nbetween\nneedle last\nafter"
	a.flows[0].resBody = body
	a.flows[0].respHeader.Set("Content-Type", "text/plain")
	tt := ui.NewTester(a.view, 960, 700)
	if err := tt.Click("Body text"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyF)
	if !tt.Focused("Search body") {
		t.Fatal("Find in the body reader searched traffic instead")
	}
	tt.Type("needle")
	tt.Frame()
	if !tt.HasText("1 of 2") {
		t.Fatalf("search did not count matches: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if !tt.HasText("2 of 2") {
		t.Fatal("Enter did not move to the next body match")
	}
	tt.Key(ui.Shift, ui.KeyEnter)
	if !tt.HasText("1 of 2") {
		t.Fatal("Shift+Enter did not move to the previous body match")
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("absent")
	tt.Frame()
	if !tt.HasText("No results") {
		t.Fatal("an empty search failed to explain no matches")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("must not overwrite the capture")
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != body {
		t.Fatalf("read-only reader changed or lost body bytes: %q", tt.Clipboard())
	}
}

func TestBodyWarningsStayOutsideStructuredPayloadAndClipboard(t *testing.T) {
	a := workspaceApp()
	a.tablePercent = 25
	openResponseBody(a)
	a.flows[0].resBody, a.flows[0].resError = `{"z":9007199254740993,"a":false}`, "transfer stopped"
	tt := ui.NewTester(a.view, 960, 600)
	if !tt.HasText("… incomplete body: transfer stopped") {
		t.Fatal("capture error was hidden")
	}
	chooseBodyFormat(t, tt, "response", "Pretty")
	chooseBodyFormat(t, tt, "response", "JSON")
	if tt.HasText("Body is not JSON; showing it as text") || !tt.HasText("9007199254740993") {
		t.Fatal("warning text corrupted the JSON reader or it rounded the captured ID")
	}
	if err := tt.Click("Response pane options"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Copy shown body"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != `{"z":9007199254740993,"a":false}` {
		t.Fatal("copy included display warnings or reformatted the captured bytes")
	}
	// The warning belongs to the response pane; the request pane beside it
	// must not inherit it.
	shown := 0
	for _, text := range tt.Texts() {
		if strings.Contains(text, "… incomplete body: transfer stopped") {
			shown++
		}
	}
	if shown != 1 {
		t.Fatalf("the response warning appeared %d times beside the request", shown)
	}
}

func TestBodyModeSurvivesIncomingTraffic(t *testing.T) {
	a := workspaceApp()
	// Wide enough for the panes' own rows: the format menu of a narrow
	// pane is in its ⋯ menu, which is another test.
	a.workbench.sidePercent = 40
	// Each side's representation is the reader's: trailers keep the request
	// out of the editor, which has one representation of its own.
	a.flows[0].reqTrailer = http.Header{"X-Done": {"1"}}
	openResponseBody(a)
	tt := ui.NewTester(a.view, 1280, 800)
	// Hex renders differently from Auto's Pretty, so a reset is observable.
	chooseBodyFormat(t, tt, "response", "Hex")
	if !tt.HasText("Hex") {
		t.Fatal("manual body mode was not selected")
	}
	a.pending = []capture.Flow{{ID: 3, Method: "GET", Scheme: "https", Host: "other.example.com", Path: "/new", Status: 200, Kind: "http"}}
	tt.Frame()
	if !tt.HasText("Hex") {
		t.Fatal("new traffic reset the manually selected body mode")
	}
	a.pending = []capture.Flow{{ID: 4, Method: "GET", Scheme: "https", Host: "other.example.com", Path: "/another", Status: 200, Kind: "http"}}
	tt.Frame()
	if !tt.HasText("Hex") {
		t.Fatal("new traffic changed the body mode while inspecting the same request")
	}
	// Each side keeps its own representation: the request still reads as
	// Pretty while the response stays in the hex dump the reader chose.
	a.inspect.tab[0] = composeTabBody
	tt.Frame()
	if !tt.HasText("Pretty") || !tt.HasText("Hex") {
		t.Fatalf("the two sides did not keep independent representations: %q", tt.Texts())
	}
}

func TestJSONExpansionSurvivesIncomingTraffic(t *testing.T) {
	a := workspaceApp()
	// Wide enough for the panes' own rows: the format menu of a narrow
	// pane is in its ⋯ menu, which is another test.
	a.workbench.sidePercent = 40
	openResponseBody(a)
	// Keep the nested disclosure in the visible detail pane. Tester.Click
	// cannot expand an offscreen row without scrolling it into view first.
	a.tablePercent = 25
	a.flows[0].resBody = `{"collapsed":{"hidden":"hidden-value"},"data":{"nested":{"leaf":"expanded-value"}}}`
	tt := ui.NewTester(a.view, 1280, 800)
	chooseBodyFormat(t, tt, "response", "JSON") // Body's JSON mode, not Capture's JSON filter.
	if !tt.HasText("collapsed") {
		t.Fatal("JSON mode was not selected")
	}
	if err := tt.Click("Expand response JSON $.data.nested"); err != nil {
		t.Fatal(err)
	}
	// Named disclosures are native buttons: keyboard activation must keep
	// the same per-node expansion behavior as the pointer.
	tt.Key(0, ui.KeySpace)
	if tt.HasText(`"expanded-value"`) {
		t.Fatal("Space did not collapse the focused JSON node")
	}
	tt.Key(0, ui.KeyEnter)
	if err := tt.Click("Collapse response JSON $.collapsed"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText(`"expanded-value"`) || tt.HasText(`"hidden-value"`) {
		t.Fatal("JSON nodes did not expand and collapse")
	}
	// Editing the request keeps the captured response's reader as it was.
	if err := tt.Click("URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnd)
	tt.Type("?edited=1")
	if !tt.HasText(`"expanded-value"`) || tt.HasText(`"hidden-value"`) || !tt.HasText("collapsed") {
		t.Fatal("editing the request lost the reader's mode or expanded JSON nodes")
	}
	for _, id := range []int{3, 4} {
		a.pending = []capture.Flow{{ID: id, Method: "GET", Scheme: "https", Host: "other.example.com", Path: "/new", Status: 200, Kind: "http"}}
		tt.Frame()
		if !tt.HasText(`"expanded-value"`) || tt.HasText(`"hidden-value"`) {
			t.Fatal("incoming traffic reset the JSON expansion state")
		}
	}
	// Replacing a session can reuse a flow ID with different content. It must
	// refresh the tree rather than preserving stale parsed values.
	a.flows[0].resBody = `{"replacement":"new-value"}`
	a.replaceSession(a.flows, a.frames)
	tt.Frame()
	if err := tt.Click("Response pane options"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Copy shown body"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != `{"replacement":"new-value"}` || tt.HasText(`"expanded-value"`) {
		t.Fatal("changed body content did not refresh the JSON tree")
	}
}

func TestResponseDurationHoverSplitsItIntoPhases(t *testing.T) {
	a := snapshotApp()
	f := &a.flows[0]
	f.millis, f.wait, f.download = 300, 240, 60
	f.conn = conn{resolved: "203.0.113.7", lookupMs: 25, dnsFresh: true, flows: 1, tls: "TLS 1.3"}
	// The numbers, and the card on them, need a pane wide enough to print
	// them beside the tabs.
	a.workbench.sidePercent = 40
	tt := ui.NewTester(a.view, 1280, 800)
	r, ok := tt.Find("Response result")
	if !ok {
		t.Fatalf("no headline: %q", tt.Texts())
	}
	if tt.HasText("Download") {
		t.Fatal("the phases showed before hovering")
	}
	// The duration starts the headline's second line.
	tt.Move(r.X+20, r.Y+r.H-14)
	tt.Advance(time.Second)
	// The lookup is part of the wait: listed apart, the phases add up to
	// the server's time rather than counting it twice.
	for _, want := range []string{"DNS lookup", "25 ms", "Waiting", "215 ms", "Download", "60 ms", "Total", "connect and TLS are inside the wait"} {
		if !tt.HasText(want) {
			t.Fatalf("hover card lacks %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("The DNS lookup is the first part of the wait.") {
		t.Fatal("the card repeated the lookup in its note")
	}
}

func TestResponseHeaderShowsTheExchangeWithoutInventingAStatus(t *testing.T) {
	for _, tc := range []struct {
		name, title string
		status      int
		failure     string
	}{
		{"success", "200 OK", 200, ""},
		{"http-error", "500 Internal Server Error", 500, ""},
		{"no-response", "No response", 0, ""},
		{"transport-error", "Failed", 502, "connection refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := snapshotApp()
			a.flows[0].status, a.flows[0].conn.error = tc.status, tc.failure
			a.flows[0].millis, a.flows[0].bytes = 1242, 1843
			tt := ui.NewTester(a.view, 960, 800)
			if _, ok := tt.Find("Response result"); !ok || !tt.HasText(tc.title) || !tt.HasText("1242 ms") || !tt.HasText(" · 1.8 KB") {
				t.Fatalf("missing response header: %q", tt.Texts())
			}
			if tc.failure != "" {
				if tt.HasText("502 Bad Gateway") {
					t.Fatal("a transport failure was presented as an upstream HTTP response")
				}
				if err := tt.Click("Explain"); err != nil {
					t.Fatal(err)
				}
				tt.Frame() // the failure scrolls into view
				if !tt.HasText("Possible causes · not confirmed") {
					t.Fatal("the response header removed connection diagnostics")
				}
			} else if tc.status != 0 {
				count := 0
				for _, text := range tt.Texts() {
					if text == tc.title {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("response status repeated %d times", count)
				}
			}
		})
	}
}

func TestCapturedRequestOpensInItsOwnTabsAndKeepsItsOriginal(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	// The capture is the document: its parts are the pane's own tabs, and
	// the capture itself is nowhere else to go until it changes.
	if !tt.HasText("Request pane: Headers") || !tt.HasText("Request pane: Auth") {
		t.Fatalf("a capture did not open in the request's tabs: %q", tt.Texts())
	}
	if err := tt.Click("Request pane options"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Original") {
		t.Fatal("an unchanged capture offered its own original")
	}
	tt.Key(0, ui.KeyEscape)
	// Edited, the capture is what the draft came from, and More says so.
	a.editCapture(a.flows[0])
	a.composeURL += "?page=2"
	tt.Frame()
	if err := tt.Click("Request pane options"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Original") {
		t.Fatalf("an edited capture has no way back to it: %q", tt.Texts())
	}
	if err := chooseMenu(tt, "Original"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("GET /projects") || tt.HasText("?page=2") {
		t.Fatalf("the original is not the capture as recorded: %q", tt.Texts())
	}
}

func TestSourcesListsTheBusiestAndShowsTheRest(t *testing.T) {
	a := workspaceApp()
	snapshotSidebar(a, 8, 0, 0)
	tt := ui.NewTester(a.view, 1280, 800)
	// The five busiest hosts, and one row for the three the region keeps.
	// The traffic table lists the same hosts: only what is left of the
	// sidebar's edge counts.
	listed := func(name string) bool {
		r, ok := tt.Find(name)
		return ok && r.X < a.sidebarWidth
	}
	for i := 1; i <= 5; i++ {
		if host := fmt.Sprintf("service-%02d.example.com", i); !listed(host) {
			t.Fatalf("%s is not listed", host)
		}
	}
	if listed("service-06.example.com") {
		t.Fatal("Sources listed more than its first five")
	}
	if err := tt.Click("Show all domains"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !listed("service-08.example.com") {
		t.Fatal("Show all domains did not list the rest")
	}
	// The switch above the list shows the other one.
	if err := tt.Click("Apps"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !listed("Browser") {
		t.Fatalf("the Apps list did not show: %q", tt.Texts())
	}
	if listed("service-01.example.com") {
		t.Fatal("the host list stayed beside the app list")
	}
}

// TestCaptureStartsBelowFourBars pins the chrome above the traffic list: the
// title bar 40, the filter bar 32, the activity row 24 and the head 24, so the
// first request starts below them all.
func TestSidebarQuietRowsShowNoZeroCount(t *testing.T) {
	a := workspaceApp()
	// Two requests, and nothing failed, nothing was slow, nothing pinned.
	tt := ui.NewTester(a.view, 1280, 800)
	if _, ok := tt.Access("All requests 2"); !ok {
		t.Fatalf("the traffic count is missing: %v", tt.Texts())
	}
	// One request failed, so Errors keeps its count; nothing was slow or
	// pinned, so those rows are their names alone.
	if _, ok := tt.Access("Errors 1"); !ok {
		t.Fatal("a count of one is missing")
	}
	for _, row := range []string{"Slow", "Pinned"} {
		if _, ok := tt.Access(row + " 0"); ok {
			t.Fatalf("%s shows a count of nothing", row)
		}
		if _, ok := tt.Access(row); !ok {
			t.Fatalf("%s is not listed", row)
		}
	}
}

func TestSidebarRegionsFoldAndRememberIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := workspaceApp()
	a.settingsPath = path
	tt := ui.NewTester(a.view, 1280, 800)
	inSidebar := func(name string) bool {
		r, ok := tt.Find(name)
		return ok && r.X < a.sidebarWidth
	}
	if !inSidebar("api.example.com") {
		t.Fatal("Sources does not list its hosts")
	}
	clickHeading := func(tt *ui.Tester, title string) {
		t.Helper()
		r, ok := tt.Find(title)
		if !ok {
			t.Fatalf("%s has no heading", title)
		}
		// The title, not the heading's middle: Sources has its switch there.
		tt.ClickAt(r.X+20, r.Y+r.H/2)
		tt.Frame()
	}
	clickHeading(tt, "Sources")
	if *a.regionOpen("sources") || inSidebar("api.example.com") {
		t.Fatal("the heading did not fold its region away")
	}
	// The choice is the person's: it survives the next launch.
	a.lastSave = time.Time{}
	if err := a.persistSettings(); err != nil {
		t.Fatal(err)
	}
	saved, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(saved.SidebarClosed, []string{"sources"}) {
		t.Fatalf("saved regions = %v", saved.SidebarClosed)
	}
	b := workspaceApp()
	b.shutRegions = slices.Clone(saved.SidebarClosed)
	if *b.regionOpen("sources") {
		t.Fatal("the folded region opened again on the next launch")
	}
	if !*b.regionOpen("traffic") || !*b.regionOpen("collections") {
		t.Fatal("an open region started folded")
	}
	tt2 := ui.NewTester(b.view, 1280, 800)
	if r, ok := tt2.Find("api.example.com"); ok && r.X < b.sidebarWidth {
		t.Fatal("a folded Sources region still lists its hosts")
	}
	clickHeading(tt, "Sources")
	if !*a.regionOpen("sources") || !inSidebar("api.example.com") {
		t.Fatal("the heading did not bring its region back")
	}
}

func TestSidebarHidesAndComesBack(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	if !a.sidebarShown() {
		t.Fatal("the sidebar starts shown")
	}
	if err := tt.Click("Hide sidebar"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.sidebarShown() {
		t.Fatal("the title bar's switch did not hide the sidebar")
	}
	if err := tt.Click("Show sidebar"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.sidebarShown() {
		t.Fatal("the title bar's switch did not bring the sidebar back")
	}
	// A window too narrow for the sidebar puts it aside by itself, and the
	// choice it was left with comes back when there is room again.
	tt.SetSize(sidebarMinWidth-1, 800)
	tt.Frame()
	if a.sidebarShown() {
		t.Fatalf("the sidebar stayed beside a %d point window", sidebarMinWidth-1)
	}
	tt.SetSize(1280, 800)
	tt.Frame()
	if !a.sidebarShown() {
		t.Fatal("the sidebar did not come back with the window")
	}
	// Hidden by hand, it stays hidden.
	if err := tt.Click("Hide sidebar"); err != nil {
		t.Fatal(err)
	}
	tt.SetSize(sidebarMinWidth-1, 800)
	tt.Frame()
	tt.SetSize(1280, 800)
	tt.Frame()
	if a.sidebarShown() {
		t.Fatal("a hidden sidebar came back on its own")
	}
}

func TestEmptyCaptureOffersTheProxyAndClientSetup(t *testing.T) {
	a := workspaceApp()
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	a.srv = srv
	a.flows = nil
	a.recount()
	a.applyFilter()
	tt := ui.NewTester(a.view, 1280, 800)
	if !tt.HasText("Capture is stopped") {
		t.Fatalf("the empty list says something else: %v", tt.Texts())
	}
	// Setting up a client is the first thing a capture needs.
	if err := tt.Click("Client setup"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.clientUI.open {
		t.Fatal("the empty list did not open Client setup")
	}
	a.clientUI.open = false
	tt.Frame()
	// A listener that is not running can be started from here.
	if srv.Running() {
		t.Fatal("the fixture listener started on its own")
	}
	if err := tt.Click("Start proxy"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	deadline := time.Now().Add(5 * time.Second)
	for !srv.Running() {
		if time.Now().After(deadline) {
			t.Fatal("the empty list did not start the proxy")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCaptureStartsBelowFourBars(t *testing.T) {
	a := workspaceApp()
	const height = 800
	tt := ui.NewTester(a.view, 1280, height)
	search, ok := tt.Find("Search host or path")
	if !ok || search.H != uikit.HeightControl || search.Y < uikit.HeightBar {
		t.Fatalf("the filter bar's field is not a control under the title bar: %v", search)
	}
	activity, ok := tt.Find("Activity strip")
	if !ok || activity.H != uikit.HeightStatus {
		t.Fatalf("the activity row is not 24 high: %v", activity)
	}
	head, ok := tt.Find("#")
	if !ok || head.H != uikit.HeightControl {
		t.Fatalf("the table's head is not 24 high: %v", head)
	}
	want := float32(uikit.HeightBar + uikit.HeightTabs + uikit.HeightStatus + uikit.HeightControl)
	if top := head.Y + head.H; top < want {
		t.Fatalf("the first request row starts at %g, above the %g the four bars take", top, want)
	}
	if search.Y+search.H > activity.Y || activity.Y+activity.H > head.Y {
		t.Fatalf("the bars overlap: field %v, activity %v, head %v", search, activity, head)
	}
	if row, ok := tt.Find("api.example.com/projects"); !ok || row.Y < head.Y+head.H {
		t.Fatalf("the first request is not below the head: %v", row)
	}
	// The status bar closes the window with the same 24.
	if footer, ok := tt.Find("Auto-follow"); !ok || footer.Y < height-uikit.HeightStatus || footer.Y+footer.H > height {
		t.Fatalf("the status bar is not the last 24 of the window: %v", footer)
	}
}

func TestSidebarRegionsScrollApart(t *testing.T) {
	a := workspaceApp()
	// Twenty hosts above a tree of forty-two requests: neither region can
	// show all of what it holds, and neither may push the other away.
	snapshotSidebar(a, 20, 3, 14)
	a.sourcesAll = true
	tt := ui.NewTester(a.view, 1280, 800)
	collections, ok := tt.Find("Collections")
	if !ok {
		t.Fatal("the collections region is not on screen beside a long list of hosts")
	}
	if _, ok := tt.Find("Workspace"); !ok {
		t.Fatal("the tree is not on screen beside a long list of hosts")
	}
	first, ok := tt.Find("service-01.example.com")
	if !ok {
		t.Fatal("the traffic region is not on screen")
	}
	tt.Scroll(120, 300, 0, 120)
	tt.Frame()
	host, ok := tt.Find("service-01.example.com")
	if !ok || host.Y >= first.Y {
		t.Fatalf("the traffic region did not scroll: %v → %v", first.Y, host.Y)
	}
	if after, ok := tt.Find("Collections"); !ok || after.Y != collections.Y {
		t.Fatalf("scrolling the traffic region moved the collections: %v → %v", collections.Y, after.Y)
	}
	// The tree scrolls in its own region, and the hosts stay where they were.
	treeBefore, ok := tt.Find("List items 1")
	if !ok {
		t.Fatal("the tree's first row is not on screen")
	}
	tt.Scroll(120, 660, 0, 120)
	tt.Frame()
	if treeAfter, ok := tt.Find("List items 1"); !ok || treeAfter.Y >= treeBefore.Y {
		t.Fatalf("the collections region did not scroll: %v → %v", treeBefore.Y, treeAfter.Y)
	}
	if after, ok := tt.Find("service-01.example.com"); !ok || after.Y != host.Y {
		t.Fatalf("scrolling the tree moved the hosts: %v → %v", host.Y, after.Y)
	}
}

func TestNarrowResponsePaneKeepsItsTabsAndReportsTheNumbersInItsMenu(t *testing.T) {
	a := workspaceApp()
	a.flows[0].millis, a.flows[0].bytes = 1242, 1843
	// The inspector beside the list is narrow: the tabs keep the row, and the
	// numbers and the body's format menu move into the pane's menu.
	tt := ui.NewTester(a.view, 1280, 800)
	// The format menu is as wide as two tabs, so it goes while the tabs stay.
	if err := tt.Click("Response pane: Body"); err != nil {
		t.Fatalf("Body tab is not wholly visible: %v", err)
	}
	tt.Frame()
	if tt.HasText("Response body format") {
		t.Fatalf("the body format menu took the tabs' room: %q", tt.Texts())
	}
	for _, tab := range []string{"Headers", "Cookies", "Timing", "Body"} {
		if err := tt.Click("Response pane: " + tab); err != nil {
			t.Fatalf("%s tab is not wholly visible: %v", tab, err)
		}
		tt.Frame()
	}
	if tt.HasText("Response result") {
		t.Fatal("the duration and size took the tabs' room")
	}
	if err := tt.Click("Response pane options"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("1242 ms · 1.8 KB") {
		t.Fatalf("the pane's menu does not report the numbers: %q", tt.Texts())
	}
	if !tt.HasText("Response body format") {
		t.Fatalf("the pane's menu does not offer the body's formats: %q", tt.Texts())
	}
}

// openEditorResponse opens the saved request in the editor and gives it a
// response, the way a send does.
func openEditorResponse(t *testing.T, tt *ui.Tester, a *app) {
	t.Helper()
	tt.Key(ui.Cmd, ui.Key2)
	if err := tt.Click("List projects"); err != nil {
		t.Fatalf("the saved request is not reachable: %v", err)
	}
	d := a.requestDocument
	if d == nil {
		t.Fatal("the saved request did not open")
	}
	d.response = &capture.Flow{Status: 201, Millis: 1242, Bytes: 1843,
		ResponseHeader: http.Header{"Content-Type": {"application/json"}}, ResponseBody: `{"id":73}`}
	d.responseArrived()
	tt.Frame()
}

// The editor's response pane carries the body's format menu, Decode and the
// response's own actions beside its tabs. At half of a 1280 window it is
// narrower than the inspector's pane, so the numbers give way first and every
// tab still shows whole.
func TestEditorResponsePaneKeepsItsTabsBesideTheRequest(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	openEditorResponse(t, tt, a)
	for _, tab := range []string{"Body", "Headers", "Cookies", "Timing"} {
		if err := tt.Click("Response pane: " + tab); err != nil {
			t.Fatalf("%s tab is not wholly visible in the editor: %v", tab, err)
		}
		tt.Frame()
	}
	if err := tt.Click("Response pane: Body"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// The row keeps what came back, and gives up what it cannot fit: the
	// body's format menu and Decode are in the pane's menu instead.
	if !tt.HasText("1242 ms") || !tt.HasText(" · 1.8 KB") {
		t.Fatalf("the response lost its duration and size: %q", tt.Texts())
	}
	if tt.HasText("Decode…") {
		t.Fatalf("Decode took the tabs' room: %q", tt.Texts())
	}
	if err := tt.Click("Response pane options"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, item := range []string{"Decode…", "Response format"} {
		if !tt.HasText(item) {
			t.Fatalf("the pane's menu does not offer %q: %q", item, tt.Texts())
		}
	}
}

// Without the sidebar the list is wider, and the inspector beside it narrower.
// The tabs keep the row there too, and the duration column stays inside the
// list rather than losing its last letter to the edge.
func TestInspectorWithoutTheSidebarKeepsItsTabsAndDuration(t *testing.T) {
	a := workspaceApp()
	a.workbench.sidebarHidden = true
	tt := ui.NewTester(a.view, 1280, 800)
	for _, tab := range []string{"Body", "Headers", "Cookies", "Timing"} {
		if err := tt.Click("Response pane: " + tab); err != nil {
			t.Fatalf("%s tab is not wholly visible without the sidebar: %v", tab, err)
		}
		tt.Frame()
	}
	table, ok := tt.Find("Captured requests")
	duration, found := tt.Find("Duration")
	if !ok || !found {
		t.Fatal("the duration column left the list")
	}
	// The column keeps its 112 points: a column the list's edge cuts comes
	// back narrower than the one the table was built with.
	if duration.W < 110 {
		t.Fatalf("the duration column was cut to %.0f wide at %.0f, the list ends at %.0f",
			duration.W, duration.X, table.X+table.W)
	}
}

// A header value reads whole when it is a common media type: the key column
// gives way to it, and a longer value still reads in full on hover.
func TestHeaderValueColumnFitsACommonMediaType(t *testing.T) {
	a := workspaceApp()
	var needed float32
	tt := ui.NewTester(func(c *ui.Context) {
		a.view(c)
		needed, _ = c.MeasureText(0, ui.Span{Text: "application/json", Size: uikit.TextBody, Font: uikit.FontMono})
	}, 1280, 800)
	tt.Key(ui.Cmd, ui.Key2)
	if err := tt.Click("List projects"); err != nil {
		t.Fatal(err)
	}
	a.requestDocument.composeHeaders = "Accept: application/json\nContent-Type: application/json"
	a.requestDocument.headerFor = ""
	tt.Frame()
	cell, ok := tt.Find("header value 2")
	if !ok {
		t.Fatalf("the second header value is not on screen: %q", tt.Texts())
	}
	if cell.W < needed+8 {
		t.Fatalf("the value cell is %.0f wide, %q needs %.0f plus its padding", cell.W, "application/json", needed)
	}
}

// The search field names the operators while it has room for the whole
// sentence, and only what it searches when it does not. Without the mode
// dropdown the field has 94 points more to spend, so it yields later.
func TestSearchPlaceholderYieldsToANarrowField(t *testing.T) {
	for _, tc := range []struct {
		width int
		want  string
	}{{1280, locales.SearchPlaceholder()}, {600, locales.SearchHostOrPath()}} {
		a := workspaceApp()
		var got string
		tt := ui.NewTester(func(c *ui.Context) {
			a.view(c)
			got = a.searchPlaceholder(c)
		}, tc.width, 800)
		tt.Frame()
		tt.Frame()
		if got != tc.want {
			t.Errorf("width %d: placeholder %q, want %q", tc.width, got, tc.want)
		}
	}
}

// The window is one chrome surface: the title bar, the sidebar and the footer
// are the same color, and the screen floats on them as a canvas panel with a
// gutter at the window's right edge (and at its left while the sidebar is
// away).
func TestShellIsOneSurfaceAroundTheRaisedPanel(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		a := workspaceApp()
		a.workbench.sidebarHidden = hidden
		tt := ui.NewTester(a.view, 1280, 800)
		im := tt.Image()
		// The bar's own surface, clear of the listening pill at its middle
		// and of the controls at either end.
		chrome := im.RGBAAt(800, 20)
		for _, at := range []struct {
			name string
			x, y int
		}{{"title bar", 800, 20}, {"footer", 640, 790}} {
			if got := im.RGBAAt(at.x, at.y); got != chrome {
				t.Fatalf("sidebarHidden=%t: the %s is %v, not the shell's %v", hidden, at.name, got, chrome)
			}
		}
		if !hidden {
			// The sidebar is part of the shell: the brand's row, the sidebar
			// and the footer are one surface, with no seam at the top left.
			if got := im.RGBAAt(100, 300); got != chrome {
				t.Fatalf("the sidebar is %v, not the shell's %v", got, chrome)
			}
		}
		canvas := im.RGBAAt(640, 300)
		if canvas == chrome {
			t.Fatalf("sidebarHidden=%t: the panel is the shell's own color", hidden)
		}
		// The panel stops short of the window's right edge, and of its left
		// edge too once the sidebar is not there to hold it.
		if got := im.RGBAAt(1276, 300); got != chrome {
			t.Fatalf("the panel runs to the window's right edge: %v", got)
		}
		if hidden {
			if got := im.RGBAAt(4, 300); got != chrome {
				t.Fatalf("the panel runs to the window's left edge: %v", got)
			}
		}
	}
}

// The footer carries the list's own actions: Clear asks before it removes
// anything, Filter opens the filters, and Auto-follow puts the list back on
// the newest request.
func TestFooterActionsClearFilterAndFollow(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		tt.Frame()
	}
	click("Filters…")
	if !a.views.filterOpen {
		t.Fatal("the search field's funnel did not open the filters")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	click("Clear")
	if !a.clearOpen || len(a.flows) != 2 {
		t.Fatal("the footer's Clear removed traffic without asking")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	// Scrolled away from the end, Auto-follow is off and brings the list back.
	for i := range 60 {
		a.flows = append(a.flows, flow{id: 100 + i, method: "GET", scheme: "https", host: "api.example.com", path: fmt.Sprintf("/more/%d", i), url: fmt.Sprintf("https://api.example.com/more/%d", i), status: 200})
	}
	a.applyFilter()
	tt.Frame()
	table, _ := tt.Find("Captured requests")
	tt.Scroll(table.X+table.W/2, table.Y+table.H/2, 0, -1e6)
	tt.Frame()
	if a.following() {
		t.Fatal("the list still follows after scrolling to the start")
	}
	click("Auto-follow")
	if !a.following() {
		t.Fatal("the footer's Auto-follow did not return to the newest request")
	}
}

// Tools that are on sit at the footer's end; the ones the window has no room
// for fold into a count that lists them, rather than running under the edge.
func TestFooterToolChipsFoldIntoACount(t *testing.T) {
	a := workspaceApp()
	// Every tool that can be on, so the bar has to fold at the window's
	// narrowest: the footer's own Filter and counts no longer take room.
	a.breakOn, a.throttle, a.allowOn, a.upstream = true, capture.Presets[1], true, "http://127.0.0.1:9"
	a.srv = requestProxy(t)
	a.srv.SetNoCaching(true)
	snapshotProfiles(a)
	// A long profile name is what makes a bar of tools run out of room at
	// the window's narrowest.
	a.profiles.set.Profiles[0].Name = "Staging and the pre-release cluster of the platform team"
	tt := ui.NewTester(a.view, 960, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	folded := ""
	for _, text := range tt.Texts() {
		if len(text) > 1 && text[0] == '+' {
			folded = text
		}
	}
	if folded == "" {
		t.Fatalf("the footer did not fold its chips: %q", tt.Texts())
	}
	// Every chip that stayed is inside the window, and none runs under the
	// count of the rest.
	if r, ok := tt.Find("Active tool: Breakpoints"); ok && r.X+r.W > 900 {
		t.Fatalf("a chip runs past the window's edge: %v", r)
	}
	if err := tt.Click(folded); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("No Caching") {
		t.Fatalf("the folded chips are not in the count's list: %q", tt.Texts())
	}
}

// The history column steps aside as soon as the request pane could not show
// its four tabs whole beside it, and comes back when the window has room: the
// same rule as the sidebar, decided from the measured pane, not a window size.
func TestHistoryColumnYieldsToTheRequestTabs(t *testing.T) {
	for _, tc := range []struct {
		width   int
		history bool
	}{{1280, false}, {1600, true}} {
		a := workspaceApp()
		a.page = pageRequest
		a.library.root = t.TempDir()
		need := map[string]float32{}
		tt := ui.NewTester(func(c *ui.Context) {
			a.view(c)
			for _, label := range []string{"Headers", "Params", "Body", "Auth"} {
				need[label] = paneTextWidth(c, label, uikit.TextBody, 400) + 2*c.Theme().Space(1.5)
			}
		}, tc.width, 800)
		tt.SetLocale("en-US")
		if err := tt.Click("List projects"); err != nil {
			t.Fatal(err)
		}
		d := a.requestDocument
		d.response = &capture.Flow{Status: 200, Millis: 12, ResponseBody: `{}`}
		d.responseArrived()
		tt.Frame()
		tt.Frame()
		if got := tt.HasText("Request history"); got != tc.history {
			t.Fatalf("width %d: history column shown = %t, want %t", tc.width, got, tc.history)
		}
		for _, label := range []string{"Headers", "Params", "Body", "Auth"} {
			r, ok := tt.Find("Request pane: " + label)
			if !ok {
				t.Fatalf("width %d: the %s tab left the row: %q", tc.width, label, tt.Texts())
			}
			if r.W < need[label]-1 {
				t.Fatalf("width %d: the %s tab is cut to %.0f of %.0f", tc.width, label, r.W, need[label])
			}
		}
	}
}

// Every tab on the request pane's row is a whole word: a pane too narrow for
// all of them moves the last ones into More instead of fading or clipping.
func TestEditorRequestTabsStayWholeWords(t *testing.T) {
	a := workspaceApp()
	labelWidth := map[string]float32{}
	tt := ui.NewTester(func(c *ui.Context) {
		a.view(c)
		for _, label := range []string{"Headers", "Params", "Body", "Auth"} {
			labelWidth[label] = paneTextWidth(c, label, uikit.TextBody, 400) + 2*c.Theme().Space(1.5)
		}
	}, 1280, 800)
	openEditorResponse(t, tt, a)
	for label, want := range labelWidth {
		r, ok := tt.Find("Request pane: " + label)
		if !ok {
			continue // It is in More.
		}
		if r.W < want-1 {
			t.Fatalf("the %s tab is cut to %.0f of %.0f", label, r.W, want)
		}
	}
	if _, ok := tt.Find("Request pane: Headers"); !ok {
		t.Fatal("the request pane lost its first tab")
	}
	if _, ok := tt.Find("Request pane: Auth"); !ok {
		if err := tt.Click("Request pane options"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if !tt.HasText("Auth") {
			t.Fatalf("the tab that left the row is not in More: %q", tt.Texts())
		}
	}
}

func TestCaptureOrdersRequestsByNumberAndCaptureTime(t *testing.T) {
	a := workspaceApp()
	start := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	a.flows = []flow{
		{id: 41, method: "GET", scheme: "https", host: "api.example.com", path: "/first", status: 200, at: start, millis: 90},
		{id: 7, method: "GET", scheme: "https", host: "api.example.com", path: "/last", status: 200, at: start.Add(2 * time.Second), millis: 40},
		{id: 19, method: "GET", scheme: "https", host: "api.example.com", path: "/middle", status: 200, at: start.Add(time.Second), millis: 10},
	}
	a.workbench.layout = "bottom" // Time is past the side by side table's edge.
	a.applyFilter()
	tt := ui.NewTester(a.view, 1440, 860)
	for _, order := range []struct {
		column string
		paths  []string
	}{
		{"#", []string{"/last", "/middle", "/first"}},
		{"Time", []string{"/first", "/middle", "/last"}},
		{"Time", []string{"/last", "/middle", "/first"}},
	} {
		if err := tt.Click(order.column); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		previous := float32(-1)
		for _, path := range order.paths {
			row, ok := tt.Find("api.example.com" + path)
			if !ok || row.Y <= previous {
				t.Fatalf("%s did not order rows as %v", order.column, order.paths)
			}
			previous = row.Y
		}
		if !tt.HasText("GET https://api.example.com/first") {
			t.Fatal("sorting changed the inspected request")
		}
	}
}

func TestCaptureKeepsSelectionAcrossListChanges(t *testing.T) {
	a := workspaceApp()
	a.flows = nil
	for id := 1; id <= defaultFlowLimit; id++ {
		path := fmt.Sprintf("/capture/%04d", id)
		a.flows = append(a.flows, flow{id: id, method: "GET", scheme: "https", host: "api.example.com", path: path, status: 200})
	}
	a.applyFilter()
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("api.example.com/capture/4998"); err != nil {
		t.Fatal(err)
	}
	selected := "GET https://api.example.com/capture/4998"
	if !tt.HasText(selected) {
		t.Fatal("request was not selected")
	}
	a.pending = []capture.Flow{{ID: 5001, Method: "GET", Scheme: "https", Host: "api.example.com", Path: "/capture/5001", Status: 200, Kind: "http"}}
	tt.Frame()
	if !tt.HasText(selected) {
		t.Fatal("evicting the oldest request changed the inspected request")
	}
	if err := tt.Click("Path"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Path"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText(selected) {
		t.Fatal("sorting changed the inspected request")
	}
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Type("/capture/499")
	if !tt.HasText(selected) {
		t.Fatal("filtering preceding rows changed the inspected request")
	}
	if err := tt.Click("New view"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Capture view: View 1"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText(selected) {
		t.Fatal("returning to a view lost its selected request")
	}
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("/capture/4997")
	if !tt.HasText("GET https://api.example.com/capture/4997") {
		t.Fatal("hiding the selected request did not choose a surviving row")
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("no-match")
	if tt.HasText("GET https://api.example.com/capture/4997") || !tt.HasText("No matching requests") {
		t.Fatal("an empty list still inspected a removed request")
	}
	// A replacement dataset may reuse an ID remembered by an inactive view.
	// Equal URLs keep the sort stable, so the first new request is unambiguous.
	a.replaceSession([]flow{
		{id: 1, method: "POST", scheme: "https", host: "api.example.com", path: "/replacement", status: 200},
		{id: 5001, method: "GET", scheme: "https", host: "api.example.com", path: "/replacement", status: 200},
	}, nil)
	tt.Frame()
	if err := tt.Click("Capture view: View 2"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("POST https://api.example.com/replacement") {
		t.Fatal("an inactive view restored a selection from the replaced session")
	}
}

func TestDiffSplitRemembersWidth(t *testing.T) {
	a := workspaceApp()
	a.diffSplit = 61
	a.detailFor = -1
	a.syncDetail()
	if a.diff == nil || a.diff.SplitPercent != 61 {
		t.Fatalf("new diff split = %+v, want 61", a.diff)
	}
	a.diffBaseline = true
	tt := ui.NewTester(func(c *ui.Context) { a.diffPane(c, flow{}) }, 900, 600)
	tt.Frame()
	a.diff.SplitPercent = 57
	tt.Frame()
	if a.diffSplit != 57 {
		t.Fatalf("saved diff split = %v, want 57", a.diffSplit)
	}
}

func TestCaptureDetailSurvivesUnrelatedTraffic(t *testing.T) {
	a := workspaceApp()
	var before, after strings.Builder
	for line := range 60 {
		fmt.Fprintf(&before, "line %02d\n", line)
		if line == 2 || line == 50 {
			fmt.Fprintf(&after, "changed %02d\n", line)
		} else {
			fmt.Fprintf(&after, "line %02d\n", line)
		}
	}
	first := a.flows[0]
	first.resBody = before.String()
	second := first
	second.id, second.resBody, second.conn.proto = 3, after.String(), "HTTP/2"
	a.flows = []flow{first, second}
	a.inspect.pane, a.inspect.ready = inspectorDiff, true
	a.applyFilter()
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("api.example.com/projects"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown)
	a.diff.GoToHunk(1)
	tt.Frame()
	position := a.diff.CursorPos()
	if position.Line < 40 {
		t.Fatal("diff did not move to the later change")
	}
	appendFlow := func(id int) {
		t.Helper()
		a.pending = []capture.Flow{{ID: id, Method: "GET", Host: "other.example.com", Path: "/new", Kind: "http"}}
		tt.Frame()
	}
	appendFlow(4)
	if a.diff.CursorPos() != position {
		t.Fatal("incoming traffic reset the diff reading position")
	}
	if err := tt.Click("Connection details"); err != nil {
		t.Fatal(err)
	}
	appendFlow(5)
	if !tt.HasText("HTTP/2") {
		t.Fatal("incoming traffic closed the connection popover")
	}
	if err := tt.Click("Connection details"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Back to request and response"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Response pane: Body"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Response pane options"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Find in text…"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Search body"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA) // replace the word prefilled by the editor's Find
	tt.Type("changed")
	appendFlow(6)
	if !tt.Focused("Search body") {
		t.Fatal("incoming traffic took focus from the body search")
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != "changed" {
		t.Fatalf("incoming traffic reset the body search text: %q", tt.Clipboard())
	}
}

func TestCaptureFeedbackIsTransientAndComplete(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 960, 600)
	message := "Copied cURL command; review credentials before sharing it with another person or service."
	a.done(message)
	// Two notices in one frame both show; an error keeps its raw detail.
	a.fail("Save session", errors.New("disk full"))
	tt.Frame()
	if !tt.HasText(message) || !tt.HasText("Save session") || !tt.HasText("disk full") {
		t.Fatalf("feedback was truncated or overwritten: %q", tt.Texts())
	}
	tt.Advance(6 * time.Second)
	if tt.HasText(message) {
		t.Fatal("feedback stayed visible after its timeout")
	}
	if !tt.HasText("disk full") {
		t.Fatal("an error closed by itself before the user saw it")
	}
}

func TestCaptureSelectionFollowsKeysAndExcludesHiddenRows(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("api.example.com/projects"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if f, ok := a.current(); !ok || f.id != 2 || !a.chosen.Has(2) || a.chosen.Has(1) {
		t.Fatal("keyboard navigation left a different request selected for export")
	}
	tt.Key(ui.Cmd|ui.Alt, ui.KeyC)
	if !strings.HasPrefix(tt.Clipboard(), "curl ") || !strings.Contains(tt.Clipboard(), "https://api.example.com/login") {
		t.Fatal("copy cURL shortcut did not copy the focused request")
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Type("projects")
	if a.chosen.Len() != 1 || !a.chosen.Has(1) || a.chosen.Has(2) {
		t.Fatalf("filtered-out requests remained chosen for bulk export: %d chosen, holds 1: %v, holds 2: %v",
			a.chosen.Len(), a.chosen.Has(1), a.chosen.Has(2))
	}
	if err := tt.Click("More capture options"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Clear unpinned requests…"); err != nil {
		t.Fatal(err)
	}
	if len(a.flows) != 2 || !tt.HasText("Clear captured requests?") {
		t.Fatal("Clear removed traffic before confirmation")
	}
	tt.Click("Cancel")
	if len(a.flows) != 2 {
		t.Fatal("Cancel removed captured traffic")
	}
}

func TestReplayResultDoesNotStealFocusAndKeepsItsSource(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, "result of this replay")
	}))
	defer upstream.Close()
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	a := workspaceApp()
	a.srv = srv
	source := a.flows[0]
	source.url = upstream.URL + "/replay"
	if _, err := a.replayFlow(t.Context(), source, true); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1280, 800)
	if !a.page.capture() || !tt.HasText("Inspect replay") {
		t.Fatal("replay stole focus or gave no route to its result")
	}
	if err := tt.Click("Inspect replay"); err != nil {
		t.Fatal(err)
	}
	// The replay's response shows on its source capture's document, in place.
	d := a.flowDrafts[1]
	if d == nil || a.requestDocument != d || d.sourceFlow != 1 || d.response == nil || d.response.Status != 201 ||
		d.response.ResponseBody != "result of this replay" || !tt.HasText("201 Created") {
		t.Fatal("inspect did not retain the exact replay response and source")
	}
}

func TestDiffDistinguishesAnEmptyBaselineFromNoBaseline(t *testing.T) {
	a := workspaceApp()
	a.inspect.pane, a.inspect.ready = inspectorDiff, true
	tt := ui.NewTester(a.view, 960, 600)
	missing := "No previous response for this method and URL. Replay the request to compare responses."
	if !tt.HasText(missing) {
		t.Fatal("first response was presented as a meaningful diff")
	}
	a.flows = []flow{a.flows[0], a.flows[0]}
	a.flows[0].resBody, a.flows[0].status = "", 204
	a.flows[1].id = 3
	a.applyFilter()
	a.picked = 1
	tt.Frame()
	if tt.HasText(missing) || !tt.HasText("Previous response (left)") || !tt.HasText("Current response (right)") {
		t.Fatal("an empty HTTP response was incorrectly treated as no baseline")
	}
}

func TestHTTPSInstallationShowsAuthorizationAndVerifiedOutcome(t *testing.T) {
	untrusted := errors.New("test CA is not trusted")
	for _, tc := range []struct {
		name       string
		trustError error
		installErr error
		want       string
	}{
		{"trusted", nil, nil, "Trusted by this system"},
		{"not-yet-trusted", untrusted, nil, "Trust not verified on this system"},
		{"cancelled", untrusted, system.ErrInstallCancelled, "Installation cancelled. You can try again when ready."},
		{"failed", untrusted, errors.New("authorization unavailable"), "Installation failed: authorization unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := workspaceApp()
			a.settingsOpen, a.settingsTab = true, 2
			a.certificate = certificateSetup{
				cert: &x509.Certificate{}, err: untrusted,
				checked: true, checking: true, installing: true,
				pending: make(chan certificateResult, 1),
			}
			tt := ui.NewTester(a.view, 1280, 800)
			if !tt.HasText("Installing certificate…") || !tt.HasText("Trust will be checked afterwards.") {
				t.Fatal("pending installation must explain the system authorization step")
			}
			a.certificate.pending <- certificateResult{cert: &x509.Certificate{}, err: tc.trustError, installErr: tc.installErr}
			tt.Frame()
			if !tt.HasText(tc.want) || tt.HasText("Installing certificate…") {
				t.Fatalf("wrong installation outcome: %q", tt.Texts())
			}
			if tc.trustError != nil && tt.HasText("Trusted by this system") {
				t.Fatal("completing installation without verified trust must not report success")
			}
			if tc.trustError == nil {
				_ = tt.Click("Install & Trust…")
				if a.certificate.checking {
					t.Fatal("an already trusted CA must not request installation again")
				}
			}
		})
	}
}

func TestProxyPortEditorSavesWithoutInterruptingCapture(t *testing.T) {
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	addr := srv.Addr()
	a := workspaceApp()
	a.srv, a.settingsPath = srv, filepath.Join(t.TempDir(), "settings.json")
	tt := ui.NewTester(a.view, 960, 600)
	openSettingsFromBar(t, tt)
	if err := tt.Click("Proxy"); err != nil {
		t.Fatal(err)
	}
	edit := func(value string) {
		t.Helper()
		if err := tt.Click("Proxy port"); err != nil {
			t.Fatal(err)
		}
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(0, ui.KeyBackspace)
		tt.Type(value)
		if err := tt.Click("Save for next launch"); err != nil {
			t.Fatal(err)
		}
	}
	for _, invalid := range []string{"0", "80", "1023", "65536", "not a port"} {
		edit(invalid)
		got, err := loadSettings(a.settingsPath)
		if err != nil || got.ProxyPort != 0 || !tt.HasText("Enter a port from 1024 to 65535, or leave empty for automatic selection") {
			t.Fatalf("invalid input %q was not rejected: saved %+v, error %v", invalid, got, err)
		}
	}
	for _, port := range []int{1024, 65535, 9091} {
		edit(fmt.Sprint(port))
		got, err := loadSettings(a.settingsPath)
		if err != nil || got.ProxyPort != port || !tt.HasText("Restart to apply") {
			t.Fatalf("port %d not saved: %+v, error %v", port, got, err)
		}
		if !srv.Running() || srv.Addr() != addr || !tt.HasText(addr) {
			t.Fatal("saving next launch's port interrupted the current capture")
		}
	}
	// Other settings must not overwrite the newly saved port.
	a.appearance, a.lastSave = "dark", time.Time{}
	tt.Frame()
	got, err := loadSettings(a.settingsPath)
	if err != nil || got.Appearance != "dark" || got.ProxyPort != 9091 {
		t.Fatalf("subsequent settings save lost the port: %+v, %v", got, err)
	}
	path := a.settingsPath
	a.settingsPath = t.TempDir() // a directory cannot be replaced by the settings file
	edit("9092")
	if a.proxyPort != 9091 || !strings.Contains(a.proxyPortProblem, "Port was not saved") {
		t.Fatal("write failure must not claim that the new port was saved")
	}
	a.settingsPath = path
	edit("")
	got, err = loadSettings(path)
	// Back to what this run started with: nothing waits for a restart.
	if err != nil || got.ProxyPort != 0 || tt.HasText("Restart to apply") {
		t.Fatalf("clearing the port must restore automatic selection: %+v, %v", got, err)
	}
}

// A saved request's History lists only the runs linked to it, newest first;
// choosing one shows its result read-only where the response goes, without
// touching the draft or its own response, and opening it never sends.
func TestSavedRequestHistoryShowsLinkedRunsReadOnly(t *testing.T) {
	a := workspaceApp()
	a.requests[0].ID = "list"
	a.requests[0].Examples = []request.Example{{ID: "ok", Name: "Two projects", Status: 200, Body: `{"total":2}`}}
	saved := a.requests[0]
	unlinked := saved
	unlinked.ID = "" // Same method and URL, never sent from this request.
	for i, run := range []struct {
		draft  savedRequest
		status int
		body   string
	}{{saved, 201, `{"run":"first"}`}, {unlinked, 500, `{"run":"other"}`}, {saved, 404, `{"run":"latest"}`}} {
		r := snapshotExperiment(i+1, run.draft, "", request.Execution{
			Response: &capture.Flow{Method: "GET", URL: saved.URL, Status: run.status, Millis: 12, ResponseBody: run.body},
		})
		if i == 2 {
			r.Source = newDebugSource(a.flows[0])
		}
		r.At = time.Now().Add(time.Duration(i) * time.Minute)
		if err := a.experiments.log.retain(r); err != nil {
			t.Fatal(err)
		}
	}
	tt := ui.NewTester(a.view, 1280, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("List projects")
	latest, ok3 := tt.Find("Run #3")
	first, ok1 := tt.Find("Run #1")
	if _, other := tt.Find("Run #2"); !ok1 || !ok3 || other || latest.Y >= first.Y {
		t.Fatalf("history is not the linked runs, newest first: #3 %v, #1 %v, unlinked shown %t", latest, first, other)
	}
	click("History source")
	click("Capture-linked")
	if _, first := tt.Find("Run #1"); first || !tt.HasText("Run #3") {
		t.Fatal("capture source filter mixed authored attempts")
	}
	click("History status")
	click("2xx")
	if tt.HasText("Run #3") || tt.HasText("Run #1") {
		t.Fatal("status and source filters did not intersect")
	}
	click("History status")
	click("4xx")
	if !tt.HasText("Run #3") || tt.HasText("Run #2") {
		t.Fatal("status filter lost a match or admitted an unrelated request")
	}
	click("History status")
	click("All statuses")
	click("History source")
	click("All sources")
	click("Run #1")
	click("Run #1 result")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if !strings.Contains(tt.Clipboard(), `{"run":"first"}`) || !tt.HasText("Read-only") {
		t.Fatalf("the chosen run is not shown read-only: %q", tt.Clipboard())
	}
	d := a.requestDocument
	if d.response != nil || d.composeURL != saved.URL || len(a.documents) != 1 {
		t.Fatal("reading a run changed the draft or its response")
	}
	click("Show latest response")
	if !tt.HasText("Send a request to see its response") {
		t.Fatal("the request's own response did not come back")
	}
	click("Run #3")
	click("Open as draft")
	if a.requestDocument == d || a.requestDocument.assetID != "" || a.requestDocument.response == nil || a.requestDocument.response.Status != 404 {
		t.Fatal("Open as draft did not open an unsaved copy with that run's result")
	}
	click("GET  List projects") // The saved one; its copy waits under Unsaved.
	click("Examples")
	if a.requestDocument.historyTab != 1 {
		t.Fatal("Examples did not switch the history column")
	}
	click("Open example: Two projects")
	if !tt.HasText("Example name") {
		t.Fatal("choosing an example did not open it in the Examples tab")
	}
}

func TestOpenRequestsKeepDraftsAndConfirmClose(t *testing.T) {
	a := workspaceApp()
	a.library.root = filepath.Join(t.TempDir(), "collections")
	tt := ui.NewTester(a.view, 960, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(label, text string) {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type(text)
	}
	copyURL := func() string {
		t.Helper()
		click("URL")
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		return tt.Clipboard()
	}
	tt.Key(ui.Cmd, ui.Key2)
	click("Create request")
	click("Rename request")
	edit("Name", "Draft A")
	click("Done")
	edit("URL", "https://example.test/a?limit=7")
	click("Body")
	edit("Request body", `{"side":"A"}`)
	// One request shows at a time; a new one waits under Unsaved.
	click("New request")
	click("Rename request")
	edit("Name", "Draft B")
	click("Done")
	edit("URL", "https://example.test/b")
	if !tt.HasText("Unsaved: Draft A") || !tt.HasText("Unsaved: Draft B") {
		t.Fatal("new requests are not listed under Unsaved")
	}
	click("Unsaved: Draft A")
	if copyURL() != "https://example.test/a?limit=7" {
		t.Fatal("switching requests lost Draft A's URL")
	}
	click("Request body")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != `{"side":"A"}` {
		t.Fatal("switching requests lost Draft A's body or editor selection")
	}
	click("Close request")
	if !tt.HasText("Save changes?") {
		t.Fatal("dirty request closed without confirmation")
	}
	click("Cancel")
	click("Close request")
	// A failed save must not silently close or mark the draft as saved.
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a.library.root = filepath.Join(blocked, "collections")
	click("Save & close")
	if !tt.HasText("Save changes?") {
		t.Fatal("failed save discarded the draft")
	}
	a.library.root = filepath.Join(t.TempDir(), "collections")
	click("Save & close")
	list, err := loadLibraryRequests(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range list {
		if r.Name == "Draft A" && r.Body == `{"side":"A"}` && r.URL == "https://example.test/a?limit=7" {
			found = true
		}
	}
	if !found || tt.HasText("Unsaved: Draft A") || !tt.HasText("Draft A") {
		t.Fatal("save-and-close failed to persist Draft A or move it into its collection")
	}
	click("Unsaved: Draft B")
	click("Close request")
	click("Discard")
	if !tt.HasText("Open a saved request") || tt.HasText("Unsaved requests") {
		t.Fatal("closing the last request did not show the empty workspace")
	}
	click("Draft A")
	if tt.HasText("Unsaved changes") {
		t.Fatal("saved request reopened dirty")
	}
	// An edited saved request stays open, marked in its collection.
	edit("URL", "https://example.test/edited")
	click("New request")
	if !tt.HasText("Unsaved changes") {
		t.Fatal("the collection did not mark the edited request")
	}
	click("Draft A")
	if copyURL() != "https://example.test/edited" {
		t.Fatal("reopening a saved request replaced its unsaved edits")
	}
}

func TestCaptureDraftComparisonKeepsTheTrafficContextAndLastSend(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "variant=%s; marker=%s; body=%s", r.URL.Query().Get("variant"), r.Header.Get("X-Marker"), body)
	}))
	defer upstream.Close()
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	a := workspaceApp()
	a.srv = srv
	a.flows[0] = flow{id: 1, method: "POST", scheme: "http", host: strings.TrimPrefix(upstream.URL, "http://"), path: "/items?variant=original", url: upstream.URL + "/items?variant=original", status: 200,
		reqBody: "original-body", reqHeader: http.Header{"X-Marker": {"before"}}, reqHeaders: "X-Marker: before", resBody: "original response"}
	a.views.init(a, "")
	a.query, a.filterStatus, a.chip, a.tablePercent = "original", "200", 1, 35
	a.applyFilter()
	tt := ui.NewTester(a.view, 960, 900)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(label, value string) {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type(value)
	}
	copyText := func(label string) string {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		return tt.Clipboard()
	}
	waitForResponse := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		// The capture's own result says 200 OK too: wait for the send.
		done := func() bool { return !tt.HasText("Sending…") && tt.HasText("200 OK") }
		for !done() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			tt.Frame()
		}
		if !done() {
			t.Fatalf("send did not complete: %v", tt.Texts())
		}
	}
	// The captured request is the document: edit and send it in place.
	edit("URL", upstream.URL+"/items?variant=edited")
	choosePaneView(t, tt, "Request pane", "Scripts")
	edit("Pre-request script", `pm.request.headers.upsert("X-Marker", "from-script"); pm.request.body.raw = "sent-body"`)
	click("Post-response")
	edit("Post-response script", `pm.request.headers.upsert("X-Marker", "after-send"); pm.request.body.raw = "after-send"`)
	click("Send")
	waitForResponse()
	// Later edits must not change what the last send actually compared.
	edit("URL", upstream.URL+"/items?variant=not-sent")
	click("Compare with source")
	if got := copyText("Old text"); got != "original response" {
		t.Fatalf("source baseline = %q", got)
	}
	if got := copyText("New text"); got != "variant=edited; marker=from-script; body=sent-body" {
		t.Fatalf("comparison did not use this send: %q", got)
	}
	click("Compare content")
	click("Request body")
	if got := copyText("New text"); got != "sent-body" {
		t.Fatalf("compared current draft or post-response script instead of sent body: %q", got)
	}
	click("Compare content")
	click("Request headers")
	if got := copyText("New text"); !strings.Contains(got, "X-Marker: from-script") || strings.Contains(got, "after-send") {
		t.Fatalf("compared mutated headers: %q", got)
	}
	if a.query != "original" || !tt.HasText("Remove condition: status:200") || !tt.HasText("Remove condition: HTTP") || !tt.HasText("POST http://"+a.flows[0].host+"/items?variant=original") || !tt.HasText("Edited") {
		t.Fatalf("editing changed capture conditions or the inspected source: %v", tt.Texts())
	}
	// Going elsewhere and back keeps the draft, its comparison and its edits.
	tt.Key(ui.Cmd, ui.Key2)
	tt.Key(ui.Cmd, ui.Key1)
	if !tt.HasText("Back to response") || !strings.Contains(copyText("New text"), "X-Marker: from-script") {
		t.Fatal("returning to the capture lost its comparison")
	}
	click("Response pane: Body")
	if got := copyText("URL"); got != upstream.URL+"/items?variant=not-sent" {
		t.Fatalf("leaving the capture lost unsent edits: %q", got)
	}
	click("Send")
	waitForResponse()
	click("Compare with source")
	click("Compare content")
	click("Response body")
	if got := copyText("New text"); got != "variant=not-sent; marker=from-script; body=sent-body" {
		t.Fatalf("second send kept a stale comparison: %q", got)
	}
	if a.query != "original" || !tt.HasText("Remove condition: status:200") {
		t.Fatal("sending reset the live view")
	}
}

func TestConcurrentRequestTabsOwnTheirResponses(t *testing.T) {
	slowStarted, releaseSlow := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner := r.Header.Get("X-Owner")
		if owner == "slow" {
			close(slowStarted)
			select {
			case <-releaseSlow:
			case <-r.Context().Done():
				return
			}
		} else {
			w.WriteHeader(http.StatusCreated)
		}
		fmt.Fprintf(w, `{"owner":%q}`, owner)
	}))
	defer upstream.Close()
	defer close(releaseSlow)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	srv, err := capture.New(addr, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a := workspaceApp()
	a.srv = srv
	a.envs = []environment{{Name: "Local", Vars: map[string]string{"keep": "original"}}, {Name: "Staging", Vars: map[string]string{"keep": "staging"}}}
	a.activeEnv = "Local"
	a.requests = []savedRequest{
		{Name: "Slow request", Method: "GET", URL: upstream.URL, Headers: "X-Owner: slow", Post: `pm.test("slow owns response", function () { pm.expect(pm.response.json().owner).to.equal("slow") }); pm.environment.set("slowDone", "yes")`},
		{Name: "Fast request", Method: "GET", URL: upstream.URL, Headers: "X-Owner: fast", Post: `pm.test("fast owns response", function () { pm.expect(pm.response.json().owner).to.equal("fast") }); pm.environment.set("fastDone", "yes")`},
	}
	tt := ui.NewTester(a.view, 1100, 650)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	waitFor := func(label string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !tt.HasText(label) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			tt.Frame()
		}
		if !tt.HasText(label) {
			t.Fatalf("did not show %q: %v", label, tt.Texts())
		}
	}
	tt.Key(ui.Cmd, ui.Key2)
	click("Slow request")
	click("Send")
	select {
	case <-slowStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("slow request never reached the proxy")
	}
	// Editing a script during a send must not change that send's assertions.
	choosePaneView(t, tt, "Request pane", "Scripts")
	click("Post-response")
	click("Post-response script")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(`pm.test("wrong script", function () { pm.expect(false).to.be.true })`)
	click("Fast request")
	click("Send")
	waitFor("201 Created")
	choosePaneView(t, tt, "Response pane", "Checks")
	if !tt.HasText("✓ fast owns response") {
		t.Fatal("fast request ran another tab's script or saw its response")
	}
	click("Active environment")
	click("Staging")
	releaseSlow <- struct{}{}
	click("Slow request")
	waitFor("200 OK")
	choosePaneView(t, tt, "Response pane", "Checks")
	if !tt.HasText("✓ slow owns response") || tt.HasText("wrong script") {
		t.Fatal("slow result used the active tab or a script edited after sending")
	}
	click("Response pane: Body")
	click("Response body")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if !strings.Contains(tt.Clipboard(), `"slow"`) {
		t.Fatal("slow response body was not retained")
	}
	click("Fast request")
	if !tt.HasText("201 Created") || tt.HasText("200 OK") || !tt.HasText("✓ fast owns response") {
		t.Fatal("slow completion overwrote the fast tab")
	}
	click("Request options")
	if err := chooseMenu(tt, "Manage environments…"); err != nil {
		t.Fatal(err)
	}
	click("Reveal variable 1")
	click("Variable initial value 1")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != "staging" {
		t.Fatal("completion wrote variables to the newly selected environment")
	}
	click("Cancel")
	click("Active environment")
	click("Local")
	click("Request options")
	if err := chooseMenu(tt, "Manage environments…"); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"yes", "original", "yes"} {
		column := "Variable initial value %d"
		if i != 1 {
			column = "Variable current value %d"
		}
		click(fmt.Sprintf("Reveal variable %d", i+1))
		click(fmt.Sprintf(column, i+1))
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		if tt.Clipboard() != want {
			t.Fatalf("concurrent script updates lost variable %d: %q", i, tt.Clipboard())
		}
	}
	click("Cancel")
	choosePaneView(t, tt, "Request pane", "Scripts")
	click("Pre-request")
	click("Pre-request script")
	tt.Type(`throw new Error("cannot send")`)
	click("Send")
	waitFor("cannot send")
	if !tt.HasText("Pre-request script:") || !tt.HasText("cannot send") || tt.HasText("201 Created") {
		t.Fatal("a failed send hid its error behind an old response")
	}
}

func TestAPIWorkflowPreservesIndependentContracts(t *testing.T) {
	a := workspaceApp()
	a.library.root = filepath.Join(t.TempDir(), "collections")
	a.apisPath = filepath.Join(t.TempDir(), "apis.json")
	tt := ui.NewTester(a.view, 1100, 860)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(label, value string) {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.SetClipboard(value)
		tt.Key(ui.Cmd, ui.KeyV)
	}
	read := func(label string) string {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		return tt.Clipboard()
	}
	if err := tt.RightClick("api.example.com/projects"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Edit request"); err != nil {
		t.Fatal(err)
	}
	click("Save to collection")
	edit("Collection", "Captured API")
	edit("Name", "Captured projects")
	click("Save")
	tt.Key(ui.Cmd, ui.Key2)
	click("Collection options")
	if err := chooseMenu(tt, "Generate API from collection…"); err != nil {
		t.Fatal(err)
	}
	click("Generate draft")
	if !tt.HasText("Unsaved draft") || !tt.HasText("Endpoint: GET /projects") {
		t.Fatal("saved capture did not generate an API endpoint")
	}
	click("Review notes")
	if !tt.HasText("not a verified contract") {
		t.Fatal("generation concealed its contract limitations")
	}
	click("OpenAPI")
	source := read("OpenAPI source")
	click("Save API")
	if !tt.HasText("Saved locally") {
		t.Fatal("API did not persist")
	}
	edit("OpenAPI source", "{")
	click("Save API")
	if !tt.HasText("Validation failed") {
		t.Fatal("invalid source was reported as saved")
	}
	click("Collection options")
	if err := chooseMenu(tt, "New API contract"); err != nil {
		t.Fatal(err)
	}
	importedSource := strings.Replace(referencedOpenAPI, "    post:\n", "    get:\n      summary: Read widget\n      responses: {'200': {description: Read}}\n    post:\n", 1)
	edit("OpenAPI source", importedSource)
	click("Save API")
	stored, err := loadAPIs(a.apisPath)
	if err != nil || len(stored) != 2 || stored[0].Source != source || stored[1].Source != importedSource {
		t.Fatalf("saving another API wrote an invalid draft or normalized YAML: %v (%v)", stored, err)
	}
	click("API: Captured API")
	if read("OpenAPI source") != "{" {
		t.Fatal("switching specifications discarded an unsaved edit")
	}
	manual := strings.Replace(source, `"version": "1.0.0"`, `"version": "2.0.0"`, 1)
	edit("OpenAPI source", manual)
	click("Save API")
	click("Endpoints")
	click("Open in Requests")
	if read("URL") != "https://api.example.com/projects" || !tt.HasText("Source API") {
		t.Fatal("endpoint did not open as a linked, unsent request draft")
	}
	click("Captured projects")
	edit("URL", "https://api.example.com/projects-v2")
	click("Save")
	click("API: Captured API")
	if !tt.HasText("Source collection changed") {
		t.Fatal("changed requests silently diverged from their generated contract")
	}
	click("Generate new draft")
	if !tt.HasText("Endpoint: GET /projects-v2") || !tt.HasText("Unsaved draft") {
		t.Fatal("regeneration did not create a fresh draft from the saved collection")
	}
	stored, err = loadAPIs(a.apisPath)
	if err != nil || len(stored) != 2 || stored[0].Source != manual {
		t.Fatal("regeneration overwrote the hand-edited, saved specification")
	}
	click("API: Referenced API")
	click("Endpoints")
	click("Endpoint: POST /widgets/{id}")
	if operation := read("Operation schema"); !strings.Contains(operation, `"requestBody"`) {
		t.Fatal("POST operation did not show its request body")
	}
	click("Endpoint: GET /widgets/{id}")
	if operation := read("Operation schema"); strings.Contains(operation, `"requestBody"`) || !strings.Contains(operation, "Read widget") {
		t.Fatal("switching endpoints left another operation's schema visible")
	}
	click("Endpoint: POST /widgets/{id}")
	click("Open in Requests")
	if read("URL") != "https://eu.example.test/v2/widgets/{{id}}?limit={{limit}}" {
		t.Fatal("imported YAML did not create the expected request")
	}
	click("Source API")
	click("OpenAPI")
	if read("OpenAPI source") != importedSource {
		t.Fatal("following the source link changed the original YAML")
	}
}

func TestCurlImportAndCopyKeepExistingDrafts(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 960, 800) // Room for the collections below Unsaved.
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	read := func(label string) string {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		return tt.Clipboard()
	}
	paste := func(command string) {
		t.Helper()
		click("cURL command")
		tt.Key(ui.Cmd, ui.KeyA)
		tt.SetClipboard(command)
		tt.Key(ui.Cmd, ui.KeyV)
		click("Import request")
	}
	tt.Key(ui.Cmd, ui.Key2)
	click("List projects")
	click("Collection options")
	if err := chooseMenu(tt, "Import cURL…"); err != nil {
		t.Fatal(err)
	}
	paste("curl https://example.test --data @private.txt")
	if !tt.HasText("@files and stdin are not read") {
		t.Fatal("file-backed input was not explained or was imported")
	}
	click("Cancel")
	if read("URL") != "https://api.example.com/projects" {
		t.Fatal("failed import replaced the existing draft")
	}
	click("Collection options")
	if err := chooseMenu(tt, "Import cURL…"); err != nil {
		t.Fatal(err)
	}
	paste(`curl --location --request POST 'https://example.test/api?x=7' -H 'X-Count: 19' --json '{"name":"it'\''s","count":7}'`)
	if read("Method") != "POST" || read("URL") != "https://example.test/api?x=7" {
		t.Fatal("imported request fields were not shown in a new tab")
	}
	if s := a.draft().Settings; s.FollowRedirects == nil || !*s.FollowRedirects || s.RedirectMode != capture.RedirectCurl || !s.KeepMethod || a.composeWaiting || a.sentSet {
		t.Fatal("import lost redirect behavior or sent the request automatically")
	}
	click("Request pane: Headers")
	rows := a.requestDocument.headerRows
	imported := false
	for _, row := range rows {
		if row.Name == "X-Count" && row.Value == "19" {
			imported = true
		}
	}
	if !imported || len(rows) != 3 {
		t.Fatalf("imported headers were not shown as table rows: %+v", rows)
	}
	choosePaneView(t, tt, "Request pane", "Bulk edit")
	if !strings.Contains(read("Request headers"), "X-Count: 19") {
		t.Fatal("bulk edit did not show the imported header block")
	}
	click("Request pane: Body")
	if read("Request body") != `{"name":"it's","count":7}` {
		t.Fatal("import changed the body quoting")
	}
	click("Request options")
	if err := chooseMenu(tt, "Copy as cURL"); err != nil {
		t.Fatal(err)
	}
	command := tt.Clipboard()
	if !strings.Contains(command, "--data-raw") || !strings.Contains(command, "X-Count: 19") {
		t.Fatal("copy did not use the active draft")
	}
	click("List projects")
	if read("URL") != "https://api.example.com/projects" {
		t.Fatal("successful import overwrote another document")
	}
	click("Collection options")
	if err := chooseMenu(tt, "Import cURL…"); err != nil {
		t.Fatal(err)
	}
	paste(command)
	click("Body")
	if read("Request body") != `{"name":"it's","count":7}` || read("URL") != "https://example.test/api?x=7" {
		t.Fatal("copied cURL did not round-trip through the import UI")
	}
}

func TestBodyTabFormatIndentsTheDraftBody(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1200, 700)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	read := func(label string) string {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		return tt.Clipboard()
	}
	tt.Key(ui.Cmd, ui.Key2)
	click("List projects")
	click("Request pane: Body")
	click("Request body")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(`{"b":1,"a":[2,3]}`)
	if got := read("Request body"); got != `{"b":1,"a":[2,3]}` {
		t.Fatalf("typing into the body editor changed the bytes: %q", got)
	}
	click("Format body")
	formatted := read("Request body")
	if !strings.Contains(formatted, "\n  \"b\": 1") || a.requestDocument.composeBody != formatted {
		t.Fatalf("Format did not indent the draft body: %q", formatted)
	}
	// The body type shares the same row and still switches what is edited.
	click("Request body type")
	click("Multipart")
	if !tt.HasText("FORM DATA · file values are local paths, 16 MiB in total") || a.requestDocument.bodyMode != "Multipart" {
		t.Fatalf("body type did not switch to form fields: %v", tt.Texts())
	}
}

func TestHeaderCellsLineUpWithRows(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1200, 1000)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	tt.Key(ui.Cmd, ui.Key2)
	click("List projects")
	click("Request pane: Headers")
	click("Add header")
	header, ok := tt.Find("Key")
	if !ok {
		t.Fatal("the header table has no Key title")
	}
	cell, ok := tt.Find("header key 1")
	if !ok {
		t.Fatal("the header table has no first key cell")
	}
	// The title and the cell end at the same column line. A cell's border
	// leaves it a pixel inside the title, which has no inner input border.
	checkRight := func(title, cell string) {
		t.Helper()
		h, ok := tt.Find(title)
		if !ok {
			t.Fatalf("the table has no %q title", title)
		}
		c, ok := tt.Find(cell)
		if !ok {
			t.Fatalf("the table has no %q cell", cell)
		}
		if d := h.X + h.W - c.X - c.W; d < -1 || d > 1 {
			t.Errorf("the %s title ends %vpx from its cell's right edge", title, d)
		}
	}
	if d := header.X + header.W - cell.X - cell.W; d < -1 || d > 1 {
		t.Fatalf("the Key title ends %vpx from the first cell's right edge", d)
	}

	a.envs = []environment{{Name: "Local", Vars: map[string]string{"baseUrl": "https://api.example.com"}}}
	a.activeEnv = "Local"
	a.openVars()
	tt.Frame()
	checkRight("Name", "Variable name 1")
	checkRight("Initial", "Variable initial value 1")
	checkRight("Current", "Variable current value 1")

	a.varsOpen = false
	a.vault.open, a.vault.loaded = true, true
	a.vault.rows = []vaultRow{{name: "demo_token", value: "secret", domains: "api.example.com"}}
	tt.Frame()
	checkRight("NAME", "Secret name 1")
	checkRight("VALUE", "Secret value 1")
	checkRight("ALLOWED DOMAINS", "Allowed domains 1")
}

func TestHeaderTableKeepsOrderDuplicatesAndDisabledRows(t *testing.T) {
	a := workspaceApp()
	a.library.root = filepath.Join(t.TempDir(), "collections")
	tt := ui.NewTester(a.view, 1200, 1000)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(label, value string) {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type(value)
	}
	read := func(label string) string {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		return tt.Clipboard()
	}
	tt.Key(ui.Cmd, ui.Key2)
	click("List projects")
	click("Request pane: Headers")
	click("Add header")
	edit("header key 1", "X-Tag")
	edit("header value 1", "first")
	click("Add header")
	edit("header key 2", "X-Tag")
	edit("header value 2", "second")
	click("Add header")
	edit("header key 3", "X-Skip")
	edit("header value 3", "not sent")
	click("Enable header 3")
	d := a.requestDocument
	if d.composeHeaders != "X-Tag: first\nX-Tag: second" {
		t.Fatalf("the header block lost order, duplicates or kept a disabled row: %q", d.composeHeaders)
	}
	kept := d.value().HeaderList
	if len(kept) != 3 || kept[2].Name != "X-Skip" || !kept[2].Disabled {
		t.Fatalf("a disabled row was dropped from the saved request: %+v", kept)
	}
	// Bulk edit shows what is sent, and its text replaces the table.
	choosePaneView(t, tt, "Request pane", "Bulk edit")
	if got := read("Request headers"); got != "X-Tag: first\nX-Tag: second" {
		t.Fatalf("bulk edit did not show the enabled rows: %q", got)
	}
	edit("Request headers", "Accept: text/plain")
	choosePaneView(t, tt, "Request pane", "Bulk edit")
	if d.composeHeaders != "Accept: text/plain" || len(d.headerRows) != 2 || !d.headerRows[1].Disabled || d.headerRows[1].Name != "X-Skip" {
		t.Fatalf("bulk text replaced the table but lost a switched-off row: %q %+v", d.composeHeaders, d.headerRows)
	}
	// Saving keeps both the block and the rows, disabled ones included.
	click("Save")
	if _, err := os.Stat(a.library.root); err != nil {
		t.Fatal(err)
	}
	stored, err := loadLibraryRequests(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	var saved *savedRequest
	for i := range stored {
		if stored[i].Name == "List projects" {
			saved = &stored[i]
		}
	}
	if saved == nil || saved.Headers != "Accept: text/plain" || len(saved.HeaderList) != 2 || !saved.HeaderList[1].Disabled {
		t.Fatalf("saved headers lost their block, rows or a switched-off row: %+v", stored)
	}
}

func TestHeaderRowKeyboardRevealAndCustomMethod(t *testing.T) {
	a := workspaceApp()
	a.requests[0].Headers = "Authorization: Bearer sk_live_123\nX-Trace: 9f2c"
	tt := ui.NewTester(a.view, 1200, 900)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	tt.Key(ui.Cmd, ui.Key2)
	click("List projects")
	click("Request pane: Headers")
	// Ordinary and credential values occupy the same columns, including
	// after resizing; the reveal control must not move the value's start.
	for _, width := range []int{1200, 960} {
		tt.SetSize(width, 900)
		secret, secretOK := tt.Find("header value 1")
		ordinary, ordinaryOK := tt.Find("header value 2")
		if !secretOK || !ordinaryOK || secret.X != ordinary.X || secret.W != ordinary.W {
			t.Fatalf("header columns diverge at width %d: %v %v", width, secret, ordinary)
		}
	}
	click("header key 1")
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("header value 1") {
		t.Fatal("Tab did not reach the credential value")
	}
	if _, ok := tt.Find("Hide header 1"); ok {
		t.Fatal("keyboard focus revealed a credential")
	}
	// Tab reaches a header key and then its value, so a row is edited from
	// the keyboard alone.
	click("header key 2")
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("header value 2") {
		t.Fatalf("Tab from a header key did not reach its value: %q", tt.Texts())
	}
	// A credential is masked until the reader reveals that row.
	click("Reveal header 1")
	if _, ok := tt.Find("Hide header 1"); !ok {
		t.Fatal("revealing a credential row left it masked")
	}
	if got := readField(t, tt, "header value 1"); got != "Bearer sk_live_123" {
		t.Fatalf("revealed value changed: %q", got)
	}
	// The method is an editable dropdown: a method it lacks still sticks.
	click("Method")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("PURGE")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.requestDocument.composeMethod != "PURGE" {
		t.Fatalf("a typed method was reverted: %q", a.requestDocument.composeMethod)
	}
}

func TestFirstResponseMovesOnlyAnUntouchedDivider(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1200, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	tt.Key(ui.Cmd, ui.Key2)
	click("List projects")
	d := a.requestDocument
	if d.requestPercent != draftPercent {
		t.Fatalf("an unsent draft opened with %v%% for the request", d.requestPercent)
	}
	// Untouched: the first response takes the room back.
	d.responseArrived()
	if d.requestPercent != responsePercent {
		t.Fatalf("the first response left the request %v%%", d.requestPercent)
	}
	// A divider the reader moved stays where they put it.
	d.requestPercent = 70
	tt.Frame()
	d.responseArrived()
	if d.requestPercent != 70 || !d.splitTouched {
		t.Fatalf("a moved divider was reset to %v%% (touched %t)", d.requestPercent, d.splitTouched)
	}
}

func TestBulkEditAsksBeforeShowingCredentials(t *testing.T) {
	a := workspaceApp()
	a.requests[0].Headers = "Authorization: Bearer sk_live_123"
	tt := ui.NewTester(a.view, 1200, 900)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	tt.Key(ui.Cmd, ui.Key2)
	click("List projects")
	click("Request pane: Headers")
	choosePaneView(t, tt, "Request pane", "Bulk edit")
	if !tt.HasText("Bulk edit shows credentials") {
		t.Fatalf("bulk edit opened without warning about credentials: %q", tt.Texts())
	}
	click("Cancel")
	if a.requestDocument.headerBulk || !tt.HasText("header value 1") {
		t.Fatal("cancelling the warning still switched to the block")
	}
	choosePaneView(t, tt, "Request pane", "Bulk edit")
	click("Show values")
	if !a.requestDocument.headerBulk {
		t.Fatal("confirming the warning did not switch to the block")
	}
	click("Request headers")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if !strings.Contains(tt.Clipboard(), "Authorization: Bearer sk_live_123") {
		t.Fatalf("the block did not show the values it warned about: %q", tt.Clipboard())
	}
}

// readField copies a text input's value.
func readField(t *testing.T, tt *ui.Tester, label string) string {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	return tt.Clipboard()
}

func TestGraphQLRuleEditorPersistsAndPreviewsSelection(t *testing.T) {
	a := workspaceApp()
	a.rulesPath = filepath.Join(t.TempDir(), "rules.json")
	a.openTools("rules")
	a.editRule(-1, capture.Rule{Name: "Search only", Type: capture.RuleBlock, Match: "/graphql"})
	tt := ui.NewTester(a.view, 1280, 800)
	edit := func(label, value string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type(value)
	}
	edit("Match GraphQL operation", "Search*")
	if err := tt.Click("Save rule"); err != nil {
		t.Fatal(err)
	}
	if len(a.rules) != 0 || !strings.Contains(a.ruleUI.problem, "exact name") {
		t.Fatal("invalid operation rule saved: ", a.ruleUI.problem)
	}
	edit("Match GraphQL operation", "Search")
	if err := tt.Click("Save rule"); err != nil {
		t.Fatal(err)
	}
	saved, err := capture.LoadRules(a.rulesPath)
	if err != nil || len(saved) != 1 || saved[0].Operation != "Search" {
		t.Fatalf("operation lost: %+v, %v", saved, err)
	}
	edit("Test a request", "POST https://api.example.com/graphql")
	edit("Test GraphQL body", `{"query":"query Search { a } query Other { b }","operationName":"Other"}`)
	if !tt.HasText("No rule acts · it goes upstream unchanged") {
		t.Fatal("unselected operation preview matched", tt.Texts())
	}
	edit("Test GraphQL body", `{"query":"query Search { a } query Other { b }","operationName":"Search"}`)
	if !tt.HasText("1 rule acts, in the order numbered") {
		t.Fatal("selected operation preview did not match", tt.Texts())
	}
	edit("Test GraphQL body", `{"query":"query Search { a } query Other { b }"}`)
	if !tt.HasText("No rule acts · it goes upstream unchanged") || !tt.HasText("GraphQL not verified:") {
		t.Fatal("ambiguous document retained stale preview", tt.Texts())
	}
}

func TestRuleEditorPersistsOrderingTogglesAndCapturedMock(t *testing.T) {
	a := workspaceApp()
	a.rulesPath = filepath.Join(t.TempDir(), "rules.json")
	tt := ui.NewTester(a.view, 1100, 650)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(label, value string) {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type(value)
	}
	load := func() []capture.Rule {
		t.Helper()
		rules, err := capture.LoadRules(a.rulesPath)
		if err != nil {
			t.Fatal(err)
		}
		return rules
	}
	click("More")
	if err := chooseMenu(tt, "Debugging rules…"); err != nil {
		t.Fatal(err)
	}
	click("New rule")
	tt.SetSize(960, 600)
	for _, label := range []string{"Done", "Save rule"} {
		bounds, ok := tt.Find(label)
		if !ok || bounds.Y < 20 || bounds.Y+bounds.H > 580 {
			t.Fatalf("rule action %q outside the compact dialog: %v", label, bounds)
		}
	}
	tt.SetSize(1100, 650)
	click("Save rule")
	if !tt.HasText("rule 1: enter a URL to match") || len(load()) != 0 {
		t.Fatal("invalid rule was saved or validation was hidden")
	}
	edit("Rule name", "Development API")
	edit("Match URL", "/projects")
	edit("Target base URL", "http://127.0.0.1:8081")
	edit("Rewrite path from", "/v1/")
	click("Save rule")
	if !strings.Contains(a.ruleUI.problem, "a path rewrite goes from") || len(load()) != 0 {
		t.Fatalf("a rewrite without a destination was saved: %q", a.ruleUI.problem)
	}
	edit("Rewrite path to", "/api/v1/")
	tt.SetSize(1100, 1000) // the form scrolls the option out of a short window
	click("Keep the original Host header")
	tt.SetSize(1100, 650)
	click("Save rule")
	if r := load(); len(r) != 1 || r[0].PathFrom != "/v1/" || r[0].PathTo != "/api/v1/" || !r[0].KeepHost {
		t.Fatalf("Map Remote options not persisted: %+v", r)
	}
	click("New rule")
	click("Rule type")
	click("Block")
	edit("Rule name", "Block projects")
	click("Regex")
	edit("Match URL", "(projects")
	click("Save rule")
	if !strings.Contains(a.ruleUI.problem, "URL regex") || len(load()) != 1 {
		t.Fatalf("an invalid regex was saved or not explained: %q", a.ruleUI.problem)
	}
	click("Drop connection")
	click("Wildcard")
	edit("Match URL", "*/projects*")
	edit("Match apps", "curl, Google Chrome*")
	click("Add header condition")
	edit("Header 1 name", "X-Env")
	edit("Header 1 value", "staging")
	click("Save rule")
	click("Rule actions")
	if err := chooseMenu(tt, "Move up"); err != nil {
		t.Fatal(err)
	}
	if a.ruleUI.index != 0 || a.ruleUI.draft.Name != "Block projects" {
		t.Fatal("the moved rule left the editor")
	}
	click("Enable rule 1")
	rules := load()
	if len(rules) != 2 || rules[0].Type != capture.RuleBlock || !rules[0].Disabled || rules[1].Name != "Development API" {
		t.Fatalf("order/toggle not persisted: %+v", rules)
	}
	if r := rules[0]; r.Block != capture.BlockDrop || r.MatchMode != "wildcard" || r.Match != "*/projects*" || !slices.Equal(r.Apps, []string{"curl", "Google Chrome*"}) ||
		len(r.When) != 1 || r.When[0].Name != "X-Env" || r.When[0].Op != "equals" || r.When[0].Value != "staging" {
		t.Fatalf("match conditions not persisted: %+v", r)
	}
	click("Rule actions")
	if err := chooseMenu(tt, "Delete rule…"); err != nil {
		t.Fatal(err)
	}
	click("Delete")
	if rules = load(); len(rules) != 1 || rules[0].Name != "Development API" {
		t.Fatalf("delete not persisted: %+v", rules)
	}
	click("Done")
	if err := tt.RightClick("api.example.com/projects"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Tools", "Mock this response…"); err != nil {
		t.Fatal(err)
	}
	// The form scrolls: bring a field into view before using it, as a user does.
	reveal := func(label string) {
		t.Helper()
		for _, dy := range []float32{120, 120, 120, 120, -120, -120, -120, -120, -120, -120, -120, -120} {
			if b, ok := tt.Find(label); ok && b.H > 0 {
				return
			}
			name, _ := tt.Find("Rule name") // above the form, never scrolled away
			tt.Scroll(name.X+20, name.Y+name.H+60, 0, dy)
		}
		t.Fatalf("%q never scrolled into view", label)
	}
	reveal("Response status")
	edit("Response status", "201")
	click("Save rule")
	rules = load()
	if len(rules) != 2 || rules[1].Type != capture.RuleMapLocal || rules[1].Status != 201 || rules[1].Method != "GET" || rules[1].Headers.Get("Content-Type") != "application/json" {
		t.Fatalf("captured mock was not saved faithfully: %+v", rules)
	}
	body, err := os.ReadFile(rules[1].Target)
	if err != nil || string(body) != `{"projects":[]}` {
		t.Fatalf("mock body %q: %v", body, err)
	}
	// The body is edited in place and saved to the rule's file.
	reveal("Response body")
	edit("Response body", `{"projects":[{"id":1}]}`)
	click("Save rule")
	if body, err := os.ReadFile(rules[1].Target); err != nil || string(body) != `{"projects":[{"id":1}]}` {
		t.Fatalf("edited body %q: %v", body, err)
	}
	// A rule with a body but no file gets one in the mocks folder.
	click("New rule")
	reveal("Rule type")
	click("Rule type")
	click("Map Local")
	edit("Match URL", "/typed")
	reveal("Response body")
	edit("Response body", "typed")
	click("Save rule")
	rules = load()
	if len(rules) != 3 || filepath.Dir(rules[2].Target) != filepath.Join(filepath.Dir(a.rulesPath), "mocks") {
		t.Fatalf("typed body rule: %+v", rules)
	}
	if body, err := os.ReadFile(rules[2].Target); err != nil || string(body) != "typed" {
		t.Fatalf("typed body %q: %v", body, err)
	}
	click("Done")
	click("More")
	if err := chooseMenu(tt, "Debugging rules…"); err != nil {
		t.Fatal(err)
	}
	edit("Rule name", "Should not apply")
	a.rulesPath = t.TempDir() // a directory cannot be replaced by a rule file
	click("Save rule")
	if a.rules[0].Name != "Development API" || a.ruleUI.problem == "" {
		t.Fatal("failed persistence changed the active rules or hid the error")
	}
}

func TestDeviceOnboardingCertificateHTTPSAndLiveTunnelRevocation(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/socket" {
			fmt.Fprint(w, `{"source":"real TLS upstream","count":19}`)
			return
		}
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			kind, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if err := ws.WriteMessage(kind, data); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	// The pinned MITM engine issues DNS SANs, not IP SANs. Keep certificate
	// verification on and exercise a DNS hostname, as a real API would use.
	target, _ := url.Parse(upstream.URL)
	target.Host = net.JoinHostPort("localhost", target.Port())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	srv, err := capture.New(addr, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv.SetSSL(capture.SSLConfig{All: true})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a := workspaceApp()
	a.srv = srv
	defer a.disableDevices()
	tt := ui.NewTester(a.view, 1100, 650)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	openSettingsFromBar(t, tt)
	click("Settings category: Devices")
	click("Connect a device…")
	if !tt.HasText("LAN off") {
		t.Fatal("LAN access was not off by default")
	}
	// Reserve an adjacent pair while selecting ports; the product intentionally
	// requires explicit ports, whereas backend tests may use port zero.
	var port int
	for range 32 {
		first, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p := first.Addr().(*net.TCPAddr).Port
		second, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p+1))
		first.Close()
		if err == nil {
			second.Close()
			port = p
			break
		}
	}
	if port == 0 {
		t.Fatal("no adjacent ports available")
	}
	a.deviceUI.ips, a.deviceUI.ip, a.deviceUI.port = []string{"127.0.0.1"}, "127.0.0.1", fmt.Sprint(port)
	tt.Frame()
	click("Enable device access")
	if !tt.HasText("LAN off") {
		t.Fatal("access was enabled without an allowlist and consent")
	}
	click("Allowed device addresses")
	tt.Type("127.0.0.1")
	click("I trust this network and the allowed devices")
	click("Enable device access")
	if !tt.HasText("LAN enabled") {
		t.Fatalf("enable failed: %s", a.deviceUI.problem)
	}
	click("Copy proxy address")
	gatewayURL, _ := url.Parse("http://" + tt.Clipboard())
	click("Copy certificate URL")
	certificateURL := tt.Clipboard()
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(certificateURL)
	if err != nil {
		t.Fatal(err)
	}
	der, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("certificate download: status %d: %v", resp.StatusCode, err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil || !ca.IsCA {
		t.Fatalf("download was not a public CA: %v", err)
	}
	a.deviceUI.open = false
	a.openDiagnostics(nil, diagnoseDevice)
	tt.Frame()
	click("Copy CA fingerprint")
	if tt.Clipboard() != fmt.Sprintf("%X", sha256.Sum256(der)) {
		t.Fatal("displayed fingerprint does not match the downloaded certificate")
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(gatewayURL), TLSClientConfig: &tls.Config{RootCAs: pool}}}
	defer client.CloseIdleConnections()
	resp, err = client.Get(target.String() + "/projects?limit=7")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(body) != `{"source":"real TLS upstream","count":19}` {
		t.Fatalf("HTTPS through device gateway: %q: %v", body, err)
	}
	dial := func(proxyURL *url.URL) *websocket.Conn {
		t.Helper()
		d := websocket.Dialer{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: pool}, HandshakeTimeout: 5 * time.Second}
		ws, _, err := d.DialContext(t.Context(), "wss"+strings.TrimPrefix(target.String(), "https")+"/socket", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ws.Close() })
		ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		return ws
	}
	localURL, _ := url.Parse("http://" + srv.Addr())
	local, mobile := dial(localURL), dial(gatewayURL)
	echo := func(ws *websocket.Conn, message string) {
		t.Helper()
		if err := ws.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
			t.Fatal(err)
		}
		_, data, err := ws.ReadMessage()
		if err != nil || string(data) != message {
			t.Fatalf("WebSocket echo %q: %v", data, err)
		}
	}
	echo(mobile, "device tunnel before disable")
	click("More")
	if err := chooseMenu(tt, "Network", "Connect a device…"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("LAN enabled") {
		t.Fatal("closing the guide unexpectedly disabled access")
	}
	click("Android")
	if !tt.HasText("Set up Android") {
		t.Fatal("platform instructions did not switch")
	}
	click("Disable device access")
	if !tt.HasText("LAN off") {
		t.Fatal("disable did not update the guide")
	}
	_, _, err = mobile.ReadMessage()
	if err == nil {
		t.Fatal("existing device tunnel survived disabling access")
	}
	if networkErr, ok := errors.AsType[net.Error](err); ok && networkErr.Timeout() {
		t.Fatal("device tunnel timed out instead of being revoked")
	}
	if _, err := (&http.Client{Timeout: 5 * time.Second}).Get(certificateURL); err == nil {
		t.Fatal("certificate endpoint survived disabling access")
	}
	echo(local, "local tunnel survives LAN revocation")
	reply, err := srv.Replay(t.Context(), capture.Flow{Method: "GET", URL: target.String() + "/after-disable"})
	if err != nil || reply.Status != 200 || reply.ResponseBody != `{"source":"real TLS upstream","count":19}` {
		t.Fatalf("local proxy cannot send a new request after LAN revocation: %+v: %v", reply, err)
	}
}

func TestCaptureRegressionIsAnOrdinarySavedRequestWithChecks(t *testing.T) {
	a := workspaceApp()
	a.library.root = filepath.Join(t.TempDir(), "collections")
	tt := ui.NewTester(a.view, 960, 600)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.RightClick("api.example.com/projects"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Save as regression…"); err != nil {
		t.Fatal(err)
	}
	if a.page.capture() || !tt.HasText("RESPONSE CHECKS") || a.sourceFlow != 1 || a.composeWaiting {
		t.Fatal("capture did not become an unsent regression draft")
	}
	click("Save")
	loaded, err := loadLibraryRequests(a.library.root)
	if err != nil || len(loaded) != 2 || loaded[0].Checks.Status != "200" {
		t.Fatalf("checks not saved: %+v %v", loaded, err)
	}
}

func TestCaptureSelectionSummarizesAndComparesArbitraryRequests(t *testing.T) {
	a := workspaceApp()
	a.tablePercent = 45 // Keep three rows visible below both capture toolbar rows.
	a.flows[0].bytes, a.flows[0].millis = 11, 10
	a.flows[0].reqHeaders, a.flows[0].resBody = "X-Case: first", "first response"
	a.flows[1].bytes, a.flows[1].millis, a.flows[1].status = 37, 90, 500
	third := a.flows[0]
	third.id, third.path, third.url = 3, "/other", "https://api.example.com/other"
	third.bytes, third.millis, third.resError = 5, 30, "transfer stopped"
	third.reqHeaders, third.resBody = "X-Case: third", "third response"
	a.flows = append(a.flows, third)
	a.applyFilter()
	tt := ui.NewTester(a.view, 960, 600)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("api.example.com/projects")
	if err := tt.ClickWith(ui.Shift, "api.example.com/other"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"3 requests selected", "Response bytes: 53 B", "Errors: 2", "Incomplete bodies: 1", "Average: 43.3 ms · P50: 30 ms · Max: 90 ms"} {
		if !tt.HasText(want) {
			t.Fatalf("missing summary %q in %q", want, tt.Texts())
		}
	}
	click("Compare two") // Disabled for three requests.
	if tt.HasText("Swap sides") {
		t.Fatal("compared an ambiguous subset of three requests")
	}
	click("api.example.com/projects")
	if err := tt.ClickWith(ui.Cmd, "api.example.com/other"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Average: 20.0 ms · P50: 10 ms · Max: 30 ms") {
		t.Fatal("two-item P50 was not nearest rank")
	}
	click("Compare two")
	if !tt.HasText("Left #1 · GET /projects · 200 · 10 ms") || !tt.HasText("Right #3 · GET /other · 200 · 30 ms") {
		t.Fatalf("different URLs could not be compared: %q", tt.Texts())
	}
	copySide := func(side string) string {
		t.Helper()
		click(side)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		return tt.Clipboard()
	}
	if got := copySide("Old text"); got != "first response" {
		t.Fatalf("wrong left body: %q", got)
	}
	if got := copySide("New text"); got != "third response\n\n… incomplete body: transfer stopped" {
		t.Fatalf("incomplete body was presented as complete: %q", got)
	}
	click("Compare content")
	click("Request headers")
	if got := copySide("New text"); got != "X-Case: third" {
		t.Fatalf("wrong request header comparison: %q", got)
	}
	click("Swap sides")
	click("Unified")
	if !tt.HasText("Left #3 · GET /other · 200 · 30 ms") || copySide("New text") != "X-Case: first" {
		t.Fatal("swap/unified lost comparison direction")
	}
	a.pending = []capture.Flow{{ID: 4, Method: "GET", Host: "new.example.com", Path: "/incoming", Kind: "http"}}
	tt.Frame()
	if !tt.HasText("2 requests selected") || copySide("New text") != "X-Case: first" {
		t.Fatal("incoming traffic changed the comparison")
	}
	click("Inspect focused")
	if tt.HasText("2 requests selected") {
		t.Fatal("could not return from summary to one request")
	}
}

func TestReplayLinksRevealFilteredSourceAndCompareWithoutGuessingURL(t *testing.T) {
	a := workspaceApp()
	a.tablePercent = 35
	replay := a.flows[0]
	replay.id, replay.replayOf, replay.path, replay.url = 3, 1, "/redirected", "https://api.example.com/redirected"
	a.flows = append(a.flows, replay)
	a.applyFilter()
	tt := ui.NewTester(a.view, 960, 600)
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Type("redirected")
	if !tt.HasText("Replay of #1") {
		t.Fatalf("replay source link missing: %q", tt.Texts())
	}
	if err := tt.Click("Compare with source"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("2 requests selected") || !tt.HasText("Left #1 · GET /projects · 200 · 0 ms") {
		t.Fatalf("hidden source was not revealed for comparison: %q", tt.Texts())
	}
	if err := tt.Click("Summary"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Inspect #1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Replays (1)"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "#3 · /redirected · 200 · 0 ms"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Replay of #1") {
		t.Fatal("could not navigate back to replay")
	}
	if err := tt.Click("Capture view: View 1"); err != nil {
		t.Fatal(err)
	}
	if a.query != "redirected" || !tt.HasText("Replay of #1") {
		t.Fatal("following a hidden relationship erased the original view")
	}
}

func TestBatchReplaySendsChosenRequestsInOrder(t *testing.T) {
	paths := make(chan string, 3)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.Method + " " + r.URL.Path
		io.WriteString(w, "replayed")
	}))
	defer upstream.Close()
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	a := workspaceApp()
	a.srv = srv
	a.flows[0].url, a.flows[1].url = upstream.URL+"/first", upstream.URL+"/second"
	a.chosen.Add(1)
	a.chosen.Add(2)
	tt := ui.NewTester(a.view, 960, 600)
	tt.Click("More")
	if err := chooseMenu(tt, "Session", "Replay chosen…"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Replay 2 requests") {
		t.Fatal("chosen requests were not snapshotted")
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("2 selected") {
		t.Fatal("bulk actions are not visible beside the table selection")
	}
	if err := tt.Click("Replay…"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Replay 2 requests") {
		t.Fatal("contextual replay did not snapshot the full selection")
	}
	tt.Click("Replay timeout")
	tt.Key(ui.Ctrl, ui.KeyA)
	tt.Type("0")
	if err := tt.Click("Send replays"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for a.replayUI.updates != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		tt.Frame()
	}
	if !tt.HasText("2/2 started · 2 succeeded · 0 failed") || !a.page.capture() {
		t.Fatalf("batch incomplete or stole focus: %v", tt.Texts())
	}
	if first, second := <-paths, <-paths; first != "GET /first" || second != "POST /second" {
		t.Fatalf("replay order: %q %q", first, second)
	}
}

func TestTriageFiltersAndQuickPresetsStayWithTheirView(t *testing.T) {
	a := workspaceApp()
	a.views.init(a, filepath.Join(t.TempDir(), "views.json"))
	base := flow{id: 1, method: "POST", status: 503, millis: 501, resSize: 99, color: "Red", note: "Checkout regression"}
	a.flows = []flow{base}
	for i := range 5 {
		other := base
		other.id = i + 2
		switch i {
		case 0:
			other.method = "GET"
		case 1:
			other.millis = 500
		case 2:
			other.resSize = 100
		case 3:
			other.color = "Blue"
		case 4:
			other.note = "Unrelated"
		}
		a.flows = append(a.flows, other)
	}
	a.applyFilter()
	tt := ui.NewTester(a.view, 960, 600)
	tt.Click("Filters…")
	for _, field := range []struct{ label, value string }{
		{"Filter method", "post, put"}, {"Duration (ms)", ">500"}, {"Response size (bytes)", "<100"}, {"Filter note", "CHECKOUT"},
	} {
		if err := clickLabel(tt, field.label); err != nil {
			t.Fatal(err)
		}
		tt.Type(field.value)
	}
	tt.Click("Filter color")
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter) // Any → Marked → Red
	tt.Click("Apply")
	if len(a.visible) != 1 || a.flows[a.visible[0]].id != 1 {
		t.Fatalf("AND conditions or strict boundaries are wrong: %v", visibleIDs(a))
	}
	a.views.openSaved(a)
	tt.Frame()
	tt.Click("Filter name")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Checkout failures")
	tt.Click("Save filter")
	tt.Click("Close")
	filters, err := loadCaptureFilters(&store.File{Path: a.views.path})
	if err != nil || len(filters) != 1 || filters[0].Triage.Color != "Red" || filters[0].Triage.Duration != ">500" {
		t.Fatalf("triage conditions not saved: %+v %v", filters, err)
	}
	tt.Click("New view")
	tt.Frame()
	if len(a.visible) != 6 {
		t.Fatal("new view inherited triage")
	}
	if err := tt.Click("Quick filter: Checkout failures"); err != nil {
		t.Fatalf("%v; texts %q; filters %+v", err, tt.Texts(), a.views.filters)
	}
	tt.Frame()
	if len(a.visible) != 1 {
		t.Fatal("one-click filter not applied")
	}
	tt.Click("Quick filter: Checkout failures")
	tt.Frame()
	if len(a.visible) != 6 {
		t.Fatal("one-click filter could not be toggled off")
	}
	a.views.activate(a, 0)
	if len(a.visible) != 1 || a.filterTriage.Color != "Red" {
		t.Fatalf("the quick filter altered the other view: %v, %+v", visibleIDs(a), a.filterTriage)
	}
}

func TestClientSetupCopiedCommandProducesLiveDecryptionEvidence(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "verified setup result")
	}))
	defer upstream.Close()
	dir := t.TempDir()
	srv, err := capture.New("127.0.0.1:0", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.SetSSL(capture.SSLConfig{All: true})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	a := workspaceApp()
	a.srv, a.caPath, a.sslAll = srv, filepath.Join(dir, "mitmproxy-ca-cert.pem"), true
	a.openClientSetup()
	a.clientUI.tab, a.clientUI.url = 1, upstream.URL
	// An imported capture with the same URL must never prove live setup.
	a.flows = []flow{{id: 9, scheme: "https", url: upstream.URL, at: time.Now(), reqHeaders: "GET /"}}
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Copy client example"); err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), "sh", "-c", tt.Clipboard()).CombinedOutput()
	if err != nil || !strings.Contains(string(output), "verified setup result") {
		t.Fatalf("copied example did not make a trusted HTTPS request: %s %v", output, err)
	}
	select {
	case f := <-srv.Flows():
		a.pending = append(a.pending, f)
	case <-time.After(5 * time.Second):
		t.Fatal("example was not captured")
	}
	tt.Frame()
	if err := tt.Click("Diagnose…"); err != nil {
		t.Fatal(err)
	}
	// The live request (#1) is the evidence; the imported one (#9) is not.
	if !tt.HasText("#1 · GET / · 200") || tt.HasText("#9") || !tt.HasText("Trust on the client can't be checked from here; Actual traffic shows the outcome") {
		t.Fatalf("capture evidence missing or overstated trust: %v", tt.Texts())
	}
}

func TestSettingsWindowViewChangesSharedState(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.settingsView, 760, 560)
	if err := tt.Click("Settings category: Appearance"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.appearance != "dark" || a.settingsTab != 3 {
		t.Fatalf("appearance %q, tab %d", a.appearance, a.settingsTab)
	}
	// The main window draws from the same state.
	main := ui.NewTester(a.view, 1280, 800)
	main.Frame()
	if !a.dark {
		t.Fatal("the main window kept the light theme")
	}
}

func TestSetupGuideAdvancesFromWhatTheAppObserves(t *testing.T) {
	a := workspaceApp()
	a.flows = nil
	a.applyFilter()
	a.srv = requestProxy(t)
	a.guide = true
	// Never read or change this machine's proxy or trust store.
	a.systemProxy.read = func(string) system.ProxyCheck { return system.ProxyCheck{Status: system.ProxyOff} }
	a.certificate.checked = true
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if !tt.HasText("Listening at") || !tt.HasText(a.srv.Addr()) || !tt.HasText("Turn on system proxy") || tt.HasText("Install and trust") {
		t.Fatalf("step 1 is not the open step: %q", tt.Texts())
	}
	// Any captured traffic shows a client reaches the proxy, however set up.
	a.flows = []flow{{id: 1, live: true, kind: "connect", method: "CONNECT", host: "api.example.com:443", process: "Browser"}}
	a.recount()
	a.applyFilter()
	tt.Frame()
	if !tt.HasText("Install and trust") || tt.HasText("Turn on system proxy") || !tt.HasText("1 request captured so far") {
		t.Fatal("captured traffic did not finish step 1")
	}
	a.certificate.cert = &x509.Certificate{}
	tt.Frame()
	if err := tt.Click("api.example.com"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.sslHosts != "api.example.com" {
		t.Fatalf("ticking a domain did not add its rule: %q", a.sslHosts)
	}
	// Waiting for the request, the choices stay open for more ticks.
	if !tt.HasText("Rules apply to new connections") || !tt.HasText("api.example.com") {
		t.Fatal("step 4 hid the choices or its hint")
	}
	if err := tt.Click("api.example.com"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.sslHosts != "" {
		t.Fatalf("unticking kept the rule: %q", a.sslHosts)
	}
	tt.Frame()
	if err := tt.Click("api.example.com"); err != nil {
		t.Fatal(err)
	}
	// The first decrypted request ends the guide and opens its details.
	a.flows = append(a.flows, flow{id: 2, live: true, method: "GET", scheme: "https", host: "api.example.com", path: "/projects", status: 200})
	a.applyFilter()
	tt.Frame()
	tt.Frame()
	if f, ok := a.current(); a.guide || !ok || f.id != 2 || !tt.HasText("Request inspector") {
		t.Fatalf("guide %v, current %v %v", a.guide, f.id, ok)
	}
}

func TestCaptureEmptyStateOffersSetupWithoutConfusingFilteredTraffic(t *testing.T) {
	a := workspaceApp()
	a.flows = nil
	a.applyFilter()
	tt := ui.NewTester(a.view, 960, 600)
	if !tt.HasText("Capture is stopped") {
		t.Fatal("stopped listener presented as waiting for clients")
	}
	if tt.HasText("Turn on system proxy") {
		t.Fatal("stopped listener offered the system proxy")
	}
	// A stopped listener is one click from running, beside the way to set a
	// client up.
	if !tt.HasText("Start proxy") || !tt.HasText("Client setup") {
		t.Fatalf("empty state lost its setup actions: %v", tt.Texts())
	}
	a.srv = requestProxy(t)
	tt.Frame()
	if !tt.HasText("Listening at " + a.srv.Addr() + " · no retained requests yet") {
		t.Fatal("empty state omitted the actual bound address")
	}
	if tt.HasText("Start proxy") || !tt.HasText("Client setup") || !tt.HasText("Diagnose capture…") {
		t.Fatal("a running listener offered the wrong two actions")
	}
	a.flows = []flow{{id: 1, method: "GET", url: "http://example.test/"}}
	a.query = "no-match"
	a.applyFilter()
	tt.Frame()
	if !tt.HasText("No matching requests") || tt.HasText("Connect a client to start capturing") {
		t.Fatal("filtered traffic was presented as a client setup problem")
	}
}

func TestDesktopLayoutDefaultsSideBySideAndSwitchesWithoutLosingCaptureContext(t *testing.T) {
	a := workspaceApp()
	a.columns.Order = []string{"method", "url", "id"}
	tt := ui.NewTester(a.view, 1440, 860)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("api.example.com/login")
	click("Body")
	check := func(right bool) {
		t.Helper()
		table, ok := tt.Find("Captured requests")
		title, found := tt.Find("POST https://api.example.com/login")
		if !ok || !found || title.H == 0 || title.Y >= 600 || (title.X >= table.X+table.W) != right {
			t.Fatalf("inspector not reachable in expected layout: table %v, title %v, right %t", table, title, right)
		}
		if f, ok := a.current(); !ok || f.id != 2 || !a.chosen.Has(2) || a.inspect.tab[1] != resViewBody {
			t.Fatal("layout transition lost the inspected request, selection or Body tab")
		}
	}
	check(true) // Side by side is the default on wide windows.
	tt.SetSize(960, 600)
	check(false) // Too narrow: stacked, without changing the choice.
	tt.Key(ui.Cmd|ui.Shift, ui.KeyEnter)
	if !tt.HasText("Back to list") || tt.HasText("All requests") {
		t.Fatal("focus did not give the inspector the content area")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyEnter)
	check(false)
	tt.SetSize(1440, 860)
	check(true)
	// One button, labelled with the layout it switches to.
	click("Stacked layout")
	check(false)
	click("Side by side layout")
	check(true)
	click("More")
	if err := chooseMenu(tt, "View", "Layout: Stacked"); err != nil {
		t.Fatal(err)
	}
	check(false)
	tt.Key(ui.Cmd, ui.KeyB)
	if tt.HasText("All requests") {
		t.Fatal("sidebar shortcut did not hide the sidebar")
	}
	tt.Key(ui.Cmd, ui.KeyB)
	if !tt.HasText("All requests") {
		t.Fatal("sidebar shortcut did not restore the sidebar")
	}
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("Reset desktop layout")
	tt.Key(0, ui.KeyEnter)
	check(true)
	number, _ := tt.Find("#")
	status, _ := tt.Find("Status")
	method, _ := tt.Find("Method")
	host, _ := tt.Find("Host")
	path, _ := tt.Find("Path")
	if number.X >= status.X || status.X >= method.X || method.X >= host.X || host.X >= path.X {
		t.Fatal("reset layout did not restore status, method, host and path columns")
	}
}

func TestSplitDoubleClickRestoresSidebarWidth(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1440, 860)
	tt.Frame()
	tt.Press(221, 400)
	tt.Move(301, 400)
	tt.Release(301, 400)
	if a.sidebarWidth != 300 {
		t.Fatalf("sidebar width after drag = %v, want 300", a.sidebarWidth)
	}
	tt.ClickAt(301, 400)
	tt.ClickAt(301, 400)
	if a.sidebarWidth != 220 {
		t.Fatalf("sidebar width after double click = %v, want 220", a.sidebarWidth)
	}
}

// The gutter between the sidebar card and the content card is 2 DIPs, and the
// pointer finds its handle without aiming at it: a press a few DIPs into the
// content resizes the sidebar, where a divider one DIP thick would miss.
func TestSidebarDividerIsAGutterWithAWideHandle(t *testing.T) {
	if sidebarGutter != 2 {
		t.Fatalf("sidebar gutter = %v, want 2", sidebarGutter)
	}
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1440, 860)
	tt.Frame()
	edge := a.sidebarWidth
	tt.Press(edge+3, 400)
	tt.Move(edge+43, 400)
	tt.Release(edge+43, 400)
	if a.sidebarWidth != edge+40 {
		t.Fatalf("sidebar width after a drag from inside the handle = %v, want %v", a.sidebarWidth, edge+40)
	}
	// The handle reaches the other side of the gutter too, and a double click
	// there puts the sidebar back.
	tt.ClickAt(a.sidebarWidth-2, 400)
	tt.ClickAt(a.sidebarWidth-2, 400)
	if a.sidebarWidth != 220 {
		t.Fatalf("sidebar width after double click = %v, want 220", a.sidebarWidth)
	}
}

func TestSplitDoubleClickRestoresInspectorSplitters(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1440, 860)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("api.example.com/login")
	click("Body")

	// Side by side: the table takes 64% of the content beside the inspector.
	table, _ := tt.Find("Captured requests")
	x := table.X + table.W + 1
	tt.Press(x, 400)
	tt.Move(x+100, 400)
	tt.Release(x+100, 400)
	if a.workbench.sidePercent == 64 {
		t.Fatal("drag did not move the side-by-side split")
	}
	tt.ClickAt(x+100, 400)
	tt.ClickAt(x+100, 400)
	if a.workbench.sidePercent != 64 {
		t.Fatalf("side percent after double click = %v, want 64", a.workbench.sidePercent)
	}

	// Stacked: the fixture starts at 56%, while the app's default is 44%.
	click("Stacked layout")
	table, _ = tt.Find("Captured requests")
	y := table.Y + table.H + 1
	tt.Press(400, y)
	tt.Move(400, y+100)
	tt.Release(400, y+100)
	if a.tablePercent == 44 {
		t.Fatal("drag did not move the stacked split")
	}
	tt.ClickAt(400, y+100)
	tt.ClickAt(400, y+100)
	if a.tablePercent != 44 {
		t.Fatalf("table percent after double click = %v, want 44", a.tablePercent)
	}
}

func TestCaptureSelectionMarkerFollowsListAndWindowFocus(t *testing.T) {
	// Expected colors are the design's Paper/Ink values, not read from
	// uikit: accent tint fill and accent marker while the list has the
	// focus, the neutral control fill and a faint marker otherwise. Text
	// never turns white.
	type look struct{ fill, marker [3]int }
	looks := map[bool][2]look{
		false: {{[3]int{232, 236, 230}, [3]int{138, 143, 136}}, {[3]int{214, 220, 214}, [3]int{20, 22, 20}}},
		true:  {{[3]int{28, 37, 38}, [3]int{109, 118, 110}}, {[3]int{47, 55, 47}, [3]int{234, 243, 233}}},
	}
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			a := workspaceApp()
			a.appearance = appearanceOf(dark)
			tt := ui.NewTester(a.view, 1280, 800)
			if err := tt.Click("api.example.com/projects"); err != nil {
				t.Fatal(err)
			}
			check := func(focused bool) {
				t.Helper()
				table, ok := tt.Find("Captured requests")
				row, found := tt.Find("api.example.com/projects")
				if !ok || !found {
					t.Fatal("selected traffic row disappeared")
				}
				want := looks[dark][0]
				if focused {
					want = looks[dark][1]
				}
				im := tt.Image()
				y := int(row.Y + row.H/2)
				// Sample empty space at the row's end, and the marker at its
				// leading edge, not font antialiasing.
				for _, sample := range []struct {
					name string
					x    int
					want [3]int
				}{{"fill", int(table.X + table.W - 5), want.fill}, {"marker", int(table.X + 1), want.marker}} {
					pixel := im.RGBAAt(sample.x, y)
					for i, got := range [3]int{int(pixel.R), int(pixel.G), int(pixel.B)} {
						if got < sample.want[i]-2 || got > sample.want[i]+2 {
							t.Fatalf("focused=%t: %s pixel %v, want RGB %v", focused, sample.name, pixel, sample.want)
						}
					}
				}
			}
			check(true)
			tt.SetFocused(false)
			check(false)
			tt.SetFocused(true)
			check(true)
			tt.Key(ui.Cmd, ui.KeyF)
			if !tt.Focused("Search host or path") {
				t.Fatal("search shortcut did not move focus out of the list")
			}
			check(false)
		})
	}
}

func TestCommandPaletteKeyboardAndDisabledActions(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 960, 600)
	tt.Key(ui.Cmd, ui.KeyK)
	if !tt.Focused("Find command") {
		t.Fatal("palette search did not take focus")
	}
	tt.Type("Layout:")
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if tt.HasText("Find command") {
		t.Fatal("executing a command did not close the palette")
	}
	// Check the visible effect on a wide window, not the command's storage.
	tt.SetSize(1440, 860)
	method, _ := tt.Find("Method")
	title, _ := tt.Find("GET https://api.example.com/projects")
	if title.Y <= method.Y+100 {
		t.Fatalf("arrow navigation did not choose Layout: Stacked: layout %q, command %d, method %v, title %v", a.workbench.layout, a.workbench.command, method, title)
	}
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("not a command")
	if !tt.HasText("No matching commands") {
		t.Fatal("empty command results were not explained")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Key(ui.Cmd, ui.Key2)
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("Replay selected")
	tt.Key(0, ui.KeyEnter)
	if !tt.HasText("Find command") || tt.HasText("Replay requests") {
		t.Fatal("unavailable capture action executed in Requests workspace")
	}
	tt.Key(0, ui.KeyEscape)
	if tt.HasText("Find command") {
		t.Fatal("Escape did not dismiss the palette")
	}
}

func TestKeyboardDebuggingLoopDoesNotSendThroughDialogsOrRepeatWhileWaiting(t *testing.T) {
	var sends atomic.Int32
	started, release := make(chan string, 2), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		started <- r.Method + " " + r.URL.Path
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "keyboard response")
	}))
	defer upstream.Close()
	defer close(release)
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	a := workspaceApp()
	a.srv = srv
	a.flows[1].reqHeader = http.Header{"Accept": {"application/json"}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Type("api.example.com")
	tt.Key(0, ui.KeyEnter)
	if !tt.Focused("Captured requests") {
		t.Fatal("search submission did not move focus to the request list")
	}
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if !tt.Focused("Request inspector") || !tt.HasText("POST https://api.example.com/login") {
		t.Fatal("list Enter did not inspect the focused request")
	}
	tt.Key(0, ui.KeyEscape)
	if !tt.Focused("Captured requests") {
		t.Fatal("inspector Escape did not restore list focus")
	}
	tt.Key(ui.Cmd|ui.Alt, ui.KeyEnter)
	if !tt.Focused("URL") {
		t.Fatal("editing a capture did not focus the draft URL")
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(upstream.URL + "/keyboard")
	tt.Key(0, ui.KeyEnter)
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("Compare with source")
	tt.Key(0, ui.KeyEnter)
	if !tt.HasText("Send a draft opened from Capture before comparing it with its source") {
		t.Fatal("unavailable comparison did not explain its prerequisite")
	}
	tt.Key(ui.Cmd, ui.KeyEnter)
	if sends.Load() != 0 || !tt.Focused("Find command") {
		t.Fatal("Enter or a shortcut through the palette sent a request")
	}
	tt.Key(0, ui.KeyEscape)
	if !tt.Focused("URL") {
		t.Fatal("dismissing the palette lost the editor's focus")
	}
	// The capture's tabs are one Tab stop away, and the arrows choose among
	// them.
	tab := func(label string) {
		t.Helper()
		for range 20 {
			if tt.Focused(label) {
				return
			}
			tt.Key(0, ui.KeyTab)
		}
		t.Fatalf("%s was not keyboard accessible", label)
	}
	tab("Request pane: Headers")
	tab("header key 1")
	tt.Key(ui.Cmd, ui.KeyEnter)
	select {
	case got := <-started:
		if got != "POST /keyboard" {
			t.Fatalf("sent the wrong draft: %s", got)
		}
	case <-time.After(time.Minute):
		t.Fatal("send never reached the server")
	}
	tt.Key(ui.Cmd, ui.KeyEnter)
	if !tt.HasText("Sending…") || sends.Load() != 1 {
		t.Fatal("in-flight send could be repeated")
	}
	release <- struct{}{}
	deadline := time.Now().Add(time.Minute)
	done := func() bool { return !tt.HasText("Sending…") && tt.HasText("Last attempt") }
	for !done() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		tt.Frame()
	}
	if !done() || sends.Load() != 1 {
		t.Fatalf("send did not complete once: %v", tt.Texts())
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyD)
	if !tt.HasText("Left Source #2") || !tt.HasText("Right Last send") {
		t.Fatal("keyboard comparison did not show the exact source and send")
	}
	// The edited capture stays in its row: Escape returns to the list, and
	// Enter opens the same document, comparison and all.
	tt.Key(0, ui.KeyEscape)
	if !tt.Focused("Captured requests") || a.query != "api.example.com" || !tt.HasText("POST https://api.example.com/login") || !tt.HasText("Edited") {
		t.Fatal("keyboard return lost capture focus, filter or the edited source")
	}
	tt.Key(0, ui.KeyEnter)
	if !tt.Focused("Request inspector") || !tt.HasText("Right Last send") {
		t.Fatal("returning to the capture lost its comparison")
	}
}

func TestReplayTargetsAndUnavailableActionsAreConsistentAcrossEntries(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1100, 720)
	if err := tt.Click("api.example.com/projects"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Shift, ui.KeyDown)
	open := func(entry string) {
		t.Helper()
		switch entry {
		case "keyboard":
			tt.Key(ui.Cmd, ui.KeyR)
		case "palette":
			tt.Key(ui.Cmd, ui.KeyK)
			tt.Type("Replay selected")
			tt.Key(0, ui.KeyEnter)
		case "row":
			if err := tt.RightClick("api.example.com/projects"); err != nil {
				t.Fatal(err)
			}
			if err := chooseMenu(tt, "Replay"); err != nil {
				t.Fatal(err)
			}
		case "toolbar":
			if err := tt.Click("Replay…"); err != nil {
				t.Fatal(err)
			}
		}
		if !tt.HasText("Replay 2 requests") {
			t.Fatalf("%s did not target the whole selection: %v", entry, tt.Texts())
		}
		tt.Key(0, ui.KeyEscape)
	}
	for _, entry := range []string{"keyboard", "palette", "row", "toolbar"} {
		open(entry)
	}
	tt.Key(ui.Cmd|ui.Alt, ui.KeyEnter)
	if tt.HasText("Source: Capture") {
		t.Fatal("bulk edit silently chose a single request")
	}
	a.flows[1].kind = "connect"
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyR)
	if tt.HasText("Replay 2 requests") {
		t.Fatal("mixed tunnel selection silently replayed only the HTTP subset")
	}
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("Replay selected")
	if !tt.HasText("Choose HTTP requests only; tunnels and streams cannot be replayed") {
		t.Fatal("mixed selection did not explain why replay is unavailable")
	}
}

func TestNativeCommandsRespectModalScopeAndDraftCloseProtection(t *testing.T) {
	a := workspaceApp()
	menu := a.menu()
	tt := ui.NewTester(a.view, 1100, 720)
	choose := func(id string) {
		t.Helper()
		item := menu.ItemByID(id)
		if item == nil || !item.IsEnabled() {
			t.Fatalf("native command %s unavailable", id)
		}
		item.Click(item, nil)
		tt.Frame()
	}
	choose("flow.edit")
	if !tt.Focused("URL") {
		t.Fatal("native edit did not open the focused capture")
	}
	if item := menu.ItemByID("request.close"); item.IsEnabled() {
		t.Fatal("a capture's document is not a tab to close")
	}
	choose("request.new")
	tt.Type("https://draft.example.com")
	choose("request.close")
	if !tt.HasText("Save changes?") {
		t.Fatal("native close bypassed unsaved draft protection")
	}
	// A native callback queued before the focus changed must be rejected too.
	item := menu.ItemByID("request.new")
	item.Click(item, nil)
	tt.Frame()
	if len(a.documents) != 1 || !tt.HasText("Save changes?") {
		t.Fatal("native command escaped the modal scope")
	}
	tt.Key(0, ui.KeyEscape)
	if !tt.Focused("URL") {
		t.Fatal("native close cancellation did not restore draft focus")
	}
}

func TestCaptureHostColumnSortsByHostNotScheme(t *testing.T) {
	a := workspaceApp()
	a.flows[0].host, a.flows[0].url = "a.test", "https://a.test/projects"
	a.flows[1].host, a.flows[1].url, a.flows[1].scheme = "z.test", "http://z.test/login", "http"
	tt := ui.NewTester(a.view, 1280, 800)
	for _, descending := range []bool{false, true} {
		if err := tt.Click("Host"); err != nil {
			t.Fatal(err)
		}
		first, ok := tt.Find("a.test/projects")
		second, found := tt.Find("z.test/login")
		if !ok || !found || (first.Y > second.Y) != descending {
			t.Fatalf("Host used URL scheme order: %v, %v; descending %t", first, second, descending)
		}
	}
}

func TestCaptureCompactLayoutKeepsSearchUsableAcrossEmptyResults(t *testing.T) {
	for _, width := range []int{960, 1280} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := workspaceApp()
			tt := ui.NewTester(a.view, width, 800)
			first, _ := tt.Find("api.example.com/projects")
			second, _ := tt.Find("api.example.com/login")
			if second.Y-first.Y != 24 {
				t.Fatalf("traffic rows are not 24px: %v, %v", first, second)
			}
			search, _ := tt.Find("Search host or path")
			for _, label := range []string{"Protocol and content type: All", "Filters…", "Breakpoints", "More capture options"} {
				control, ok := tt.Find(label)
				if !ok || control.Y >= search.Y+search.H || control.Y+control.H <= search.Y {
					t.Fatalf("%s escaped the single toolbar row: %v", label, control)
				}
			}
			if err := tt.Click("Search host or path"); err != nil {
				t.Fatal(err)
			}
			tt.Type("no-match")
			if !tt.Focused("Search host or path") || !tt.HasText("No matching requests") || tt.HasText("Request inspector") {
				t.Fatal("empty results lost input focus or left the full inspector open")
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Type("login")
			if !tt.Focused("Search host or path") || len(a.visible) != 1 || !tt.HasText("POST https://api.example.com/login") {
				t.Fatal("search could not restore the table and matching inspector")
			}
			if err := tt.Click("More capture options"); err != nil {
				t.Fatal(err)
			}
			if err := chooseMenu(tt, "Images"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if !tt.HasText("Remove condition: Images") || len(a.visible) != 0 {
				t.Fatal("the segmented control overwrote the menu-only Images condition")
			}
		})
	}
}

func TestRequestCompactControlsKeepTemplateEditingAndCounts(t *testing.T) {
	for _, width := range []int{960, 1280} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := snapshotApp()
			a.addDocument(savedRequest{Method: "PATCH", URL: "https://例.test/{{path}}?tag=a&tag=b", HeaderList: []request.Field{
				{Name: "X-Tag", Value: "one"}, {Name: "X-Tag", Value: "two"}, {Name: "Accept", Value: "text/plain"},
				{Name: "X-Off", Value: "ignored", Disabled: true},
			}, Checks: request.Checks{Status: "201", Headers: "X-One: one\nX-Two: two", JSON: "/ok = true"}}, "")
			a.composeTab = composeTabHeaders
			tt := ui.NewTester(a.view, width, 800)
			click := func(label string) {
				t.Helper()
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			method, _ := tt.Find("Method")
			urlBox, _ := tt.Find("URL")
			send, _ := tt.Find("Send")
			if send.H != uikit.HeightLarge || method.Y >= urlBox.Y+urlBox.H || send.Y >= urlBox.Y+urlBox.H {
				t.Fatal("method, URL and 28px Send no longer share one row")
			}
			tab, ok := tt.Find("Request pane: Headers")
			number, found := tt.Find("3")
			if !ok || !found || number.X < tab.X || number.X+number.W > tab.X+tab.W || number.Y < tab.Y || number.Y+number.H > tab.Y+tab.H {
				t.Fatalf("Headers did not show its configured count: tab %v, number %v", tab, number)
			}
			// Checks is a view from More, and its count goes with it.
			choosePaneView(t, tt, "Request pane", "Checks 4")
			if a.composeTab != composeTabChecks {
				t.Fatal("the Checks view is not reachable from More")
			}
			click("Request pane: Headers")
			click("URL")
			tt.Key(ui.Cmd, ui.KeyA)
			raw := "https://例.test/中文/{{path}}?secret={{vault:token}}&unfinished={{name"
			tt.Type(raw)
			click("Request pane: Headers") // Blur shows chips without replacing the input.
			click("URL")
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Key(ui.Cmd, ui.KeyC)
			if tt.Clipboard() != raw {
				t.Fatal("URL chips changed Unicode, reference braces or incomplete input")
			}
			tt.Key(0, ui.KeyEnd)
			tt.Type("z")
			click("Active environment")
			click("Staging")
			click("URL")
			tt.Key(ui.Cmd, ui.KeyZ)
			if a.composeURL != raw || a.activeEnv != "Staging" || !tt.Focused("URL") {
				t.Fatal("environment selection or chip rendering lost native undo/focus")
			}
			a.requestDocument.setDebugSource(newDebugSource(a.flows[0]))
			a.response = &capture.Flow{Status: 201, Millis: 47, Bytes: 43, ResponseHeader: http.Header{"Content-Type": {"application/json"}, "X-Tag": {"first", "second"}}, ResponseBody: `{"id":73}`}
			a.composeTests = []request.TestResult{{Name: "Created", Pass: true}, {Name: "Active", Pass: false}}
			a.responseArrived()
			tt.Frame()
			if _, ok := tt.Find("Response result"); !ok || !tt.HasText("201 Created") || !tt.HasText("47 ms") || !tt.HasText(" · 43 B") {
				t.Fatalf("sent response lost its status or metadata: %q", tt.Texts())
			}
			choosePaneView(t, tt, "Response pane", "Console")
			if a.responseTab != resViewConsole {
				t.Fatal("the console view is not reachable from More")
			}
		})
	}
}

func TestFilterTokensRemoveOnlyTheirOwnCondition(t *testing.T) {
	a := workspaceApp()
	a.views.init(a, "")
	a.query, a.filterStatus = "login", "4xx"
	a.filterTriage.Method = "POST"
	a.applyFilter()
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Remove condition: status:4xx"); err != nil {
		t.Fatal(err)
	}
	if a.query != "login" || !tt.HasText("Remove condition: method:POST") || tt.HasText("status:4xx") || len(a.visible) != 1 {
		t.Fatal("removing status cleared other conditions or left a stale token")
	}
	if err := tt.Click("Reset all"); err != nil {
		t.Fatal(err)
	}
	if len(a.visible) != 2 || a.query != "" || tt.HasText("method:POST") {
		t.Fatal("reset did not remove all capture conditions")
	}
	// Full-body conditions live on the view, not the URL search model.
	a.views.activeView().body = "projects"
	a.views.applyFilter(a)
	settle(t, a, &a.views)
	tt.Frame()
	if len(a.visible) != 1 {
		t.Fatal("body fixture did not narrow the view")
	}
	if err := tt.Click("Remove condition: body:projects"); err != nil {
		t.Fatal(err)
	}
	if len(a.visible) != 2 || a.filtersActive() {
		t.Fatal("body token did not remove the full-body search condition")
	}
}

// A pattern is written where the text is: slashes make a regular expression
// and * or ? a wildcard, so the field needs no mode to say which it is.
func TestCapturePatternSearchSyntax(t *testing.T) {
	for _, width := range []int{960, 1280} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := workspaceApp()
			tt := ui.NewTester(a.view, width, 600)
			click := func(label string) {
				t.Helper()
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			edit := func(text string) {
				t.Helper()
				click("Search host or path")
				tt.Key(ui.Cmd, ui.KeyA)
				tt.Type(text)
				tt.Frame()
			}
			edit(`/(projects|LOGIN)$/`)
			if len(a.visible) != 2 {
				t.Fatal("a case-insensitive regular expression did not apply")
			}
			edit(`/projects/ status:4xx`)
			tt.Key(0, ui.KeyEnter)
			tt.Frame()
			if a.query != "/projects/" || a.filterStatus != "4xx" || len(a.visible) != 0 {
				t.Fatalf("a pattern beside a chip: query %q, status %q", a.query, a.filterStatus)
			}
			click("Reset all")
			edit("/[/")
			tt.Key(0, ui.KeyEnter)
			if !tt.HasText("Invalid search pattern") || len(a.visible) != 0 || !tt.Focused("Search host or path") {
				t.Fatal("an invalid regular expression was not explained or stole editing focus")
			}
			edit("/LOGIN$/")
			if a.searchProblem != "" || len(a.visible) != 1 {
				t.Fatal("correcting the regular expression left a stale error or matcher")
			}
			edit("*/login")
			if len(a.visible) != 1 {
				t.Fatal("a wildcard did not match the whole host and path")
			}
			a.filterStatus = "4xx"
			tt.Frame()
			// A chip turns back into text at the end of the field, where a
			// pattern cannot swallow it.
			click("Edit condition: status:4xx")
			if a.views.filterOpen || a.filterStatus != "" || a.query != "*/login status:4xx" {
				t.Fatalf("editing a condition: dialog %t, status %q, query %q", a.views.filterOpen, a.filterStatus, a.query)
			}
			click("Reset all")
			if a.query != "" || a.filtersActive() || len(a.visible) != 2 {
				t.Fatal("reset did not clear the field")
			}
			a.flows = nil
			a.query = "/[/"
			a.applyFilter()
			tt.Frame()
			if !tt.HasText("Invalid search pattern") {
				t.Fatal("empty captures hid pattern validation behind the setup prompt")
			}
		})
	}
}

func TestQuerySyntaxTurnsFinishedTokensIntoChips(t *testing.T) {
	a := workspaceApp()
	a.views.init(a, "")
	tt := ui.NewTester(a.view, 1280, 700)
	if err := tt.Click("Search host or path"); err != nil {
		t.Fatal(err)
	}
	// A token still being typed is neither a condition nor URL text.
	tt.Type("login status:4")
	tt.Frame()
	if got := fmt.Sprint(visibleIDs(a)); a.filterStatus != "" || got != "[2]" {
		t.Fatalf("pending token filtered: status %q, visible %s", a.filterStatus, got)
	}
	tt.Type("xx ")
	tt.Frame()
	if got := fmt.Sprint(visibleIDs(a)); a.filterStatus != "4xx" || a.query != "login " || got != "[2]" || !tt.HasText("Remove condition: status:4xx") {
		t.Fatalf("finished token: status %q, query %q, visible %s", a.filterStatus, a.query, got)
	}
	// Unknown keys are text; an invalid value stays in the field with why.
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("localhost:8080 status:abc ")
	tt.Frame()
	if a.query != "localhost:8080 status:abc " || a.filterStatus != "4xx" || a.queryProblem == "" {
		t.Fatalf("query %q, status %q, problem %q", a.query, a.filterStatus, a.queryProblem)
	}
	// Enter takes the last token too; quotes keep a value's spaces.
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(`slow app:"Google Chrome" method:post operation:Search`)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.query != "" || a.filterTriage.Client != "Google Chrome" || a.filterTriage.Method != "POST" || a.filterTriage.Duration != ">=1000" || a.filterTriage.Operation != "Search" ||
		!tt.HasText(`Remove condition: app:"Google Chrome"`) || !tt.HasText("Remove condition: slow") {
		t.Fatalf("query %q, triage %+v", a.query, a.filterTriage)
	}
	// A chip's text comes back exactly, ready to change and commit again.
	if err := tt.Click("Edit condition: status:4xx"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.filterStatus != "" || a.query != "status:4xx" || !tt.Focused("Search host or path") {
		t.Fatalf("edit: status %q, query %q", a.filterStatus, a.query)
	}
}

func TestQueryAutocompleteCompletesKeysAndValuesWithoutTakingEnterOrDown(t *testing.T) {
	a := workspaceApp()
	a.views.init(a, "")
	tt := ui.NewTester(a.view, 1280, 700)
	search := func() {
		t.Helper()
		if err := tt.Click("Search host or path"); err != nil {
			t.Fatal(err)
		}
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(0, ui.KeyBackspace)
		tt.Frame()
	}
	// Free text that starts like a key is still a search on Enter.
	search()
	tt.Type("lo")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.query != "lo" || !tt.Focused("Captured requests") {
		t.Fatalf("Enter after free text: query %q", a.query)
	}
	// Down chooses a key; the values follow, filtered, the first for Enter.
	search()
	tt.Type("sta")
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.query != "status:" || !tt.HasText("status:4xx") {
		t.Fatalf("key completion: query %q", a.query)
	}
	tt.Type("4")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.query != "" || a.filterStatus != "4xx" || !tt.Focused("Search host or path") {
		t.Fatalf("value completion: query %q, status %q", a.query, a.filterStatus)
	}
	// Values come from the capture, and a click takes one.
	tt.Type("login app:b")
	tt.Frame()
	if err := tt.Click("app:Browser"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.query != "login " || a.filterTriage.Client != "Browser" || fmt.Sprint(visibleIDs(a)) != "[2]" {
		t.Fatalf("clicked value: query %q, app %q, visible %v", a.query, a.filterTriage.Client, visibleIDs(a))
	}
	// A chip turned back into text leaves the caret after it.
	if err := tt.Click("Edit condition: status:4xx"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type(" ")
	tt.Frame()
	if a.query != "login " || a.filterStatus != "4xx" {
		t.Fatalf("edited chip: query %q, status %q", a.query, a.filterStatus)
	}
	// With no suggestions showing, Down still moves to the requests.
	tt.Key(0, ui.KeyEscape)
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if !tt.Focused("Captured requests") {
		t.Fatal("Down did not move to the requests")
	}
}

func TestConnectionDiagnosticsMatchTheTargetOriginAndKeepFilters(t *testing.T) {
	fake := &fakeSystemProxy{check: system.ProxyCheck{Status: system.ProxyMatches}}
	a := proxyTestApp(t, fake)
	a.flows = []flow{
		{id: 1, live: true, method: "CONNECT", scheme: "https", url: "https://API.example.com:443/", kind: "connect", status: 200},
		{id: 2, live: true, method: "GET", scheme: "https", url: "https://api.example.com/items", kind: "http", status: 502, reqHeaders: "Accept: */*", conn: conn{errorStage: "TCP", error: "fixture connection refused"}},
		{id: 3, live: false, method: "GET", scheme: "https", url: "https://api.example.com/imported", status: 200, reqHeaders: "Accept: */*"},
		{id: 4, live: true, method: "GET", scheme: "https", url: "https://api.example.com:8443/other-port", status: 200, reqHeaders: "Accept: */*"},
		{id: 5, live: true, method: "GET", scheme: "http", url: "http://api.example.com:443/other-scheme", status: 200},
	}
	a.views.init(a, "")
	a.query = "no-match"
	a.views.applyFilter(a)
	tt := ui.NewTester(a.view, 960, 600)
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("Connection diagnostics")
	tt.Key(0, ui.KeyEnter)
	settleProxy(t, a, tt)
	if err := tt.Click("Target URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Ctrl, ui.KeyA)
	tt.Type("https://api.example.com/")
	// Imported (#3), other-port (#4) and other-scheme (#5) records are not
	// this origin's evidence; the latest live one (#2) is.
	if !tt.HasText("HTTP and HTTPS point at this app") || !tt.HasText("#2 · TCP failed") || tt.HasText("Waiting for a request") {
		t.Fatalf("wrong evidence: %q", tt.Texts())
	}
	if err := tt.Click("Actual traffic"); err != nil {
		t.Fatal(err)
	}
	tt.Frame() // the step scrolls into view
	if !tt.HasText("fixture connection refused") || !tt.HasText("Possible causes · not confirmed") || !tt.HasText("2 live records for this origin, from any client.") {
		t.Fatalf("failure not explained in place: %q", tt.Texts())
	}
	if fake.writes != 0 || a.deviceUI.gateway != nil {
		t.Fatal("opening diagnostics changed the proxy or exposed a device gateway")
	}
	// Inspecting hidden evidence must preserve the original filter tab.
	if err := tt.Click("Inspect #2"); err != nil {
		t.Fatal(err)
	}
	if a.pickedID != 2 || len(a.views.views) != 2 || a.views.views[0].query != "no-match" || a.page != pageLive {
		t.Fatal("inspecting evidence lost the old filter or selected other-origin traffic")
	}
}

func TestConnectionDiagnosticsEmptyAndInvalidTargetsDoNotClaimProxyFailure(t *testing.T) {
	a := workspaceApp()
	a.flows, a.visible = nil, nil
	a.systemProxy.read = func(target string) system.ProxyCheck {
		return system.ProxyCheck{Target: target, Status: system.ProxyOff}
	}
	tt := ui.NewTester(a.view, 960, 600)
	// Diagnostics has its own place in the sidebar's footer, under both of
	// the empty list's setup actions.
	if err := tt.Click("Diagnostics"); err != nil {
		t.Fatal(err)
	}
	settleProxy(t, a, tt)
	if !tt.HasText("No system proxy is set") || !tt.HasText("Waiting for a request to example.com") {
		t.Fatalf("no-traffic state not explained: %q", tt.Texts())
	}
	for _, target := range []string{"not a URL", "ftp://example.com", "https://user:secret@example.com"} {
		if err := tt.Click("Target URL"); err != nil {
			t.Fatal(err)
		}
		tt.Key(ui.Ctrl, ui.KeyA)
		tt.Type(target)
		if !tt.HasText("Enter an HTTP or HTTPS URL without embedded credentials.") || tt.HasText("Actual traffic") {
			t.Fatalf("invalid target %q received a diagnosis", target)
		}
	}
	if err := tt.Click("All requests"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Target URL") || !tt.HasText("Capture is stopped") {
		t.Fatal("the sidebar did not return to the unchanged capture view")
	}
}

func TestExperimentWorkbenchComparesIndependentRunsAndReopensWithoutSending(t *testing.T) {
	a := workspaceApp()
	draft := savedRequest{Method: "POST", URL: "https://example.test/items", Body: "original"}
	for i, body := range []string{`{"value":1,"timestamp":10}`, `{"value":2,"timestamp":20}`} {
		draft.Body = fmt.Sprintf("attempt %d", i+1)
		if err := a.experiments.log.retain(snapshotExperiment(0, draft, "staging", request.Execution{Response: &capture.Flow{Method: "POST", URL: draft.URL, Status: 200, RequestBody: draft.Body, ResponseBody: body}})); err != nil {
			t.Fatal(err)
		}
	}
	tt := ui.NewTester(a.view, 1440, 900)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	copyText := func(label string) string {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		return tt.Clipboard()
	}
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("History")
	tt.Key(0, ui.KeyEnter)
	click("Experiment #1")
	click("Set baseline")
	click("Experiment #2")
	click("Compare with left")
	if got := copyText("Old text"); got != `{"value":1,"timestamp":10}` {
		t.Fatalf("old run = %q", got)
	}
	if got := copyText("New text"); got != `{"value":2,"timestamp":20}` {
		t.Fatalf("new run = %q", got)
	}
	click("Ignore JSON keys")
	tt.Type("timestamp")
	if got := copyText("Old text"); got != `{"value":1}` {
		t.Fatalf("filtered old run = %q", got)
	}
	if got := copyText("New text"); got != `{"value":2}` {
		t.Fatalf("filtered new run = %q", got)
	}
	click("Experiment #1")
	click("Recorded result")
	if got := copyText("Experiment result"); !strings.Contains(got, `"timestamp":10`) {
		t.Fatal("comparison rewrote the original snapshot")
	}
	click("Open as draft")
	if !tt.HasText("Recorded run #1") || a.composeWaiting || a.composeBody != "attempt 1" || a.response.ResponseBody != `{"value":1,"timestamp":10}` {
		t.Fatal("opening a historical run sent it, chose the wrong snapshot, or lost its result")
	}
}

func TestRunRecordTabsRemainReadableAtMinimumWidth(t *testing.T) {
	for _, dark := range []bool{false, true} {
		a := snapshotApp()
		a.appearance = appearanceOf(dark)
		snapshotRuns(a)
		wide := ui.NewTester(a.view, 1280, 800)
		full, ok := wide.Find("Variables")
		if !ok {
			t.Fatal("missing variables tab")
		}
		tt := ui.NewTester(a.view, 960, 800)
		for _, label := range []string{"History status", "History source"} {
			bounds, ok := tt.Find(label)
			if !ok || bounds.W < 24 || bounds.X < 0 || bounds.X+bounds.W > 960 {
				t.Fatalf("dark=%t: history filter unavailable at minimum width: %s %v", dark, label, bounds)
			}
		}
		last, ok := tt.Find("Variables")
		if !ok || last.W < full.W || last.X+last.W > 948 {
			t.Fatalf("dark=%t: final tab clipped at minimum width: %v, full=%v", dark, last, full)
		}
		if err := tt.Click("Variables"); err != nil {
			t.Fatal(err)
		}
		if !tt.HasText("baseUrl") || len(a.documents) != 0 {
			t.Fatal("variables tab lost its snapshot or opened a draft")
		}
		if err := tt.Click("Console"); err != nil {
			t.Fatal(err)
		}
		if !tt.HasText("Created project 73") {
			t.Fatal("console tab lost retained output")
		}
	}
}

func TestExperimentDraftCheckpointRestoresUnsavedEditsAndCloseFlushes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "experiments.json")
	a := workspaceApp()
	a.loadExperiments(path)
	a.addDocument(savedRequest{Name: "Draft A", Method: "POST", URL: "https://example.test/a", Body: "unsaved body"}, "")
	a.addDocument(savedRequest{Name: "Draft B", Method: "GET", URL: "https://example.test/b"}, "")
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("https://example.test/edited")
	// Closing immediately, without waiting for the debounce, must save the last edit.
	a.experiments.close()
	b := workspaceApp()
	b.loadExperiments(path)
	defer b.experiments.close()
	if len(b.documents) != 2 || b.documents[0].composeBody != "unsaved body" || b.composeURL != "https://example.test/edited" || !b.documents[0].dirty() || b.composeWaiting {
		t.Fatal("restart lost drafts/active tab/dirty state or sent a request")
	}
	if len(b.experiments.log.Records) != 0 {
		t.Fatal("restoring drafts invented an experiment")
	}
}

func TestCaptureFacetsIntersectAndResetOnlyTheActiveView(t *testing.T) {
	a := workspaceApp()
	a.flows = append(a.flows,
		flow{id: 3, method: "GET", host: "other.test", path: "/other", process: "Browser", status: 401},
		flow{id: 4, method: "GET", host: "api.example.com", path: "/cli", process: "CLI", status: 401},
	)
	a.recount()
	a.applyFilter()
	tt := ui.NewTester(a.view, 1280, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	// Sources shows one of the two lists at a time: the switch above it
	// chooses which.
	a.sourcesApps = true
	tt.Frame()
	click("Browser")
	a.sourcesApps = false
	tt.Frame()
	click("api.example.com")
	if got := fmt.Sprint(visibleIDs(a)); got != "[1 2]" {
		t.Fatalf("app and host did not intersect: %s", got)
	}
	if err := tt.RightClick("api.example.com/login"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Filter captures", "Status: 401"); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(visibleIDs(a)); got != "[2]" {
		t.Fatalf("row filter replaced a facet: %s", got)
	}
	click("Remove condition: app:Browser")
	if got := fmt.Sprint(visibleIDs(a)); got != "[2 4]" || a.filterTriage.Host != "api.example.com" || a.filterStatus != "401" {
		t.Fatalf("removing app changed other conditions: %s", got)
	}
	click("New view")
	// A URL-only condition must also make the overflow reset available.
	a.query = "other"
	tt.Frame()
	click("More capture options")
	if err := chooseMenu(tt, "Reset all conditions"); err != nil {
		t.Fatal(err)
	}
	if len(a.visible) != 4 || a.filtersActive() {
		t.Fatal("overflow reset did not clear the URL-only condition")
	}
	click("Capture view: View 1")
	if got := fmt.Sprint(visibleIDs(a)); got != "[2 4]" {
		t.Fatalf("reset modified the other view: %s", got)
	}
	a.filterStatus = "503"
	tt.Frame()
	if !tt.HasText("No matching requests") {
		t.Fatal("empty intersection has no filter recovery state")
	}
	click("Show all requests")
	if len(a.visible) != 4 || a.filtersActive() {
		t.Fatal("empty-result reset left hidden conditions")
	}
}

func TestHeaderContextFilterUsesEitherSideContainsWithoutLeakingCredentials(t *testing.T) {
	a := workspaceApp()
	a.flows[0].reqHeader = http.Header{"X-Format": {"text", "JSON"}, "X-Api-Key": {"private-fixture"}}
	a.flows[1].respHeader = http.Header{"X-Format": {"application/json; charset=utf-8"}}
	a.flows = append(a.flows, flow{id: 3, host: "other.test", respHeader: http.Header{"X-Format": {"text/plain"}}})
	a.applyFilter()
	tt := ui.NewTester(a.view, 1280, 800)
	// The capture opens in the editor; its cells carry the same filter menu
	// the reader offers. Rows are sorted, so X-Api-Key comes first.
	if err := tt.RightClick("header key 1"); err != nil {
		t.Fatal(err)
	}
	// Inspect only the menu overlay, not the header value behind it.
	_, menu, found := strings.Cut(strings.Join(tt.Texts(), "\n"), "Matches request or response headers\n")
	if !found || !strings.Contains(menu, "Credential values are not added to filters") {
		t.Fatalf("credential filter menu did not open: %s", menu)
	}
	if strings.Contains(menu, "private-fixture") || strings.Contains(menu, "Contains value") {
		t.Fatalf("credential escaped into the filter menu: %s", menu)
	}
	if err := chooseMenu(tt, "Header exists: X-Api-Key"); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(visibleIDs(a)); got != "[1]" || a.filterHeader != "X-Api-Key" {
		t.Fatalf("header existence filter = %q, rows %s", a.filterHeader, got)
	}
	a.filterHeader = ""
	a.applyFilter()
	tt.Frame()
	if err := tt.RightClick("header key 2"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Contains value 2: JSON"); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(visibleIDs(a)); got != "[1 2]" {
		t.Fatalf("header condition did not use case-insensitive contains on both sides: %s", got)
	}
}

func TestSendCancellationKeepsTheSentSnapshotAndDoesNotRetry(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		fmt.Fprint(w, "finished")
	}))
	defer upstream.Close()
	defer close(release)
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.addDocument(savedRequest{Name: "Slow checkout", Method: "POST", URL: upstream.URL + "/checkout", Body: "original"}, "")
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Minute):
		t.Fatal("send never reached upstream")
	}
	// Editing is allowed while sending; cancellation belongs to the snapshot,
	// not these new edits, and repeated shortcuts must not send a second POST.
	if err := tt.Click("URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(upstream.URL + "/unsent")
	tt.Key(ui.Cmd, ui.KeyEnter)
	if err := tt.Click("Cancel send"); err != nil {
		t.Fatal(err)
	}
	guard := time.NewTimer(time.Minute)
	defer guard.Stop()
	for a.composeWaiting {
		select {
		case <-guard.C:
			t.Fatal("cancel did not finish")
		default:
		}
		tt.Frame()
		runtime.Gosched()
	}
	if !tt.HasText("Cancelled") || tt.HasText("Not sent yet") || hits.Load() != 1 {
		t.Fatalf("cancel state or duplicate-send protection failed: %v; hits %d", tt.Texts(), hits.Load())
	}
	if tt.HasText("Edit the draft and send again.") || tt.HasText("context canceled") || !tt.HasText("The server may still be processing") {
		t.Fatalf("cancellation was presented as an editable request failure: %q", tt.Texts())
	}
	if err := tt.Click("Technical details"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if !tt.HasText("context canceled") {
		t.Fatal("cancellation technical evidence was lost")
	}
	if a.composeURL != upstream.URL+"/unsent" {
		t.Fatal("cancelling overwrote in-flight edits")
	}
	if err := tt.Click("Open request History"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Cancelled") || !tt.HasText("Experiment #1") {
		t.Fatal("a sourceless cancelled send was not retained in send history")
	}
	if err := tt.Click("Open as draft"); err != nil {
		t.Fatal(err)
	}
	if a.composeURL != upstream.URL+"/checkout" || a.documents[0].composeURL != upstream.URL+"/unsent" || !tt.HasText("Cancelled") || tt.HasText("Not sent yet") || hits.Load() != 1 {
		t.Fatalf("history restoration sent or replaced edits: %v", tt.Texts())
	}
	// An independent draft using the identical URL must not inherit this
	// cancelled attempt. A pre-script failure records it without another POST.
	a.addDocument(savedRequest{Name: "Independent checkout", Method: "POST", URL: upstream.URL + "/checkout", Pre: `throw new Error("not ready")`}, "")
	tt.Frame()
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	for a.composeWaiting {
		select {
		case <-guard.C:
			t.Fatal("independent attempt did not finish")
		default:
		}
		tt.Frame()
		runtime.Gosched()
	}
	if err := tt.Click("Open request History"); err != nil {
		t.Fatal(err)
	}
	if _, found := tt.Find("Experiment #1"); found || !tt.HasText("Pre-request script failed") || hits.Load() != 1 {
		t.Fatal("same URL mixed independent send histories")
	}
}

func TestRequestsWorkspaceDoesNotCreateUnrequestedDrafts(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 960, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	tt.Key(ui.Cmd, ui.Key2)
	if !tt.HasText("Open a saved request or create a new one") {
		t.Fatal("the workspace shortcut silently created an empty draft")
	}
	tt.Key(ui.Cmd, ui.Key1)
	tt.Key(ui.Cmd, ui.Key2)
	if !tt.HasText("Open a saved request or create a new one") {
		t.Fatal("entering Requests silently created an empty draft")
	}
	click("List projects")
	if tt.HasText("Unsaved requests") {
		t.Fatal("opening a saved request left an automatic blank draft")
	}
	click("New request")
	click("List projects")
	click("Unsaved: Untitled request")
	if readField(t, tt, "URL") != "https://" {
		t.Fatal("opening a saved request discarded a user-created blank draft")
	}
	click("URL")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("https://example.test/unsaved")
	tt.Key(ui.Cmd, ui.Key1)
	tt.Key(ui.Cmd, ui.Key2)
	if readField(t, tt, "URL") != "https://example.test/unsaved" {
		t.Fatal("workspace shortcut lost the edited draft")
	}
}

func TestSendResultsSeparateFailuresAndLinkTheExactAttempt(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/502" {
			w.WriteHeader(502)
		}
		fmt.Fprint(w, "vscroxy: fixture response, not evidence of a network error")
	}))
	defer upstream.Close()
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadURL := "http://" + dead.Addr().String()
	dead.Close()
	for _, tc := range []struct {
		name, outcome  string
		request        savedRequest
		wire, snapshot bool
	}{
		{"setup", "Request setup failed", savedRequest{Method: "GET", URL: "bad-url"}, false, false},
		{"pre-script", "Pre-request script failed", savedRequest{Method: "GET", URL: upstream.URL, Pre: `throw new Error("before sending")`}, false, false},
		{"http", "HTTP error response", savedRequest{Method: "GET", URL: upstream.URL + "/502"}, true, false},
		{"network", "Network error", savedRequest{Method: "GET", URL: deadURL}, true, false},
		{"checks", "Checks failed", savedRequest{Method: "POST", URL: upstream.URL, Body: "draft-only-body", Pre: `pm.request.body.raw = "actual-sent-body"; pm.request.headers.upsert("X-Prepared", "yes")`, Checks: request.Checks{Status: "201"}}, true, true},
		{"post-script", "Post-response script failed", savedRequest{Method: "GET", URL: upstream.URL, Post: `throw new Error("after response")`}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := workspaceApp()
			a.flows = []flow{{id: 900, method: "GET", url: tc.request.URL, host: "source.test", status: 200}}
			a.applyFilter()
			a.srv = requestProxy(t)
			a.addDocument(tc.request, "")
			tt := ui.NewTester(a.view, 960, 600)
			if err := tt.Click("Send"); err != nil {
				t.Fatal(err)
			}
			guard := time.NewTimer(time.Minute)
			defer guard.Stop()
			for a.composeWaiting {
				select {
				case <-guard.C:
					t.Fatal("send did not finish")
				default:
				}
				tt.Frame()
				runtime.Gosched()
			}
			if tc.wire {
				select {
				case f := <-a.srv.Flows():
					a.pending = append(a.pending, f)
				case <-guard.C:
					t.Fatal("missing wire evidence")
				}
				tt.Frame()
			}
			if !tt.HasText(tc.outcome) || tt.HasText("Not sent yet") {
				t.Fatalf("wrong attempt state: %q", tt.Texts())
			}
			if tc.name == "http" && tt.HasText("Network error") || tc.name == "network" && tt.HasText("HTTP error response") {
				t.Fatalf("network and upstream HTTP502 mixed: %q", tt.Texts())
			}
			if tc.name == "network" && !tt.HasText("502 · proxy response") || tc.name == "http" && !tt.HasText("502 Bad Gateway") {
				t.Fatalf("response origin is ambiguous: %q", tt.Texts())
			}
			if err := tt.Click("Open request History"); err != nil {
				t.Fatal(err)
			}
			if !tt.HasText(tc.outcome) {
				t.Fatal("history lost the outcome")
			}
			if !tc.snapshot {
				return
			}
			if err := tt.Click("Sent request"); err != nil {
				t.Fatal(err)
			}
			text := readField(t, tt, "Sent request snapshot")
			if !strings.Contains(text, "actual-sent-body") || !strings.Contains(text, "X-Prepared: yes") || strings.Contains(text, "draft-only-body") {
				t.Fatalf("history confused editable draft and prepared request: %s", text)
			}
			if err := tt.Click("Close"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("URL"); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Type(upstream.URL + "/unsent")
			if !tt.HasText("Draft changed · not sent") {
				t.Fatal("old response was attributed to current edits")
			}
			if err := tt.Click("Last send details"); err != nil {
				t.Fatal(err)
			}
			if readField(t, tt, "Last sent draft URL") != "POST "+upstream.URL || !tt.HasText("Draft changed · not sent") {
				t.Fatalf("expanded provenance lost the original URL or the unsent warning: %q", tt.Texts())
			}
			if err := tt.Click("Last send details"); err != nil {
				t.Fatal(err)
			}
			if tt.HasText("Last sent draft URL") || readField(t, tt, "URL") != upstream.URL+"/unsent" {
				t.Fatal("collapsing provenance changed the draft or left the detail open")
			}
			if err := tt.Click("Response pane: Timing"); err != nil {
				t.Fatal(err)
			}
			if !tt.HasText("Timing waterfall") {
				t.Fatal("this send's request-local timing was not shown inline: ", tt.Texts())
			}
			if err := tt.Click("Inspect timing in Capture"); err != nil {
				t.Fatal(err)
			}
			if !a.page.capture() || a.pickedID == 900 || !tt.HasText("Timing waterfall") {
				t.Fatalf("timing target: page %d, capture %d, tab %d", a.page, a.pickedID, a.inspect.tab[1])
			}
		})
	}
}

// The collection's menu opens its read-only documentation with the folder's
// Markdown and each request's method, URL and description.
func TestCollectionDocumentationView(t *testing.T) {
	a := workspaceApp()
	a.folders = []request.Folder{{Path: "Workspace/API", Description: "## Workspace API\n\nEndpoints for **projects**."}}
	a.requests[0].Description = "Lists projects."
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Collection actions: Workspace/API"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "View documentation…"); err != nil {
		t.Fatal(err)
	}
	if !a.docsOpen || a.docsPath != "Workspace/API" {
		t.Fatalf("documentation = %v %q", a.docsOpen, a.docsPath)
	}
	if !tt.HasText("Documentation · Workspace/API") || !tt.HasText("Lists projects.") || !tt.HasText("https://api.example.com/projects") {
		t.Fatalf("documentation view lacks its content: %v", tt.Texts())
	}
	tt.Click("Close")
	if a.docsOpen {
		t.Fatal("Close left the documentation view open")
	}
}

// The Docs and Settings tabs edit the request itself, and Save writes both to
// the library.
func TestRequestDocsAndSettingsSave(t *testing.T) {
	a := libraryApp(t)
	a.requests = []savedRequest{{Request: request.Request{ID: "one", Name: "One", Collection: "Docs", Method: "GET", URL: "https://example.com"}}}
	a.openSaved(a.requests[0].key())
	a.composeTab = composeTabDocs
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Request description"); err != nil {
		t.Fatal(err)
	}
	tt.Type("## Notes\n\nWhat it does.")
	choosePaneView(t, tt, "Request pane", "Settings")
	if err := tt.Click("Verify TLS certificate"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Follow redirects"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.requests[0].Description != "## Notes\n\nWhat it does." {
		t.Fatalf("description = %q", a.requests[0].Description)
	}
	if p := a.requests[0].Settings.VerifyTLS; p == nil || *p {
		t.Fatalf("verify TLS = %v, want off", p)
	}
	if p := a.requests[0].Settings.FollowRedirects; p == nil || *p {
		t.Fatalf("follow redirects = %v, want off", p)
	}
	loaded, err := loadLibraryRequests(a.library.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Description != a.requests[0].Description || loaded[0].Settings.VerifyTLS == nil || *loaded[0].Settings.VerifyTLS {
		t.Fatalf("saved request = %+v", loaded)
	}
}

// A collection's Request defaults tab saves the transport values requests
// under it inherit.
func TestFolderRequestDefaultsSave(t *testing.T) {
	a := libraryApp(t)
	a.folders = []request.Folder{{Path: "Docs"}}
	a.openFolder("Docs")
	a.folderUI.tab = 4
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Verify TLS certificate"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Save settings"); err != nil {
		t.Fatal(err)
	}
	if len(a.folders) != 1 || a.folders[0].Settings.VerifyTLS == nil || *a.folders[0].Settings.VerifyTLS {
		t.Fatalf("folders = %+v, want TLS verification off", a.folders)
	}
}

// A change made inside the write gap is not dropped: the app asks for a frame
// once the gap passes, and writes it then. Before, the change waited for
// something else to move the window and was lost when nothing did.
func TestSettingsChangeInsideTheWriteGapIsWrittenLater(t *testing.T) {
	a := workspaceApp()
	dir := t.TempDir()
	a.settingsPath = filepath.Join(dir, "settings.json")
	a.saved = a.currentSettings()
	// A write just went out.
	a.lastSave = time.Now()
	a.sidebarWidth = 300
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	data, err := os.ReadFile(a.settingsPath)
	if err == nil && strings.Contains(string(data), "\"sidebarWidth\":300") {
		t.Fatal("the change was written inside the gap")
	}
	// The gap passes, and the frame the app asked for arrives.
	a.lastSave = time.Now().Add(-time.Second)
	tt.Frame()
	data, err = os.ReadFile(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"sidebarWidth\":300") {
		t.Fatalf("the change inside the gap never reached the file: %s", data)
	}
}

func TestFlushSettingsWritesWhatTheGapHeldBack(t *testing.T) {
	a := workspaceApp()
	dir := t.TempDir()
	a.settingsPath = filepath.Join(dir, "settings.json")
	a.saved = a.currentSettings()
	a.lastSave = time.Now()
	a.sidebarWidth = 310
	a.persistSettings()
	if _, err := os.Stat(a.settingsPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the change was written inside the gap")
	}
	a.flushSettings()
	data, err := os.ReadFile(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"sidebarWidth\":310") {
		t.Fatalf("quitting lost the change the gap held back: %s", data)
	}
}

// Text that carries a credential goes to the clipboard as concealed, so a
// clipboard manager keeps it out of its history; ordinary text does not.
func TestCopyConcealsWhatCarriesACredential(t *testing.T) {
	for text, want := range map[string]bool{
		"GET / HTTP/1.1\r\nAuthorization: Bearer abc\r\n\r\n": true,
		"GET / HTTP/1.1\r\nCookie: sid=1\r\n\r\n":             true,
		`{"access_token":"abc"}`:                              true,
		"-----BEGIN PRIVATE KEY-----\nabc\n":                  true,
		"GET / HTTP/1.1\r\nAccept: */*\r\n\r\n":               false,
		`{"name":"example"}`:                                  false,
	} {
		if got := looksSecret(text); got != want {
			t.Fatalf("looksSecret(%q) = %t, want %t", text, got, want)
		}
	}
}

// A selected event that the ring drops comes back as -1 from the list: the
// detail goes to the newest event still there, not back to the oldest one.
func TestAnEvictedEventSelectsTheNewestOneLeft(t *testing.T) {
	for _, c := range []struct {
		selected, n, want int
	}{
		{-1, 4, 3}, // the selected key went away
		{0, 4, 0},  // the first event stays chosen
		{3, 4, 3},  // the last event stays chosen
		{4, 4, 3},  // past the end
	} {
		selected := c.selected
		settleEvent(&selected, c.n)
		if selected != c.want {
			t.Fatalf("settleEvent(%d, %d) = %d, want %d", c.selected, c.n, selected, c.want)
		}
	}
}

// Dragging a column and letting go writes the layout without anything else
// happening: the table reports the change, the app copies it back then, and the
// settings are written even though no other frame follows.
func TestColumnDragIsSavedWithoutAnotherEvent(t *testing.T) {
	a := workspaceApp()
	dir := t.TempDir()
	a.settingsPath = filepath.Join(dir, "settings.json")
	a.saved = a.currentSettings()
	tt := ui.NewTester(a.view, 1440, 860)
	tt.Frame()
	if len(a.columns.Widths) != 0 {
		t.Fatalf("layout before the drag = %+v", a.columns)
	}
	// Drag the Status column's right edge, as the pointer does.
	header, ok := tt.Find("Status")
	if !ok {
		t.Fatal(tt.Texts())
	}
	x, y := header.X+header.W-1, header.Y+header.H/2
	tt.Press(x, y)
	tt.Move(x+30, y)
	tt.Release(x+30, y)
	tt.Frame()
	if got := a.columns.Widths["status"]; got <= header.W {
		t.Fatalf("the dragged width was not copied back: %v (was %v)", a.columns.Widths, header.W)
	}
	// The frame the app asked for once the write gap passed.
	a.lastSave = time.Now().Add(-time.Second)
	tt.Frame()
	data, err := os.ReadFile(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"status":`) {
		t.Fatalf("the dragged width never reached the file: %s", data)
	}
}

// A pointer move costs one frame and no more: the tester runs twenty view
// passes to settle a frame, and a hover over the sidebar or the toolbar is one
// frame of that. What this catches is a hover that asks for frames of its own.
func TestAPointerMoveCostsOneFrame(t *testing.T) {
	a := workspaceApp()
	for i := range 1000 {
		a.flows = append(a.flows, flow{id: 100 + i, at: time.Now().Add(time.Duration(i) * time.Millisecond), host: "example.test", method: "GET", status: 200, path: "/x"})
	}
	a.recount()
	tt := ui.NewTester(a.view, 1440, 860)
	tt.Frame()
	before := a.builds
	tt.Frame()
	perFrame := a.builds - before
	before = a.builds
	moved := 0
	for _, label := range []string{"All requests", "Errors", "Slow", "Pinned"} {
		if r, ok := tt.Find(label); ok {
			tt.Move(r.X+10, r.Y+r.H/2)
			moved++
		}
	}
	if moved == 0 {
		t.Fatal(tt.Texts())
	}
	if got := a.builds - before; got > perFrame*moved {
		t.Fatalf("%d hovers ran %d view passes, more than the %d a frame costs", moved, got, perFrame*moved)
	}
}

// The icon cache asks the desktop once per path: a table of a thousand rows
// from one app must not read the file icon a thousand times.
func TestAppIconsAreLookedUpOncePerPath(t *testing.T) {
	a := workspaceApp()
	lookups := 0
	a.iconLookup = func(string) ([]byte, bool) { lookups++; return nil, false }
	a.clientIcon("curl", "/usr/bin/curl")
	a.clientIcon("curl", "/usr/bin/curl")
	a.clientIcon("curl", "/usr/bin/curl")
	if lookups != 1 {
		t.Fatalf("the icon was looked up %d times for one path, want once", lookups)
	}
}

// rowCenter is where a table row is drawn: the header sits above the rows, and
// every row is one line high.
func rowCenter(table ui.Rect, row int) (float32, float32) {
	return table.X + 300, table.Y + 32 + float32(row)*24
}

// The row the inspector shows is the row a person is reading, so the table
// draws it as chosen. MyGo paints a table's chosen rows from the selection set
// it is given, not from the row the keys move from, and the app picks the first
// arrival itself: without the two in step, the inspected row looked like every
// other one.
func TestTheInspectedRowIsTheChosenRow(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	table, ok := tt.Find("Captured requests")
	if !ok {
		t.Fatal("no table")
	}
	if !a.chosen.Has(a.pickedID) {
		t.Fatalf("the inspected row %d is not chosen: %d chosen", a.pickedID, a.chosen.Len())
	}
	im := tt.Image()
	x, picked := rowCenter(table, 0)
	_, other := rowCenter(table, 1)
	if im.RGBAAt(int(x), int(picked)) == im.RGBAAt(int(x), int(other)) {
		t.Fatal("the inspected row is drawn like the one below it")
	}
}

// A filter that hides the chosen row leaves the inspector on a neighbour, and
// the table marks that neighbour: the choice follows the row.
func TestAFilterMovesTheChoiceToTheRowLeft(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Type("login")
	tt.Key(0, ui.KeyEnter)
	if a.pickedID != 2 {
		t.Fatalf("the filter left the inspector on %d, want the surviving request", a.pickedID)
	}
	if !a.chosen.Has(2) || a.chosen.Len() != 1 {
		t.Fatalf("the choice did not follow the row: %d chosen, holds 2: %v", a.chosen.Len(), a.chosen.Has(2))
	}
}

// A choice the user widened is what bulk actions see, and the footer says so
// only then: one chosen row is the row being read, not a bulk choice.
func TestAWidenedChoiceStaysChosen(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	table, ok := tt.Find("Captured requests")
	if !ok {
		t.Fatal("no table")
	}
	x, first := rowCenter(table, 0)
	_, second := rowCenter(table, 1)
	tt.ClickAtWith(0, x, first)
	tt.Frame()
	if got := a.footerCountsText(); got != "" {
		t.Fatalf("one chosen row reads as a bulk choice: %q", got)
	}
	tt.ClickAtWith(ui.Shift, x, second)
	tt.Frame()
	if a.chosen.Len() != 2 || !a.chosen.Has(1) || !a.chosen.Has(2) {
		t.Fatalf("Shift-click chose %d rows, holds 1 and 2: %v %v", a.chosen.Len(), a.chosen.Has(1), a.chosen.Has(2))
	}
	if !tt.HasText("2 of 2 selected") {
		t.Fatalf("the footer does not count a bulk choice: %q", tt.Texts())
	}
}

// The traffic list is where the keys go in LIVE: the window's own column takes
// the focus on launch, and the arrows did nothing until the list was clicked.
func TestTheTrafficListTakesTheKeysOnLaunch(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if !tt.Focused("Captured requests") {
		t.Fatal("the traffic list does not have the keys on launch")
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if f, ok := a.current(); !ok || f.id != 2 {
		t.Fatalf("the arrow key did not move the inspector: %+v", f)
	}
	if !a.chosen.Has(2) || a.chosen.Len() != 1 {
		t.Fatalf("the moved row is not the choice: %d chosen", a.chosen.Len())
	}
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if f, ok := a.current(); !ok || f.id != 1 {
		t.Fatalf("the arrow key did not move back: %+v", f)
	}
}

// Escape in the search field takes back what was typed, as in a filter field
// elsewhere: the chips, the view and the focus stay.
func TestEscapeClearsTheSearchText(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Type("login")
	tt.Frame()
	if a.query != "login" || !tt.HasText("1 of 2") {
		t.Fatalf("the search did not narrow the list: %q", a.query)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.query != "" || a.footerCountsText() != "" {
		t.Fatalf("Escape left the search in place: %q", a.query)
	}
	if !tt.Focused("Search host or path") {
		t.Fatal("Escape moved the keys out of the search field")
	}
}

// ⌘D opens a copy of the request being edited, as the ⋯ menu's Duplicate does.
func TestDuplicateOpensACopyOfTheRequest(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if err := tt.Click("List projects"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	opened := a.requestDocument
	tt.Key(ui.Cmd, ui.KeyD)
	tt.Frame()
	d := a.requestDocument
	if d == nil || d == opened || a.page != pageRequest {
		t.Fatal("⌘D did not open a copy of the request")
	}
	if d.savedKey != "" || !strings.HasSuffix(d.value().Name, " copy") {
		t.Fatalf("the copy is not a new unsaved request: %q %q", d.savedKey, d.value().Name)
	}
	if d.value().URL != "https://api.example.com/projects" {
		t.Fatalf("the copy has another URL: %q", d.value().URL)
	}
}

// Narrowing has one entry: the search field, with the dialog's own fields at
// its end. The footer and the sidebar keep no filter of their own, and the
// field has no mode to pick: /regex/ and * wildcards are written in it.
func TestFilteringHasOneEntry(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if tt.HasText("Capture search mode") {
		t.Fatal("the search field still offers a mode")
	}
	for _, text := range tt.Texts() {
		if text == "Filter" {
			t.Fatal("the footer still offers a Filter of its own")
		}
	}
	field, ok := tt.Find("Search host or path")
	funnel, found := tt.Find("Filters…")
	tabs, tabsFound := tt.Find("Protocol and content type")
	if !ok || !found || !tabsFound {
		t.Fatalf("the field, its funnel or the protocol tabs is missing: %v %v %v", ok, found, tabsFound)
	}
	// The funnel sits past the input's text and before the protocol tabs:
	// inside the field, not in a row of its own.
	if funnel.X < field.X || funnel.X+funnel.W > tabs.X {
		t.Fatalf("the funnel is not at the field's end: funnel %v, field %v, tabs %v", funnel, field, tabs)
	}
	if err := tt.Click("Filters…"); err != nil {
		t.Fatal(err)
	}
	if !a.views.filterOpen {
		t.Fatal("the field's funnel did not open the filters")
	}
}

// A count is said once: the sidebar counts the capture, so the footer only
// says how much of it the view hides, and the activity strip is the plot and
// the time it ends at.
func TestCountsAreSaidOnce(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if got := a.footerCountsText(); got != "" {
		t.Fatalf("the footer counts what the sidebar already does: %q", got)
	}
	for _, text := range tt.Texts() {
		if strings.Contains(text, "requests") && strings.Contains(text, "min") {
			t.Fatalf("the activity strip still counts: %q", text)
		}
	}
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Type("login")
	tt.Frame()
	if !tt.HasText("1 of 2") {
		t.Fatalf("the footer does not say how much the view hides: %q", tt.Texts())
	}
	if a.footerCountsText() != "1 of 2" {
		t.Fatalf("footer counts %q", a.footerCountsText())
	}
}

// tunnelApp is the window with one CONNECT row: a client that asked for TLS
// and nothing that can be read inside it.
func tunnelApp() *app {
	a := workspaceApp()
	a.iconLookup = func(string) ([]byte, bool) { return nil, false }
	a.flows = []flow{{
		id: 1, kind: "connect", method: "CONNECT", scheme: "https",
		host: "api.github.com:443", url: "https://api.github.com:443", status: 200,
		process: "Safari", processPath: "/Applications/Safari.app/Contents/MacOS/Safari",
		conn: conn{proto: "HTTP/1.1", client: "Safari", remote: "140.82.121.6", log: "CONNECT api.github.com:443\n  tunnel established"},
	}}
	a.recount()
	a.applyFilter()
	return a
}

// A tunnel says what it is in one line and offers the way out of it. The rules
// that would decrypt the next connection are the Connection tab's.
func TestTunnelDetailSaysOneThingAndOffersDecrypting(t *testing.T) {
	a := tunnelApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if !tt.HasText("Encrypted, not decrypted") {
		t.Fatalf("the tunnel does not say what it is: %q", tt.Texts())
	}
	for _, gone := range []string{
		"This connection is encrypted. CONNECT is not a decrypted HTTP response.",
		"Known configuration",
		"Domain decryption disabled",
	} {
		if tt.HasText(gone) {
			t.Fatalf("the tunnel still explains itself: %q", gone)
		}
	}
	if !tt.HasText("Decrypt api.github.com") {
		t.Fatalf("the tunnel offers no way to decrypt it: %q", tt.Texts())
	}
	if err := tt.Click("Connection"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Decryption") || !tt.HasText("App rule") || !tt.HasText("Trust") {
		t.Fatalf("the rules that would decrypt it are not in the Connection tab: %q", tt.Texts())
	}
}

// A tunnel row is a lock in Status and nothing in Path: "Tunnel" and a lock
// before a "/" read as data that is not there.
func TestTunnelRowsReadAsTunnels(t *testing.T) {
	a := tunnelApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if tt.HasText("Tunnel") {
		t.Fatalf("a tunnel row spells its status: %q", tt.Texts())
	}
	if !tt.HasText("CONNECT") || !tt.HasText("api.github.com:443") {
		t.Fatalf("the tunnel row lost its request line: %q", tt.Texts())
	}
	if _, ok := tt.Find("api.github.com:443/"); ok {
		t.Fatal("the tunnel row still shows a path")
	}
	status, ok := tt.Find(locales.TunnelTooltip(200))
	if !ok {
		t.Fatalf("the lock in Status says nothing: %q", tt.Texts())
	}
	table, _ := tt.Find("Captured requests")
	if status.Y < table.Y || status.Y > table.Y+80 {
		t.Fatalf("the lock is not in the row: %v in %v", status, table)
	}
}

// The footer's actions are actions: at rest they are text on the bar's own
// surface, the pointer gives them a face, and a Host profile that is off is
// not a chip at all.
func TestFooterActionsReadAsActions(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if tt.HasText("Off") {
		t.Fatalf("the footer offers a Hosts switch that is off: %q", tt.Texts())
	}
	clear, ok := tt.Find("Clear")
	if !ok {
		t.Fatal("the footer has no Clear")
	}
	chrome := uikit.Colors(uikit.Theme(false)).Chrome
	at := func() ui.Color {
		p := tt.Image().RGBAAt(int(clear.X+3), int(clear.Y+clear.H/2))
		return ui.RGB(p.R, p.G, p.B)
	}
	if at() != chrome {
		t.Fatalf("Clear wears a face at rest: %v, want the bar's %v", at(), chrome)
	}
	tt.Move(clear.X+clear.W/2, clear.Y+clear.H/2)
	tt.Frame()
	if at() == chrome {
		t.Fatal("Clear shows no face under the pointer")
	}
}

// The capture screen says what it is, not how it works: no sentence explains
// how conditions combine or where a link is kept, and the buttons are verbs.
func TestCaptureScreenKeepsExplanationsOut(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Type("no-such-host")
	tt.Frame()
	if !tt.HasText("No matching requests") || !tt.HasText("Show all requests") || !tt.HasText("Edit filters…") {
		t.Fatalf("the empty list lost its title or its actions: %q", tt.Texts())
	}
	if tt.HasText("All conditions must match") {
		t.Fatalf("the empty list explains how conditions combine: %q", tt.Texts())
	}
	a.views.resetFilters(a)
	// A replay names its source; the row does not also explain that links
	// stay with saved sessions.
	a.flows[0].replayOf = 2
	a.tally.known[2] = struct{}{}
	a.applyFilter()
	tt.Frame()
	if !tt.HasText("Replay of #2") {
		t.Fatalf("the replay row lost its source: %q", tt.Texts())
	}
	if tt.HasText("Links stay with saved sessions") {
		t.Fatal("the replay row still explains itself")
	}
	a.flows = nil
	a.applyFilter()
	tt.Frame()
	if !tt.HasText("Capture is stopped") || !tt.HasText("Start proxy") || !tt.HasText("Set up a client…") {
		t.Fatalf("the empty capture does not offer what it can do: %q", tt.Texts())
	}
}

// The sidebar's own list is a source list: one row chosen, the keys moving the
// choice, the rows read as a tree, and the region around it scrolling on its
// own rather than the list.
func TestSidebarListChoosesWithTheKeysAndReadsAsATree(t *testing.T) {
	a := workspaceApp()
	a.iconLookup = func(string) ([]byte, bool) { return nil, false }
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if err := tt.Click("Errors"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.sidebarChoice != "errors" {
		t.Fatalf("a click chose %q", a.sidebarChoice)
	}
	// The rows are one list: the arrows move from the chosen one, whatever
	// section it is in.
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if a.sidebarChoice != "slow" {
		t.Fatalf("Down chose %q, want the row after Errors", a.sidebarChoice)
	}
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	if a.sidebarChoice != "all" {
		t.Fatalf("Home chose %q", a.sidebarChoice)
	}
	// Assistive technology reads the rows as items of a tree.
	if _, ok := tt.AccessByRole(ui.RoleTreeItem, "All requests 2"); !ok {
		t.Fatalf("the rows are not tree items: %v", tt.Texts())
	}
	// A row's label brightens under the pointer without moving.
	label, ok := tt.Find("Pinned")
	if !ok {
		t.Fatal("no Pinned row")
	}
	before := tt.Image().RGBAAt(int(label.X+2), int(label.Y+label.H/2))
	tt.Move(label.X+label.W/2, label.Y+label.H/2)
	tt.Frame()
	after := tt.Image().RGBAAt(int(label.X+2), int(label.Y+label.H/2))
	if before == after {
		t.Fatalf("the row did not brighten under the pointer: %v", before)
	}
	if moved, ok := tt.Find("Pinned"); !ok || moved != label {
		t.Fatalf("the hover moved the row: %v → %v", label, moved)
	}
}

// An app's row shows the app's own icon, where a host has its dot.
func TestAppsRowsShowTheAppIcon(t *testing.T) {
	a := workspaceApp()
	a.sourcesApps = true
	a.clients = []domain{{name: "Safari", n: 3}}
	a.clientApps["Safari"] = []capture.SSLApp{{Name: "Safari", Path: "/Applications/Safari.app/Contents/MacOS/Safari"}}
	a.iconLookup = func(path string) ([]byte, bool) {
		if !strings.Contains(path, "Safari.app") {
			return nil, false
		}
		img := image.NewRGBA(image.Rect(0, 0, 16, 16))
		for i := range img.Pix {
			img.Pix[i] = 0xff
		}
		var buf bytes.Buffer
		png.Encode(&buf, img)
		return buf.Bytes(), true
	}
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	row, ok := tt.Find("Safari")
	if !ok {
		t.Fatalf("no Safari row: %q", tt.Texts())
	}
	// The icon sits in the row's slot, left of the name.
	im := tt.Image()
	pixel := im.RGBAAt(int(row.X-16), int(row.Y+row.H/2))
	if pixel.R < 0xf0 || pixel.G < 0xf0 || pixel.B < 0xf0 {
		t.Fatalf("the app icon is not drawn in the row: %v at %v", pixel, row)
	}
}

// Sessions are the capture's history, not a collection: they are rows of the
// traffic region, above the collections, and one click opens the session.
func TestSessionsLiveWithTheTraffic(t *testing.T) {
	a := workspaceApp()
	a.sessions.items = []*savedSession{{key: "k1", data: sessionFile{Title: "Morning evidence"}, count: 3, loaded: true}}
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	row, ok := tt.Find("Morning evidence")
	if !ok {
		t.Fatalf("the session is not in the sidebar: %q", tt.Texts())
	}
	collections, ok := tt.Find("Collections")
	if !ok || row.Y > collections.Y {
		t.Fatalf("the session sits at %v, not above the collections at %v", row.Y, collections.Y)
	}
	if err := tt.Click("Morning evidence"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.page != pageSession || a.sessions.active != "k1" {
		t.Fatalf("the session row opened page %v, active %q", a.page, a.sessions.active)
	}
	// The footer's rows open windows and pages, not sections: no disclosure
	// arrows, unlike the rows above them.
	if _, ok := tt.Find("›"); ok {
		t.Fatal("the sidebar's footer still carries disclosure arrows")
	}
}

// An empty Collections tree offers a request rather than a sentence about
// where one would go, and the header does not offer the same thing twice: with
// nothing saved the row in the tree is the way in, and a saved request puts the
// header's button back.
func TestEmptyCollectionsOfferARequestRow(t *testing.T) {
	a := workspaceApp()
	a.requests, a.folders, a.documents = nil, nil, nil
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	row, ok := tt.Find("New request")
	search, okSearch := tt.Find("Search collections")
	if !ok || !okSearch {
		t.Fatalf("no New request row: %q", tt.Texts())
	}
	if row.Y <= search.Y {
		t.Fatalf("New request is at %v, not in the tree below the search field at %v", row.Y, search.Y)
	}
	if err := tt.Click("New request"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.page != pageRequest || a.requestDocument == nil {
		t.Fatalf("the row opened page %v with document %v", a.page, a.requestDocument != nil)
	}
	a.requests = []savedRequest{{Name: "One", Collection: "Workspace", Method: "GET", URL: "https://example.test"}}
	a.page = pageLive
	tt.Frame()
	row, ok = tt.Find("New request")
	search, _ = tt.Find("Search collections")
	if !ok || row.Y >= search.Y {
		t.Fatalf("with a collection saved, New request is at %v, not in the header above %v", row.Y, search.Y)
	}
}
