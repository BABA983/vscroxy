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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Rule maps a name to an address. The name is a host, a host with a leading
// dot for its subdomains only, or a path.Match pattern such as *.example.com.
// Apps limits the rule to those apps; empty means every app.
type Rule struct {
	Name string
	IP   net.IP
	Apps []string
}

// Parse reads "name = address" lines with an optional "@ app1, app2" limiting
// the rule to those apps; a line starting with # is a comment.
func Parse(lines []string) ([]Rule, error) {
	var rules []Rule
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		spec, apps, _ := strings.Cut(line, "@")
		name, address, ok := strings.Cut(spec, "=")
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
		rule := Rule{Name: name, IP: ip}
		for _, app := range strings.Split(apps, ",") {
			if app = strings.TrimSpace(app); app != "" {
				rule.Apps = append(rule.Apps, app)
			}
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// Lookup returns the rule that maps a name for an app, the last rule winning.
// A rule without apps applies to every app, so a name-scoped rule written
// after an app-scoped one overrides it for everyone.
func Lookup(rules []Rule, name, app string) (Rule, bool) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if name == "" {
		return Rule{}, false
	}
	app = strings.ToLower(strings.TrimSpace(app))
	for i := len(rules) - 1; i >= 0; i-- {
		rule := rules[i]
		if !nameMatches(rule.Name, name) {
			continue
		}
		if len(rule.Apps) == 0 {
			return rule, true
		}
		for _, want := range rule.Apps {
			if strings.EqualFold(want, app) || strings.EqualFold(want, base(app)) {
				return rule, true
			}
		}
	}
	return Rule{}, false
}

// Match returns the address a name maps to for every app, the last rule for
// it winning: the DNS server answers these, while app-scoped rules apply to
// the flows their app sends.
func Match(rules []Rule, name string) (net.IP, bool) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if name == "" {
		return nil, false
	}
	for i := len(rules) - 1; i >= 0; i-- {
		rule := rules[i]
		if len(rule.Apps) == 0 && nameMatches(rule.Name, name) {
			return rule.IP, true
		}
	}
	return nil, false
}

// nameMatches reports whether a rule's name covers a host.
func nameMatches(pattern, name string) bool {
	if ok, _ := path.Match(pattern, name); ok {
		return true
	}
	if rest, ok := strings.CutPrefix(pattern, "."); ok {
		return name == rest || strings.HasSuffix(name, pattern)
	}
	if !strings.ContainsAny(pattern, "*?[") && (name == pattern || strings.HasSuffix(name, "."+pattern)) {
		return true
	}
	return false
}

// base is the executable's name in a process path.
func base(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// Hit is a name the server answered from the rules.
type Hit struct {
	Name string
	IP   net.IP
	At   time.Time
}

// Resolution is a name the server resolved: the addresses it answered, and how
// long the answer took.
type Resolution struct {
	Name string
	IPs  []net.IP
	At   time.Time
	Took time.Duration
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

	// pending are the addresses an app-scoped rule gives a name: the server
	// cannot see which app asks, so the flow that matched registers the
	// address for the dial that follows.
	pendingMu sync.Mutex
	pending   map[string]pending

	// lookups are the names the server resolved lately, for the connection
	// panel: the proxy dials through it, so it sees every lookup.
	lookupMu sync.Mutex
	lookups  map[string]Resolution
}

type pending struct {
	ip    net.IP
	until time.Time
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
	s := &Server{udp: udp, upstream: upstream, done: make(chan struct{}), pending: make(map[string]pending), lookups: make(map[string]Resolution)}
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

// SetPending answers name with ip for the next d: an app-scoped rule cannot be
// answered by name alone, so the flow that matched hands the address to the
// dial it is about to make. The server keeps the address for a moment because
// a lookup asks for both families, and lets it expire so a later request from
// another app resolves the name normally.
func (s *Server) SetPending(name string, ip net.IP, d time.Duration) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if name == "" || ip == nil {
		return
	}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	now := time.Now()
	for key, p := range s.pending {
		if !now.Before(p.until) {
			delete(s.pending, key)
		}
	}
	s.pending[name] = pending{ip: ip, until: now.Add(d)}
}

// pendingIP is the address a flow registered for a name, if it has not expired.
func (s *Server) pendingIP(name string) (net.IP, bool) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	p, ok := s.pending[name]
	if !ok || !time.Now().Before(p.until) {
		delete(s.pending, name)
		return nil, false
	}
	return p.ip, true
}

// Resolved is what the server last resolved a name to, so the UI can say which
// address a host went to and how long the answer took.
func (s *Server) Resolved(name string) (Resolution, bool) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	s.lookupMu.Lock()
	defer s.lookupMu.Unlock()
	l, ok := s.lookups[name]
	return l, ok
}

// noteLookup records an answer the server gave: a newer answer replaces the
// addresses of the same family, so both families stay in view.
func (s *Server) noteLookup(name string, ips []net.IP, took time.Duration) {
	if name == "" || len(ips) == 0 {
		return
	}
	s.lookupMu.Lock()
	defer s.lookupMu.Unlock()
	if len(s.lookups) >= 1024 {
		clear(s.lookups)
	}
	entry := s.lookups[name]
	kept := entry.IPs[:0]
	for _, old := range entry.IPs {
		stale := false
		for _, ip := range ips {
			if (ip.To4() != nil) == (old.To4() != nil) {
				stale = true
				break
			}
		}
		if !stale {
			kept = append(kept, old)
		}
	}
	entry.IPs = append(kept, ips...)
	slices.SortStableFunc(entry.IPs, func(a, b net.IP) int {
		switch {
		case a.To4() != nil && b.To4() == nil:
			return -1
		case a.To4() == nil && b.To4() != nil:
			return 1
		}
		return 0
	})
	entry.Name, entry.At, entry.Took = name, time.Now(), took
	s.lookups[name] = entry
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
