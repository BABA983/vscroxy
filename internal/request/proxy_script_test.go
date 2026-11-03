package request

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
)

func TestParseProxyScriptFindsHooks(t *testing.T) {
	s, err := ParseProxyScript("t", "// only the response\nexport async function onResponse() {}\nfunction onRequest() {}\nfunction onMock() {}\nexport function helper() {}")
	if err != nil {
		t.Fatal(err)
	}
	if s.OnRequest || !s.OnResponse || s.OnMock {
		t.Fatalf("hooks = %v, %v, %v: only exported hooks count", s.OnRequest, s.OnResponse, s.OnMock)
	}
	s, err = ParseProxyScript("t", `function mock() {} export { mock as onMock };`)
	if err != nil || s == nil || !s.OnMock || s.OnRequest || s.OnResponse {
		t.Fatalf("mock hook = %+v, %v", s, err)
	}
	for src, line := range map[string]int{
		"export function onRequest() {\n  pm.request.headers.upsert(\n}":                 3,
		"const token = 1;\nawait pm.sendRequest(\"x\");\nexport function onRequest() {}": 2,
	} {
		_, err = ParseProxyScript("t", src)
		if problem, ok := errors.AsType[ScriptProblem](err); !ok || problem.Line != line {
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

func TestProxyScriptMocksDynamicResponse(t *testing.T) {
	s, err := ParseProxyScript("Mock", `import { greeting } from "greet";
export async function onMock() {
	if (pm.response.code !== 200 || pm.response.text() !== "" || pm.response.headers.count() !== 0) throw new Error("not a fresh response");
	const input = JSON.parse(pm.request.body.raw);
	const message = await Promise.resolve(greeting(input.name, pm.environment.get("prefix")));
	pm.response.code = 201;
	pm.response.headers.add("X-Value", "first");
	pm.response.headers.add({key: "X-Value", value: "second"});
	pm.response.headers.upsert("Content-Type", "application/json");
	pm.response.setBody({message, method: pm.request.method, page: pm.request.url.query.get("page"), token: pm.request.headers.get("X-Token"), local: pm.variables.get("local"), global: pm.globals.get("global")});
	pm.variables.set("seen", "yes");
	pm.globals.set("count", "1");
}`)
	if err != nil {
		t.Fatal(err)
	}
	x := &capture.ScriptExchange{Phase: "mock", Method: "POST", URL: "https://api.test/items?page=2",
		Header: http.Header{"X-Token": {"abc"}}, Body: []byte(`{"name":"Ada"}`),
		Status: 503, Response: http.Header{"X-Old": {"old"}}, ResponseBody: []byte("old"),
		Locals: map[string]string{"local": "here"}}
	out, err := s.Run(t.Context(), x, ProxyEnv{
		Environment: map[string]string{"prefix": "Hello"}, Globals: map[string]string{"global": "shared"},
		Addons: map[string]string{"greet": `export const greeting = (name, prefix) => prefix + ", " + name;`},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"message":"Hello, Ada","method":"POST","page":"2","token":"abc","local":"here","global":"shared"}`
	if x.Status != 201 || string(x.ResponseBody) != want || x.Response.Get("Content-Type") != "application/json" ||
		!reflect.DeepEqual(x.Response.Values("X-Value"), []string{"first", "second"}) || x.Response.Get("X-Old") != "" {
		t.Fatalf("response = %d %v %s", x.Status, x.Response, x.ResponseBody)
	}
	if x.Locals["seen"] != "yes" || !reflect.DeepEqual(out.Writes, []Write{{Scope: "global", Name: "count", Value: "1"}}) {
		t.Fatalf("locals = %v, writes = %+v", x.Locals, out.Writes)
	}
}

func TestProxyScriptMockRequestIsReadOnly(t *testing.T) {
	s, err := ParseProxyScript("Mock", `export function onMock() {
	const before = JSON.stringify(pm.request.toJSON());
	[
		() => { pm.request.method = "DELETE"; },
		() => { pm.request.url = "https://changed.test/"; },
		() => { pm.request.body = "changed"; },
		() => { pm.request.body.raw = "changed"; },
		() => pm.request.body.update("changed"),
		() => pm.request.headers.add("X-New", "1"),
		() => pm.request.headers.upsert("X-Token", "changed"),
		() => pm.request.headers.remove("X-Token"),
		() => pm.request.headers.clear(),
		() => pm.request.addHeader("X-New", "1"),
		() => pm.request.upsertHeader("X-Token", "changed"),
		() => pm.request.removeHeader("X-Token"),
		() => pm.request.url.update("https://changed.test/"),
		() => pm.request.url.query.add("page", "3"),
		() => pm.request.url.query.upsert("page", "3"),
		() => pm.request.url.query.remove("page"),
		() => pm.request.url.query.clear(),
		() => pm.request.url.addQueryParams("page=3"),
		() => pm.request.url.removeQueryParams("page"),
		() => pm.request.addQueryParams("page=3"),
		() => pm.request.removeQueryParams("page"),
		() => { pm.request = {}; }
	].forEach(edit => {
		let rejected = false;
		try { edit(); } catch (e) { rejected = e.message.includes("read-only"); }
		if (!rejected || JSON.stringify(pm.request.toJSON()) !== before) throw new Error("request mutation was allowed");
	});
	pm.response.setBody(pm.request.body.raw);
}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, binary := range []bool{false, true} {
		x := &capture.ScriptExchange{Phase: "mock", Method: "POST", URL: "https://api.test/?page=2",
			Header: http.Header{"X-Token": {"abc"}}, Body: []byte("original"), BinaryBody: binary}
		if binary {
			x.Body = []byte{0, 255, 128, 1}
		}
		body, header := bytes.Clone(x.Body), x.Header.Clone()
		if _, err := s.Run(t.Context(), x, ProxyEnv{}); err != nil {
			t.Fatalf("binary=%v: %v", binary, err)
		}
		if x.Method != "POST" || x.URL != "https://api.test/?page=2" || !reflect.DeepEqual(x.Header, header) || !bytes.Equal(x.Body, body) {
			t.Fatalf("binary=%v: request changed: %+v", binary, x)
		}
		if binary && len(x.ResponseBody) != 0 || !binary && string(x.ResponseBody) != "original" {
			t.Fatalf("binary=%v: exposed body = %q", binary, x.ResponseBody)
		}
	}
}

func TestProxyScriptMockFailuresDoNotPublishResponse(t *testing.T) {
	for name, tc := range map[string]struct{ source, want string }{
		"missing":      {`export function onRequest() {}`, "must export onMock"},
		"throw":        {"export async function onMock() {\n pm.response.code = 201; pm.response.headers.add('X-New', '1'); pm.response.setBody('changed');\n await Promise.resolve(); throw new Error('failed');\n}", "Mock.js:3:"},
		"module throw": {`throw new Error("module failed"); export function onMock() {}`, "module failed"},
		"not callable": {`export const onMock = 1;`, "TypeError"},
		"environment":  {`export function onMock() { pm.environment.set("key", "value"); }`, "read-only"},
		"request":      {`export function onMock() { pm.request.method = "DELETE"; }`, "read-only"},
		"pending":      {`export async function onMock() { await new Promise(() => {}); }`, "never finished"},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := ParseProxyScript("Mock", tc.source)
			if err != nil {
				t.Fatal(err)
			}
			x := &capture.ScriptExchange{Phase: "mock", Method: "GET", URL: "https://api.test/", Header: http.Header{},
				Status: 202, Response: http.Header{"X-Old": {"old"}}, ResponseBody: []byte("old"), Locals: map[string]string{}}
			want := *x
			want.Response, want.ResponseBody = x.Response.Clone(), bytes.Clone(x.ResponseBody)
			if _, err := s.Run(t.Context(), x, ProxyEnv{}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if !reflect.DeepEqual(*x, want) {
				t.Fatalf("failed mock published changes: %+v", x)
			}
		})
	}
}

func TestProxyScriptMockCannotSendRequests(t *testing.T) {
	var contacted atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contacted.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	s, err := ParseProxyScript("Mock", `export async function onMock() {
	pm.response.setBody("not published");
	await pm.sendRequest("`+server.URL+`");
}`)
	if err != nil {
		t.Fatal(err)
	}
	x := &capture.ScriptExchange{Phase: "mock", ResponseBody: []byte("original")}
	if _, err := s.Run(t.Context(), x, ProxyEnv{}); err == nil || !strings.Contains(err.Error(), "pm.sendRequest is not available here") {
		t.Fatalf("err = %v", err)
	}
	if contacted.Load() || string(x.ResponseBody) != "original" {
		t.Fatalf("contacted = %v, body = %q", contacted.Load(), x.ResponseBody)
	}
}

func TestProxyScriptMockTimeoutAndCancellation(t *testing.T) {
	s, err := ParseProxyScript("Mock", `export function onMock() {
	pm.response.code = 201;
	pm.response.headers.add("X-New", "1");
	pm.response.setBody("not published");
	for (;;) {}
}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"budget", "deadline", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			if mode == "deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			} else if mode == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				timer := time.AfterFunc(50*time.Millisecond, cancel)
				defer timer.Stop()
			}
			x := &capture.ScriptExchange{Phase: "mock", Status: 202, Response: http.Header{"X-Old": {"old"}}, ResponseBody: []byte("original")}
			_, err := s.Run(ctx, x, ProxyEnv{})
			want := context.DeadlineExceeded
			if mode == "cancel" {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("err = %v, want %v", err, want)
			}
			if x.Status != 202 || len(x.Response) != 1 || x.Response.Get("X-Old") != "old" || string(x.ResponseBody) != "original" {
				t.Fatalf("interrupted mock published response: %+v", x)
			}
		})
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
