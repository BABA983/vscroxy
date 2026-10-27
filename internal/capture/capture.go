// Package capture runs the MITM proxy and turns what it sees into Flows for
// the UI. The engine is go-mitmproxy; this package owns the boundary, so the
// UI never imports the engine.
package capture

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lqqyt2423/go-mitmproxy/proxy"

	"github.com/baba983/vscroxy/internal/dnsmap"
	"github.com/baba983/vscroxy/internal/procinfo"
)

const (
	// maxBody bounds what one flow keeps in memory. A larger body is written
	// to the spill directory and the flow keeps only its head, so a capture
	// with many large responses does not grow without limit.
	maxBody = 64 << 10
	// maxRuleBody bounds what a map local rule serves from a file.
	maxRuleBody = 8 << 20
	// maxScriptBody bounds what a response script reads.
	maxScriptBody = 1 << 20
	// maxFrames bounds the messages kept for one WebSocket or SSE flow, and
	// maxFrameBytes what one message keeps in memory.
	maxFrames     = 200
	maxFrameBytes = 4 << 10
	// maxFrameSpill bounds what one message keeps on disk: a larger message
	// is written out up to this many bytes and reports the rest as cut.
	maxFrameSpill = 256 << 10
	// queue bounds the flows waiting for the UI. A burst past it is dropped
	// and counted instead of growing without limit.
	queue = 1024
)

// hopHeaders are per-connection headers: they belong to the connection a
// message came on, so a replay must not send them again.
var hopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// Flow is one captured exchange: what the UI shows and what a replay
// rebuilds. It is the boundary type of the capture core.
type Flow struct {
	ID int
	// ReplayOf identifies the original capture; zero for ordinary traffic.
	ReplayOf int
	// DebugRun correlates a local editor send with its exact wire exchanges.
	// It travels through socket ownership, never through an HTTP header.
	DebugRun string
	// Actions records interventions applied to this exchange, not today's rules.
	Actions []string
	Method  string
	Scheme  string
	Host    string
	Path    string
	URL     string
	Status  int
	Bytes   int
	Millis  int

	RequestHeaders  string
	ResponseHeaders string

	// RequestBody and ResponseBody are the whole body, or its head when the
	// body was spilled: then the file holds all of it and Size its length.
	RequestBody      string
	RequestBodyFile  string
	RequestBodySize  int
	ResponseBody     string
	ResponseBodyFile string
	ResponseBodySize int

	// RequestBodyError and ResponseBodyError say why a body is not the whole
	// one the exchange carried: the transfer stopped before its end, the body
	// could not be kept, or its encoding is not one the capture decodes. Empty
	// means the body beside it is complete.
	RequestBodyError  string
	ResponseBodyError string

	// At is when the exchange finished; ResponseHeader the structured
	// response headers, for an export.
	At             time.Time
	ResponseHeader http.Header

	// RequestHeader is the structured request headers, for a replay.
	RequestHeader http.Header

	// RequestTrailer and ResponseTrailer are the trailer blocks the
	// exchange ended with, kept apart from the headers: a gRPC status and
	// its binary details live there, and a trailer may repeat a name the
	// headers also carried, with other values. ResponseProto is the HTTP
	// version the server answered with, such as "HTTP/2.0"; empty when the
	// response never came from a server.
	RequestTrailer  http.Header
	ResponseTrailer http.Header
	ResponseProto   string

	// Kind is "http", "ws" or "sse"; Frames carries the messages of the
	// last two.
	Kind   string
	Frames []Frame

	// Process is the app the connection came from, when it could be told,
	// and ProcessPath its executable's path.
	Process     string
	ProcessPath string

	// DNSMapped says what a DNS mapping did to the dial, as
	// "name → address"; empty when none did.
	DNSMapped string

	// Conn is what the exchange ran over: the addresses at both ends, the
	// HTTP version, the addresses the name resolved to, and the TLS the
	// upstream negotiated.
	Conn ConnInfo

	// Wait is how long the request took to reach its first response byte,
	// and Download the rest of Millis: the two phases a waterfall shows.
	// Both are zero when the exchange never reached a server.
	Wait     int
	Download int
}

// FrameEvent is a frame that arrived after its flow was published. A frame the
// UI was too slow to take is not sent at all: the next one's Seq shows the gap.
type FrameEvent struct {
	FlowID int
	Frame  Frame
}

// Frame is one WebSocket message or SSE event of a flow. The whole message is
// kept: a large one is written to the spill directory and only its head stays
// in memory, so a stream of long messages does not grow without limit.
type Frame struct {
	// Seq numbers the messages of a flow from 1, so a gap says how many the
	// capture or the UI could not keep.
	Seq       int
	Direction string // "out" for client to server, "in" for server to client
	// Kind is "text", "binary", "close", "ping" or "pong" for a WebSocket
	// message, and "event" for an SSE event, whose EventName says which one.
	Kind string
	// Data is the message, or its head when File holds the rest.
	Data string
	// File holds the message, or its first Kept bytes when it was larger
	// than the spill cap. It is empty for a message kept whole inline.
	File string
	// Size is the message's length in bytes, and Kept how much of it is here:
	// Kept below Size means the message was cut, and by how much.
	Size int
	Kept int
	At   time.Time
	// EventID and EventName are the SSE "id" and "event" fields; the engine
	// names an event that had none "message".
	EventID   string
	EventName string
}

// RuleType is what a rule does to a request that matches it.
type RuleType string

const (
	// RuleMapRemote sends the request to another host or URL.
	RuleMapRemote RuleType = "map_remote"
	// RuleMapLocal answers the request from a file, without an upstream.
	RuleMapLocal RuleType = "map_local"
	// RuleBlock answers the request with a refusal.
	RuleBlock RuleType = "block"
	// RuleBreak pauses a matching request until the UI decides what to do.
	RuleBreak RuleType = "break"
	// RuleBreakResponse pauses a matching response until the UI decides.
	RuleBreakResponse RuleType = "break_response"
)

// Rule rewrites requests whose URL contains Match. Mapping/block rules use
// first-match order; breakpoints match independently at their own phase.
// An empty Method matches all methods. Old files remain enabled by default.
type Rule struct {
	Name     string      `json:"name,omitempty"`
	Type     RuleType    `json:"type"`
	Match    string      `json:"match"`
	Method   string      `json:"method,omitempty"`
	Target   string      `json:"target,omitempty"`
	Status   int         `json:"status,omitzero"`
	Headers  http.Header `json:"headers,omitempty"`
	Disabled bool        `json:"disabled,omitzero"`
	// Scenario groups response-affecting rules; empty is always global.
	// Every member of a group has the same ScenarioActive value, and at most
	// one group is active. Disabled remains an independent per-rule choice.
	Scenario       string `json:"scenario,omitempty"`
	ScenarioActive bool   `json:"scenarioActive,omitzero"`
}

// Phase is where a breakpoint paused an exchange.
type Phase int

const (
	PhaseRequest Phase = iota
	PhaseResponse
)

// Breakpoint is an exchange paused for the UI to edit.
type Breakpoint struct {
	ID           int
	Phase        Phase
	Method       string
	URL          string
	Status       int
	Body         string
	Header       http.Header
	BodyReadOnly bool // binary, undecodable or too large for the text editor
}

// Decision is what the UI does with a paused exchange.
type Decision struct {
	Abort       bool
	AbortStatus int  // 502 by default; the UI also offers 429 and 503
	Original    bool // discard edits and preserve the message at the breakpoint
	Method      string
	URL         string
	Status      int
	Body        string
	Header      http.Header // nil preserves headers; an empty map removes them
	KeepBody    bool        // header-only edit of a body the text editor cannot represent
}

// Server is the running MITM proxy. Start it once; Flows drains what it
// captures.
type Server struct {
	proxy   *proxy.Proxy
	addr    string
	out     chan Flow
	dropped atomic.Int64
	nextID  atomic.Int64

	listenMu sync.Mutex // owns addr and listener across Start/Close
	listener net.Listener
	autoPort bool
	running  atomic.Bool

	mu            sync.Mutex
	started       map[uuid.UUID]time.Time
	firstByte     map[uuid.UUID]time.Time
	original      map[uuid.UUID]url.URL
	clients       map[clientSocket]clientIdentity // active socket -> immutable ownership
	replayClients map[string]replayOrigin         // replay socket address -> local send
	closeOnce     sync.Once

	// bodyDir holds the bodies too large to keep in memory. It is removed
	// when the server closes.
	bodyDir string

	// streams holds the bodies the engine streams past its buffered path,
	// until their flow is published or done.
	streams map[uuid.UUID]*streamBodies

	// trailers holds the trailer block each side of a flow ended with,
	// until the flow is published.
	trailers map[uuid.UUID]*flowTrailers

	// closed is closed when the server does, so a watcher waiting on a flow
	// never outlives it; sendMu and sendClosed keep a publish off a closed
	// Flows.
	closed     chan struct{}
	sendMu     sync.RWMutex
	sendClosed bool

	// dns answers the names mapped to an address and forwards the rest, so
	// the proxy dials a mapping without changing the URL or Host header.
	dns *dnsmap.Server

	// dnsHits are the flows whose target a mapping changed, for the table.
	lastDNS atomic.Value // DNSHit
	dnsHits map[uuid.UUID]DNSHit

	// roots are the CAs a replay trusts: the system's and this proxy's, so
	// a replay of an HTTPS flow succeeds without trusting the CA machine
	// wide.
	roots *x509.CertPool

	rulesMu sync.RWMutex
	rules   []Rule

	frames  chan FrameEvent
	flowIDs map[uuid.UUID]int
	// frameSeq numbers the messages of each flow, and frameDrops counts the
	// ones a full queue took.
	frameSeq   map[uuid.UUID]int
	frameDrops atomic.Int64
	// Retain completion evidence beyond the UI's 5000-flow window, without
	// putting terminal updates into the lossy message queue. IDs are monotonic.
	streamEnds [8192]struct {
		id  int
		end StreamEnd
	}

	scriptMu     sync.RWMutex
	script       *Script
	scriptErrors atomic.Int64

	throttleMu sync.RWMutex
	throttle   Throttle

	upstream   atomic.Value // string
	ssl        atomic.Value // SSLConfig
	ignoreList atomic.Value // []string
	ignoreApps atomic.Value // []string

	breakOn     atomic.Bool
	noCaching   atomic.Bool
	bpNext      atomic.Int64
	bpMu        sync.Mutex
	bpWait      map[int]chan Decision
	breakpoints chan Breakpoint
}

// New builds a proxy to listen on addr, with its CA under caRootPath, or in
// ~/.mitmproxy when that is empty as mitmproxy does. An empty addr prefers
// 127.0.0.1:7890 and falls back to an OS-assigned port if it cannot be bound.
// An explicit addr is used as-is; port 0 requests an OS-assigned port directly.
func New(addr, caRootPath string) (*Server, error) {
	autoPort := addr == ""
	if addr == "" {
		addr = "127.0.0.1:7890"
	}
	s := &Server{
		addr:          addr,
		autoPort:      autoPort,
		out:           make(chan Flow, queue),
		started:       make(map[uuid.UUID]time.Time),
		firstByte:     make(map[uuid.UUID]time.Time),
		original:      make(map[uuid.UUID]url.URL),
		clients:       make(map[clientSocket]clientIdentity),
		replayClients: make(map[string]replayOrigin),
		dnsHits:       make(map[uuid.UUID]DNSHit),
		bpWait:        make(map[int]chan Decision),
		breakpoints:   make(chan Breakpoint, 16),
		frames:        make(chan FrameEvent, queue),
		flowIDs:       make(map[uuid.UUID]int),
		frameSeq:      make(map[uuid.UUID]int),
		streams:       make(map[uuid.UUID]*streamBodies),
		trailers:      make(map[uuid.UUID]*flowTrailers),
		closed:        make(chan struct{}),
	}
	p, err := proxy.NewProxy(&proxy.Options{
		Addr:              addr,
		CaRootPath:        caRootPath,
		SslInsecure:       true, // a debugging proxy inspects self-signed upstreams too
		StreamLargeBodies: 8 << 20,
	})
	if err != nil {
		return nil, err
	}
	p.AddAddon(&addon{server: s})
	p.SetShouldInterceptRule(func(req *http.Request) bool {
		// RemoteAddr is supplied by the proxy's HTTP server, never a header.
		// Ownership was resolved from both ends of this accepted socket.
		var process procinfo.Process
		if local, ok := req.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr); ok {
			addr := clientSocket{req.RemoteAddr, local.String()}
			s.mu.Lock()
			client := s.clients[addr].client
			s.mu.Unlock()
			// net/http retains this accepted TCP socket's LocalAddr object.
			// Equal address text alone could refer to a recycled connection.
			if client != nil && client.Conn.LocalAddr() == local {
				process = s.socketProcess(addr, client)
			}
		}
		return s.SSL().DecryptsProcess(requestHost(req), process.Path)
	})
	p.SetUpstreamProxy(func(*http.Request) (*url.URL, error) {
		raw, _ := s.upstream.Load().(string)
		if raw == "" {
			return nil, nil
		}
		return url.Parse(raw)
	})
	s.proxy = p
	if pool, err := x509.SystemCertPool(); err == nil {
		ca := p.GetCertificate()
		pool.AddCert(&ca)
		s.roots = pool
	}
	if dir, err := os.MkdirTemp("", "vscroxy-bodies-"); err == nil {
		s.bodyDir = dir
	}
	if resolver, err := dnsmap.Start(net.DefaultResolver); err == nil {
		s.dns = resolver
		s.dns.Install()
	}
	return s, nil
}

// Addr is the requested address before Start and the actual bound address after.
func (s *Server) Addr() string {
	s.listenMu.Lock()
	defer s.listenMu.Unlock()
	return s.addr
}

// Running reports whether this server owns an active proxy listener.
func (s *Server) Running() bool { return s.running.Load() }

// CACertificate returns only the public CA certificate, in DER form. It never
// reads a CA bundle or exposes the signing key.
func (s *Server) CACertificate() []byte {
	ca := s.proxy.GetCertificate()
	return bytes.Clone(ca.Raw)
}

// Flows is the stream of captured exchanges. It closes when the server does.
func (s *Server) Flows() <-chan Flow { return s.out }

// Dropped counts the flows the UI could not keep up with.
func (s *Server) Dropped() int64 { return s.dropped.Load() }

// SSLApp selects an executable for HTTPS inspection. Name is display-only;
// only an exact, nonempty Path can authorize inspection.
type SSLApp struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// SSLConfig says which domains or local executables the proxy decrypts.
// Nothing is decrypted until a domain/app is listed, or All is set.
type SSLConfig struct {
	All   bool     `json:"all"`
	Hosts []string `json:"hosts,omitempty"`
	Apps  []SSLApp `json:"apps,omitempty"`
}

// Decrypts reports whether the current rules enable HTTPS inspection for host.
// Existing connections keep the rules they were opened with.
func (c SSLConfig) Decrypts(host string) bool {
	if c.All {
		return true
	}
	host = sslHostname(host)
	for _, pattern := range c.Hosts {
		if hostMatches(host, []string{sslHostname(pattern)}) {
			return true
		}
	}
	return false
}

// DecryptsProcess adds opt-in executable rules to the host/global rules.
// Names, parent apps, path prefixes and unidentified clients never match.
func (c SSLConfig) DecryptsProcess(host, path string) bool {
	if c.Decrypts(host) {
		return true
	}
	return path != "" && slices.ContainsFunc(c.Apps, func(app SSLApp) bool {
		return app.Path != "" && app.Path == path
	})
}

// SSL rules describe domains, not ports. Accept authorities written by older
// versions of the UI as well as bare hostnames and bracketed IPv6 literals.
func sslHostname(host string) string {
	host = strings.TrimSpace(host)
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	return strings.TrimSuffix(host, ".")
}

// SetDNS sets the names the proxy dials elsewhere, as "name = address" lines
// with an optional "@ app1, app2" limiting a rule to those apps: the request
// keeps its name, URL and Host header, and the proxy connects to the address,
// so a device's traffic reaches another machine as if the name pointed there.
func (s *Server) SetDNS(lines []string) error {
	rules, err := dnsmap.Parse(lines)
	if err != nil {
		return err
	}
	if s.dns == nil {
		return errors.New("the DNS server did not start")
	}
	s.dns.SetRules(rules)
	return nil
}

// DNSRules lists the mappings the proxy dials.
func (s *Server) DNSRules() []dnsmap.Rule {
	if s.dns == nil {
		return nil
	}
	return s.dns.Rules()
}

// DNSHit is a flow whose target the DNS mappings changed.
type DNSHit struct {
	Name string
	IP   net.IP
	At   time.Time
}

// LastDNSMatch is the last flow a mapping changed, for the UI to show.
func (s *Server) LastDNSMatch() (DNSHit, bool) {
	hit, ok := s.lastDNS.Load().(DNSHit)
	return hit, ok
}

// noteDNS records the mapping a flow's dial goes through, for the app the flow
// came from, and hands an app-scoped rule's address to the dial that follows:
// the server answers a name, not an app, so the flow scopes it.
func (s *Server) noteDNS(f *proxy.Flow) {
	if s.dns == nil || f.Request == nil || f.Request.URL == nil {
		return
	}
	host := f.Request.URL.Hostname()
	if host == "" {
		return
	}
	rule, ok := dnsmap.Lookup(s.dns.Rules(), host, s.clientProcess(f).Name)
	if !ok {
		return
	}
	hit := DNSHit{Name: host, IP: rule.IP, At: time.Now()}
	s.mu.Lock()
	s.dnsHits[f.Id] = hit
	s.mu.Unlock()
	s.lastDNS.Store(hit)
	if len(rule.Apps) > 0 {
		s.dns.SetPending(host, rule.IP, 2*time.Second)
	}
}

// SetSSL sets what the proxy decrypts.
func (s *Server) SetSSL(c SSLConfig) {
	c.Hosts = slices.Clone(c.Hosts)
	c.Apps = slices.Clone(c.Apps)
	s.ssl.Store(c)
}

// SSL is what the proxy decrypts.
func (s *Server) SSL() SSLConfig {
	c, _ := s.ssl.Load().(SSLConfig)
	c.Hosts = slices.Clone(c.Hosts)
	c.Apps = slices.Clone(c.Apps)
	return c
}

// LoadSSL reads the SSL proxying settings; a missing file decrypts nothing.
func LoadSSL(path string) (SSLConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return SSLConfig{}, nil
	}
	if err != nil {
		return SSLConfig{}, err
	}
	var c SSLConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return SSLConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// SetIgnore sets the hosts whose flows the table does not show, for traffic
// that is noise: they are still proxied.
func (s *Server) SetIgnore(patterns []string) {
	s.ignoreList.Store(slices.Clone(patterns))
}

// Ignore lists the hosts whose flows are not shown.
func (s *Server) Ignore() []string {
	patterns, _ := s.ignoreList.Load().([]string)
	return patterns
}

// ignored reports whether host is one whose flows are dropped.
func (s *Server) ignored(host string) bool {
	return hostMatches(host, s.Ignore())
}

// SetIgnoreApps sets the apps whose flows the table does not show, for a
// browser's background traffic.
func (s *Server) SetIgnoreApps(apps []string) {
	s.ignoreApps.Store(slices.Clone(apps))
}

// IgnoreApps lists the apps whose flows are not shown.
func (s *Server) IgnoreApps() []string {
	apps, _ := s.ignoreApps.Load().([]string)
	return apps
}

// ignoredApp reports whether the flow of an app is dropped.
func (s *Server) ignoredApp(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	for _, app := range s.IgnoreApps() {
		app = strings.ToLower(strings.TrimSpace(app))
		if app == "" || strings.HasPrefix(app, "#") {
			continue
		}
		if ok, _ := path.Match(app, name); ok {
			return true
		}
	}
	return false
}

// hostMatches reports whether host matches one of the patterns: a host with
// its subdomains, a leading dot for subdomains only, or a path.Match glob.
func hostMatches(host string, patterns []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		if ok, _ := path.Match(pattern, host); ok {
			return true
		}
		if rest, ok := strings.CutPrefix(pattern, "."); ok {
			if host == rest || strings.HasSuffix(host, pattern) {
				return true
			}
			continue
		}
		if !strings.ContainsAny(pattern, "*?[") && (host == pattern || strings.HasSuffix(host, "."+pattern)) {
			return true
		}
	}
	return false
}

// LoadIgnore reads the hosts to keep out of the table, one per line.
func LoadIgnore(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var patterns []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns, nil
}

// requestHost is the host a request goes to, without its port.
func requestHost(req *http.Request) string {
	if req == nil {
		return ""
	}
	if host := req.URL.Hostname(); host != "" {
		return host
	}
	host, _, err := net.SplitHostPort(req.Host)
	if err != nil {
		return req.Host
	}
	return host
}

// SetRules replaces the rules the proxy applies to new requests. Invalid
// snapshots leave the running configuration unchanged, just as SaveRules does.
func (s *Server) SetRules(rules []Rule) error {
	if err := ValidateRules(rules); err != nil {
		return err
	}
	s.rulesMu.Lock()
	s.rules = cloneRules(rules)
	s.rulesMu.Unlock()
	return nil
}

// Rules returns the rules in force.
func (s *Server) Rules() []Rule {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	return cloneRules(s.rules)
}

// SetScript replaces the script the proxy runs. A nil script runs nothing.
func (s *Server) SetScript(script *Script) {
	s.scriptMu.Lock()
	s.script = script
	s.scriptMu.Unlock()
}

func (s *Server) currentScript() *Script {
	s.scriptMu.RLock()
	defer s.scriptMu.RUnlock()
	return s.script
}

// SetUpstream sends every request through another proxy, as an empty string
// turns that off. The address needs a scheme and a host, such as
// http://127.0.0.1:8080.
func (s *Server) SetUpstream(raw string) error {
	if raw == "" {
		s.upstream.Store("")
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("upstream %q needs a scheme and a host", raw)
	}
	s.upstream.Store(u.String())
	return nil
}

// Upstream is the proxy every request goes through, or empty.
func (s *Server) Upstream() string {
	raw, _ := s.upstream.Load().(string)
	return raw
}

// ScriptErrors counts the hooks that failed or timed out.
func (s *Server) ScriptErrors() int64 { return s.scriptErrors.Load() }

// SetBreak turns breakpoints on or off. With them off, no request pauses.
func (s *Server) SetBreak(on bool) { s.breakOn.Store(on) }

// Breaking reports whether breakpoints are on.
func (s *Server) Breaking() bool { return s.breakOn.Load() }

// Frames is the stream of WebSocket messages and SSE events, by flow.
func (s *Server) Frames() <-chan FrameEvent { return s.frames }

// FrameDropped counts the messages a full queue took, which the sequence
// numbers of the frames that did arrive also show as gaps.
func (s *Server) FrameDropped() int64 { return s.frameDrops.Load() }

// Breakpoints is the stream of paused exchanges waiting for a decision.
func (s *Server) Breakpoints() <-chan Breakpoint { return s.breakpoints }

// Resume answers a paused exchange. It reports false when the breakpoint is
// no longer waiting, as after a timeout.
func (s *Server) Resume(id int, d Decision) bool {
	s.bpMu.Lock()
	ch, ok := s.bpWait[id]
	delete(s.bpWait, id)
	s.bpMu.Unlock()
	if !ok {
		return false
	}
	ch <- d
	return true
}

// maybeBreak pauses the flow when breakpoints are on and a break rule matches,
// and applies what the UI decided. It returns the decision, or nil when the
// exchange should go on unchanged.
func (s *Server) maybeBreak(f *proxy.Flow, phase Phase) *Decision {
	if !s.breakOn.Load() || f.Request == nil {
		return nil
	}
	raw := f.Request.URL.String()
	if !s.matchesBreak(raw, f.Request.Method, phase) {
		return nil
	}
	id := int(s.bpNext.Add(1))
	ch := make(chan Decision, 1)
	s.bpMu.Lock()
	s.bpWait[id] = ch
	s.bpMu.Unlock()

	bp := Breakpoint{
		ID:     id,
		Phase:  phase,
		Method: f.Request.Method,
		URL:    raw,
	}
	if phase == PhaseResponse && f.Response != nil {
		bp.Status = f.Response.StatusCode
		bp.Header = f.Response.Header.Clone()
	} else {
		bp.Header = f.Request.Header.Clone()
	}
	body, err := f.Request.DecodedBody()
	if phase == PhaseResponse && f.Response != nil {
		body, err = f.Response.DecodedBody()
	}
	bp.BodyReadOnly = err != nil || len(body) > maxScriptBody || !utf8.Valid(body) || bytes.ContainsRune(body, 0)
	if !bp.BodyReadOnly {
		bp.Body = string(body)
	}
	select {
	case s.breakpoints <- bp:
	default:
		s.bpMu.Lock()
		delete(s.bpWait, id)
		s.bpMu.Unlock()
		return nil
	}
	pausedAt := time.Now()
	var d *Decision
	select {
	case decided := <-ch:
		if !decided.Original {
			d = &decided
		}
	case <-s.closed:
	case <-time.After(2 * time.Minute):
	}
	s.bpMu.Lock()
	delete(s.bpWait, id)
	s.bpMu.Unlock()
	// The user's time at the breakpoint is not network time: move the start
	// forward so the flow's duration stays the exchange's.
	s.mu.Lock()
	if start, ok := s.started[f.Id]; ok {
		s.started[f.Id] = start.Add(time.Since(pausedAt))
	}
	s.mu.Unlock()
	return d
}

func (s *Server) matchesBreak(raw, method string, phase Phase) bool {
	want := RuleBreak
	if phase == PhaseResponse {
		want = RuleBreakResponse
	}
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	for _, r := range s.rules {
		if r.Type == want && r.matches(raw, method) {
			return true
		}
	}
	return false
}

// willBreakResponse reports whether a response for this flow will pause, so
// the Response hook leaves publishing to the stream modifier.
func (s *Server) willBreakResponse(f *proxy.Flow) bool {
	return s.breakOn.Load() && f.Request != nil && s.matchesBreak(f.Request.URL.String(), f.Request.Method, PhaseResponse)
}

// LoadRules reads a rules file. A missing file is no rules, not an error.
func LoadRules(path string) ([]Rule, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rules []Rule
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := ValidateRules(rules); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rules, nil
}

// Start binds its own listener synchronously, then serves it in the background.
// It never mistakes another application's listener for this proxy.
func (s *Server) Start() error {
	s.listenMu.Lock()
	defer s.listenMu.Unlock()
	select {
	case <-s.closed:
		return net.ErrClosed
	default:
	}
	if s.listener != nil {
		return errors.New("proxy already started")
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil && s.autoPort {
		var fallbackErr error
		ln, fallbackErr = net.Listen("tcp", "127.0.0.1:0")
		if fallbackErr != nil {
			return fmt.Errorf("preferred proxy address: %w; automatic port: %w", err, fallbackErr)
		}
	} else if err != nil {
		return err
	}
	s.listener, s.addr = ln, ln.Addr().String()
	s.running.Store(true)
	go func() {
		_ = s.proxy.Serve(ln)
		s.running.Store(false)
	}()
	return nil
}

// Close stops the proxy and closes Flows.
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.listenMu.Lock()
		close(s.closed)
		s.running.Store(false)
		// Let net/http close its registered listener first. Closing the raw
		// listener first races Serve's unregister with http.Server.Close,
		// which can then report a duplicate close as a shutdown failure.
		err = s.proxy.Close()
		// Serve may not have registered it yet. Close marks net/http shut
		// down, so a late Serve cannot register this fallback listener.
		if s.listener != nil {
			if e := s.listener.Close(); !errors.Is(e, net.ErrClosed) {
				err = errors.Join(err, e)
			}
		}
		s.listenMu.Unlock()
		if s.dns != nil {
			_ = s.dns.Close()
		}
		// Nothing may publish into Flows once it is closed.
		s.sendMu.Lock()
		s.sendClosed = true
		close(s.out)
		s.sendMu.Unlock()
		s.mu.Lock()
		streams := s.streams
		s.streams = make(map[uuid.UUID]*streamBodies)
		s.trailers = make(map[uuid.UUID]*flowTrailers)
		s.mu.Unlock()
		for _, st := range streams {
			st.release()
		}
		if s.bodyDir != "" {
			_ = os.RemoveAll(s.bodyDir)
		}
	})
	return err
}

// Replay sends the flow's request again through this proxy. It returns the
// response to this send, independently of the capture queue, so concurrent
// sends of the same URL cannot pick up each other's responses. The preview
// keeps at most 8 MiB; the capture still keeps the complete exchange.
func (s *Server) Replay(ctx context.Context, f Flow) (Flow, error) {
	return s.ReplayWithCookies(ctx, f, nil, true)
}

type replaySourceKey struct{}

type replayOrigin struct {
	source int
	run    string
}

// ReplayWithCookies uses the request workspace's cookie jar, including across
// redirects. Ordinary capture replay deliberately does not inherit login state.
// Regression checks stop at the original response instead of following redirects.
func (s *Server) ReplayWithCookies(ctx context.Context, f Flow, jar http.CookieJar, followRedirects bool) (Flow, error) {
	rawURL := f.URL
	if rawURL == "" {
		rawURL = f.Scheme + "://" + f.Host + f.Path
	}
	var body io.Reader
	raw := f.RequestBody
	if f.RequestBodyFile != "" {
		if spilled, err := os.ReadFile(f.RequestBodyFile); err == nil {
			raw = string(spilled)
		}
	}
	if raw != "" {
		body = strings.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, f.Method, rawURL, body)
	if err != nil {
		return Flow{}, err
	}
	for key, values := range f.RequestHeader {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	for _, header := range hopHeaders {
		req.Header.Del(header)
	}

	proxyURL := &url.URL{Scheme: "http", Host: s.Addr()}
	var addresses []string
	finished := false // guarded by s.mu, including late transport dials
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		finished = true
		for _, addr := range addresses {
			delete(s.replayClients, addr)
		}
	}()
	client := &http.Client{
		Jar: jar,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: s.roots},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
				if err == nil && (f.ID != 0 || f.DebugRun != "") {
					// Register before the transport can send anything. Each send
					// owns its transport, so concurrent identical URLs cannot mix.
					s.mu.Lock()
					if !finished {
						local := conn.LocalAddr().String()
						s.replayClients[local] = replayOrigin{source: f.ID, run: f.DebugRun}
						addresses = append(addresses, local)
					}
					s.mu.Unlock()
				}
				return conn, err
			},
		},
	}
	if !followRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	defer client.CloseIdleConnections()
	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return Flow{}, err
	}
	defer resp.Body.Close()
	preview, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Flow{}, err
	}
	rest, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return Flow{}, err
	}
	return Flow{
		Method: req.Method, URL: rawURL, Status: resp.StatusCode,
		RequestHeader: req.Header.Clone(), RequestBody: strings.Clone(raw[:min(len(raw), 8<<20)]),
		RequestBodySize: len(raw),
		ResponseHeader:  resp.Header.Clone(), ResponseBody: string(preview),
		ResponseBodySize: len(preview) + int(rest), Bytes: len(preview) + int(rest),
		ResponseTrailer: resp.Trailer.Clone(), ResponseProto: resp.Proto,
		Millis: int(time.Since(started).Milliseconds()), At: time.Now(),
	}, nil
}

// addon records when a request started, applies the rules, and publishes it
// once its response is complete.
type addon struct {
	proxy.BaseAddon
	server *Server
}

func (a *addon) ClientConnected(client *proxy.ClientConn) {
	a.server.noteClient(client, true)
}

func (a *addon) ClientDisconnected(client *proxy.ClientConn) {
	a.server.noteClient(client, false)
}

func (a *addon) Requestheaders(f *proxy.Flow) {
	if f.Request == nil {
		return
	}
	if raw := f.Request.Raw(); raw != nil {
		// Keep the socket's identity on the exchange before it disconnects.
		// A later connection reusing its address must not change history.
		process := a.server.clientProcess(f)
		*raw = *raw.WithContext(context.WithValue(raw.Context(), clientProcessKey{}, process))
	}
	if raw := f.Request.Raw(); raw != nil && f.ConnContext != nil && f.ConnContext.ClientConn != nil && f.ConnContext.ClientConn.Conn != nil {
		a.server.mu.Lock()
		source := a.server.replayClients[f.ConnContext.ClientConn.Conn.RemoteAddr().String()]
		a.server.mu.Unlock()
		if source != (replayOrigin{}) {
			// Keep the link on the exchange even if its socket closes before
			// capture finishes. No correlation header reaches the upstream.
			*raw = *raw.WithContext(context.WithValue(raw.Context(), replaySourceKey{}, source))
		}
	}
	if f.Request.Method == "CONNECT" {
		// A CONNECT the proxy does not decrypt ends here: show the tunnel so
		// the table says what went through it.
		a.server.noteDNS(f)
		a.server.publishConnect(f)
		return
	}
	a.server.noteDNS(f)
	a.server.mu.Lock()
	a.server.started[f.Id] = time.Now()
	a.server.mu.Unlock()
	a.server.startDebug(f)
	noCacheRequest(f)
	// The engine streams a gRPC call instead of buffering it (it sets Stream
	// before this hook), so it never calls Request for one: apply the rules
	// here, while the URL and the headers are still the proxy's to change and
	// a rule may answer the call itself. A breakpoint or a script that edits
	// the body cannot, since the client is still sending it.
	if f.Stream {
		if resp := a.server.applyRules(f); resp != nil {
			f.Response = resp
			a.server.publish(f)
		}
	}
}

// Responseheaders notes when the response started, the end of the wait.
func (a *addon) Responseheaders(f *proxy.Flow) {
	noCacheResponse(f)
	a.server.mu.Lock()
	a.server.firstByte[f.Id] = time.Now()
	a.server.mu.Unlock()
}

func (a *addon) Request(f *proxy.Flow) {
	if f.Request == nil {
		return
	}
	// The buffered body and its trailers are complete before a breakpoint
	// or a rule can publish a local response and short-circuit the exchange.
	a.server.keepTrailers(f, "req", f.Request.Trailers())
	script := a.server.currentScript()
	if err := script.applyRequest(f); err != nil {
		a.server.scriptErrors.Add(1)
	} else if script != nil && script.hasRequest {
		recordAction(f, "Request script")
	}
	if d := a.server.maybeBreak(f, PhaseRequest); d != nil {
		recordAction(f, "Request breakpoint applied")
		if d.Abort {
			f.Response = breakpointAbort(d.AbortStatus)
			a.server.publish(f)
			return
		}
		if d.Method != "" {
			f.Request.Method = d.Method
		}
		if d.URL != "" {
			if u, err := url.Parse(d.URL); err == nil {
				f.Request.URL = u
			}
		}
		if d.Header != nil {
			f.Request.Header = d.Header.Clone()
		}
		if !d.KeepBody {
			f.Request.Body = []byte(d.Body)
			clearBodyEncoding(f.Request.Header)
		}
	}
	noCacheRequest(f)
	if resp := a.server.applyRules(f); resp != nil {
		f.Response = resp
		// A rule that answers the request itself short-circuits the engine,
		// which then never calls Response: publish it here instead.
		a.server.publish(f)
	}
}

func (a *addon) WebSocketStart(f *proxy.Flow) {
	a.server.publishFlow(f, "ws", nil, http.StatusSwitchingProtocols)
}

func (a *addon) WebSocketMessage(f *proxy.Flow) {
	if f.WebSocket == nil || len(f.WebSocket.Messages) == 0 {
		return
	}
	m := f.WebSocket.Messages[len(f.WebSocket.Messages)-1]
	direction := "in"
	if m.FromClient {
		direction = "out"
	}
	a.server.emitFrame(f, frameInput{
		Direction: direction,
		Kind:      wsFrameKind(m.Type),
		Data:      m.Content,
		At:        m.Timestamp,
	})
}

func (a *addon) WebSocketEnd(f *proxy.Flow) { a.server.endFrames(f) }

func (a *addon) SSEStart(f *proxy.Flow) {
	a.server.publishFlow(f, "sse", nil, 0)
}

func (a *addon) SSEMessage(f *proxy.Flow) {
	if f.SSE == nil || len(f.SSE.Events) == 0 {
		return
	}
	e := f.SSE.Events[len(f.SSE.Events)-1]
	data := e.Data
	if data == "" {
		data = e.Raw
	}
	a.server.emitFrame(f, frameInput{
		Direction: "in",
		Kind:      "event",
		Data:      []byte(data),
		At:        e.Time,
		EventID:   e.ID,
		EventName: e.Event,
	})
}

func (a *addon) SSEEnd(f *proxy.Flow) { a.server.endFrames(f) }

func (a *addon) Response(f *proxy.Flow) {
	if f.Response == nil || f.Stream || isSSE(f.Response) {
		a.server.publish(f)
		return
	}
	// The engine buffered the response body, so its trailer block is complete.
	a.server.keepTrailers(f, "res", f.Response.Trailers())
	// A response script or breakpoint edits the body in
	// StreamResponseModifier, the only place the proxy still lets the reader
	// be replaced: publish there, once the edits are in.
	if a.server.currentScript().HasResponse() || a.server.willBreakResponse(f) {
		return
	}
	a.server.publish(f)
}

// StreamRequestModifier keeps a request body the engine streams instead of
// buffering, and paces what goes upstream.
func (a *addon) StreamRequestModifier(f *proxy.Flow, in io.Reader) io.Reader {
	noCacheRequest(f)
	// The engine skipped Request for a body past the buffering threshold, so
	// this reader is all of it, the buffered prefix included: keep it.
	if f.Stream && f.Request != nil && f.Request.Body == nil {
		in = a.server.captureStream(f, "req", in, contentEncoding(f.Request.Header), requestLength(f.Request))
	}
	return a.server.throttleUp(in)
}

// StreamResponseModifier runs the response script on the body before the
// client gets it, and publishes the flow after the edit.
func (a *addon) StreamResponseModifier(f *proxy.Flow, in io.Reader) io.Reader {
	noCacheResponse(f)
	if f.Response == nil {
		return a.server.throttleDown(in)
	}
	if f.Stream {
		// An SSE stream publishes its own flow and events; any other streamed
		// response is a body the engine never buffered, so keep it here and
		// publish the flow when it ends.
		if !isSSE(f.Response) {
			in = a.server.captureStream(f, "res", in, contentEncoding(f.Response.Header), declaredLength(f.Response.Header))
		}
		return a.server.throttleDown(in)
	}
	sc := a.server.currentScript()
	scripted := sc.HasResponse()
	breaking := a.server.willBreakResponse(f)
	if !scripted && !breaking {
		return a.server.throttleDown(in)
	}
	body, err := f.Response.DecodedBody()
	scripted = scripted && err == nil && len(body) <= maxScriptBody
	if scripted {
		f.Response.Body = body
		if err := sc.applyResponse(f); err != nil {
			a.server.scriptErrors.Add(1)
		} else {
			recordAction(f, "Response script")
		}
		clearBodyEncoding(f.Response.Header)
	}
	edited := scripted
	bodyEdited := scripted
	if breaking {
		if d := a.server.maybeBreak(f, PhaseResponse); d != nil {
			recordAction(f, "Response breakpoint applied")
			applyResponseDecision(f, d)
			edited = true
			bodyEdited = bodyEdited || d.Abort || !d.KeepBody
		}
	}
	if !edited {
		a.server.publish(f)
		return a.server.throttleDown(in)
	}
	if bodyEdited {
		clearBodyEncoding(f.Response.Header)
	}
	f.Response.Header.Set("Content-Length", strconv.Itoa(len(f.Response.Body)))
	a.server.publish(f)
	// reply writes the returned reader and response.Body both: keep the
	// reader, which carries the edit, and clear the field.
	body = f.Response.Body
	f.Response.Body = nil
	return a.server.throttleDown(bytes.NewReader(body))
}

// applyResponseDecision writes what the user chose at a response breakpoint.
func applyResponseDecision(f *proxy.Flow, d *Decision) {
	if f.Response == nil {
		return
	}
	if d.Abort {
		f.Response = breakpointAbort(d.AbortStatus)
		return
	}
	if d.Status > 0 {
		f.Response.StatusCode = d.Status
	}
	if d.Header != nil {
		f.Response.Header = d.Header.Clone()
	}
	if !d.KeepBody {
		f.Response.Body = []byte(d.Body)
	}
}

func breakpointAbort(status int) *proxy.Response {
	if status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable {
		status = http.StatusBadGateway
	}
	return &proxy.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
		Body:       []byte("aborted at a breakpoint\n"),
	}
}

// The editor holds decoded bytes, so edited messages need fresh framing and
// must not retain the original encoding or integrity fields.
func clearBodyEncoding(h http.Header) {
	for _, name := range []string{"Content-Length", "Transfer-Encoding", "Content-Encoding", "Content-MD5", "Digest", "Content-Digest", "Repr-Digest"} {
		h.Del(name)
	}
}

func isSSE(r *proxy.Response) bool {
	return strings.Contains(r.Header.Get("Content-Type"), "text/event-stream")
}

// RequestError and HTTPConnectError keep failed exchanges visible, since a
// refused connection is what the user is often looking for.
func (a *addon) RequestError(f *proxy.Flow, err error) {
	a.server.publishError(f, err)
}

func (a *addon) HTTPConnectError(f *proxy.Flow, err error) {
	a.server.publishError(f, err)
}

// applyRules runs the first matching rule. It returns a response for a rule
// that answers the request itself, and nil when the request goes upstream,
// with its URL rewritten for map_remote.
func (s *Server) applyRules(f *proxy.Flow) *proxy.Response {
	s.rulesMu.RLock()
	rules := s.rules
	s.rulesMu.RUnlock()
	if len(rules) == 0 || f.Request.URL == nil {
		return nil
	}
	raw := f.Request.URL.String()
	if i := firstResponseRule(rules, raw, f.Request.Method); i >= 0 {
		r := rules[i]
		if r.Scenario != "" {
			recordAction(f, "Scenario · "+r.Scenario)
		}
		switch r.Type {
		case RuleBlock:
			recordAction(f, "Block · "+r.Match)
			return &proxy.Response{
				StatusCode: http.StatusForbidden,
				Header:     http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
				Body:       []byte("blocked by vscroxy\n"),
			}
		case RuleMapLocal:
			recordAction(f, "Map Local · "+r.Match+" → "+r.Target)
			body, err := readMockBody(r.Target)
			if err != nil {
				return &proxy.Response{
					StatusCode: http.StatusBadGateway,
					Header:     http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
					Body:       []byte("map local: " + err.Error() + "\n"),
				}
			}
			status := r.Status
			if status == 0 {
				status = http.StatusOK
			}
			headers := r.Headers.Clone()
			if headers == nil {
				headers = make(http.Header)
			}
			stripHopHeaders(headers)
			if headers.Get("Content-Type") == "" {
				headers.Set("Content-Type", contentType(r.Target))
			}
			headers.Set("Content-Length", strconv.Itoa(len(body)))
			return &proxy.Response{
				StatusCode: status,
				Header:     headers,
				Body:       body,
			}
		case RuleMapRemote:
			u, err := mapURL(f.Request.URL, r.Target)
			if err != nil {
				return nil
			}
			recordAction(f, "Map Remote · "+r.Match+" → "+u.String())
			// Keep the URL the client asked for: the table shows that one,
			// and a replay sends it again.
			s.mu.Lock()
			s.original[f.Id] = *f.Request.URL
			s.mu.Unlock()
			f.Request.URL = u
			return nil
		}
	}
	return nil
}

// mapURL points a URL at another base, keeping its path and query unless the
// base has a path of its own to put in front.
func mapURL(orig *url.URL, target string) (*url.URL, error) {
	t, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if t.Scheme == "" || t.Host == "" {
		return nil, fmt.Errorf("map remote target %q needs a scheme and a host", target)
	}
	u := *orig
	u.Scheme = t.Scheme
	u.Host = t.Host
	if t.Path != "" && t.Path != "/" {
		u.Path = strings.TrimRight(t.Path, "/") + orig.Path
	}
	if t.RawQuery != "" {
		u.RawQuery = t.RawQuery
	}
	return &u, nil
}

func contentType(path string) string {
	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

type clientSocket struct {
	remote, local string
}

func socketOf(conn net.Conn) clientSocket {
	return clientSocket{conn.RemoteAddr().String(), conn.LocalAddr().String()}
}

type clientIdentity struct {
	client  *proxy.ClientConn
	process procinfo.Process
}

type clientProcessKey struct{}

// noteClient resolves ownership using the full accepted socket. Unknown
// identities fail closed, rather than borrowing a process from a recycled port.
func (s *Server) noteClient(client *proxy.ClientConn, connected bool) {
	if client == nil || client.Conn == nil {
		return
	}
	addr := socketOf(client.Conn)
	if !connected {
		s.mu.Lock()
		if s.clients[addr].client == client {
			delete(s.clients, addr)
		}
		s.mu.Unlock()
		return
	}
	process := procinfo.Lookup(addr.remote, addr.local)
	s.mu.Lock()
	s.clients[addr] = clientIdentity{client: client, process: process}
	s.mu.Unlock()
}

// socketProcess retries an unreadable owner only while the exact accepted
// connection is still active. The OS may not list its socket at accept time.
func (s *Server) socketProcess(addr clientSocket, client *proxy.ClientConn) procinfo.Process {
	s.mu.Lock()
	identity := s.clients[addr]
	s.mu.Unlock()
	if client == nil || identity.client != client {
		return procinfo.Process{}
	}
	if identity.process.Path != "" {
		return identity.process
	}
	process := procinfo.Lookup(addr.remote, addr.local)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clients[addr].client != identity.client {
		return procinfo.Process{}
	}
	identity.process = process
	s.clients[addr] = identity
	return process
}

// clientProcess returns the process behind the connection a flow arrived on.
func (s *Server) clientProcess(f *proxy.Flow) procinfo.Process {
	if f.Request != nil {
		if raw := f.Request.Raw(); raw != nil {
			if process, ok := raw.Context().Value(clientProcessKey{}).(procinfo.Process); ok {
				return process
			}
		}
	}
	if f.ConnContext == nil || f.ConnContext.ClientConn == nil || f.ConnContext.ClientConn.Conn == nil {
		return procinfo.Process{}
	}
	client := f.ConnContext.ClientConn
	return s.socketProcess(socketOf(client.Conn), client)
}

// publishConnect shows a CONNECT the proxy tunnels without decrypting it.
func (s *Server) publishConnect(f *proxy.Flow) {
	host := f.Request.URL.Hostname()
	if host == "" || (f.ConnContext != nil && f.ConnContext.Intercept) || s.ignored(host) {
		// The tunnel gets no row of its own, so there is nothing to mark.
		s.mu.Lock()
		delete(s.dnsHits, f.Id)
		s.mu.Unlock()
		return
	}
	s.publishFlow(f, "connect", nil, 0)
}

func (s *Server) publish(f *proxy.Flow) {
	s.publishFlow(f, "http", nil, 0)
}

// flowTrailers is the trailer block one flow ended each of its sides with. The
// side that finishes its body writes here, and the publisher reads once, under
// the same lock, so the map the transport owns is never read while it fills it.
type flowTrailers struct {
	req, res http.Header
}

// keepTrailers keeps the trailers a side of a flow ended with. It is called
// once that side's body has ended, so the transport has finished the block it
// passes in.
func (s *Server) keepTrailers(f *proxy.Flow, side string, trailer http.Header) {
	if f == nil || len(trailer) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, active := s.started[f.Id]; !active {
		return // A late request EOF must not recreate state after publication.
	}
	select {
	case <-s.closed:
		return
	default:
	}
	t := s.trailers[f.Id]
	if t == nil {
		t = &flowTrailers{}
		s.trailers[f.Id] = t
	}
	if side == "req" {
		t.req = trailer.Clone()
	} else {
		t.res = trailer.Clone()
	}
}

// publishError shows a request that never reached its server as the gateway
// error the client got, with the reason in the body.
func (s *Server) publishError(f *proxy.Flow, err error) {
	f.Error = err
	kind := "http"
	if f.Request != nil && f.Request.Method == "CONNECT" {
		kind = "connect"
	}
	if cc := f.ConnContext; cc != nil {
		events := cc.ConnectionEvents()
		if len(events) == 0 || events[len(events)-1].Error == "" || events[len(events)-1].At.Before(f.StartTime) {
			stage := "HTTP"
			if kind == "connect" {
				stage = "CONNECT"
			}
			cc.RecordEvent(stage, "Exchange failed", err)
		}
	}
	if f.Response == nil {
		f.Response = &proxy.Response{
			StatusCode: http.StatusBadGateway,
			Header:     http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
			Body:       []byte("vscroxy: " + err.Error() + "\n"),
		}
	}
	s.publishFlow(f, kind, nil, 0)
}

// publishFlow turns one exchange into the boundary Flow the UI shows. kind is
// "http", "ws" or "sse"; status overrides the response's when non-zero. A
// streamed exchange passes its kept bodies in, since they never reached the
// flow; every other caller leaves them out and publishFlow takes what the
// capture kept, if anything.
func (s *Server) publishFlow(f *proxy.Flow, kind string, frames []Frame, status int, bodies ...*streamBodies) int {
	if f.Request == nil {
		return 0
	}
	noCacheResponse(f)
	var streamed *streamBodies
	if len(bodies) > 0 {
		streamed = bodies[0]
	} else {
		streamed = s.takeStream(f)
	}
	var reqTrailer, resTrailer http.Header
	now := time.Now()
	s.mu.Lock()
	start, ok := s.started[f.Id]
	delete(s.started, f.Id)
	if trailers := s.trailers[f.Id]; trailers != nil {
		reqTrailer, resTrailer = trailers.req, trailers.res
		delete(s.trailers, f.Id)
	}
	first, hasFirst := s.firstByte[f.Id]
	delete(s.firstByte, f.Id)
	display := f.Request.URL
	if orig, mapped := s.original[f.Id]; mapped {
		display = &orig
		delete(s.original, f.Id)
	}
	if kind == "connect" {
		// A tunnel's URL is its host, with the scheme HTTPS implies.
		host := display.Hostname()
		port := display.Port()
		if port != "" && port != "443" {
			host = net.JoinHostPort(host, port)
		}
		display = &url.URL{Scheme: "https", Host: host}
	}
	s.mu.Unlock()
	if !ok {
		start = f.StartTime
	}

	reqData, err := f.Request.DecodedBody()
	if err != nil {
		reqData = nil
	}
	if s.ignored(display.Hostname()) {
		streamed.release()
		return 0
	}
	client := s.clientProcess(f)
	if s.ignoredApp(client.Name) {
		streamed.release()
		return 0
	}
	reqBody, reqFile, reqSize, reqErr := s.keepBody(f.Id, "req", reqData)
	if err != nil {
		reqErr = "request body could not be decoded: " + err.Error()
	}
	streamReq, streamRes := streamed.sides()
	if streamReq != nil {
		r := streamReq.finish()
		reqBody, reqFile, reqSize, reqErr = r.Head, r.File, r.Size, r.Err
	}
	size, respHeaders, respProto := 0, "", ""
	respBody, respFile, respSize := "", "", 0
	respErr := ""
	if f.Response != nil {
		size = len(f.Response.Body)
		respHeaders = formatResponse(f.Response)
		respProto = f.Response.Proto
		respData, err := f.Response.DecodedBody()
		if err != nil {
			respData = nil
		}
		respBody, respFile, respSize, respErr = s.keepBody(f.Id, "res", respData)
		if err != nil {
			respErr = "response body could not be decoded: " + err.Error()
		}
	}
	if streamRes != nil {
		r := streamRes.finish()
		respBody, respFile, respSize, respErr = r.Head, r.File, r.Size, r.Err
		if r.Wire > 0 {
			size = r.Wire
		}
	}
	if status == 0 && f.Response != nil {
		status = f.Response.StatusCode
	}
	if len(frames) > 0 {
		size = 0
		for _, frame := range frames {
			size += len(frame.Data)
		}
	}
	method := f.Request.Method
	if kind == "ws" {
		method = "WS"
	}
	// Both WS and WSS carry the actual upgrade request, including its path
	// and escaped query, rather than the CONNECT envelope.
	shown, shownPath := display, display.RequestURI()
	millis := int(now.Sub(start).Milliseconds())
	if kind == "http" && streamed == nil {
		// The throttle paces the body after this hook, so add what the
		// transfer will take: the flow's time should match the client's. A
		// streamed body is published as it ends, so its time is already in.
		t := s.Throttle()
		if t.DownPerSec > 0 && size > 0 {
			millis += int(float64(size) / float64(t.DownPerSec) * 1000)
		}
		if t.UpPerSec > 0 && len(f.Request.Body) > 0 {
			millis += int(float64(len(f.Request.Body)) / float64(t.UpPerSec) * 1000)
		}
	}
	wait, download := 0, 0
	if hasFirst {
		wait = int(first.Sub(start).Milliseconds())
		download = millis - wait
	}
	flow := Flow{
		ID:                int(s.nextID.Add(1)),
		Actions:           debugActions(f),
		Method:            method,
		Scheme:            shown.Scheme,
		Host:              shown.Host,
		Path:              shownPath,
		URL:               shown.String(),
		Status:            status,
		Bytes:             size,
		Millis:            millis,
		Wait:              wait,
		Download:          download,
		RequestHeaders:    formatRequest(f.Request, display),
		ResponseHeaders:   respHeaders,
		RequestBody:       reqBody,
		RequestBodyFile:   reqFile,
		RequestBodySize:   reqSize,
		ResponseBody:      respBody,
		ResponseBodyFile:  respFile,
		ResponseBodySize:  respSize,
		RequestBodyError:  reqErr,
		ResponseBodyError: respErr,
		RequestHeader:     f.Request.Header.Clone(),
		ResponseHeader:    responseHeader(f.Response),
		RequestTrailer:    reqTrailer,
		ResponseTrailer:   resTrailer,
		ResponseProto:     respProto,
		At:                now,
		Kind:              kind,
		Frames:            clipFrames(frames),
		Process:           client.Name,
		ProcessPath:       client.Path,
		DNSMapped:         s.dnsMapped(f),
		Conn:              s.connInfo(f, start),
	}
	if raw := f.Request.Raw(); raw != nil {
		origin, _ := raw.Context().Value(replaySourceKey{}).(replayOrigin)
		flow.ReplayOf, flow.DebugRun = origin.source, origin.run
	}
	s.mu.Lock()
	if len(s.flowIDs) > 4096 {
		clear(s.flowIDs)
	}
	s.flowIDs[f.Id] = flow.ID
	s.mu.Unlock()
	s.send(flow)
	return flow.ID
}

// send hands a flow to the UI, counting it as dropped when the queue is full or
// the server has closed, so a publish never writes to a closed channel.
func (s *Server) send(flow Flow) {
	s.sendMu.RLock()
	defer s.sendMu.RUnlock()
	if s.sendClosed {
		return
	}
	select {
	case s.out <- flow:
	default:
		s.dropped.Add(1)
	}
}

// ConnInfo is the connection an exchange ran over, as the detail pane shows
// it: who talked to the proxy, where the proxy dialled, and what it negotiated.
type ConnInfo struct {
	Proto     string // HTTP version the client spoke, such as HTTP/2.0
	Client    string // the client end of the connection to the proxy
	Proxy     string // the proxy end of that connection
	Server    string // the address the proxy dialled
	Resolved  string // the addresses the name resolved to
	LookupMs  int    // how long that answer took
	Flows     int    // how many requests this connection carried before this one
	TLS       string // TLS version the upstream agreed on
	Cipher    string // cipher suite the upstream agreed on
	CertCN    string // the upstream certificate's common name
	Issuer    string // who issued that certificate
	CertUntil string // when it expires, as a date

	// Reused says the connection was already open, so this request spent no
	// time on connect or TLS; DNSFresh says the lookup happened during it.
	Reused     bool
	DNSFresh   bool
	Log        string // immutable connection history and metadata, also saved in sessions
	Error      string // transport failure, independent of an HTTP/CONNECT status
	ErrorStage string
}

// connInfo reads the connection an exchange ran over.
func (s *Server) connInfo(f *proxy.Flow, start time.Time) ConnInfo {
	var ci ConnInfo
	var log strings.Builder
	if f.Request != nil {
		ci.Proto = f.Request.Proto
	}
	if f.Error != nil {
		ci.Error, ci.ErrorStage = f.Error.Error(), "HTTP"
	}
	if cc := f.ConnContext; cc != nil {
		fmt.Fprintf(&log, "Connection %s\n", cc.Id())
		if conn := cc.ClientConn; conn != nil && conn.Conn != nil {
			ci.Client = conn.Conn.RemoteAddr().String()
			ci.Proxy = conn.Conn.LocalAddr().String()
		}
		ci.Flows = int(cc.FlowCount.Load())
		ci.Reused = ci.Flows > 1
		if ci.Reused {
			fmt.Fprintf(&log, "Reused connection · request #%d (includes connection setup history)\n", ci.Flows)
		} else {
			fmt.Fprintln(&log, "New connection")
		}
		events := cc.ConnectionEvents()
		if f.Error != nil && len(events) > 0 {
			ci.ErrorStage = events[len(events)-1].Stage
		}
		for _, event := range events {
			fmt.Fprintf(&log, "%s  [%s] %s", event.At.Format("15:04:05.000"), event.Stage, event.Message)
			if event.Error != "" {
				fmt.Fprintf(&log, " · FAILED: %s", event.Error)
			}
			fmt.Fprintln(&log)
		}
		if f.UseSeparateClient {
			fmt.Fprintln(&log, "Target changed: upstream details are in the separate transport events above, not the original connection's certificate.")
		}
		if server := cc.ServerConn; server != nil && !f.UseSeparateClient {
			ci.Server = server.Address
			if state := server.TlsState(); state != nil {
				ci.TLS = tls.VersionName(state.Version)
				ci.Cipher = tls.CipherSuiteName(state.CipherSuite)
				fmt.Fprintf(&log, "TLS: %s · %s\nALPN: %s\n", ci.TLS, ci.Cipher, cmp.Or(state.NegotiatedProtocol, "none"))
				if len(state.VerifiedChains) > 0 {
					fmt.Fprintln(&log, "Upstream certificate verification: verified")
				} else {
					fmt.Fprintln(&log, "Upstream certificate verification: skipped (debugging proxy accepts untrusted certificates)")
				}
				if len(state.PeerCertificates) > 0 {
					leaf := state.PeerCertificates[0]
					ci.CertCN = leaf.Subject.CommonName
					ci.Issuer = leaf.Issuer.CommonName
					ci.CertUntil = leaf.NotAfter.Format("2006-01-02")
					sans := slices.Clone(leaf.DNSNames)
					for _, ip := range leaf.IPAddresses {
						sans = append(sans, ip.String())
					}
					fmt.Fprintf(&log, "Certificate: %s · issuer %s · expires %s\nSAN: %s\n", ci.CertCN, ci.Issuer, ci.CertUntil, strings.Join(sans, ", "))
				}
			}
		}
	}
	if s.dns != nil && f.Request != nil && f.Request.URL != nil {
		if lookup, ok := s.dns.Resolved(f.Request.URL.Hostname()); ok {
			ips := make([]string, len(lookup.IPs))
			for i, ip := range lookup.IPs {
				ips[i] = ip.String()
			}
			ci.Resolved = strings.Join(ips, ", ")
			ci.LookupMs = int(lookup.Took.Milliseconds())
			ci.DNSFresh = !lookup.At.Before(start)
		}
	}
	if ci.Client != "" {
		fmt.Fprintf(&log, "Client: %s → proxy %s\n", ci.Client, ci.Proxy)
	}
	if ci.Resolved != "" {
		fmt.Fprintf(&log, "Resolved: %s (%d ms; fresh lookup: %t)\n", ci.Resolved, ci.LookupMs, ci.DNSFresh)
	}
	if ci.Error != "" && f.Request != nil && f.Request.Method == "CONNECT" && f.Response != nil && f.Response.StatusCode == 200 {
		fmt.Fprintln(&log, "CONNECT succeeded, but the subsequent connection failed. No HTTP response from the target was received.")
	}
	ci.Log = log.String()
	return ci
}

// keepBody returns the body a flow keeps in memory, the file that holds the
// whole body when it was spilled, and the body's size. A spilled body keeps
// its head inline so search and the table work without touching the disk.
func (s *Server) keepBody(id uuid.UUID, kind string, body []byte) (head, file string, size int, problem string) {
	size = len(body)
	if size <= maxBody {
		return string(body), "", size, ""
	}
	head = string(body[:maxBody])
	if s.bodyDir != "" {
		file = filepath.Join(s.bodyDir, kind+"-"+id.String()+".bin")
		if err := os.WriteFile(file, body, 0o600); err != nil {
			file = ""
			problem = "the body could not be kept: " + err.Error()
		}
	}
	if file == "" && problem == "" {
		problem = "the body could not be kept: no spill directory"
	}
	return head, file, size, problem
}

// frameInput is one message the engine reported, before the capture keeps it.
type frameInput struct {
	Direction string
	Kind      string
	Data      []byte
	At        time.Time
	EventID   string
	EventName string
}

// emitFrame sends a message that belongs to an already published flow. It
// numbers the message, so a frame the queue could not take leaves a gap the UI
// shows rather than a message that vanishes. A flow the table never saw keeps
// no messages at all.
func (s *Server) emitFrame(f *proxy.Flow, in frameInput) {
	s.mu.Lock()
	id, ok := s.flowIDs[f.Id]
	seq := 0
	if ok {
		s.frameSeq[f.Id]++
		seq = s.frameSeq[f.Id]
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	frame := s.keepFrame(f, in, seq)
	select {
	case s.frames <- FrameEvent{FlowID: id, Frame: frame}:
	default:
		if frame.File != "" {
			_ = os.Remove(frame.File)
		}
		s.frameDrops.Add(1)
	}
}

// StreamEnd is the end the engine observed, not a WebSocket close code or an
// error classification: the underlying hooks supply neither.
type StreamEnd struct {
	At       time.Time `json:"at,omitzero"`
	Messages int       `json:"messages,omitzero"`
}

// StreamEnd returns bounded completion evidence. A zero time means no end was
// recorded, not proof that the socket is still open.
func (s *Server) StreamEnd(id int) StreamEnd {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id <= 0 {
		return StreamEnd{}
	}
	slot := s.streamEnds[id%len(s.streamEnds)]
	if slot.id == id {
		return slot.end
	}
	return StreamEnd{}
}

// endFrames forgets active numbering but keeps bounded completion evidence,
// including messages dropped at the very end of a full queue.
func (s *Server) endFrames(f *proxy.Flow) {
	s.mu.Lock()
	if id, ok := s.flowIDs[f.Id]; ok {
		slot := &s.streamEnds[id%len(s.streamEnds)]
		if slot.id <= id {
			slot.id = id
			slot.end = StreamEnd{At: time.Now(), Messages: s.frameSeq[f.Id]}
		}
	}
	delete(s.frameSeq, f.Id)
	s.mu.Unlock()
}

// keepFrame returns the frame the UI gets: the whole message, or its head with
// the rest in the spill directory. Kept says how much of the message is here,
// so a message past the spill cap reads as cut instead of silently short.
func (s *Server) keepFrame(f *proxy.Flow, in frameInput, seq int) Frame {
	frame := Frame{
		Seq:       seq,
		Direction: in.Direction,
		Kind:      in.Kind,
		Size:      len(in.Data),
		At:        in.At,
		EventID:   in.EventID,
		EventName: in.EventName,
	}
	kept := in.Data[:min(len(in.Data), maxFrameSpill)]
	if len(kept) > maxFrameBytes {
		// The head stays inline, so the list and a search read it without
		// the disk; the rest goes to a file that Close removes with the
		// bodies.
		if s.bodyDir != "" {
			file := filepath.Join(s.bodyDir, "frame-"+f.Id.String()+"-"+strconv.Itoa(seq)+".bin")
			if err := os.WriteFile(file, kept, 0o600); err == nil {
				frame.File = file
			}
		}
		if frame.File == "" {
			kept = kept[:maxFrameBytes]
		}
	}
	frame.Kept = len(kept)
	frame.Data = string(kept[:min(len(kept), maxFrameBytes)])
	return frame
}

func responseHeader(r *proxy.Response) http.Header {
	if r == nil {
		return nil
	}
	return r.Header.Clone()
}

// wsFrameKind names the WebSocket frame type the engine reported. The engine
// reads data messages only, so the control types are what a flow saved by
// another tool may carry.
func wsFrameKind(t int) string {
	switch t {
	case 1:
		return "text"
	case 2:
		return "binary"
	case 8:
		return "close"
	case 9:
		return "ping"
	case 10:
		return "pong"
	}
	return "frame"
}

func clipFrames(frames []Frame) []Frame {
	if len(frames) > maxFrames {
		frames = frames[len(frames)-maxFrames:]
	}
	return frames
}

func bodyText(b []byte, err error) string {
	if err != nil {
		return ""
	}
	if len(b) > maxBody {
		return string(b[:maxBody]) + fmt.Sprintf("\n\n… truncated: %d of %d bytes shown", maxBody, len(b))
	}
	return string(b)
}

func formatRequest(r *proxy.Request, u *url.URL) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s\n", r.Method, u.RequestURI(), r.Proto)
	fmt.Fprintf(&b, "Host: %s\n", u.Host)
	writeHeaders(&b, r.Header)
	return strings.TrimRight(b.String(), "\n")
}

func formatResponse(r *proxy.Response) string {
	proto := r.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %d %s\n", proto, r.StatusCode, http.StatusText(r.StatusCode))
	writeHeaders(&b, r.Header)
	return strings.TrimRight(b.String(), "\n")
}

func writeHeaders(b *strings.Builder, h http.Header) {
	for _, key := range slices.Sorted(maps.Keys(h)) {
		for _, value := range h[key] {
			fmt.Fprintf(b, "%s: %s\n", key, value)
		}
	}
}

// dnsMapped is the mapping a flow's dial went through, once.
func (s *Server) dnsMapped(f *proxy.Flow) string {
	s.mu.Lock()
	hit, ok := s.dnsHits[f.Id]
	delete(s.dnsHits, f.Id)
	s.mu.Unlock()
	if !ok {
		return ""
	}
	return hit.Name + " → " + hit.IP.String()
}
