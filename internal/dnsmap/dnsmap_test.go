package dnsmap

import (
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/baba983/vscroxy/internal/hosts"
)

func TestServerAnswersTheRules(t *testing.T) {
	s, err := Start(net.DefaultResolver)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()
	rules, _ := hosts.Parse("10.9.8.7 mapped.test")
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
	if resolved, ok := s.Resolved("localhost"); !ok || len(resolved.IPs) == 0 || resolved.IPs[0].String() != "127.0.0.1" {
		t.Fatalf("resolved localhost = %+v, %v; want the addresses it answered", resolved, ok)
	}
	if resolved, ok := s.Resolved("mapped.test"); !ok || resolved.IPs[0].String() != "10.9.8.7" {
		t.Fatalf("resolved mapped.test = %+v, %v; want the rule's address", resolved, ok)
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
	rules, _ := hosts.Parse("10.9.8.7 mapped.test @chrome")
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
