package request

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

// SocketIOConfig is saved with a request document. URL's path is the Engine.IO
// endpoint (default /socket.io/); Namespace is independent of that HTTP path.
type SocketIOConfig struct {
	Namespace   string `json:"namespace,omitempty"`
	Auth        string `json:"auth,omitempty"`
	Event       string `json:"event,omitempty"`
	Attachments string `json:"attachments,omitempty"`
	WantACK     bool   `json:"wantAck,omitzero"`
}

// SocketIOMessage keeps JSON placeholders and raw attachments, not lossy base64
// substitutions. ACKID belongs to this connection only; nil means no ACK.
type SocketIOMessage struct {
	Event       string
	Args        jsontext.Value
	Attachments [][]byte
	ACKID       *uint64
	IsACK       bool
}

// SocketIOConn supports one namespace over EIO4 WebSocket with the default
// Socket.IO v3/v4 parser. Read must run continuously (one reader) for heartbeat
// and delivery. Emit/Acknowledge are serialized; Close is safe concurrently.
// There is deliberately no reconnect, retry or disconnected send buffer.
type SocketIOConn struct {
	ws            *websocket.Conn
	namespace     string
	SID           string
	heartbeat     time.Duration
	limit         int
	mu            sync.Mutex
	closed        bool
	next          uint64
	pending, owed map[uint64]bool
	stop          func() bool
	ctx           context.Context
}

func DialSocketIO(ctx context.Context, proxy string, req *Prepared, cfg SocketIOConfig, ca []byte, jar http.CookieJar) (*SocketIOConn, error) {
	if proxy == "" {
		return nil, errors.New("Socket.IO requires a running capture proxy")
	}
	ns, err := socketIONamespace(cfg.Namespace)
	if err != nil {
		return nil, err
	}
	auth := jsontext.Value(strings.TrimSpace(cfg.Auth))
	if len(auth) == 0 {
		auth = jsontext.Value(`{}`)
	}
	if len(auth) > socketIOLimit || auth.Kind() != '{' || !auth.IsValid() {
		return nil, errors.New("Socket.IO auth must be a JSON object (1 MiB maximum)")
	}
	u, err := url.Parse(req.URL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("Socket.IO requires an absolute HTTP(S) or WS(S) endpoint without credentials or a fragment")
	}
	switch u.Scheme {
	case "http", "ws":
		u.Scheme = "ws"
	case "https", "wss":
		u.Scheme = "wss"
	default:
		return nil, errors.New("Socket.IO requires an HTTP(S) or WS(S) endpoint")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/socket.io/"
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, errors.New("invalid Socket.IO endpoint query")
	}
	q.Set("EIO", "4")
	q.Set("transport", "websocket")
	q.Del("sid")
	u.RawQuery = q.Encode()
	r := *req
	r.URL, r.Body = u.String(), ""
	ws, err := DialWebSocket(ctx, proxy, &r, ca, jar)
	if err != nil {
		return nil, err
	}
	c := &SocketIOConn{ws: ws, namespace: ns, limit: socketIOLimit, pending: map[uint64]bool{}, owed: map[uint64]bool{}, ctx: ctx}
	// Closing the transport interrupts a dial/read/write without acquiring the
	// send lock, including cancellation while a writer is backpressured.
	c.stop = context.AfterFunc(ctx, func() { ws.Close() })
	ok := false
	defer func() {
		if !ok {
			c.Close()
		}
	}()
	ws.SetReadLimit(socketIOLimit)
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	typ, raw, err := ws.ReadMessage()
	if err != nil {
		return nil, c.readError(err)
	}
	var open struct {
		SID          string `json:"sid"`
		PingInterval int64  `json:"pingInterval"`
		PingTimeout  int64  `json:"pingTimeout"`
		MaxPayload   int    `json:"maxPayload"`
	}
	if typ != websocket.TextMessage || len(raw) < 2 || raw[0] != '0' || json.Unmarshal(raw[1:], &open) != nil || open.SID == "" || open.PingInterval <= 0 || open.PingTimeout <= 0 || open.PingInterval > 86400000 || open.PingTimeout > 86400000 {
		return nil, errors.New("invalid Engine.IO 4 handshake")
	}
	c.heartbeat = time.Duration(open.PingInterval+open.PingTimeout) * time.Millisecond
	if open.MaxPayload > 0 {
		c.limit = min(c.limit, open.MaxPayload)
	}
	if err := c.write(socketIOPacket{kind: '0', ns: ns, data: auth}, nil); err != nil {
		return nil, err
	}
	// Namespace approval has its own deadline: server pings cannot keep a
	// refused/unanswered CONNECT alive indefinitely.
	until := time.Now().Add(10 * time.Second)
	pingDeadline := time.Now().Add(c.heartbeat)
	_ = ws.SetReadDeadline(minTime(until, pingDeadline))
	for {
		typ, raw, err = ws.ReadMessage()
		if err != nil {
			return nil, c.readError(err)
		}
		if typ != websocket.TextMessage || !utf8.Valid(raw) {
			return nil, errors.New("expected Socket.IO namespace approval")
		}
		if len(raw) > 0 && raw[0] == '2' {
			if err := c.pong(raw[1:]); err != nil {
				return nil, err
			}
			pingDeadline = time.Now().Add(c.heartbeat)
			_ = ws.SetReadDeadline(minTime(until, pingDeadline))
			continue
		}
		if len(raw) < 2 || raw[0] != '4' {
			return nil, errors.New("expected Socket.IO namespace approval")
		}
		p, err := parseSocketIOPacket(raw[1:])
		if err != nil {
			return nil, err
		}
		if p.ns != ns {
			return nil, errors.New("unexpected Socket.IO namespace")
		}
		if p.kind == '4' {
			return nil, errors.New("Socket.IO namespace connection rejected (check namespace and auth)")
		}
		var approved struct {
			SID string `json:"sid"`
		}
		if p.kind != '0' || json.Unmarshal(p.data, &approved) != nil || approved.SID == "" {
			return nil, errors.New("invalid Socket.IO namespace approval")
		}
		c.SID = approved.SID
		_ = ws.SetReadDeadline(pingDeadline)
		ok = true
		return c, nil
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (c *SocketIOConn) readError(err error) error {
	if c.ctx.Err() != nil {
		return c.ctx.Err()
	}
	return err
}

func (c *SocketIOConn) Close() {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		c.ws.Close()
		clear(c.pending)
		clear(c.owed)
	}
	c.mu.Unlock()
	if c.stop != nil {
		c.stop()
	}
}

// write requires mu except during single-threaded dialing.
func (c *SocketIOConn) write(p socketIOPacket, attachments [][]byte) error {
	if c.closed {
		return errors.New("Socket.IO is disconnected; events are not buffered")
	}
	wire := p.wire()
	size := len(wire)
	for _, b := range attachments {
		size += len(b)
	}
	if size > c.limit {
		return errors.New("Socket.IO packet and attachments exceed the negotiated limit (at most 1 MiB)")
	}
	_ = c.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err := c.ws.WriteMessage(websocket.TextMessage, wire)
	for _, b := range attachments {
		if err != nil {
			break
		}
		err = c.ws.WriteMessage(websocket.BinaryMessage, b)
	}
	if err != nil {
		c.closed = true
		c.ws.Close()
		clear(c.pending)
		clear(c.owed)
	}
	return err
}

func (c *SocketIOConn) pong(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return io.EOF
	}
	_ = c.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return c.ws.WriteMessage(websocket.TextMessage, append([]byte{'3'}, data...))
}

func (c *SocketIOConn) Emit(event string, args jsontext.Value, attachments [][]byte, wantACK bool) (*uint64, error) {
	switch event {
	case "", "connect", "connect_error", "disconnect", "disconnecting", "newListener", "removeListener":
		return nil, errors.New("choose a non-reserved Socket.IO event name")
	}
	if !utf8.ValidString(event) || len(event) > 4096 || args.Kind() != '[' || len(args) > socketIOLimit || !args.IsValid() || len(attachments) > socketIOMaxAttachments {
		return nil, errors.New("Socket.IO arguments must be a JSON array, with at most 32 binary attachments")
	}
	name, _ := json.Marshal(event)
	var values []jsontext.Value
	if err := json.Unmarshal(args, &values); err != nil {
		return nil, errors.New("invalid Socket.IO argument array")
	}
	values = append([]jsontext.Value{name}, values...)
	data, _ := json.Marshal(values)
	if err := socketIOPlaceholders(data, len(attachments)); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p := socketIOPacket{kind: '2', ns: c.namespace, data: data, count: len(attachments)}
	if p.count > 0 {
		p.kind = '5'
	}
	if wantACK {
		if len(c.pending) >= 128 || c.next > socketIOMaxAck {
			return nil, errors.New("Socket.IO has 128 outstanding ACKs; reconnect or wait for replies")
		}
		p.id = new(c.next)
	}
	if err := c.write(p, attachments); err != nil {
		return nil, err
	}
	if p.id != nil {
		c.pending[*p.id] = true
		c.next++
	}
	return p.id, nil
}

func (c *SocketIOConn) Acknowledge(id uint64, args jsontext.Value, attachments [][]byte) error {
	if args.Kind() != '[' || len(args) > socketIOLimit || !args.IsValid() || len(attachments) > socketIOMaxAttachments {
		return errors.New("Socket.IO ACK arguments must be a JSON array, with at most 32 binary attachments")
	}
	if err := socketIOPlaceholders(args, len(attachments)); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.owed[id] {
		return errors.New("Socket.IO ACK is not pending on this connection")
	}
	p := socketIOPacket{kind: '3', ns: c.namespace, id: new(id), data: args, count: len(attachments)}
	if p.count > 0 {
		p.kind = '6'
	}
	if err := c.write(p, attachments); err != nil {
		return err
	}
	delete(c.owed, id)
	return nil
}

func (c *SocketIOConn) Read() (message SocketIOMessage, err error) {
	defer func() {
		if err != nil {
			c.Close()
		}
	}()
	var packet *socketIOPacket
	var attachments [][]byte
	size := 0
	for {
		typ, raw, e := c.ws.ReadMessage()
		if e != nil {
			return message, c.readError(e)
		}
		if typ == websocket.BinaryMessage {
			if packet == nil {
				return message, errors.New("unexpected Socket.IO binary attachment")
			}
			size += len(raw)
			if size > socketIOLimit {
				return message, errors.New("Socket.IO packet and attachments exceed 1 MiB")
			}
			attachments = append(attachments, raw)
			if len(attachments) < packet.count {
				continue
			}
		} else {
			if typ != websocket.TextMessage || !utf8.Valid(raw) || len(raw) == 0 {
				return message, errors.New("invalid Engine.IO packet")
			}
			switch raw[0] {
			case '2':
				if e := c.pong(raw[1:]); e != nil {
					return message, e
				}
				_ = c.ws.SetReadDeadline(time.Now().Add(c.heartbeat))
				continue
			case '1':
				return message, io.EOF
			case '4':
			default:
				return message, errors.New("unsupported Engine.IO packet")
			}
			if packet != nil {
				return message, errors.New("Socket.IO message interrupted pending binary attachments")
			}
			p, e := parseSocketIOPacket(raw[1:])
			if e != nil {
				return message, e
			}
			if p.ns != c.namespace {
				return message, errors.New("unexpected Socket.IO namespace")
			}
			if p.kind == '1' {
				return message, io.EOF
			}
			if p.kind != '2' && p.kind != '3' && p.kind != '5' && p.kind != '6' {
				return message, errors.New("unexpected Socket.IO packet after connection")
			}
			if e := socketIOPlaceholders(p.data, p.count); e != nil {
				return message, e
			}
			packet, size = &p, len(raw)
			if p.count > 0 {
				continue
			}
		}
		p := *packet
		message = SocketIOMessage{Args: p.data, Attachments: attachments, ACKID: p.id, IsACK: p.kind == '3' || p.kind == '6'}
		c.mu.Lock()
		if message.IsACK {
			known := c.pending[*p.id]
			delete(c.pending, *p.id)
			c.mu.Unlock()
			if !known {
				packet, attachments, size = nil, nil, 0
				continue
			}
		} else {
			if p.id != nil {
				if len(c.owed) >= 128 || c.owed[*p.id] {
					c.mu.Unlock()
					return message, errors.New("too many outstanding or duplicate Socket.IO ACK requests")
				}
				c.owed[*p.id] = true
			}
			c.mu.Unlock()
			var values []jsontext.Value
			_ = json.Unmarshal(p.data, &values)
			if values[0].Kind() == '"' {
				_ = json.Unmarshal(values[0], &message.Event)
			} else {
				message.Event = string(values[0])
			}
			message.Args, _ = json.Marshal(values[1:])
		}
		return message, nil
	}
}

// SocketIOAttachments decodes a JSON array of padded base64 strings. An empty
// string represents an empty binary attachment, distinct from no attachments.
func SocketIOAttachments(text string) ([][]byte, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	var encoded []jsontext.Value
	if len(text) > 2<<20 || json.Unmarshal([]byte(text), &encoded) != nil || jsontext.Value(text).Kind() != '[' || len(encoded) > socketIOMaxAttachments {
		return nil, errors.New("Socket.IO attachments require a JSON array of at most 32 padded base64 strings")
	}
	var result [][]byte
	size := 0
	for _, raw := range encoded {
		var value string
		if raw.Kind() != '"' || json.Unmarshal(raw, &value) != nil {
			return nil, errors.New("Socket.IO attachments must be base64 strings, not null or other JSON values")
		}
		b, err := base64.StdEncoding.Strict().DecodeString(value)
		if err != nil || base64.StdEncoding.EncodeToString(b) != value {
			return nil, fmt.Errorf("attachment %d requires canonical padded base64", len(result))
		}
		size += len(b)
		if size > socketIOLimit || len(result) >= socketIOMaxAttachments {
			return nil, errors.New("Socket.IO attachments exceed 32 items or 1 MiB")
		}
		result = append(result, b)
	}
	return result, nil
}
