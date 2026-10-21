package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// maxWSReplies bounds what a composed WebSocket keeps: the first answers say
// whether the endpoint works.
const maxWSReplies = 5

// isWebSocket reports whether a URL asks for a WebSocket, so the editor sends
// a message instead of a request.
func isWebSocket(rawURL string) bool {
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
func wsSend(ctx context.Context, proxyAddr, rawURL, message string, wait time.Duration) ([]string, error) {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		Proxy: func(*http.Request) (*url.URL, error) {
			return url.Parse("http://" + proxyAddr)
		},
	}
	conn, resp, err := dialer.DialContext(ctx, rawURL, nil)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("%s: %w", resp.Status, err)
		}
		return nil, err
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
		return nil, err
	}
	var replies []string
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	for len(replies) < maxWSReplies {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		replies = append(replies, string(data))
	}
	return replies, nil
}
