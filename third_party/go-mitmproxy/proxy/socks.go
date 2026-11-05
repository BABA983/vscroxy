package proxy

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/lqqyt2423/go-mitmproxy/internal/helper"
	log "github.com/sirupsen/logrus"
)

// ServeSOCKSConn serves one connection whose destination a SOCKS5 CONNECT
// agreed, before any HTTP byte arrived. The target's decryption policy is
// applied before any byte is read, as it is for HTTP CONNECT: a target this
// proxy does not decrypt is tunnelled byte for byte, so a server-first
// protocol sends its banner without waiting for a peek. A decrypted target is
// inspected: TLS goes through the ordinary attacker path, and plaintext is
// parsed by the HTTP front end with its origin-form requests pinned to the
// target. A decrypted target that sends neither closes after the peek timeout.
//
// The caller owns conn until this method returns. It replies to the SOCKS
// request before calling, because the client waits for that reply before it
// sends the first byte this method inspects.
func (proxy *Proxy) ServeSOCKSConn(conn net.Conn, target string, done <-chan struct{}) error {
	wc := newWrapClientConn(conn, proxy)
	connCtx := newConnContext(wc, proxy)
	wc.connCtx = connCtx
	connCtx.TransparentTarget = target
	for _, addon := range proxy.Addons {
		addon.ClientConnected(connCtx.ClientConn)
	}

	// A shutdown cancels the initial dial or TLS handshake, as the HTTP
	// front end's request context does for a CONNECT.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if done != nil {
		go func() {
			select {
			case <-done:
				cancel()
			case <-ctx.Done():
			}
		}()
	}

	req := socksConnectRequest(ctx, wc, target)
	connCtx.Intercept = proxy.shouldIntercept == nil || proxy.shouldIntercept(req)
	if !connCtx.Intercept {
		// A bypassed target is a raw tunnel, exactly like a CONNECT this
		// proxy does not decrypt. Do not peek: the target may speak first.
		proxy.entry.serveSOCKSTunnel(wc, req)
		return nil
	}

	// A decrypted target must start with TLS or an HTTP request. Bound the
	// wait so a server-first protocol cannot hold the connection forever.
	timeout := proxy.Opts.SOCKSPeekTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	_ = wc.SetReadDeadline(time.Now().Add(timeout))
	peek, err := wc.Peek(3)
	_ = wc.SetReadDeadline(time.Time{})
	if err != nil {
		_ = wc.Close()
		return err
	}
	if helper.IsTls(peek) {
		proxy.entry.serveSOCKSTunnel(wc, req)
		return nil
	}
	proxy.entry.serveSOCKSHTTP(wc)
	return nil
}

// socksConnectRequest is the CONNECT envelope a SOCKS destination stands in
// for. Addons see it before the tunnel is published or intercepted.
func socksConnectRequest(baseCtx context.Context, cconn *wrapClientConn, target string) *http.Request {
	req := &http.Request{
		Method:     http.MethodConnect,
		Host:       target,
		URL:        &url.URL{Scheme: "https", Host: target},
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		RemoteAddr: cconn.RemoteAddr().String(),
	}
	return req.WithContext(context.WithValue(context.WithValue(baseCtx, connContextKey, cconn.connCtx), http.LocalAddrContextKey, cconn.LocalAddr()))
}

// serveSOCKSTunnel applies the CONNECT policy to an already negotiated
// connection: it publishes the tunnel when it will not decrypt, and otherwise
// runs the same eager or lazy MITM the HTTP CONNECT path uses.
func (e *entry) serveSOCKSTunnel(cconn *wrapClientConn, req *http.Request) {
	proxy := e.proxy
	ctx := cconn.connCtx

	f := newFlow()
	f.Request = newRequest(req)
	f.ConnContext = ctx
	defer f.finish()

	// The tunnel has no HTTP request of its own to show; Requestheaders still
	// records its host and publishes the row of one this proxy will not read.
	for _, addon := range proxy.Addons {
		addon.Requestheaders(f)
	}

	if !ctx.Intercept {
		conn, err := proxy.getUpstreamConn(req.Context(), req)
		if err != nil {
			for _, addon := range proxy.Addons {
				addon.HTTPConnectError(f, err)
			}
			_ = cconn.Close()
			return
		}
		transfer(log.WithField("in", "Proxy.SOCKS.transfer"), conn, cconn)
		_ = conn.Close()
		_ = cconn.Close()
		return
	}

	if !ctx.ClientConn.UpstreamCert || proxy.lazyIntercept != nil && proxy.lazyIntercept(req) {
		ctx.RecordEvent("TLS", "Deferring upstream connection until the request", nil)
		proxy.attacker.httpsLazyAttack(req.Context(), cconn, req, f)
		return
	}
	conn, err := proxy.attacker.httpsDial(req.Context(), req)
	if err != nil && proxy.dialFallback != nil && proxy.dialFallback(req) {
		ctx.RecordEvent("TLS", "Could not reach "+req.Host+"; a rule may send its requests elsewhere, so using a generated certificate", err)
		proxy.attacker.httpsLazyAttack(req.Context(), cconn, req, f)
		return
	}
	if err != nil {
		for _, addon := range proxy.Addons {
			addon.HTTPConnectError(f, err)
		}
		_ = cconn.Close()
		return
	}
	proxy.attacker.httpsTlsDial(req.Context(), cconn, conn, f)
}

// serveSOCKSHTTP hands a plaintext connection to the entry server. Its
// TransparentTarget supplies the host its origin-form request line omits.
func (e *entry) serveSOCKSHTTP(c net.Conn) {
	proxy := e.proxy
	proxy.socksOnce.Do(func() {
		proxy.socksHTTP = newConnListener(c.LocalAddr())
		go func() {
			_ = e.server.Serve(&wrapListener{Listener: proxy.socksHTTP, proxy: proxy})
		}()
	})
	if !proxy.socksHTTP.push(c) {
		_ = c.Close()
	}
}

// connListener feeds accepted, already-wrapped connections to one http.Server.
// Close unblocks Accept so the server can shut down with the rest of the proxy.
type connListener struct {
	conns chan net.Conn
	done  chan struct{}
	addr  net.Addr
	once  sync.Once
}

func newConnListener(addr net.Addr) *connListener {
	return &connListener{conns: make(chan net.Conn), done: make(chan struct{}), addr: addr}
}

func (l *connListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *connListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *connListener) Addr() net.Addr { return l.addr }

func (l *connListener) push(c net.Conn) bool {
	select {
	case l.conns <- c:
		return true
	case <-l.done:
		return false
	}
}
