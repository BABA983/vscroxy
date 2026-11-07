package request

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baba983/vscroxy/internal/graphql"
	"github.com/baba983/vscroxy/internal/vault"
)

// One send through a collection and a folder: inherited auth from the vault,
// scopes resolving nearest first, and scripts collection → folder → request.
func TestExecuteInheritsFromCollectionAndFolder(t *testing.T) {
	var got http.Header
	var path string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, path = r.Header.Clone(), r.URL.RequestURI()
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	srv := testProxy(t)
	secrets := vault.Secrets{{Name: "token", Value: "from-vault", Domains: []string{"127.0.0.1"}}}
	in := Context{
		Globals: map[string]string{"base": "http://global.invalid", "region": "global", "only": "g"},
		Folders: []Folder{
			{Path: "Shop", Auth: Auth{Type: "Bearer", Token: "{{vault:token}}"},
				Variables: []Variable{{Name: "base", Initial: "http://collection.invalid"}, {Name: "region", Initial: "eu"}, {Name: "off", Initial: "x", Disabled: true}},
				Pre:       `pm.environment.set("order", (pm.variables.get("order") || "") + "C")`, Post: `pm.environment.set("post", (pm.variables.get("post") || "") + "C")`},
			{Path: "Shop/Orders", Auth: Auth{Type: AuthInherit},
				Variables: []Variable{{Name: "base", Initial: upstream.URL}},
				Pre:       `pm.environment.set("order", (pm.variables.get("order") || "") + "F")`, Post: `pm.environment.set("post", (pm.variables.get("post") || "") + "F")`},
		},
		Vault: secrets.Lookup,
	}
	r := Request{Method: "GET", Collection: "Shop/Orders", Auth: Auth{Type: AuthInherit},
		URL:     "{{base}}/orders?a={{$guid}}&b={{$guid}}",
		Headers: "X-Order: {{order}}\nX-Region: {{region}}\nX-Only: {{only}}\nX-Env: {{env}}\nX-Off: {{off}}",
		Pre:     `pm.environment.set("order", (pm.variables.get("order") || "") + "R")`, Post: `pm.environment.set("post", (pm.variables.get("post") || "") + "R")`}
	out := r.Execute(t.Context(), srv, in, map[string]string{"env": "dev", "region": "env-wins"}, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	for name, want := range map[string]string{
		"Authorization": "Bearer from-vault", // Inherit skipped the folder up to the collection
		"X-Order":       "CFR",               // pre-request scripts, collection first
		"X-Region":      "env-wins",          // environment over collection over global
		"X-Only":        "g",                 // globals fill what nothing else defines
		"X-Env":         "dev",
		"X-Off":         "{{off}}", // a disabled variable is not defined
	} {
		if got.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, got.Get(name), want)
		}
	}
	guids := regexp.MustCompile(`a=([0-9a-f-]{36})&b=([0-9a-f-]{36})$`).FindStringSubmatch(path)
	if guids == nil || guids[1] == guids[2] {
		t.Errorf("each {{$guid}} must be a fresh UUID: %q", path)
	}
	if out.Vars["post"] != "CFR" {
		t.Errorf("post-response scripts ran %q, want CFR", out.Vars["post"])
	}
	// Scripts' writes and the environment come back; inherited and vault
	// values never leak into what the app saves to the environment.
	for _, name := range []string{"base", "only", "token", "vault:token"} {
		if _, ok := out.Vars[name]; ok {
			t.Errorf("Execution.Vars carries inherited %q", name)
		}
	}
	sources := map[string]string{}
	for _, v := range out.Variables {
		sources[v.Name] = v.Source
		if v.Name == "vault:token" && v.Value != "" {
			t.Error("a vault value was copied into the resolved variables")
		}
	}
	for name, want := range map[string]string{"base": "Collection: Shop/Orders", "only": "Global", "vault:token": "Vault", "order": "Pre-request script", "env": "Run input"} {
		if sources[name] != want {
			t.Errorf("source of %s = %q, want %q", name, sources[name], want)
		}
	}
}

func TestExecuteCommonDynamicVariables(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		w.Write(body)
	}))
	defer upstream.Close()
	cases := []struct{ name, pattern string }{
		{"$randomIPV6", `[0-9a-f:]+`},
		{"$randomMACAddress", `(?:[0-9a-f]{2}:){5}[0-9a-f]{2}`},
		{"$randomProtocol", `https?`},
		{"$randomSemver", `(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)`},
		{"$randomFirstName", `[A-Z][a-z]+`},
		{"$randomLastName", `[A-Z][a-z]+`},
		{"$randomFullName", `[A-Z][a-z]+ [A-Z][a-z]+`},
		{"$randomPhoneNumber", `[0-9]{3}-[0-9]{3}-[0-9]{4}`},
		{"$randomCountryCode", `AU|BR|CA|CN|DE|FR|GB|IN|JP|US`},
		{"$randomLatitude", `-?[0-9]+\.[0-9]{4}`},
		{"$randomLongitude", `-?[0-9]+\.[0-9]{4}`},
		{"$randomExampleEmail", `[a-z0-9]+@example\.(?:com|net|org)`},
		{"$randomDomainName", `[a-z0-9]+\.example\.(?:com|net|org)`},
		{"$randomDomainSuffix", `com|net|org`},
		{"$randomDomainWord", `[a-z0-9]+`},
		{"$randomUrl", `https?://[a-z0-9]+\.example\.(?:com|net|org)`},
		{"$randomPrice", `[0-9]+\.[0-9]{2}`},
		{"$randomColor", `red|green|blue|yellow|purple|orange|black|white|gray|pink`},
		{"$randomWeekday", `Monday|Tuesday|Wednesday|Thursday|Friday|Saturday|Sunday`},
		{"$randomMonth", `January|February|March|April|May|June|July|August|September|October|November|December`},
	}
	template := map[string][]string{}
	for _, tc := range cases {
		for range 64 {
			template[tc.name] = append(template[tc.name], "{{"+tc.name+"}}")
		}
	}
	body, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	r := Request{Method: "POST", URL: upstream.URL, Body: string(body),
		Pre: `pm.variables.set("generated", pm.variables.replaceIn(pm.request.body.raw))`}
	out := r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	for source, body := range map[string]string{"sent body": out.Response.ResponseBody, "script replaceIn": out.Locals["generated"]} {
		t.Run(source, func(t *testing.T) {
			var values map[string][]string
			if err := json.Unmarshal([]byte(body), &values); err != nil {
				t.Fatal(err)
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					pattern := regexp.MustCompile("^(?:" + tc.pattern + ")$")
					if len(values[tc.name]) != 64 {
						t.Fatalf("got %d samples, want 64", len(values[tc.name]))
					}
					switch tc.name {
					case "$randomIPV6", "$randomMACAddress", "$randomDomainWord", "$randomDomainName", "$randomUrl", "$randomExampleEmail":
						if values[tc.name][0] == values[tc.name][1] {
							t.Fatal("repeated occurrences reused the same random value")
						}
					}
					for _, value := range values[tc.name] {
						if !pattern.MatchString(value) {
							t.Fatalf("%q does not match %s", value, tc.pattern)
						}
						switch tc.name {
						case "$randomIPV6":
							if addr, err := netip.ParseAddr(value); err != nil || !addr.Is6() {
								t.Fatalf("not IPv6: %q", value)
							}
						case "$randomLatitude", "$randomLongitude", "$randomPrice":
							lo, hi := -90.0, 90.0
							if tc.name == "$randomLongitude" {
								lo, hi = -180, 180
							} else if tc.name == "$randomPrice" {
								lo, hi = 0, 1000
							}
							if n, err := strconv.ParseFloat(value, 64); err != nil || n < lo || n > hi {
								t.Fatalf("%q is outside [%g, %g]", value, lo, hi)
							}
						}
					}
				})
			}
		})
	}
	if len(out.Vars) != 0 {
		t.Errorf("generated values leaked into the environment: %v", out.Vars)
	}
}

func TestExecuteDynamicVariableOverrides(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		fmt.Fprintf(w, "%s\n%s\n%s\n", r.URL.Path, r.Header.Get("X-Name"), r.Header.Get("Authorization"))
		w.Write(body)
	}))
	defer upstream.Close()
	r := Request{Method: "POST", URL: upstream.URL + "/{{$randomDomainWord}}",
		Headers: "X-Name: {{$randomFirstName}}", Auth: Auth{Type: "Bearer", Token: "{{$randomProtocol}}"},
		Body: "{{$randomFirstName}}|{{$randomLastName}}|{{$randomNotImplemented}}|{{$randomIpv6}}",
		Pre: `pm.test("environment override", () => pm.expect(pm.variables.replaceIn("{{$randomFirstName}}")).to.equal("environment"));
		pm.variables.set("$randomFirstName", "local");
		pm.test("empty override", () => pm.expect(pm.variables.replaceIn("{{$randomLastName}}")).to.equal(""));`}
	vars := map[string]string{"$randomDomainWord": "orders", "$randomFirstName": "environment", "$randomLastName": "", "$randomProtocol": "custom-token"}
	out := r.Execute(t.Context(), testProxy(t), Context{Globals: map[string]string{"$randomFirstName": "global"}}, vars, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	if got, want := out.Response.ResponseBody, "/orders\nlocal\nBearer custom-token\nlocal||{{$randomNotImplemented}}|{{$randomIpv6}}"; got != want {
		t.Errorf("sent = %q, want %q", got, want)
	}
	if len(out.Tests) != 2 || !out.Tests[0].Pass || !out.Tests[1].Pass {
		t.Errorf("script expansion = %+v", out.Tests)
	}
	if vars["$randomFirstName"] != "environment" || !strings.Contains(r.Body, "{{$randomFirstName}}") {
		t.Error("sending changed the authored request or input variables")
	}
}

func TestBinaryBodyThroughTheProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("X-Body-Type", r.Header.Get("Content-Type"))
		w.Write(body)
	}))
	defer upstream.Close()
	srv := testProxy(t)
	path := filepath.Join(t.TempDir(), "payload.bin")
	r := Request{Method: "POST", URL: upstream.URL, BodyMode: "Binary", BodyFile: "{{upload}}",
		Body: "{{inactive_raw}}", Form: []Field{{Name: "{{inactive_form}}", Value: "ignored"}},
		Headers: "Content-Length: 1", Checks: Checks{Status: "200"},
		Pre: `pm.variables.set('upload', pm.environment.get('file'));`}
	if got := r.Variables(); !slices.Equal(got, []string{"upload"}) {
		t.Fatalf("variables = %q; want only the active file path", got)
	}
	for _, payload := range [][]byte{[]byte("\x00\xff\xfe\r\n{{token}}|{{$randomUUID}}|中文"), {}, []byte("file changed")} {
		if err := os.WriteFile(path, payload, 0600); err != nil {
			t.Fatal(err)
		}
		for _, contentType := range []string{"", "image/custom"} {
			r.Headers = "Content-Length: 1"
			if contentType != "" {
				r.Headers += "\nContent-Type: " + contentType
			}
			out := r.Execute(t.Context(), srv, Context{}, map[string]string{"file": path, "token": "must-not-expand"}, nil)
			if out.Err != nil || out.Response == nil || out.Failed() {
				t.Fatalf("binary send: %s: %v", out.Stage, out.Err)
			}
			if !bytes.Equal([]byte(out.Response.ResponseBody), payload) || out.Request.Body != string(payload) {
				t.Fatal("binary bytes changed or a previous send's contents were reused")
			}
			wantType := contentType
			if wantType == "" {
				wantType = "application/octet-stream"
			}
			if got := out.Response.ResponseHeader.Get("X-Body-Type"); got != wantType {
				t.Errorf("Content-Type = %q, want %q", got, wantType)
			}
		}
	}
	// An explicit script body replaces the file, even if the path is missing.
	r.BodyFile, r.Pre = "missing.bin", `pm.request.body.update('script-body');`
	out := r.Execute(t.Context(), srv, Context{}, nil, nil)
	if out.Err != nil || out.Response == nil || out.Response.ResponseBody != "script-body" {
		t.Fatalf("script override: %s: %v", out.Stage, out.Err)
	}
}

func TestGraphQLBodyPreparationAndExecution(t *testing.T) {
	r := Request{Method: "POST", URL: "https://example.test/graphql", BodyMode: "GraphQL",
		GraphQL: GraphQLBody{Query: "query Search($q: String!) {\n  search(q: $q) { {{fields}} }\n}", Variables: `{"q":"{{term}}","limit":{{limit}},"id":9007199254740993}`},
		Body:    "{{inactive_raw}}", BodyFile: "{{inactive_file}}", Form: []Field{{Name: "{{inactive_form}}"}}, Headers: "Content-Length: 1"}
	if got := r.Variables(); !slices.Equal(got, []string{"fields", "limit", "term"}) {
		t.Fatalf("variables = %q", got)
	}
	vars := map[string]string{"fields": "id name", "term": `shoes & 中文`, "limit": "3"}
	prepared, err := r.Prepare(vars)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Query     string `json:"query"`
		Variables struct {
			Q     string `json:"q"`
			Limit int    `json:"limit"`
			ID    int64  `json:"id"`
		} `json:"variables"`
	}
	if err := json.Unmarshal([]byte(prepared.Body), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Query != "query Search($q: String!) {\n  search(q: $q) { id name }\n}" || envelope.Variables.Q != "shoes & 中文" || envelope.Variables.Limit != 3 || envelope.Variables.ID != 9007199254740993 {
		t.Fatalf("encoded GraphQL = %+v", envelope)
	}
	if prepared.Header.Get("Content-Type") != "application/json" || prepared.Header.Get("Content-Length") != "" {
		t.Fatalf("headers = %v", prepared.Header)
	}
	r.Headers = "Content-Type: application/custom+json"
	r.GraphQL = GraphQLBody{Query: `{ viewer { id } }`}
	prepared, err = r.Prepare(nil)
	if err != nil || prepared.Body != `{"query":"{ viewer { id } }","variables":{}}` || prepared.Header.Get("Content-Type") != "application/custom+json" {
		t.Fatalf("empty variables/custom header = %+v, %v", prepared, err)
	}
	for _, variables := range []string{"null", "[]", `"text"`, "42", "true", `{"n":}`, `{} {}`, `{"n":{{missing}}}`, `{"n":1,"n":2}`} {
		r.GraphQL.Variables = variables
		if _, err := r.Prepare(nil); err == nil {
			t.Errorf("accepted invalid variables %q", variables)
		}
	}
	r.GraphQL = GraphQLBody{Query: " \n\t"}
	if _, err := r.Prepare(nil); err == nil {
		t.Fatal("accepted an empty query")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "POST" || req.Header.Get("Content-Type") != "application/json" {
			t.Errorf("GraphQL wire request = %s, %v", req.Method, req.Header)
		}
		io.Copy(w, req.Body)
	}))
	defer upstream.Close()
	r.URL, r.Headers = upstream.URL, "Content-Length: 1"
	r.GraphQL = GraphQLBody{Query: `query { {{field}} } # {{$randomUUID}}`, Variables: `{"n":{{n}},"uuid":"{{$randomUUID}}","secret":"{{vault:token}}"}`}
	r.Pre = `pm.variables.set('field', 'viewer { id }'); pm.variables.set('n', '7');`
	r.Checks = Checks{Status: "200"}
	secrets := vault.Secrets{{Name: "token", Value: "local-secret", Domains: []string{"127.0.0.1"}}}
	srv := testProxy(t)
	out := r.Execute(t.Context(), srv, Context{Vault: secrets.Lookup}, nil, nil)
	if out.Err != nil || out.Response == nil || out.Failed() {
		t.Fatalf("GraphQL execute = %s, %v", out.Stage, out.Err)
	}
	pattern := `^\{"query":"query \{ viewer \{ id \} \} # [a-f0-9-]{36}","variables":\{"n":7,"uuid":"[a-f0-9-]{36}","secret":"local-secret"\}\}$`
	if !regexp.MustCompile(pattern).MatchString(out.Response.ResponseBody) || out.Request.Body != out.Response.ResponseBody {
		t.Fatalf("GraphQL wire body = %q", out.Response.ResponseBody)
	}
	// A raw script override wins even over an invalid inactive GraphQL body.
	r.GraphQL.Variables = "invalid"
	r.Pre = `pm.request.body.update('script-body'); pm.request.headers.upsert('Content-Type', 'application/json');`
	out = r.Execute(t.Context(), srv, Context{}, nil, nil)
	if out.Err != nil || out.Response == nil || out.Response.ResponseBody != "script-body" {
		t.Fatalf("GraphQL raw override = %s, %v", out.Stage, out.Err)
	}
}

func TestBinaryBodyFileLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "limit.bin")
	for _, size := range []int{(16 << 20) - 1, 16 << 20, (16 << 20) + 1} {
		if err := os.WriteFile(path, bytes.Repeat([]byte{0xa7}, size), 0600); err != nil {
			t.Fatal(err)
		}
		r := Request{URL: "http://example.test", BodyMode: "Binary", BodyFile: path}
		prepared, err := r.Prepare(nil)
		if size <= 16<<20 {
			if err != nil || len(prepared.Body) != size || prepared.Body[0] != 0xa7 || prepared.Body[size-1] != 0xa7 {
				t.Fatalf("%d-byte file not preserved: %v", size, err)
			}
		} else if err == nil {
			t.Fatal("accepted a binary body larger than 16 MiB")
		}
	}
	for _, invalid := range []string{"", dir, filepath.Join(dir, "missing")} {
		r := Request{URL: "http://example.test", BodyMode: "Binary", BodyFile: invalid, Body: "must not fall back"}
		if _, err := r.Prepare(nil); err == nil {
			t.Errorf("invalid binary file %q was accepted", invalid)
		}
	}
}

func TestVaultSecretNeverReachesAnotherHost(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer upstream.Close()
	secrets := vault.Secrets{{Name: "token", Value: "s", Domains: []string{"api.example.com"}}}
	r := Request{Method: "GET", URL: upstream.URL, Headers: "Authorization: Bearer {{vault:token}}"}
	out := r.Execute(t.Context(), testProxy(t), Context{Vault: secrets.Lookup}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "not allowed") || hits.Load() != 0 {
		t.Fatalf("err = %v, upstream hits = %d; want a refusal before sending", out.Err, hits.Load())
	}
	out = r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
	if out.Err == nil || hits.Load() != 0 {
		t.Fatalf("without a vault: err = %v, hits = %d", out.Err, hits.Load())
	}
}

func TestChainAndInheritAuth(t *testing.T) {
	folders := []Folder{{Path: "Shop/Orders/Refunds"}, {Path: "Shop", Auth: Auth{Type: "Basic"}}, {Path: "Shop/Orders", Auth: Auth{Type: AuthInherit}}, {Path: "Shopping", Auth: Auth{Type: "Bearer"}}}
	var paths []string
	for _, f := range Chain(folders, "Shop/Orders/Refunds") {
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, ",") != "Shop,Shop/Orders,Shop/Orders/Refunds" {
		t.Fatalf("Chain = %q", paths)
	}
	in := Context{Folders: Chain(folders, "Shop/Orders/Refunds")}
	if auth, from := in.InheritAuth(Auth{Type: AuthInherit}); auth.Type != "Basic" || from != "Shop" {
		t.Errorf("inherited %q from %q, want Basic from Shop", auth.Type, from)
	}
	if auth, _ := in.InheritAuth(Auth{Type: "None"}); auth.Type != "None" {
		t.Error("an explicit auth must not inherit")
	}
	if auth, from := (Context{}).InheritAuth(Auth{Type: AuthInherit}); auth.Type != "None" || from != "" {
		t.Error("nothing to inherit must send no auth")
	}
}

func TestGraphQLOperationNameSelection(t *testing.T) {
	multi := Request{Method: "POST", URL: "https://example.test/graphql", BodyMode: "GraphQL",
		GraphQL: GraphQLBody{Query: "query Read { viewer { id } } mutation Write { save { id } }"}}
	if _, err := multi.Prepare(nil); err == nil {
		t.Fatal("a multi-operation document without a choice was sent")
	}
	decode := func(t *testing.T, body string) (query, operationName string) {
		t.Helper()
		var envelope struct {
			Query         string `json:"query"`
			OperationName string `json:"operationName"`
		}
		if err := json.Unmarshal([]byte(body), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Query, envelope.OperationName
	}
	for _, name := range []string{"Write", "Missing"} {
		multi.GraphQL.OperationName = name
		prepared, err := multi.Prepare(nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		query, operation := decode(t, prepared.Body)
		if query != multi.GraphQL.Query || operation != name {
			t.Fatalf("%s envelope = %q, %q", name, query, operation)
		}
	}
	multi.GraphQL = GraphQLBody{Query: "query Read { viewer { id } }", OperationName: "Stale"}
	prepared, err := multi.Prepare(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, operation := decode(t, prepared.Body); operation != "Read" {
		t.Fatalf("single operation used the stale name: %q", operation)
	}
	multi.GraphQL = GraphQLBody{Query: "mutation Save { save { id } }", OperationName: "Write"}
	prepared, err = multi.Prepare(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, operation := decode(t, prepared.Body); operation != "Save" {
		t.Fatalf("renamed single operation used the stale name: %q", operation)
	}
	multi.GraphQL = GraphQLBody{Query: "query Read { viewer { id } }"}
	prepared, err = multi.Prepare(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, operation := decode(t, prepared.Body); operation != "Read" {
		t.Fatalf("single operation was not selected automatically: %q", operation)
	}
	multi.GraphQL = GraphQLBody{Query: "{ viewer { id } }", OperationName: "Imported"}
	prepared, err = multi.Prepare(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, operation := decode(t, prepared.Body); operation != "" {
		t.Fatalf("anonymous operation carried %q", operation)
	}
	multi.GraphQL = GraphQLBody{Query: "query A { a } query B { b }", OperationName: "not a name!"}
	if _, err := multi.Prepare(nil); err == nil || !strings.Contains(err.Error(), "invalid operationName") {
		t.Fatalf("invalid multi-operation name was sent: %v", err)
	}
	multi.GraphQL = GraphQLBody{Query: "{ user(id: 1) { name }", OperationName: "not a name!"}
	if _, err := multi.Prepare(nil); err == nil || !strings.Contains(err.Error(), "invalid operationName") {
		t.Fatalf("invalid malformed-document name was sent: %v", err)
	}
	// Malformed documents and missing variables are sent to the server so an
	// API client can test its error handling. A saved name still goes along.
	malformed := []string{
		`{ user(id: 1) { name }`,
		`{ user(id: {{missing}}) { id } }`,
		strings.Repeat("{ f", 65) + strings.Repeat("}", 65),
		`query A { a } query A { b }`,
		`fragment Only on Query { viewer { id } }`,
	}
	for _, query := range malformed {
		r := Request{Method: "POST", URL: "https://example.test/graphql", BodyMode: "GraphQL",
			GraphQL: GraphQLBody{Query: query}}
		prepared, err := r.Prepare(nil)
		if err != nil {
			t.Errorf("malformed document %q was refused: %v", query, err)
			continue
		}
		gotQuery, operation := decode(t, prepared.Body)
		if gotQuery != query || operation != "" {
			t.Errorf("malformed envelope = %q, %q", gotQuery, operation)
		}
		r.GraphQL.OperationName = "Chosen"
		prepared, err = r.Prepare(nil)
		if err != nil {
			t.Errorf("malformed document with a name %q was refused: %v", query, err)
			continue
		}
		if _, operation := decode(t, prepared.Body); operation != "Chosen" {
			t.Errorf("malformed document lost the saved name: %q", operation)
		}
	}
}

func TestGraphQLOperationNameReachesExports(t *testing.T) {
	r := Request{Method: "POST", URL: "https://example.test/graphql", BodyMode: "GraphQL",
		GraphQL: GraphQLBody{Query: "query Read { viewer { id } } mutation Write { save { id } }", OperationName: "Write"}}
	prepared, err := r.Prepare(nil)
	if err != nil {
		t.Fatal(err)
	}
	snippet, err := Snippet("JavaScript (fetch)", prepared)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snippet, "operationName") || !strings.Contains(snippet, `Write`) {
		t.Fatalf("snippet lost operationName:\n%s", snippet)
	}
	ops, err := graphql.RequestOperations("POST", "", "application/json", prepared.Body)
	if err != nil || len(ops) != 1 || ops[0].Name != "Write" || ops[0].Type != "mutation" {
		t.Fatalf("capture recognition = %+v, %v", ops, err)
	}
	single := Request{Method: "POST", URL: "https://example.test/graphql", BodyMode: "GraphQL",
		GraphQL: GraphQLBody{Query: "mutation Save { save { id } }", OperationName: "Write"}}
	prepared, err = single.Prepare(nil)
	if err != nil {
		t.Fatal(err)
	}
	ops, err = graphql.RequestOperations("POST", "", "application/json", prepared.Body)
	if err != nil || len(ops) != 1 || ops[0].Name != "Save" || ops[0].Type != "mutation" {
		t.Fatalf("single-operation capture recognition = %+v, %v", ops, err)
	}
}
