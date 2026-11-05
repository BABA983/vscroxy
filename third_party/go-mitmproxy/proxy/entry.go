package proxy

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/lqqyt2423/go-mitmproxy/internal/helper"
	log "github.com/sirupsen/logrus"
)

// wrap tcpListener for remote client
type wrapListener struct {
	net.Listener
	proxy *Proxy
}

func (l *wrapListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	// A connection a protocol other than HTTP negotiated (SOCKS5) was wrapped
	// and registered by ServeSOCKSConn already; wrapping it twice would make
	// two client identities for one socket.
	if wc, ok := c.(*wrapClientConn); ok {
		return wc, nil
	}

	proxy := l.proxy
	wc := newWrapClientConn(c, proxy)
	connCtx := newConnContext(wc, proxy)
	wc.connCtx = connCtx

	for _, addon := range proxy.Addons {
		addon.ClientConnected(connCtx.ClientConn)
	}

	return wc, nil
}

// wrap tcpConn for remote client
type wrapClientConn struct {
	net.Conn
	r       *bufio.Reader
	proxy   *Proxy
	connCtx *ConnContext

	closeMu   sync.Mutex
	closed    bool
	closeErr  error
	closeChan chan struct{}
}

func newWrapClientConn(c net.Conn, proxy *Proxy) *wrapClientConn {
	return &wrapClientConn{
		Conn:      c,
		r:         bufio.NewReader(c),
		proxy:     proxy,
		closeChan: make(chan struct{}),
	}
}

func (c *wrapClientConn) Peek(n int) ([]byte, error) {
	return c.r.Peek(n)
}

func (c *wrapClientConn) PeekBuffered() ([]byte, error) {
	return c.r.Peek(c.r.Buffered())
}

func (c *wrapClientConn) Read(data []byte) (int, error) {
	return c.r.Read(data)
}

func (c *wrapClientConn) Close() error {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return c.closeErr
	}
	log.Debugln("in wrapClientConn close", c.connCtx.ClientConn.Conn.RemoteAddr())

	c.closed = true
	c.closeErr = c.Conn.Close()
	c.closeMu.Unlock()
	close(c.closeChan)

	for _, addon := range c.proxy.Addons {
		addon.ClientDisconnected(c.connCtx.ClientConn)
	}

	if c.connCtx.ServerConn != nil && c.connCtx.ServerConn.Conn != nil {
		c.connCtx.ServerConn.Conn.Close()
	}

	return c.closeErr
}

// wrap tcpConn for remote server
type wrapServerConn struct {
	net.Conn
	proxy   *Proxy
	connCtx *ConnContext
	// keepClient keeps the client's connection open when this upstream ends.
	// A forward proxy connection can outlive the target it was dialed for, so
	// retiring that target must not end the client still waiting for an answer.
	keepClient bool

	closeMu  sync.Mutex
	closed   bool
	closeErr error
}

// retire closes this upstream but keeps the client's connection open: a
// forward proxy connection can outlive the target it was dialed for.
func (c *wrapServerConn) retire() error {
	c.closeMu.Lock()
	c.keepClient = true
	c.closeMu.Unlock()
	return c.Close()
}

func (c *wrapServerConn) Close() error {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return c.closeErr
	}
	log.Debugln("in wrapServerConn close", c.connCtx.ClientConn.Conn.RemoteAddr())

	c.closed = true
	c.closeErr = c.Conn.Close()
	keepClient := c.keepClient
	c.closeMu.Unlock()

	for _, addon := range c.proxy.Addons {
		addon.ServerDisconnected(c.connCtx)
	}

	// A Connection: close response is still being delivered downstream.
	// Closing even the client's read half cancels its HTTP request context
	// and aborts a paced response body. net/http closes after delivery.
	if keepClient || c.connCtx.closeAfterResponse.Load() {
		return c.closeErr
	}

	if !c.connCtx.ClientConn.Tls {
		c.connCtx.ClientConn.Conn.(*wrapClientConn).Conn.(*net.TCPConn).CloseRead()
	} else {
		c.connCtx.ClientConn.Conn.Close()
	}

	return c.closeErr
}

type entry struct {
	proxy  *Proxy
	server *http.Server
}

func newEntry(proxy *Proxy) *entry {
	e := &entry{proxy: proxy}
	e.server = &http.Server{
		Addr:    proxy.Opts.Addr,
		Handler: e,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connContextKey, c.(*wrapClientConn).connCtx)
		},
	}
	return e
}

func (e *entry) serve(ln net.Listener) error {
	log.Infof("Proxy start listen at %v\n", ln.Addr())
	pln := &wrapListener{
		Listener: ln,
		proxy:    e.proxy,
	}
	return e.server.Serve(pln)
}

func (e *entry) close() error {
	return e.server.Close()
}

func (e *entry) shutdown(ctx context.Context) error {
	return e.server.Shutdown(ctx)
}

func (e *entry) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	proxy := e.proxy

	log := log.WithFields(log.Fields{
		"in":   "Proxy.entry.ServeHTTP",
		"host": req.Host,
	})
	// Add entry proxy authentication
	if e.proxy.authProxy != nil {
		b, err := e.proxy.authProxy(res, req)
		if !b {
			log.Errorf("Proxy authentication failed: %s", err.Error())
			httpError(res, "", http.StatusProxyAuthRequired)
			return
		}
	}
	ctx, _ := req.Context().Value(connContextKey).(*ConnContext)
	if ctx != nil && ctx.TransparentTarget != "" {
		// The destination was agreed outside HTTP (SOCKS5). A nested CONNECT
		// would open a second target on the same request, so it is refused
		// before the HTTP CONNECT path can hijack the connection. An
		// origin-form request gets the missing host; an absolute-form one is
		// still pinned to the same destination.
		if req.Method == "CONNECT" {
			res.Header().Set("Content-Type", "text/plain; charset=utf-8")
			res.WriteHeader(http.StatusBadRequest)
			io.WriteString(res, "vscroxy: a SOCKS connection is pinned to its target\n")
			return
		}
		req.URL.Scheme = "http"
		req.URL.Host = ctx.TransparentTarget
	} else if req.Method == "CONNECT" {
		// proxy via connect tunnel
		e.handleConnect(res, req)
		return
	} else if !req.URL.IsAbs() || req.URL.Host == "" {
		res = helper.NewResponseCheck(res)
		for _, addon := range proxy.Addons {
			addon.AccessProxyServer(req, res)
		}
		if res, ok := res.(*helper.ResponseCheck); ok {
			if !res.Wrote {
				res.WriteHeader(400)
				io.WriteString(res, "此为代理服务器，不能直接发起请求")
			}
		}
		return
	}

	// http proxy
	proxy.attacker.initHttpDialFn(req)
	proxy.attacker.attack(res, req)
}

func (e *entry) handleConnect(res http.ResponseWriter, req *http.Request) {
	proxy := e.proxy

	log := log.WithFields(log.Fields{
		"in":   "Proxy.entry.handleConnect",
		"host": req.Host,
	})

	shouldIntercept := proxy.shouldIntercept == nil || proxy.shouldIntercept(req)
	connCtx := req.Context().Value(connContextKey).(*ConnContext)
	// A replay that wants the upstream certificate verified asks on its
	// CONNECT. The header is the proxy's own signal, not part of the exchange.
	if req.Header.Get(HeaderVerifyUpstream) == "1" {
		connCtx.VerifyUpstream = true
	}
	req.Header.Del(HeaderVerifyUpstream)
	f := newFlow()
	f.Request = newRequest(req)
	f.ConnContext = connCtx
	f.ConnContext.Intercept = shouldIntercept
	defer f.finish()

	// trigger addon event Requestheaders
	for _, addon := range proxy.Addons {
		addon.Requestheaders(f)
	}

	if !shouldIntercept {
		log.Debugf("begin transpond %v", req.Host)
		e.directTransfer(res, req, f)
		return
	}

	if f.ConnContext.ClientConn.UpstreamCert {
		e.httpsDialFirstAttack(res, req, f)
		return
	}

	log.Debugf("begin intercept %v", req.Host)
	e.httpsDialLazyAttack(res, req, f)
}

func (e *entry) establishConnection(res http.ResponseWriter, f *Flow) (net.Conn, error) {
	cconn, _, err := res.(http.Hijacker).Hijack()
	if err != nil {
		for _, addon := range e.proxy.Addons {
			addon.HTTPConnectError(f, err)
		}
		res.WriteHeader(502)
		return nil, err
	}
	_, err = io.WriteString(cconn, "HTTP/1.1 200 Connection Established\r\n\r\n")
	if err != nil {
		cconn.Close()
		for _, addon := range e.proxy.Addons {
			addon.HTTPConnectError(f, err)
		}
		return nil, err
	}

	f.Response = &Response{
		StatusCode: 200,
		Header:     make(http.Header),
	}

	// trigger addon event Responseheaders
	for _, addon := range e.proxy.Addons {
		addon.Responseheaders(f)
	}

	return cconn, nil
}

func (e *entry) directTransfer(res http.ResponseWriter, req *http.Request, f *Flow) {
	proxy := e.proxy
	log := log.WithFields(log.Fields{
		"in":   "Proxy.entry.directTransfer",
		"host": req.Host,
	})

	conn, err := proxy.getUpstreamConn(req.Context(), req)
	if err != nil {
		for _, addon := range proxy.Addons {
			addon.HTTPConnectError(f, err)
		}
		res.WriteHeader(502)
		return
	}
	defer conn.Close()

	cconn, err := e.establishConnection(res, f)
	if err != nil {
		return
	}
	defer cconn.Close()

	transfer(log, conn, cconn)
}

func (e *entry) httpsDialFirstAttack(res http.ResponseWriter, req *http.Request, f *Flow) {
	proxy := e.proxy
	log := log.WithFields(log.Fields{
		"in":   "Proxy.entry.httpsDialFirstAttack",
		"host": req.Host,
	})

	if proxy.lazyIntercept != nil && proxy.lazyIntercept(req) {
		f.ConnContext.RecordEvent("TLS", "Deferring upstream connection for a local response rule", nil)
		e.httpsDialLazyAttack(res, req, f)
		return
	}
	conn, err := proxy.attacker.httpsDial(req.Context(), req)
	if err != nil && proxy.dialFallback != nil && proxy.dialFallback(req) {
		f.ConnContext.RecordEvent("TLS", "Could not reach "+req.Host+"; a rule may send its requests elsewhere, so using a generated certificate", err)
		e.httpsDialLazyAttack(res, req, f)
		return
	}
	if err != nil {
		for _, addon := range proxy.Addons {
			addon.HTTPConnectError(f, err)
		}
		res.WriteHeader(502)
		return
	}

	cconn, err := e.establishConnection(res, f)
	if err != nil {
		conn.Close()
		return
	}

	peek, err := cconn.(*wrapClientConn).Peek(3)
	if err != nil {
		f.ConnContext.RecordEvent("Client", "Could not read handshake after CONNECT", err)
		for _, addon := range proxy.Addons {
			addon.HTTPConnectError(f, err)
		}
		cconn.Close()
		conn.Close()
		log.Error(err)
		return
	}

	if helper.IsTls(peek) {
		f.ConnContext.ClientConn.Tls = true
		proxy.attacker.httpsTlsDial(req.Context(), cconn, conn, f)
		return
	}

	wsPeek, err := cconn.(*wrapClientConn).PeekBuffered()
	if err == io.EOF {
		err = nil
	}
	if err != nil {
		cconn.Close()
		conn.Close()
		log.Error(err)
		return
	}

	if helper.IsWebSocket(wsPeek) {
		err = proxy.webSocketHandler.handle(conn, cconn, f)
		if err != nil {
			log.Errorf("WebSocket handle error: %v", err)
			cconn.Close()
			conn.Close()
		}
		return
	}

	transfer(log, conn, cconn)
	cconn.Close()
	conn.Close()
}

func (e *entry) httpsDialLazyAttack(res http.ResponseWriter, req *http.Request, f *Flow) {
	proxy := e.proxy
	log := log.WithFields(log.Fields{
		"in":   "Proxy.entry.httpsDialLazyAttack",
		"host": req.Host,
	})

	cconn, err := e.establishConnection(res, f)
	if err != nil {
		log.Error(err)
		return
	}

	peek, err := cconn.(*wrapClientConn).Peek(3)
	if err != nil {
		f.ConnContext.RecordEvent("Client", "Could not read handshake after CONNECT", err)
		for _, addon := range proxy.Addons {
			addon.HTTPConnectError(f, err)
		}
		cconn.Close()
		log.Error(err)
		return
	}

	if helper.IsTls(peek) {
		f.ConnContext.ClientConn.Tls = true
		proxy.attacker.httpsLazyAttack(req.Context(), cconn, req, f)
		return
	}

	wsPeek, err := cconn.(*wrapClientConn).PeekBuffered()
	if err == io.EOF {
		err = nil
	}
	if err != nil {
		cconn.Close()
		log.Error(err)
		return
	}

	if helper.IsWebSocket(wsPeek) {
		// handle dials the server once addons have seen the handshake,
		// which may point it elsewhere.
		err = proxy.webSocketHandler.handle(nil, cconn, f)
		if err != nil {
			log.Errorf("WebSocket handle error: %v", err)
			cconn.Close()
		}
		return
	}

	conn, err := proxy.attacker.httpsDial(req.Context(), req)
	if err != nil {
		cconn.Close()
		log.Error(err)
		return
	}
	transfer(log, conn, cconn)
	conn.Close()
	cconn.Close()
}
