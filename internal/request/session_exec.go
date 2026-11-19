package request

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/errorsx"
)

// DefaultSocketIOWait is the reply window after a Socket.IO emit, and after
// the last one, when the request leaves WaitMS unset.
const DefaultSocketIOWait = time.Second

// MaxSocketIOWait bounds a request's Socket.IO reply window.
const MaxSocketIOWait = 30 * time.Second

// Wait is the reply window after each Socket.IO emit.
func (c SocketIOConfig) Wait() time.Duration {
	wait := time.Duration(c.WaitMS) * time.Millisecond
	switch {
	case wait <= 0:
		return DefaultSocketIOWait
	case wait > MaxSocketIOWait:
		return MaxSocketIOWait
	}
	return wait
}

// sessionFlow is the synthetic response a WebSocket or Socket.IO send exposes
// to Checks and post-response scripts: the received messages as a JSON array,
// the count sent and received, and the transport's completion status. A
// session is not an HTTP exchange, so it has no HTTP status of its own.
func sessionFlow(req *Prepared, body []byte, sent, received int, millis int) capture.Flow {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("X-Vscroxy-Sent", strconv.Itoa(sent))
	header.Set("X-Vscroxy-Received", strconv.Itoa(received))
	return capture.Flow{
		Method: "GET", URL: req.URL, RequestHeader: req.Header.Clone(), RequestBody: req.Body,
		Status: http.StatusSwitchingProtocols, ResponseHeader: header,
		ResponseBody: string(body), ResponseBodySize: len(body),
		Millis: millis, Bytes: len(body),
	}
}

// repliesJSON encodes received payloads as a JSON array of strings, which is
// what Checks and pm.response.json() read. A non-UTF-8 binary frame becomes
// the replacement character; keep binary checks on lengths or headers.
func repliesJSON(replies []string) []byte {
	if replies == nil {
		replies = []string{}
	}
	data, _ := json.Marshal(replies)
	return data
}

// socketIOEvent is one received Socket.IO packet in the synthetic response:
// its event, arguments and acknowledgement number. Args stays raw JSON so a
// number or nested object is not re-encoded lossily.
type socketIOEvent struct {
	Event   string         `json:"event,omitempty"`
	Args    jsontext.Value `json:"args,omitempty"`
	Ack     *uint64        `json:"ack,omitempty"`
	Reply   bool           `json:"acknowledgement,omitempty"`
	AtMilli int64          `json:"atMillis,omitempty"`
}

// socketIOEventText is what the Runner's console and report show for a
// received packet.
func socketIOEventText(m SocketIOMessage) string {
	text := m.Event
	if m.IsACK {
		text = "ACK"
	}
	if m.ACKID != nil {
		text += " #" + strconv.FormatUint(*m.ACKID, 10)
	}
	if len(m.Args) > 0 {
		text += " " + string(m.Args)
	}
	return text
}

// executeSocketIO sends the document's saved emit list, or its composer event
// when the list is empty, and reports the replies as a synthetic response so
// Checks and post-response scripts run as they do for a unary gRPC call.
func (r Request) executeSocketIO(ctx context.Context, srv *capture.Server, in Context, effective map[string]string, run *scriptRun, out Execution) (result Execution) {
	defer func() { result.Tests, result.Vars = run.tests, run.vars }()
	if in.HTTPOnly {
		out.Err = errors.New("load runs support HTTP(S) requests only")
		return out
	}
	if srv == nil || !srv.Running() {
		out.Err = errors.New("Socket.IO requires a running capture proxy")
		return out
	}
	req, cfg, err := r.PrepareSocketIO(in, effective)
	if err != nil {
		out.Err = err
		return out
	}
	out.Request = req
	out.Stage, run.phase = "Socket.IO connection failed", "network"
	conn, err := DialSocketIO(ctx, srv.Addr(), req, cfg, srv.CACertificate(), in.Certificates, run.jar)
	if err != nil {
		out.Err = err
		return out
	}
	defer conn.Close()
	events := make(chan SocketIOMessage, 128)
	readErr := make(chan error, 1)
	stop := make(chan struct{})
	defer close(stop)
	go errorsx.Guard(func() {
		for {
			message, err := conn.Read()
			if err != nil {
				select {
				case readErr <- err:
				case <-stop:
				}
				return
			}
			select {
			case events <- message:
			case <-stop:
				return
			}
		}
	})
	var received []SocketIOMessage
	collect := func(until func(SocketIOMessage) bool, wait time.Duration) {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		for {
			select {
			case message := <-events:
				received = append(received, message)
				if until != nil && until(message) {
					return
				}
			case <-readErr:
				return
			case <-timer.C:
				return
			case <-ctx.Done():
				return
			}
		}
	}
	docs := r.SocketIO.Messages
	if len(docs) == 0 {
		docs = []SocketIOMessageDoc{{Event: r.SocketIO.Event, Args: cmp.Or(strings.TrimSpace(req.Body), "[]"), Attachments: r.SocketIO.Attachments, WantACK: r.SocketIO.WantACK}}
	}
	wait := cfg.Wait()
	sent := 0
	for i, doc := range docs {
		messageRequest := r
		messageRequest.Body = cmp.Or(strings.TrimSpace(doc.Args), "[]")
		messageRequest.SocketIO.Event, messageRequest.SocketIO.Attachments, messageRequest.SocketIO.WantACK = doc.Event, doc.Attachments, doc.WantACK
		prepared, messageConfig, err := messageRequest.PrepareSocketIO(in, effective)
		if err != nil {
			out.Err = fmt.Errorf("message %d: %w", i+1, err)
			return out
		}
		attachments, err := SocketIOAttachments(messageConfig.Attachments)
		if err != nil {
			out.Err = fmt.Errorf("message %d: %w", i+1, err)
			return out
		}
		ack, err := conn.Emit(messageConfig.Event, jsontext.Value(prepared.Body), attachments, messageConfig.WantACK)
		if err != nil {
			out.Err = fmt.Errorf("message %d: %w", i+1, err)
			return out
		}
		sent++
		if ack != nil {
			wanted := *ack
			collect(func(message SocketIOMessage) bool {
				return message.IsACK && message.ACKID != nil && *message.ACKID == wanted
			}, wait)
		}
	}
	// One more window keeps a broadcast the server sends after the last emit
	// without making every emit wait for the whole window.
	collect(nil, wait)
	if ctx.Err() != nil {
		out.Err = ctx.Err()
		return out
	}
	docsOut := make([]socketIOEvent, 0, len(received))
	var replies []string
	for _, message := range received {
		event := socketIOEvent{Event: message.Event, Args: message.Args, Ack: message.ACKID, Reply: message.IsACK}
		if len(event.Args) == 0 {
			event.Args = jsontext.Value("[]")
		}
		docsOut = append(docsOut, event)
		replies = append(replies, socketIOEventText(message))
	}
	body, err := json.Marshal(docsOut)
	if err != nil {
		out.Err = err
		return out
	}
	flow := sessionFlow(req, body, sent, len(received), 0)
	flow.ResponseBodySize = len(body)
	flow.Bytes = len(body)
	flow.ResponseBody = string(body)
	out.Response = &flow
	out.Replies = replies
	run.tests = append(run.tests, r.Checks.Evaluate(flow)...)
	run.target = func() string { return req.URL }
	res := &jsResponse{Status: flow.Status, Body: flow.ResponseBody, Header: flow.ResponseHeader, Millis: flow.Millis, Bytes: flow.Bytes}
	out.Stage = "Post-response script failed"
	if err := run.scripts(ctx, "post-response", in.Folders, func(f Folder) string { return f.Post }, r.Post, map[string]any{"res": res, "req": req}); err != nil {
		out.Err = err
	}
	return out
}
