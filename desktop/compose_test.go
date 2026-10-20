package main

import (
	"net/http"
	"testing"
)

func TestParseHeaderBlock(t *testing.T) {
	h := parseHeaderBlock("Content-Type: application/json\nX-Empty: \n\n# a comment\nnot a header\nAccept: text/html, */*\n")
	if got := h.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := h.Get("Accept"); got != "text/html, */*" {
		t.Errorf("Accept = %q", got)
	}
	if got := h.Get("X-Empty"); got != "" || len(h["X-Empty"]) != 1 {
		t.Errorf("X-Empty = %q, want one empty value", got)
	}
	if got := h.Get("not a header"); got != "" {
		t.Errorf("a line without a colon became a header: %q", got)
	}
	if len(h) != 3 {
		t.Errorf("headers = %v, want three", h)
	}
}

func TestHeaderBlockRoundTrip(t *testing.T) {
	h := http.Header{"Content-Type": {"text/plain"}, "X-Two": {"a", "b"}}
	back := parseHeaderBlock(headerBlock(h))
	for key, values := range h {
		got := back[key]
		if len(got) != len(values) {
			t.Fatalf("%s = %v, want %v", key, got, values)
		}
		for i := range values {
			if got[i] != values[i] {
				t.Errorf("%s[%d] = %q, want %q", key, i, got[i], values[i])
			}
		}
	}
}
