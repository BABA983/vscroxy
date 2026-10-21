package main

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCurlCommand(t *testing.T) {
	f := flow{
		method:    "POST",
		url:       "https://example.com/a?b=1",
		reqHeader: http.Header{"Content-Type": {"application/json"}, "Host": {"example.com"}, "Cookie": {"a=1"}},
		reqBody:   `{"name":"it's"}`,
	}
	got, err := curlCommand(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"curl -X 'POST'",
		`-H 'Content-Type: application/json'`,
		`-H 'Cookie: a=1'`,
		`--data-raw '{"name":"it'\''s"}'`,
		"'https://example.com/a?b=1'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("curlCommand() = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "Host:") || strings.Contains(got, "Content-Length") {
		t.Errorf("curlCommand() = %q, should leave Host and Content-Length to curl", got)
	}
}

func TestCurlCommandGetsOnlyTheURL(t *testing.T) {
	if got, err := curlCommand(flow{method: "GET", url: "http://example.com/"}); err != nil || got != "curl 'http://example.com/'" {
		t.Errorf("curlCommand() = %q, %v", got, err)
	}
}

func TestParseCurlRequestData(t *testing.T) {
	for _, tc := range []struct {
		name, command, method, url, body string
		headers                          http.Header
	}{
		{"browser", "curl 'https://api.example.test/items?limit=7' \\\n -H 'X-Tag: first' -H 'X-Tag: second' -b 'session=abc' --compressed --data-raw '{\"name\":\"it'\\''s\"}'", "POST", "https://api.example.test/items?limit=7", `{"name":"it's"}`, http.Header{"X-Tag": {"first", "second"}, "Cookie": {"session=abc"}, "Content-Type": {"application/x-www-form-urlencoded"}}},
		{"ansi and double quotes", `curl --url=https://api.example.test -XPATCH -H"X-Path: a\z" --data-binary $'a\nb%20c'`, "PATCH", "https://api.example.test", "a\nb%20c", http.Header{"X-Path": {`a\z`}, "Content-Type": {"application/x-www-form-urlencoded"}}},
		{"query encoding", `curl -G 'https://api.example.test/items?fixed=3#top' --data-urlencode 'q=a b&c' -d limit=7`, "GET", "https://api.example.test/items?fixed=3&q=a+b%26c&limit=7#top", "", http.Header{}},
		{"json and explicit overrides", `curl https://api.example.test --json '{"x":' --json '7}' -u 'user:pass' -H 'Authorization: Bearer literal' -H 'Accept:' -H 'X-Empty;'`, "POST", "https://api.example.test", `{"x":7}`, http.Header{"Authorization": {"Bearer literal"}, "Content-Type": {"application/json"}, "X-Empty": {""}}},
		{"basic auth and raw at", `curl https://api.example.test --user 'user:p:a ss' --data-raw '@not-a-file'`, "POST", "https://api.example.test", "@not-a-file", http.Header{"Authorization": {"Basic dXNlcjpwOmEgc3M="}, "Content-Type": {"application/x-www-form-urlencoded"}}},
		{"head query", `curl -I -G https://api.example.test --data-urlencode 'q=a+b'`, "HEAD", "https://api.example.test?q=a%2Bb", "", http.Header{}},
		{"environment placeholders", `curl '{{base_url}}/items/{{id}}' -H 'Authorization: Bearer {{token}}'`, "GET", "{{base_url}}/items/{{id}}", "", http.Header{"Authorization": {"Bearer {{token}}"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := parseCurl(tc.command)
			if err != nil {
				t.Fatal(err)
			}
			if r.Method != tc.method || r.URL != tc.url || r.Body != tc.body || !reflect.DeepEqual(parseHeaderBlock(r.Headers), tc.headers) {
				t.Fatalf("got %+v, headers %v; want %s %s body %q headers %v", r, parseHeaderBlock(r.Headers), tc.method, tc.url, tc.body, tc.headers)
			}
		})
	}
}

func TestParseCurlRefusesExecutableOrUnsupportedInput(t *testing.T) {
	for _, command := range []string{
		"", "wget https://example.test", "curl 'unclosed", "curl",
		"curl https://a.test https://b.test", "curl --url https://a.test --url https://b.test",
		"curl https://a.test; touch /tmp/not-executed", "curl https://a.test | cat", "curl https://a.test && echo hi", "curl https://a.test > /tmp/not-written", "curl https://a.test &", "TOKEN=secret curl https://a.test",
		"curl https://a.test -d $(touch /tmp/not-executed)", "curl https://a.test -d `id`", "curl https://a.test -H \"Authorization: $TOKEN\"", "curl https://a.test -d $((1+2))", "curl https://a.test -d <(cat /etc/passwd)",
		"curl https://a.test --data @/etc/passwd", "curl https://a.test --data-binary @-", "curl https://a.test --data-urlencode x@file", "curl https://a.test -H @file", "curl https://a.test -b cookies.txt", "curl https://a.test --config config.txt", "curl https://a.test --form file=@private", "curl https://a.test --insecure", "curl https://a.test --location", "curl https://a.test -H $'X-Test: a\\nInjected: b'", "curl https://a.test -d $'a\\0b'", "curl https://a.test -X", "curl file:///etc/passwd",
	} {
		if _, err := parseCurl(command); err == nil {
			t.Errorf("accepted unsafe or unsupported input: %q", command)
		}
	}
}

func TestCurlCopyPreservesTextBodyAndMethod(t *testing.T) {
	body := strings.Repeat("a", 70<<10) + "\nit's $HOME; $(not-executed)"
	path := filepath.Join(t.TempDir(), "spilled-body")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range []flow{
		{method: "GET", url: "https://api.example.test/?q[]=7", reqBody: "stale head", reqFile: path, reqSize: len(body), reqHeader: http.Header{"X-Empty": {""}}},
		{method: "X|id", url: "https://api.example.test", reqBody: "quotes ' and \" and \\", reqHeader: http.Header{}},
	} {
		command, err := curlCommand(f)
		if err != nil {
			t.Fatal(err)
		}
		r, err := parseCurl(command)
		if err != nil {
			t.Fatal(err)
		}
		wantBody := f.reqBody
		if f.reqFile != "" {
			wantBody = body
		}
		if r.Method != f.method || r.URL != f.url || r.Body != wantBody || !reflect.DeepEqual(parseHeaderBlock(r.Headers), f.reqHeader) {
			t.Fatalf("copy/import changed method, headers, URL or body: %+v", r)
		}
	}
	if _, err := curlCommand(flow{reqFile: filepath.Join(t.TempDir(), "gone")}); err == nil {
		t.Fatal("missing body was silently exported as a truncated request")
	}
	if _, err := curlCommand(flow{reqBody: "a\x00b"}); err == nil {
		t.Fatal("binary body was exported as an unusable shell argument")
	}
}
