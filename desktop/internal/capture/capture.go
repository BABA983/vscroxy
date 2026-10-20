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

	"github.com/google/uuid"
	"github.com/lqqyt2423/go-mitmproxy/proxy"

	"github.com/baba983/vscroxy/desktop/internal/procinfo"
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
	// maxFrameBytes what one message keeps.
	maxFrames     = 200
	maxFrameBytes = 4 << 10
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
	ID     int
	Method string
	Scheme string
	Host   string
	Path   string
	URL    string
	Status int
	Bytes  int
	Millis int

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

	// At is when the exchange finished; ResponseHeader the structured
	// response headers, for an export.
	At             time.Time
	ResponseHeader http.Header

	// RequestHeader is the structured request headers, for a replay.
	RequestHeader http.Header

	// Kind is "http", "ws" or "sse"; Frames carries the messages of the
	// last two.
	Kind   string
	Frames []Frame

	// Process is the app the connection came from, when it could be told.
	Process string

	// Wait is how long the request took to reach its first response byte,
	// and Download the rest of Millis: the two phases a waterfall shows.
	// Both are zero when the exchange never reached a server.
	Wait     int
	Download int
}

// FrameEvent is a frame that arrived after its flow was published.
type FrameEvent struct {
	FlowID int
	Frame  Frame
}

// Frame is one WebSocket message or SSE event of a flow.
type Frame struct {
	Direction string // "out" for client to server, "in" for server to client
	Kind      string // "text", "binary", "event" or the WebSocket frame type
	Data      string
	At        time.Time
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

// Rule rewrites requests whose URL contains Match, in order, first match
// wins. Target is a base URL for map_remote and a file for map_local.
type Rule struct {
	Type   RuleType `json:"type"`
	Match  string   `json:"match"`
	Target string   `json:"target,omitempty"`
	Status int      `json:"status,omitempty"`
}

// Phase is where a breakpoint paused an exchange.
type Phase int

const (
	PhaseRequest Phase = iota
	PhaseResponse
)

// Breakpoint is an exchange paused for the UI to edit.
type Breakpoint struct {
	ID     int
	Phase  Phase
	Method string
	URL    string
	Status int
	Body   string
}

// Decision is what the UI does with a paused exchange.
type Decision struct {
	Abort  bool
	Method string
	URL    string
	Status int
	Body   string
}

// Server is the running MITM proxy. Start it once; Flows drains what it
// captures.
type Server struct {
	proxy   *proxy.Proxy
	addr    string
	out     chan Flow
	dropped atomic.Int64
	nextID  atomic.Int64

	mu        sync.Mutex
	started   map[uuid.UUID]time.Time
	firstByte map[uuid.UUID]time.Time
	original  map[uuid.UUID]url.URL
	clients   map[string]string
	closeOnce sync.Once

	// bodyDir holds the bodies too large to keep in memory. It is removed
	// when the server closes.
	bodyDir string

	// roots are the CAs a replay trusts: the system's and this proxy's, so
	// a replay of an HTTPS flow succeeds without trusting the CA machine
	// wide.
	roots *x509.CertPool

	rulesMu sync.RWMutex
	rules   []Rule

	frames  chan FrameEvent
	flowIDs map[uuid.UUID]int

	scriptMu     sync.RWMutex
	script       *Script
	scriptErrors atomic.Int64

	throttleMu sync.RWMutex
	throttle   Throttle

	upstream   atomic.Value // string
	exclusions atomic.Value // []string
	ignoreList atomic.Value // []string

	breakOn     atomic.Bool
	bpNext      atomic.Int64
	bpMu        sync.Mutex
	bpWait      map[int]chan Decision
	breakpoints chan Breakpoint
}

// New builds a proxy listening on addr, with its CA under caRootPath, or in
// ~/.mitmproxy when that is empty as mitmproxy does.
func New(addr, caRootPath string) (*Server, error) {
	if addr == "" {
		addr = "127.0.0.1:7890"
	}
	s := &Server{
		addr:        addr,
		out:         make(chan Flow, queue),
		started:     make(map[uuid.UUID]time.Time),
		firstByte:   make(map[uuid.UUID]time.Time),
		original:    make(map[uuid.UUID]url.URL),
		clients:     make(map[string]string),
		bpWait:      make(map[int]chan Decision),
		breakpoints: make(chan Breakpoint, 16),
		frames:      make(chan FrameEvent, queue),
		flowIDs:     make(map[uuid.UUID]int),
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
		return !s.excluded(requestHost(req))
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
	return s, nil
}

// Addr is the address the proxy listens on.
func (s *Server) Addr() string { return s.addr }

// Flows is the stream of captured exchanges. It closes when the server does.
func (s *Server) Flows() <-chan Flow { return s.out }

// Dropped counts the flows the UI could not keep up with.
func (s *Server) Dropped() int64 { return s.dropped.Load() }

// SetExclusions sets the hosts the proxy tunnels without decrypting them, so
// an app that pins its certificates keeps working. A pattern is a host, a
// host with a leading dot or a path.Match pattern such as *.example.com.
func (s *Server) SetExclusions(patterns []string) {
	s.exclusions.Store(slices.Clone(patterns))
}

// Exclusions lists the hosts the proxy does not decrypt.
func (s *Server) Exclusions() []string {
	patterns, _ := s.exclusions.Load().([]string)
	return patterns
}

// excluded reports whether host is one the proxy tunnels.
func (s *Server) excluded(host string) bool {
	return hostMatches(host, s.Exclusions())
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
	return LoadExclusions(path)
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

// LoadExclusions reads the hosts to tunnel from a file, one per line, where a
// line starting with # is a comment.
func LoadExclusions(path string) ([]string, error) {
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

// SetRules replaces the rules the proxy applies to new requests.
func (s *Server) SetRules(rules []Rule) {
	s.rulesMu.Lock()
	s.rules = slices.Clone(rules)
	s.rulesMu.Unlock()
}

// Rules returns the rules in force.
func (s *Server) Rules() []Rule {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	return slices.Clone(s.rules)
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
	if !s.matchesBreak(raw, phase) {
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
		bp.Body = bodyText(f.Response.DecodedBody())
	} else {
		bp.Body = bodyText(f.Request.DecodedBody())
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
		d = &decided
	case <-time.After(2 * time.Minute):
		s.bpMu.Lock()
		delete(s.bpWait, id)
		s.bpMu.Unlock()
	}
	// The user's time at the breakpoint is not network time: move the start
	// forward so the flow's duration stays the exchange's.
	s.mu.Lock()
	if start, ok := s.started[f.Id]; ok {
		s.started[f.Id] = start.Add(time.Since(pausedAt))
	}
	s.mu.Unlock()
	return d
}

func (s *Server) matchesBreak(raw string, phase Phase) bool {
	want := RuleBreak
	if phase == PhaseResponse {
		want = RuleBreakResponse
	}
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	for _, r := range s.rules {
		if r.Type == want && strings.Contains(raw, r.Match) {
			return true
		}
	}
	return false
}

// willBreakResponse reports whether a response for this flow will pause, so
// the Response hook leaves publishing to the stream modifier.
func (s *Server) willBreakResponse(f *proxy.Flow) bool {
	return s.breakOn.Load() && f.Request != nil && s.matchesBreak(f.Request.URL.String(), PhaseResponse)
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
	for i, r := range rules {
		switch r.Type {
		case RuleMapRemote, RuleMapLocal, RuleBlock, RuleBreak, RuleBreakResponse:
		default:
			return nil, fmt.Errorf("%s: rule %d has unknown type %q", path, i, r.Type)
		}
		if r.Match == "" {
			return nil, fmt.Errorf("%s: rule %d has no match", path, i)
		}
		if r.Type != RuleBlock && r.Type != RuleBreak && r.Type != RuleBreakResponse && r.Target == "" {
			return nil, fmt.Errorf("%s: rule %d has no target", path, i)
		}
	}
	return rules, nil
}

// Start listens and serves in the background, and returns once the address
// accepts connections.
func (s *Server) Start() error {
	done := make(chan error, 1)
	go func() { done <- s.proxy.Start() }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			return err
		default:
		}
		conn, err := net.DialTimeout("tcp", s.addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("proxy did not listen on %s", s.addr)
}

// Close stops the proxy and closes Flows.
func (s *Server) Close() error {
	err := s.proxy.Close()
	s.closeOnce.Do(func() {
		close(s.out)
		if s.bodyDir != "" {
			_ = os.RemoveAll(s.bodyDir)
		}
	})
	return err
}

// Replay sends the flow's request again through this proxy, so the exchange
// is captured like any other and can be compared with the original.
func (s *Server) Replay(ctx context.Context, f Flow) error {
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
		return err
	}
	for key, values := range f.RequestHeader {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	for _, header := range hopHeaders {
		req.Header.Del(header)
	}

	proxyURL := &url.URL{Scheme: "http", Host: s.addr}
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: s.roots},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
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
	a.server.mu.Lock()
	a.server.started[f.Id] = time.Now()
	a.server.mu.Unlock()
}

// Responseheaders notes when the response started, the end of the wait.
func (a *addon) Responseheaders(f *proxy.Flow) {
	a.server.mu.Lock()
	a.server.firstByte[f.Id] = time.Now()
	a.server.mu.Unlock()
}

func (a *addon) Request(f *proxy.Flow) {
	if f.Request == nil {
		return
	}
	if err := a.server.currentScript().applyRequest(f); err != nil {
		a.server.scriptErrors.Add(1)
	}
	if d := a.server.maybeBreak(f, PhaseRequest); d != nil {
		if d.Abort {
			f.Response = &proxy.Response{
				StatusCode: http.StatusBadGateway,
				Header:     http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
				Body:       []byte("aborted at a breakpoint\n"),
			}
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
		f.Request.Body = []byte(d.Body)
		f.Request.Header.Del("Content-Length")
	}
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
	a.server.emitFrame(f, Frame{
		Direction: direction,
		Kind:      wsFrameKind(m.Type),
		Data:      clipFrame(m.Content),
		At:        m.Timestamp,
	})
}

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
	a.server.emitFrame(f, Frame{
		Direction: "in",
		Kind:      cmp.Or(e.Event, "event"),
		Data:      clipFrame([]byte(data)),
		At:        e.Time,
	})
}

func (a *addon) Response(f *proxy.Flow) {
	if f.Response == nil || f.Stream || isSSE(f.Response) {
		a.server.publish(f)
		return
	}
	// A response script or breakpoint edits the body in
	// StreamResponseModifier, the only place the proxy still lets the reader
	// be replaced: publish there, once the edits are in.
	if a.server.currentScript().HasResponse() || a.server.willBreakResponse(f) {
		return
	}
	a.server.publish(f)
}

// StreamResponseModifier runs the response script on the body before the
// client gets it, and publishes the flow after the edit.
func (a *addon) StreamRequestModifier(f *proxy.Flow, in io.Reader) io.Reader {
	return a.server.throttleUp(in)
}

func (a *addon) StreamResponseModifier(f *proxy.Flow, in io.Reader) io.Reader {
	if f.Response == nil || f.Stream || isSSE(f.Response) {
		return a.server.throttleDown(in)
	}
	sc := a.server.currentScript()
	scripted := sc.HasResponse()
	breaking := a.server.willBreakResponse(f)
	if !scripted && !breaking {
		return a.server.throttleDown(in)
	}
	body, err := f.Response.DecodedBody()
	if err != nil || len(body) > maxScriptBody {
		a.server.publish(f)
		return a.server.throttleDown(in)
	}
	f.Response.Body = body
	if scripted {
		if err := sc.applyResponse(f); err != nil {
			a.server.scriptErrors.Add(1)
		}
	}
	if breaking {
		if d := a.server.maybeBreak(f, PhaseResponse); d != nil {
			applyResponseDecision(f, d)
		}
	}
	f.Response.Header.Del("Content-Encoding")
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
		f.Response.StatusCode = http.StatusBadGateway
		f.Response.Header = http.Header{"Content-Type": {"text/plain; charset=utf-8"}}
		f.Response.Body = []byte("aborted at a breakpoint\n")
		return
	}
	if d.Status > 0 {
		f.Response.StatusCode = d.Status
	}
	f.Response.Body = []byte(d.Body)
}

func isSSE(r *proxy.Response) bool {
	return strings.Contains(r.Header.Get("Content-Type"), "text/event-stream")
}

// RequestError and HTTPConnectError keep failed exchanges visible, since a
// refused connection is what the user is often looking for.
func (a *addon) RequestError(f *proxy.Flow, err error) {
	a.server.publish(f)
}

func (a *addon) HTTPConnectError(f *proxy.Flow, err error) {
	a.server.publish(f)
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
	for _, r := range rules {
		if !strings.Contains(raw, r.Match) {
			continue
		}
		switch r.Type {
		case RuleBlock:
			return &proxy.Response{
				StatusCode: http.StatusForbidden,
				Header:     http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
				Body:       []byte("blocked by vscroxy\n"),
			}
		case RuleMapLocal:
			body, err := os.ReadFile(r.Target)
			if err != nil {
				return &proxy.Response{
					StatusCode: http.StatusBadGateway,
					Header:     http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
					Body:       []byte("map local: " + err.Error() + "\n"),
				}
			}
			if len(body) > maxRuleBody {
				body = body[:maxRuleBody]
			}
			status := r.Status
			if status == 0 {
				status = http.StatusOK
			}
			return &proxy.Response{
				StatusCode: status,
				Header: http.Header{
					"Content-Type":   {contentType(r.Target)},
					"Content-Length": {fmt.Sprint(len(body))},
				},
				Body: body,
			}
		case RuleMapRemote:
			u, err := mapURL(f.Request.URL, r.Target)
			if err != nil {
				return nil
			}
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

// noteClient records which process opened or closed a client connection, so
// each flow on it can be attributed without another scan.
func (s *Server) noteClient(client *proxy.ClientConn, connected bool) {
	if client == nil || client.Conn == nil {
		return
	}
	addr := client.Conn.RemoteAddr().String()
	if !connected {
		s.mu.Lock()
		delete(s.clients, addr)
		s.mu.Unlock()
		return
	}
	name := procinfo.Lookup(addr)
	s.mu.Lock()
	if len(s.clients) > 1024 {
		clear(s.clients)
	}
	s.clients[addr] = name
	s.mu.Unlock()
}

// clientName returns the process behind the connection a flow arrived on.
func (s *Server) clientName(f *proxy.Flow) string {
	if f.ConnContext == nil || f.ConnContext.ClientConn == nil || f.ConnContext.ClientConn.Conn == nil {
		return ""
	}
	addr := f.ConnContext.ClientConn.Conn.RemoteAddr().String()
	s.mu.Lock()
	name, ok := s.clients[addr]
	s.mu.Unlock()
	if ok {
		return name
	}
	return procinfo.Lookup(addr)
}

func (s *Server) publish(f *proxy.Flow) {
	s.publishFlow(f, "http", nil, 0)
}

// publishFlow turns one exchange into the boundary Flow the UI shows. kind is
// "http", "ws" or "sse"; status overrides the response's when non-zero.
func (s *Server) publishFlow(f *proxy.Flow, kind string, frames []Frame, status int) int {
	if f.Request == nil {
		return 0
	}
	now := time.Now()
	s.mu.Lock()
	start, ok := s.started[f.Id]
	delete(s.started, f.Id)
	first, hasFirst := s.firstByte[f.Id]
	delete(s.firstByte, f.Id)
	display := f.Request.URL
	if orig, mapped := s.original[f.Id]; mapped {
		display = &orig
		delete(s.original, f.Id)
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
		return 0
	}
	reqBody, reqFile, reqSize := s.keepBody(f.Id, "req", reqData)
	size, respHeaders := 0, ""
	respBody, respFile, respSize := "", "", 0
	if f.Response != nil {
		size = len(f.Response.Body)
		respHeaders = formatResponse(f.Response)
		respData, err := f.Response.DecodedBody()
		if err != nil {
			respData = nil
		}
		respBody, respFile, respSize = s.keepBody(f.Id, "res", respData)
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
	millis := int(now.Sub(start).Milliseconds())
	if kind == "http" {
		// The throttle paces the body after this hook, so add what the
		// transfer will take: the flow's time should match the client's.
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
		ID:               int(s.nextID.Add(1)),
		Method:           method,
		Scheme:           display.Scheme,
		Host:             display.Host,
		Path:             display.RequestURI(),
		URL:              display.String(),
		Status:           status,
		Bytes:            size,
		Millis:           millis,
		Wait:             wait,
		Download:         download,
		RequestHeaders:   formatRequest(f.Request, display),
		ResponseHeaders:  respHeaders,
		RequestBody:      reqBody,
		RequestBodyFile:  reqFile,
		RequestBodySize:  reqSize,
		ResponseBody:     respBody,
		ResponseBodyFile: respFile,
		ResponseBodySize: respSize,
		RequestHeader:    f.Request.Header.Clone(),
		ResponseHeader:   responseHeader(f.Response),
		At:               now,
		Kind:             kind,
		Frames:           clipFrames(frames),
		Process:          s.clientName(f),
	}
	s.mu.Lock()
	if len(s.flowIDs) > 4096 {
		clear(s.flowIDs)
	}
	s.flowIDs[f.Id] = flow.ID
	s.mu.Unlock()
	select {
	case s.out <- flow:
	default:
		s.dropped.Add(1)
	}
	return flow.ID
}

// keepBody returns the body a flow keeps in memory, the file that holds the
// whole body when it was spilled, and the body's size. A spilled body keeps
// its head inline so search and the table work without touching the disk.
func (s *Server) keepBody(id uuid.UUID, kind string, body []byte) (head, file string, size int) {
	size = len(body)
	if size <= maxBody {
		return string(body), "", size
	}
	head = string(body[:maxBody])
	if s.bodyDir != "" {
		file = filepath.Join(s.bodyDir, kind+"-"+id.String()+".bin")
		if err := os.WriteFile(file, body, 0o600); err != nil {
			file = ""
		}
	}
	if file == "" {
		head += fmt.Sprintf("\n\n… truncated: %d of %d bytes shown", maxBody, size)
	}
	return head, file, size
}

// emitFrame sends a message that belongs to an already published flow.
func (s *Server) emitFrame(f *proxy.Flow, frame Frame) {
	s.mu.Lock()
	id, ok := s.flowIDs[f.Id]
	s.mu.Unlock()
	if !ok {
		return
	}
	select {
	case s.frames <- FrameEvent{FlowID: id, Frame: frame}:
	default:
		s.dropped.Add(1)
	}
}

func responseHeader(r *proxy.Response) http.Header {
	if r == nil {
		return nil
	}
	return r.Header.Clone()
}

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

func clipFrame(b []byte) string {
	if len(b) > maxFrameBytes {
		return string(b[:maxFrameBytes]) + "…"
	}
	return string(b)
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
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\n", r.StatusCode, http.StatusText(r.StatusCode))
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
