package request

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
)

func TestParseProxyScriptFindsHooks(t *testing.T) {
	s, err := ParseProxyScript("t", "// only the response\nexport async function onResponse() {}\nfunction onRequest() {}\nexport function helper() {}")
	if err != nil {
		t.Fatal(err)
	}
	if s.OnRequest || !s.OnResponse {
		t.Fatalf("hooks = %v, %v: only exported hooks count", s.OnRequest, s.OnResponse)
	}
	for src, line := range map[string]int{
		"export function onRequest() {\n  pm.request.headers.upsert(\n}":                 3,
		"const token = 1;\nawait pm.sendRequest(\"x\");\nexport function onRequest() {}": 2,
	} {
		_, err = ParseProxyScript("t", src)
		var problem ScriptProblem
		if !errors.As(err, &problem) || problem.Line != line {
			t.Errorf("%q: err = %#v, want a problem on line %d", src, err, line)
		}
	}
}

// Syntax newer than goja runs, and errors name the line as written, not as
// converted.
func TestProxyScriptErrorsPointAtTheSource(t *testing.T) {
	s, err := ParseProxyScript("Debug", "import _ from \"lodash\";\nconst seen = {..._.pick({a: 1}, \"a\")};\n\nexport function onRequest() {\n  pm.request.headers.upsert(\"X-A\", String(seen?.a ?? 0));\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	x := &capture.ScriptExchange{Phase: "request", URL: "https://api.test/", Header: http.Header{}}
	if _, err := s.Run(t.Context(), x, ProxyEnv{}); err != nil || x.Header.Get("X-A") != "1" {
		t.Fatalf("X-A = %q, %v", x.Header.Get("X-A"), err)
	}
	s, _ = ParseProxyScript("Debug", "// first\nimport _ from \"lodash\";\n\nexport function onRequest() {\n  const x = null;\n  return x.y;\n}\n")
	_, err = s.Run(t.Context(), x, ProxyEnv{})
	if err == nil || !strings.Contains(err.Error(), "Debug.js:6:") {
		t.Fatalf("err = %v, want it at Debug.js line 6", err)
	}
}

func TestProxyScriptEditsTheRequest(t *testing.T) {
	s, err := ParseProxyScript("Auth", `
export function onRequest() {
	pm.request.headers.upsert("Authorization", "Bearer " + process.env.VSCROXY_TEST_TOKEN);
	pm.request.headers.remove("X-Drop");
	pm.request.url.query.upsert("page", "2");
	pm.request.method = "post";
	pm.request.body.raw = JSON.stringify({sum: _sum(pm.environment.get("a"), 2)});
	pm.variables.set("seen", "yes");
	pm.globals.set("count", "1");
	console.log("ran", pm.info.eventName, pm.info.requestName);
}
function _sum(a, b) { return Number(a) + b; }`)
	if err != nil {
		t.Fatal(err)
	}
	x := &capture.ScriptExchange{Phase: "request", Rule: "Auth", Method: "GET", URL: "https://api.test/items?page=1",
		Header: http.Header{"X-Drop": {"1"}}, Body: []byte("old")}
	out, err := s.Run(t.Context(), x, ProxyEnv{Environment: map[string]string{"a": "40"}, OS: map[string]string{"VSCROXY_TEST_TOKEN": "t-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if x.Method != "POST" || x.URL != "https://api.test/items?page=2" || x.Header.Get("Authorization") != "Bearer t-1" || x.Header.Get("X-Drop") != "" || string(x.Body) != `{"sum":42}` {
		t.Fatalf("exchange = %s %s %v %q", x.Method, x.URL, x.Header, x.Body)
	}
	if x.Locals["seen"] != "yes" {
		t.Errorf("locals = %v", x.Locals)
	}
	if len(out.Writes) != 1 || out.Writes[0] != (Write{Scope: "global", Name: "count", Value: "1"}) {
		t.Errorf("writes = %+v", out.Writes)
	}
	if !strings.Contains(consoleText(out.Console), "ran request Auth") {
		t.Errorf("console = %+v", out.Console)
	}
}

func TestProxyScriptEditsTheResponse(t *testing.T) {
	s, err := ParseProxyScript("Edit", `
export async function onResponse() {
	const CryptoJS = require("crypto-js");
	const body = pm.response.json();
	body.sig = CryptoJS.HmacSHA256("data", "key").toString();
	body.random = CryptoJS.lib.WordArray.random(8).toString().length;
	body.before = pm.variables.get("seen");
	pm.response.setBody(body);
	pm.response.code = 299;
	pm.response.headers.upsert("X-Edited", "1");
	pm.response.headers.remove("ETag");
}`)
	if err != nil {
		t.Fatal(err)
	}
	x := &capture.ScriptExchange{Phase: "response", Method: "GET", URL: "https://api.test/", Header: http.Header{},
		Status: 200, Response: http.Header{"Etag": {`"v1"`}}, ResponseBody: []byte(`{"id":1}`), Locals: map[string]string{"seen": "yes"}}
	if _, err := s.Run(t.Context(), x, ProxyEnv{}); err != nil {
		t.Fatal(err)
	}
	// HMAC-SHA256("data", "key"), as computed by openssl dgst.
	want := `{"id":1,"sig":"5031fe3d989c6d1537a013fa6e739da23463fdaec3b70137d828e36ace221bd0","random":16,"before":"yes"}`
	if x.Status != 299 || string(x.ResponseBody) != want || x.Response.Get("X-Edited") != "1" || x.Response.Get("Etag") != "" {
		t.Fatalf("response = %d %v %s", x.Status, x.Response, x.ResponseBody)
	}
}

func TestProxyScriptLimits(t *testing.T) {
	s, _ := ParseProxyScript("t", `export function onRequest() { pm.environment.set("a", "1") }`)
	x := &capture.ScriptExchange{Phase: "request", URL: "https://api.test/", Header: http.Header{}}
	if _, err := s.Run(t.Context(), x, ProxyEnv{}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("err = %v: the environment is read-only to rule scripts", err)
	}
	// A hook the script does not declare is not an error.
	if _, err := s.Run(t.Context(), &capture.ScriptExchange{Phase: "response"}, ProxyEnv{}); err != nil {
		t.Fatal(err)
	}
	// A binary body is not handed to the script, nor replaced.
	s, _ = ParseProxyScript("t", `export function onRequest() { pm.request.body.raw = "text" }`)
	x = &capture.ScriptExchange{Phase: "request", URL: "https://api.test/", Header: http.Header{}, BinaryBody: true}
	if _, err := s.Run(t.Context(), x, ProxyEnv{}); err != nil || x.Body != nil {
		t.Fatalf("binary body = %q, %v", x.Body, err)
	}
}

// pm.sendRequest from a rule script goes to the server directly: through
// the proxy, the script's own rule would match it again.
func TestProxyScriptSendsDirectly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"token":"abc"}`))
	}))
	defer server.Close()
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	s, _ := ParseProxyScript("t", `export async function onRequest() {
	const res = await pm.sendRequest("`+server.URL+`/token");
	pm.request.headers.upsert("X-Token", res.json().token);
}`)
	x := &capture.ScriptExchange{Phase: "request", URL: "https://api.test/", Header: http.Header{}}
	if _, err := s.Run(t.Context(), x, ProxyEnv{}); err != nil || x.Header.Get("X-Token") != "abc" {
		t.Fatalf("header %q, %v", x.Header.Get("X-Token"), err)
	}
}

func consoleText(list []ConsoleMessage) string {
	var b strings.Builder
	for _, m := range list {
		b.WriteString(m.Text + "\n")
	}
	return b.String()
}

func TestAddonsAreRequiredOncePerScript(t *testing.T) {
	addons := map[string]string{
		"counter": `var n = 0; module.exports = { next: () => ++n, other: require("helper").name };`,
		"helper":  `exports.name = "helper"; exports.counter = require("counter");`, // a cycle, as in Node
		"broken":  `throw new Error("addon failed")`,
	}
	s, _ := ParseProxyScript("t", `export function onRequest() {
	const counter = require("counter");
	counter.next();
	pm.request.headers.upsert("X-Count", String(require("counter").next()));
	pm.request.headers.upsert("X-Other", counter.other);
	try { require("broken") } catch (e) { pm.request.headers.upsert("X-Broken", e.message) }
	try { require("missing") } catch (e) { pm.request.headers.upsert("X-Missing", e.message) }
}`)
	x := &capture.ScriptExchange{Phase: "request", URL: "https://api.test/", Header: http.Header{}}
	if _, err := s.Run(t.Context(), x, ProxyEnv{Addons: addons}); err != nil {
		t.Fatal(err)
	}
	if x.Header.Get("X-Count") != "2" || x.Header.Get("X-Other") != "helper" || x.Header.Get("X-Broken") != "addon failed" ||
		!strings.Contains(x.Header.Get("X-Missing"), "is not available") {
		t.Fatalf("headers = %v", x.Header)
	}
	// Each run starts again: module state does not leak between exchanges.
	if _, err := s.Run(t.Context(), x, ProxyEnv{Addons: addons}); err != nil || x.Header.Get("X-Count") != "2" {
		t.Fatalf("second run count = %q, %v", x.Header.Get("X-Count"), err)
	}
}

// An ES module addon imports libraries and other addons; a CommonJS one
// still works; request scripts require either.
func TestESModuleAddons(t *testing.T) {
	addons := map[string]string{
		"sign":   "import CryptoJS from \"crypto-js\";\nimport { prefix } from \"legacy\";\nexport const sign = (text) => prefix + CryptoJS.SHA256(text).toString().slice(0, 8);\nexport default { name: \"sign\" };\n",
		"legacy": "module.exports = { prefix: \"v1:\" };",
		"broken": "export const x = ;",
	}
	s, err := ParseProxyScript("Sign", `import { sign } from "sign";
import def from "sign";
export function onRequest() {
	pm.request.headers.upsert("X-Sig", sign("data") + " " + def.name);
	try { require("broken") } catch (e) { pm.request.headers.upsert("X-Broken", e.message) }
}`)
	if err != nil {
		t.Fatal(err)
	}
	x := &capture.ScriptExchange{Phase: "request", URL: "https://api.test/", Header: http.Header{}}
	if _, err := s.Run(t.Context(), x, ProxyEnv{Addons: addons}); err != nil {
		t.Fatal(err)
	}
	// SHA-256("data") starts 3a6eb079, as sha256sum computes it.
	if x.Header.Get("X-Sig") != "v1:3a6eb079 sign" || !strings.Contains(x.Header.Get("X-Broken"), `addon "broken": line 1`) {
		t.Fatalf("headers = %v", x.Header)
	}
	run := &scriptRun{addons: addons}
	req := &Prepared{Method: "GET", URL: "https://api.test/", Header: http.Header{}}
	if err := runScript(`pm.request.headers.upsert("X-Sig", require("sign").sign("data") + " " + require("sign").default.name)`, run, map[string]any{"req": req}); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("X-Sig") != "v1:3a6eb079 sign" {
		t.Fatalf("request script header = %q", req.Header.Get("X-Sig"))
	}
}
