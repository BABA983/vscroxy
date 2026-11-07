package app

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/gorilla/websocket"
)

type socketIOEvent struct {
	seq                   int
	at                    time.Time
	direction, name, text string
	size                  int
}

// The session is the only state workers mutate. Documents/editors stay on the
// UI thread, and a reconnect always creates an entirely new session.
type socketIOSession struct {
	mu             sync.Mutex
	conn           *request.SocketIOConn
	cancel         context.CancelFunc
	done           chan struct{}
	notify         func()
	state, problem string
	finished, busy bool
	events         []socketIOEvent
	seq, evicted   int
	owed           map[uint64]bool
	input          request.Request
	scope          request.Context
	vars           map[string]string
}

type socketIOSnapshot struct {
	state, problem        string
	finished, ready, busy bool
	events                []socketIOEvent
	evicted               int
	owed                  []string
}

func (s *socketIOSession) snapshot() socketIOSnapshot {
	if s == nil {
		return socketIOSnapshot{state: "Not connected", finished: true}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := socketIOSnapshot{state: s.state, problem: s.problem, finished: s.finished, ready: s.conn != nil && !s.finished, busy: s.busy, events: slices.Clone(s.events), evicted: s.evicted}
	for _, id := range slices.Sorted(maps.Keys(s.owed)) {
		v.owed = append(v.owed, strconv.FormatUint(id, 10))
	}
	return v
}

func (s *socketIOSession) changed() {
	if s.notify != nil {
		s.notify()
	}
}

func (s *socketIOSession) append(direction, name, text string) {
	s.seq++
	if len(s.events) == 200 {
		s.events = slices.Delete(s.events, 0, 1)
		s.evicted++
	}
	s.events = append(s.events, socketIOEvent{seq: s.seq, at: time.Now(), direction: direction, name: ssePreview(name), text: ssePreview(text), size: len(text)})
}

func socketIOMessageText(m request.SocketIOMessage) (string, string) {
	name := m.Event
	if m.IsACK {
		name = "ACK"
	}
	if m.ACKID != nil {
		name += " · #" + strconv.FormatUint(*m.ACKID, 10)
	}
	text, _ := indentJSON(string(m.Args))
	if text == "" {
		text = string(m.Args)
	}
	for i, b := range m.Attachments {
		text += fmt.Sprintf("\n\nAttachment %d · %d B · base64\n%s", i, len(b), base64.StdEncoding.EncodeToString(b))
	}
	return name, text
}

func openSocketIOSession(proxy string, ca []byte, certificates certs.Settings, jar http.CookieJar, input request.Request, scope request.Context, vars map[string]string, req *request.Prepared, cfg request.SocketIOConfig, notify func()) *socketIOSession {
	ctx, cancel := context.WithCancel(context.Background())
	s := &socketIOSession{state: "Connecting", cancel: cancel, done: make(chan struct{}), notify: notify, owed: map[uint64]bool{}, input: input, scope: scope, vars: maps.Clone(vars)}
	go func() {
		defer close(s.done)
		defer cancel()
		conn, err := request.DialSocketIO(ctx, proxy, req, cfg, ca, certificates, jar)
		if err == nil {
			defer conn.Close()
			s.mu.Lock()
			s.conn, s.state = conn, "Connected"
			s.append("System", "Connected", "Namespace "+cfg.Namespace+" · Socket ID "+conn.SID+"\nEngine.IO 4 · WebSocket only · No reconnect or replay")
			s.mu.Unlock()
			s.changed()
			for {
				var m request.SocketIOMessage
				m, err = conn.Read()
				if err != nil {
					break
				}
				name, text := socketIOMessageText(m)
				s.mu.Lock()
				if m.ACKID != nil && !m.IsACK {
					s.owed[*m.ACKID] = true
				}
				s.append("In", name, text)
				s.mu.Unlock()
				s.changed()
			}
		}
		s.mu.Lock()
		s.finished, s.busy = true, false
		clear(s.owed)
		switch {
		case ctx.Err() != nil:
			s.state = "Disconnected"
		case errors.Is(err, io.EOF), websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway):
			s.state = "Server disconnected"
		default:
			s.state, s.problem = "Failed", err.Error()
		}
		s.append("System", s.state, s.problem)
		s.mu.Unlock()
		s.changed()
	}()
	return s
}

func (s *socketIOSession) send(event, args string, attachments [][]byte, wantACK bool, ack *uint64) {
	s.mu.Lock()
	if s.finished || s.conn == nil || s.busy {
		s.mu.Unlock()
		return
	}
	s.busy, s.problem = true, ""
	conn := s.conn
	s.mu.Unlock()
	s.changed()
	go func() {
		m := request.SocketIOMessage{Event: event, Args: jsontext.Value(args), Attachments: attachments, IsACK: ack != nil, ACKID: ack}
		var err error
		if ack != nil {
			err = conn.Acknowledge(*ack, m.Args, attachments)
		} else {
			m.ACKID, err = conn.Emit(event, m.Args, attachments, wantACK)
		}
		var name, text string
		if err == nil {
			name, text = socketIOMessageText(m)
		}
		s.mu.Lock()
		if !s.finished {
			s.busy = false
			if err != nil {
				s.problem = err.Error()
			} else {
				if ack != nil {
					delete(s.owed, *ack)
				}
				s.append("Out", name, text)
			}
		}
		s.mu.Unlock()
		s.changed()
	}()
}
