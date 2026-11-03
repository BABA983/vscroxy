package request

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/net/http/httpguts"
)

// maxWSReplies bounds what a composed WebSocket keeps: the first answers say
// whether the endpoint works.
const maxWSReplies = 5

// IsWebSocket reports whether a URL asks for a WebSocket, so the editor sends
// a message instead of a request.
func IsWebSocket(rawURL string) bool {
	scheme, _, ok := strings.Cut(rawURL, "://")
	if !ok {
		return false
	}
	switch strings.ToLower(scheme) {
	case "ws", "wss":
		return true
	}
	return false
}

// wsSend opens a WebSocket through the proxy, sends one text message and
// collects what comes back until wait is over. The exchange is captured like
// any other, so the frames show in the table too.
func wsSend(ctx context.Context, proxyAddr string, req *Prepared, ca []byte, jar http.CookieJar, wait time.Duration) ([]string, error) {
	conn, err := DialWebSocket(ctx, proxyAddr, req, ca, jar)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetReadLimit(1 << 20)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(req.Body)); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	var replies []string
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	for len(replies) < maxWSReplies {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return replies, ctx.Err()
			}
			if timeout, ok := errors.AsType[net.Error](err); ok && timeout.Timeout() || websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				break
			}
			return replies, err
		}
		replies = append(replies, string(data))
	}
	return replies, nil
}

func DialWebSocket(ctx context.Context, proxyAddr string, req *Prepared, ca []byte, jar http.CookieJar) (*websocket.Conn, error) {
	header := req.Header.Clone()
	var protocols []string
	for _, value := range header.Values("Sec-WebSocket-Protocol") {
		for part := range strings.SplitSeq(value, ",") {
			protocol := strings.TrimSpace(part)
			if !httpguts.ValidHeaderFieldName(protocol) || slices.Contains(protocols, protocol) {
				return nil, fmt.Errorf("WebSocket subprotocols must be unique HTTP tokens separated by commas")
			}
			protocols = append(protocols, protocol)
		}
	}
	header.Del("Sec-WebSocket-Protocol")
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if len(ca) > 0 {
		cert, err := x509.ParseCertificate(ca)
		if err != nil {
			return nil, err
		}
		roots.AddCert(cert)
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig:  &tls.Config{RootCAs: roots},
		Subprotocols:     protocols,
		Jar:              jar,
		Proxy: func(*http.Request) (*url.URL, error) {
			return url.Parse("http://" + proxyAddr)
		},
	}
	conn, resp, err := dialer.DialContext(ctx, req.URL, header)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if resp != nil {
			return nil, fmt.Errorf("%s: %w", resp.Status, err)
		}
		return nil, err
	}
	if selected := conn.Subprotocol(); selected != "" && !slices.Contains(protocols, selected) {
		conn.Close()
		return nil, fmt.Errorf("server selected an unoffered WebSocket subprotocol")
	}
	return conn, nil
}
