package proxy

import (
	"cmp"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	uuid "github.com/google/uuid"
)

// client connection
type ClientConn struct {
	Id                 uuid.UUID
	Conn               net.Conn
	Tls                bool
	NegotiatedProtocol string
	UpstreamCert       bool // Connect to upstream server to look up certificate details. Default: True
	clientHello        *tls.ClientHelloInfo
}

func newClientConn(c net.Conn) *ClientConn {
	return &ClientConn{
		Id:           uuid.New(),
		Conn:         c,
		Tls:          false,
		UpstreamCert: true,
	}
}

func (c *ClientConn) MarshalJSON() ([]byte, error) {
	m := make(map[string]interface{})
	m["id"] = c.Id
	m["tls"] = c.Tls
	m["address"] = c.Conn.RemoteAddr().String()
	return json.Marshal(m)
}

// server connection
type ServerConn struct {
	Id      uuid.UUID
	Address string
	Conn    net.Conn

	client   *http.Client
	tlsConn  *tls.Conn
	tlsState *tls.ConnectionState
	// retireOnce keeps the client's close and a target switch from closing
	// the same upstream socket twice when they meet a late dial.
	retireOnce sync.Once
}

func newServerConn() *ServerConn {
	return &ServerConn{
		Id: uuid.New(),
	}
}

func (c *ServerConn) MarshalJSON() ([]byte, error) {
	m := make(map[string]interface{})
	m["id"] = c.Id
	m["address"] = c.Address
	peername := ""
	if c.Conn != nil {
		peername = c.Conn.RemoteAddr().String()
	}
	m["peername"] = peername
	return json.Marshal(m)
}

func (c *ServerConn) TlsState() *tls.ConnectionState {
	return c.tlsState
}

// retire closes the upstream socket but leaves the client's connection open:
// a forward proxy connection can outlive the target it was dialed for, and its
// transport would otherwise hand this socket to the next target's request.
// A client closing while this connection is still being dialed retires it
// once it is published; a later retire is a no-op.
func (c *ServerConn) retire() {
	if c == nil {
		return
	}
	c.retireOnce.Do(func() {
		if c.Conn == nil {
			return
		}
		if w, ok := c.Conn.(*wrapServerConn); ok {
			w.retire()
			return
		}
		_ = c.Conn.Close()
	})
}

// connection context ctx key
var connContextKey = new(struct{})

// connection context
type ConnContext struct {
	ClientConn *ClientConn `json:"clientConn"`
	// ServerConn is published by setServerConn and read through
	// ServerConnection: a client can close while a dial is still in flight,
	// so Close must never race with the writer of this field.
	ServerConn *ServerConn   `json:"serverConn"`
	Intercept  bool          `json:"intercept"` // Indicates whether to parse HTTPS
	FlowCount  atomic.Uint32 `json:"-"`         // Number of HTTP requests made on the same connection

	serverMu     sync.Mutex
	serverClosed bool

	// VerifyUpstream makes an intercepted TLS handshake verify the server's
	// certificate, as a CONNECT's HeaderVerifyUpstream asks.
	VerifyUpstream bool `json:"-"`
	// TLSVersions and TLSCipherSuites narrow an intercepted upstream
	// handshake, as a CONNECT's own headers ask. Empty keeps the engine's
	// defaults.
	TLSVersions     []uint16 `json:"-"`
	TLSCipherSuites []uint16 `json:"-"`
	// upstreamTLSFailure is set when an intercepted upstream handshake fails
	// before the client handshake. The client handshake still completes with
	// our certificate so the request can be answered with the reason.
	upstreamTLSFailure error `json:"-"`
	// serverCertificateFailure keeps the reason a configured server
	// certificate could not be loaded, so the failed client handshake's
	// connection detail explains more than the TLS layer's generic error.
	serverCertificateFailure error `json:"-"`
	// TransparentTarget is the destination agreed outside HTTP, as in a
	// SOCKS5 CONNECT. An origin-form request on such a connection is sent to
	// this target; an absolute-form one is still pinned to it. Empty for the
	// HTTP proxy, whose absolute URL already names the destination.
	TransparentTarget string `json:"transparentTarget,omitempty"`

	proxy              *Proxy
	closeAfterResponse atomic.Bool                 // after http response, http server will close the connection
	dialFn             func(context.Context) error // when begin request, if there no ServerConn, use this func to dial
	eventsMu           sync.Mutex
	events             []ConnectionEvent
}

// ConnectionEvent records observed connection stages, not inferred HTTP status.
// Keep a bounded history on the connection; published flows take a snapshot.
type ConnectionEvent struct {
	At      time.Time
	Stage   string
	Message string
	Error   string
}

func (c *ConnContext) RecordEvent(stage, message string, err error) {
	e := ConnectionEvent{At: time.Now(), Stage: stage, Message: message}
	if err != nil {
		e.Error = err.Error()
	}
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	if len(c.events) == 64 {
		// Retain the initial connection event and the most recent stages.
		c.events = append(c.events[:1], c.events[2:]...)
	}
	c.events = append(c.events, e)
}

func (c *ConnContext) ConnectionEvents() []ConnectionEvent {
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	return slices.Clone(c.events)
}

// parseUint16List reads a comma-separated list of decimal wire values, as a
// CONNECT's TLS headers carry them.
func parseUint16List(value string) []uint16 {
	var out []uint16
	for part := range strings.SplitSeq(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.ParseUint(part, 10, 16)
		if err != nil {
			continue
		}
		out = append(out, uint16(n))
	}
	return out
}

// Used by the normal dial and by a separate transport after a URL rewrite.
func (c *ConnContext) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(info httptrace.DNSStartInfo) { c.RecordEvent("DNS", "Resolving "+info.Host, nil) },
		DNSDone: func(info httptrace.DNSDoneInfo) {
			ips := make([]string, len(info.Addrs))
			for i, addr := range info.Addrs {
				ips[i] = addr.String()
			}
			c.RecordEvent("DNS", strings.Join(ips, ", "), info.Err)
		},
		ConnectStart: func(network, addr string) { c.RecordEvent("TCP", "Dial "+network+" "+addr, nil) },
		ConnectDone: func(network, addr string, err error) {
			c.RecordEvent("TCP", "Dial finished "+addr, err)
		},
		GotConn: func(info httptrace.GotConnInfo) {
			c.RecordEvent("TCP", fmt.Sprintf("Using %s · reused: %t", info.Conn.RemoteAddr(), info.Reused), nil)
		},
		TLSHandshakeStart: func() { c.RecordEvent("TLS upstream", "Starting separate transport handshake", nil) },
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			if err != nil {
				c.RecordEvent("TLS upstream", "Handshake failed", err)
				return
			}
			c.RecordEvent("TLS upstream", fmt.Sprintf("%s · %s · ALPN %s · certificate verified: %t", tls.VersionName(state.Version), tls.CipherSuiteName(state.CipherSuite), cmp.Or(state.NegotiatedProtocol, "none"), len(state.VerifiedChains) > 0), nil)
			if len(state.PeerCertificates) > 0 {
				leaf := state.PeerCertificates[0]
				c.RecordEvent("TLS upstream", fmt.Sprintf("Certificate %s · issuer %s · expires %s · SAN %v %v", leaf.Subject.CommonName, leaf.Issuer.CommonName, leaf.NotAfter.Format("2006-01-02"), leaf.DNSNames, leaf.IPAddresses), nil)
			}
		},
	}
}

func newConnContext(c net.Conn, proxy *Proxy) *ConnContext {
	clientConn := newClientConn(c)
	cc := &ConnContext{
		ClientConn: clientConn,
		proxy:      proxy,
	}
	cc.RecordEvent("Client", "Accepted "+c.RemoteAddr().String(), nil)
	return cc
}

func (connCtx *ConnContext) Id() uuid.UUID {
	return connCtx.ClientConn.Id
}

// ServerConnection returns the upstream bound to this client connection, if
// any. A dial publishes its ServerConn before addons see it, so callers can
// use the returned pointer without holding serverMu.
func (connCtx *ConnContext) ServerConnection() *ServerConn {
	connCtx.serverMu.Lock()
	defer connCtx.serverMu.Unlock()
	return connCtx.ServerConn
}

// setServerConn binds a dialled upstream to the client connection. If the
// client closed while the dial was in flight, the new upstream is retired
// here instead of being left behind with nobody to close it. Publishing it
// first keeps the addon and diagnostics view of the connection unchanged.
func (connCtx *ConnContext) setServerConn(serverConn *ServerConn) {
	connCtx.serverMu.Lock()
	connCtx.ServerConn = serverConn
	closed := connCtx.serverClosed
	connCtx.serverMu.Unlock()
	if closed {
		serverConn.retire()
	}
}

// closeServerConn marks the connection closed before closing the upstream it
// knows about. A dial that lands after this point retires its own upstream in
// setServerConn, so the socket is closed exactly once either way.
func (connCtx *ConnContext) closeServerConn() {
	connCtx.serverMu.Lock()
	connCtx.serverClosed = true
	serverConn := connCtx.ServerConn
	connCtx.serverMu.Unlock()
	serverConn.retire()
}
