package graphql

import (
	"strings"
	"testing"
)

func TestOutlineSymbolsDocumentAndPartialText(t *testing.T) {
	const doc = "query A { user { id name } }\n" +
		"query B($id: ID!) { node(id: $id) { ...Fields } }\n" +
		"fragment Fields on User { id email }"
	symbols := OutlineSymbols(doc)
	want := []struct {
		kind  SymbolKind
		label string
		text  string
	}{
		{SymbolOperation, "query A", "query A { user { id name } }"},
		{SymbolField, "user", "user { id name }"},
		{SymbolField, "id", "id"},
		{SymbolField, "name", "name"},
		{SymbolOperation, "query B", "query B($id: ID!) { node(id: $id) { ...Fields } }"},
		{SymbolField, "node", "node(id: $id) { ...Fields }"},
		{SymbolField, "...Fields", "Fields"},
		{SymbolFragment, "fragment Fields on User", "fragment Fields on User { id email }"},
		{SymbolField, "id", "id"},
		{SymbolField, "email", "email"},
	}
	if len(symbols) != len(want) {
		t.Fatalf("got %d symbols, want %d: %+v", len(symbols), len(want), symbols)
	}
	for i, expected := range want {
		got := symbols[i]
		if got.Kind != expected.kind || got.Label != expected.label {
			t.Errorf("symbol %d = %s %q, want %s %q", i, got.Kind, got.Label, expected.kind, expected.label)
		}
		if got.Start < 0 || got.End > len(doc) || got.End <= got.Start || doc[got.Start:got.End] != expected.text {
			t.Errorf("symbol %d span = %q, want %q", i, slice(doc, got.Start, got.End), expected.text)
		}
		if got.Kind == SymbolField && got.Parent < 0 {
			t.Errorf("symbol %d %q has no parent", i, got.Label)
		}
	}
	if symbols[4].Parent != -1 {
		t.Errorf("second operation parent = %d, want root", symbols[4].Parent)
	}
	if symbols[2].Parent != 1 || symbols[6].Parent != 5 {
		t.Errorf("nested field parents = %d, %d; want 1, 5", symbols[2].Parent, symbols[6].Parent)
	}
}

func slice(s string, start, end int) string {
	start, end = min(max(start, 0), len(s)), min(max(end, 0), len(s))
	if end < start {
		return ""
	}
	return s[start:end]
}

func TestOutlineSymbolsToleratesTruncatedAndBrokenText(t *testing.T) {
	for _, source := range []string{
		"",
		"{",
		"query { user { id ",
		"garbage } {",
		"query A { user(where: {{name}}) { id } }",
		strings.Repeat("query A { a b c }\n", 64) + "query Broken {",
	} {
		symbols := OutlineSymbols(source)
		for _, s := range symbols {
			if s.Start < 0 || s.End <= s.Start || s.End > len(source) {
				t.Errorf("source %q has invalid span %+v", source[:min(40, len(source))], s)
			}
			if s.Parent >= len(symbols) {
				t.Errorf("source %q has invalid parent %d", source[:min(40, len(source))], s.Parent)
			}
		}
	}
	if got := OutlineSymbols("query { user { id "); len(got) < 2 || got[1].Label != "user" {
		t.Fatalf("truncated document lost its fields: %+v", got)
	}
}
