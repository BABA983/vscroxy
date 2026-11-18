// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See [VSCODE_LICENSE.txt] in this directory.
//
// The test names and expectations mirror the suite in
// src/vs/base/test/common/jsonFormatter.test.ts at microsoft/vscode
// 972b66fdc541e10689adfd90ade9549dea1b18bc.

package jsonc

import (
	"strconv"
	"strings"
	"testing"
)

func join(lines ...string) string { return strings.Join(lines, "\n") }

func testFormat(t *testing.T, content, expected string, insertSpaces ...bool) {
	t.Helper()
	spaces := true
	if len(insertSpaces) > 0 {
		spaces = insertSpaces[0]
	}
	var rg *Range
	rangeStart := strings.Index(content, "|")
	rangeEnd := strings.LastIndex(content, "|")
	if rangeStart != -1 && rangeEnd != -1 {
		content = content[:rangeStart] + content[rangeStart+1:rangeEnd] + content[rangeEnd+1:]
		rg = &Range{Offset: rangeStart, Length: rangeEnd - rangeStart}
	}
	edits := Format(content, rg, FormattingOptions{TabSize: 2, InsertSpaces: spaces, EOL: "\n"})

	lastEditOffset := len(content)
	for i := len(edits) - 1; i >= 0; i-- {
		edit := edits[i]
		if edit.Offset < 0 || edit.Length < 0 || edit.Offset+edit.Length > len(content) {
			t.Fatalf("invalid edit %#v for %q", edit, content)
		}
		if lastEditOffset < edit.Offset+edit.Length {
			t.Fatalf("overlapping or unordered edits: %#v", edits)
		}
		lastEditOffset = edit.Offset
	}
	actual, err := ApplyEdits(content, edits)
	if err != nil {
		t.Fatalf("ApplyEdits: %v", err)
	}
	if actual != expected {
		t.Fatalf("format %q:\n got %q\nwant %q", content, actual, expected)
	}
}

func TestVSCodeFormatObjectSingleProperty(t *testing.T) {
	testFormat(t, `{"x" : 1}`, join("{", `  "x": 1`, "}"))
}

func TestVSCodeFormatObjectMultipleProperties(t *testing.T) {
	testFormat(t, `{"x" : 1,  "y" : "foo", "z"  : true}`,
		join("{", `  "x": 1,`, `  "y": "foo",`, `  "z": true`, "}"))
}

func TestVSCodeFormatObjectNoProperties(t *testing.T) {
	testFormat(t, `{"x" : {    },  "y" : {}}`, join("{", `  "x": {},`, `  "y": {}`, "}"))
}

func TestVSCodeFormatObjectNesting(t *testing.T) {
	testFormat(t, `{"x" : {  "y" : { "z"  : { }}, "a": true}}`, join(
		"{",
		`  "x": {`,
		`    "y": {`,
		`      "z": {}`,
		"    },",
		`    "a": true`,
		"  }",
		"}",
	))
}

func TestVSCodeFormatArraySingleItems(t *testing.T) {
	testFormat(t, `["[]"]`, join("[", `  "[]"`, "]"))
}

func TestVSCodeFormatArrayMultipleItems(t *testing.T) {
	testFormat(t, "[true,null,1.2]", join("[", "  true,", "  null,", "  1.2", "]"))
}

func TestVSCodeFormatArrayNoItems(t *testing.T) {
	testFormat(t, "[      ]", "[]")
}

func TestVSCodeFormatArrayNesting(t *testing.T) {
	testFormat(t, `[ [], [ [ {} ], "a" ]  ]`, join(
		"[",
		"  [],",
		"  [",
		"    [",
		"      {}",
		"    ],",
		`    "a"`,
		"  ]",
		"]",
	))
}

func TestVSCodeFormatSyntaxErrors(t *testing.T) {
	testFormat(t, "[ null 1.2 ]", join("[", "  null 1.2", "]"))
}

func TestVSCodeFormatEmptyLines(t *testing.T) {
	testFormat(t, join("{", `"a": true,`, "", `"b": true`, "}"),
		join("{", "\t\"a\": true,", "\t\"b\": true", "}"), false)
}

func TestVSCodeFormatSingleLineComment(t *testing.T) {
	testFormat(t, join("[ ", "//comment", `"foo", "bar"`, "] "),
		join("[", "  //comment", `  "foo",`, `  "bar"`, "]"))
}

func TestVSCodeFormatBlockLineComment(t *testing.T) {
	testFormat(t, join("[{", "        /*comment*/     ", `"foo" : true`, "}] "),
		join("[", "  {", "    /*comment*/", `    "foo": true`, "  }", "]"))
}

func TestVSCodeFormatSingleLineCommentOnSameLine(t *testing.T) {
	testFormat(t, join(" {  ", `        "a": {}// comment    `, " } "),
		join("{", `  "a": {} // comment    `, "}"))
}

func TestVSCodeFormatSingleLineCommentOnSameLine2(t *testing.T) {
	testFormat(t, join("{ //comment", "}"), join("{ //comment", "}"))
}

func TestVSCodeFormatBlockCommentOnSameLine(t *testing.T) {
	testFormat(t, join(`{      "a": {}, /*comment*/    `,
		`        /*comment*/ "b": {},    `, `        "c": {/*comment*/}    } `),
		join("{", `  "a": {}, /*comment*/`, `  /*comment*/ "b": {},`, `  "c": { /*comment*/}`, "}"))
}

func TestVSCodeFormatBlockCommentOnSameLineAdvanced(t *testing.T) {
	testFormat(t, join(` {       "d": [`, "             null", "        ] /*comment*/",
		`        ,"e": /*comment*/ [null] }`),
		join("{", `  "d": [`, "    null", "  ] /*comment*/,", `  "e": /*comment*/ [`, "    null", "  ]", "}"))
}

func TestVSCodeFormatMultipleBlockCommentsOnSameLine(t *testing.T) {
	testFormat(t, join(`{      "a": {} /*comment*/, /*comment*/   `,
		`        /*comment*/ "b": {}  /*comment*/  } `),
		join("{", `  "a": {} /*comment*/, /*comment*/`, `  /*comment*/ "b": {} /*comment*/`, "}"))
}

func TestVSCodeFormatMultipleMixedCommentsOnSameLine(t *testing.T) {
	testFormat(t, join("[ /*comment*/  /*comment*/   // comment ", "]"),
		join("[ /*comment*/ /*comment*/ // comment ", "]"))
}

func TestVSCodeFormatRange(t *testing.T) {
	testFormat(t, join(`{ "a": {},`, `|"b": [null, null]|`, "} "),
		join(`{ "a": {},`, `"b": [`, "  null,", "  null", "]", "} "))
}

func TestVSCodeFormatRangeWithExistingIndent(t *testing.T) {
	testFormat(t, join(`{ "a": {},`, `   |"b": [null],`, `"c": {}`, "}|"),
		join(`{ "a": {},`, `   "b": [`, "    null", "  ],", `  "c": {}`, "}"))
}

func TestVSCodeFormatRangeWithExistingIndentTabs(t *testing.T) {
	testFormat(t, join(`{ "a": {},`, `|  "b": [null],   `, `"c": {}`, `} |    `),
		join(`{ "a": {},`, "\t\"b\": [", "\t\tnull", "\t],", "\t\"c\": {}", "}"), false)
}

func TestVSCodeFormatBlockCommentNonLineBreakingSymbols(t *testing.T) {
	testFormat(t, join(`{ "a": [ 1`, "/* comment */", ", 2", "/* comment */", "]",
		"/* comment */", ",", ` "b": true`, "/* comment */", "}"),
		join("{", `  "a": [`, "    1", "    /* comment */", "    ,", "    2",
			"    /* comment */", "  ]", "  /* comment */", "  ,", `  "b": true`,
			"  /* comment */", "}"))
}

func TestVSCodeFormatLineCommentAfterNonLineBreakingSymbols(t *testing.T) {
	testFormat(t, join(`{ "a":`, "// comment", "null,", ` "b"`, "// comment", ": null",
		"// comment", "}"),
		join("{", `  "a":`, "  // comment", "  null,", `  "b"`, "  // comment",
			"  : null", "  // comment", "}"))
}

func TestVSCodeFormatToFormattedString(t *testing.T) {
	value := struct {
		A struct {
			B float64  `json:"b"`
			D []string `json:"d"`
		} `json:"a"`
	}{}
	value.A.B = 1
	value.A.D = []string{"hello"}
	expected := func(tab string) string {
		return join("{",
			tab+`"a": {`,
			tab+tab+`"b": 1,`,
			tab+tab+`"d": [`,
			tab+tab+tab+`"hello"`,
			tab+tab+"]",
			tab+"}",
			"}") // join uses \n; replace separately for CRLF
	}
	for _, tc := range []struct {
		name    string
		options FormattingOptions
		tab     string
		eol     string
	}{
		{"spaces", FormattingOptions{InsertSpaces: true, TabSize: 2, EOL: "\n"}, "  ", "\n"},
		{"crlf", FormattingOptions{InsertSpaces: true, TabSize: 2, EOL: "\r\n"}, "  ", "\r\n"},
		{"tabs", FormattingOptions{InsertSpaces: false, EOL: "\r\n"}, "\t", "\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := ToFormattedString(value, tc.options)
			if err != nil {
				t.Fatalf("ToFormattedString: %v", err)
			}
			want := strings.ReplaceAll(expected(tc.tab), "\n", tc.eol)
			if actual != want {
				t.Fatalf("got %q, want %q", actual, want)
			}
		})
	}
}

func TestFormatTruncatedInput(t *testing.T) {
	testFormat(t, `{"a":1,"b":[1,2`, join("{", `  "a": 1,`, `  "b": [`, "    1,", "    2"))
	testFormat(t, `{"a":12`, join("{", `  "a": 12`))
	testFormat(t, "{\"a\":\"你", join("{", `  "a":"你`))
}

func TestFormatOptionsAndEOL(t *testing.T) {
	if got := GetEOL(FormattingOptions{EOL: "\r"}, "first\nsecond"); got != "\n" {
		t.Fatalf("GetEOL: got %q", got)
	}
	if got := GetEOL(FormattingOptions{EOL: "\r"}, "no line break"); got != "\r" {
		t.Fatalf("GetEOL fallback: got %q", got)
	}
	text := "{\r\n\"a\" : 1\r\n}"
	edits := Format(text, nil, FormattingOptions{TabSize: 2, InsertSpaces: true})
	got, err := ApplyEdits(text, edits)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\r\n  \"a\": 1\r\n}"; got != want {
		t.Fatalf("CRLF formatting: got %q, want %q", got, want)
	}
}

func TestApplyEdits(t *testing.T) {
	edits := []Edit{
		{Offset: 2, Length: 1, Content: " b"},
		{Offset: 0, Length: 1, Content: "B"},
	}
	got, err := ApplyEdits("a,c", edits)
	if err != nil || got != "B, b" {
		t.Fatalf("got %q, %v", got, err)
	}
	if !reflectEditsUnchanged(edits) {
		t.Fatal("ApplyEdits changed the input slice")
	}
	if _, err := ApplyEdits("abc", []Edit{{Offset: 0, Length: 2, Content: "x"}, {Offset: 1, Length: 1, Content: "y"}}); err == nil {
		t.Fatal("expected an overlapping edit error")
	}
}

func reflectEditsUnchanged(edits []Edit) bool {
	return len(edits) == 2 && edits[0].Offset == 2 && edits[1].Offset == 0
}

func benchmarkDocument(target int) string {
	var text strings.Builder
	text.Grow(target)
	text.WriteString(`{"items":[`)
	for i := 0; text.Len() < target; i++ {
		if i > 0 {
			text.WriteByte(',')
		}
		text.WriteString(`{"id":`)
		text.WriteString(strconv.Itoa(i))
		text.WriteString(`,"name":"benchmark-item","active":true,"score":12.5,"tags":["a","b","c"]}`)
	}
	text.WriteString(`]}`)
	return text.String()
}

func BenchmarkParseTree5MiB(b *testing.B) {
	text := benchmarkDocument(5 << 20)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		ParseTree(text)
	}
}

func BenchmarkFormat5MiB(b *testing.B) {
	text := benchmarkDocument(5 << 20)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		Format(text, nil, FormattingOptions{TabSize: 2, InsertSpaces: true, EOL: "\n"})
	}
}
