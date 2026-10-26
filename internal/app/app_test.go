package app

import (
	"net/http"
	"testing"
)

func TestStatusMatches(t *testing.T) {
	cases := []struct {
		expr   string
		status int
		want   bool
	}{
		{"", 200, true},
		{"200", 200, true},
		{"200", 201, false},
		{"4xx", 404, true},
		{"4xx", 200, false},
		{"5xx", 503, true},
		{">=400", 404, true},
		{">=400", 399, false},
		{"<300", 204, true},
		{"<300", 301, false},
		{"200-299", 204, true},
		{"200-299", 300, false},
		{"4xx, 5xx", 401, true},
		{"4xx, 5xx", 503, true},
		{"4xx, 5xx", 302, false},
		{"201, 404", 202, false},
		{"201, 404", 404, true},
		{"4xx, nonsense", 401, false},
		{"4xx,", 401, false},
		{"599-200", 300, false},
		{"6xx", 600, false},
		// Something that is not a status filter keeps nothing, rather than
		// showing everything as if the field were empty.
		{"nonsense", 200, false},
	}
	for _, c := range cases {
		if got := statusMatches(c.expr, c.status); got != c.want {
			t.Errorf("statusMatches(%q, %d) = %v, want %v", c.expr, c.status, got, c.want)
		}
	}
}

func TestHeaderMatches(t *testing.T) {
	f := flow{
		reqHeader:  http.Header{"X-Trace": {"abc-1"}},
		respHeader: http.Header{"Content-Type": {"application/json; charset=utf-8"}},
	}
	cases := []struct {
		expr string
		want bool
	}{
		{"", true},
		{"x-trace", true},
		{"X-Trace", true},       // the name is case insensitive
		{"x-trace: abc", true},  // a value the header carries
		{"x-trace: zzz", false}, // a value it does not
		{"content-type", true},  // from the response
		{"content-type: json", true},
		{"x-missing", false},
		{"  x-trace  ", true}, // spaces around the name
	}
	for _, c := range cases {
		if got := headerMatches(f, c.expr); got != c.want {
			t.Errorf("headerMatches(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestHeaderValuePrefersTheResponse(t *testing.T) {
	a := &app{headerColumn: "x-cache"}
	f := flow{
		reqHeader:  http.Header{"X-Cache": {"request"}},
		respHeader: http.Header{"X-Cache": {"HIT"}},
	}
	if got := a.headerValue(f); got != "HIT" {
		t.Errorf("headerValue = %q, want the response's", got)
	}
	a.headerColumn = "x-only-request"
	f = flow{reqHeader: http.Header{"X-Only-Request": {"yes"}}}
	if got := a.headerValue(f); got != "yes" {
		t.Errorf("headerValue = %q, want the request's", got)
	}
}
