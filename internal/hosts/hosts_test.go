package hosts

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestParseReadsBothSyntaxesAndReportsBadLines(t *testing.T) {
	text := strings.Join([]string{
		"# Staging",
		"10.0.3.21  api.example.com AUTH.example.com # two hosts",
		"fd00::12\tws.example.com",
		"*.internal.test = 10.0.9.1",
		"10.0.3.30 pay.example.com @ Google Chrome, curl",
		"10.0.3.4O img.example.com",
		"10.0.3.5",
		"a.test b.test = 10.0.0.1",
		"10.0.0.2 c.test @",
		"\r",
	}, "\n")
	got, problems := Parse(text)
	var lines []string
	for _, r := range got {
		lines = append(lines, fmt.Sprintf("%d %s %s %q", r.Line, r.Name, r.IP, r.Apps))
	}
	want := []string{
		`2 api.example.com 10.0.3.21 []`,
		`2 auth.example.com 10.0.3.21 []`,
		`3 ws.example.com fd00::12 []`,
		`4 *.internal.test 10.0.9.1 []`,
		`5 pay.example.com 10.0.3.30 ["Google Chrome" "curl"]`,
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("rules =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	var bad []int
	for _, p := range problems {
		bad = append(bad, p.Line)
	}
	if !slices.Equal(bad, []int{6, 7, 8, 9}) {
		t.Fatalf("problems on lines %v, want 6–9: %+v", bad, problems)
	}
	if problems[0].Message != `"10.0.3.4O" is not an address` {
		t.Fatalf("problem = %q", problems[0].Message)
	}
}

func TestLookupTakesTheFirstRuleForTheApp(t *testing.T) {
	rules, _ := Parse(strings.Join([]string{
		"10.0.0.1 api.test @chrome, Safari",
		"10.0.0.2 api.test",
		"10.0.0.4 api.test",
		"10.0.0.3 *.svc.test",
	}, "\n"))
	cases := []struct{ host, app, want string }{
		{"api.test", "chrome", "10.0.0.1"},
		{"api.test", "/Applications/Safari.app/Contents/MacOS/Safari", "10.0.0.1"},
		{"API.test.", "firefox", "10.0.0.2"}, // the first line for every app, not the last
		{"a.svc.test", "firefox", "10.0.0.3"},
		{"svc.test", "firefox", ""},
		{"nothing.test", "chrome", ""},
	}
	for _, c := range cases {
		r, ok := Lookup(rules, c.host, c.app)
		if got := map[bool]string{true: r.IP.String(), false: ""}[ok]; got != c.want {
			t.Errorf("Lookup(%q, %q) = %q, want %q", c.host, c.app, got, c.want)
		}
	}
	// A DNS server sees names only: the app-scoped first line is not its answer.
	if r, ok := Match(rules, "api.test"); !ok || r.IP.String() != "10.0.0.2" || r.Line != 2 {
		t.Fatalf("Match(api.test) = %+v, %v; want line 2", r, ok)
	}
}

func TestHigherProfilesWinAndConflictsAreReported(t *testing.T) {
	s := Set{Profiles: []Profile{
		{Name: "Staging", On: true, Text: "10.0.3.22 cdn.example.com\n10.0.3.21 api.example.com"},
		{Name: "Local", Text: "127.0.0.1 cdn.example.com"},
		{Name: "Default", On: true, Text: "10.1.0.8 cdn.example.com\n10.0.3.21 api.example.com\n10.1.0.9 cdn.example.com @curl"},
	}}
	r, ok := Lookup(s.Rules(), "cdn.example.com", "Safari")
	if !ok || r.Profile != "Staging" || r.Line != 1 {
		t.Fatalf("cdn = %+v, want Staging line 1", r)
	}
	// Off profiles map nothing and conflict with nothing; the same address
	// twice, or a rule for other apps, is no conflict.
	conflicts := s.Conflicts()
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want one", conflicts)
	}
	if c := conflicts[0]; c.Winner.Profile != "Staging" || c.Loser.Profile != "Default" || c.Loser.Line != 1 || c.Loser.IP.String() != "10.1.0.8" {
		t.Fatalf("conflict = %+v", c)
	}
	if got := s.On(); !slices.Equal(got, []string{"Staging", "Default"}) {
		t.Fatalf("On = %q", got)
	}
}

func TestOneAtATime(t *testing.T) {
	s := Set{Profiles: []Profile{{Name: "A", On: true}, {Name: "B"}, {Name: "C", On: true}}}
	s.SetExclusive(true)
	if got := s.On(); !slices.Equal(got, []string{"A"}) {
		t.Fatalf("after one at a time, on = %q, want the highest", got)
	}
	s.Switch(1, true)
	if got := s.On(); !slices.Equal(got, []string{"B"}) {
		t.Fatalf("switching B on = %q, want only B", got)
	}
	s.SetExclusive(false)
	s.Switch(2, true)
	if got := s.On(); !slices.Equal(got, []string{"B", "C"}) {
		t.Fatalf("combined = %q, want B and C", got)
	}
}

func TestNamesAndTheIndex(t *testing.T) {
	s := Set{Exclusive: true, Profiles: []Profile{{Name: "Staging", On: true, URL: "https://example.com/hosts", Text: "not kept here"}, {Name: "Local"}}}
	for _, bad := range []string{"", " x", "a/b", ".hidden", "STAGING"} {
		if s.CheckName(bad, -1) == nil {
			t.Errorf("CheckName(%q) accepted", bad)
		}
	}
	if err := s.CheckName("staging", 0); err != nil {
		t.Errorf("renaming a profile's case: %v", err)
	}
	data, err := s.MarshalIndex()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "not kept here") {
		t.Fatalf("the index holds a profile's hosts: %s", data)
	}
	got, err := UnmarshalIndex(data)
	if err != nil || !got.Exclusive || len(got.Profiles) != 2 || !got.Profiles[0].On || got.Profiles[0].URL != s.Profiles[0].URL || got.Profiles[1].Name != "Local" {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	if _, err := UnmarshalIndex([]byte(`{"profiles":[{"name":"a"},{"name":"A"}]}`)); err == nil {
		t.Fatal("an index naming one profile twice was accepted")
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hosts" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "10.0.0.1 team.test\n")
	}))
	defer srv.Close()
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1") // never used
	if text, err := Fetch(t.Context(), srv.URL+"/hosts"); err != nil || text != "10.0.0.1 team.test\n" {
		t.Fatalf("Fetch = %q, %v", text, err)
	}
	if _, err := Fetch(t.Context(), srv.URL+"/missing"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing file: %v", err)
	}
	if _, err := Fetch(t.Context(), "file:///etc/hosts"); err == nil {
		t.Fatal("a file URL was fetched")
	}
}
