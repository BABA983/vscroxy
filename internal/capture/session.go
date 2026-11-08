package capture

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/lqqyt2423/go-mitmproxy/proxy"

	"github.com/baba983/vscroxy/internal/certs"
)

// ReplaySession sends requests through this proxy over one connection per
// origin. The proxy pins its upstream to the downstream connection, so a
// connection-oriented exchange such as NTLM's Type 1/2/3 challenge reaches the
// same server connection at every step. It is an http.RoundTripper: redirects,
// cookies and retries belong to the caller's http.Client.
//
// Each request is attributed to the flow's capture source like
// ReplayWithPolicy's, and Close releases the connections. A session created
// under WithoutRecording leaves its socket links to ClientDisconnected, as
// ReplayWithPolicy does, so a canceled load cannot leak traffic into Capture.
type ReplaySession struct {
	server     *Server
	flow       Flow
	policy     TransferPolicy
	suppressed bool

	mu         sync.Mutex
	closed     bool
	transports map[string]*http.Transport // scheme://host:port -> connection
	addresses  []string                   // proxy sockets registered for capture
}

// ReplaySession starts a session that sends through this proxy. A policy that
// names an unsupported redirect or TLS mode is refused here.
func (s *Server) ReplaySession(ctx context.Context, f Flow, policy TransferPolicy) (*ReplaySession, error) {
	if err := policy.validate(); err != nil {
		return nil, err
	}
	suppressed, _ := ctx.Value(withoutRecordingKey{}).(bool)
	return &ReplaySession{server: s, flow: f, policy: policy, suppressed: suppressed, transports: make(map[string]*http.Transport)}, nil
}

// RoundTrip sends req on the session's connection for its origin.
func (rs *ReplaySession) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil || req.URL.Host == "" {
		return nil, errors.New("replay needs an absolute HTTP(S) URL")
	}
	scheme := strings.ToLower(req.URL.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("replay supports HTTP(S) only")
	}
	transport, err := rs.transport(scheme, req.URL)
	if err != nil {
		return nil, err
	}
	return transport.RoundTrip(req)
}

// transport returns the connection to one origin, dialing it on first use.
func (rs *ReplaySession) transport(scheme string, u *url.URL) (*http.Transport, error) {
	address := certs.Address(u)
	if address == "" {
		return nil, errors.New("replay needs a URL with a host")
	}
	key := scheme + "://" + address
	rs.mu.Lock()
	existing := rs.transports[key]
	closed := rs.closed
	rs.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	if existing != nil {
		return existing, nil
	}

	proxyURL := &url.URL{Scheme: "http", Host: rs.server.Addr()}
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		// One connection to the proxy per origin: the proxy pins its
		// upstream to that downstream connection, which is what a
		// connection-oriented handshake needs. HTTP/2 would multiplex
		// requests and break that pairing.
		MaxConnsPerHost:   1,
		ForceAttemptHTTP2: false,
	}
	if scheme == "https" {
		config, err := rs.server.certificates().TLSConfig(address, rs.server.CACertificate(), rs.policy.TLS == TLSInsecure)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = config
	}
	// An intercepted upstream is verified by the proxy, not by this client,
	// which only sees the proxy's own certificate.
	if rs.policy.TLS == TLSVerify {
		transport.GetProxyConnectHeader = func(context.Context, *url.URL, string) (http.Header, error) {
			return http.Header{proxy.HeaderVerifyUpstream: {"1"}}, nil
		}
	}
	transport.DialContext = rs.dial

	rs.mu.Lock()
	if rs.closed {
		rs.mu.Unlock()
		transport.CloseIdleConnections()
		return nil, net.ErrClosed
	}
	if existing := rs.transports[key]; existing != nil {
		rs.mu.Unlock()
		transport.CloseIdleConnections()
		return existing, nil
	}
	rs.transports[key] = transport
	rs.mu.Unlock()
	return transport, nil
}

// dial connects to the proxy and registers the socket so the flows it carries
// link back to the send that made them.
func (rs *ReplaySession) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	f := rs.flow
	suppressed := rs.suppressed
	if f.ID == 0 && f.DebugRun == "" && !suppressed {
		return conn, nil
	}
	rs.mu.Lock()
	if rs.closed {
		rs.mu.Unlock()
		_ = conn.Close()
		return nil, net.ErrClosed
	}
	s := rs.server
	s.mu.Lock()
	select {
	case <-s.closed:
		s.mu.Unlock()
		rs.mu.Unlock()
		_ = conn.Close()
		return nil, net.ErrClosed
	default:
	}
	local := conn.LocalAddr().String()
	s.replayClients[local] = replayOrigin{source: f.ID, run: f.DebugRun, withoutRecording: suppressed}
	rs.addresses = append(rs.addresses, local)
	s.mu.Unlock()
	rs.mu.Unlock()
	return conn, nil
}

// Close releases the session's connections. It is safe to call more than once.
func (rs *ReplaySession) Close() {
	rs.mu.Lock()
	if rs.closed {
		rs.mu.Unlock()
		return
	}
	rs.closed = true
	transports, addresses := rs.transports, rs.addresses
	rs.transports, rs.addresses = nil, nil
	suppressed := rs.suppressed
	rs.mu.Unlock()

	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
	// Suppression must survive a canceled send until the proxy has read
	// everything already sent on its socket. ClientDisconnected removes it,
	// exactly as it does for ReplayWithPolicy.
	if suppressed || len(addresses) == 0 {
		return
	}
	rs.server.mu.Lock()
	for _, addr := range addresses {
		delete(rs.server.replayClients, addr)
	}
	rs.server.mu.Unlock()
}
