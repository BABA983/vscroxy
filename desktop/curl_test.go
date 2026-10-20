package main

import (
	"net/http"
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
	got := curlCommand(f)
	for _, want := range []string{
		"curl -X POST",
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
	if got := curlCommand(flow{method: "GET", url: "http://example.com/"}); got != "curl 'http://example.com/'" {
		t.Errorf("curlCommand() = %q", got)
	}
}
