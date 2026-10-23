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

// connection context ctx key
var connContextKey = new(struct{})

// connection context
type ConnContext struct {
	ClientConn *ClientConn   `json:"clientConn"`
	ServerConn *ServerConn   `json:"serverConn"`
	Intercept  bool          `json:"intercept"` // Indicates whether to parse HTTPS
	FlowCount  atomic.Uint32 `json:"-"`         // Number of HTTP requests made on the same connection

	proxy              *Proxy
	closeAfterResponse bool                        // after http response, http server will close the connection
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
