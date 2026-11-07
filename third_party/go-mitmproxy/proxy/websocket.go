package proxy

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lqqyt2423/go-mitmproxy/internal/helper"
	log "github.com/sirupsen/logrus"
)

var wsHandshakeHeaders = map[string]struct{}{
	"Upgrade":                  {},
	"Connection":               {},
	"Sec-Websocket-Key":        {},
	"Sec-Websocket-Version":    {},
	"Sec-Websocket-Extensions": {},
	"Sec-Websocket-Protocol":   {},
}

func cloneHeaderWithoutWSHandshake(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, vals := range h {
		if _, skip := wsHandshakeHeaders[http.CanonicalHeaderKey(k)]; skip {
			continue
		}
		cloned := make([]string, len(vals))
		copy(cloned, vals)
		out[k] = cloned
	}
	return out
}

type webSocketHandler struct {
	proxy *Proxy
}

func newWebSocketHandler(proxy *Proxy) *webSocketHandler {
	return &webSocketHandler{proxy: proxy}
}

// connResponseWriter 自定义 ResponseWriter，包装 net.Conn
// 用于让 websocket.Upgrader 能够升级连接
type connResponseWriter struct {
	conn        net.Conn
	header      http.Header
	statusCode  int
	wroteHeader bool
}

func newConnResponseWriter(conn net.Conn) *connResponseWriter {
	return &connResponseWriter{
		conn:   conn,
		header: make(http.Header),
	}
}

func (w *connResponseWriter) Header() http.Header {
	return w.header
}

func (w *connResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.conn.Write(data)
}

func (w *connResponseWriter) WriteHeader(statusCode int) {
	w.wroteHeader = true
	w.statusCode = statusCode
}

// Hijack 劫持连接，返回底层的 net.Conn 和 bufio.ReadWriter
// websocket.Upgrader.Upgrade() 需要调用这个方法
func (w *connResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	buf := bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn))
	return w.conn, buf, nil
}

// handle 处理 WebSocket 连接
// serverConn: 与服务器的连接（已经建立 TCP 连接）, or nil to dial the server
// the handshake goes to once addons have seen it.
// clientConn: 与客户端的连接（已经完成 CONNECT，客户端发送了 WebSocket 握手请求）
func (h *webSocketHandler) handle(serverConn, clientConn net.Conn, f *Flow) error {
	// 步骤 1: 读取客户端握手请求
	buf := bufio.NewReader(clientConn)
	clientReq, err := http.ReadRequest(buf)
	if err != nil {
		log.Errorf("Failed to read client handshake: %v", err)
		return err
	}

	// Keep the actual upgrade request, not the CONNECT envelope, for addons.
	wsURL := *clientReq.URL
	wsURL.Scheme, wsURL.Host = "ws", clientReq.Host
	clientReq.URL = &wsURL
	f.Request = newRequest(clientReq)
	// Addons may point the WebSocket elsewhere, as for a decrypted one.
	for _, addon := range h.proxy.Addons {
		addon.Requestheaders(f)
	}
	target := f.Request.URL
	if serverConn == nil || target.Host != clientReq.Host || target.Scheme != "ws" {
		dialReq := clientReq.WithContext(context.WithValue(clientReq.Context(), connContextKey, f.ConnContext))
		conn, err := h.proxy.getUpstreamConn(dialReq.Context(), webSocketDialRequest(dialReq, target))
		if err != nil {
			return err
		}
		// Closing the wrapper would end the client's tunnel too: replace
		// only the socket it wraps.
		if w, ok := serverConn.(*wrapServerConn); ok {
			w.Conn.Close()
			w.Conn = conn
		} else {
			if serverConn != nil {
				serverConn.Close()
			}
			serverConn = conn
		}
	}

	log.Debugf("Client WebSocket handshake: %s %s", clientReq.Method, clientReq.URL.Path)

	// 步骤 2: 使用 Dialer 连接到服务器
	dialer := &websocket.Dialer{
		NetDial: func(network, addr string) (net.Conn, error) {
			return serverConn, nil
		},
		Subprotocols:     websocket.Subprotocols(clientReq),
		HandshakeTimeout: 0,
	}
	if target.Scheme == "wss" {
		verify := !h.proxy.Opts.SslInsecure || f.ConnContext.VerifyUpstream
		tlsConfig, configErr := h.proxy.upstreamTLSConfig(helper.CanonicalAddr(target), verify)
		if configErr != nil {
			return configErr
		}
		dialer.TLSClientConfig = tlsConfig
	}

	serverURL := target.String()
	log.Debugf("Connecting to server: %s", serverURL)

	// Regenerate protocol headers, preserving auth, cookies and custom headers.
	serverWS, _, err := dialer.Dial(serverURL, cloneHeaderWithoutWSHandshake(f.Request.Header))
	if err != nil {
		log.Errorf("Failed to dial server: %v", err)
		return err
	}
	defer serverWS.Close()
	if selected := serverWS.Subprotocol(); selected != "" && !slices.Contains(dialer.Subprotocols, selected) {
		return fmt.Errorf("upstream selected an unoffered WebSocket subprotocol")
	}

	log.Debugf("Server WebSocket connected, subprotocol: %s", serverWS.Subprotocol())

	// 步骤 3: 使用 Upgrader 升级客户端连接
	respWriter := newConnResponseWriter(clientConn)

	upgrader := &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		Subprotocols:    []string{serverWS.Subprotocol()},
		CheckOrigin: func(r *http.Request) bool {
			return true // 代理模式下总是允许
		},
	}

	clientWS, err := upgrader.Upgrade(respWriter, clientReq, nil)
	if err != nil {
		log.Errorf("Failed to upgrade client connection: %v", err)
		return err
	}
	defer clientWS.Close()

	log.Debugf("Client WebSocket upgraded successfully")

	wsData := newWebSocketData()
	f.WebSocket = wsData

	for _, addon := range h.proxy.Addons {
		addon.WebSocketStart(f)
	}

	// 步骤 4: 双向转发消息
	return h.forwardMessages(clientWS, serverWS, f)
}

// forwardMessages 双向转发 WebSocket 消息
func (h *webSocketHandler) forwardMessages(clientWS, serverWS *websocket.Conn, f *Flow) error {
	defer func() {
		for _, addon := range h.proxy.Addons {
			addon.WebSocketEnd(f)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errChan := make(chan error, 5)
	fail := func(err error) {
		select {
		case errChan <- err:
		default:
		}
		cancel()
	}
	var readers, writers sync.WaitGroup
	// Local shutdown must release hooks even if both directions are paused.
	readers.Go(func() {
		select {
		case <-f.ConnContext.ClientConn.Conn.(*wrapClientConn).closeChan:
			fail(net.ErrClosed)
		case <-ctx.Done():
		}
	})
	forward := func(src, dst *websocket.Conn, fromClient bool) {
		messages := make(chan *WebSocketMessage)
		// One bounded lookahead keeps reading control/close frames while a
		// message hook waits. A busy sender then gets normal TCP backpressure.
		readers.Go(func() {
			defer close(messages)
			for {
				kind, data, err := src.ReadMessage()
				if err != nil {
					fail(err)
					return
				}
				select {
				case messages <- newWebSocketMessage(kind, data, fromClient):
				case <-ctx.Done():
					return
				}
			}
		})
		writers.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case m, ok := <-messages:
					if !ok {
						return
					}
					for _, addon := range h.proxy.Addons {
						addon.WebSocketMessage(ctx, f, m)
					}
					f.WebSocket.addMessage(m)
					if !m.Drop {
						if err := dst.WriteMessage(m.Type, m.Content); err != nil {
							fail(err)
							return
						}
					}
				}
			}
		})
	}
	forward(clientWS, serverWS, true)
	forward(serverWS, clientWS, false)
	<-ctx.Done()
	// Let an already received last message finish, but bound a blocked write.
	// A socket deadline is insufficient: gorilla resets it inside WriteMessage.
	// Close is safe concurrently with readers/writers and cannot be reset.
	forceClose := time.AfterFunc(time.Second, func() {
		clientWS.Close()
		serverWS.Close()
	})
	writers.Wait()
	forceClose.Stop()
	deadline := time.Now().Add(time.Second)
	for _, c := range []*websocket.Conn{clientWS, serverWS} {
		c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline)
		c.Close()
	}
	readers.Wait()
	err := <-errChan
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		return nil
	}
	return err
}

// webSocketDialRequest is req pointed at a WebSocket's target, as an http(s)
// request, for getUpstreamConn.
func webSocketDialRequest(req *http.Request, target *url.URL) *http.Request {
	u := *target
	u.Scheme = map[string]string{"ws": "http", "wss": "https"}[target.Scheme]
	dial := req.Clone(req.Context())
	dial.URL, dial.Host = &u, u.Host
	return dial
}

// handleWSS proxies a decrypted WebSocket whose handshake f holds, to the
// URL f.Request.URL says, which addons may have changed.
func (h *webSocketHandler) handleWSS(res http.ResponseWriter, req *http.Request, f *Flow) error {
	defer f.finish()
	target := f.Request.URL
	serverURL := target.String()
	log.Debugf("Connecting to WSS server: %s", serverURL)

	connCtx := req.Context().Value(connContextKey).(*ConnContext)

	// 步骤 1: 获取上游连接
	plainConn, err := h.proxy.getUpstreamConn(req.Context(), webSocketDialRequest(req, target))
	if err != nil {
		log.Errorf("Failed to get upstream connection: %v", err)
		return err
	}

	// 步骤 2: 创建并初始化 ServerConn
	serverConn := newServerConn()
	serverConn.Address = target.Host
	serverConn.Conn = &wrapServerConn{
		Conn:    plainConn,
		proxy:   h.proxy,
		connCtx: connCtx,
	}
	connCtx.ServerConn = serverConn

	// 步骤 3: 调用 addon 的 ServerConnected 回调
	for _, addon := range connCtx.proxy.Addons {
		addon.ServerConnected(connCtx)
	}

	// 步骤 4: 创建 Dialer，使用自定义 NetDial
	verify := !h.proxy.Opts.SslInsecure || connCtx.VerifyUpstream
	tlsConfig, err := h.proxy.upstreamTLSConfig(helper.CanonicalAddr(target), verify)
	if err != nil {
		return err
	}
	dialer := &websocket.Dialer{
		NetDial: func(network, addr string) (net.Conn, error) {
			return serverConn.Conn, nil
		},
		Subprotocols:    websocket.Subprotocols(req),
		TLSClientConfig: tlsConfig,
	}

	upstreamHeader := cloneHeaderWithoutWSHandshake(f.Request.Header)
	serverWS, _, err := dialer.Dial(serverURL, upstreamHeader)
	if err != nil {
		log.Errorf("Failed to dial WSS server: %v", err)
		return err
	}
	defer serverWS.Close()
	if selected := serverWS.Subprotocol(); selected != "" && !slices.Contains(dialer.Subprotocols, selected) {
		return fmt.Errorf("upstream selected an unoffered WebSocket subprotocol")
	}

	upgrader := &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		Subprotocols:    []string{serverWS.Subprotocol()},
		CheckOrigin: func(r *http.Request) bool {
			return true // 代理模式下总是允许
		},
	}

	clientWS, err := upgrader.Upgrade(res, req, nil)
	if err != nil {
		log.Errorf("Failed to upgrade client connection: %v", err)
		return err
	}
	defer clientWS.Close()

	log.Debugf("Client WSS upgraded successfully")

	f.WebSocket = newWebSocketData()

	for _, addon := range h.proxy.Addons {
		addon.WebSocketStart(f)
	}

	// 双向转发消息
	return h.forwardMessages(clientWS, serverWS, f)
}
