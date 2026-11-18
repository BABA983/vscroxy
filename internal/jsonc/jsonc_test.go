// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See [VSCODE_LICENSE.txt] in this directory.
//
// Expectations are adapted from src/vs/base/test/common/jsonParse.test.ts at
// microsoft/vscode 972b66fdc541e10689adfd90ade9549dea1b18bc.

package jsonc

import "testing"

func TestVSCodeStripComments(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"line comment", "{\n  \"prop\": 10 // a comment\n}", "{\n  \"prop\": 10 \n}"},
		{"line comment EOF", "{\n}\n// a comment", "{\n}\n"},
		{"line comment CRLF", "{\r\n  \"prop\": 10 // a comment\r\n}", "{\r\n  \"prop\": 10 \r\n}"},
		{"line comment EOF CRLF", "{\r\n}\r\n// a comment", "{\r\n}\r\n"},
		{"block comment single line", "{\n  /* before */\"prop\": 10/* after */\n}", "{\n  \"prop\": 10\n}"},
		{"block comment multi line", "{\n  /**\n   * Some comment\n   */\n  \"prop\": 10\n}", "{\n  \n  \"prop\": 10\n}"},
		{"comment markers in strings", "{\"/* */\":\"http://x\", \"quote\":\"\\\"/* not a comment */\"}", "{\"/* */\":\"http://x\", \"quote\":\"\\\"/* not a comment */\"}"},
		{"single quote text", "'/* */': 10", "'/* */': 10"},
		{"trailing comma in object", "{\n  \"a\": 10,\n}", "{\n  \"a\": 10\n}"},
		{"trailing comma in array", "[ \"a\", \"b\", \"c\", ]", "[ \"a\", \"b\", \"c\" ]"},
		{"trailing comma after comment", "{\"a\": 10, // trailing\n\"b\": false, /* trailing */ }", "{\"a\": 10, \n\"b\": false  }"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripComments(tc.input); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStripCommentsLeavesInvalidJSONCUnchanged(t *testing.T) {
	for _, input := range []string{
		"/* abc */ */", // VS Code strips this in arbitrary text; it is not JSONC.
		`{"a":1,`,
		`{"a": broken}`,
		`hello // not JSON`,
		`{"a":"unterminated`,
		"",
	} {
		if got := StripComments(input); got != input {
			t.Errorf("%q: got %q, want the input unchanged", input, got)
		}
	}
}
