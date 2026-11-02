package rules

import (
	"net/http"
	"testing"
)

func TestHost(t *testing.T) {
	patterns := []string{"example.com", "*.internal.test", "# comment", ".corp.test"}
	cases := map[string]bool{
		"example.com":       true,
		"api.example.com":   true,
		"EXAMPLE.COM.":      true,
		"api.internal.test": true,
		"internal.test":     false,
		"corp.test":         true,
		"deep.corp.test":    true,
		"example.org":       false,
		"notexample.com":    false,
		"":                  false,
	}
	for host, want := range cases {
		if got := Host(host, patterns); got != want {
			t.Errorf("Host(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestApp(t *testing.T) {
	patterns := []string{"Google Chrome*", "# curl", "node"}
	cases := map[string]bool{
		"google chrome helper": true,
		"Node":                 true,
		"/usr/bin/node":        true,
		`C:\bin\node`:          true,
		"nodejs":               false,
		"curl":                 false,
		"":                     false,
	}
	for app, want := range cases {
		if got := App(app, patterns); got != want {
			t.Errorf("App(%q) = %v, want %v", app, got, want)
		}
	}
}

func TestMatcherNeedsEveryCondition(t *testing.T) {
	m := Matcher{URL: "/api/", Method: "POST", Hosts: []string{"example.com"}, Apps: []string{"curl"}}
	r := Request{URL: "https://api.example.com/api/users", Method: "POST", Host: "api.example.com", App: "curl"}
	if !m.Match(r) {
		t.Fatalf("Match(%+v) = false, want true", r)
	}
	r.Method, r.App = "GET", "wget"
	got := m.Mismatch(r)
	if len(got) != 2 || got[0] != "method does not match" || got[1] != "app does not match" {
		t.Fatalf("Mismatch = %q, want method and app", got)
	}
	if !(Matcher{}).Match(Request{}) {
		t.Fatal("an empty matcher should match anything")
	}
}

func TestURLModes(t *testing.T) {
	cases := []struct {
		mode, pattern, url string
		want               bool
	}{
		// Wildcards cover the whole URL; the scheme may be left out.
		{URLWildcard, "*.example.com/v1/users/*", "https://api.example.com/v1/users/42", true},
		{URLWildcard, "*.example.com/v1/users/*", "https://api.example.com/v1/users", false},
		{URLWildcard, "api.example.com/v1/*", "https://evil.test/?api.example.com/v1/x", false},
		{URLWildcard, "https://api.example.com/*", "http://api.example.com/x", false},
		// A dot is a dot, ? one character.
		{URLWildcard, "api.example.com/v?", "https://api.example.com/v1", true},
		{URLWildcard, "api.example.com/v?", "https://api.example.com/v12", false},
		{URLWildcard, "api.example.com/*", "https://apixexample.com/x", false},
		// A regex is found anywhere unless anchored; a substring is literal.
		{URLRegex, `/users/\d+$`, "https://api.example.com/v1/users/42", true},
		{URLRegex, `/users/\d+$`, "https://api.example.com/v1/users/42/posts", false},
		{URLContains, "/users/*", "https://api.example.com/users/42", false},
		{URLContains, "/users/*", "https://api.example.com/users/*", true},
	}
	for _, tc := range cases {
		m := Matcher{URL: tc.pattern, URLMode: tc.mode}
		if got := m.Match(Request{URL: tc.url}); got != tc.want {
			t.Errorf("%s %q on %q = %v, want %v", tc.mode, tc.pattern, tc.url, got, tc.want)
		}
	}
}

func TestHeaderConditions(t *testing.T) {
	r := Request{URL: "https://x.test/", Header: http.Header{"X-Env": {"prod", "staging-2"}, "Authorization": {"Bearer abc"}}}
	cases := []struct {
		h    HeaderCondition
		want bool
	}{
		{HeaderCondition{Name: "x-env", Op: HeaderEquals, Value: "staging-2"}, true}, // any value, any name case
		{HeaderCondition{Name: "X-Env", Op: HeaderEquals, Value: "staging"}, false},
		{HeaderCondition{Name: "X-Env", Op: HeaderContains, Value: "stag"}, true},
		{HeaderCondition{Name: "Authorization", Op: HeaderMatches, Value: `^Bearer \w+$`}, true},
		{HeaderCondition{Name: "Authorization", Op: HeaderMatches, Value: `^Basic`}, false},
		{HeaderCondition{Name: "Cookie", Op: HeaderPresent}, false},
		{HeaderCondition{Name: "Authorization", Op: HeaderPresent}, true},
	}
	for _, tc := range cases {
		if got := (Matcher{Headers: []HeaderCondition{tc.h}}).Match(r); got != tc.want {
			t.Errorf("%+v = %v, want %v", tc.h, got, tc.want)
		}
	}
	if got := (Matcher{Headers: []HeaderCondition{{Name: "Cookie", Op: HeaderPresent}}}).Mismatch(r); len(got) != 1 || got[0] != "no Cookie header" {
		t.Errorf("Mismatch = %q", got)
	}
}

func TestValidate(t *testing.T) {
	for _, m := range []Matcher{
		{URL: "(", URLMode: URLRegex},
		{URL: "x", URLMode: "glob"},
		{URL: "x", Headers: []HeaderCondition{{Name: "bad name", Op: HeaderPresent}}},
		{URL: "x", Headers: []HeaderCondition{{Name: "X", Op: HeaderMatches, Value: "["}}},
		{URL: "x", Headers: []HeaderCondition{{Name: "X", Op: "starts"}}},
	} {
		if m.Validate() == nil {
			t.Errorf("%+v validated", m)
		}
		if m.Match(Request{URL: "x", Header: http.Header{"X": {"["}}}) && m.URLMode != "" {
			t.Errorf("invalid %+v matched", m)
		}
	}
	if err := (Matcher{URL: "*.x/*", URLMode: URLWildcard, Headers: []HeaderCondition{{Name: "X", Op: HeaderMatches, Value: "a+"}}}).Validate(); err != nil {
		t.Error(err)
	}
}
