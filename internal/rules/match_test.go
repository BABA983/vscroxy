package rules

import "testing"

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
