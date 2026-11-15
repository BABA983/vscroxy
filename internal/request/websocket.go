package request

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/gorilla/websocket"
	"golang.org/x/net/http/httpguts"
)

// WebSocketConfig is saved with a ws/wss request: the messages it sends, in
// order, and how long each one collects replies. HTTP requests leave it empty.
type WebSocketConfig struct {
	Messages []WebSocketMessage `json:"messages,omitempty"`
	// WaitMS is how long each message collects replies before the next one is
	// sent. Zero uses DefaultWebSocketWait; MaxWebSocketWait is the ceiling.
	WaitMS int `json:"waitMs,omitzero"`
}

// WebSocketMessage is one message a saved ws/wss request sends. Encoding is
// Text (the default), Hex or Base64; anything but Text goes out as a binary
// frame. Payload keeps the message as authored, so variables survive until the
// send.
type WebSocketMessage struct {
	Encoding string `json:"encoding,omitempty"`
	Payload  string `json:"payload,omitempty"`
}

const (
	// DefaultWebSocketWait is the reply window after a message when the request
	// leaves WaitMS unset.
	DefaultWebSocketWait = 2 * time.Second
	// MaxWebSocketWait bounds a request's reply window.
	MaxWebSocketWait = 60 * time.Second
	// MaxWebSocketMessage bounds one sent message.
	MaxWebSocketMessage = 1 << 20
	// maxWSReplies bounds what one message keeps: a chatty server must not grow
	// a run's result without limit.
	maxWSReplies = 200
	// maxWSReplyBytes bounds one message's retained replies.
	maxWSReplyBytes = 1 << 20
)

// Wait is the reply window after each message.
func (c WebSocketConfig) Wait() time.Duration {
	wait := time.Duration(c.WaitMS) * time.Millisecond
	switch {
	case wait <= 0:
		return DefaultWebSocketWait
	case wait > MaxWebSocketWait:
		return MaxWebSocketWait
	}
	return wait
}

// Decode returns the frame kind and the bytes to send.
func (m WebSocketMessage) Decode() (int, []byte, error) {
	kind := websocket.TextMessage
	var data []byte
	switch strings.ToLower(strings.TrimSpace(m.Encoding)) {
	case "", "text":
		if !utf8.ValidString(m.Payload) {
			return 0, nil, errors.New("text messages must be valid UTF-8; use Hex or Base64 for binary")
		}
		data = []byte(m.Payload)
	case "hex":
		decoded, err := hex.DecodeString(strings.Join(strings.Fields(m.Payload), ""))
		if err != nil {
			return 0, nil, fmt.Errorf("hex message: %w", err)
		}
		kind, data = websocket.BinaryMessage, decoded
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(m.Payload)
		if err != nil {
			return 0, nil, fmt.Errorf("base64 message: %w", err)
		}
		kind, data = websocket.BinaryMessage, decoded
	default:
		return 0, nil, fmt.Errorf("unknown WebSocket message encoding %q", m.Encoding)
	}
	if len(data) > MaxWebSocketMessage {
		return 0, nil, fmt.Errorf("messages are limited to %d MiB", MaxWebSocketMessage>>20)
	}
	return kind, data, nil
}

// webSocketMessages returns what a ws/wss request sends: its saved list with
// variables expanded, or body as one text message when the list is empty. body
// is the prepared body, so a pre-request script's edit still reaches a request
// saved with a single message.
func (r Request) webSocketMessages(body string, vars map[string]string) []WebSocketMessage {
	if len(r.WebSocket.Messages) > 0 {
		messages := make([]WebSocketMessage, len(r.WebSocket.Messages))
		for i, message := range r.WebSocket.Messages {
			message.Payload = ExpandVars(message.Payload, vars)
			messages[i] = message
		}
		return messages
	}
	if body == "" {
		return nil
	}
	return []WebSocketMessage{{Payload: body}}
}

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

// WebSocketExchange is one sent message and the replies that arrived before
// the next message or the end of its wait.
type WebSocketExchange struct {
	Message WebSocketMessage
	Replies []string
}

// wsSendMessages opens a WebSocket through the proxy, sends each message and
// collects its replies for wait. The exchange is captured like any other, so
// the frames show in the table too. A request with no messages listens for
// pushes instead, returned separately because no message belongs to them. A
// failed send keeps the exchanges already made.
//
// One reader owns the connection for the whole exchange: gorilla treats a read
// deadline as a broken connection, so each message's window consumes from a
// channel the reader fills instead of setting a deadline per message.
func wsSendMessages(ctx context.Context, proxyAddr string, req *Prepared, messages []WebSocketMessage, wait time.Duration, ca []byte, certificates certs.Settings, jar http.CookieJar) ([]WebSocketExchange, []string, error) {
	conn, err := DialWebSocket(ctx, proxyAddr, req, ca, certificates, jar)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	conn.SetReadLimit(MaxWebSocketMessage)
	frames, readErr, stop := make(chan []byte, 64), make(chan error, 1), make(chan struct{})
	defer close(stop)
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				select {
				case readErr <- err:
				case <-stop:
				}
				return
			}
			select {
			case frames <- data:
			case <-stop:
				return
			}
		}
	}()
	window := func(exchange *WebSocketExchange) (bool, error) {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		size := 0
		for {
			select {
			case data := <-frames:
				exchange.Replies = append(exchange.Replies, string(data))
				size += len(data)
				if len(exchange.Replies) >= maxWSReplies || size >= maxWSReplyBytes {
					return false, nil
				}
			case err := <-readErr:
				if ctx.Err() != nil {
					return false, ctx.Err()
				}
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					return true, nil
				}
				return false, err
			case <-timer.C:
				return false, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}
	}
	if len(messages) == 0 {
		exchange := WebSocketExchange{}
		_, err := window(&exchange)
		return nil, exchange.Replies, err
	}
	exchanges := make([]WebSocketExchange, 0, len(messages))
	for i, message := range messages {
		kind, data, err := message.Decode()
		if err != nil {
			return exchanges, nil, err
		}
		if err := conn.WriteMessage(kind, data); err != nil {
			if ctx.Err() != nil {
				return exchanges, nil, ctx.Err()
			}
			return exchanges, nil, err
		}
		exchanges = append(exchanges, WebSocketExchange{Message: message})
		closed, err := window(&exchanges[len(exchanges)-1])
		if err != nil {
			return exchanges, nil, err
		}
		if closed {
			if i < len(messages)-1 {
				return exchanges, nil, errors.New("the server closed the connection before every message was sent")
			}
			return exchanges, nil, nil
		}
	}
	return exchanges, nil, nil
}

func DialWebSocket(ctx context.Context, proxyAddr string, req *Prepared, ca []byte, certificates certs.Settings, jar http.CookieJar) (*websocket.Conn, error) {
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
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, err
	}
	var tlsConfig *tls.Config
	if u.Scheme == "wss" {
		tlsConfig, err = certificates.TLSConfig(certs.Address(u), ca, false)
		if err != nil {
			return nil, err
		}
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig:  tlsConfig,
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
