package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	uuid "github.com/google/uuid"
)

// flow http request
type Request struct {
	Method string
	URL    *url.URL
	Proto  string
	Header http.Header
	// Trailer is the trailer block the request ended with, which a client
	// sends after a chunked or HTTP/2 body. The transport fills it while the
	// body is read, so whoever keeps it must copy it once the body ended.
	Trailer http.Header
	Body    []byte

	raw *http.Request
}

func newRequest(req *http.Request) *Request {
	return &Request{
		Method:  req.Method,
		URL:     req.URL,
		Proto:   req.Proto,
		Header:  req.Header,
		Trailer: req.Trailer,
		raw:     req,
	}
}

func (r *Request) Raw() *http.Request {
	return r.raw
}

// Trailers is the trailer block the request ended with. A transport that had
// no trailer block announced assigns a fresh one when the trailers arrive, so
// this reads the request the engine kept rather than the copy taken when the
// flow was made; it is complete only once the body has ended.
func (r *Request) Trailers() http.Header {
	if r == nil {
		return nil
	}
	if r.raw != nil && r.raw.Trailer != nil {
		return r.raw.Trailer
	}
	return r.Trailer
}

func (req *Request) MarshalJSON() ([]byte, error) {
	r := make(map[string]interface{})
	r["method"] = req.Method
	r["url"] = req.URL.String()
	r["proto"] = req.Proto
	r["header"] = req.Header
	return json.Marshal(r)
}

func (req *Request) UnmarshalJSON(data []byte) error {
	r := make(map[string]interface{})
	err := json.Unmarshal(data, &r)
	if err != nil {
		return err
	}

	rawurl, ok := r["url"].(string)
	if !ok {
		return errors.New("url parse error")
	}
	u, err := url.Parse(rawurl)
	if err != nil {
		return err
	}

	rawheader, ok := r["header"].(map[string]interface{})
	if !ok {
		return errors.New("rawheader parse error")
	}

	header := make(map[string][]string)
	for k, v := range rawheader {
		vals, ok := v.([]interface{})
		if !ok {
			return errors.New("header parse error")
		}

		svals := make([]string, 0)
		for _, val := range vals {
			sval, ok := val.(string)
			if !ok {
				return errors.New("header parse error")
			}
			svals = append(svals, sval)
		}
		header[k] = svals
	}

	*req = Request{
		Method: r["method"].(string),
		URL:    u,
		Proto:  r["proto"].(string),
		Header: header,
	}
	return nil
}

// flow http response
type Response struct {
	StatusCode int         `json:"statusCode"`
	Header     http.Header `json:"header"`
	// Trailer is the trailer block the response ended with, which a server
	// sends after a chunked or HTTP/2 body: a gRPC status and its binary
	// details live there, apart from the headers. The transport fills it
	// while the body is read, so whoever keeps it must copy it once the body
	// ended.
	Trailer http.Header `json:"trailer,omitempty"`
	// Proto is the HTTP version the server answered with, such as "HTTP/2.0".
	// It is empty for a response the engine made itself.
	Proto      string `json:"proto,omitempty"`
	Body       []byte `json:"-"`
	BodyReader io.Reader

	close bool // connection close

	// raw is the response the transport returned, which owns the trailer
	// block the transport fills while the body is read.
	raw *http.Response
}

// Trailers is the trailer block the response ended with. A transport that had
// no trailer block announced assigns a fresh one when the trailers arrive, so
// this reads the response the transport kept rather than the copy taken when
// the flow was made; it is complete only once the body has ended.
func (r *Response) Trailers() http.Header {
	if r == nil {
		return nil
	}
	if r.raw != nil && r.raw.Trailer != nil {
		return r.raw.Trailer
	}
	return r.Trailer
}

// flow
type Flow struct {
	Id          uuid.UUID
	ConnContext *ConnContext
	Request     *Request
	Response    *Response
	WebSocket   *WebSocketData
	SSE         *SSEData // Server-Sent Events data
	Error       error    // exchange failure, not individual failed dial attempts

	// https://docs.mitmproxy.org/stable/overview-features/#streaming
	// 如果为 true，则不缓冲 Request.Body 和 Response.Body，且不进入之后的 Addon.Request 和 Addon.Response
	Stream            bool
	UseSeparateClient bool // use separate http client to send http request
	StartTime         time.Time
	done              chan struct{}
}

func newFlow() *Flow {
	return &Flow{
		Id:        uuid.New(),
		StartTime: time.Now(),
		done:      make(chan struct{}),
	}
}

func (f *Flow) Done() <-chan struct{} {
	return f.done
}

func (f *Flow) finish() {
	close(f.done)
}

func (f *Flow) MarshalJSON() ([]byte, error) {
	j := make(map[string]interface{})
	j["id"] = f.Id
	j["request"] = f.Request
	j["response"] = f.Response
	return json.Marshal(j)
}

type WebSocketMessage struct {
	Type       int
	Content    []byte
	FromClient bool
	Timestamp  time.Time
}

func (m *WebSocketMessage) MarshalJSON() ([]byte, error) {
	typeAlias := struct {
		Type       int    `json:"type"`
		Content    string `json:"content"` // base64 encoded
		FromClient bool   `json:"fromClient"`
		Timestamp  string `json:"timestamp"`
	}{
		Type:       m.Type,
		Content:    string(m.Content), // []byte 会被编码为 base64
		FromClient: m.FromClient,
		Timestamp:  m.Timestamp.Format(time.RFC3339Nano),
	}
	return json.Marshal(typeAlias)
}

func newWebSocketMessage(msgType int, content []byte, fromClient bool) *WebSocketMessage {
	return &WebSocketMessage{
		Type:       msgType,
		Content:    content,
		FromClient: fromClient,
		Timestamp:  time.Now(),
	}
}

type WebSocketData struct {
	Messages []*WebSocketMessage

	mu sync.Mutex
}

func newWebSocketData() *WebSocketData {
	return &WebSocketData{
		Messages: make([]*WebSocketMessage, 0),
	}
}

func (wsData *WebSocketData) addMessage(msgType int, content []byte, fromClient bool) {
	msg := newWebSocketMessage(msgType, content, fromClient)
	wsData.mu.Lock()
	defer wsData.mu.Unlock()
	wsData.Messages = append(wsData.Messages, msg)
}

// SSEEvent represents a single Server-Sent Event
type SSEEvent struct {
	ID    string    `json:"id,omitempty"`    // event id
	Event string    `json:"event,omitempty"` // event type (default: "message")
	Data  string    `json:"data"`            // event data
	Retry int       `json:"retry,omitempty"` // retry interval in milliseconds
	Raw   string    `json:"raw"`             // raw event text
	Time  time.Time `json:"timestamp"`       // when this event was received
}

// SSEData holds all SSE events for a flow
type SSEData struct {
	Events []*SSEEvent

	mu sync.Mutex
}

func newSSEData() *SSEData {
	return &SSEData{
		Events: make([]*SSEEvent, 0),
	}
}

func (sseData *SSEData) addEvent(event *SSEEvent) {
	sseData.mu.Lock()
	defer sseData.mu.Unlock()
	sseData.Events = append(sseData.Events, event)
}
