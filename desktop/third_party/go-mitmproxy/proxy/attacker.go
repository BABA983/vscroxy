package proxy

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"slices"
	"strings"

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
	client   *http.Client
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
		client: &http.Client{
			Transport: &http.Transport{
				Proxy:              proxy.realUpstreamProxy(),
				ForceAttemptHTTP2:  true,
				DisableCompression: true, // To get the original response from the server, set Transport.DisableCompression to true.
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: proxy.Opts.SslInsecure,
					KeyLogWriter:       helper.GetTlsKeyLogWriter(),
				},
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// 禁止自动重定向
				return http.ErrUseLastResponse
			},
		},
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
		f.finish()

		if err := a.proxy.webSocketHandler.handleWSS(res, req); err != nil {
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

// send clientHello to server, server handshake
func (a *attacker) serverTlsHandshake(ctx context.Context, connCtx *ConnContext) error {
	proxy := a.proxy
	clientHello := connCtx.ClientConn.clientHello
	serverConn := connCtx.ServerConn

	serverTlsConfig := &tls.Config{
		InsecureSkipVerify: proxy.Opts.SslInsecure,
		KeyLogWriter:       helper.GetTlsKeyLogWriter(),
		ServerName:         clientHello.ServerName,
		NextProtos:         clientHello.SupportedProtos,
		// CurvePreferences:   clientHello.SupportedCurves, // todo: 如果打开会出错
		CipherSuites: clientHello.CipherSuites,
	}
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
	if proxy.Opts.SslInsecure {
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
	serverConn.Address = req.Host
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

		if body != nil {
			err := copyStream(body)
			if err != nil {
				logErr(log, err)
			}
		}
		if response.BodyReader != nil {
			err := copyStream(response.BodyReader)
			if err != nil {
				logErr(log, err)
			}
		}
		if len(response.Body) > 0 {
			_, err := res.Write(response.Body)
			if err != nil {
				logErr(log, err)
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
	}

	proxyReqCtx := context.WithValue(req.Context(), proxyReqCtxKey, req)
	proxyReq, err := http.NewRequestWithContext(proxyReqCtx, f.Request.Method, f.Request.URL.String(), reqBody)
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
		proxyRes, err = a.client.Do(proxyReq)
	} else {
		if f.ConnContext.ServerConn == nil && f.ConnContext.dialFn != nil {
			if err := f.ConnContext.dialFn(req.Context()); err != nil {
				for _, addon := range proxy.Addons {
					addon.RequestError(f, err)
				}
				// Check for authentication failure
				if strings.Contains(err.Error(), "Proxy Authentication Required") {
					httpError(res, "", http.StatusProxyAuthRequired)
					return
				}
				res.WriteHeader(502)
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
		res.WriteHeader(502)
		return
	}

	if proxyRes.Close {
		f.ConnContext.closeAfterResponse = true
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
