package app

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
)

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
	name, ok := a.newScript(&a.scripts, "Debug", `function onRequest() {
  pm.request.headers.upsert("X-Debug", process.env.VSCROXY_SCRIPT_TEST);
  pm.variables.set("seen", "1");
}
function onResponse() {
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
	a.newScript(&a.scripts, "Auth", "function onRequest() {}\n")
	a.newScript(&a.addons, "sign", "")
	a.newProfile("Staging", "10.0.0.1 api.test\n", "")
	a.ignoreText = "telemetry.test"
	rules := []capture.Rule{
		{Name: "User", Type: capture.RuleMapLocal, Match: "/users/", Target: mock, Status: 200},
		{Name: "User again", Type: capture.RuleMapLocal, Match: "/me", Target: mock, Status: 200, Scenario: "Demo"},
		{Name: "Folder", Type: capture.RuleMapLocal, Match: "/static/", Target: src},
		{Name: "Auth", Type: capture.RuleScript, Match: "api.test", Target: "Auth.js"},
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
	if len(c.rules) != 4 || c.rules[0].Target != c.rules[1].Target || filepath.Dir(c.rules[0].Target) != c.mocksDir() || c.rules[2].Target != src {
		t.Fatalf("rules = %+v", c.rules)
	}
	if body, _ := os.ReadFile(c.rules[0].Target); string(body) != `{"id":42}` {
		t.Fatalf("mock = %q", body)
	}
	if c.scripts.texts["Auth"] != "function onRequest() {}\n" {
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

// An addon saved on the Scripts page is what require("name") loads, in a
// rule script on proxied traffic and in a request script alike.
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
	if err := a.writeScript(&a.addons, "tag", `module.exports = { tag: (who) => who + ":" + require("lodash").kebabCase("Shared Addon") };`); err != nil {
		t.Fatal(err)
	}
	name, _ := a.newScript(&a.scripts, "Tag", `function onRequest() { pm.request.headers.upsert("X-Tag", require("tag").tag("rule")); }`)
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
