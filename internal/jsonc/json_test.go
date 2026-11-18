// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See [VSCODE_LICENSE.txt] in this directory.
//
// The test names and expectations mirror src/vs/base/test/common/json.test.ts
// at microsoft/vscode 972b66fdc541e10689adfd90ade9549dea1b18bc.

package jsonc

import (
	"reflect"
	"strings"
	"testing"
)

func assertKinds(t *testing.T, text string, kinds ...SyntaxKind) {
	t.Helper()
	scanner := CreateScanner(text, false)
	for i, want := range kinds {
		if got := scanner.Scan(); got != want {
			t.Fatalf("%q: token %d: got %d, want %d", text, i, got, want)
		}
	}
	if got := scanner.Scan(); got != SyntaxKindEOF {
		t.Fatalf("%q: got extra token %d before EOF", text, got)
	}
}

func assertScanError(t *testing.T, text string, wantKind SyntaxKind, wantError ScanError) {
	t.Helper()
	scanner := CreateScanner(text, false)
	if got := scanner.Scan(); got != wantKind {
		t.Fatalf("%q: got token %d, want %d", text, got, wantKind)
	}
	if got := scanner.GetTokenError(); got != wantError {
		t.Fatalf("%q: got scan error %d, want %d", text, got, wantError)
	}
}

func assertValidParse(t *testing.T, input string, expected any, options ...ParseOptions) {
	t.Helper()
	actual, errs := Parse(input, options...)
	if len(errs) != 0 {
		t.Fatalf("%q: unexpected errors: %v (%s)", input, errs, GetParseErrorMessage(errs[0].Error))
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("%q: got %#v, want %#v", input, actual, expected)
	}
}

func assertInvalidParse(t *testing.T, input string, expected any, options ...ParseOptions) {
	t.Helper()
	actual, errs := Parse(input, options...)
	if len(errs) == 0 {
		t.Fatalf("%q: expected errors", input)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("%q: got %#v, want %#v", input, actual, expected)
	}
}

func scalar(kind NodeType, offset, length int, value any) Node {
	return Node{Type: kind, Offset: offset, Length: length, ColonOffset: -1, Value: value}
}

func childPointers(children []Node) []*Node {
	result := make([]*Node, len(children))
	for i := range children {
		child := children[i]
		result[i] = &child
	}
	return result
}

func container(kind NodeType, offset, length int, children ...Node) Node {
	return Node{Type: kind, Offset: offset, Length: length, ColonOffset: -1, Children: childPointers(children)}
}

func property(offset, length, colonOffset int, children ...Node) Node {
	return Node{Type: NodeTypeProperty, Offset: offset, Length: length, ColonOffset: colonOffset, Children: childPointers(children)}
}

func assertNode(t *testing.T, got *Node, want Node, path string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: node is nil, want %#v", path, want)
	}
	if got.Type != want.Type || got.Offset != want.Offset || got.Length != want.Length ||
		got.ColonOffset != want.ColonOffset || !reflect.DeepEqual(got.Value, want.Value) {
		t.Fatalf("%s: got %#v, want %#v", path, flattenNode(got), want)
	}
	if len(got.Children) != len(want.Children) {
		t.Fatalf("%s: got %d children, want %d (%#v)", path, len(got.Children), len(want.Children), got)
	}
	for i, child := range want.Children {
		assertNode(t, got.Children[i], *child, path+"."+string(child.Type))
	}
}

func flattenNode(node *Node) Node {
	copy := *node
	copy.Parent = nil
	copy.Children = nil
	return copy
}

func assertParentLinks(t *testing.T, node *Node, parent *Node) {
	t.Helper()
	if node == nil {
		return
	}
	if node.Parent != parent {
		t.Fatalf("node at %d has wrong parent", node.Offset)
	}
	for _, child := range node.Children {
		assertParentLinks(t, child, node)
	}
}

func assertTree(t *testing.T, input string, expected Node, expectedErrors []ParseErrorCode, options ...ParseOptions) {
	t.Helper()
	actual, errs := ParseTree(input, options...)
	var gotErrors []ParseErrorCode
	for _, err := range errs {
		gotErrors = append(gotErrors, err.Error)
	}
	if !reflect.DeepEqual(gotErrors, expectedErrors) {
		t.Fatalf("%q: got errors %v, want %v", input, gotErrors, expectedErrors)
	}
	if actual == nil {
		t.Fatalf("%q: no tree", input)
	}
	assertParentLinks(t, actual, nil)
	assertNode(t, actual, expected, "root")
}

func TestVSCodeJSONTokens(t *testing.T) {
	assertKinds(t, "{", SyntaxKindOpenBraceToken)
	assertKinds(t, "}", SyntaxKindCloseBraceToken)
	assertKinds(t, "[", SyntaxKindOpenBracketToken)
	assertKinds(t, "]", SyntaxKindCloseBracketToken)
	assertKinds(t, ":", SyntaxKindColonToken)
	assertKinds(t, ",", SyntaxKindCommaToken)
}

func TestVSCodeJSONComments(t *testing.T) {
	assertKinds(t, "// this is a comment", SyntaxKindLineCommentTrivia)
	assertKinds(t, "// this is a comment\n", SyntaxKindLineCommentTrivia, SyntaxKindLineBreakTrivia)
	assertKinds(t, "/* this is a comment*/", SyntaxKindBlockCommentTrivia)
	assertKinds(t, "/* this is a \r\ncomment*/", SyntaxKindBlockCommentTrivia)
	assertKinds(t, "/* this is a \ncomment*/", SyntaxKindBlockCommentTrivia)

	assertKinds(t, "/* this is a", SyntaxKindBlockCommentTrivia)
	assertKinds(t, "/* this is a \ncomment", SyntaxKindBlockCommentTrivia)

	assertKinds(t, "/ ttt", SyntaxKindUnknown, SyntaxKindTrivia, SyntaxKindUnknown)
}

func TestVSCodeJSONStrings(t *testing.T) {
	assertKinds(t, `"test"`, SyntaxKindStringLiteral)
	assertKinds(t, `"\""`, SyntaxKindStringLiteral)
	assertKinds(t, `"\/"`, SyntaxKindStringLiteral)
	assertKinds(t, `"\b"`, SyntaxKindStringLiteral)
	assertKinds(t, `"\f"`, SyntaxKindStringLiteral)
	assertKinds(t, `"\n"`, SyntaxKindStringLiteral)
	assertKinds(t, `"\r"`, SyntaxKindStringLiteral)
	assertKinds(t, `"\t"`, SyntaxKindStringLiteral)
	assertKinds(t, `"\v"`, SyntaxKindStringLiteral)
	assertKinds(t, "\"\u88ff\"", SyntaxKindStringLiteral)
	assertKinds(t, "\"\u200b\u2028\"", SyntaxKindStringLiteral)

	assertKinds(t, `"test`, SyntaxKindStringLiteral)
	assertKinds(t, "\"test\n\"", SyntaxKindStringLiteral, SyntaxKindLineBreakTrivia, SyntaxKindStringLiteral)

	assertScanError(t, "\"\t\"", SyntaxKindStringLiteral, ScanErrorInvalidCharacter)
	assertScanError(t, "\"\t \"", SyntaxKindStringLiteral, ScanErrorInvalidCharacter)
}

func TestVSCodeJSONNumbers(t *testing.T) {
	assertKinds(t, "0", SyntaxKindNumericLiteral)
	assertKinds(t, "0.1", SyntaxKindNumericLiteral)
	assertKinds(t, "-0.1", SyntaxKindNumericLiteral)
	assertKinds(t, "-1", SyntaxKindNumericLiteral)
	assertKinds(t, "1", SyntaxKindNumericLiteral)
	assertKinds(t, "123456789", SyntaxKindNumericLiteral)
	assertKinds(t, "10", SyntaxKindNumericLiteral)
	assertKinds(t, "90", SyntaxKindNumericLiteral)
	assertKinds(t, "90E+123", SyntaxKindNumericLiteral)
	assertKinds(t, "90e+123", SyntaxKindNumericLiteral)
	assertKinds(t, "90e-123", SyntaxKindNumericLiteral)
	assertKinds(t, "90E-123", SyntaxKindNumericLiteral)
	assertKinds(t, "90E123", SyntaxKindNumericLiteral)
	assertKinds(t, "90e123", SyntaxKindNumericLiteral)

	assertKinds(t, "01", SyntaxKindNumericLiteral, SyntaxKindNumericLiteral)
	assertKinds(t, "-01", SyntaxKindNumericLiteral, SyntaxKindNumericLiteral)

	assertKinds(t, "-", SyntaxKindUnknown)
	assertKinds(t, ".0", SyntaxKindUnknown)
}

func TestVSCodeJSONKeywords(t *testing.T) {
	assertKinds(t, "true", SyntaxKindTrueKeyword)
	assertKinds(t, "false", SyntaxKindFalseKeyword)
	assertKinds(t, "null", SyntaxKindNullKeyword)

	assertKinds(t, "true false null",
		SyntaxKindTrueKeyword, SyntaxKindTrivia, SyntaxKindFalseKeyword,
		SyntaxKindTrivia, SyntaxKindNullKeyword)

	assertKinds(t, "nulllll", SyntaxKindUnknown)
	assertKinds(t, "True", SyntaxKindUnknown)
	assertKinds(t, "foo-bar", SyntaxKindUnknown)
	assertKinds(t, "foo bar", SyntaxKindUnknown, SyntaxKindTrivia, SyntaxKindUnknown)
}

func TestVSCodeJSONTrivia(t *testing.T) {
	assertKinds(t, " ", SyntaxKindTrivia)
	assertKinds(t, "  \t  ", SyntaxKindTrivia)
	assertKinds(t, "  \t  \n  \t  ", SyntaxKindTrivia, SyntaxKindLineBreakTrivia, SyntaxKindTrivia)
	assertKinds(t, "\r\n", SyntaxKindLineBreakTrivia)
	assertKinds(t, "\r", SyntaxKindLineBreakTrivia)
	assertKinds(t, "\n", SyntaxKindLineBreakTrivia)
	assertKinds(t, "\n\r", SyntaxKindLineBreakTrivia, SyntaxKindLineBreakTrivia)
	assertKinds(t, "\n   \n", SyntaxKindLineBreakTrivia, SyntaxKindTrivia, SyntaxKindLineBreakTrivia)
}

func TestVSCodeJSONParseLiterals(t *testing.T) {
	assertValidParse(t, "true", true)
	assertValidParse(t, "false", false)
	assertValidParse(t, "null", nil)
	assertValidParse(t, `"foo"`, "foo")
	assertValidParse(t, `"\"-\\-\/-\b-\f-\n-\r-\t"`, "\"-\\-/-\b-\f-\n-\r-\t")
	assertValidParse(t, `"\u00DC"`, "Ü")
	assertValidParse(t, "9", float64(9))
	assertValidParse(t, "-9", float64(-9))
	assertValidParse(t, "0.129", 0.129)
	assertValidParse(t, "23e3", float64(23e3))
	assertValidParse(t, "1.2E+3", float64(1.2e+3))
	assertValidParse(t, "1.2E-3", 1.2e-3)
	assertValidParse(t, "1.2E-3 // comment", 1.2e-3)
}

func TestVSCodeJSONParseObjects(t *testing.T) {
	assertValidParse(t, "{}", map[string]any{})
	assertValidParse(t, `{ "foo": true }`, map[string]any{"foo": true})
	assertValidParse(t, `{ "bar": 8, "xoo": "foo" }`, map[string]any{"bar": float64(8), "xoo": "foo"})
	assertValidParse(t, `{ "hello": [], "world": {} }`, map[string]any{"hello": []any{}, "world": map[string]any{}})
	assertValidParse(t, `{ "a": false, "b": true, "c": [ 7.4 ] }`, map[string]any{
		"a": false, "b": true, "c": []any{7.4},
	})
	assertValidParse(t, `{ "lineComment": "//", "blockComment": ["/*", "*/"], "brackets": [ ["{", "}"], ["[", "]"], ["(", ")"] ] }`,
		map[string]any{
			"lineComment":  "//",
			"blockComment": []any{"/*", "*/"},
			"brackets":     []any{[]any{"{", "}"}, []any{"[", "]"}, []any{"(", ")"}},
		})
	assertValidParse(t, `{ "hello": { "again": { "inside": 5 }, "world": 1 }}`, map[string]any{
		"hello": map[string]any{"again": map[string]any{"inside": float64(5)}, "world": float64(1)},
	})
	assertValidParse(t, `{ "foo": /*hello*/true }`, map[string]any{"foo": true})
}

func TestVSCodeJSONParseProtoIsOwnKey(t *testing.T) {
	value, errs := Parse(`{ "__proto__": { "polluted": true } }`)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", value)
	}
	nested, ok := object["__proto__"].(map[string]any)
	if !ok || nested["polluted"] != true {
		t.Fatalf("__proto__ was not preserved as an own key: %#v", object)
	}
}

func TestVSCodeJSONParseArrays(t *testing.T) {
	assertValidParse(t, "[]", []any{})
	assertValidParse(t, "[ [],  [ [] ]]", []any{[]any{}, []any{[]any{}}})
	assertValidParse(t, "[ 1, 2, 3 ]", []any{float64(1), float64(2), float64(3)})
	assertValidParse(t, `[ { "a": null } ]`, []any{map[string]any{"a": nil}})
}

func TestVSCodeJSONParseObjectErrors(t *testing.T) {
	assertInvalidParse(t, "{,}", map[string]any{})
	assertInvalidParse(t, `{ "foo": true, }`, map[string]any{"foo": true}, ParseOptions{})
	assertInvalidParse(t, `{ "bar": 8 "xoo": "foo" }`, map[string]any{"bar": float64(8), "xoo": "foo"})
	assertInvalidParse(t, `{ ,"bar": 8 }`, map[string]any{"bar": float64(8)})
	assertInvalidParse(t, `{ ,"bar": 8, "foo" }`, map[string]any{"bar": float64(8)})
	assertInvalidParse(t, `{ "bar": 8, "foo": }`, map[string]any{"bar": float64(8)})
	assertInvalidParse(t, `{ 8, "foo": 9 }`, map[string]any{"foo": float64(9)})
}

func TestVSCodeJSONParseArrayErrors(t *testing.T) {
	assertInvalidParse(t, "[,]", []any{})
	assertInvalidParse(t, "[ 1, 2, ]", []any{float64(1), float64(2)}, ParseOptions{})
	assertInvalidParse(t, "[ 1 2, 3 ]", []any{float64(1), float64(2), float64(3)})
	assertInvalidParse(t, "[ ,1, 2, 3 ]", []any{float64(1), float64(2), float64(3)})
	assertInvalidParse(t, "[ ,1, 2, 3, ]", []any{float64(1), float64(2), float64(3)}, ParseOptions{})
}

func TestVSCodeJSONParseDisallowComments(t *testing.T) {
	options := ParseOptions{DisallowComments: true}
	assertValidParse(t, `[ 1, 2, null, "foo" ]`, []any{float64(1), float64(2), nil, "foo"}, options)
	assertValidParse(t, `{ "hello": [], "world": {} }`, map[string]any{
		"hello": []any{}, "world": map[string]any{},
	}, options)
	assertInvalidParse(t, `{ "foo": /*comment*/ true }`, map[string]any{"foo": true}, options)
}

func TestVSCodeJSONParseTrailingComma(t *testing.T) {
	assertValidParse(t, `{ "hello": [], }`, map[string]any{"hello": []any{}})
	allow := ParseOptions{AllowTrailingComma: true}
	assertValidParse(t, `{ "hello": [], }`, map[string]any{"hello": []any{}}, allow)
	assertValidParse(t, `{ "hello": [] }`, map[string]any{"hello": []any{}}, allow)
	assertValidParse(t, `{ "hello": [], "world": {}, }`, map[string]any{
		"hello": []any{}, "world": map[string]any{},
	}, allow)
	assertValidParse(t, `{ "hello": [], "world": {} }`, map[string]any{
		"hello": []any{}, "world": map[string]any{},
	}, allow)
	assertValidParse(t, `{ "hello": [1,] }`, map[string]any{"hello": []any{float64(1)}}, allow)

	deny := ParseOptions{AllowTrailingComma: false}
	assertInvalidParse(t, `{ "hello": [], }`, map[string]any{"hello": []any{}}, deny)
	assertInvalidParse(t, `{ "hello": [], "world": {}, }`, map[string]any{
		"hello": []any{}, "world": map[string]any{},
	}, deny)
}

func TestVSCodeJSONTreeLiterals(t *testing.T) {
	assertTree(t, "true", scalar(NodeTypeBoolean, 0, 4, true), nil)
	assertTree(t, "false", scalar(NodeTypeBoolean, 0, 5, false), nil)
	assertTree(t, "null", scalar(NodeTypeNull, 0, 4, nil), nil)
	assertTree(t, "23", scalar(NodeTypeNumber, 0, 2, float64(23)), nil)
	assertTree(t, "-1.93e-19", scalar(NodeTypeNumber, 0, 9, -1.93e-19), nil)
	assertTree(t, `"hello"`, scalar(NodeTypeString, 0, 7, "hello"), nil)
}

func TestVSCodeJSONTreeArrays(t *testing.T) {
	assertTree(t, "[]", container(NodeTypeArray, 0, 2), nil)
	assertTree(t, "[ 1 ]", container(NodeTypeArray, 0, 5,
		scalar(NodeTypeNumber, 2, 1, float64(1))), nil)
	assertTree(t, `[ 1,"x"]`, container(NodeTypeArray, 0, 8,
		scalar(NodeTypeNumber, 2, 1, float64(1)),
		scalar(NodeTypeString, 4, 3, "x")), nil)
	assertTree(t, "[[]]", container(NodeTypeArray, 0, 4,
		container(NodeTypeArray, 1, 2)), nil)
}

func TestVSCodeJSONTreeObjects(t *testing.T) {
	assertTree(t, "{ }", container(NodeTypeObject, 0, 3), nil)
	assertTree(t, `{ "val": 1 }`, container(NodeTypeObject, 0, 12,
		property(2, 8, 7,
			scalar(NodeTypeString, 2, 5, "val"),
			scalar(NodeTypeNumber, 9, 1, float64(1)))), nil)
	assertTree(t, `{"id": "$", "v": [ null, null] }`, container(NodeTypeObject, 0, 32,
		property(1, 9, 5,
			scalar(NodeTypeString, 1, 4, "id"),
			scalar(NodeTypeString, 7, 3, "$")),
		property(12, 18, 15,
			scalar(NodeTypeString, 12, 3, "v"),
			container(NodeTypeArray, 17, 13,
				scalar(NodeTypeNull, 19, 4, nil),
				scalar(NodeTypeNull, 25, 4, nil)))), nil)
	assertTree(t, `{  "id": { "foo": { } } , }`, container(NodeTypeObject, 0, 27,
		property(3, 20, 7,
			scalar(NodeTypeString, 3, 4, "id"),
			container(NodeTypeObject, 9, 14,
				property(11, 10, 16,
					scalar(NodeTypeString, 11, 5, "foo"),
					container(NodeTypeObject, 18, 3))))),
		[]ParseErrorCode{ParseErrorCodePropertyNameExpected, ParseErrorCodeValueExpected},
		ParseOptions{AllowTrailingComma: false})
}

func TestVSCodeJSONErrorMessage(t *testing.T) {
	if got := GetParseErrorMessage(ParseErrorCodeInvalidSymbol); got != "Invalid symbol" {
		t.Fatalf("got %q", got)
	}
	if got := GetParseErrorMessage(ParseErrorCodeInvalidCharacter); got != "" {
		t.Fatalf("got %q for an unmapped code", got)
	}
}

func TestVisitAndEmptyContentOption(t *testing.T) {
	var events []string
	Visit(`{"a":1}`, JSONVisitor{
		OnObjectBegin: func(_, _ int) { events = append(events, "object begin") },
		OnObjectProperty: func(property string, _, _ int) {
			events = append(events, "property "+property)
		},
		OnLiteralValue: func(value any, _, _ int) {
			events = append(events, "value")
			if value != float64(1) {
				t.Fatalf("literal value: got %#v", value)
			}
		},
		OnSeparator: func(character string, _, _ int) {
			events = append(events, "separator "+character)
		},
		OnObjectEnd: func(_, _ int) { events = append(events, "object end") },
	})
	want := []string{"object begin", "property a", "separator :", "value", "object end"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("Visit events: got %v, want %v", events, want)
	}

	if _, errs := Parse(""); len(errs) != 1 || errs[0].Error != ParseErrorCodeValueExpected {
		t.Fatalf("empty content without the option: %#v", errs)
	}
	if value, errs := Parse("", ParseOptions{AllowEmptyContent: true}); value != nil || len(errs) != 0 {
		t.Fatalf("empty content with the option: %#v, %#v", value, errs)
	}
}

func TestTruncatedInputByteOffsets(t *testing.T) {
	const midString = `{"a":"你`
	tree, errs := ParseTree(midString)
	wantErrors := []ParseError{
		{Error: ParseErrorCodeUnexpectedEndOfString, Offset: 5, Length: 4},
		{Error: ParseErrorCodeCloseBraceExpected, Offset: len(midString), Length: 0},
	}
	if !reflect.DeepEqual(errs, wantErrors) {
		t.Fatalf("mid-string errors: got %#v, want %#v", errs, wantErrors)
	}
	assertNode(t, tree, container(NodeTypeObject, 0, len(midString),
		property(1, len(midString)-1, 4,
			scalar(NodeTypeString, 1, 3, "a"),
			scalar(NodeTypeString, 5, 4, "你"))), "root")

	const midNumber = `{"a":12`
	tree, errs = ParseTree(midNumber)
	if len(errs) != 1 || errs[0].Error != ParseErrorCodeCloseBraceExpected ||
		errs[0].Offset != len(midNumber) {
		t.Fatalf("mid-number errors: got %#v", errs)
	}
	assertNode(t, tree, container(NodeTypeObject, 0, len(midNumber),
		property(1, len(midNumber)-1, 4,
			scalar(NodeTypeString, 1, 3, "a"),
			scalar(NodeTypeNumber, 5, 2, float64(12)))), "root")

	const midObject = `{"a":[1,2`
	tree, errs = ParseTree(midObject)
	wantErrors = []ParseError{
		{Error: ParseErrorCodeCloseBracketExpected, Offset: len(midObject), Length: 0},
		{Error: ParseErrorCodeCloseBraceExpected, Offset: len(midObject), Length: 0},
	}
	if !reflect.DeepEqual(errs, wantErrors) {
		t.Fatalf("mid-object errors: got %#v, want %#v", errs, wantErrors)
	}
	assertNode(t, tree, container(NodeTypeObject, 0, len(midObject),
		property(1, len(midObject)-1, 4,
			scalar(NodeTypeString, 1, 3, "a"),
			container(NodeTypeArray, 5, len(midObject)-5,
				scalar(NodeTypeNumber, 6, 1, float64(1)),
				scalar(NodeTypeNumber, 8, 1, float64(2))))), "root")

	value, errs := Parse(midObject)
	if !reflect.DeepEqual(value, map[string]any{"a": []any{float64(1), float64(2)}}) || len(errs) != 2 {
		t.Fatalf("truncated Parse: got %#v, %#v", value, errs)
	}
}

func TestNonASCIIByteOffsetsAndEscapes(t *testing.T) {
	const text = `{"é":"你好"}`
	scanner := CreateScanner(text, false)
	type token struct {
		kind      SyntaxKind
		offset    int
		length    int
		value     string
		scanError ScanError
	}
	want := []token{
		{SyntaxKindOpenBraceToken, 0, 1, "", ScanErrorNone},
		{SyntaxKindStringLiteral, 1, 4, "é", ScanErrorNone},
		{SyntaxKindColonToken, 5, 1, "", ScanErrorNone},
		{SyntaxKindStringLiteral, 6, 8, "你好", ScanErrorNone},
		{SyntaxKindCloseBraceToken, 14, 1, "", ScanErrorNone},
		{SyntaxKindEOF, 15, 0, "", ScanErrorNone},
	}
	for i, expected := range want {
		got := token{scanner.Scan(), scanner.GetTokenOffset(), scanner.GetTokenLength(),
			scanner.GetTokenValue(), scanner.GetTokenError()}
		if got != expected {
			t.Fatalf("token %d: got %#v, want %#v", i, got, expected)
		}
	}

	assertValidParse(t, `"\ud83d\ude00"`, "😀")
	assertValidParse(t, `"\u00dc"`, "Ü")
	assertValidParse(t, `"\uD800"`, "\uFFFD")

	crlf := CreateScanner("\r\n", false)
	if kind := crlf.Scan(); kind != SyntaxKindLineBreakTrivia || crlf.GetTokenLength() != 2 || crlf.GetTokenValue() != "\n" {
		t.Fatalf("CRLF trivia: kind %d, length %d, value %q", kind, crlf.GetTokenLength(), crlf.GetTokenValue())
	}
}

func TestTruncatedStringAfterEscape(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"no escape", `"abc`, "abc"},
		{"trailing backslash", `"abc\`, "abc"},
		{"short trailing backslash", `"ab\`, "ab"},
		{"escaped newline then trailing backslash", `"a\nb\`, "a\nb"},
		{"escaped then raw newline", "\"a\\nb\nc\"", "a\nb"},
		{"invalid escape at end", `"a\q`, "a"},
		{"truncated unicode escape", `"\u12`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scanner := CreateScanner(tc.input, false)
			if kind := scanner.Scan(); kind != SyntaxKindStringLiteral {
				t.Fatalf("got token %d, want string literal", kind)
			}
			if got := scanner.GetTokenValue(); got != tc.want {
				t.Fatalf("value: got %q, want %q", got, tc.want)
			}
			if got := scanner.GetTokenError(); got != ScanErrorUnexpectedEndOfString {
				t.Fatalf("scan error: got %d, want UnexpectedEndOfString", got)
			}
		})
	}

	trees := []struct {
		input string
		want  string
	}{
		{`{"x":"abc\`, "abc"},
		{`{"x":"ab\`, "ab"},
		{`{"x":"a\nb\`, "a\nb"},
		{`{"x":"\u12`, ""},
	}
	for _, tc := range trees {
		t.Run("tree "+tc.input, func(t *testing.T) {
			tree, errs := ParseTree(tc.input)
			if len(errs) == 0 || errs[0].Error != ParseErrorCodeUnexpectedEndOfString {
				t.Fatalf("errors: got %#v", errs)
			}
			node := FindNodeAtLocation(tree, JSONPath{KeySegment("x")})
			if node == nil || node.Type != NodeTypeString || node.Value != tc.want {
				t.Fatalf("value node: got %#v, want string %q", node, tc.want)
			}
		})
	}
}

func TestTruncatedEscapeAt8MiBCut(t *testing.T) {
	const payload = 8 << 20
	input := `{"x":"` + strings.Repeat("a", payload) + `\`
	tree, errs := ParseTree(input)
	if len(errs) == 0 || errs[0].Error != ParseErrorCodeUnexpectedEndOfString {
		t.Fatalf("errors: got %#v", errs)
	}
	node := FindNodeAtLocation(tree, JSONPath{KeySegment("x")})
	if node == nil || node.Type != NodeTypeString {
		t.Fatalf("value node: got %#v", node)
	}
	value, ok := node.Value.(string)
	if !ok || len(value) != payload || value[0] != 'a' || value[len(value)-1] != 'a' {
		t.Fatalf("value: got a string of length %d", len(value))
	}
}

func TestNodePathsOffsetsAndLocation(t *testing.T) {
	const text = `{"a":[1,{"b":true}]}`
	tree, errs := ParseTree(text)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	path := JSONPath{KeySegment("a"), IndexSegment(1), KeySegment("b")}
	node := FindNodeAtLocation(tree, path)
	if node == nil || node.Type != NodeTypeBoolean || node.Value != true {
		t.Fatalf("FindNodeAtLocation: got %#v", node)
	}
	if got := GetNodePath(node); !reflect.DeepEqual(got, path) {
		t.Fatalf("GetNodePath: got %#v, want %#v", got, path)
	}
	if got := FindNodeAtOffset(tree, node.Offset+1, false); got != node {
		t.Fatalf("FindNodeAtOffset: got %#v, want %#v", got, node)
	}
	if FindNodeAtLocation(tree, JSONPath{KeySegment("missing")}) != nil ||
		FindNodeAtLocation(tree, JSONPath{IndexSegment(0)}) != nil {
		t.Fatal("expected nil for missing paths")
	}
	if got := GetNodeValue(tree); !reflect.DeepEqual(got, map[string]any{
		"a": []any{float64(1), map[string]any{"b": true}},
	}) {
		t.Fatalf("GetNodeValue: got %#v", got)
	}

	location := GetLocation(text, strings.LastIndex(text, "true")+1)
	if !reflect.DeepEqual(location.Path, path) {
		t.Fatalf("GetLocation path: got %#v, want %#v", location.Path, path)
	}
	if location.PreviousNode == nil || location.PreviousNode.Value != true {
		t.Fatalf("GetLocation previous node: %#v", location.PreviousNode)
	}
	if !location.Matches(JSONPath{KeySegment("a"), KeySegment("*"), KeySegment("b")}) {
		t.Fatal("wildcard did not match the location path")
	}
	if location.Matches(JSONPath{KeySegment("other")}) {
		t.Fatal("nonmatching path matched")
	}
}
