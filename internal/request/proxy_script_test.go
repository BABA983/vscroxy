package request

import (
	"bytes"
	"context"
	"encoding/base64"
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
	s, err := ParseProxyScript("t", "// only the response\nexport async function onResponse() {}\nfunction onRequest() {}\nfunction onMock() {}\nfunction onMessage() {}\nexport function helper() {}")
	if err != nil {
		t.Fatal(err)
	}
	if s.OnRequest || !s.OnResponse || s.OnMock || s.OnMessage {
		t.Fatalf("hooks = %+v: only exported hooks count", s)
	}
	s, err = ParseProxyScript("t", `function mock() {} export { mock as onMock };`)
	if err != nil || s == nil || !s.OnMock || s.OnRequest || s.OnResponse {
		t.Fatalf("mock hook = %+v, %v", s, err)
	}
	s, err = ParseProxyScript("t", `function message() {} export { message as onMessage };`)
	if err != nil || s == nil || !s.OnMessage || s.OnRequest || s.OnResponse || s.OnMock {
		t.Fatalf("message hook = %+v, %v", s, err)
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

func TestProxyScriptMessageBinaryIsLossless(t *testing.T) {
	data := []byte{0, 255, 128, 192, 175, 237, 160, 128, 240, 159, 146, 169, 1}
	edited := []byte{255, 0, 254, 128, 193}
	encoded := base64.StdEncoding.EncodeToString(data)
	for _, edit := range []bool{false, true} {
		source := `export async function onMessage(message) {
	if (arguments.length !== 1 || pm.message !== undefined || globalThis.message !== undefined) throw new Error("argument only");
	if (message.type !== "binary" || message.direction !== "in") throw new Error("metadata");
	let rejected = false;
	try { message.text(); } catch (e) { rejected = e.message.includes("text messages"); }
	if (!rejected) throw new Error("binary exposed as text");
	if (message.base64() !== "` + encoded + `") throw new Error("bytes changed");
	await Promise.resolve();
	message.setBase64(message.base64());`
		want := data
		if edit {
			source += `message.setBase64("` + base64.StdEncoding.EncodeToString(edited) + `");`
			want = edited
		}
		source += `}`
		s, err := ParseProxyScript("Binary", source)
		if err != nil {
			t.Fatal(err)
		}
		input := bytes.Clone(data)
		x := &capture.ScriptExchange{Phase: "message", Message: &capture.ScriptMessage{Type: "binary", Direction: "in", Data: input}}
		if _, err := s.Run(t.Context(), x, ProxyEnv{}); err != nil {
			t.Fatal(err)
		}
		if x.Message.Type != "binary" || x.Message.Direction != "in" || x.Message.Drop || !bytes.Equal(x.Message.Data, want) || !bytes.Equal(input, data) {
			t.Fatalf("edit=%v: message = %+v, original bytes = %x", edit, x.Message, input)
		}
	}
}

func TestProxyScriptMessageTextAndTypeChanges(t *testing.T) {
	for name, tc := range map[string]struct {
		kind, source, wantType string
		data, want             []byte
	}{
		"text edit":      {"text", `if (message.text() !== "before") throw new Error("text"); message.setText("héllo\u0000🌍");`, "text", []byte("before"), []byte("héllo\x00🌍")},
		"binary to text": {"binary", `message.setText("\uD83C\uDF0D"); if (message.text() !== "🌍") throw new Error("text");`, "text", []byte{255}, []byte("🌍")},
		"text to binary": {"text", `message.setBase64("/wCA"); if (message.type !== "binary") throw new Error("type");`, "binary", []byte("before"), []byte{255, 0, 128}},
		"empty text":     {"binary", `message.setText("");`, "text", []byte{255}, []byte{}},
		"empty binary":   {"text", `message.setBase64("");`, "binary", []byte("before"), []byte{}},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := ParseProxyScript("Text", `export function onMessage(message) { `+tc.source+` }`)
			if err != nil {
				t.Fatal(err)
			}
			x := &capture.ScriptExchange{Phase: "message", Message: &capture.ScriptMessage{Type: tc.kind, Direction: "out", Data: tc.data}}
			if _, err := s.Run(t.Context(), x, ProxyEnv{}); err != nil {
				t.Fatal(err)
			}
			if x.Message.Type != tc.wantType || x.Message.Direction != "out" || !bytes.Equal(x.Message.Data, tc.want) || x.Message.Drop {
				t.Fatalf("message = %+v, want %s %x", x.Message, tc.wantType, tc.want)
			}
		})
	}
}

func TestProxyScriptMessageDropAndConnectionState(t *testing.T) {
	s, err := ParseProxyScript("Messages", `import { next } from "counter";
export function onRequest() { throw new Error("wrong phase"); }
export function onMessage(message) {
	const n = next(pm.variables.get("count"));
	pm.variables.set("count", String(n));
	pm.globals.set("count", String(n));
	message.setText(pm.environment.get("prefix") + process.env.SUFFIX + n);
	if (message.direction === "out") message.drop();
	console.log(pm.info.eventName, pm.info.requestName, n);
}`)
	if err != nil {
		t.Fatal(err)
	}
	locals := map[string]string{}
	env := ProxyEnv{Environment: map[string]string{"prefix": "value"}, OS: map[string]string{"SUFFIX": "-"},
		Addons: map[string]string{"counter": `export const next = n => Number(n || 0) + 1;`}}
	for i, direction := range []string{"out", "in"} {
		x := &capture.ScriptExchange{Phase: "message", Rule: "Messages", Locals: locals,
			Message: &capture.ScriptMessage{Type: "text", Direction: direction, Data: []byte("old")}}
		out, err := s.Run(t.Context(), x, env)
		if err != nil {
			t.Fatal(err)
		}
		count := []string{"1", "2"}[i]
		if x.Message.Drop != (direction == "out") || string(x.Message.Data) != "value-"+count || locals["count"] != count {
			t.Fatalf("message = %+v, locals = %v", x.Message, locals)
		}
		if !reflect.DeepEqual(out.Writes, []Write{{Scope: "global", Name: "count", Value: count}}) || !strings.Contains(consoleText(out.Console), "message Messages "+count) {
			t.Fatalf("output = %+v", out)
		}
	}
}

func TestProxyScriptMessageMetadataAndHandshakeAreReadOnly(t *testing.T) {
	s, err := ParseProxyScript("ReadOnly", `export function onMessage(message) {
	const before = JSON.stringify(pm.request.toJSON());
	if (pm.request.method !== "GET" || pm.request.headers.get("X-Token") !== "abc" || pm.request.url.query.get("page") !== "2") throw new Error("handshake metadata");
	[
		() => { message.type = "binary"; },
		() => { message.direction = "in"; },
		() => { pm.request.method = "DELETE"; },
		() => { pm.request.url = "https://changed.test/"; },
		() => { pm.request.body.raw = "changed"; },
		() => pm.request.body.update("changed"),
		() => pm.request.headers.upsert("X-Token", "changed"),
		() => pm.request.headers.clear(),
		() => pm.request.url.query.upsert("page", "3"),
		() => pm.request.url.update("https://changed.test/"),
		() => { pm.request = {}; }
	].forEach(edit => {
		let rejected = false;
		try { edit(); } catch (e) { rejected = e.message.includes("read-only"); }
		if (!rejected || JSON.stringify(pm.request.toJSON()) !== before) throw new Error("mutation was allowed");
	});
	if (message.type !== "text" || message.direction !== "out") throw new Error("message metadata changed");
	message.setText("changed");
}`)
	if err != nil {
		t.Fatal(err)
	}
	x := &capture.ScriptExchange{Phase: "message", Method: "GET", URL: "https://api.test/?page=2", Header: http.Header{"X-Token": {"abc"}}, Body: []byte("original"),
		Message: &capture.ScriptMessage{Type: "text", Direction: "out", Data: []byte("old")}}
	if _, err := s.Run(t.Context(), x, ProxyEnv{}); err != nil {
		t.Fatal(err)
	}
	if x.Method != "GET" || x.URL != "https://api.test/?page=2" || x.Header.Get("X-Token") != "abc" || string(x.Body) != "original" || string(x.Message.Data) != "changed" {
		t.Fatalf("exchange = %+v", x)
	}
}

func TestProxyScriptMessageFailuresAreTransactional(t *testing.T) {
	for name, tc := range map[string]struct{ source, want string }{
		"throw":               {`throw new Error("failed");`, "failed"},
		"bad base64":          {`message.setBase64("not base64");`, "padded base64"},
		"missing padding":     {`message.setBase64("Zg");`, "padded base64"},
		"noncanonical bits":   {`message.setBase64("Zh==");`, "padded base64"},
		"newline":             {`message.setBase64("Zg==\n");`, "padded base64"},
		"URL alphabet":        {`message.setBase64("_w==");`, "padded base64"},
		"oversize text":       {`message.setText("a".repeat(1048577));`, "1 MiB"},
		"oversize UTF8":       {`message.setText("é".repeat(524289));`, "1 MiB"},
		"oversize binary":     {`message.setBase64(pm.environment.get("oversize"));`, "1 MiB"},
		"lone high surrogate": {`message.setText("\uD800");`, "UTF-8"},
		"lone low surrogate":  {`message.setText("\uDC00");`, "UTF-8"},
		"text argument":       {`message.setText(42);`, "requires a string"},
		"base64 argument":     {`message.setBase64(null);`, "requires a string"},
		"environment":         {`pm.environment.set("key", "value");`, "read-only"},
		"pending":             {`await new Promise(() => {});`, "never finished"},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := ParseProxyScript("Failure", "export async function onMessage(message) {\nmessage.setText('not published'); message.drop();\n"+tc.source+"\n}")
			if err != nil {
				t.Fatal(err)
			}
			message := &capture.ScriptMessage{Type: "binary", Direction: "in", Data: []byte{255, 128, 0, 254}}
			want := *message
			want.Data = bytes.Clone(message.Data)
			x := &capture.ScriptExchange{Phase: "message", Message: message}
			env := ProxyEnv{}
			if name == "oversize binary" {
				env.Environment = map[string]string{"oversize": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{255}, (1<<20)+1))}
			}
			if _, err := s.Run(t.Context(), x, env); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			} else if name == "throw" && !strings.Contains(err.Error(), "Failure.js:3:") {
				t.Fatalf("error lost source location: %v", err)
			}
			if !reflect.DeepEqual(*message, want) {
				t.Fatalf("failed hook published message: %+v, want %+v", message, want)
			}
		})
	}
}

func TestProxyScriptMessagePayloadLimitBoundary(t *testing.T) {
	data := bytes.Repeat([]byte("a"), 1<<20)
	for _, kind := range []string{"text", "binary"} {
		s, err := ParseProxyScript("Boundary", `export function onMessage(message) {
	if (message.type === "text") message.setText(message.text());
	else message.setBase64(message.base64());
}`)
		if err != nil {
			t.Fatal(err)
		}
		x := &capture.ScriptExchange{Phase: "message", Message: &capture.ScriptMessage{Type: kind, Direction: "out", Data: bytes.Clone(data)}}
		if _, err := s.Run(t.Context(), x, ProxyEnv{}); err != nil || x.Message.Type != kind || !bytes.Equal(x.Message.Data, data) {
			t.Fatalf("%s: err = %v, bytes = %d", kind, err, len(x.Message.Data))
		}
	}
}

func TestProxyScriptMessageHookSelectionAndValidation(t *testing.T) {
	s, err := ParseProxyScript("Missing", `throw new Error("must not run"); function onMessage() {}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(t.Context(), &capture.ScriptExchange{Phase: "message"}, ProxyEnv{}); err != nil {
		t.Fatalf("unexported hook ran: %v", err)
	}
	s, err = ParseProxyScript("Validate", `export function onMessage(message) { message.setText("changed"); }`)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []*capture.ScriptMessage{
		nil,
		{Type: "text", Data: []byte{255}},
		{Type: "other"},
		{Type: "binary", Data: make([]byte, (1<<20)+1)},
	} {
		if _, err := s.Run(t.Context(), &capture.ScriptExchange{Phase: "message", Message: message}, ProxyEnv{}); err == nil {
			t.Errorf("invalid input accepted: %v", message == nil)
		}
	}
}

func TestProxyScriptMessageTimeoutAndCancellation(t *testing.T) {
	s, err := ParseProxyScript("Loop", `export function onMessage(message) {
	message.setText("not published"); message.drop();
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
			x := &capture.ScriptExchange{Phase: "message", Message: &capture.ScriptMessage{Type: "binary", Direction: "out", Data: []byte{255, 0, 128}}}
			_, err := s.Run(ctx, x, ProxyEnv{})
			want := context.DeadlineExceeded
			if mode == "cancel" {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("err = %v, want %v", err, want)
			}
			if x.Message.Type != "binary" || x.Message.Direction != "out" || x.Message.Drop || !bytes.Equal(x.Message.Data, []byte{255, 0, 128}) {
				t.Fatalf("interrupted hook published message: %+v", x.Message)
			}
		})
	}
}
