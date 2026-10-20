// Package capture runs the MITM proxy and turns what it sees into Flows for
// the UI. The engine is go-mitmproxy; this package owns the boundary, so the
// UI never imports the engine.
package capture

import (
	"bytes"
	"context"
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
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

const (
	// maxBody bounds what one flow keeps in memory. A larger body is cut and
	// marked, and the full body stays with the capture engine for now.
	maxBody = 1 << 20
	// maxRuleBody bounds what a map local rule serves from a file.
	maxRuleBody = 8 << 20
	// maxScriptBody bounds what a response script reads.
	maxScriptBody = 1 << 20
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
	RequestBody     string
	ResponseBody    string

	// RequestHeader is the structured request headers, for a replay.
	RequestHeader http.Header
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
	original  map[uuid.UUID]url.URL
	closeOnce sync.Once

	rulesMu sync.RWMutex
	rules   []Rule

	scriptMu     sync.RWMutex
	script       *Script
	scriptErrors atomic.Int64

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
		original:    make(map[uuid.UUID]url.URL),
		bpWait:      make(map[int]chan Decision),
		breakpoints: make(chan Breakpoint, 16),
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
	s.proxy = p
	return s, nil
}

// Addr is the address the proxy listens on.
func (s *Server) Addr() string { return s.addr }

// Flows is the stream of captured exchanges. It closes when the server does.
func (s *Server) Flows() <-chan Flow { return s.out }

// Dropped counts the flows the UI could not keep up with.
func (s *Server) Dropped() int64 { return s.dropped.Load() }

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

// ScriptErrors counts the hooks that failed or timed out.
func (s *Server) ScriptErrors() int64 { return s.scriptErrors.Load() }

// SetBreak turns breakpoints on or off. With them off, no request pauses.
func (s *Server) SetBreak(on bool) { s.breakOn.Store(on) }

// Breaking reports whether breakpoints are on.
func (s *Server) Breaking() bool { return s.breakOn.Load() }

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
	s.closeOnce.Do(func() { close(s.out) })
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
	if f.RequestBody != "" {
		body = strings.NewReader(f.RequestBody)
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
			Proxy: http.ProxyURL(proxyURL),
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

func (a *addon) Requestheaders(f *proxy.Flow) {
	a.server.mu.Lock()
	a.server.started[f.Id] = time.Now()
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
func (a *addon) StreamResponseModifier(f *proxy.Flow, in io.Reader) io.Reader {
	if f.Response == nil || f.Stream || isSSE(f.Response) {
		return in
	}
	sc := a.server.currentScript()
	scripted := sc.HasResponse()
	breaking := a.server.willBreakResponse(f)
	if !scripted && !breaking {
		return in
	}
	body, err := f.Response.DecodedBody()
	if err != nil || len(body) > maxScriptBody {
		a.server.publish(f)
		return in
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
	return bytes.NewReader(body)
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

func (s *Server) publish(f *proxy.Flow) {
	if f.Request == nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	start, ok := s.started[f.Id]
	delete(s.started, f.Id)
	display := f.Request.URL
	if orig, mapped := s.original[f.Id]; mapped {
		display = &orig
		delete(s.original, f.Id)
	}
	s.mu.Unlock()
	if !ok {
		start = f.StartTime
	}

	status, size, respHeaders, respBody := 0, 0, "", ""
	if f.Response != nil {
		status = f.Response.StatusCode
		size = len(f.Response.Body)
		respHeaders = formatResponse(f.Response)
		respBody = bodyText(f.Response.DecodedBody())
	}
	flow := Flow{
		ID:              int(s.nextID.Add(1)),
		Method:          f.Request.Method,
		Scheme:          display.Scheme,
		Host:            display.Host,
		Path:            display.RequestURI(),
		URL:             display.String(),
		Status:          status,
		Bytes:           size,
		Millis:          int(now.Sub(start).Milliseconds()),
		RequestHeaders:  formatRequest(f.Request, display),
		ResponseHeaders: respHeaders,
		RequestBody:     bodyText(f.Request.DecodedBody()),
		ResponseBody:    respBody,
		RequestHeader:   f.Request.Header.Clone(),
	}
	select {
	case s.out <- flow:
	default:
		s.dropped.Add(1)
	}
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
