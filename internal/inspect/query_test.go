package inspect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestQuery(t *testing.T) {
	const records = `{"rows":[{"n":2,"name":"first"},{"n":19,"name":"middle"},{"n":7,"name":"last"}]}`
	tests := []struct {
		name, dialect, expression, input, want string
	}{
		{"path asymmetric", "JSONPath", `$.rows[?(@.n > 7)].name`, records, "[\n  \"middle\"\n]"},
		{"jq asymmetric", "jq", `.rows[] | select(.n > 7) | .name`, records, `"middle"`},
		{"path punctuation", "JSONPath", `$['a.b']['x-y']['space key'][0]['quote\'key']`, `{"a.b":{"x-y":{"space key":[{"quote'key":42}]}}}`, "[\n  42\n]"},
		{"jq punctuation", "jq", `.["a.b"]["x-y"]["space key"][0]["quote'key"]`, `{"a.b":{"x-y":{"space key":[{"quote'key":42}]}}}`, "42"},
		{"path precision filter", "JSONPath", `$[?(@.n > 9007199254740992)].n`, `[{"n":9007199254740992},{"n":9007199254740993}]`, "[\n  9007199254740993\n]"},
		{"path precision equality", "JSONPath", `$[?(@.n == 9007199254740993)].n`, `[{"n":9007199254740992},{"n":9007199254740993}]`, "[\n  9007199254740993\n]"},
		{"path int64 maximum", "JSONPath", `$[?(@ > 9223372036854775806)]`, `[9223372036854775806,9223372036854775807]`, "[\n  9223372036854775807\n]"},
		{"path int64 minimum", "JSONPath", `$[?(@ == -9223372036854775808)]`, `[-9223372036854775807,-9223372036854775808]`, "[\n  -9223372036854775808\n]"},
		{"path big selection", "JSONPath", `$.n`, `{"n":1234567890123456789012345678901234567890}`, "[\n  1234567890123456789012345678901234567890\n]"},
		{"jq precision filter", "jq", `.[] | select(.n > 9007199254740992) | .n`, `[{"n":9007199254740992},{"n":9007199254740993}]`, "9007199254740993"},
		{"jq big filter", "jq", `.[] | select(. > 1234567890123456789012345678901234567890)`, `[1234567890123456789012345678901234567890,1234567890123456789012345678901234567891]`, "1234567890123456789012345678901234567891"},
		{"jq arithmetic", "jq", `.n + 1`, `{"n":1234567890123456789012345678901234567890}`, "1234567890123456789012345678901234567891"},
		{"jq negative precision", "jq", `.[] | select(. < -9007199254740992)`, `[-9007199254740992,-9007199254740993]`, "-9007199254740993"},
		{"jq scalar", "jq", `.`, `true`, "true"},
		{"jq null", "jq", `.`, `null`, "null"},
		{"jq missing", "jq", `.missing`, `{}`, "null"},
		{"jq empty", "jq", `empty`, `{}`, ""},
		{"jq empty array", "jq", `.`, `[]`, "[]"},
		{"jq stream", "jq", `.[]`, `[1,null,"a",{}]`, "1\nnull\n\"a\"\n{}"},
		{"jq pretty", "jq", `.`, `{"b":2,"a":[1]}`, "{\n  \"a\": [\n    1\n  ],\n  \"b\": 2\n}"},
		{"path scalar", "JSONPath", `$`, `true`, "[\n  true\n]"},
		{"path null", "JSONPath", `$`, `null`, "[\n  null\n]"},
		{"path missing", "JSONPath", `$.missing`, `{}`, "[]"},
		{"path union", "JSONPath", `$[2,0]`, `[1,2,3]`, "[\n  3,\n  1\n]"},
		{"path slice", "JSONPath", `$[0:4:2]`, `[1,2,3,4,5]`, "[\n  1,\n  3\n]"},
		{"path descent", "JSONPath", `$..n`, `{"a":{"n":1},"b":{"n":2}}`, "[\n  1,\n  2\n]"},
		{"path logical filter", "JSONPath", `$.rows[?(@.n > 3 && @.n < 18)].name`, records, "[\n  \"last\"\n]"},
		{"path existence", "JSONPath", `$[?(@.x)].x`, `[{"x":false},{"y":true},{"x":null}]`, "[\n  false,\n  null\n]"},
		{"path filter nested punctuation", "JSONPath", `$[?(@['a.b']['x-y'] > 4)]['a.b']['x-y']`, `[{"a.b":{"x-y":2}},{"a.b":{"x-y":19}}]`, "[\n  19\n]"},
		{"jq map", "jq", `map(select(.n > 7) | {label: .name})`, `[{"n":2,"name":"a"},{"n":19,"name":"b"}]`, "[\n  {\n    \"label\": \"b\"\n  }\n]"},
		{"jq variable", "jq", `.n as $n | [$n, $n + 1]`, `{"n":4}`, "[\n  4,\n  5\n]"},
		{"jq recursion", "jq", `.. | numbers`, `{"n":[1,2]}`, "1\n2"},
		{"jq bounded infinite stream", "jq", `limit(3; repeat(1))`, `null`, "1\n1\n1"},
		{"jq literals are not imports", "jq", `"import env input _inspect_guard"`, `null`, `"import env input _inspect_guard"`},
		{"jq number boundary", "jq", `.`, strings.Repeat("9", queryNumberLimit), strings.Repeat("9", queryNumberLimit)},
		{"jq large read index", "jq", `.[1000000000000]`, `[]`, "null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Query(t.Context(), tt.dialect, tt.expression, tt.input)
			if err != nil || got != tt.want {
				t.Fatalf("Query() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestQueryRejects(t *testing.T) {
	tests := []struct{ dialect, expression, input string }{
		{"JQ", ".", "null"},
		{"jq", "", "null"},
		{"JSONPath", " ", "null"},
		{"jq", ".[", "null"},
		{"JSONPath", "$[", "null"},
		{"JSONPath", "@.x", "{}"},
		{"jq", ".", ""},
		{"jq", ".", "{]"},
		{"JSONPath", "$", "null null"},
		{"jq", ".", `{"a":1,"a":2}`},
		{"JSONPath", "$", "\"\xff\""},
		{"jq", `import "anything" as x; x::f`, "null"},
		{"jq", `include "anything"; .`, "null"},
		{"jq", `module {}; .`, "null"},
		{"jq", "env", "null"},
		{"jq", "$ENV", "null"},
		{"jq", "input", "null"},
		{"jq", "inputs", "null"},
		{"jq", `system("echo unsafe")`, "null"},
		{"jq", `debug`, "null"},
		{"jq", `_inspect_guard`, "null"},
		{"jq", `.a[1000000000000] = 1`, "{}"},
		{"jq", `setpath([1000000000000]; 1)`, "{}"},
		{"jq", `"x" * 1000000000`, "null"},
		{"jq", `. - .`, "[]"},
		{"jq", `lpad(1000000000)`, `"x"`},
		{"jq", `def f: f; f`, "null"},
		{"jq", `reduce range(1000000000) as $i ([]; . + [$i])`, "null"},
		{"JSONPath", `$..*..missing`, "{}"},
		{"JSONPath", `$[0:3:9223372036854775807]`, "[1,2,3]"},
		{"JSONPath", `$[?(@..x == 1)]`, "[]"},
		{"JSONPath", `$[?(@[*] == 1)]`, "[]"},
		{"JSONPath", `$[?(@.n > $.limit)]`, "[]"},
		{"JSONPath", `$[?(@.n + 1 > 3)]`, "[]"},
		{"JSONPath", `$[?(length(@.n) == 3)]`, "[]"},
		{"JSONPath", `$[?true]`, "[]"},
		{"JSONPath", `$[?(@ == 1)]`, "[123456789012345678901234567890]"},
		{"JSONPath", `$[?(@ > 9007199254740992.0)]`, "[9007199254740993]"},
		{"JSONPath", `$[?(@ == 9007199254740993)]`, "[9007199254740992.0]"},
		{"JSONPath", `$[?(@ > 0)]`, "[9007199254740993,0.0]"},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			got, err := Query(t.Context(), tt.dialect, tt.expression, tt.input)
			if err == nil || got != "" {
				t.Fatalf("Query(%q, %q) = %q, %v; want empty output and error", tt.dialect, tt.expression, got, err)
			}
		})
	}
}

func TestQueryLimits(t *testing.T) {
	tests := []struct{ name, dialect, expression, input, limit string }{
		{"input", "jq", ".", strings.Repeat(" ", queryInputLimit+1), "input"},
		{"expression", "jq", strings.Repeat(" ", queryExpressionLimit+1), "null", "expression"},
		{"input depth", "jq", ".", strings.Repeat("[", queryDepthLimit+1) + "0" + strings.Repeat("]", queryDepthLimit+1), "depth"},
		{"path result depth", "JSONPath", "$", strings.Repeat("[", queryDepthLimit) + "0" + strings.Repeat("]", queryDepthLimit), "depth"},
		{"input nodes", "JSONPath", "$", "[" + strings.Repeat("0,", queryNodeLimit) + "0]", "nodes"},
		{"input number", "jq", ".", strings.Repeat("9", queryNumberLimit+1), "number"},
		{"expression number", "jq", strings.Repeat("9", queryNumberLimit+1), "null", "number"},
		{"jq results", "jq", "range(4097)", "null", "results"},
		{"path results", "JSONPath", "$[*]", "[" + strings.Repeat("0,", queryResultLimit) + "0]", "results"},
		{"output", "jq", ".,.,.", `"` + strings.Repeat("x", 800000) + `"`, "output"},
		{"path output", "JSONPath", "$[0,0,0]", `["` + strings.Repeat("x", 800000) + `"]`, "output"},
		{"work collecting infinite", "jq", "[repeat(0)]", "null", "work"},
		{"work empty stream", "jq", "repeat(0) | empty", "null", "work"},
		{"work try cannot swallow", "jq", "try [repeat(0)] catch null", "null", "work"},
		{"declared shape", "jq", "[range(1000000000000)]", "null", "limit"},
		{"result depth", "jq", "recurse([.]) | empty", "null", "depth"},
		{"branching shape", "jq", "recurse([., .]) | empty", "null", "limit"},
		{"path branching no matches", "JSONPath", "$" + strings.Repeat("[0,0]", 18) + ".absent", strings.Repeat("[", 18) + "0" + strings.Repeat("]", 18), "work"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Query(t.Context(), tt.dialect, tt.expression, tt.input)
			if got != "" || err == nil || !strings.Contains(err.Error(), tt.limit) {
				t.Fatalf("Query() = (%d output bytes), %v; want %s limit", len(got), err, tt.limit)
			}
		})
	}
}

func TestQueryCancellation(t *testing.T) {
	for _, dialect := range []string{"JSONPath", "jq"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, err := Query(ctx, dialect, ".", "null")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled context: %v", err)
			}
		})
	}
	// No output and no user AST guard yields. gojq's VM must still stop.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Query(ctx, "jq", "last(repeat(empty))", "null")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("running cancellation: %v after %v", err, time.Since(start))
	}
	_, err = Query(t.Context(), "jq", "last(repeat(empty))", "null")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("default deadline: %v", err)
	}
}

func TestQueryBoundaries(t *testing.T) {
	for _, dialect := range []string{"JSONPath", "jq"} {
		expression := ".[]"
		if dialect == "JSONPath" {
			expression = "$[*]"
		}
		input := "[" + strings.Repeat("0,", queryResultLimit-1) + "0]"
		if _, err := Query(t.Context(), dialect, expression, input); err != nil {
			t.Errorf("%s at result limit: %v", dialect, err)
		}
	}
}

func TestQueryConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for _, dialect := range []string{"jq", "JSONPath"} {
				expression := `.[] | select(.n > 7) | .n`
				want := "19"
				if dialect == "JSONPath" {
					expression = `$[?(@.n > 7)].n`
					want = "[\n  19\n]"
				}
				got, err := Query(t.Context(), dialect, expression, `[{"n":2},{"n":19}]`)
				if err != nil || got != want {
					t.Errorf("Query(%s) = %q, %v", dialect, got, err)
				}
			}
		})
	}
	wg.Wait()
}

func FuzzQuery(f *testing.F) {
	for _, seed := range []struct{ expression, input string }{
		{`$[?(@.x > 7)].x`, `[{"x":2},{"x":19}]`},
		{`$[?true]`, `[]`},
		{`$[0:3:-9223372036854775808]`, `[1,2,3]`},
		{`.[] | select(.x > 7) | .x`, `[{"x":2},{"x":19}]`},
		{`recurse([., .]) | empty`, `null`},
		{`last(repeat(empty))`, `null`},
	} {
		f.Add(seed.expression, seed.input)
	}
	f.Fuzz(func(t *testing.T, expression, input string) {
		for _, dialect := range []string{"JSONPath", "jq"} {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			output, err := Query(ctx, dialect, expression, input)
			cancel()
			if len(output) > queryOutputLimit || err != nil && output != "" {
				t.Fatalf("Query violated output contract: %d bytes, %v", len(output), err)
			}
		}
	})
}
