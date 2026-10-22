package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/desktop/internal/capture"
	"github.com/egoist/mygo/ui"
	"github.com/gorilla/websocket"
)

func workspaceApp() *app {
	a := &app{
		filter: "all", sidebarWidth: 220, tablePercent: 56,
		trafficOpen: true, domainsOpen: true, clientsOpen: true, historyOpen: true,
		collectionOpen: make(map[string]*bool), icons: make(map[string]ui.ImageSource),
		detailFor: -1, frames: make(map[int][]frame),
		flows: []flow{
			{id: 1, method: "GET", scheme: "https", host: "api.example.com", path: "/projects", url: "https://api.example.com/projects", status: 200, process: "Browser", reqHeader: http.Header{"Accept": {"application/json"}}, respHeader: http.Header{"Content-Type": {"application/json"}}, resBody: `{"projects":[]}`},
			{id: 2, method: "POST", scheme: "https", host: "api.example.com", path: "/login", url: "https://api.example.com/login", status: 401, process: "Browser"},
		},
		requests: []savedRequest{{Name: "List projects", Collection: "Workspace/API", Method: "GET", URL: "https://api.example.com/projects", Pre: `req.set("Accept", "application/json")`}},
		history:  []historyEntry{{Method: "POST", URL: "https://api.example.com/login"}},
	}
	a.countDomains()
	a.countClients()
	a.applyFilter()
	return a
}

func TestWorkspaceKeepsCaptureAndDraftIndependent(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 960, 600)
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
	if !tt.HasText("401") || tt.HasText("List projects") {
		t.Fatal("Capture should show its filtered traffic, not the request library")
	}
	if err := tt.RightClick("api.example.com/login"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Edit & resend"); err != nil {
		t.Fatal(err)
	}
	if value("URL") != "https://api.example.com/login" || !tt.HasText("Save request") {
		t.Fatal("captured request did not open in the Requests workspace")
	}
	click("List projects")
	if value("URL") != "https://api.example.com/projects" || tt.HasText("Domains") {
		t.Fatal("Requests should show the saved draft, not capture filters")
	}
	click("URL")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("https://api.example.com/projects?limit=7")
	click("Method")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	click("Scripts")
	click("Pre-request")
	click("Capture")
	if !tt.HasText("401") || tt.HasText("200") {
		t.Fatal("switching workspaces lost the error filter")
	}
	click("Requests")
	if url, script := value("URL"), value("Pre-request script"); url != "https://api.example.com/projects?limit=7" || a.composeMethod != "" || script != `req.set("Accept", "application/json")` {
		t.Fatalf("switching workspaces changed the draft: URL %q, method %q, script %q", url, a.composeMethod, script)
	}
	click(a.history[0].label())
	if value("URL") != "https://api.example.com/login" {
		t.Fatal("history did not open in the request editor")
	}
}

func TestCaptureUIUsesOnlyTheRunningListener(t *testing.T) {
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a := workspaceApp()
	a.srv, a.flows, a.visible = srv, nil, nil
	tt := ui.NewTester(a.view, 1240, 800)
	if !tt.HasText("Capture stopped") || tt.HasText("Live · "+srv.Addr()) {
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
	if !tt.HasText("Live · "+srv.Addr()) || !tt.HasText("Set your HTTP proxy to "+srv.Addr()) {
		t.Fatalf("capture must show its actual bound address, texts: %q", tt.Texts())
	}
	for _, label := range []string{"Settings", "Proxy"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	if !tt.HasText("Proxy address: " + srv.Addr()) {
		t.Fatal("settings did not use the actual bound address")
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Capture is not running") || tt.HasText("Proxy address: "+srv.Addr()) {
		t.Fatal("closed proxy must not advertise its old address")
	}
}

func TestSettingsOpensExistingEditors(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 960, 600)
	for _, label := range []string{"Settings", "Proxy", "Configure upstream…"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	if !tt.HasText("Upstream proxy") {
		t.Fatal("settings did not open the upstream editor")
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Settings", "Certificates", "TLS key log…"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	if !tt.HasText("TLS key log") {
		t.Fatal("settings did not open the TLS key log editor")
	}
}

func TestRequestTabsKeepDraftsAndConfirmClose(t *testing.T) {
	a := workspaceApp()
	a.requestsPath = filepath.Join(t.TempDir(), "requests.json")
	tt := ui.NewTester(a.view, 1100, 650)
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
	click("Requests")
	edit("Name", "Draft A")
	edit("URL", "https://example.test/a?limit=7")
	click("Body")
	edit("Request body", `{"side":"A"}`)
	click("Add request tab")
	edit("Name", "Draft B")
	edit("URL", "https://example.test/b")
	click("Request tab: Draft A")
	click("URL")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != "https://example.test/a?limit=7" {
		t.Fatal("switching tabs lost Draft A's URL")
	}
	click("Request body")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != `{"side":"A"}` {
		t.Fatal("switching tabs lost Draft A's body or editor selection")
	}
	click("Close Draft A")
	if !tt.HasText("Save changes?") {
		t.Fatal("dirty tab closed without confirmation")
	}
	click("Cancel")
	click("Close Draft A")
	// A failed save must not silently close or mark the draft as saved.
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a.requestsPath = filepath.Join(blocked, "requests.json")
	click("Save & close")
	if !tt.HasText("Save changes?") {
		t.Fatal("failed save discarded the draft")
	}
	a.requestsPath = filepath.Join(t.TempDir(), "requests.json")
	click("Save & close")
	list, err := loadRequests(a.requestsPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range list {
		if r.Name == "Draft A" && r.Body == `{"side":"A"}` && r.URL == "https://example.test/a?limit=7" {
			found = true
		}
	}
	if !found || tt.HasText("Request tab: Draft A") {
		t.Fatal("save-and-close failed to persist or close Draft A")
	}
	click("Close Draft B")
	click("Discard")
	if !tt.HasText("Open a saved request") {
		t.Fatal("closing the last tab did not show the empty workspace")
	}
	click("Draft A")
	if tt.HasText("Unsaved changes") {
		t.Fatal("saved request reopened dirty")
	}
	edit("URL", "https://example.test/edited")
	click("Add request tab")
	click("Draft A")
	click("URL")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != "https://example.test/edited" {
		t.Fatal("reopening a saved request replaced its existing unsaved tab")
	}
	for i := range 8 {
		click("Add request tab")
		edit("Name", fmt.Sprintf("Extra %d", i))
	}
	edit("Name", "Endpoint 8 with a longer name")
	click("Close Endpoint 8 with a longer name")
	if !tt.HasText("Save changes?") {
		t.Fatal("the renamed tab's close button was hidden beyond the scroll area")
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
		{Name: "Slow request", Method: "GET", URL: upstream.URL, Headers: "X-Owner: slow", Post: `test("slow owns response", res.json().owner === "slow"); vars.set("slowDone", "yes")`},
		{Name: "Fast request", Method: "GET", URL: upstream.URL, Headers: "X-Owner: fast", Post: `test("fast owns response", res.json().owner === "fast"); vars.set("fastDone", "yes")`},
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
	click("Requests")
	click("Slow request")
	click("Send")
	select {
	case <-slowStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("slow request never reached the proxy")
	}
	// Editing a script during a send must not change that send's assertions.
	click("Scripts")
	click("Post-response")
	click("Post-response script")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(`test("wrong script", false)`)
	click("Fast request")
	click("Send")
	waitFor("201 Created")
	click("Test results")
	if !tt.HasText("✓ fast owns response") {
		t.Fatal("fast request ran another tab's script or saw its response")
	}
	click("Active environment")
	click("Staging")
	releaseSlow <- struct{}{}
	click("Request tab: Slow request")
	waitFor("200 OK")
	click("Test results")
	if !tt.HasText("✓ slow owns response") || tt.HasText("wrong script") {
		t.Fatal("slow result used the active tab or a script edited after sending")
	}
	click("Response")
	click("Response body")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if !strings.Contains(tt.Clipboard(), `"slow"`) {
		t.Fatal("slow response body was not retained")
	}
	click("Request tab: Fast request")
	if !tt.HasText("201 Created") || tt.HasText("200 OK") || !tt.HasText("✓ fast owns response") {
		t.Fatal("slow completion overwrote the fast tab")
	}
	click("Variables…")
	click("Environment variables")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != "keep = staging" {
		t.Fatal("completion wrote variables to the newly selected environment")
	}
	click("Cancel")
	click("Active environment")
	click("Local")
	click("Variables…")
	click("Environment variables")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != "fastDone = yes\nkeep = original\nslowDone = yes" {
		t.Fatalf("concurrent script updates lost variables: %q", tt.Clipboard())
	}
	click("Cancel")
	click("Scripts")
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
	a.requestsPath = filepath.Join(t.TempDir(), "requests.json")
	a.apisPath = filepath.Join(t.TempDir(), "apis.json")
	tt := ui.NewTester(a.view, 1100, 700)
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
	if err := tt.ChooseMenuItem("Edit & resend"); err != nil {
		t.Fatal(err)
	}
	edit("Collection", "Captured API")
	edit("Name", "Captured projects")
	click("Save request")
	click("APIs")
	click("Generate from collection")
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
	click("New API")
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
	click("Save request")
	click("APIs")
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
	tt := ui.NewTester(a.view, 960, 600)
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
	click("Requests")
	click("List projects")
	click("Import cURL…")
	paste("curl https://example.test --data @private.txt")
	if !tt.HasText("@files and stdin are not read") {
		t.Fatal("file-backed input was not explained or was imported")
	}
	click("Cancel")
	if read("URL") != "https://api.example.com/projects" {
		t.Fatal("failed import replaced the existing draft")
	}
	click("Import cURL…")
	paste(`curl 'https://example.test/api?x=7' -H 'X-Count: 19' --json '{"name":"it'\''s","count":7}'`)
	if read("Method") != "POST" || read("URL") != "https://example.test/api?x=7" || !strings.Contains(read("Request headers"), "X-Count: 19") {
		t.Fatal("imported request fields were not shown in a new tab")
	}
	click("Body")
	if read("Request body") != `{"name":"it's","count":7}` {
		t.Fatal("import changed the body quoting")
	}
	click("Copy as cURL")
	command := tt.Clipboard()
	if !strings.Contains(command, "--data-raw") || !strings.Contains(command, "X-Count: 19") {
		t.Fatal("copy did not use the active draft")
	}
	click("List projects")
	if read("URL") != "https://api.example.com/projects" {
		t.Fatal("successful import overwrote another document")
	}
	click("Import cURL…")
	paste(command)
	click("Body")
	if read("Request body") != `{"name":"it's","count":7}` || read("URL") != "https://example.test/api?x=7" {
		t.Fatal("copied cURL did not round-trip through the import UI")
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
	click("Rules")
	click("Save rule")
	if !tt.HasText("rule 1: enter a URL substring to match") || len(load()) != 0 {
		t.Fatal("invalid rule was saved or validation was hidden")
	}
	edit("Rule name", "Development API")
	edit("URL contains", "/projects")
	edit("Target base URL", "http://127.0.0.1:8081")
	click("Save rule")
	click("+ New rule")
	click("Rule type")
	click("Block")
	edit("Rule name", "Block projects")
	edit("URL contains", "/projects")
	click("Save rule")
	click("Move up")
	click("Enable rule 1")
	rules := load()
	if len(rules) != 2 || rules[0].Type != capture.RuleBlock || !rules[0].Disabled || rules[1].Name != "Development API" {
		t.Fatalf("order/toggle not persisted: %+v", rules)
	}
	click("Delete rule…")
	click("Delete")
	if rules = load(); len(rules) != 1 || rules[0].Name != "Development API" {
		t.Fatalf("delete not persisted: %+v", rules)
	}
	click("Close")
	if err := tt.RightClick("api.example.com/projects"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Create rule", "Mock this response…"); err != nil {
		t.Fatal(err)
	}
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
	click("Close")
	click("Rules")
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
	click("Settings")
	click("Proxy")
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
	click("Checks")
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
	click("Close")
	click("Network · LAN")
	if err := tt.ChooseMenuItem("Connect a device…"); err != nil {
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
