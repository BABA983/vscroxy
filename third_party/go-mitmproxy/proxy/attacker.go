package proxy

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/lqqyt2423/go-mitmproxy/cert"
	"github.com/lqqyt2423/go-mitmproxy/internal/helper"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
)

type attackerListener struct {
	connChan chan net.Conn
}

func (l *attackerListener) accept(conn net.Conn) {
	l.connChan <- conn
}

func (l *attackerListener) Accept() (net.Conn, error) {
	c := <-l.connChan
	return c, nil
}
func (l *attackerListener) Close() error   { return nil }
func (l *attackerListener) Addr() net.Addr { return nil }

type attackerConn struct {
	net.Conn
	connCtx *ConnContext
}

type attacker struct {
	proxy    *Proxy
	ca       cert.CA
	server   *http.Server
	h2Server *http2.Server
	// h2Base holds the settings the HTTP/2 server takes from an http.Server.
	// It is never served itself: ServeConn copies it once per connection and
	// net/http fills such a copy in as it starts, so serving this one too
	// would leave two goroutines reading and writing the same fields.
	h2Base   *http.Server
	listener *attackerListener
}

func newAttacker(proxy *Proxy) (*attacker, error) {
	ca, err := newCa(proxy.Opts)
	if err != nil {
		return nil, err
	}

	a := &attacker{
		proxy: proxy,
		ca:    ca,
		listener: &attackerListener{
			connChan: make(chan net.Conn),
		},
	}

	a.server = &http.Server{
		Handler: a,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connContextKey, c.(*attackerConn).connCtx)
		},
	}

	a.h2Server = &http2.Server{
		MaxConcurrentStreams: 100, // todo: wait for remote server setting
	}
	a.h2Base = &http.Server{
		Handler: a,
	}

	return a, nil
}

// hostOnly is the host part of a CONNECT or dial address, without its port.
func hostOnly(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return strings.Trim(addr, "[]")
}

func newCa(opts *Options) (cert.CA, error) {
	newCaFunc := opts.NewCaFunc
	if newCaFunc != nil {
		return newCaFunc()
	}
	return cert.NewSelfSignCA(opts.CaRootPath)
}

func (a *attacker) start() error {
	return a.server.Serve(a.listener)
}

func (a *attacker) serveConn(clientTlsConn *tls.Conn, connCtx *ConnContext) {
	connCtx.ClientConn.NegotiatedProtocol = clientTlsConn.ConnectionState().NegotiatedProtocol

	if connCtx.ClientConn.NegotiatedProtocol == "h2" && connCtx.ServerConn != nil {
		connCtx.ServerConn.client = &http.Client{
			Transport: &http2.Transport{
				DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
					return connCtx.ServerConn.tlsConn, nil
				},
				DisableCompression: true,
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// 禁止自动重定向
				return http.ErrUseLastResponse
			},
		}

		ctx := context.WithValue(context.Background(), connContextKey, connCtx)
		ctx, cancel := context.WithCancel(ctx)
		go func() {
			<-connCtx.ClientConn.Conn.(*wrapClientConn).closeChan
			cancel()
		}()
		go func() {
			a.h2Server.ServeConn(clientTlsConn, &http2.ServeConnOpts{
				Context:    ctx,
				Handler:    a,
				BaseConfig: a.h2Base,
			})
		}()
		return
	}

	a.listener.accept(&attackerConn{
		Conn:    clientTlsConn,
		connCtx: connCtx,
	})
}

func (a *attacker) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	if req.URL.Scheme == "" {
		req.URL.Scheme = "https"
	}
	if req.URL.Host == "" {
		req.URL.Host = req.Host
	}

	if strings.EqualFold(req.Header.Get("Connection"), "Upgrade") && strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		f := newFlow()
		f.Request = newRequest(req)
		// Addons see the WebSocket's own URL, and may point it elsewhere.
		wsURL := *req.URL
		wsURL.Scheme = map[string]string{"http": "ws"}[req.URL.Scheme]
		if wsURL.Scheme == "" {
			wsURL.Scheme = "wss"
		}
		f.Request.URL = &wsURL
		f.ConnContext = req.Context().Value(connContextKey).(*ConnContext)
		f.ConnContext.FlowCount.Add(1)

		for _, addon := range a.proxy.Addons {
			addon.Requestheaders(f)
			if f.Response != nil {
				if f.Response.Header != nil {
					for key, vals := range f.Response.Header {
						for _, v := range vals {
							res.Header().Add(key, v)
						}
					}
				}
				res.WriteHeader(f.Response.StatusCode)
				if len(f.Response.Body) > 0 {
					_, _ = res.Write(f.Response.Body)
				}
				f.finish()
				return
			}
		}
		if err := a.proxy.webSocketHandler.handleWSS(res, req, f); err != nil {
			log.Errorf("handleWSS error: %v", err)
		}
		return
	}

	a.attack(res, req)
}

func (a *attacker) initHttpDialFn(req *http.Request) {
	connCtx := req.Context().Value(connContextKey).(*ConnContext)
	connCtx.dialFn = func(ctx context.Context) error {
		addr := helper.CanonicalAddr(req.URL)
		c, err := a.proxy.getUpstreamConn(ctx, req)
		if err != nil {
			return err
		}
		proxy := a.proxy
		cw := &wrapServerConn{
			Conn:    c,
			proxy:   proxy,
			connCtx: connCtx,
		}

		serverConn := newServerConn()
		serverConn.Conn = cw
		serverConn.Address = addr
		serverConn.client = &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return cw, nil
				},
				ForceAttemptHTTP2:  false, // disable http2
				DisableCompression: true,  // To get the original response from the server, set Transport.DisableCompression to true.
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// 禁止自动重定向
				return http.ErrUseLastResponse
			},
		}

		connCtx.ServerConn = serverConn
		for _, addon := range proxy.Addons {
			addon.ServerConnected(connCtx)
		}

		return nil
	}
}

// upstreamClient dials a rewritten target itself, so its TLS configuration
// can name that target and present its client certificate. Its dialer uses the
// same ordered upstream candidates as the main path, so a rewritten request
// gets the same host rules, PAC fallback and credentials. It keeps no idle
// connections: it is built per request and must not leave sockets behind.
func (a *attacker) upstreamClient(req *http.Request, address string, verify bool) (*http.Client, error) {
	tlsConfig, err := a.proxy.upstreamTLSConfig(address, verify)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		ForceAttemptHTTP2:  true,
		DisableCompression: true,
		DisableKeepAlives:  true,
		TLSClientConfig:    tlsConfig,
	}
	// A plain HTTP target through an HTTP(S) upstream keeps net/http's
	// absolute-URI forwarding, as it did before ordered candidates. CONNECT
	// to port 80 is not universally allowed, and Map Remote must not regress.
	candidates, err := a.proxy.getUpstreamProxies(req, address)
	if err != nil {
		return nil, err
	}
	if req.URL != nil && req.URL.Scheme == "http" {
		if t, ok, err := a.httpUpstreamTransport(req, address, candidates); err != nil {
			return nil, err
		} else if ok {
			return &http.Client{
				Transport: t,
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}, nil
		}
	}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return a.proxy.dialUpstream(ctx, req, addr)
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

// httpUpstreamTransport picks the first candidate that accepts a TCP
// connection for a plain HTTP target. An HTTP(S) candidate is used with
// net/http's absolute-URI forwarding and the connection already dialled; a
// DIRECT candidate is used with the target connection already dialled. SOCKS5
// candidates need the regular tunnel path, so they leave the choice to
// dialUpstream. TCP failures before a request is written are safe to retry.
func (a *attacker) httpUpstreamTransport(req *http.Request, address string, candidates []UpstreamProxy) (*http.Transport, bool, error) {
	if len(candidates) == 0 {
		return nil, false, nil
	}
	cc, _ := req.Context().Value(connContextKey).(*ConnContext)
	for _, candidate := range candidates {
		if cc != nil && candidate.Reason != "" {
			cc.RecordEvent("Upstream", candidate.Reason, nil)
		}
		if candidate.URL == nil {
			conn, err := (&net.Dialer{Timeout: upstreamDialTimeout}).DialContext(req.Context(), "tcp", address)
			if err != nil {
				if cc != nil {
					cc.RecordEvent("TCP", "Could not connect to "+address, err)
				}
				continue
			}
			return httpTransportWithConn(nil, conn, address), true, nil
		}
		if candidate.URL.Scheme != "http" && candidate.URL.Scheme != "https" {
			return nil, false, nil // SOCKS5: dialUpstream handles the whole list.
		}
		var proxyTLS *tls.Config
		if candidate.URL.Scheme == "https" {
			var err error
			proxyTLS, err = a.proxy.proxyTLSConfig()
			if err != nil {
				return nil, false, err
			}
		}
		ctx, cancel := context.WithTimeout(req.Context(), upstreamDialTimeout)
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", candidate.URL.Host)
		cancel()
		if err != nil {
			if cc != nil {
				cc.RecordEvent("TCP", "Could not connect to "+candidate.URL.Host, err)
			}
			continue
		}
		t := httpTransportWithConn(candidate.URL, conn, candidate.URL.Host)
		if candidate.URL.Scheme == "https" {
			t.TLSClientConfig = proxyTLS
		}
		return t, true, nil
	}
	return nil, false, nil
}

// httpTransportWithConn returns a one-request transport that uses conn for the
// first dial. Subsequent dials, if the transport retries, are ordinary. The
// dial timeout is enough here: a slow response header is valid for long polls
// and slow APIs, so no response-header deadline is imposed.
func httpTransportWithConn(proxyURL *url.URL, conn net.Conn, address string) *http.Transport {
	var once sync.Once
	t := &http.Transport{
		ForceAttemptHTTP2:  true,
		DisableCompression: true,
		DisableKeepAlives:  true,
		Proxy:              http.ProxyURL(proxyURL),
	}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != address {
			return (&net.Dialer{Timeout: upstreamDialTimeout}).DialContext(ctx, network, addr)
		}
		var out net.Conn
		once.Do(func() { out = conn })
		if out != nil {
			return out, nil
		}
		return (&net.Dialer{Timeout: upstreamDialTimeout}).DialContext(ctx, network, addr)
	}
	return t
}

// writeGatewayError answers a failed upstream exchange with the reason in the
// body and records the same error on the connection, so Send, Runner and the
// inspector do not show an empty 502.
func writeGatewayError(res http.ResponseWriter, f *Flow, err error) {
	writeGatewayStatus(res, f, http.StatusBadGateway, err)
}

func writeGatewayStatus(res http.ResponseWriter, f *Flow, status int, err error) {
	if f != nil {
		f.Error = err
		if f.ConnContext != nil {
			f.ConnContext.RecordEvent("HTTP", "Upstream request failed", err)
		}
	}
	res.Header().Set("Content-Type", "text/plain; charset=utf-8")
	res.WriteHeader(status)
	fmt.Fprintf(res, "vscroxy: %v\n", err)
}

// send clientHello to server, server handshake
func (a *attacker) serverTlsHandshake(ctx context.Context, connCtx *ConnContext) error {
	proxy := a.proxy
	clientHello := connCtx.ClientConn.clientHello
	serverConn := connCtx.ServerConn

	// The capture engine accepts any certificate so it can inspect self-signed
	// servers. A replay may ask for verification on its CONNECT instead.
	verify := !proxy.Opts.SslInsecure || connCtx.VerifyUpstream
	// A client connecting to an IP sends no SNI. When verifying, use the
	// CONNECT host, which carries the IP the certificate's IP SAN must match;
	// inspection traffic keeps its previous empty ServerName behavior.
	serverName := clientHello.ServerName
	if serverName == "" && verify {
		serverName = hostOnly(serverConn.Address)
	}
	serverTlsConfig, err := proxy.upstreamTLSConfig(serverConn.Address, verify)
	if err != nil {
		connCtx.RecordEvent("TLS upstream", "Upstream TLS configuration failed", err)
		return err
	}
	serverTlsConfig.ServerName = serverName
	serverTlsConfig.NextProtos = clientHello.SupportedProtos
	// CurvePreferences:   clientHello.SupportedCurves, // todo: 如果打开会出错
	serverTlsConfig.CipherSuites = clientHello.CipherSuites
	if len(clientHello.SupportedVersions) > 0 {
		minVersion, maxVersion := uint16(0xffff), uint16(0)
		for _, v := range clientHello.SupportedVersions {
			if v&0x0F0F == 0x0A0A && v&0xff == v>>8 { // skip GREASE
				continue
			}
			if v < minVersion {
				minVersion = v
			}
			if v > maxVersion {
				maxVersion = v
			}
		}
		if minVersion != 0xffff {
			serverTlsConfig.MinVersion = minVersion
			serverTlsConfig.MaxVersion = maxVersion
		}
	}
	serverTlsConn := tls.Client(serverConn.Conn, serverTlsConfig)
	serverConn.tlsConn = serverTlsConn
	connCtx.RecordEvent("TLS upstream", "Starting handshake with "+serverConn.Address, nil)
	if err := serverTlsConn.HandshakeContext(ctx); err != nil {
		connCtx.RecordEvent("TLS upstream", "Upstream handshake failed", err)
		return err
	}
	serverTlsState := serverTlsConn.ConnectionState()
	serverConn.tlsState = &serverTlsState
	verification := "certificate verified"
	if !verify {
		verification = "certificate verification skipped by proxy"
	}
	connCtx.RecordEvent("TLS upstream", tls.VersionName(serverTlsState.Version)+" · ALPN "+cmp.Or(serverTlsState.NegotiatedProtocol, "none")+" · "+verification, nil)
	for _, addon := range proxy.Addons {
		addon.TlsEstablishedServer(connCtx)
	}

	serverConn.client = &http.Client{
		Transport: &http.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return serverTlsConn, nil
			},
			ForceAttemptHTTP2:  true,
			DisableCompression: true, // To get the original response from the server, set Transport.DisableCompression to true.
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// 禁止自动重定向
			return http.ErrUseLastResponse
		},
	}

	return nil
}

func (a *attacker) initHttpsDialFn(req *http.Request) {
	connCtx := req.Context().Value(connContextKey).(*ConnContext)

	connCtx.dialFn = func(ctx context.Context) error {
		_, err := a.httpsDial(ctx, req)
		if err != nil {
			return err
		}
		if err := a.serverTlsHandshake(ctx, connCtx); err != nil {
			return err
		}
		return nil
	}
}

func (a *attacker) httpsDial(ctx context.Context, req *http.Request) (net.Conn, error) {
	proxy := a.proxy
	connCtx := req.Context().Value(connContextKey).(*ConnContext)

	plainConn, err := proxy.getUpstreamConn(ctx, req)
	if err != nil {
		return nil, err
	}

	serverConn := newServerConn()
	address := req.Host
	if req.URL != nil && req.URL.Host != "" {
		address = helper.CanonicalAddr(req.URL)
	}
	serverConn.Address = address
	serverConn.Conn = &wrapServerConn{
		Conn:    plainConn,
		proxy:   proxy,
		connCtx: connCtx,
	}
	connCtx.ServerConn = serverConn
	for _, addon := range connCtx.proxy.Addons {
		addon.ServerConnected(connCtx)
	}

	return serverConn.Conn, nil
}

func (a *attacker) httpsTlsDial(ctx context.Context, cconn net.Conn, conn net.Conn, f *Flow) {
	connCtx := cconn.(*wrapClientConn).connCtx
	log := log.WithFields(log.Fields{
		"in":   "Proxy.attacker.httpsTlsDial",
		"host": connCtx.ClientConn.Conn.RemoteAddr().String(),
	})

	var clientHello *tls.ClientHelloInfo
	clientHelloChan := make(chan *tls.ClientHelloInfo)
	serverTlsStateChan := make(chan *tls.ConnectionState)
	errChan1 := make(chan error, 1)
	errChan2 := make(chan error, 1)
	clientHandshakeDoneChan := make(chan struct{})
	connCtx.RecordEvent("TLS client", "Starting intercepted client handshake", nil)

	clientTlsConn := tls.Server(cconn, &tls.Config{
		SessionTicketsDisabled: true, // 设置此值为 true ，确保每次都会调用下面的 GetConfigForClient 方法
		GetConfigForClient: func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
			clientHelloChan <- chi
			nextProtos := make([]string, 0)

			// wait server handshake finish
			select {
			case err := <-errChan2:
				return nil, err
			case serverTlsState := <-serverTlsStateChan:
				if serverTlsState.NegotiatedProtocol != "" {
					nextProtos = append([]string{serverTlsState.NegotiatedProtocol}, nextProtos...)
				}
			}

			name := chi.ServerName
			if name == "" {
				// IP clients omit SNI; certify the original CONNECT target.
				name, _, _ = net.SplitHostPort(connCtx.ServerConn.Address)
			}
			c, err := a.ca.GetCert(name)
			if err != nil {
				return nil, err
			}
			return &tls.Config{
				SessionTicketsDisabled: true,
				Certificates:           []tls.Certificate{*c},
				NextProtos:             nextProtos,
			}, nil

		},
	})
	go func() {
		if err := clientTlsConn.HandshakeContext(ctx); err != nil {
			errChan1 <- err
			return
		}
		close(clientHandshakeDoneChan)
	}()

	// get clientHello from client
	select {
	case err := <-errChan1:
		a.clientTLSError(f, err)
		cconn.Close()
		conn.Close()
		log.Error(err)
		return
	case clientHello = <-clientHelloChan:
	}
	connCtx.ClientConn.clientHello = clientHello

	if err := a.serverTlsHandshake(ctx, connCtx); err != nil {
		for _, addon := range a.proxy.Addons {
			addon.HTTPConnectError(f, err)
		}
		cconn.Close()
		conn.Close()
		errChan2 <- err
		log.Error(err)
		return
	}
	serverTlsStateChan <- connCtx.ServerConn.tlsState

	// wait client handshake finish
	select {
	case err := <-errChan1:
		a.clientTLSError(f, err)
		cconn.Close()
		conn.Close()
		log.Error(err)
		return
	case <-clientHandshakeDoneChan:
	}
	connCtx.RecordEvent("TLS client", "Client handshake completed", nil)

	// will go to attacker.ServeHTTP
	a.serveConn(clientTlsConn, connCtx)
}

func (a *attacker) httpsLazyAttack(ctx context.Context, cconn net.Conn, req *http.Request, f *Flow) {
	connCtx := cconn.(*wrapClientConn).connCtx
	log := log.WithFields(log.Fields{
		"in":   "Proxy.attacker.httpsLazyAttack",
		"host": connCtx.ClientConn.Conn.RemoteAddr().String(),
	})

	clientTlsConn := tls.Server(cconn, &tls.Config{
		SessionTicketsDisabled: true, // 设置此值为 true ，确保每次都会调用下面的 GetConfigForClient 方法
		GetConfigForClient: func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
			connCtx.ClientConn.clientHello = chi
			name := chi.ServerName
			if name == "" {
				name = req.URL.Hostname()
			}
			c, err := a.ca.GetCert(name)
			if err != nil {
				return nil, err
			}
			return &tls.Config{
				SessionTicketsDisabled: true,
				Certificates:           []tls.Certificate{*c},
				NextProtos:             []string{"http/1.1"}, // only support http/1.1
			}, nil
		},
	})
	connCtx.RecordEvent("TLS client", "Starting intercepted client handshake", nil)
	if err := clientTlsConn.HandshakeContext(ctx); err != nil {
		a.clientTLSError(f, err)
		cconn.Close()
		log.Error(err)
		return
	}
	connCtx.RecordEvent("TLS client", "Client handshake completed", nil)

	// will go to attacker.ServeHTTP
	a.initHttpsDialFn(req)
	a.serveConn(clientTlsConn, connCtx)
}

func (a *attacker) clientTLSError(f *Flow, err error) {
	f.ConnContext.RecordEvent("TLS client", "Client handshake failed; check CA trust, client certificates or certificate pinning", err)
	for _, addon := range a.proxy.Addons {
		addon.HTTPConnectError(f, err)
	}
}

func (a *attacker) attack(res http.ResponseWriter, req *http.Request) {
	proxy := a.proxy

	log := log.WithFields(log.Fields{
		"in":     "Proxy.attacker.attack",
		"url":    req.URL,
		"method": req.Method,
	})

	reply := func(response *Response, body io.Reader, streamed bool) {
		if response.Header != nil {
			for key, value := range response.Header {
				for _, v := range value {
					res.Header().Add(key, v)
				}
			}
		}
		if trailers := response.Trailers(); len(trailers) > 0 {
			// Trailers follow the body, so a response that carries them
			// cannot also declare its length. An HTTP/1.1 client reads them
			// only from a chunked response, so the names are announced
			// before the headers go out; HTTP/2 has a frame for them and
			// needs nothing.
			res.Header().Del("Content-Length")
			if req.ProtoMajor < 2 {
				names := make([]string, 0, len(trailers))
				for name := range trailers {
					names = append(names, name)
				}
				slices.Sort(names)
				res.Header().Add("Trailer", strings.Join(names, ", "))
			}
		}
		if response.close {
			res.Header().Set("Connection", "close")
		}
		res.WriteHeader(response.StatusCode)

		flusher, _ := res.(http.Flusher)
		if streamed && flusher != nil {
			// A streamed response must reach the client before its body
			// ends: a gRPC server stream or a bidirectional call sends
			// nothing until the headers are through, and the client waits
			// for them before it sends the rest of its own stream.
			flusher.Flush()
		}

		copyStream := func(r io.Reader) error {
			if r == nil {
				return nil
			}

			buf := make([]byte, 32*1024)
			for {
				n, err := r.Read(buf)
				if n > 0 {
					if _, werr := res.Write(buf[:n]); werr != nil {
						return werr
					}
					flusher.Flush()
				}
				if err != nil {
					if err == io.EOF {
						return nil
					}
					return err
				}
			}
		}

		// A body that fails part way, because the upstream reset its stream
		// or the client went away, must not end as if it were whole: net/http
		// would close an HTTP/2 stream cleanly or finish the chunked body, and
		// the client would read a short body as complete (a gRPC client, as a
		// call that ended without its status). Aborting the handler resets the
		// stream or closes the connection instead, as httputil.ReverseProxy
		// does.
		abort := func(err error) {
			logErr(log, err)
			panic(http.ErrAbortHandler)
		}
		if body != nil {
			if err := copyStream(body); err != nil {
				abort(err)
			}
		}
		if response.BodyReader != nil {
			if err := copyStream(response.BodyReader); err != nil {
				abort(err)
			}
		}
		if len(response.Body) > 0 {
			if _, err := res.Write(response.Body); err != nil {
				abort(err)
			}
		}
		// The trailer block goes after the body, as HTTP/1.1 chunked
		// trailers or an HTTP/2 trailers frame: never as ordinary headers,
		// since a name may repeat there with another value. The transport
		// fills it while the body is read, so it is read here, once.
		response.Trailer = response.Trailers()
		for key, values := range response.Trailer {
			for _, v := range values {
				res.Header().Add(http.TrailerPrefix+key, v)
			}
		}
	}

	// when addons panic
	defer func() {
		if err := recover(); err != nil {
			if err == http.ErrAbortHandler {
				panic(err)
			}
			log.Warnf("Recovered: %v\n", err)
		}
	}()

	f := newFlow()
	f.Request = newRequest(req)
	f.ConnContext = req.Context().Value(connContextKey).(*ConnContext)
	defer f.finish()

	f.ConnContext.FlowCount.Add(1)

	// A gRPC call is a stream, not a message: a client-streaming or
	// bidirectional call sends no end of body until the server has answered,
	// so buffering the request would deadlock the call, and the response must
	// go out as it arrives for the same reason. The capture keeps both bodies
	// through StreamRequestModifier and StreamResponseModifier instead.
	if isGRPC(f.Request.Header) {
		f.Stream = true
	}

	rawReqUrlHost := f.Request.URL.Host
	rawReqUrlScheme := f.Request.URL.Scheme

	// trigger addon event Requestheaders
	for _, addon := range proxy.Addons {
		addon.Requestheaders(f)
		if f.Response != nil {
			reply(f.Response, nil, false)
			return
		}
	}

	// Read request body
	var reqBody io.Reader = req.Body
	if !f.Stream {
		reqBuf, r, err := helper.ReaderToBuffer(req.Body, proxy.Opts.StreamLargeBodies)
		reqBody = r
		if err != nil {
			for _, addon := range proxy.Addons {
				addon.RequestError(f, err)
			}
			res.WriteHeader(502)
			return
		}

		if reqBuf == nil {
			log.Warnf("request body size >= %v\n", proxy.Opts.StreamLargeBodies)
			f.Stream = true
		} else {
			f.Request.Body = reqBuf

			// trigger addon event Request
			for _, addon := range proxy.Addons {
				addon.Request(f)
				if f.Response != nil {
					reply(f.Response, nil, false)
					return
				}
			}
			reqBody = bytes.NewReader(f.Request.Body)
		}
	}

	for _, addon := range proxy.Addons {
		reqBody = addon.StreamRequestModifier(f, reqBody)
		// A streamed request skipped Request. An addon may reject it here
		// rather than forwarding a body its local response rule cannot read.
		if f.Response != nil {
			reply(f.Response, nil, false)
			return
		}
	}

	proxyReq, err := http.NewRequestWithContext(req.Context(), f.Request.Method, f.Request.URL.String(), reqBody)
	if err != nil {
		for _, addon := range proxy.Addons {
			addon.RequestError(f, err)
		}
		res.WriteHeader(502)
		return
	}

	for key, value := range f.Request.Header {
		if strings.EqualFold(key, "Host") {
			proxyReq.Host = f.Request.Header.Get(key)
			continue
		}
		for _, v := range value {
			proxyReq.Header.Add(key, v)
		}
	}
	// A request that ended with trailers keeps them: the body is complete
	// here, buffered or streamed, so the transport sends them after it. An
	// HTTP/1.1 upstream sends them only with a chunked body, which a declared
	// length rules out.
	if trailers := f.Request.Trailers(); len(trailers) > 0 {
		proxyReq.Trailer = trailers
		proxyReq.ContentLength = -1
	}

	useSeparateClient := f.UseSeparateClient
	if !useSeparateClient {
		if rawReqUrlHost != f.Request.URL.Host || rawReqUrlScheme != f.Request.URL.Scheme {
			useSeparateClient = true
		}
	}

	var proxyRes *http.Response
	if useSeparateClient {
		f.UseSeparateClient = true
		f.ConnContext.RecordEvent("HTTP", "Using a separate upstream transport for "+f.Request.URL.Host, nil)
		proxyReq = proxyReq.WithContext(httptrace.WithClientTrace(proxyReq.Context(), f.ConnContext.trace()))
		verify := !a.proxy.Opts.SslInsecure || f.ConnContext.VerifyUpstream
		client, clientErr := a.upstreamClient(proxyReq, helper.CanonicalAddr(f.Request.URL), verify)
		if clientErr != nil {
			for _, addon := range proxy.Addons {
				addon.RequestError(f, clientErr)
			}
			writeGatewayError(res, f, clientErr)
			return
		}
		proxyRes, err = client.Do(proxyReq)
	} else {
		// A client may keep one forward-proxy connection and send requests
		// for several targets on it. The upstream socket and its transport
		// belong to the target they were dialed for, and that transport hands
		// its socket to any address, so a new target must dial its own.
		// An intercepted tunnel keeps one target for the whole connection.
		if sc := f.ConnContext.ServerConn; sc != nil && !f.ConnContext.ClientConn.Tls {
			if addr := helper.CanonicalAddr(f.Request.URL); sc.Address != addr {
				f.ConnContext.RecordEvent("TCP", "Closing the connection to "+sc.Address+" for "+addr, nil)
				sc.retire()
				f.ConnContext.ServerConn = nil
			}
		}
		if f.ConnContext.ServerConn == nil && f.ConnContext.dialFn != nil {
			if err := f.ConnContext.dialFn(req.Context()); err != nil {
				for _, addon := range proxy.Addons {
					addon.RequestError(f, err)
				}
				// Check for authentication failure
				if strings.Contains(err.Error(), "Proxy Authentication Required") {
					res.Header().Set("Proxy-Authenticate", `Basic realm="proxy"`)
					writeGatewayStatus(res, f, http.StatusProxyAuthRequired, err)
					return
				}
				writeGatewayError(res, f, err)
				return
			}
		}
		proxyRes, err = f.ConnContext.ServerConn.client.Do(proxyReq)
	}
	if err != nil {
		logErr(log, err)
		for _, addon := range proxy.Addons {
			addon.RequestError(f, err)
		}
		if strings.Contains(err.Error(), "Proxy Authentication Required") {
			res.Header().Set("Proxy-Authenticate", `Basic realm="proxy"`)
			writeGatewayStatus(res, f, http.StatusProxyAuthRequired, err)
			return
		}
		writeGatewayError(res, f, err)
		return
	}

	if proxyRes.Close {
		f.ConnContext.closeAfterResponse.Store(true)
	}

	defer proxyRes.Body.Close()

	f.Response = &Response{
		StatusCode: proxyRes.StatusCode,
		Header:     proxyRes.Header,
		Trailer:    proxyRes.Trailer,
		Proto:      proxyRes.Proto,
		close:      proxyRes.Close,
		raw:        proxyRes,
	}
	// A gRPC server may end a call in the header block itself, with its
	// status there and no body at all: a client reads that frame as the
	// trailers. Hand the status over as trailers so the call keeps its
	// shape and its status reaches the client as a status, not as metadata.
	if isGRPC(f.Response.Header) && f.Response.Header.Get("Grpc-Status") != "" {
		moveGRPCStatusToTrailer(f.Response)
	}

	// trigger addon event Responseheaders
	for _, addon := range proxy.Addons {
		addon.Responseheaders(f)
		if f.Response.Body != nil {
			reply(f.Response, nil, false)
			return
		}
	}

	// 检测是否为 SSE 响应，如果是则强制使用流式模式
	isSSE := strings.Contains(f.Response.Header.Get("Content-Type"), "text/event-stream")
	if isSSE {
		f.Stream = true
		f.SSE = newSSEData()

		// 触发 SSEStart hook
		for _, addon := range proxy.Addons {
			addon.SSEStart(f)
		}
		// Observe termination even for an empty body or a downstream write
		// failure: the SSE reader may never get another Read returning EOF.
		defer func() {
			for _, addon := range proxy.Addons {
				addon.SSEEnd(f)
			}
		}()

		log.Debugf("SSE stream detected for %s", f.Request.URL.String())
	}

	// Read response body
	var resBody io.Reader = proxyRes.Body
	if !f.Stream {
		resBuf, r, err := helper.ReaderToBuffer(proxyRes.Body, proxy.Opts.StreamLargeBodies)
		resBody = r
		if err != nil {
			for _, addon := range proxy.Addons {
				addon.RequestError(f, err)
			}
			res.WriteHeader(502)
			return
		}
		if resBuf == nil {
			log.Warnf("response body size >= %v\n", proxy.Opts.StreamLargeBodies)
			f.Stream = true
		} else {
			f.Response.Body = resBuf

			// trigger addon event Response
			for _, addon := range proxy.Addons {
				addon.Response(f)
			}
		}
	}

	// 如果是 SSE，包装 reader 以实时解析事件
	if isSSE {
		resBody = newSSEReader(f, resBody)
	}

	for _, addon := range proxy.Addons {
		resBody = addon.StreamResponseModifier(f, resBody)
	}

	reply(f.Response, resBody, f.Stream)
}

// isGRPC reports whether a message is gRPC, whose bodies are streams of
// length-prefixed messages: a call may end its request only after the server
// has answered, so neither side can be buffered.
//
// The gRPC-Web content types are not gRPC here: a browser sends gRPC-Web as an
// ordinary request whose body is complete, and the proxy already buffers and
// shows those.
func isGRPC(h http.Header) bool {
	contentType := strings.ToLower(strings.TrimSpace(h.Get("Content-Type")))
	if mediaType, _, found := strings.Cut(contentType, ";"); found {
		contentType = strings.TrimSpace(mediaType)
	}
	if strings.HasPrefix(contentType, "application/grpc-web") {
		return false
	}
	return contentType == "application/grpc" || strings.HasPrefix(contentType, "application/grpc+")
}

// moveGRPCStatusToTrailer moves the status a trailers-only gRPC response
// carries in its header block into the trailers, where a gRPC client looks for
// it, keeping every value of every repeated field.
func moveGRPCStatusToTrailer(r *Response) {
	if r.Trailer == nil {
		r.Trailer = make(http.Header)
	}
	for key, values := range r.Header {
		if !strings.HasPrefix(strings.ToLower(key), "grpc-") {
			continue
		}
		r.Trailer[key] = append(r.Trailer[key], values...)
		delete(r.Header, key)
	}
}
