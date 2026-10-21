// Package dnsmap answers the names the proxy maps to an address, and forwards
// the rest to the system's resolver, so an app's traffic can be pointed at
// another machine without touching the hosts file: the proxy resolves on its
// own machine, and the client keeps the name in the URL and the Host header.
package dnsmap

import (
	"context"
	"fmt"
	"io"
	"net"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Rule maps a name to an address. The name is a host, a host with a leading
// dot for its subdomains only, or a path.Match pattern such as *.example.com.
type Rule struct {
	Name string
	IP   net.IP
}

// Parse reads "name = address" lines; a line starting with # is a comment.
func Parse(lines []string) ([]Rule, error) {
	var rules []Rule
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, address, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%q: want \"name = address\"", line)
		}
		name = strings.ToLower(strings.TrimSpace(name))
		ip := net.ParseIP(strings.TrimSpace(address))
		if name == "" {
			return nil, fmt.Errorf("%q: no name", line)
		}
		if ip == nil {
			return nil, fmt.Errorf("%q: %q is not an address", line, strings.TrimSpace(address))
		}
		rules = append(rules, Rule{Name: name, IP: ip})
	}
	return rules, nil
}

// Match returns the address a name maps to, the last rule for it winning.
func Match(rules []Rule, name string) (net.IP, bool) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if name == "" {
		return nil, false
	}
	for i := len(rules) - 1; i >= 0; i-- {
		rule := rules[i]
		if ok, _ := path.Match(rule.Name, name); ok {
			return rule.IP, true
		}
		if rest, ok := strings.CutPrefix(rule.Name, "."); ok {
			if name == rest || strings.HasSuffix(name, rule.Name) {
				return rule.IP, true
			}
			continue
		}
		if !strings.ContainsAny(rule.Name, "*?[") && (name == rule.Name || strings.HasSuffix(name, "."+rule.Name)) {
			return rule.IP, true
		}
	}
	return nil, false
}

// Hit is a name the server answered from the rules.
type Hit struct {
	Name string
	IP   net.IP
	At   time.Time
}

// Server is a DNS server on the loopback interface that the proxy's dials go
// through: it answers the rules' names and forwards the others to the
// resolver it started with.
type Server struct {
	udp      *net.UDPConn
	tcp      net.Listener
	upstream *net.Resolver
	rules    atomic.Value // []Rule
	last     atomic.Value // Hit
	done     chan struct{}
	stopOnce sync.Once
}

// Start listens on a free loopback port. upstream is the resolver to forward
// the names no rule matches, usually the one net.DefaultResolver was.
func Start(upstream *net.Resolver) (*Server, error) {
	if upstream == nil {
		upstream = net.DefaultResolver
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	s := &Server{udp: udp, upstream: upstream, done: make(chan struct{})}
	s.rules.Store([]Rule(nil))
	if tcp, err := net.Listen("tcp", udp.LocalAddr().String()); err == nil {
		s.tcp = tcp
		go s.serveTCP()
	}
	go s.serveUDP()
	return s, nil
}

// Addr is where the server listens.
func (s *Server) Addr() string { return s.udp.LocalAddr().String() }

// SetRules replaces the names the server answers itself.
func (s *Server) SetRules(rules []Rule) { s.rules.Store(rules) }

// Rules lists the names the server answers.
func (s *Server) Rules() []Rule {
	rules, _ := s.rules.Load().([]Rule)
	return rules
}

// LastMatch is the last name the rules answered, for the UI to show.
func (s *Server) LastMatch() (Hit, bool) {
	m, ok := s.last.Load().(Hit)
	return m, ok
}

// Close stops the server. It may be called more than once.
func (s *Server) Close() error {
	var err error
	s.stopOnce.Do(func() {
		close(s.done)
		if s.tcp != nil {
			_ = s.tcp.Close()
		}
		err = s.udp.Close()
	})
	return err
}

// Install makes the process resolve through the server: every dial the proxy
// makes asks it first.
func (s *Server) Install() {
	addr := s.Addr()
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
}

func (s *Server) serveUDP() {
	buf := make([]byte, 1500)
	for {
		n, from, err := s.udp.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			return
		}
		go func(query []byte, to *net.UDPAddr) {
			if answer := s.answer(query); answer != nil {
				_, _ = s.udp.WriteToUDP(answer, to)
			}
		}(append([]byte(nil), buf[:n]...), from)
	}
}

func (s *Server) serveTCP() {
	for {
		conn, err := s.tcp.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			for {
				var size [2]byte
				if _, err := io.ReadFull(c, size[:]); err != nil {
					return
				}
				n := int(size[0])<<8 | int(size[1])
				query := make([]byte, n)
				if _, err := io.ReadFull(c, query); err != nil {
					return
				}
				answer := s.answer(query)
				if answer == nil {
					return
				}
				_, _ = c.Write([]byte{byte(len(answer) >> 8), byte(len(answer))})
				_, _ = c.Write(answer)
			}
		}(conn)
	}
}
