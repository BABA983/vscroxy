package request

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/vault"
)

// A Postman-style flow through the proxy: a pre-request script fetches a
// token with pm.sendRequest, edits the request through pm.request, and
// post-response tests use pm.response, pm.expect and the legacy globals.
func TestPostmanScriptsThroughTheProxy(t *testing.T) {
	var orders *http.Request
	var tokenBody, tokenType string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			b, _ := io.ReadAll(r.Body)
			tokenBody, tokenType = string(b), r.Header.Get("Content-Type")
			fmt.Fprint(w, `{"access_token":"t-123"}`)
		case "/orders":
			orders = r.Clone(t.Context())
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			json.MarshalWrite(w, map[string]any{"local": r.Header.Get("X-Local"), "items": []int{1, 2}})
		}
	}))
	defer upstream.Close()
	jar, _ := cookiejar.New(nil)
	in := Context{
		Globals: map[string]string{"g": "1"},
		Folders: []Folder{{Path: "Shop", Variables: []Field{{Name: "base", Value: upstream.URL}},
			Pre: `pm.collectionVariables.set("seen", "yes"); pm.globals.unset("g")`}},
	}
	r := Request{Name: "List orders", Method: "GET", Collection: "Shop/Orders",
		URL: "{{base}}/orders?page=1", Headers: "Authorization: Bearer {{token}}",
		Pre: `const res = await pm.sendRequest({
	url: pm.variables.get("base") + "/token", method: "POST",
	header: {"Content-Type": "application/json"},
	body: {mode: "raw", raw: JSON.stringify({u: 1})},
});
pm.environment.set("token", res.json().access_token);
pm.variables.set("local", "L");
pm.request.headers.add({key: "X-Local", value: "{{local}}"});
pm.request.url.query.upsert("page", "2");
console.log("event", pm.info.eventName, pm.execution.location.join("/"), pm.globals.has("g"));`,
		Post: `pm.test("status is 200", () => pm.response.to.have.status(200));
pm.test("body", () => {
	pm.expect(pm.response.json()).to.have.property("local", "L");
	pm.expect(pm.response.json().items).to.eql([1, 2]);
});
pm.test("a failing one", () => pm.expect(pm.response.code).to.equal(404));
pm.test.skip("later");
pm.test("cookie", () => pm.expect(pm.cookies.get("session")).to.equal("abc"));
tests["legacy"] = responseCode.code === 200 && JSON.parse(responseBody).local === "L";
pm.response.to.not.be.error;
pm.collectionVariables.set("lastStatus", pm.response.code);`}
	out := r.Execute(t.Context(), testProxy(t), in, map[string]string{"keep": "env"}, jar)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	if tokenBody != `{"u":1}` || tokenType != "application/json" {
		t.Errorf("token request body %q, type %q", tokenBody, tokenType)
	}
	if orders == nil {
		t.Fatal("the request was not sent")
	}
	if got := orders.Header.Get("Authorization"); got != "Bearer t-123" {
		t.Errorf("Authorization = %q: the environment value a pre-request script set must expand", got)
	}
	if got := orders.URL.RawQuery; got != "page=2" {
		t.Errorf("query = %q, want the upserted page=2 only", got)
	}
	type result struct {
		Name, Error   string
		Pass, Skipped bool
	}
	var tests []result
	for _, test := range out.Tests {
		tests = append(tests, result{test.Name, test.Error, test.Pass, test.Skipped})
	}
	want := []result{
		{Name: "status is 200", Pass: true},
		{Name: "body", Pass: true},
		{Name: "a failing one", Error: "expected 200 to equal 404"},
		{Name: "later", Pass: true, Skipped: true},
		{Name: "cookie", Pass: true},
		{Name: "legacy", Pass: true},
	}
	if !slices.Equal(tests, want) {
		t.Errorf("tests =\n%+v\nwant\n%+v", tests, want)
	}
	if out.Vars["token"] != "t-123" || out.Vars["keep"] != "env" {
		t.Errorf("environment after the run = %v", out.Vars)
	}
	if _, ok := out.Vars["local"]; ok || out.Locals["local"] != "L" {
		t.Errorf("pm.variables.set must stay local: vars %v, locals %v", out.Vars, out.Locals)
	}
	wantWrites := []Write{
		{Scope: "collection", Path: "Shop", Name: "seen", Value: "yes"},
		{Scope: "global", Name: "g", Unset: true},
		{Scope: "collection", Path: "Shop", Name: "lastStatus", Value: "200"},
	}
	if !slices.Equal(out.Writes, wantWrites) {
		t.Errorf("writes = %+v, want %+v", out.Writes, wantWrites)
	}
	if !slices.ContainsFunc(out.Console, func(m ConsoleMessage) bool {
		return m.Phase == "pre-request" && m.Text == "event prerequest Shop/Orders/List orders false"
	}) {
		t.Errorf("console = %+v", out.Console)
	}
	next := in.Apply(out.Writes, "Shop/Other")
	if _, ok := next.Globals["g"]; ok || in.Globals["g"] != "1" {
		t.Errorf("Apply globals = %v (original %v): it must remove g without mutating the original", next.Globals, in.Globals)
	}
	if got := next.Folders[0].Variables; len(got) != 3 || got[1] != (Field{Name: "seen", Value: "yes"}) || got[2] != (Field{Name: "lastStatus", Value: "200"}) {
		t.Errorf("Apply collection variables = %+v", got)
	}
	if other := in.Apply(out.Writes, "Elsewhere"); len(other.Folders[0].Variables) != 1 {
		t.Error("collection writes must not reach a request in another collection")
	}
}

func TestJSONSchemaAssertionsThroughTheProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/text" {
			fmt.Fprint(w, "not JSON")
			return
		}
		fmt.Fprint(w, `{"id":7,"items":[{"sku":"AB-12","quantity":2}],"email":"not-an-email"}`)
	}))
	defer upstream.Close()
	srv := testProxy(t)
	for _, tc := range []struct {
		name, path, script string
		pass               bool
		errors             []string
	}{
		{"nested reference", "/", `pm.response.to.have.jsonSchema({
			$schema: "http://json-schema.org/draft-07/schema#",
			type: "object", required: ["id", "items"],
			properties: {id: {type: "integer"}, items: {type: "array", minItems: 1, items: {$ref: "#/definitions/item"}}},
			definitions: {item: {type: "object", required: ["sku", "quantity"], additionalProperties: false,
				properties: {sku: {type: "string", pattern: "^[A-Z]{2}-[0-9]+$"}, quantity: {type: "integer", minimum: 1}}}}
		}).and.have.status(200)`, true, nil},
		{"all errors by default", "/", `pm.response.to.have.jsonSchema({type: "object", required: ["missing"], properties: {id: {type: "string"}}})`, false, []string{"missing", "should be string"}},
		{"first error option", "/", `pm.response.to.have.jsonSchema({type: "object", required: ["missing"], properties: {id: {type: "string"}}}, {allErrors: false})`, false, []string{"missing"}},
		{"negated mismatch", "/", `pm.response.to.not.have.jsonSchema({type: "array"})`, true, nil},
		{"negated match", "/", `pm.response.to.not.have.jsonSchema({type: "object"})`, false, []string{"expected data to not satisfy schema"}},
		{"format checked", "/", `pm.response.to.have.jsonSchema({properties: {email: {type: "string", format: "email"}}})`, false, []string{"email", "format"}},
		{"format option", "/", `pm.response.to.have.jsonSchema({properties: {email: {type: "string", format: "email"}}}, {format: false})`, true, nil},
		{"expect data", "/", `pm.expect(pm.response.json().items).to.have.jsonSchema({type: "array", items: {properties: {quantity: {enum: [2]}}}})`, true, nil},
		{"expect invalid data", "/", `pm.expect(pm.response.json().items).to.have.jsonSchema({type: "array", items: {properties: {quantity: {enum: [3]}}}})`, false, []string{"data[0].quantity", "allowed values"}},
		{"expect negation", "/", `pm.expect(7).to.not.have.jsonSchema({type: "string"})`, true, nil},
		{"coercion option", "/", `var data = {id: "7"}; pm.expect(data).to.have.jsonSchema({properties: {id: {type: "integer"}}}, {coerceTypes: true}); pm.expect(data.id).to.equal(7); pm.expect({id: "7"}).to.not.have.jsonSchema({properties: {id: {type: "integer"}}})`, true, nil},
		{"coercion off by default", "/", `pm.expect({id: "7"}).to.have.jsonSchema({properties: {id: {type: "integer"}}})`, false, []string{"id", "should be integer"}},
		{"registered reference", "/", `pm.response.to.have.jsonSchema({$ref: "https://schema.test/order"}, {schemas: [{$id: "https://schema.test/order", type: "object", required: ["id"]}]})`, true, nil},
		{"unresolved reference", "/", `pm.response.to.not.have.jsonSchema({$ref: "https://schema.test/missing"})`, false, []string{"can't resolve reference"}},
		{"invalid schema", "/", `pm.response.to.not.have.jsonSchema({type: "unknown"})`, false, []string{"schema is invalid"}},
		{"object schema required", "/", `pm.response.to.have.jsonSchema(true)`, false, []string{"to be an object"}},
		{"non JSON body", "/text", `pm.response.to.not.have.jsonSchema({type: "object"})`, false, []string{"response body is not JSON"}},
		{"async schema rejected", "/", `pm.response.to.have.jsonSchema({$async: true, type: "array"})`, false, []string{"Asynchronous JSON schemas are not supported"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Request{Method: "GET", URL: upstream.URL + tc.path,
				Post: "pm.test('schema', () => {" + tc.script + "}); pm.test('continues', () => pm.response.to.have.status(200));"}
			out := r.Execute(t.Context(), srv, Context{}, nil, nil)
			if out.Err != nil {
				t.Fatalf("%s: %v", out.Stage, out.Err)
			}
			if len(out.Tests) != 2 || !out.Tests[1].Pass {
				t.Fatalf("tests = %+v; want the schema result and a passing subsequent test", out.Tests)
			}
			got := out.Tests[0]
			if got.Pass != tc.pass {
				t.Errorf("pass = %v, want %v: %s", got.Pass, tc.pass, got.Error)
			}
			for _, fragment := range tc.errors {
				if !strings.Contains(got.Error, fragment) {
					t.Errorf("error = %q, want %q", got.Error, fragment)
				}
			}
			if tc.name == "first error option" && strings.Contains(got.Error, "should be string") {
				t.Errorf("allErrors: false collected the second error: %s", got.Error)
			}
		})
	}
}

func TestScriptControlsTheRun(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/slow" {
			time.Sleep(2100 * time.Millisecond)
		}
	}))
	defer upstream.Close()
	srv := testProxy(t)

	skip := Request{Method: "GET", URL: upstream.URL, Pre: `pm.execution.skipRequest()`, Post: `pm.test("never", () => {})`}
	out := skip.Execute(t.Context(), srv, Context{}, nil, nil)
	if !out.Skipped || out.Err != nil || hits != 0 || len(out.Tests) != 0 || out.Failed() || out.Outcome() != "Skipped by script" {
		t.Errorf("skipRequest: skipped %v, err %v, hits %d, tests %v", out.Skipped, out.Err, hits, out.Tests)
	}

	next := Request{Method: "GET", URL: upstream.URL, Post: `postman.setNextRequest("Checkout")`}
	if out := next.Execute(t.Context(), srv, Context{}, nil, nil); out.Next == nil || *out.Next != "Checkout" {
		t.Errorf("setNextRequest = %v", out.Next)
	}
	stop := Request{Method: "GET", URL: upstream.URL, Post: `pm.execution.setNextRequest(null)`}
	if out := stop.Execute(t.Context(), srv, Context{}, nil, nil); out.Next == nil || *out.Next != "" {
		t.Errorf("setNextRequest(null) = %v, want a stop", out.Next)
	}

	// Waiting on the network does not use up the script's two seconds.
	slow := Request{Method: "GET", URL: upstream.URL, Pre: `await pm.sendRequest("` + upstream.URL + `/slow")`}
	if out := slow.Execute(t.Context(), srv, Context{}, nil, nil); out.Err != nil {
		t.Errorf("a slow pm.sendRequest: %s: %v", out.Stage, out.Err)
	}

	broken := Request{Method: "GET", URL: upstream.URL, Post: "pm.test(\"ok\", () => {})\nnotDefined()"}
	out = broken.Execute(t.Context(), srv, Context{}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "ReferenceError: notDefined is not defined") || !strings.Contains(out.Err.Error(), "post-response.js:2") {
		t.Errorf("error = %v, want the ReferenceError at line 2", out.Err)
	}
	if len(out.Tests) != 1 || !out.Tests[0].Pass {
		t.Errorf("tests before the error = %+v, want them kept", out.Tests)
	}
}

func TestScriptsUseTheVaultOnlyWhenAllowed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer upstream.Close()
	srv := testProxy(t)
	secrets := vault.Secrets{
		{Name: "here", Value: "h-secret", Domains: []string{"127.0.0.1"}},
		{Name: "there", Value: "t-secret", Domains: []string{"api.example.com"}},
	}
	script := `const v = await pm.vault.get("here");
pm.test("read", () => pm.expect(v).to.equal("h-secret"));
pm.test("missing", async () => pm.expect(await pm.vault.get("nope")).to.be.undefined);
pm.test("other host", async () => {
	let refused = false;
	try { await pm.vault.get("there") } catch (e) { refused = /not allowed/.test(e.message) }
	pm.expect(refused).to.be.true;
});
await pm.vault.set("new", "n-secret");
pm.test("own write", async () => pm.expect(await pm.vault.get("new")).to.equal("n-secret"));`
	r := Request{Method: "GET", URL: upstream.URL, Post: script}

	out := r.Execute(t.Context(), srv, Context{Vault: secrets.Lookup}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "may not use the vault") {
		t.Errorf("without permission: err = %v", out.Err)
	}
	out = r.Execute(t.Context(), srv, Context{Vault: secrets.Lookup, ScriptVault: true}, nil, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	for _, test := range out.Tests {
		if !test.Pass {
			t.Errorf("%s: %s", test.Name, test.Error)
		}
	}
	if len(out.Tests) != 4 || !slices.Equal(out.Writes, []Write{{Scope: "vault", Name: "new", Value: "n-secret"}}) {
		t.Errorf("tests %+v, writes %+v", out.Tests, out.Writes)
	}
	for _, m := range out.Console {
		if strings.Contains(m.Text, "secret") && m.Text != "pm.vault.set(new)" {
			t.Errorf("console leaks a secret: %q", m.Text)
		}
	}
}
