package graphql_test

import (
	"encoding/json/v2"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

func envelope(t testing.TB, query string, extra ...string) string {
	t.Helper()
	raw, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	return `{"query":` + string(raw) + strings.Join(extra, "") + `}`
}

func TestRequestOperations(t *testing.T) {
	multi := `query Read { user { id } } mutation Write { update { id } } subscription Watch { changes { id } }`
	cases := []struct {
		name, method, rawURL, contentType, body string
		want                                    []graphql.Operation
		bad                                     bool
	}{
		{name: "ordinary GET", method: "GET", rawURL: "https://example.com/api?x=1"},
		{name: "ordinary text", method: "POST", contentType: "text/plain", body: multi},
		{name: "ordinary JSON", method: "POST", contentType: "application/json", body: `{"message":"query Fake { x }","variables":3}`},
		{name: "ordinary array", method: "POST", contentType: "application/json", body: `[{},1,null]`},
		{name: "ordinary empty batch", method: "POST", contentType: "application/json", body: `[]`},
		{name: "case-sensitive envelope", method: "POST", contentType: "application/json", body: `{"Query":"{ x }"}`},
		{name: "anonymous raw", method: "POST", contentType: "application/graphql; charset=utf-8", body: `{ viewer { id } }`, want: []graphql.Operation{{Type: "query"}}},
		{name: "raw named", method: "POST", contentType: "Application/GraphQL", body: `mutation Write { update }`, want: []graphql.Operation{{Name: "Write", Type: "mutation"}}},
		{name: "POST ignores URL selector", method: "POST", rawURL: "https://example.com/?operationName=Other", contentType: "application/graphql", body: `query Read { x }`, want: []graphql.Operation{{Name: "Read", Type: "query"}}},
		{name: "GET ignores body", method: "GET", rawURL: "https://example.com/?query=" + url.QueryEscape(`query Read { x }`), contentType: "application/graphql", body: "invalid", want: []graphql.Operation{{Name: "Read", Type: "query"}}},
		{name: "GET selects second", method: "GET", rawURL: "/graphql?query=" + url.QueryEscape(multi) + "&operationName=Write", want: []graphql.Operation{{Name: "Write", Type: "mutation"}}},
		{name: "JSON selects first", method: "POST", contentType: "application/json", body: envelope(t, multi, `,"operationName":"Read"`), want: []graphql.Operation{{Name: "Read", Type: "query"}}},
		{name: "JSON selects last", method: "POST", contentType: "application/graphql-request+json; charset=UTF-8", body: envelope(t, multi, `,"operationName":"Watch"`), want: []graphql.Operation{{Name: "Watch", Type: "subscription"}}},
		{name: "JSON null selector", method: "POST", contentType: "application/json", body: envelope(t, `{ x }`, `,"operationName":null`), want: []graphql.Operation{{Type: "query"}}},
		{name: "no float roundtrip", method: "POST", contentType: "application/json", body: envelope(t, `query Q($n: Float) { x(n:$n) }`, `,"variables":{"n":123456789012345678901234567890,"exponent":1e99999,"negativeZero":-0.00E+2}`), want: []graphql.Operation{{Name: "Q", Type: "query"}}},
		{name: "GET raw variables", method: "GET", rawURL: "/graphql?query=" + url.QueryEscape(`{ x }`) + "&variables=" + url.QueryEscape(`{"n":1e99999}`), want: []graphql.Operation{{Type: "query"}}},
		{name: "persisted only", method: "POST", contentType: "application/json", body: `{"operationName":"Unverified","extensions":{"persistedQuery":{"version":1,"sha256Hash":"abc"}}}`},
		{name: "persisted null query", method: "POST", contentType: "application/json", body: `{"query":null,"extensions":{"persistedQuery":{}}}`},
		{name: "persisted with document", method: "POST", contentType: "application/json", body: envelope(t, `query Verified { x }`, `,"extensions":{"persistedQuery":{}}`), want: []graphql.Operation{{Name: "Verified", Type: "query"}}},
		{name: "fragments", method: "POST", contentType: "application/json", body: envelope(t, `fragment Fields on User { id } query Read { user { ...Fields ... on User { name } } }`), want: []graphql.Operation{{Name: "Read", Type: "query"}}},
		{name: "no false names", method: "POST", contentType: "application/json", body: envelope(t, "# mutation Fake { ignored }\nquery Real { Fake: field(text: \"subscription Fake { nope }\") }"), want: []graphql.Operation{{Name: "Real", Type: "query"}}},
		{name: "string not selector", method: "POST", contentType: "application/json", body: envelope(t, `query Real { field(text: "query Fake { x }") }`, `,"operationName":"Fake"`), bad: true},
		{name: "comment not selector", method: "POST", contentType: "application/json", body: envelope(t, "# query Fake { x }\nquery Real { x }", `,"operationName":"Fake"`), bad: true},
		{name: "alias not selector", method: "POST", contentType: "application/json", body: envelope(t, `query Real { Fake: field }`, `,"operationName":"Fake"`), bad: true},
		{name: "fragment not selector", method: "POST", contentType: "application/json", body: envelope(t, `query Real { ...Fake } fragment Fake on Query { field }`, `,"operationName":"Fake"`), bad: true},
		{name: "selector case", method: "POST", contentType: "application/json", body: envelope(t, multi, `,"operationName":"write"`), bad: true},
		{name: "selector invalid", method: "POST", contentType: "application/json", body: envelope(t, multi, `,"operationName":"Write # comment"`), bad: true},
		{name: "selector empty", method: "POST", contentType: "application/json", body: envelope(t, `{ x }`, `,"operationName":""`), bad: true},
		{name: "selector space", method: "POST", contentType: "application/json", body: envelope(t, multi, `,"operationName":" Write"`), bad: true},
		{name: "selector Unicode", method: "POST", contentType: "application/json", body: envelope(t, multi, `,"operationName":"Écrire"`), bad: true},
		{name: "selector number", method: "POST", contentType: "application/json", body: envelope(t, `{ x }`, `,"operationName":1`), bad: true},
		{name: "ambiguous JSON", method: "POST", contentType: "application/json", body: envelope(t, multi), bad: true},
		{name: "ambiguous raw", method: "POST", contentType: "application/graphql", body: multi, bad: true},
		{name: "ambiguous GET", method: "GET", rawURL: "/graphql?query=" + url.QueryEscape(multi), bad: true},
		{name: "anonymous mixed", method: "POST", contentType: "application/json", body: envelope(t, `{ x } query Read { y }`, `,"operationName":"Read"`), bad: true},
		{name: "duplicate selected name", method: "POST", contentType: "application/json", body: envelope(t, `query Same { x } mutation Same { y }`, `,"operationName":"Same"`), bad: true},
		{name: "malformed unselected operation", method: "POST", contentType: "application/json", body: envelope(t, `query Read { x } mutation Write {`, `,"operationName":"Read"`), bad: true},
		{name: "fragment only", method: "POST", contentType: "application/graphql", body: `fragment F on Query { x }`, bad: true},
		{name: "empty document", method: "POST", contentType: "application/graphql", body: "# nothing", bad: true},
		{name: "invalid grammar", method: "POST", contentType: "application/graphql", body: `query Bad { f(a:01) }`, bad: true},
		{name: "adjacent number and name", method: "POST", contentType: "application/graphql", body: `{f(a:[1true])}`, bad: true},
		{name: "raw surrogate escape", method: "POST", contentType: "application/graphql", body: `{f(a:"\uD800")}`, bad: true},
		{name: "raw trailing surrogate escape", method: "POST", contentType: "application/graphql", body: `{f(a:"\uDC00")}`, bad: true},
		{name: "empty selection", method: "POST", contentType: "application/graphql", body: `query Bad {}`, bad: true},
		{name: "missing query", method: "POST", contentType: "application/json", body: `{"operationName":"Read"}`, bad: true},
		{name: "null query", method: "POST", contentType: "application/json", body: `{"query":null}`, bad: true},
		{name: "wrong query type", method: "POST", contentType: "application/json", body: `{"query":{}}`, bad: true},
		{name: "wrong variables type", method: "POST", contentType: "application/json", body: envelope(t, `{ x }`, `,"variables":"{}"`), bad: true},
		{name: "wrong extensions type", method: "POST", contentType: "application/json", body: envelope(t, `{ x }`, `,"extensions":[]`), bad: true},
		{name: "wrong persisted type", method: "POST", contentType: "application/json", body: `{"extensions":{"persistedQuery":1}}`, bad: true},
		{name: "malformed JSON", method: "POST", contentType: "application/json", body: `{"query":`, bad: true},
		{name: "trailing JSON", method: "POST", contentType: "application/json", body: `{"query":"{ x }"} {}`, bad: true},
		{name: "duplicate JSON member", method: "POST", contentType: "application/json", body: `{"query":"{ x }","query":"{ y }"}`, bad: true},
		{name: "duplicate variable", method: "POST", contentType: "application/json", body: envelope(t, `{ x }`, `,"variables":{"x":1,"x":2}`), bad: true},
		{name: "invalid JSON UTF8", method: "POST", contentType: "application/json", body: "{\"query\":\"{ f(a: \\\"\xff\\\") }\"}", bad: true},
		{name: "invalid raw UTF8", method: "POST", contentType: "application/graphql", body: "{ f(a: \"\xff\") }", bad: true},
		{name: "JSON surrogate", method: "POST", contentType: "application/json", body: `{"query":"{ x }","variables":{"x":"\ud800"}}`, bad: true},
		{name: "duplicate GET query", method: "GET", rawURL: "/graphql?query=%7Bx%7D&query=%7By%7D", bad: true},
		{name: "invalid GET encoding", method: "GET", rawURL: "/graphql?query=%FF", bad: true},
		{name: "invalid GET escape", method: "GET", rawURL: "/graphql?query=%xx", bad: true},
		{name: "invalid GET variables", method: "GET", rawURL: "/graphql?query=%7Bx%7D&variables=%5B%5D", bad: true},
		{name: "template not expanded", method: "POST", contentType: "application/graphql", body: `query {{operation}} { x }`, bad: true},
		{name: "PUT is ordinary", method: "PUT", contentType: "application/json", body: envelope(t, `{ x }`)},
		{name: "batch order", method: "POST", contentType: "application/json", body: `[` + envelope(t, multi, `,"operationName":"Write"`) + `,` + envelope(t, multi, `,"operationName":"Read"`) + `]`, want: []graphql.Operation{{Name: "Write", Type: "mutation"}, {Name: "Read", Type: "query"}}},
		{name: "batch omits persisted", method: "POST", contentType: "application/json", body: `[{"extensions":{"persistedQuery":{}}},{"query":"{ x }"}]`, want: []graphql.Operation{{Type: "query"}}},
		{name: "batch failure atomic", method: "POST", contentType: "application/json", body: `[{"query":"{ x }"},{"query":"query Broken {"}]`, bad: true},
		{name: "batch mixed", method: "POST", contentType: "application/json", body: `[{"query":"{ x }"},{}]`, bad: true},
		{name: "batch non-object", method: "POST", contentType: "application/json", body: `[{"query":"{ x }"},null]`, bad: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := graphql.RequestOperations(tt.method, tt.rawURL, tt.contentType, tt.body)
			if (err != nil) != tt.bad || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, %v; want %#v, error=%v", got, err, tt.want, tt.bad)
			}
		})
	}
}

func TestLimits(t *testing.T) {
	const sourceLimit = 1 << 20
	for _, tt := range []struct {
		name, query string
		bad         bool
	}{
		{"source boundary", "{x}#" + strings.Repeat("x", sourceLimit-4), false},
		{"source over", "{x}#" + strings.Repeat("x", sourceLimit-3), true},
		{"depth boundary", strings.Repeat("{f", 64) + strings.Repeat("}", 64), false},
		{"depth over", strings.Repeat("{f", 65) + strings.Repeat("}", 65), true},
		{"deep hostile", strings.Repeat("{f", 20000) + strings.Repeat("}", 20000), true},
		{"type depth over", "query Q($v:" + strings.Repeat("[", 65) + "Int" + strings.Repeat("]", 65) + "){x}", true},
		{"list depth over", "{f(a:" + strings.Repeat("[", 64) + "0" + strings.Repeat("]", 64) + ")}", true},
		{"object depth over", "{f(a:" + strings.Repeat("{x:", 64) + "0" + strings.Repeat("}", 64) + ")}", true},
		{"token boundary", "{" + strings.Repeat("f ", 16382) + "}", false},
		{"tokens over", "{" + strings.Repeat("f ", 16383) + "}", true},
		{"comment tokens over", "{x}\n" + strings.Repeat("# comment\n", 16384), true},
		{"strings not nesting", `{ f(a:"` + strings.Repeat("[{(", 100) + `") }`, false},
		{"comments not nesting", "# " + strings.Repeat("[{(", 100) + "\n{x}", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := graphql.RequestOperations("POST", "", "application/graphql", tt.query)
			if (err != nil) != tt.bad || (tt.bad && got != nil) {
				t.Fatalf("got %d operations, %v; want error=%v", len(got), err, tt.bad)
			}
			if tt.bad {
				out, err := graphql.Format(tt.query)
				if err == nil || out != "" {
					t.Fatalf("Format returned %d bytes, %v", len(out), err)
				}
			}
		})
	}
	for _, n := range []int{32, 33} {
		t.Run(fmt.Sprintf("batch %d", n), func(t *testing.T) {
			body := "[" + strings.TrimSuffix(strings.Repeat(`{"query":"{x}"},`, n), ",") + "]"
			got, err := graphql.RequestOperations("POST", "", "application/json", body)
			if n == 32 && (err != nil || len(got) != 32) || n == 33 && (err == nil || got != nil) {
				t.Fatalf("got %d operations, %v", len(got), err)
			}
		})
	}
	for _, body := range []string{
		envelope(t, `{x}`, `,"variables":{"v":`+strings.Repeat("[", 64)+"0"+strings.Repeat("]", 64)+`}`),
		envelope(t, `{x}`, `,"variables":{"v":[`+strings.Repeat("0,", 16384)+`0]}`),
		envelope(t, `{x}`, `,"variables":{"v":"`+strings.Repeat("x", sourceLimit)+`"}`),
		"[" + envelope(t, "{"+strings.Repeat("f ", 8191)+"}") + "," + envelope(t, "{"+strings.Repeat("f ", 8191)+"}") + "]",
	} {
		if got, err := graphql.RequestOperations("POST", "", "application/json", body); err == nil || got != nil {
			t.Errorf("JSON limits: got %d operations, %v", len(got), err)
		}
	}
	if _, err := graphql.RequestOperations("GET", "/?query="+strings.Repeat("x", sourceLimit), "", ""); err == nil {
		t.Error("oversized URL accepted")
	}
}

func TestFormat(t *testing.T) {
	queries := []string{
		`{ me { id name } }`,
		`query Read($n: Int = 12345678901234567890, $f: Float = -0.00E+12) @cached { alias: field(a: [$n, {x: 1e1000}]) @skip(if: false) } mutation Write { update }`,
		`fragment F on Query { field } query Read { ...F ... on Query @skip(if:false) { x } }`,
		"# keep operation\nquery Read {\n# keep field\nfield(text:\"mutation Bogus { x }\")\n}",
		`query Unicode { field(a: "你好 🦊 é", b: "\uD83E\uDD8A", c: "\u{1F98A}", d: "\u001b\u0007\u007f\u0085\"\\\n") }`,
		"{f(a:\"literal\ttab\", b:\"\\\\u{not an escape}\", c:\"\\\\uD800\")}",
		"query Block { f(a: \"\"\"\n    alpha\n      beta\n    中文 🦊\n  \"\"\") }",
		`query Block { f(a: """quote: \""" and backslash: \ end""") }`,
		"query Q($a:String=\"\"\"\n  hello\n  world\n\"\"\") { f(a:$a) }",
		`{ f(a: "{{literal}}") }`,
	}
	for _, query := range queries {
		t.Run(query[:min(len(query), 35)], func(t *testing.T) {
			out, err := graphql.Format(query)
			if err != nil {
				t.Fatal(err)
			}
			again, err := graphql.Format(out)
			if err != nil || out != again {
				t.Fatalf("not idempotent: %q, %v", again, err)
			}
			if strings.Contains(query, "# keep") && (!strings.Contains(out, "# keep operation") || !strings.Contains(out, "# keep field")) {
				t.Fatalf("lost supported comments: %s", out)
			}
		})
	}
	// Inspect the independently reparsed argument values, not just output spelling.
	query := "{ f(a: \"你好 🦊\", b: \"\"\"\n  alpha\n    beta\n  中文\n\"\"\") }"
	out, err := graphql.Format(query)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parser.ParseQuery(&ast.Source{Input: out})
	if err != nil {
		t.Fatal(err)
	}
	args := doc.Operations[0].SelectionSet[0].(*ast.Field).Arguments
	if args[0].Value.Raw != "你好 🦊" || args[1].Value.Raw != "alpha\n  beta\n中文" {
		t.Fatalf("changed string values: %#v, %#v", args[0].Value.Raw, args[1].Value.Raw)
	}
	for _, query := range []string{
		"", "# only comment", "{", "{f(a:\"\xff\")}",
		`{f(a:"\uD800")}`, `{f(a:"\uDC00\u{1F98A}")}`, `{f(a:[1true])}`,
		`query {{name}} { x }`, `query Q($v: Int) { f(a: {{value}}) }`,
		`query Q($v: Int @custom) { f(a:$v) }`, // Upstream formatter drops this directive.
	} {
		if out, err := graphql.Format(query); err == nil || out != "" {
			t.Errorf("Format(%q) = %q, %v; want empty output and error", query, out, err)
		}
	}
}

func TestFormatOutputLimits(t *testing.T) {
	// A short block-string token can expand sixfold when quoted. The cap must
	// hold even though gqlparser's formatter ignores io.Writer errors.
	query := "{f(a:\"\"\"" + strings.Repeat("\u0085", 400000) + "\"\"\")}"
	if out, err := graphql.Format(query); err == nil || out != "" {
		t.Fatalf("output cap: returned %d bytes, %v", len(out), err)
	}
	// Indentation can exceed the source budget without exceeding 2 MiB. Reject
	// it too, rather than returning a result Format cannot accept a second time.
	query = strings.Repeat("{f", 63) + "{" + strings.Repeat("x ", 10000) + "}" + strings.Repeat("}", 63)
	if out, err := graphql.Format(query); err == nil || out != "" {
		t.Fatalf("idempotence source cap: returned %d bytes, %v", len(out), err)
	}
}

func FuzzFormat(f *testing.F) {
	for _, seed := range []string{
		`{x}`, `query Q { f(a: "你好") }`, "# c\n{x}",
		`{f(a:"""a\"""b""")}`, `query Q($v:Int @d){x}`, `query {{name}} { x }`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		out, err := graphql.Format(source)
		if err != nil {
			if out != "" {
				t.Fatal("partial format on error")
			}
			return
		}
		again, err := graphql.Format(out)
		if err != nil || again != out || len(out) > 2<<20 {
			t.Fatalf("unstable format: %v", err)
		}
	})
}

func FuzzRequestOperations(f *testing.F) {
	for _, seed := range []string{`{"query":"{x}"}`, `[{"query":"query Q{x}"}]`, `{"extensions":{"persistedQuery":{}}}`, `{"query":`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		for _, mediaType := range []string{"application/json", "application/graphql"} {
			ops, err := graphql.RequestOperations("POST", "", mediaType, body)
			if err != nil && ops != nil {
				t.Fatal("partial operations on error")
			}
			for _, op := range ops {
				if op.Type != "query" && op.Type != "mutation" && op.Type != "subscription" {
					t.Fatalf("invalid operation type %q", op.Type)
				}
			}
		}
	})
}

func TestSelectOperationValidatesSentNames(t *testing.T) {
	if _, err := graphql.SelectOperation("query A { a } query B { b }", "not a name!"); err == nil {
		t.Fatal("an invalid multi-operation name was accepted")
	}
	if _, err := graphql.SelectOperation("{ user(id: 1) { name }", "not a name!"); err == nil {
		t.Fatal("an invalid name for a malformed document was accepted")
	}
	if name, err := graphql.SelectOperation("query A { a }", "not a name!"); err != nil || name != "A" {
		t.Fatalf("a single operation did not override its stale name: %q, %v", name, err)
	}
}

func TestOperationsLargeDocument(t *testing.T) {
	var b strings.Builder
	for _, name := range []string{"A", "B"} {
		b.WriteString("query " + name + " {")
		for i := range 3000 {
			fmt.Fprintf(&b, " f%d(a: 1)", i)
		}
		b.WriteString(" }")
	}
	source := b.String()
	if len(source) > 1<<20 {
		t.Fatalf("fixture is %d bytes", len(source))
	}
	ops, err := graphql.Operations(source)
	if err != nil || len(ops) != 2 || ops[0].Name != "A" || ops[1].Name != "B" {
		t.Fatalf("Operations = %+v, %v", ops, err)
	}
	if name, err := graphql.SelectOperation(source, "B"); err != nil || name != "B" {
		t.Fatalf("SelectOperation = %q, %v", name, err)
	}
}
