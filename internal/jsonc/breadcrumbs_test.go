package jsonc

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

func labels(path BreadcrumbPath) []string {
	out := make([]string, 0, len(path.Segments))
	for _, s := range path.Segments {
		out = append(out, s.Label)
	}
	return out
}

func offsetOf(src, needle string) int {
	pos := strings.Index(src, needle)
	if pos < 0 {
		panic("test fixture missing " + needle)
	}
	return pos
}

func TestBreadcrumbsAtPathsAndJSONPath(t *testing.T) {
	const src = `{"data":{"items":[{"sku":"A"},{"sku":"B"},{"sku":"C"},{"id":3}]},"ok":true}`
	cases := []struct {
		name, at, wantJSONPath string
		wantLabels             []string
	}{
		{"value in nested array", `"id":3`, `$.data.items[3].id`,
			[]string{"$", "data", "items", "[3]", "id"}},
		{"key of nested value", `"id"`, `$.data.items[3].id`,
			[]string{"$", "data", "items", "[3]", "id"}},
		{"array element", `"sku":"C"`, `$.data.items[2].sku`,
			[]string{"$", "data", "items", "[2]", "sku"}},
		{"sibling key", `"ok":true`, `$.ok`, []string{"$", "ok"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pos := offsetOf(src, tc.at) + len(tc.at) - 1
			got := BreadcrumbsAt(src, pos)
			if !slices.Equal(labels(got), tc.wantLabels) {
				t.Fatalf("labels = %v, want %v", labels(got), tc.wantLabels)
			}
			if got.JSONPath != tc.wantJSONPath {
				t.Fatalf("JSONPath = %q, want %q", got.JSONPath, tc.wantJSONPath)
			}
		})
	}
}

func TestBreadcrumbsCopyKeyAndValue(t *testing.T) {
	const src = `{"data":{"items":[{"sku":"A"},{"id":3}]},"ok":true}`
	cases := []struct {
		name, at, wantKey, wantValue string
	}{
		{"value in nested array", `"id":3`, "id", "3"},
		{"key of nested value", `"id"`, "id", "3"},
		{"array element", `"sku":"A"`, "sku", `"A"`},
		{"root member", `"ok":true`, "ok", "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pos := offsetOf(src, tc.at) + len(tc.at) - 1
			got := BreadcrumbsAt(src, pos)
			if got.Key != tc.wantKey || got.Value != tc.wantValue {
				t.Fatalf("key = %q, value = %q; want %q, %q", got.Key, got.Value, tc.wantKey, tc.wantValue)
			}
		})
	}
}

func TestBreadcrumbsInReusesTheParsedTree(t *testing.T) {
	const src = `{"data":{"items":[{"id":3}]},"ok":true}`
	root, errs := ParseTree(src)
	if root == nil || len(errs) != 0 {
		t.Fatalf("fixture did not parse: %v", errs)
	}
	for _, offset := range []int{0, 5, 19, offsetOf(src, "3"), len(src)} {
		want, got := BreadcrumbsAt(src, offset), BreadcrumbsIn(root, src, offset)
		if want.JSONPath != got.JSONPath || len(want.Segments) != len(got.Segments) {
			t.Fatalf("at %d: At = %+v, In = %+v", offset, want, got)
		}
		for i := range want.Segments {
			if want.Segments[i].Start != got.Segments[i].Start || want.Segments[i].End != got.Segments[i].End {
				t.Fatalf("at %d, segment %d: At = %+v, In = %+v", offset, i, want.Segments[i], got.Segments[i])
			}
		}
	}
	// Offsets outside the text are clamped, with or without a supplied tree.
	if got := BreadcrumbsIn(root, src, len(src)+40); got.JSONPath != "$" {
		t.Fatalf("past the end: %q", got.JSONPath)
	}
}

func TestBreadcrumbsAtQuotedKeys(t *testing.T) {
	const src = `{"a.b":{"q\"x":7,"it's":8,"back\\slash":9,"ok_key":10,"2bad":11}}`
	cases := []struct {
		at   string
		want string
	}{
		{`7`, `$['a.b']['q"x']`},
		{`8`, `$['a.b']['it\'s']`},
		{`9`, `$['a.b']['back\\slash']`},
		{`10`, `$['a.b'].ok_key`},
		{`11`, `$['a.b']['2bad']`},
	}
	for _, tc := range cases {
		got := BreadcrumbsAt(src, offsetOf(src, tc.at)).JSONPath
		if got != tc.want {
			t.Errorf("at %s: JSONPath = %q, want %q", tc.at, got, tc.want)
		}
	}
}

func TestBreadcrumbsAtKeyAndValueSpan(t *testing.T) {
	const src = `{"alpha":1}`
	key := offsetOf(src, `"alpha"`)
	value := key + len(`"alpha":`)
	for _, pos := range []int{key + 2, value} {
		path := BreadcrumbsAt(src, pos)
		if len(path.Segments) != 2 || path.Segments[1].Label != "alpha" {
			t.Fatalf("at %d: labels = %v", pos, labels(path))
		}
		seg := path.Segments[1]
		if seg.Start != key || seg.End != len(src)-1 {
			t.Fatalf("at %d: span = %d:%d, want %d:%d", pos, seg.Start, seg.End, key, len(src)-1)
		}
	}
}

func TestBreadcrumbsAtBetweenTokens(t *testing.T) {
	const src = "{\n  \"a\": 1,\n  \"b\": 2\n}"
	pos := strings.Index(src, "\n  \"b\"") // the whitespace after the comma
	path := BreadcrumbsAt(src, pos)
	if !slices.Equal(labels(path), []string{"$"}) {
		t.Fatalf("labels = %v, want only the root", labels(path))
	}
	if len(path.Segments[0].Siblings) != 0 || path.JSONPath != "$" {
		t.Fatalf("between tokens: %+v", path)
	}
}

func TestBreadcrumbsAtInvalidTail(t *testing.T) {
	const src = `{"keep":{"leaf":3},"broken":[1,,oops`
	path := BreadcrumbsAt(src, offsetOf(src, "3"))
	if !slices.Equal(labels(path), []string{"$", "keep", "leaf"}) || path.JSONPath != "$.keep.leaf" {
		t.Fatalf("labels = %v, JSONPath = %q", labels(path), path.JSONPath)
	}
	// Even a caret past the bad bytes must return a path, not a panic.
	if got := BreadcrumbsAt(src, len(src)+20); len(got.Segments) == 0 {
		t.Fatal("no path past the end of a broken body")
	}
}

func TestBreadcrumbsSiblingLists(t *testing.T) {
	src := `{"a":[0,1,2,3],"b":{"x":1,"y":2}}`
	path := BreadcrumbsAt(src, offsetOf(src, "3"))
	array := path.Segments[len(path.Segments)-1]
	var siblingLabels []string
	for _, s := range array.Siblings {
		siblingLabels = append(siblingLabels, s.Label)
	}
	if !slices.Equal(siblingLabels, []string{"[0]", "[1]", "[2]", "[3]"}) {
		t.Fatalf("array siblings = %v", siblingLabels)
	}
	path = BreadcrumbsAt(src, offsetOf(src, `"x":1`))
	obj := path.Segments[len(path.Segments)-1]
	siblingLabels = nil
	for _, s := range obj.Siblings {
		siblingLabels = append(siblingLabels, s.Label)
	}
	if !slices.Equal(siblingLabels, []string{"x", "y"}) {
		t.Fatalf("object siblings = %v", siblingLabels)
	}
	if obj.Siblings[0].Start != offsetOf(src, `"x"`) || obj.Siblings[1].End != offsetOf(src, `"y"`)+len(`"y":2`) {
		t.Fatalf("sibling spans = %+v", obj.Siblings)
	}
}

func TestBreadcrumbsSiblingLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("{")
	for i := range 200 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"k` + strconv.Itoa(i) + `":1`)
	}
	b.WriteString("}")
	path := BreadcrumbsAt(b.String(), 3)
	last := path.Segments[len(path.Segments)-1]
	if len(last.Siblings) != BreadcrumbSiblingLimit || !last.More {
		t.Fatalf("siblings = %d, more = %v", len(last.Siblings), last.More)
	}
}

func TestBreadcrumbsAtLargeBrokenBody(t *testing.T) {
	body := `{"list":[` + strings.Repeat(`{"n":1},`, 20_000) + `{"n":2}`
	path := BreadcrumbsAt(body, 5)
	if !slices.Equal(labels(path), []string{"$", "list"}) {
		t.Fatalf("labels = %v", labels(path))
	}
	if len(body) < 100_000 {
		t.Fatal("fixture stopped being large")
	}
}

func TestBreadcrumbsAtNonJSON(t *testing.T) {
	if got := BreadcrumbsAt("plain text", 2); len(got.Segments) != 0 || got.JSONPath != "" {
		t.Fatalf("plain text got %+v", got)
	}
	if got := BreadcrumbsAt(`42`, 1); len(got.Segments) != 0 {
		t.Fatalf("scalar got %+v", got)
	}
}
