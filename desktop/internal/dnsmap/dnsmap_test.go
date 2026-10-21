package dnsmap

import (
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestParse(t *testing.T) {
	rules, err := Parse([]string{"api.example.com = 10.0.0.5", "  # a comment", "", "*.internal.test = 192.168.1.2", "dev.test = 10.0.0.7 @Chrome, /usr/bin/firefox"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rules) != 3 || rules[0].Name != "api.example.com" || rules[1].IP.String() != "192.168.1.2" {
		t.Fatalf("rules = %+v", rules)
	}
	if apps := rules[2].Apps; len(apps) != 2 || apps[0] != "Chrome" || apps[1] != "/usr/bin/firefox" {
		t.Fatalf("apps = %q, want the two listed", apps)
	}
	if len(rules[0].Apps) != 0 {
		t.Fatalf("a rule without @ has apps %q", rules[0].Apps)
	}
	if _, err := Parse([]string{"no address here"}); err == nil {
		t.Error("a line without = was accepted")
	}
	if _, err := Parse([]string{"name = not-an-address"}); err == nil {
		t.Error("a line without an address was accepted")
	}
}

func TestLookupLimitsARuleToItsApps(t *testing.T) {
	rules, err := Parse([]string{
		"api.test = 10.0.0.1 @chrome, Safari",
		"api.test = 10.0.0.2 @other",
		"*.svc.test = 10.0.0.3",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := []struct {
		name, app, want string
	}{
		{"api.test", "chrome", "10.0.0.1"},
		{"api.test", "Safari", "10.0.0.1"},
		{"api.test", "other", "10.0.0.2"},
		{"api.test", "firefox", ""},                                                // no rule for this app
		{"api.test", "/Applications/Safari.app/Contents/MacOS/Safari", "10.0.0.1"}, // the executable's name counts
		{"a.svc.test", "firefox", "10.0.0.3"},                                      // a rule without apps applies to every app
		{"nothing.test", "chrome", ""},
	}
	for _, c := range cases {
		rule, ok := Lookup(rules, c.name, c.app)
		switch {
		case c.want == "" && ok:
			t.Errorf("Lookup(%q, %q) = %v, want none", c.name, c.app, rule.IP)
		case c.want != "" && (!ok || rule.IP.String() != c.want):
			t.Errorf("Lookup(%q, %q) = %v, %v; want %s", c.name, c.app, rule.IP, ok, c.want)
		}
	}
	// The server answers names, not apps: an app-scoped rule is not one of
	// the names it answers for everyone.
	if ip, ok := Match(rules, "api.test"); ok {
		t.Errorf("Match(api.test) = %v, want none", ip)
	}
}

func TestMatch(t *testing.T) {
	rules, err := Parse([]string{
		"example.com = 10.0.0.1",
		"*.internal.test = 10.0.0.2",
		".corp.test = 10.0.0.3",
		"example.com = 10.0.0.9",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := []struct {
		name string
		want string
	}{
		{"example.com", "10.0.0.9"}, // the last rule wins
		{"api.example.com", "10.0.0.9"},
		{"one.internal.test", "10.0.0.2"},
		{"corp.test", "10.0.0.3"},
		{"deep.corp.test", "10.0.0.3"},
		{"other.org", ""},
	}
	for _, c := range cases {
		ip, ok := Match(rules, c.name)
		switch {
		case c.want == "" && ok:
			t.Errorf("Match(%q) = %v, want none", c.name, ip)
		case c.want != "" && (!ok || ip.String() != c.want):
			t.Errorf("Match(%q) = %v, %v; want %s", c.name, ip, ok, c.want)
		}
	}
}

func TestServerAnswersTheRules(t *testing.T) {
	s, err := Start(net.DefaultResolver)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()
	rules, err := Parse([]string{"mapped.test = 10.9.8.7"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s.SetRules(rules)

	if ip := ask(t, s.Addr(), "mapped.test", dnsmessage.TypeA); ip != "10.9.8.7" {
		t.Fatalf("mapped.test = %q, want the rule's address", ip)
	}
	if hit, ok := s.LastMatch(); !ok || hit.Name != "mapped.test" || hit.IP.String() != "10.9.8.7" {
		t.Fatalf("last match = %+v, %v", hit, ok)
	}
	// A name no rule matches goes to the system resolver, which knows
	// localhost from the hosts file.
	if ip := ask(t, s.Addr(), "localhost", dnsmessage.TypeA); ip != "127.0.0.1" {
		t.Fatalf("localhost = %q, want the system resolver's", ip)
	}
}

// TestServerAnswersAPendingAddress covers the app-scoped rules: the flow that
// matched hands the address to the dial that follows, because the server only
// sees the name.
func TestServerAnswersAPendingAddress(t *testing.T) {
	s, err := Start(net.DefaultResolver)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()
	rules, err := Parse([]string{"mapped.test = 10.9.8.7 @chrome"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s.SetRules(rules)
	// The name alone is not answered: the rule belongs to an app.
	if ip := ask(t, s.Addr(), "mapped.test", dnsmessage.TypeA); ip != "" {
		t.Fatalf("mapped.test = %q, want no answer before a flow registers it", ip)
	}
	s.SetPending("mapped.test", net.ParseIP("10.9.8.7"), time.Minute)
	if ip := ask(t, s.Addr(), "mapped.test", dnsmessage.TypeA); ip != "10.9.8.7" {
		t.Fatalf("mapped.test = %q, want the pending address", ip)
	}
	// The address is kept for the second family of the same lookup.
	if ip := ask(t, s.Addr(), "mapped.test", dnsmessage.TypeA); ip != "10.9.8.7" {
		t.Fatalf("mapped.test = %q, want the pending address again", ip)
	}
	if hit, ok := s.LastMatch(); !ok || hit.Name != "mapped.test" || hit.IP.String() != "10.9.8.7" {
		t.Fatalf("last match = %+v, %v", hit, ok)
	}
	// An expired address does not outlive the dial it was registered for.
	s.SetPending("expired.test", net.ParseIP("10.9.8.8"), -time.Second)
	if ip := ask(t, s.Addr(), "expired.test", dnsmessage.TypeA); ip != "" {
		t.Fatalf("expired.test = %q, want no answer", ip)
	}
}

// ask sends one question to addr and returns the first address of the answer.
func ask(t *testing.T, addr, name string, kind dnsmessage.Type) string {
	t.Helper()
	q := dnsmessage.NewBuilder(nil, dnsmessage.Header{RecursionDesired: true})
	q.EnableCompression()
	if err := q.StartQuestions(); err != nil {
		t.Fatalf("questions: %v", err)
	}
	if err := q.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name + "."), Type: kind, Class: dnsmessage.ClassINET}); err != nil {
		t.Fatalf("question: %v", err)
	}
	msg, err := q.Finish()
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var p dnsmessage.Parser
	if _, err := p.Start(buf[:n]); err != nil {
		t.Fatalf("parse answer: %v", err)
	}
	if _, err := p.Question(); err != nil {
		t.Fatalf("parse question: %v", err)
	}
	if err := p.SkipAllQuestions(); err != nil {
		t.Fatalf("skip questions: %v", err)
	}
	for {
		header, err := p.AnswerHeader()
		if err != nil {
			return ""
		}
		switch header.Type {
		case dnsmessage.TypeA:
			a, err := p.AResource()
			if err != nil {
				t.Fatalf("A: %v", err)
			}
			return net.IP(a.A[:]).String()
		case dnsmessage.TypeAAAA:
			if _, err := p.AAAAResource(); err != nil {
				t.Fatalf("AAAA: %v", err)
			}
		default:
			if err := p.SkipAnswer(); err != nil {
				t.Fatalf("skip: %v", err)
			}
		}
	}
}
