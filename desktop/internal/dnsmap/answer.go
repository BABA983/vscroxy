package dnsmap

import (
	"context"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// answer builds the response to a query: the rule's address for the names
// mapped, the system resolver's for the rest, and no addresses for the kinds
// of query this server does not answer.
func (s *Server) answer(query []byte) []byte {
	var p dnsmessage.Parser
	hdr, err := p.Start(query)
	if err != nil {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	name := strings.TrimSuffix(q.Name.String(), ".")

	var ips []net.IP
	switch q.Type {
	case dnsmessage.TypeA, dnsmessage.TypeAAAA:
		if ip, ok := Match(s.Rules(), name); ok {
			s.last.Store(Hit{Name: name, IP: ip, At: time.Now()})
			ips = []net.IP{ip}
		} else if resolved, err := s.lookup(name); err == nil {
			ips = resolved
		}
	}
	return build(hdr, q, ips)
}

// lookup asks the resolver the server started with.
func (s *Server) lookup(name string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.upstream.LookupIP(ctx, "ip", name)
}

// build writes the answers of the query's family.
func build(hdr dnsmessage.Header, q dnsmessage.Question, ips []net.IP) []byte {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID:                 hdr.ID,
		Response:           true,
		RecursionDesired:   hdr.RecursionDesired,
		RecursionAvailable: true,
	})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil
	}
	if err := b.Question(q); err != nil {
		return nil
	}
	if err := b.StartAnswers(); err != nil {
		return nil
	}
	for _, ip := range ips {
		header := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60}
		switch q.Type {
		case dnsmessage.TypeA:
			v4 := ip.To4()
			if v4 == nil {
				continue
			}
			header.Type = dnsmessage.TypeA
			_ = b.AResource(header, dnsmessage.AResource{A: [4]byte{v4[0], v4[1], v4[2], v4[3]}})
		case dnsmessage.TypeAAAA:
			v6 := ip.To16()
			if v6 == nil || ip.To4() != nil {
				continue
			}
			header.Type = dnsmessage.TypeAAAA
			var a [16]byte
			copy(a[:], v6)
			_ = b.AAAAResource(header, dnsmessage.AAAAResource{AAAA: a})
		}
	}
	msg, err := b.Finish()
	if err != nil {
		return nil
	}
	return msg
}
