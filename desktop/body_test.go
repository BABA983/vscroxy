package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContentType(t *testing.T) {
	structured := flow{respHeader: http.Header{"Content-Type": {"Image/PNG; charset=binary"}}}
	if got := structured.contentType(true); got != "image/png" {
		t.Errorf("structured = %q, want image/png", got)
	}
	formatted := flow{resHeaders: "HTTP/1.1 200 OK\nContent-Type: text/html; charset=utf-8\n"}
	if got := formatted.contentType(true); got != "text/html" {
		t.Errorf("formatted = %q, want text/html", got)
	}
	request := flow{reqHeaders: "GET / HTTP/1.1\nContent-Type: application/json\n"}
	if got := request.contentType(false); got != "application/json" {
		t.Errorf("request = %q, want application/json", got)
	}
	if got := (flow{}).contentType(true); got != "" {
		t.Errorf("empty = %q, want empty", got)
	}
}

func TestBodyReadsSpillFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.bin")
	if err := os.WriteFile(path, []byte("whole body"), 0o600); err != nil {
		t.Fatalf("write spill file: %v", err)
	}
	f := flow{resBody: "head", resFile: path, resSize: 10}
	if got := f.body(true); got != "whole body" {
		t.Errorf("spilled body = %q, want the file's", got)
	}
	f.resFile = filepath.Join(t.TempDir(), "gone.bin")
	if got := f.body(true); !strings.HasPrefix(got, "head") || !strings.Contains(got, "gone") {
		t.Errorf("missing file = %q, want the head and a note", got)
	}
	if got := (flow{resBody: "inline"}).body(true); got != "inline" {
		t.Errorf("inline body = %q, want inline", got)
	}
}

func TestParseJSON(t *testing.T) {
	roots, ok := parseJSON(`{"z":[9007199254740993,1.2300e+09],"a":true}`)
	if !ok || len(roots) != 1 {
		t.Fatalf("parse = %v, %v", roots, ok)
	}
	root := roots[0]
	if root.kind != "object" || len(root.children) != 2 {
		t.Fatalf("root = %+v", root)
	}
	if root.children[0].key != "z" || root.children[0].kind != "array" || len(root.children[0].children) != 2 {
		t.Fatalf("first child = %+v", root.children[0])
	}
	if root.children[0].children[0].value != "9007199254740993" || root.children[0].children[1].value != "1.2300e+09" {
		t.Fatal("JSON tree rounded or reformatted the original numbers")
	}
	if root.children[1].key != "a" || root.children[1].value != "true" {
		t.Fatalf("second child = %+v", root.children[1])
	}
	for _, invalid := range []string{"not json", `{"a":1`, `[1,]`, `{} {}`, `{"a":1,"a":2}`} {
		if _, ok := parseJSON(invalid); ok {
			t.Fatalf("invalid body %q parsed as JSON", invalid)
		}
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

// Display limits are independent of capture errors and must not inject
// warning text into payload bytes used by JSON/image/hex readers.
func TestBodyPreviewLimitAndWarnings(t *testing.T) {
	for _, size := range []int{maxBodyView, maxBodyView + 1} {
		f := flow{resBody: strings.Repeat("x", size), resSize: size}
		body, notice := f.bodyPreview(true)
		if len(body) != maxBodyView || strings.Contains(body, "truncated") {
			t.Fatal("display limit changed the retained payload bytes")
		}
		if (notice != "") != (size > maxBodyView) {
			t.Fatalf("size %d has incorrect limit notice: %q", size, notice)
		}
	}
	f := flow{resBody: `{"ok":true}`, resError: "transfer stopped"}
	body, notice := f.bodyPreview(true)
	if body != `{"ok":true}` || !strings.Contains(notice, "transfer stopped") || f.body(true) != body+notice {
		t.Fatal("capture warning was lost or mixed into the payload")
	}
}
