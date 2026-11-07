package app

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/gorilla/websocket"
)

func TestScriptSnippetsParse(t *testing.T) {
	for _, kind := range []string{"request", "rule", "addon"} {
		for _, snippet := range scriptSnippets(kind) {
			if _, err := editor.ParseSnippet(snippet[1]); err != nil {
				t.Errorf("%s snippet %q: %v", kind, snippet[0], err)
			}
		}
	}
}

func TestSnippetMenuInsertsPlaceholderDefaults(t *testing.T) {
	st := &editor.State{Buffer: editor.NewBuffer("")}
	tt := ui.NewTester(func(c *ui.Context) { snippetMenu(c, st, "rule") }, 400, 200)
	if err := tt.Click("Insert snippet"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Add a request header"); err != nil {
		t.Fatal(err)
	}
	if got, want := st.Buffer.String(), "pm.request.headers.upsert(\"X-Debug\", \"1\");\n"; got != want {
		t.Fatalf("inserted %q, want %q", got, want)
	}
	if got := st.SelectedText(); got != "X-Debug" {
		t.Fatalf("first tab stop selected %q, want X-Debug", got)
	}
	if !st.Run(editor.Tab) || st.SelectedText() != "1" {
		t.Fatalf("Tab did not move to the second stop: %q", st.SelectedText())
	}
}

// A script saved on the Scripts page runs on the traffic a Script rule
// matches, through the real proxy, and what it prints reaches its Console.
func TestScriptRuleRunsTheSavedScript(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"ok":false}`)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.rulesPath = filepath.Join(dir, "rules.json")
	a.scripts.dir = filepath.Join(dir, "tools", "scripts")
	a.loadScripts()
	name, ok := a.newScript(&a.scripts, "Debug", `export function onRequest() {
  pm.request.headers.upsert("X-Debug", process.env.VSCROXY_SCRIPT_TEST);
  pm.variables.set("seen", "1");
}
export function onResponse() {
  console.log("was", pm.response.code, pm.variables.get("seen"));
  pm.response.code = 200;
  pm.response.setBody({ok: true});
}`)
	if !ok {
		t.Fatal("new script failed")
	}
	t.Setenv("VSCROXY_SCRIPT_TEST", "from-env")
	a.scripts.setEnv(nil, nil, nil)
	if !a.applyRuleChanges([]capture.Rule{{Name: "Debug", Type: capture.RuleScript, Match: "/fail", Target: scriptFile(name)}}) {
		t.Fatal(a.ruleUI.problem)
	}
	proxyGet := func(path string) (int, string) {
		t.Helper()
		addr, _ := url.Parse("http://" + a.srv.Addr())
		proxy := &http.Transport{Proxy: http.ProxyURL(addr), DisableKeepAlives: true}
		resp, err := (&http.Client{Transport: proxy}).Get(upstream.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if status, body := proxyGet("/fail"); status != 200 || body != `{"ok":true}` {
		t.Fatalf("client got %d %q; console %+v", status, body, a.scripts.console)
	}
	if got.Get("X-Debug") != "from-env" {
		t.Fatalf("upstream headers = %v", got)
	}
	if status, _ := proxyGet("/other"); status != 500 {
		t.Fatalf("a request the rule does not match was changed: %d", status)
	}
	a.scripts.mu.RLock()
	console := a.scripts.console
	a.scripts.mu.RUnlock()
	if len(console) != 1 || console[0].text != "was 500 1" || console[0].rule != "Debug" {
		t.Fatalf("console = %+v", console)
	}
	// On disk, as a file people can edit and keep in Git.
	if data, err := os.ReadFile(filepath.Join(a.scripts.dir, "Debug.js")); err != nil || !strings.Contains(string(data), "onResponse") {
		t.Fatalf("script file: %q, %v", data, err)
	}
}

func TestWebSocketSavedScriptDuplexLocalsAndMessageDecisions(t *testing.T) {
	const count = 24
	received := make(chan []string, 1)
	u := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for i := range count {
			if c.WriteMessage(websocket.TextMessage, fmt.Appendf(nil, "server-%d", i)) != nil {
				return
			}
		}
		var messages []string
		for range count {
			_, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			messages = append(messages, string(data))
		}
		received <- messages
		for {
			kind, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if c.WriteMessage(kind, data) != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.srv.SetSSL(capture.SSLConfig{All: true})
	a.rulesPath = filepath.Join(t.TempDir(), "rules.json")
	a.scripts.dir = filepath.Join(t.TempDir(), "scripts")
	a.loadScripts()
	name, ok := a.newScript(&a.scripts, "Socket", `export function onMessage(message) {
  if (message.type === "binary") {
    if (message.direction === "out") message.setBase64("AP6AAQ==");
    return;
  }
  const text = message.text();
  if (text === "skip") { message.drop(); return; }
  if (text === "fail" && message.direction === "out") {
    message.setText("must-not-leak"); pm.variables.set("n", "999"); throw new Error("rollback");
  }
  const n = Number(pm.variables.get("n") || 0) + 1;
  pm.variables.set("n", String(n));
  message.setText(text + "|" + message.direction + "|" + n);
}`)
	if !ok || !a.applyRuleChanges([]capture.Rule{{Type: capture.RuleScript, Match: "/socket", Target: scriptFile(name)}}) {
		t.Fatal("could not install saved message script")
	}
	addr, _ := url.Parse("http://" + a.srv.Addr())
	dialer := websocket.Dialer{Proxy: http.ProxyURL(addr)}
	c, _, err := dialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(upstream.URL, "http")+"/socket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for i := range count {
		if err := c.WriteMessage(websocket.TextMessage, fmt.Appendf(nil, "client-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[int]bool{}
	check := func(data, prefix, direction string, index int) {
		t.Helper()
		var n int
		want := fmt.Sprintf("%s-%d|%s|", prefix, index, direction)
		if !strings.HasPrefix(data, want) {
			t.Fatalf("wrong duplex identity: %q, want %q", data, want)
		}
		if _, err := fmt.Sscan(strings.TrimPrefix(data, want), &n); err != nil || n < 1 || n > 2*count || seen[n] {
			t.Fatalf("lost/duplicated connection local: %q", data)
		}
		seen[n] = true
	}
	for i := range count {
		_, data, err := c.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		check(string(data), "server", "in", i)
	}
	select {
	case messages := <-received:
		for i, data := range messages {
			check(data, "client", "out", i)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("upstream did not receive duplex messages")
	}
	for _, tc := range []struct {
		kind       int
		sent, want []byte
	}{
		{websocket.BinaryMessage, []byte{255, 0, 195, 40}, []byte{0, 254, 128, 1}},
		{websocket.TextMessage, []byte("fail"), []byte(fmt.Sprintf("fail|in|%d", 2*count+1))},
		{websocket.TextMessage, []byte("sentinel"), []byte(fmt.Sprintf("sentinel|out|%d|in|%d", 2*count+2, 2*count+3))},
	} {
		if string(tc.sent) == "sentinel" {
			if err := c.WriteMessage(websocket.TextMessage, []byte("skip")); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.WriteMessage(tc.kind, tc.sent); err != nil {
			t.Fatal(err)
		}
		kind, data, err := c.ReadMessage()
		if err != nil || kind != tc.kind || !bytes.Equal(data, tc.want) {
			t.Fatalf("message decision: %d %x %v, want %d %x", kind, data, err, tc.kind, tc.want)
		}
	}
	if got := a.srv.ScriptErrors(); got != 1 {
		t.Fatalf("script failures = %d", got)
	}
}

func TestWebSocketDisconnectCancelsActiveScriptRequest(t *testing.T) {
	entered, cancelled := make(chan struct{}), make(chan struct{})
	dependency := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer dependency.Close()
	u := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.srv.SetSSL(capture.SSLConfig{All: true})
	a.rulesPath = filepath.Join(t.TempDir(), "rules.json")
	a.scripts.dir = filepath.Join(t.TempDir(), "scripts")
	a.loadScripts()
	name, ok := a.newScript(&a.scripts, "Waiting", fmt.Sprintf(`export async function onMessage(message) { await pm.sendRequest(%q); }`, dependency.URL))
	if !ok || !a.applyRuleChanges([]capture.Rule{{Type: capture.RuleScript, Match: "/socket", Target: scriptFile(name)}}) {
		t.Fatal("could not install script")
	}
	addr, _ := url.Parse("http://" + a.srv.Addr())
	dialer := websocket.Dialer{Proxy: http.ProxyURL(addr)}
	c, _, err := dialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(upstream.URL, "http")+"/socket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.WriteMessage(websocket.TextMessage, []byte("wait")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("script request did not start")
	}
	c.Close()
	select {
	case <-cancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("script request survived socket disconnect")
	}
}

// An export carries the rules with the files they answer from, the scripts
// and the profiles, and an import on another computer restores them, the
// mocks written into its own mocks folder.
func TestConfigExportImportRoundTrip(t *testing.T) {
	src := t.TempDir()
	a := workspaceApp()
	a.rulesPath = filepath.Join(src, "rules.json")
	a.scripts.dir = filepath.Join(src, "tools", "scripts")
	a.profiles.dir = filepath.Join(src, "tools", "hosts")
	mock := filepath.Join(src, "elsewhere", "user.json")
	_ = os.MkdirAll(filepath.Dir(mock), 0o700)
	_ = os.WriteFile(mock, []byte(`{"id":42}`), 0o600)
	a.addons.dir = filepath.Join(src, "tools", "addons")
	a.loadScripts()
	a.newScript(&a.scripts, "Auth", "export function onRequest() {}\n")
	a.newScript(&a.scripts, "Mock", newMockScriptText)
	a.newScript(&a.addons, "sign", "")
	a.newProfile("Staging", "10.0.0.1 api.test\n", "")
	a.ignoreText = "telemetry.test"
	rules := []capture.Rule{
		{Name: "User", Type: capture.RuleMapLocal, Match: "/users/", Target: mock, Status: 200},
		{Name: "User again", Type: capture.RuleMapLocal, Match: "/me", Target: mock, Status: 200, Scenario: "Demo"},
		{Name: "Folder", Type: capture.RuleMapLocal, Match: "/static/", Target: src},
		{Name: "Auth", Type: capture.RuleScript, Match: "api.test", Target: "Auth.js"},
		{Name: "Dynamic", Type: capture.RuleCodeMock, Match: "/dynamic", Target: "Mock.js", Scenario: "Demo"},
		{Name: "Slow", Type: capture.RuleNetwork, Match: "/slow", Scenario: "Demo", Network: new(capture.Throttle{DownPerSec: 1024, Latency: time.Millisecond})},
	}
	if !a.applyRuleChanges(rules) {
		t.Fatal(a.ruleUI.problem)
	}
	a.ignorePath = filepath.Join(src, "ignore.txt")
	b, notes := a.exportConfig()
	if len(notes) != 1 || !strings.Contains(notes[0], "1 Map Local rule") {
		t.Errorf("notes = %q: the folder rule stays a path", notes)
	}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), mock) {
		t.Error("the export names this computer's mock file")
	}

	dst := t.TempDir()
	c := workspaceApp()
	c.srv = requestProxy(t) // Reload configuration reads Host profiles for a running proxy
	c.rulesPath = filepath.Join(dst, "rules.json")
	c.scripts.dir = filepath.Join(dst, "tools", "scripts")
	c.addons.dir = filepath.Join(dst, "tools", "addons")
	c.profiles.dir = filepath.Join(dst, "tools", "hosts")
	c.ignorePath = filepath.Join(dst, "ignore.txt")
	parsed, err := parseConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.importConfig(parsed, [3]bool{true, true, true}); err != nil {
		t.Fatal(err)
	}
	if len(c.rules) != 6 || c.rules[0].Target != c.rules[1].Target || filepath.Dir(c.rules[0].Target) != c.mocksDir() || c.rules[2].Target != src ||
		c.rules[4].Type != capture.RuleCodeMock || c.rules[4].Target != "Mock.js" || scriptHooks(&c.scripts, "Mock") != "onMock" ||
		c.rules[5].Network == nil || *c.rules[5].Network != *rules[5].Network {
		t.Fatalf("rules = %+v", c.rules)
	}
	if body, _ := os.ReadFile(c.rules[0].Target); string(body) != `{"id":42}` {
		t.Fatalf("mock = %q", body)
	}
	if c.scripts.texts["Auth"] != "export function onRequest() {}\n" {
		t.Fatalf("scripts = %v", c.scripts.texts)
	}
	if c.addons.texts["sign"] != newAddonText("sign") || c.addons.problems["sign"] != nil {
		t.Fatalf("addons = %v, %v", c.addons.texts, c.addons.problems)
	}
	if len(c.profiles.set.Profiles) != 1 || c.profiles.set.Profiles[0].Text != "10.0.0.1 api.test\n" {
		t.Fatalf("profiles = %+v", c.profiles.set.Profiles)
	}
	if lines, _ := os.ReadFile(c.ignorePath); string(lines) != "telemetry.test\n" {
		t.Fatalf("ignore list = %q", lines)
	}

	// A file that names a mock it does not hold is refused before anything
	// is written.
	parsed.Mocks = nil
	broken, _ := json.Marshal(parsed)
	if _, err := parseConfig(broken); err == nil {
		t.Fatal("a configuration missing its mock files was accepted")
	}
}

// An addon saved on the Scripts page is what a rule script on proxied
// traffic imports and a request script requires.
func TestAddonsLoadInRuleAndRequestScripts(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	dir := t.TempDir()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.rulesPath = filepath.Join(dir, "rules.json")
	a.scripts.dir = filepath.Join(dir, "tools", "scripts")
	a.addons.dir = filepath.Join(dir, "tools", "addons")
	a.loadScripts()
	if err := a.writeScript(&a.addons, "tag", "import _ from \"lodash\";\nexport const tag = (who) => who + \":\" + _.kebabCase(\"Shared Addon\");\n"); err != nil {
		t.Fatal(err)
	}
	name, _ := a.newScript(&a.scripts, "Tag", "import { tag } from \"tag\";\nexport function onRequest() { pm.request.headers.upsert(\"X-Tag\", tag(\"rule\")); }")
	a.scripts.setEnv(nil, nil, a.addons.sources())
	if !a.applyRuleChanges([]capture.Rule{{Type: capture.RuleScript, Match: "/rule", Target: scriptFile(name)}}) {
		t.Fatal(a.ruleUI.problem)
	}
	addr, _ := url.Parse("http://" + a.srv.Addr())
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(addr), DisableKeepAlives: true}}).Get(upstream.URL + "/rule")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got.Get("X-Tag") != "rule:shared-addon" {
		t.Fatalf("rule script header = %q; console %+v", got.Get("X-Tag"), a.scripts.console)
	}

	r := request.Request{Method: "GET", URL: upstream.URL, Pre: `pm.request.headers.upsert("X-Tag", require("tag").tag("request"))`}
	out := r.Execute(t.Context(), a.srv, a.inheritance(""), nil, nil)
	if out.Err != nil || got.Get("X-Tag") != "request:shared-addon" {
		t.Fatalf("request script header = %q, %v", got.Get("X-Tag"), out.Err)
	}
	if n, _ := a.scriptUsers(&a.addons, "tag"); n != 1 {
		t.Errorf("addon users = %d, want the one rule script", n)
	}
	// A name require already loads is not an addon's.
	if _, ok := a.newScript(&a.addons, "lodash", ""); ok {
		t.Error("an addon named lodash was saved")
	}
}

// A rule script that does not convert is marked where it fails, and does
// nothing; an addon that does not is marked too, and scripts cannot load it.
func TestScriptProblemsAreMarked(t *testing.T) {
	dir := t.TempDir()
	a := workspaceApp()
	a.scripts.dir, a.addons.dir = filepath.Join(dir, "scripts"), filepath.Join(dir, "addons")
	a.loadScripts()
	a.newScript(&a.scripts, "Waits", "export function onRequest() {}\nawait pm.sendRequest(\"https://example.com\");\n")
	a.newScript(&a.addons, "half", "")
	if err := a.writeScript(&a.addons, "half", "export const x = ;\n"); err != nil {
		t.Fatal(err)
	}
	var problem request.ScriptProblem
	if !errors.As(a.scripts.problems["Waits"], &problem) || problem.Line != 2 || !strings.Contains(problem.Message, "await") {
		t.Errorf("rule script problem = %v", a.scripts.problems["Waits"])
	}
	if on, _, _ := a.scripts.Hooks("Waits.js"); on {
		t.Error("a script that does not convert still runs")
	}
	if !errors.As(a.addons.problems["half"], &problem) || problem.Line != 1 {
		t.Errorf("addon problem = %v", a.addons.problems["half"])
	}
	if _, ok := a.addons.sources()["half"]; ok {
		t.Error("scripts can load an addon that does not convert")
	}
}

func TestCodeMockEditorAndSavedRuleLifecycle(t *testing.T) {
	dir := t.TempDir()
	a := workspaceApp()
	a.srv, a.rulesPath = requestProxy(t), filepath.Join(dir, "rules.json")
	a.scripts.dir, a.addons.dir = filepath.Join(dir, "scripts"), filepath.Join(dir, "addons")
	a.loadScripts()
	a.openTools("mocks")
	tt := ui.NewTester(a.toolsView, 960, 600)
	for _, label := range []string{"New rule", "Rule type", "Code Mock"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range [][2]string{{"Rule name", "API mock"}, {"Match URL", "/mock"}} {
		if err := tt.Click(field[0]); err != nil {
			t.Fatal(err)
		}
		tt.Type(field[1])
	}
	for _, label := range []string{"Rule script", "New script", "Save rule"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.rules) != 1 || a.rules[0].Type != capture.RuleCodeMock || a.rules[0].Target != "API mock.js" ||
		a.scripts.texts["API mock"] != newMockScriptText || scriptHooks(&a.scripts, "API mock") != "onMock" {
		t.Fatalf("saved Code Mock = %+v, scripts=%v", a.rules, a.scripts.texts)
	}
	for _, dark := range []bool{false, true} {
		a.appearance = appearanceOf(dark)
		for _, width := range []int{960, 1280} {
			tt.SetSize(width, 600)
			tt.Frame()
			if !tt.HasText("Edit script") || !tt.HasText("Code Mock") || !tt.HasText("Save rule") {
				t.Fatalf("mock editor missing controls at width=%d dark=%v: %v", width, dark, tt.Texts())
			}
		}
	}
	if err := tt.Click("Edit script"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("onMock") || !tt.HasText("Run by 1 rule") {
		t.Fatalf("mock script editor = %v", tt.Texts())
	}
	if err := tt.Click("Script actions"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("New Code Mock rule"); err != nil {
		t.Fatal(err)
	}
	if a.toolsPage != "mocks" || a.ruleUI.draft.Target != "API mock.js" || a.ruleUI.kind != "Code Mock" {
		t.Fatalf("script action did not create a code mock: %+v", a.ruleUI)
	}
	// The saved files, not the draft/old runtime, must survive reload.
	a.scripts.parsed = nil
	a.loadScripts()
	loaded, err := capture.LoadRules(a.rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	a.rules = loaded
	if err := a.srv.SetRules(loaded); err != nil {
		t.Fatal(err)
	}
	send := func(want int, body string) {
		t.Helper()
		got, err := a.srv.Replay(t.Context(), capture.Flow{Method: "POST", URL: "http://127.0.0.1:1/mock", RequestBody: "input"})
		if err != nil || got.Status != want || got.ResponseBody != body {
			t.Fatalf("mock reply = %d %q, %v; console=%v", got.Status, got.ResponseBody, err, a.scripts.console)
		}
	}
	send(200, `{"ok":true,"method":"POST","path":"/mock"}`)
	a.renameScript(&a.scripts, "API mock", "Renamed")
	if a.rules[0].Target != "Renamed.js" || a.ruleUI.draft.Target != "Renamed.js" {
		t.Fatalf("rename lost a saved or draft reference: %+v, %+v", a.rules, a.ruleUI.draft)
	}
	send(200, `{"ok":true,"method":"POST","path":"/mock"}`)
	if err := a.writeScript(&a.scripts, "Renamed", `export function onMock() { throw new Error("private diagnostic"); }`); err != nil {
		t.Fatal(err)
	}
	send(502, "code mock: script failed; see Scripts Console\n")
	if err := a.writeScript(&a.scripts, "Renamed", "export function onMock( {"); err != nil {
		t.Fatal(err)
	}
	send(502, "code mock: script failed; see Scripts Console\n")
	a.dropScript(&a.scripts, "Renamed")
	send(502, "code mock: script failed; see Scripts Console\n")
}
