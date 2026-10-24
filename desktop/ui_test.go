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
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/desktop/internal/capture"
	"github.com/baba983/vscroxy/desktop/internal/system"
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

func TestBreakpointDialogValidatesEditsAndKeepsOriginalActionReachable(t *testing.T) {
	a := workspaceApp()
	a.bp = &capture.Breakpoint{ID: 1, Method: "POST", URL: "https://api.example.com/login"}
	a.bpOpen, a.bpMethod, a.bpURL = true, "POST", a.bp.URL
	a.bpHeaders, a.bpAbort = "Authorization: Bearer old", "502 Bad Gateway"
	a.setMessage("Previous breakpoint continued")
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
	if err := tt.Click("Explain"); err != nil {
		t.Fatal(err)
	}
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
	a.pending = []capture.Flow{{ID: 11, Method: "GET", Scheme: "https", Host: "other.example.com", Path: "/gateway", Status: 502, Kind: "http"}}
	tt.Frame()
	if !tt.HasText("Client TLS handshake failed") {
		t.Fatal("new traffic closed the explanation for the inspected request")
	}
	tt.Key(0, ui.KeyEscape)
	if err := tt.Click("Errors"); err != nil {
		t.Fatal(err)
	}
	if len(a.visible) != 2 {
		t.Fatal("TLS failure missing from Errors filter")
	}
	if err := tt.Click("Explain"); err != nil {
		t.Fatal(err)
	}
	// The popover can cover the URL column; use the exposed method cell.
	if err := tt.Click("GET"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Client TLS handshake failed") || tt.HasText("Explain") || !tt.HasText("502 · — · 0 ms") {
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
			if !tt.HasText(tc.title) || !tt.HasText(raw) {
				t.Fatalf("wrong explanation for stage %q: %q", tc.stage, tt.Texts())
			}
			if box, ok := tt.Find("Possible causes · not confirmed"); !ok || box.H <= 0 {
				t.Fatal("explanation content is clipped out of the popover")
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
	if err := tt.Click("Explain"); err != nil {
		t.Fatal(err)
	}
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
	for i := range maxFlows {
		path := fmt.Sprintf("/capture/%04d", i+1)
		a.flows = append(a.flows, flow{id: i + 1, method: "GET", scheme: "https", host: "api.example.com", path: path, url: "https://api.example.com" + path, status: 200})
	}
	a.applyFilter()
	tt := ui.NewTester(a.view, 1280, 800)
	row := "api.example.com/capture/0001"
	box, ok := tt.Find("api.example.com/capture/5000")
	if !ok {
		t.Fatal("a capture view must start at the latest request")
	}
	x, y := box.X+10, box.Y+10
	tt.Scroll(x, y, 0, -1e6)
	tt.Scroll(x, y, 0, 640)
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
	if err := tt.Click("Edit & replay"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Back to traffic"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("api.example.com/capture/0021") || !tt.HasText("GET https://api.example.com/capture/0021") || !tt.HasText("Paused · Latest") {
		t.Fatal("the editing round trip changed the list position, follow state or inspected request")
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
	if !tt.HasText("Paused · Latest") {
		t.Fatal("the paused follow state is not visible")
	}
	if err := tt.Click(row); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Latest"); err != nil {
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
	for _, label := range []string{"Settings", "Proxy"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	tt.Scroll(480, 300, 0, 500)
	if err := tt.Click("Configure upstream…"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Upstream proxy") {
		t.Fatal("settings did not open the upstream editor")
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Settings", "Proxy"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	tt.Key(0, ui.KeyRight) // HTTPS tab, not Capture's HTTPS filter chip.
	tt.Scroll(480, 300, 0, 500)
	if err := tt.Click("TLS key log…"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("TLS key log") {
		t.Fatal("settings did not open the TLS key log editor")
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
	if !tt.HasText("Encrypted tunnel — not individual requests") || !tt.HasText("Domain decryption disabled") || !tt.HasText("Trust not verified on this system") {
		t.Fatalf("tunnel setup missing or an uninstalled CA was reported trusted: %q", tt.Texts())
	}
	click("Enable for dc.dlife.cn:18443")
	if !tt.HasText("Domain rule enabled · reconnect to inspect new requests") || !tt.HasText("Encrypted tunnel — not individual requests") {
		t.Fatal("enabling a domain must not make the old CONNECT appear decrypted")
	}
	cfg, err := capture.LoadSSL(a.sslPath)
	if err != nil || !cfg.Decrypts("api.dc.dlife.cn") || cfg.Decrypts("other.dlife.cn") || cfg.All || !srv.SSL().Decrypts("dc.dlife.cn") || srv.SSL().All {
		t.Fatalf("domain selection was not saved with the correct scope: %+v, %v", cfg, err)
	}
	click("Set up certificate…")
	waitCertificate()
	if !tt.HasText("HTTPS inspection") || !tt.HasText("Trust not verified on this system") || !tt.HasText("Proxy listening · "+srv.Addr()) || !tt.HasText("dc.dlife.cn") {
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
	cfg, err = capture.LoadSSL(a.sslPath)
	if err != nil || !cfg.Decrypts("other.dlife.cn") || cfg.Decrypts("notdlife.cn") || !tt.HasText("Domain rule enabled · reconnect to inspect new requests") {
		t.Fatal("a parent-domain rule must enable its subdomains in the inspector too")
	}
}

func TestHTTPSAppRuleCanBeScopedEditedAndCancelled(t *testing.T) {
	a := workspaceApp()
	a.sslPath = filepath.Join(t.TempDir(), "ssl.json")
	a.sslHosts = "existing.test"
	a.certificate.checked = true
	path := filepath.Join(t.TempDir(), "client")
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
		cfg, err := capture.LoadSSL(a.sslPath)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	click("Enable HTTPS for client")
	cfg := load()
	if cfg.All || len(cfg.Apps) != 1 || cfg.Apps[0].Path != path || !cfg.Decrypts("existing.test") || !cfg.DecryptsProcess("another.test:9443", path) || cfg.DecryptsProcess("another.test", "") {
		t.Fatalf("app rule changed domain/global scope or lost its executable: %+v", cfg)
	}
	if !tt.HasText("App rule enabled · reconnect to inspect new requests") || !tt.HasText("Encrypted tunnel — not individual requests") {
		t.Fatal("enabling an app incorrectly claimed this historical tunnel was decrypted")
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
	if tt.HasText("Enable HTTPS for client") || !tt.HasText("App rule unavailable · local executable identity not recorded") {
		t.Fatal("an unidentified client was offered a same-name app permission")
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
	if !tt.HasText("2 flows · 2 shown") {
		t.Fatal("reset did not restore the captured requests")
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
	if !tt.HasText("2 flows · 1 shown") {
		t.Fatalf("comma-separated alternatives did not match the 401 response: %q", tt.Texts())
	}
}

func TestBodyAutomaticallyChoosesAUsableMode(t *testing.T) {
	a := workspaceApp()
	a.flows[0].respHeader.Set("Content-Type", "application/octet-stream")
	a.flows[0].resBody = "\x00\xffbinary"
	a.tab = 1
	tt := ui.NewTester(a.view, 1280, 800)
	if !tt.HasText("Showing: Hex") {
		t.Fatal("binary body must not default to text")
	}
	if err := tt.Click("Hex"); err != nil {
		t.Fatal(err)
	}
	a.flows[1].respHeader = http.Header{"Content-Type": {"application/json"}}
	a.flows[1].resBody = `{"answer":42}`
	a.picked = 1
	tt.Frame()
	if !tt.HasText("Showing: Pretty") || tt.HasText("Showing: Hex") {
		t.Fatal("switching requests kept an unusable body mode")
	}
}

func TestBodyTextFindKeepsContextAndCannotEditCapture(t *testing.T) {
	a := workspaceApp()
	a.tab, a.tablePercent = 1, 25
	body := "before\nneedle first\nbetween\nneedle last\nafter"
	a.flows[0].resBody = body
	a.flows[0].respHeader.Set("Content-Type", "text/plain")
	tt := ui.NewTester(a.view, 960, 600)
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
	a.tab, a.tablePercent = 1, 25
	a.flows[0].resBody, a.flows[0].resError = `{"z":9007199254740993,"a":false}`, "transfer stopped"
	tt := ui.NewTester(a.view, 960, 600)
	if !tt.HasText("… incomplete body: transfer stopped") {
		t.Fatal("capture error was hidden")
	}
	if err := tt.Click("Pretty"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyRight)
	if tt.HasText("Body is not JSON; showing it as text") || !tt.HasText("9007199254740993") {
		t.Fatal("warning text corrupted the JSON reader or it rounded the captured ID")
	}
	if err := tt.Click("Copy shown body"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != `{"z":9007199254740993,"a":false}` {
		t.Fatal("copy included display warnings or reformatted the captured bytes")
	}
	if err := tt.Click("Request"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("… incomplete body: transfer stopped") {
		t.Fatal("response warning leaked into the request side")
	}
}

func TestBodyModeSurvivesIncomingTraffic(t *testing.T) {
	a := workspaceApp()
	a.tab = 1
	tt := ui.NewTester(a.view, 1280, 800)
	// Hex renders differently from Auto's Pretty, so a reset is observable.
	if err := tt.Click("Hex"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Showing: Hex") {
		t.Fatal("manual body mode was not selected")
	}
	a.pending = []capture.Flow{{ID: 3, Method: "GET", Scheme: "https", Host: "other.example.com", Path: "/new", Status: 200, Kind: "http"}}
	tt.Frame()
	if !tt.HasText("Showing: Hex") {
		t.Fatal("new traffic reset the manually selected body mode")
	}
	a.pending = []capture.Flow{{ID: 4, Method: "GET", Scheme: "https", Host: "other.example.com", Path: "/another", Status: 200, Kind: "http"}}
	tt.Frame()
	if !tt.HasText("Showing: Hex") {
		t.Fatal("new traffic changed the body mode while inspecting the same request")
	}
	if err := tt.Click("Request"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Showing: Pretty") || tt.HasText("Showing: Hex") {
		t.Fatal("switching body sides must still reset to Auto")
	}
}

func TestJSONExpansionSurvivesIncomingTraffic(t *testing.T) {
	a := workspaceApp()
	a.tab = 1
	// Keep the nested disclosure in the visible detail pane. Tester.Click
	// cannot expand an offscreen row without scrolling it into view first.
	a.tablePercent = 25
	a.flows[0].resBody = `{"collapsed":{"hidden":"hidden-value"},"data":{"nested":{"leaf":"expanded-value"}}}`
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("Pretty"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyRight) // Body's JSON mode, not Capture's JSON filter.
	if !tt.HasText("Showing: JSON") {
		t.Fatal("JSON mode was not selected")
	}
	if err := tt.Click("▸"); err != nil {
		t.Fatal(err)
	}
	box, ok := tt.Find("collapsed")
	if !ok {
		t.Fatal("missing object to collapse")
	}
	tt.ClickAt(box.X-16, box.Y+box.H/2)
	if !tt.HasText(`"expanded-value"`) || tt.HasText(`"hidden-value"`) {
		t.Fatal("JSON nodes did not expand and collapse")
	}
	if err := tt.Click("Edit & replay"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Traffic tab"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText(`"expanded-value"`) || tt.HasText(`"hidden-value"`) || !tt.HasText("Showing: JSON") {
		t.Fatal("returning from editing lost the reader's mode or expanded JSON nodes")
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
	if err := tt.Click("Copy shown body"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != `{"replacement":"new-value"}` || tt.HasText(`"expanded-value"`) {
		t.Fatal("changed body content did not refresh the JSON tree")
	}
}

func TestCaptureKeepsSelectionAcrossListChanges(t *testing.T) {
	a := workspaceApp()
	a.flows = nil
	for id := 1; id <= maxFlows; id++ {
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
	if err := tt.Click("URL"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("URL"); err != nil {
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
	if !tt.HasText("No request chosen") {
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
	a.tab = 2
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
	if err := tt.Click("Body"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Find in text…"); err != nil {
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
	a.setMessage(message)
	tt.Frame()
	if !tt.HasText(message) {
		t.Fatal("feedback was truncated")
	}
	tt.Advance(6 * time.Second)
	if tt.HasText(message) {
		t.Fatal("feedback stayed visible after its timeout")
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
	if !tt.HasText("2 flows · 1 shown") || a.chosen.Has(2) {
		t.Fatal("filtered-out requests remained selected for bulk export")
	}
	if err := tt.Click("Clear"); err != nil {
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
	if err := a.replay(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1280, 800)
	if a.workspace != 0 || !tt.HasText("Inspect replay") {
		t.Fatal("replay stole focus or gave no route to its result")
	}
	if err := tt.Click("Inspect replay"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Source: Capture #1") || a.response == nil || a.response.Status != 201 || a.response.ResponseBody != "result of this replay" {
		t.Fatal("inspect did not retain the exact replay response and source")
	}
}

func TestDiffDistinguishesAnEmptyBaselineFromNoBaseline(t *testing.T) {
	a := workspaceApp()
	a.tab = 2
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
	for _, label := range []string{"Settings", "Proxy"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
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
		if err != nil || got.ProxyPort != port || !tt.HasText(fmt.Sprintf("Next launch: 127.0.0.1:%d · restart the app to apply", port)) {
			t.Fatalf("port %d not saved: %+v, error %v", port, got, err)
		}
		if !srv.Running() || srv.Addr() != addr || !tt.HasText("Proxy address: "+addr) {
			t.Fatal("saving next launch's port interrupted the current capture")
		}
	}
	// Other settings must not overwrite the newly saved port.
	a.dark, a.lastSave = true, time.Time{}
	tt.Frame()
	got, err := loadSettings(a.settingsPath)
	if err != nil || !got.Dark || got.ProxyPort != 9091 {
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
	if err != nil || got.ProxyPort != 0 || !tt.HasText("Next launch: Automatic · restart the app to apply") {
		t.Fatalf("clearing the port must restore automatic selection: %+v, %v", got, err)
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
	tt := ui.NewTester(a.view, 960, 600)
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
		for !tt.HasText("200 OK") && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			tt.Frame()
		}
		if !tt.HasText("200 OK") {
			t.Fatalf("send did not complete: %v", tt.Texts())
		}
	}
	click("Edit & replay")
	edit("URL", upstream.URL+"/items?variant=edited")
	click("Scripts")
	edit("Pre-request script", `req.set("X-Marker", "from-script"); req.body = "sent-body"`)
	click("Post-response")
	edit("Post-response script", `req.set("X-Marker", "after-send"); req.body = "after-send"`)
	click("Send")
	waitForResponse()
	if !tt.HasText("Capture filters kept · new traffic may be hidden") {
		t.Fatal("filtered traffic can hide the send without explanation")
	}
	// Later edits must not change what the last send actually compared.
	edit("URL", upstream.URL+"/items?variant=not-sent")
	click("Compare source")
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
	click("Back to traffic")
	if !tt.HasText("URL: original ×") || !tt.HasText("Status: 200 ×") || !tt.HasText("HTTP ×") || !tt.HasText("POST http://"+a.flows[0].host+"/items?variant=original") {
		t.Fatalf("return changed capture conditions or inspected source: %v", tt.Texts())
	}
	click("Request tab: /items?variant=original")
	if !tt.HasText("Compare source") || !strings.Contains(copyText("New text"), "X-Marker: from-script") {
		t.Fatal("returning to the draft lost its comparison")
	}
	click("Response")
	if got := copyText("URL"); got != upstream.URL+"/items?variant=not-sent" {
		t.Fatalf("switching workspaces lost unsent edits: %q", got)
	}
	click("Send")
	waitForResponse()
	click("Compare source")
	click("Compare content")
	click("Response body")
	if got := copyText("New text"); got != "variant=not-sent; marker=from-script; body=sent-body" {
		t.Fatalf("second send kept a stale comparison: %q", got)
	}
	click("Traffic tab")
	if !tt.HasText("URL: original ×") || !tt.HasText("Status: 200 ×") {
		t.Fatal("Traffic tab reset the live view")
	}
	click("New view")
	click("Close view: View 1")
	click("Request tab: /items?variant=original")
	click("Back to traffic")
	tt.Frame()
	if !tt.HasText("Original capture view is no longer open; returned to the current traffic view") || !tt.HasText("2 flows · 2 shown") {
		t.Fatal("closed source view was silently resurrected or the fallback was unexplained")
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

func TestCaptureRegressionCanBeSavedAndSharedOnlyAfterReview(t *testing.T) {
	a := workspaceApp()
	a.requestsPath = filepath.Join(t.TempDir(), "requests.json")
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
	if err := tt.ChooseMenuItem("Save as regression…"); err != nil {
		t.Fatal(err)
	}
	if a.workspace != 1 || !tt.HasText("RESPONSE CHECKS") || a.sourceFlow != 1 || a.composeWaiting {
		t.Fatal("capture did not become an unsent regression draft")
	}
	click("Save request")
	loaded, err := loadRequests(a.requestsPath)
	if err != nil || len(loaded) != 2 || loaded[0].Checks.Status != "200" {
		t.Fatalf("checks not saved: %+v %v", loaded, err)
	}
	click("Case")
	if err := tt.ChooseMenuItem("Share regression…"); err != nil {
		t.Fatal(err)
	}
	click("Copy case JSON")
	if tt.Clipboard() != "" {
		t.Fatal("unreviewed case copied")
	}
	click("Confirm regression review")
	click("Copy case JSON")
	shared := tt.Clipboard()
	if _, err := decodeRegression([]byte(shared)); err != nil || strings.Contains(shared, "api.example.com") {
		t.Fatalf("reviewed clipboard not a sanitized case: %s %v", shared, err)
	}
	click("Regression JSON preview")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.SetClipboard(shared + "\n")
	tt.Key(ui.Cmd, ui.KeyV)
	tt.SetClipboard("not copied")
	click("Copy case JSON")
	if tt.Clipboard() != "not copied" {
		t.Fatal("editing the preview did not revoke review approval")
	}
	click("Cancel")
	a.regressionUI = regressionShare{open: true, importing: true, preview: shared}
	tt.Frame()
	count := len(a.documents)
	click("Open unsent case")
	if len(a.documents) != count {
		t.Fatal("unreviewed import opened")
	}
	click("Confirm regression review")
	click("Open unsent case")
	if len(a.documents) != count+1 || a.composeWaiting || a.response != nil || a.composeURL != "{{base_url}}/projects" {
		t.Fatal("import should open only an unsent, parameterized draft")
	}
}

func TestCaptureSelectionSummarizesAndComparesArbitraryRequests(t *testing.T) {
	a := workspaceApp()
	a.tablePercent = 35
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
	if err := tt.ChooseMenuItem("#3 · /redirected · 200 · 0 ms"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Replay of #1") {
		t.Fatal("could not navigate back to replay")
	}
	if err := tt.Click("Capture view: View 1"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("URL: redirected ×") || !tt.HasText("Replay of #1") {
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
	tt.Click("Session")
	if err := tt.ChooseMenuItem("Replay chosen…"); err != nil {
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
	if !tt.HasText("2/2 sent · 0 failed") || a.workspace != 0 {
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
		if err := tt.Click(field.label); err != nil {
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
	tt.Click("Saved filters…")
	tt.Click("Filter name")
	tt.Key(ui.Ctrl, ui.KeyA)
	tt.Type("Checkout failures")
	tt.Click("Save filter")
	tt.Click("Close")
	filters, err := loadCaptureFilters(a.views.path)
	if err != nil || len(filters) != 1 || filters[0].Triage.Color != "Red" || filters[0].Triage.Duration != ">500" {
		t.Fatalf("triage conditions not saved: %+v %v", filters, err)
	}
	tt.Click("New view")
	if len(a.visible) != 6 {
		t.Fatal("new view inherited triage")
	}
	if err := tt.Click("Quick filter: Checkout failures"); err != nil {
		t.Fatal(err)
	}
	if len(a.visible) != 1 {
		t.Fatal("one-click filter not applied")
	}
	tt.Click("Quick filter: Checkout failures")
	if len(a.visible) != 6 {
		t.Fatal("one-click filter could not be toggled off")
	}
	a.views.activate(a, 0)
	if len(a.visible) != 1 {
		t.Fatal("quick filter altered the other view")
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
	a.clientUI.url = upstream.URL
	// An imported capture with the same URL must never prove live setup.
	a.flows = []flow{{id: 9, scheme: "https", url: upstream.URL, at: time.Now(), reqHeaders: "GET /"}}
	tt := ui.NewTester(a.view, 960, 600)
	if !tt.HasText("Decrypted HTTPS: not observed for this host since opening setup.") {
		t.Fatal("imported traffic was mistaken for setup evidence")
	}
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
	if !tt.HasText("Decrypted HTTPS observed · #1 · / · 200") || !tt.HasText("Client CA trust: not verified here. A captured request can also come from a client that skips verification.") {
		t.Fatalf("capture evidence missing or overstated trust: %v", tt.Texts())
	}
}

func TestDesktopLayoutDefaultsBelowAndSwitchesWithoutLosingCaptureContext(t *testing.T) {
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
	check := func(right bool) {
		t.Helper()
		method, ok := tt.Find("Method")
		title, found := tt.Find("POST https://api.example.com/login")
		if !ok || !found || title.H == 0 || title.Y >= 600 || (title.X > method.X+300) != right {
			t.Fatalf("inspector not reachable in expected layout: method %v, title %v, right %t", method, title, right)
		}
		if f, ok := a.current(); !ok || f.id != 2 || !a.chosen.Has(2) || !tt.HasText("Pretty") {
			t.Fatal("layout transition lost the inspected request, selection or Body tab")
		}
	}
	check(false) // A: bottom inspector is the default even on wide windows.
	click("View")
	if err := tt.ChooseMenuItem("Inspector: Right (wide windows)"); err != nil {
		t.Fatal(err)
	}
	check(true)
	tt.SetSize(960, 600)
	check(false)
	click("Focus")
	if !tt.HasText("Back to list") || tt.HasText("All requests") {
		t.Fatal("focus did not give the inspector the content area")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyEnter)
	check(false)
	tt.SetSize(1440, 860)
	check(true)
	click("View")
	if err := tt.ChooseMenuItem("Inspector: Bottom"); err != nil {
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
}

func TestCommandPaletteKeyboardAndDisabledActions(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 960, 600)
	tt.Key(ui.Cmd, ui.KeyK)
	if !tt.Focused("Find command") {
		t.Fatal("palette search did not take focus")
	}
	tt.Type("Inspector:")
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
		t.Fatalf("arrow navigation did not choose Inspector: Bottom: layout %q, command %d, method %v, title %v", a.workbench.layout, a.workbench.command, method, title)
	}
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("not a command")
	if !tt.HasText("No matching commands") {
		t.Fatal("empty command results were not explained")
	}
	tt.Key(0, ui.KeyEscape)
	if err := tt.Click("Requests"); err != nil {
		t.Fatal(err)
	}
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
	tt.Type("Compare source")
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
	for range 20 {
		if tt.Focused("Request headers") {
			break
		}
		tt.Key(0, ui.KeyTab)
	}
	if !tt.Focused("Request headers") {
		t.Fatal("request headers were not keyboard accessible")
	}
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
	tt.Key(ui.Cmd, ui.KeyW)
	if !tt.HasText("Sending…") || tt.HasText("Save changes?") || sends.Load() != 1 {
		t.Fatal("in-flight send could be repeated or its document closed")
	}
	release <- struct{}{}
	deadline := time.Now().Add(time.Minute)
	for !tt.HasText("200 OK") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		tt.Frame()
	}
	if !tt.HasText("200 OK") || sends.Load() != 1 {
		t.Fatalf("send did not complete once: %v", tt.Texts())
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyD)
	if !tt.HasText("Left Source #2") || !tt.HasText("Right Last send") {
		t.Fatal("keyboard comparison did not show the exact source and send")
	}
	tt.Key(ui.Cmd, ui.KeyBracketLeft)
	if !tt.Focused("Captured requests") || !tt.HasText("URL: api.example.com ×") || !tt.HasText("POST https://api.example.com/login") {
		t.Fatal("keyboard return lost capture focus, filter or source selection")
	}
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyTab)
	if !tt.Focused("URL") || !tt.HasText("Right Last send") {
		t.Fatal("cycling back to the draft lost its comparison")
	}
	tt.Key(ui.Cmd, ui.KeyW)
	if !tt.HasText("Save changes?") {
		t.Fatal("keyboard close discarded unsaved changes")
	}
	tt.Key(0, ui.KeyEscape)
	if !tt.Focused("URL") || !tt.HasText("Right Last send") {
		t.Fatal("cancelling close lost the draft or editor focus")
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
			if err := tt.ChooseMenuItem("Replay"); err != nil {
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
	choose("edit")
	if !tt.Focused("URL") {
		t.Fatal("native edit did not open the focused capture")
	}
	choose("close-request")
	if !tt.HasText("Save changes?") {
		t.Fatal("native close bypassed unsaved draft protection")
	}
	// A native callback queued before the focus changed must be rejected too.
	item := menu.ItemByID("new-request")
	item.Click(item, nil)
	tt.Frame()
	if tt.HasText("Request tab: Untitled request") || !tt.HasText("Save changes?") {
		t.Fatal("native command escaped the modal scope")
	}
	tt.Key(0, ui.KeyEscape)
	if !tt.Focused("URL") {
		t.Fatal("native close cancellation did not restore draft focus")
	}
	choose("back")
	if !tt.Focused("Captured requests") {
		t.Fatal("native Back did not restore capture focus")
	}
}

func TestFilterTokensRemoveOnlyTheirOwnCondition(t *testing.T) {
	a := workspaceApp()
	a.views.init(a, "")
	a.query, a.filterStatus = "login", "4xx"
	a.filterTriage.Method = "POST"
	a.applyFilter()
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Status: 4xx ×"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("URL: login ×") || !tt.HasText("Method: POST ×") || tt.HasText("Status: 4xx ×") || len(a.visible) != 1 {
		t.Fatal("removing status cleared other conditions or left a stale token")
	}
	if err := tt.Click("Reset all"); err != nil {
		t.Fatal(err)
	}
	if len(a.visible) != 2 || tt.HasText("URL: login ×") || tt.HasText("Method: POST ×") {
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
	if err := tt.Click("Body: projects ×"); err != nil {
		t.Fatal(err)
	}
	if len(a.visible) != 2 || a.filtersActive() {
		t.Fatal("body token did not remove the full-body search condition")
	}
}

func TestCaptureDiagnosticsSeparateConfigurationLiveEvidenceAndViewVisibility(t *testing.T) {
	fake := &fakeSystemProxy{check: system.ProxyCheck{Status: system.ProxyMatches}}
	a := proxyTestApp(t, fake)
	a.flows = []flow{
		{id: 1, live: true, method: "CONNECT", scheme: "https", url: "https://API.example.com:443/", kind: "connect", status: 200},
		{id: 2, live: true, method: "GET", scheme: "https", url: "https://api.example.com/items", kind: "http", status: 502, reqHeaders: "Accept: */*", conn: conn{errorStage: "TCP", error: "fixture connection refused"}, actions: []string{"Map Remote · fixture"}},
		{id: 3, live: false, method: "GET", scheme: "https", url: "https://api.example.com/imported", status: 200, reqHeaders: "Accept: */*"},
		{id: 4, live: true, method: "GET", scheme: "https", url: "https://api.example.com:8443/other-port", status: 200, reqHeaders: "Accept: */*"},
		{id: 5, live: true, method: "GET", scheme: "http", url: "http://api.example.com:443/other-scheme", status: 200},
	}
	a.views.init(a, "")
	a.query = "no-match"
	a.views.applyFilter(a)
	tt := ui.NewTester(a.view, 960, 600)
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("Capture diagnostics")
	tt.Key(0, ui.KeyEnter)
	settleProxy(t, a, tt)
	if err := tt.Click("Target URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Ctrl, ui.KeyA)
	tt.Type("https://api.example.com/")
	for _, want := range []string{
		"HTTP and HTTPS point at this app",
		"2 live records · 0 shown · 2 hidden in the current capture view",
		"Observed HTTPS: 1 decrypted request records · 1 CONNECT records",
		"HTTPS policy: Disabled for new connections",
		"TCP failed: fixture connection refused",
		"Recorded actions on this exchange: Map Remote · fixture",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %q", want, tt.Texts())
		}
	}
	if fake.writes != 0 || a.deviceUI.gateway != nil {
		t.Fatal("opening diagnostics changed the proxy or exposed a device gateway")
	}
	// Inspecting hidden evidence must preserve the original filter tab.
	tt.Scroll(480, 300, 0, 220)
	if err := tt.Click("Inspect latest evidence"); err != nil {
		t.Fatal(err)
	}
	if a.pickedID != 2 || len(a.views.views) != 2 || a.views.views[0].query != "no-match" || tt.HasText("Capture diagnostics") {
		t.Fatal("inspecting evidence lost the old filter or selected imported/other-origin traffic")
	}
}

func TestCaptureDiagnosticsEmptyAndInvalidTargetsDoNotClaimProxyFailure(t *testing.T) {
	a := workspaceApp()
	a.flows, a.visible = nil, nil
	a.systemProxy.read = func(target string) system.ProxyCheck {
		return system.ProxyCheck{Target: target, Status: system.ProxyOff}
	}
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Diagnose capture…"); err != nil {
		t.Fatal(err)
	}
	settleProxy(t, a, tt)
	if !tt.HasText("No system proxy is set") || !tt.HasText("No retained live evidence for this origin.") {
		t.Fatalf("no-traffic state not explained: %q", tt.Texts())
	}
	for _, target := range []string{"not a URL", "ftp://example.com", "https://user:secret@example.com"} {
		if err := tt.Click("Target URL"); err != nil {
			t.Fatal(err)
		}
		tt.Key(ui.Ctrl, ui.KeyA)
		tt.Type(target)
		if !tt.HasText("Enter an HTTP or HTTPS URL without embedded credentials.") || tt.HasText("0 live records") {
			t.Fatalf("invalid target %q received a diagnosis", target)
		}
	}
	tt.Key(0, ui.KeyEscape)
	if tt.HasText("Target URL") || !tt.HasText("Waiting for traffic") {
		t.Fatal("Escape did not return to the unchanged capture view")
	}
}

func TestExperimentWorkbenchComparesIndependentRunsAndReopensWithoutSending(t *testing.T) {
	a := workspaceApp()
	draft := savedRequest{Method: "POST", URL: "https://example.test/items", Body: "original"}
	for i, body := range []string{`{"value":1,"timestamp":10}`, `{"value":2,"timestamp":20}`} {
		draft.Body = fmt.Sprintf("attempt %d", i+1)
		if err := a.experiments.log.retain(snapshotExperiment(0, draft, "staging", requestExecution{response: &capture.Flow{Method: "POST", URL: draft.URL, Status: 200, RequestBody: draft.Body, ResponseBody: body}})); err != nil {
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
	tt.Type("Experiment workbench")
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
	if !tt.HasText("Recorded experiment #1") || a.composeWaiting || a.composeBody != "attempt 1" || a.response.ResponseBody != `{"value":1,"timestamp":10}` {
		t.Fatal("opening a historical run sent it, chose the wrong snapshot, or lost its result")
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
