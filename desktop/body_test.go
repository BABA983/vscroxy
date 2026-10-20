package main

import "testing"

func TestParseJSON(t *testing.T) {
	roots, ok := parseJSON(`{"a":[1,2],"b":true}`)
	if !ok || len(roots) != 1 {
		t.Fatalf("parse = %v, %v", roots, ok)
	}
	root := roots[0]
	if root.kind != "object" || len(root.children) != 2 {
		t.Fatalf("root = %+v", root)
	}
	if root.children[0].key != "a" || root.children[0].kind != "array" || len(root.children[0].children) != 2 {
		t.Fatalf("first child = %+v", root.children[0])
	}
	if root.children[1].key != "b" || root.children[1].value != "true" {
		t.Fatalf("second child = %+v", root.children[1])
	}
	if _, ok := parseJSON("not json"); ok {
		t.Fatal("plain text parsed as JSON")
	}
}

func TestHexLine(t *testing.T) {
	body := "Hello, world!\x00\x01\x02"
	got := hexLine(body, 0)
	want := "00000000  48 65 6c 6c 6f 2c 20 77  6f 72 6c 64 21 00 01 02  |Hello, world!...|"
	if got != want {
		t.Fatalf("hex line =\n%q\nwant\n%q", got, want)
	}
	if got := hexLine(body, 1); got != "" {
		t.Fatalf("second line = %q, want empty", got)
	}
}

func TestFilterLines(t *testing.T) {
	got := filterLines("alpha\nbeta\nalphabet", "alpha")
	want := "    1  alpha\n    3  alphabet\n"
	if got != want {
		t.Fatalf("filtered = %q, want %q", got, want)
	}
	if got := filterLines("alpha", "zzz"); got != "No matches" {
		t.Fatalf("no matches = %q", got)
	}
}
