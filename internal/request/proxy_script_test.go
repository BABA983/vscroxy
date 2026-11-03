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
	s, err := ParseProxyScript("// only the response\nasync function onResponse() {}\nconst onRequest = 1;\nfunction helper() { function onRequest() {} }")
	if err != nil {
		t.Fatal(err)
	}
	if s.OnRequest || !s.OnResponse {
		t.Fatalf("hooks = %v, %v: only top-level declarations count", s.OnRequest, s.OnResponse)
	}
	_, err = ParseProxyScript("function onRequest() {\n  pm.request.headers.upsert(\n}")
	var problem ScriptProblem
	if !errors.As(err, &problem) || problem.Line != 3 {
		t.Fatalf("err = %#v, want a problem on line 3", err)
	}
}

func TestProxyScriptEditsTheRequest(t *testing.T) {
	s, err := ParseProxyScript(`
function onRequest() {
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
	s, err := ParseProxyScript(`
async function onResponse() {
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
	s, _ := ParseProxyScript(`function onRequest() { pm.environment.set("a", "1") }`)
	x := &capture.ScriptExchange{Phase: "request", URL: "https://api.test/", Header: http.Header{}}
	if _, err := s.Run(t.Context(), x, ProxyEnv{}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("err = %v: the environment is read-only to rule scripts", err)
	}
	// A hook the script does not declare is not an error.
	if _, err := s.Run(t.Context(), &capture.ScriptExchange{Phase: "response"}, ProxyEnv{}); err != nil {
		t.Fatal(err)
	}
	// A binary body is not handed to the script, nor replaced.
	s, _ = ParseProxyScript(`function onRequest() { pm.request.body.raw = "text" }`)
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
	s, _ := ParseProxyScript(`async function onRequest() {
	const res = await pm.sendRequest("` + server.URL + `/token");
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
	s, _ := ParseProxyScript(`function onRequest() {
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
