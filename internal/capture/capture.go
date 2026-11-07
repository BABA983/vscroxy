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
	"github.com/sirupsen/logrus"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/dnsmap"
	"github.com/baba983/vscroxy/internal/graphql"
	"github.com/baba983/vscroxy/internal/hosts"
	"github.com/baba983/vscroxy/internal/procinfo"
	"github.com/baba983/vscroxy/internal/rules"
	"github.com/baba983/vscroxy/internal/upstream"
)

func init() {
	// The engine's process-wide logger includes raw URLs both in fields and
	// error text. Traffic belongs in Capture, never diagnostic stderr/files.
	// Apply this at the engine boundary for desktop and headless callers alike;
	// application slog diagnostics remain independent and enabled.
	logrus.SetOutput(io.Discard)
}

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

	// DNSMapped says what a Host profile did to the dial, as
	// "name → address", and HostProfile which profile and line did it, as
	// "Staging:2"; both are empty when none did.
	DNSMapped   string
	HostProfile string

	// Conn is what the exchange ran over: the addresses at both ends, the
	// HTTP version, the addresses the name resolved to, and the TLS the
	// upstream negotiated.
	Conn ConnInfo

	// Wait is how long the request took to reach its first response byte,
	// and Download the rest of Millis: the two phases a waterfall shows.
	// Both are zero when the exchange never reached a server.
	Wait     int
	Download int
	// Timing contains request-local trace observations, not connection history.
	Timing *Timing
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
	// RuleBlock refuses the request, drops its connection or hides it, as
	// its Block says.
	RuleBlock RuleType = "block"
	// RuleBreak pauses a matching request until the UI decides what to do.
	RuleBreak RuleType = "break"
	// RuleBreakResponse pauses a matching response until the UI decides.
	RuleBreakResponse RuleType = "break_response"
	// RuleBreakMessage pauses complete WebSocket data messages, not handshakes.
	RuleBreakMessage RuleType = "break_message"
	// RuleScript runs a script on a matching request and its response: the
	// one its Target names, in the scripts folder (see ScriptRunner).
	RuleScript RuleType = "script"
	// RuleCodeMock answers locally using its script's onMock hook. Failures
	// return a local error, never an upstream fallback.
	RuleCodeMock RuleType = "code_mock"
	// RuleNetwork overrides global network conditions for a matching exchange.
	RuleNetwork RuleType = "network"
)

// What a Block rule does.
const (
	BlockRefuse = ""     // answer 403 without contacting the server
	BlockDrop   = "drop" // close the client's connection without an answer
	BlockHide   = "hide" // proxy it as usual, but keep it out of the table
)

// BlockActions are the Block actions, in the order an editor offers them.
var BlockActions = []string{BlockRefuse, BlockDrop, BlockHide}

// errDropped is the failure a Block rule that drops the connection records.
var errDropped = errors.New("connection dropped by a Block rule")

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
	// Network is required for network rules; an all-zero profile means Off.
	Network *Throttle `json:"network,omitempty"`
	// MatchMode says how Match reads: a part of the URL (empty), a
	// "wildcard" for the whole URL or a "regex"; see rules.Matcher.
	MatchMode string `json:"matchMode,omitempty"`
	// Apps limits the rule to requests from these apps (rules.App patterns).
	Apps []string `json:"apps,omitempty"`
	// When are request headers the rule needs, all of them.
	When []rules.HeaderCondition `json:"when,omitempty"`
	// Operation requires a verified selected GraphQL operation, case-sensitive.
	Operation string `json:"operation,omitempty"`
	// PathFrom and PathTo rewrite a Map Remote path: a path starting with
	// PathFrom starts with PathTo instead. KeepHost sends the client's Host
	// header to the new target instead of the target's own.
	PathFrom string `json:"pathFrom,omitempty"`
	PathTo   string `json:"pathTo,omitempty"`
	KeepHost bool   `json:"keepHost,omitzero"`
	// Block is what a Block rule does: BlockRefuse, BlockDrop or BlockHide.
	Block string `json:"block,omitempty"`
	// Scenario groups response-affecting rules; empty is always global.
	// Every member of a group has the same ScenarioActive value, and at most
	// one group is active. Disabled remains an independent per-rule choice.
	Scenario       string `json:"scenario,omitempty"`
	ScenarioActive bool   `json:"scenarioActive,omitzero"`
	Direction      string `json:"direction,omitempty"` // message breakpoint: "", "in" or "out"
}

// Phase is where a breakpoint paused an exchange.
type Phase int

const (
	PhaseRequest Phase = iota
	PhaseResponse
	PhaseMessage
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
	BodyReadOnly bool            // binary, undecodable or too large for the text editor
	MessageType  string          // message phase: "text" or "binary" (Body is padded base64 for binary)
	Direction    string          // message phase: "out" or "in"
	Done         <-chan struct{} `json:"-"` // pause ended, including timeout/disconnect
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
	socketMu      sync.Mutex
	socketWG      sync.WaitGroup
	sockets       map[uuid.UUID]bool

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

	// caPool holds only this proxy's CA, for traffic that must come from
	// this proxy rather than the real server.
	caPool *x509.CertPool
	// certs holds the client certificates and extra roots shared by authored
	// sends, intercepted upstream TLS and reverse proxies. Files are read on
	// each handshake.
	certs atomic.Value // certs.Settings

	rulesMu sync.RWMutex
	rules   []Rule
	// hits counts, per rule, the exchanges it changed since launch. It is
	// keyed by what the rule does, so editing a rule starts it over.
	hits map[string]int

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
	scripts      ScriptRunner
	scriptErrors atomic.Int64

	throttleMu sync.RWMutex
	throttle   Throttle

	upstream   atomic.Value // *upstreamSnapshot
	ssl        atomic.Value // SSLConfig
	ignoreList atomic.Value // []string
	ignoreApps atomic.Value // []string
	allowList  atomic.Value // AllowList

	// socks is the additional SOCKS5 entry. Its listener and connections
	// belong to this server, so closing capture ends both.
	socksMu    sync.Mutex
	socksCfg   SOCKSConfig
	socksLn    net.Listener
	socksAddr  string
	socksErr   error
	socksConns map[net.Conn]struct{}
	socksWG    sync.WaitGroup

	// reverses are the reverse proxy entries. They forward through this
	// server's own HTTP listener, so rules and capture apply to them.
	reverseMu      sync.Mutex
	reverseCfg     []ReverseProxy
	reverseTargets map[string]*url.URL
	reverses       []*reverseEntry
	reverseStatus  map[string]ReverseProxyStatus
	// reverseToken marks requests the reverse proxies send through this
	// listener, so their HTTPS tunnels are always decrypted for capture.
	reverseToken string

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
		reverseToken:  uuid.NewString(),
		reverseStatus: make(map[string]ReverseProxyStatus),
	}
	s.certs.Store(certs.Settings{})
	p, err := proxy.NewProxy(&proxy.Options{
		Addr:              addr,
		CaRootPath:        caRootPath,
		SslInsecure:       true, // a debugging proxy inspects self-signed upstreams too
		StreamLargeBodies: 8 << 20,
		ClientCertificate: func(address string) (*tls.Certificate, error) {
			return s.certificates().ClientCertificate(address)
		},
		RootCAs: func() (*x509.CertPool, error) {
			return s.certificates().RootPool(nil)
		},
	})
	if err != nil {
		return nil, err
	}
	p.AddAddon(&addon{server: s})
	p.SetShouldInterceptRule(func(req *http.Request) bool {
		// A reverse proxy's own HTTPS tunnel is captured regardless of the
		// decryption lists: it exists to be seen. Only this app knows the
		// per-run token, and the addon removes it from the exchange.
		if req.Header.Get(reverseTokenHeader) == s.reverseToken {
			return true
		}
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
	p.SetDialFallbackRule(func(req *http.Request) bool {
		s.rulesMu.RLock()
		defer s.rulesMu.RUnlock()
		return mayMapHost(s.rules, req.Host)
	})
	p.SetLazyInterceptRule(func(req *http.Request) bool {
		s.rulesMu.RLock()
		defer s.rulesMu.RUnlock()
		return mayMatchHost(s.rules, req.Host, RuleCodeMock)
	})
	p.SetUpstreamProxy(func(req *http.Request, address string) ([]proxy.UpstreamProxy, error) {
		snapshot, _ := s.upstream.Load().(*upstreamSnapshot)
		if snapshot == nil {
			return nil, nil
		}
		return snapshot.resolve(req, address)
	})
	s.proxy = p
	ca := p.GetCertificate()
	own := x509.NewCertPool()
	own.AddCert(&ca)
	s.caPool = own
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

// SetCertificates replaces the client certificates and extra roots used by
// authored sends, intercepted upstream TLS and reverse proxies. The snapshot
// is immutable: later edits in the caller do not change an in-flight send.
func (s *Server) SetCertificates(settings certs.Settings) {
	s.certs.Store(settings.Clone())
}

// Certificates returns a copy of the active certificate settings.
func (s *Server) Certificates() certs.Settings {
	return s.certificates().Clone()
}

func (s *Server) certificates() certs.Settings {
	settings, _ := s.certs.Load().(certs.Settings)
	return settings
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
		if rules.Host(host, []string{sslHostname(pattern)}) {
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

// SetHosts sets the hosts the proxy dials elsewhere, as Host profiles map
// them, highest priority first: the request keeps its name, URL and Host
// header, and the proxy connects to the address, so a device's traffic
// reaches another machine as if the name pointed there. Open connections keep
// the address they dialled; DropConnections makes clients dial again.
func (s *Server) SetHosts(rules []hosts.Rule) error {
	if s.dns == nil {
		return errors.New("the DNS server did not start")
	}
	s.dns.SetRules(slices.Clone(rules))
	return nil
}

// DropConnections closes every client connection, so the next requests dial
// again and pick up changed Host profiles. It returns how many it closed.
func (s *Server) DropConnections() int {
	s.mu.Lock()
	open := make([]*proxy.ClientConn, 0, len(s.clients))
	for _, id := range s.clients {
		open = append(open, id.client)
	}
	s.mu.Unlock()
	for _, client := range open {
		_ = client.Conn.Close()
	}
	return len(open)
}

// DNSHit is a flow whose target a Host profile changed: the host, the
// address, and the profile and line that mapped it.
type DNSHit struct {
	Name    string
	IP      net.IP
	Profile string
	Line    int
	At      time.Time
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
	rule, ok := hosts.Lookup(s.dns.Rules(), host, s.clientProcess(f).Name)
	if !ok {
		return
	}
	hit := DNSHit{Name: host, IP: rule.IP, Profile: rule.Profile, Line: rule.Line, At: time.Now()}
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

// ParseSSL reads the SSL proxying settings; no data decrypts nothing.
func ParseSSL(data []byte) (SSLConfig, error) {
	var c SSLConfig
	if len(data) == 0 {
		return c, nil
	}
	err := json.Unmarshal(data, &c)
	return c, err
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
	return rules.Host(host, s.Ignore())
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
	return rules.App(name, s.IgnoreApps())
}

// AllowList narrows the table to the traffic being debugged. While On, only
// flows to a host in Hosts or from an app in Apps are shown, the rest is
// still proxied; with both empty nothing is. The ignore lists still apply.
type AllowList struct {
	On          bool
	Hosts, Apps []string
}

// SetAllow sets the allow list.
func (s *Server) SetAllow(l AllowList) {
	l.Hosts, l.Apps = slices.Clone(l.Hosts), slices.Clone(l.Apps)
	s.allowList.Store(l)
}

// Allow returns the allow list.
func (s *Server) Allow() AllowList {
	l, _ := s.allowList.Load().(AllowList)
	return l
}

// allows reports whether a flow to host from app is shown.
func (l AllowList) allows(host, app string) bool {
	return !l.On || rules.Host(host, l.Hosts) || rules.App(app, l.Apps)
}

// ParseIgnore reads a list of hosts or apps, one per line; a line starting
// with # is a comment.
func ParseIgnore(data []byte) []string {
	var patterns []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
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

// RuleHits is how many exchanges r changed since launch, or since it was
// last edited.
func (s *Server) RuleHits(r Rule) int {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	return s.hits[r.key()]
}

func (s *Server) hit(r Rule) {
	s.rulesMu.Lock()
	if s.hits == nil {
		s.hits = make(map[string]int)
	}
	s.hits[r.key()]++
	s.rulesMu.Unlock()
}

// upstreamSnapshot is one immutable upstream configuration. Secrets are kept
// here so a later SetUpstream that only changes the default address keeps the
// configured passwords.
type upstreamSnapshot struct {
	settings upstream.Settings
	secrets  map[string]string
	cfg      *upstream.Config
}

func (u *upstreamSnapshot) resolve(req *http.Request, address string) ([]proxy.UpstreamProxy, error) {
	host, port := splitHostPort(address)
	decision, err := u.cfg.ResolveContext(req.Context(), upstreamURL(req, address), host, port)
	if err != nil {
		return nil, err
	}
	out := make([]proxy.UpstreamProxy, len(decision.Candidates))
	for i, candidate := range decision.Candidates {
		out[i].Reason = candidate.Reason
		if candidate.Proxy != nil {
			out[i].URL = candidate.Proxy.URL
		}
	}
	return out, nil
}

func splitHostPort(address string) (string, int) {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return strings.Trim(address, "[]"), 0
	}
	port, _ := strconv.Atoi(rawPort)
	return host, port
}

// upstreamURL is what a PAC script sees. A request rewritten to another
// address keeps no path from the original host.
func upstreamURL(req *http.Request, address string) string {
	scheme := "http"
	if req.Method == http.MethodConnect {
		scheme = "https"
	}
	if req.URL != nil {
		if req.Method != http.MethodConnect {
			switch req.URL.Scheme {
			case "https", "wss":
				scheme = "https"
			case "http", "ws":
				scheme = "http"
			}
		}
		if req.URL.Host != "" {
			if host, _, err := net.SplitHostPort(address); err == nil && strings.EqualFold(req.URL.Hostname(), host) {
				u := *req.URL
				switch {
				case req.Method == http.MethodConnect:
					u.Scheme = "https"
				case u.Scheme == "ws":
					u.Scheme = "http"
				case u.Scheme == "wss":
					u.Scheme = "https"
				case u.Scheme == "":
					u.Scheme = scheme
				}
				return u.String()
			}
		}
	}
	return scheme + "://" + address + "/"
}

// SetUpstreamConfig replaces the host rules, bypass list, PAC script and
// default upstream. Passwords are supplied separately and are never part of
// the settings. Invalid configuration leaves the running one unchanged.
func (s *Server) SetUpstreamConfig(settings upstream.Settings, passwords map[string]string) error {
	settings = settings.Normalize()
	cfg, err := upstream.New(settings, passwords)
	if err != nil {
		return err
	}
	s.upstream.Store(&upstreamSnapshot{settings: settings.Clone(), secrets: maps.Clone(passwords), cfg: cfg})
	return nil
}

// SetUpstream sends every request through another proxy, as an empty string
// turns rules and PAC off too. The address needs a scheme and a port, such as
// http://127.0.0.1:8080.
func (s *Server) SetUpstream(raw string) error {
	snapshot, _ := s.upstream.Load().(*upstreamSnapshot)
	settings := upstream.Settings{}
	var secrets map[string]string
	if snapshot != nil {
		settings, secrets = snapshot.settings.Clone(), maps.Clone(snapshot.secrets)
	}
	settings.Default, settings.Rules, settings.PAC = strings.TrimSpace(raw), nil, ""
	return s.SetUpstreamConfig(settings, secrets)
}

// Upstream is the default proxy, or empty.
func (s *Server) Upstream() string {
	snapshot, _ := s.upstream.Load().(*upstreamSnapshot)
	if snapshot == nil {
		return ""
	}
	return snapshot.settings.Default
}

// UpstreamConfig returns the active host rules, bypass list, PAC source and
// default upstream, without passwords.
func (s *Server) UpstreamConfig() upstream.Settings {
	snapshot, _ := s.upstream.Load().(*upstreamSnapshot)
	if snapshot == nil {
		return upstream.Settings{}
	}
	return snapshot.settings.Clone()
}

// ScriptErrors counts the hooks that failed or timed out.
func (s *Server) ScriptErrors() int64 { return s.scriptErrors.Load() }

// SetBreak turns breakpoints on or off. With them off, no request pauses.
func (s *Server) SetBreak(on bool) {
	s.breakOn.Store(on)
	if !on {
		s.bpMu.Lock()
		for id, ch := range s.bpWait {
			ch <- Decision{Original: true}
			delete(s.bpWait, id)
		}
		s.bpMu.Unlock()
	}
}

// Breaking reports whether breakpoints are on.
func (s *Server) Breaking() bool { return s.breakOn.Load() }

// ReserveIDs claims n consecutive IDs from the capture sequence, so imported
// rows cannot collide with later live traffic. It returns the first reserved
// ID, or 0 when n is not positive. The capture engine allocates live flows
// from the same counter.
func (s *Server) ReserveIDs(n int) int {
	if s == nil || n <= 0 {
		return 0
	}
	first := s.nextID.Add(int64(n)) - int64(n) + 1
	return int(first)
}

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
	r, ok := s.breakRule(s.ruleRequest(f), phase)
	if !ok {
		return nil
	}
	s.hit(r)

	bp := Breakpoint{
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
	pausedAt := time.Now()
	d := s.waitBreakpoint(context.Background(), bp)
	// The user's time at the breakpoint is not network time.
	s.mu.Lock()
	if start, ok := s.started[f.Id]; ok {
		s.started[f.Id] = start.Add(time.Since(pausedAt))
	}
	s.mu.Unlock()
	return d
}

func (s *Server) waitBreakpoint(ctx context.Context, bp Breakpoint) *Decision {
	id := int(s.bpNext.Add(1))
	ch := make(chan Decision, 1)
	done := make(chan struct{})
	bp.ID, bp.Done = id, done
	defer close(done)
	s.bpMu.Lock()
	if !s.breakOn.Load() || ctx.Err() != nil {
		s.bpMu.Unlock()
		return nil
	}
	s.bpWait[id] = ch
	s.bpMu.Unlock()
	defer func() {
		s.bpMu.Lock()
		delete(s.bpWait, id)
		s.bpMu.Unlock()
	}()
	select {
	case s.breakpoints <- bp:
	default:
		return nil
	}
	select {
	case decided := <-ch:
		if !decided.Original {
			return &decided
		}
	case <-ctx.Done():
	case <-s.closed:
	case <-time.After(2 * time.Minute):
	}
	return nil
}

// ruleRequest is what the rules look at in an exchange, as it is now: after
// a Map Remote rule, the URL it was sent to.
func (s *Server) ruleRequest(f *proxy.Flow) rules.Request {
	req := rules.Request{URL: f.Request.URL.String(), Method: f.Request.Method, Host: f.Request.URL.Hostname(),
		App: s.clientProcess(f).Name, Header: f.Request.Header}
	s.rulesMu.RLock()
	needed := slices.ContainsFunc(s.rules, func(r Rule) bool { return r.Operation != "" && !r.Disabled && (r.Scenario == "" || r.ScenarioActive) })
	s.rulesMu.RUnlock()
	if !needed {
		return req
	}
	var body []byte
	if req.Method == http.MethodPost {
		// Streamed or oversized requests cannot supply verified operation names.
		// Bound decompression as well as the GraphQL parser; never inspect a prefix.
		if f.Request.Body == nil || len(f.Request.Body) > 1<<20 {
			return req
		}
		decoder, err := newDecoder(req.Header.Get("Content-Encoding"), bytes.NewReader(f.Request.Body))
		if err != nil {
			return req
		}
		defer decoder.Close()
		body, err = io.ReadAll(io.LimitReader(decoder, 1<<20+1))
		if err != nil || len(body) > 1<<20 {
			return req
		}
	}
	operations, err := graphql.RequestOperations(req.Method, req.URL, req.Header.Get("Content-Type"), string(body))
	if err == nil {
		for _, op := range operations {
			if op.Name != "" {
				req.Operations = append(req.Operations, op.Name)
			}
		}
	}
	return req
}

// breakRule is the first breakpoint rule that pauses an exchange in phase.
func (s *Server) breakRule(req rules.Request, phase Phase) (Rule, bool) {
	want := RuleBreak
	if phase == PhaseResponse {
		want = RuleBreakResponse
	}
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	for _, r := range s.rules {
		if r.Type == want && r.matches(req) {
			return r, true
		}
	}
	return Rule{}, false
}

// willBreakResponse reports whether a response for this flow will pause, so
// the Response hook leaves publishing to the stream modifier.
func (s *Server) willBreakResponse(f *proxy.Flow) bool {
	if !s.breakOn.Load() || f.Request == nil {
		return false
	}
	_, ok := s.breakRule(s.ruleRequest(f), PhaseResponse)
	return ok
}

// ParseRules reads and validates rules; no data is no rules.
func ParseRules(data []byte) ([]Rule, error) {
	var rules []Rule
	if len(data) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, err
	}
	if err := ValidateRules(rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// LoadRules reads a rules file. A missing file is no rules, not an error.
func LoadRules(path string) ([]Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	rules, err := ParseRules(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rules, nil
}

// Start binds its own listener synchronously, then serves it in the background.
// It never mistakes another application's listener for this proxy.
func (s *Server) Start() error {
	s.listenMu.Lock()
	select {
	case <-s.closed:
		s.listenMu.Unlock()
		return net.ErrClosed
	default:
	}
	if s.listener != nil {
		s.listenMu.Unlock()
		return errors.New("proxy already started")
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil && s.autoPort {
		var fallbackErr error
		ln, fallbackErr = net.Listen("tcp", "127.0.0.1:0")
		if fallbackErr != nil {
			s.listenMu.Unlock()
			return fmt.Errorf("preferred proxy address: %w; automatic port: %w", err, fallbackErr)
		}
	} else if err != nil {
		s.listenMu.Unlock()
		return err
	}
	s.listener, s.addr = ln, ln.Addr().String()
	s.running.Store(true)
	go func() {
		_ = s.proxy.Serve(ln)
		s.running.Store(false)
	}()
	// The extra entries belong to this listener's lifetime: start them while
	// the listener lock is held, so a concurrent Close cannot miss them.
	// Reverse proxies bind first, so an automatic SOCKS5 port cannot land on
	// one of their fixed ports.
	s.startReverseProxies(s.addr, "")
	s.startSOCKS()
	s.listenMu.Unlock()
	return nil
}

// Close stops the proxy and closes Flows.
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.listenMu.Lock()
		s.socketMu.Lock()
		close(s.closed)
		s.socketMu.Unlock()
		s.running.Store(false)
		// The extra entries are this server's own listeners and connections.
		s.stopSOCKS()
		s.stopReverseProxies()
		// net/http does not own hijacked WebSocket/CONNECT sockets.
		s.DropConnections()
		s.socketWG.Wait()
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
	source           int
	run              string
	withoutRecording bool
}

// ReplayWithCookies uses the request workspace's cookie jar, including across
// redirects. Ordinary capture replay deliberately does not inherit login state.
// Regression checks stop at the original response instead of following redirects.
func (s *Server) ReplayWithCookies(ctx context.Context, f Flow, jar http.CookieJar, followRedirects bool) (Flow, error) {
	policy := TransferPolicy{}
	if !followRedirects {
		policy.Redirects.Mode = RedirectManual
	}
	return s.ReplayWithPolicy(ctx, f, jar, policy)
}

// RedirectPolicy keeps imported cURL transfer behavior distinct from ordinary
// workspace sends. The zero value follows Go's standard ten requests. Max is
// the number of redirects to follow; zero keeps the mode's default: 10, or
// 50 for cURL's -L.
type RedirectPolicy struct {
	Mode       string `json:"mode,omitempty"`
	Max        int    `json:"max,omitempty"`
	KeepMethod bool   `json:"keepMethod,omitzero"`
	// DropContentType marks curl's implicit form Content-Type, not a -H header.
	// It disappears with the body; explicitly authored headers survive redirects.
	DropContentType bool `json:"dropContentType,omitzero"`
}

const (
	RedirectDefault = ""
	RedirectManual  = "manual"
	RedirectCurl    = "curl"
)

// Limit is the number of redirects to follow.
func (p RedirectPolicy) Limit() int {
	if p.Max > 0 {
		return p.Max
	}
	if p.Mode == RedirectCurl {
		return 50
	}
	return 10
}

// TransferPolicy is what an authored send asks of its HTTP transfer beyond the
// request itself: its redirects and how it treats the server certificate.
// Capture replay keeps the zero value: the proxy accepts an intercepted
// upstream's certificate, and the replay client verifies a tunneled one.
type TransferPolicy struct {
	Redirects RedirectPolicy
	// TLS is TLSDefault (the capture engine's inspection behavior), TLSVerify,
	// or TLSInsecure. Authored requests default to TLSVerify.
	TLS string
}

const (
	TLSDefault  = ""
	TLSVerify   = "verify"
	TLSInsecure = "insecure"
)

// ReplayWithPolicy applies an authored transfer policy. cURL modes follow at
// most 50 HTTP(S) hops unless Max says otherwise; -X keeps the method, not a
// body dropped by 301/302/303. TLSVerify asks the proxy to verify an
// intercepted upstream certificate and the client to verify a tunneled one;
// TLSInsecure accepts any certificate on both legs.
func (s *Server) ReplayWithPolicy(ctx context.Context, f Flow, jar http.CookieJar, policy TransferPolicy) (Flow, error) {
	switch policy.Redirects.Mode {
	case RedirectDefault, RedirectManual, RedirectCurl:
	default:
		return Flow{}, errors.New("unsupported request redirect policy")
	}
	switch policy.TLS {
	case TLSDefault, TLSVerify, TLSInsecure:
	default:
		return Flow{}, errors.New("unsupported request TLS policy")
	}
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
	// A captured Host header is sent as the request's Host, which Go's
	// transport takes from Request.Host rather than Header. Ordinary proxy
	// captures have none: their URL names the host.
	if host := req.Header.Get("Host"); host != "" {
		req.Host = host
		req.Header.Del("Host")
	}

	proxyURL := &url.URL{Scheme: "http", Host: s.Addr()}
	suppressed, _ := ctx.Value(withoutRecordingKey{}).(bool)
	var addresses []string
	finished := false // guarded by s.mu, including late transport dials
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		finished = true
		if !suppressed {
			for _, addr := range addresses {
				delete(s.replayClients, addr)
			}
		}
		// Suppression must survive a canceled send until the proxy has read
		// everything already sent on its socket. ClientDisconnected removes it.
	}()
	base := &http.Transport{
		// The proxy pins its upstream to each client connection. Redirects
		// must not reuse that socket for a different HTTP origin.
		DisableKeepAlives: true,
		Proxy:             http.ProxyURL(proxyURL),
		// An intercepted upstream is verified by the proxy, not by this
		// client, which only sees the proxy's own certificate.
		GetProxyConnectHeader: func(context.Context, *url.URL, string) (http.Header, error) {
			if policy.TLS != TLSVerify {
				return nil, nil
			}
			return http.Header{proxy.HeaderVerifyUpstream: {"1"}}, nil
		},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err == nil && (f.ID != 0 || f.DebugRun != "" || suppressed) {
				// Register before the transport can send anything. Each send
				// owns its transport, so concurrent identical URLs cannot mix.
				s.mu.Lock()
				select {
				case <-s.closed:
					finished = true
				default:
				}
				if finished {
					s.mu.Unlock()
					_ = conn.Close()
					return nil, net.ErrClosed
				}
				local := conn.LocalAddr().String()
				s.replayClients[local] = replayOrigin{source: f.ID, run: f.DebugRun, withoutRecording: suppressed}
				addresses = append(addresses, local)
				s.mu.Unlock()
			}
			return conn, err
		},
	}
	client := &http.Client{
		Jar: jar,
		// The transport builds TLS per request, so a redirect to another host
		// presents that host's certificate, never the first host's.
		Transport: s.certificates().Transport(base, s.CACertificate(), policy.TLS == TLSInsecure),
	}
	if policy.Redirects.Mode == RedirectManual {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	} else if policy.Redirects.Mode == RedirectCurl {
		bodyDropped := false
		client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
			if limit := policy.Redirects.Limit(); len(via) > limit {
				return fmt.Errorf("stopped after %d redirects", limit)
			}
			if next.URL.Scheme != "http" && next.URL.Scheme != "https" {
				return errors.New("redirects require HTTP(S)")
			}
			if policy.Redirects.KeepMethod {
				next.Method = req.Method
			}
			switch next.Response.StatusCode {
			case 301, 302, 303:
				bodyDropped = true
			}
			for _, name := range []string{"Content-Type", "Content-Encoding", "Content-Language", "Content-Location"} {
				if values := req.Header.Values(name); len(values) > 0 {
					next.Header[name] = slices.Clone(values)
				}
			}
			if bodyDropped && policy.Redirects.DropContentType && req.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
				next.Header.Del("Content-Type")
			}
			// curl does not enable automatic Referer generation without --auto-referer.
			next.Header.Del("Referer")
			if value := req.Header.Get("Referer"); value != "" {
				next.Header.Set("Referer", value)
			}
			// Bound Authorization to the original origin; literal cookies to the
			// original host (cookie domains do not include ports). Unlike Go's
			// defaults, never widen either to subdomains or restore stripped values.
			origin := func(u *url.URL) string {
				port := u.Port()
				if port == "" {
					port = "80"
					if u.Scheme == "https" {
						port = "443"
					}
				}
				return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
			}
			for _, previous := range via {
				if origin(previous.URL) != origin(next.URL) {
					next.Header.Del("Authorization")
				}
				if !strings.EqualFold(previous.URL.Hostname(), next.URL.Hostname()) {
					next.Header.Del("Cookie")
				}
			}
			return nil
		}
	} else {
		limit := policy.Redirects.Limit()
		client.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
			if len(via) > limit {
				return fmt.Errorf("stopped after %d redirects", limit)
			}
			return nil
		}
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
	// A reverse proxy names the remote in the request so the engine dials
	// and records it, and carries the local client's Host beside it when the
	// entry keeps that Host. Restore it before rules, scripts or the table
	// see the request; the marker never leaves this process.
	if a.server.reverseToken != "" && f.Request.Header.Get(reverseTokenHeader) == a.server.reverseToken {
		f.Request.Header.Del(reverseTokenHeader)
		if host := f.Request.Header.Get(reverseHostHeader); host != "" && f.Request.Method != http.MethodConnect {
			f.Request.Header.Set("Host", host)
		}
		f.Request.Header.Del(reverseHostHeader)
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
			if source.withoutRecording {
				go a.server.awaitUnrecorded(f.Id, f.Done())
			}
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
	start := time.Now()
	a.server.mu.Lock()
	a.server.started[f.Id] = start
	a.server.mu.Unlock()
	startTiming(f, start)
	a.server.startDebug(f)
	noCacheRequest(f)
	if isWebSocket(f.Request.URL) {
		// The engine dials a WebSocket's server after this hook, where the
		// URL says: a rule changes it here or not at all.
		a.server.applyWebSocketRules(f)
		return
	}
	// The engine streams a gRPC call instead of buffering it (it sets Stream
	// before this hook), so it never calls Request for one: apply the rules
	// here, while the URL and the headers are still the proxy's to change and
	// a rule may answer the call itself. A breakpoint or a script that edits
	// the body cannot, since the client is still sending it.
	if f.Stream {
		if a.server.applyNetwork(f) {
			return
		}
		if resp := a.server.applyRules(f); resp != nil {
			a.server.answer(f, resp)
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
	if a.server.applyNetwork(f) {
		return
	}
	a.server.runRequestScripts(f)
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
		// A rule that answers the request itself short-circuits the engine,
		// which then never calls Response: publish it here instead.
		a.server.answer(f, resp)
	}
}

func (a *addon) WebSocketStart(f *proxy.Flow) {
	s := a.server
	s.socketMu.Lock()
	select {
	case <-s.closed:
		s.socketMu.Unlock()
		f.ConnContext.ClientConn.Conn.Close()
		return
	default:
	}
	if s.sockets == nil {
		s.sockets = make(map[uuid.UUID]bool)
	}
	s.sockets[f.Id] = true
	s.socketWG.Add(1)
	s.socketMu.Unlock()
	a.server.publishFlow(f, "ws", nil, http.StatusSwitchingProtocols)
}

func (a *addon) WebSocketMessage(ctx context.Context, f *proxy.Flow, m *proxy.WebSocketMessage) {
	a.server.interceptMessage(ctx, f, m)
	direction := "in"
	if m.FromClient {
		direction = "out"
	}
	kind := wsFrameKind(m.Type)
	if m.Drop {
		kind = "dropped " + kind
	}
	a.server.emitFrame(f, frameInput{
		Direction: direction,
		Kind:      kind,
		Data:      m.Content,
		At:        m.Timestamp,
	})
}

func (a *addon) WebSocketEnd(f *proxy.Flow) {
	a.server.endFrames(f)
	s := a.server
	s.socketMu.Lock()
	active := s.sockets[f.Id]
	delete(s.sockets, f.Id)
	s.socketMu.Unlock()
	if active {
		s.socketWG.Done()
	}
}

func (a *addon) SSEStart(f *proxy.Flow) {
	a.server.publishFlow(f, "sse", nil, 0)
}

func (a *addon) SSEMessage(f *proxy.Flow) {
	if f.SSE == nil || len(f.SSE.Events) == 0 {
		return
	}
	e := f.SSE.Events[len(f.SSE.Events)-1]
	a.server.emitFrame(f, frameInput{
		Direction: "in",
		Kind:      "event",
		Data:      []byte(e.Data),
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
	if scriptsResponse(f) || a.server.willBreakResponse(f) {
		return
	}
	a.server.publish(f)
}

// StreamRequestModifier keeps a request body the engine streams instead of
// buffering, and paces what goes upstream.
func (a *addon) StreamRequestModifier(f *proxy.Flow, in io.Reader) io.Reader {
	noCacheRequest(f)
	if a.server.applyNetwork(f) {
		return in
	}
	// An oversized upload skipped Request. A matching Code Mock must fail
	// locally rather than accidentally forwarding a request it cannot read.
	if f.Stream && a.server.streamCodeMock(f) {
		return in
	}
	// The engine skipped Request for a body past the buffering threshold, so
	// this reader is all of it, the buffered prefix included: keep it.
	if f.Stream && f.Request != nil && f.Request.Body == nil {
		in = a.server.captureStream(f, "req", in, contentEncoding(f.Request.Header), requestLength(f.Request))
	}
	return a.server.throttleUp(f, in)
}

// StreamResponseModifier runs the response script on the body before the
// client gets it, and publishes the flow after the edit.
func (a *addon) StreamResponseModifier(f *proxy.Flow, in io.Reader) io.Reader {
	noCacheResponse(f)
	if f.Response == nil {
		return a.server.throttleDown(f, in)
	}
	if f.Stream {
		// An SSE stream publishes its own flow and events; any other streamed
		// response is a body the engine never buffered, so keep it here and
		// publish the flow when it ends.
		if !isSSE(f.Response) {
			in = a.server.captureStream(f, "res", in, contentEncoding(f.Response.Header), declaredLength(f.Response.Header))
		}
		return a.server.throttleDown(f, in)
	}
	scripted := scriptsResponse(f)
	breaking := a.server.willBreakResponse(f)
	if !scripted && !breaking {
		return a.server.throttleDown(f, in)
	}
	edited, bodyEdited := false, false
	// A body the engine cannot decode is left to the breakpoint alone.
	if body, err := f.Response.DecodedBody(); scripted && err == nil {
		edited, bodyEdited = a.server.runResponseScripts(f, body)
	}
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
		return a.server.throttleDown(f, in)
	}
	if bodyEdited {
		clearBodyEncoding(f.Response.Header)
	}
	f.Response.Header.Set("Content-Length", strconv.Itoa(len(f.Response.Body)))
	a.server.publish(f)
	// reply writes the returned reader and response.Body both: keep the
	// reader, which carries the edit, and clear the field.
	body := f.Response.Body
	f.Response.Body = nil
	return a.server.throttleDown(f, bytes.NewReader(body))
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
	if i := firstResponseRule(rules, s.ruleRequest(f)); i >= 0 {
		r := rules[i]
		file := r.Target
		if r.Type == RuleMapLocal {
			var ok bool
			if file, ok = localFile(r, f.Request.URL); !ok {
				return nil // a folder without the file: the server answers
			}
		}
		if r.Scenario != "" {
			recordAction(f, "Scenario · "+r.Scenario)
		}
		if r.Type != RuleMapRemote {
			s.hit(r) // Map Remote counts once its target is known to work
		}
		switch r.Type {
		case RuleBlock:
			switch r.Block {
			case BlockHide:
				if d := debugOf(f); d != nil {
					d.hidden = true
				}
				return nil
			case BlockDrop:
				recordAction(f, "Block · drop connection · "+r.Match)
				f.Error = errDropped
				if d := debugOf(f); d != nil {
					d.drop = true
				}
				if f.ConnContext != nil {
					f.ConnContext.RecordEvent("Rule", "Dropping the client connection", errDropped)
				}
				// The client never reads this: answer turns it into a closed
				// connection once the row is published.
				return &proxy.Response{
					StatusCode: http.StatusBadGateway,
					Header:     http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
					Body:       []byte("vscroxy: " + errDropped.Error() + "\n"),
				}
			}
			recordAction(f, "Block · "+r.Match)
			return &proxy.Response{
				StatusCode: http.StatusForbidden,
				Header:     http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
				Body:       []byte("blocked by vscroxy\n"),
			}
		case RuleMapLocal:
			recordAction(f, "Map Local · "+r.Match+" → "+file)
			body, err := readMockBody(file)
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
			StripHopHeaders(headers)
			if headers.Get("Content-Type") == "" {
				headers.Set("Content-Type", contentType(file))
			}
			headers.Set("Content-Length", strconv.Itoa(len(body)))
			return &proxy.Response{
				StatusCode: status,
				Header:     headers,
				Body:       body,
			}
		case RuleCodeMock:
			return s.codeMock(f, r)
		case RuleMapRemote:
			s.mapRemote(f, r)
			return nil
		}
	}
	return nil
}

// localFile is the file a Map Local rule answers u with: its target, or for a
// folder the file at u's path, after PathFrom, inside it (index.html for a
// path ending in /). ok is false when the folder has no such file.
func localFile(r Rule, u *url.URL) (file string, ok bool) {
	info, err := os.Stat(r.Target)
	if err != nil || !info.IsDir() {
		return r.Target, true // a missing file is the rule's error to report
	}
	p := u.Path
	if rest, cut := strings.CutPrefix(p, r.PathFrom); cut && r.PathFrom != "" {
		p = "/" + strings.TrimPrefix(rest, "/")
	}
	if strings.HasSuffix(p, "/") {
		p += "index.html"
	}
	// Clean drops every .. against the root: the file stays in the folder.
	file = filepath.Join(r.Target, filepath.FromSlash(path.Clean("/"+p)))
	info, err = os.Stat(file)
	return file, err == nil && info.Mode().IsRegular()
}

// mapRemote sends the request where r says.
func (s *Server) mapRemote(f *proxy.Flow, r Rule) {
	u, err := mapURL(f.Request.URL, r)
	if err != nil {
		return
	}
	s.hit(r)
	recordAction(f, "Map Remote · "+r.Match+" → "+u.String())
	if r.KeepHost && f.Request.Header.Get("Host") == "" {
		host := f.Request.URL.Host
		if raw := f.Request.Raw(); raw != nil && raw.Host != "" {
			host = raw.Host
		}
		f.Request.Header.Set("Host", host)
	}
	// Keep the URL the client asked for: the table shows that one, and a
	// replay sends it again.
	s.mu.Lock()
	s.original[f.Id] = *f.Request.URL
	s.mu.Unlock()
	f.Request.URL = u
}

// applyWebSocketRules sends a WebSocket handshake, whose URL is ws or wss,
// where the first matching Map Remote rule says.
func (s *Server) applyWebSocketRules(f *proxy.Flow) {
	s.rulesMu.RLock()
	list := s.rules
	s.rulesMu.RUnlock()
	if i := firstResponseRule(list, s.ruleRequest(f)); i >= 0 {
		s.mapRemote(f, list[i])
	}
}

func isWebSocket(u *url.URL) bool { return u.Scheme == "ws" || u.Scheme == "wss" }

// mapURL points a URL at r's target: its scheme (ws or wss for a WebSocket)
// and host, its path rewritten by PathFrom and PathTo and then put after the
// target's own path, and the target's query, if it has one, in place of the
// URL's.
func mapURL(orig *url.URL, r Rule) (*url.URL, error) {
	t, err := url.Parse(r.Target)
	if err != nil {
		return nil, err
	}
	if t.Scheme == "" || t.Host == "" {
		return nil, fmt.Errorf("map remote target %q needs a scheme and a host", r.Target)
	}
	u := *orig
	u.Scheme, u.Host = t.Scheme, t.Host
	if isWebSocket(orig) {
		u.Scheme = map[string]string{"http": "ws", "https": "wss"}[t.Scheme]
	}
	path := orig.EscapedPath()
	if rest, ok := strings.CutPrefix(path, r.PathFrom); ok && r.PathFrom != "" {
		path = r.PathTo + rest
	}
	if t.Path != "" && t.Path != "/" {
		path = strings.TrimRight(t.EscapedPath(), "/") + path
	}
	if u.Path, err = url.PathUnescape(path); err != nil {
		return nil, err
	}
	u.RawPath = path
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
			delete(s.replayClients, addr.remote)
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

// answer publishes a response a rule gave in place of the server's, and
// closes the client's connection when the rule drops it instead.
func (s *Server) answer(f *proxy.Flow, resp *proxy.Response) {
	f.Response = resp
	s.publish(f)
	if d := debugOf(f); d != nil && d.drop && f.ConnContext != nil && f.ConnContext.ClientConn != nil && f.ConnContext.ClientConn.Conn != nil {
		_ = f.ConnContext.ClientConn.Conn.Close()
	} else if f.Error == nil && f.Response != nil {
		// Local answers skip the response modifier. Keep their body before
		// moving it into the same paced download reader used upstream.
		f.Response.BodyReader = s.throttleDown(f, f.Response.BodyReader)
	}
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
	if recordingSuppressed(f) {
		return 0
	}
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
	if d := debugOf(f); s.ignored(display.Hostname()) || d != nil && d.hidden {
		streamed.release()
		return 0
	}
	client := s.clientProcess(f)
	if s.ignoredApp(client.Name) || !s.Allow().allows(display.Hostname(), client.Name) {
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
		// Buffered downloads publish before their paced write: include that
		// estimate using this exchange's conditions. Upload and latency have
		// already elapsed. Streamed transfers include their actual read time.
		t := s.flowThrottle(f)
		if f.Error == nil && t.DownPerSec > 0 && f.Response != nil {
			millis += int(float64(len(f.Response.Body)) / float64(t.DownPerSec) * 1000)
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
		Timing:            snapshotTiming(f, now, kind == "http" && f.Error == nil),
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
		Conn:              s.connInfo(f, start),
	}
	flow.DNSMapped, flow.HostProfile = s.dnsMapped(f)
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
	Remote    string // the IP address that connection reached
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
			if server.Conn != nil {
				if addr, ok := server.Conn.RemoteAddr().(*net.TCPAddr); ok {
					ci.Remote = addr.IP.String()
				}
			}
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

// Discard deletes spill files of flows the UI no longer keeps. Only files in
// this capture's own spill directory are removed, never a path from elsewhere.
func (s *Server) Discard(files []string) {
	if s.bodyDir == "" {
		return
	}
	for _, file := range files {
		if file != "" && filepath.Dir(file) == s.bodyDir {
			_ = os.Remove(file)
		}
	}
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
	if recordingSuppressed(f) {
		return
	}
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
	host := u.Host
	if h := r.Header.Get("Host"); h != "" {
		host = h
	}
	fmt.Fprintf(&b, "Host: %s\n", host)
	for _, key := range slices.Sorted(maps.Keys(r.Header)) {
		if strings.EqualFold(key, "Host") {
			continue
		}
		for _, value := range r.Header[key] {
			fmt.Fprintf(&b, "%s: %s\n", key, value)
		}
	}
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

// dnsMapped is the mapping a flow's dial went through, and the profile and
// line it came from, once.
func (s *Server) dnsMapped(f *proxy.Flow) (mapped, profile string) {
	s.mu.Lock()
	hit, ok := s.dnsHits[f.Id]
	delete(s.dnsHits, f.Id)
	s.mu.Unlock()
	if !ok {
		return "", ""
	}
	return hit.Name + " → " + hit.IP.String(), fmt.Sprintf("%s:%d", hit.Profile, hit.Line)
}
