// Package capture runs the MITM proxy and turns what it sees into Flows for
// the UI. The engine is go-mitmproxy; this package owns the boundary, so the
// UI never imports the engine.
package capture

import (
	"fmt"
	"maps"
	"net"
	"net/http"
	"slices"
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
	// queue bounds the flows waiting for the UI. A burst past it is dropped
	// and counted instead of growing without limit.
	queue = 1024
)

// Flow is one captured exchange: what the UI shows and what a replay would
// rebuild. It is the boundary type of the capture core.
type Flow struct {
	ID     int
	Method string
	Scheme string
	Host   string
	Path   string
	Status int
	Bytes  int
	Millis int

	RequestHeaders  string
	ResponseHeaders string
	RequestBody     string
	ResponseBody    string
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
	closeOnce sync.Once
}

// New builds a proxy listening on addr, with its CA under caRootPath, or in
// ~/.mitmproxy when that is empty as mitmproxy does.
func New(addr, caRootPath string) (*Server, error) {
	if addr == "" {
		addr = "127.0.0.1:7890"
	}
	s := &Server{
		addr:    addr,
		out:     make(chan Flow, queue),
		started: make(map[uuid.UUID]time.Time),
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

// addon records when a request started and publishes it once its response is
// complete.
type addon struct {
	proxy.BaseAddon
	server *Server
}

func (a *addon) Requestheaders(f *proxy.Flow) {
	a.server.mu.Lock()
	a.server.started[f.Id] = time.Now()
	a.server.mu.Unlock()
}

func (a *addon) Response(f *proxy.Flow) {
	a.server.publish(f)
}

// RequestError and HTTPConnectError keep failed exchanges visible, since a
// refused connection is what the user is often looking for.
func (a *addon) RequestError(f *proxy.Flow, err error) {
	a.server.publish(f)
}

func (a *addon) HTTPConnectError(f *proxy.Flow, err error) {
	a.server.publish(f)
}

func (s *Server) publish(f *proxy.Flow) {
	if f.Request == nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	start, ok := s.started[f.Id]
	delete(s.started, f.Id)
	s.mu.Unlock()
	if !ok {
		start = f.StartTime
	}

	status, respHeaders, respBody := 0, "", ""
	if f.Response != nil {
		status = f.Response.StatusCode
		respHeaders = formatResponse(f.Response)
		respBody = bodyText(f.Response.DecodedBody())
	}
	flow := Flow{
		ID:              int(s.nextID.Add(1)),
		Method:          f.Request.Method,
		Scheme:          f.Request.URL.Scheme,
		Host:            f.Request.URL.Host,
		Path:            f.Request.URL.RequestURI(),
		Status:          status,
		Bytes:           len(f.Response.Body),
		Millis:          int(now.Sub(start).Milliseconds()),
		RequestHeaders:  formatRequest(f.Request),
		ResponseHeaders: respHeaders,
		RequestBody:     bodyText(f.Request.DecodedBody()),
		ResponseBody:    respBody,
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

func formatRequest(r *proxy.Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s\n", r.Method, r.URL.RequestURI(), r.Proto)
	fmt.Fprintf(&b, "Host: %s\n", r.URL.Host)
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
