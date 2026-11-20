package app

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"github.com/baba983/vscroxy/locales"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/lifecycle"
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
	lifetime       *lifecycle.DisposableStore
	notify         func()
	afterWrite     func(string) // test hook: after a successful write, before it is recorded
	state, problem string
	finished, busy bool
	events         []socketIOEvent
	sent           []request.SocketIOMessageDoc
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

// stop cancels the connection. The document's Cancel button keeps the raw
// function so it can end just the current send without closing the session.
func (s *socketIOSession) stop() {
	if s == nil {
		return
	}
	stopSessionLifetime(s.lifetime, s.cancel)
}

func (s *socketIOSession) Dispose() { s.stop() }

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
		text += locales.AttachmentBase64(i, len(b), base64.StdEncoding.EncodeToString(b))
	}
	return name, text
}

func openSocketIOSession(proxy string, ca []byte, certificates certs.Settings, jar http.CookieJar, input request.Request, scope request.Context, vars map[string]string, req *request.Prepared, cfg request.SocketIOConfig, notify func()) *socketIOSession {
	ctx, cancel := context.WithCancel(context.Background())
	s := &socketIOSession{state: "Connecting", cancel: cancel, done: make(chan struct{}), lifetime: newSessionLifetime(cancel), notify: notify, owed: map[uint64]bool{}, input: input, scope: scope, vars: maps.Clone(vars)}
	go guarded(func() {
		defer close(s.done)
		defer cancel()
		conn, err := request.DialSocketIO(ctx, proxy, req, cfg, ca, certificates, jar)
		if err == nil {
			defer conn.Close()
			s.mu.Lock()
			s.conn, s.state = conn, "Connected"
			s.append("System", "Connected", locales.SocketIOSessionNote(cfg.Namespace, conn.SID))
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
			s.state = locales.ServerDisconnected()
		default:
			s.state, s.problem = "Failed", err.Error()
		}
		s.append("System", s.state, s.problem)
		s.mu.Unlock()
		s.changed()
	})
	return s
}

// sentMessages are the saved-form messages this session emitted, in order.
func (s *socketIOSession) sentMessages() []request.SocketIOMessageDoc {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
}

func (s *socketIOSession) send(event, args string, attachments [][]byte, wantACK bool, ack *uint64, doc request.SocketIOMessageDoc) {
	s.mu.Lock()
	if s.finished || s.conn == nil || s.busy {
		s.mu.Unlock()
		return
	}
	s.busy, s.problem = true, ""
	conn := s.conn
	s.mu.Unlock()
	s.changed()
	go guarded(func() {
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
			if s.afterWrite != nil {
				s.afterWrite(event)
			}
		}
		s.mu.Lock()
		if err != nil {
			if !s.finished {
				s.problem = err.Error()
			}
		} else {
			// A write that succeeded is real traffic even if the reader has
			// since closed the session; keep it in the sent list and timeline.
			if ack != nil {
				delete(s.owed, *ack)
			} else {
				s.sent = append(s.sent, doc)
			}
			s.append("Out", name, text)
		}
		if !s.finished {
			s.busy = false
		}
		s.mu.Unlock()
		s.changed()
	})
}

// emitAll sends saved messages in order on the connection's original endpoint
// and scope, as one busy period. It runs on a worker; each emitted message
// joins the timeline and the session's sent list.
func (s *socketIOSession) emitAll(messages []request.SocketIOMessageDoc) error {
	s.mu.Lock()
	if s.finished || s.conn == nil || s.busy {
		s.mu.Unlock()
		return errors.New("connect before emitting")
	}
	s.busy, s.problem = true, ""
	conn := s.conn
	s.mu.Unlock()
	s.changed()
	var failure error
	for i, doc := range messages {
		r := s.input
		r.Body = cmp.Or(strings.TrimSpace(doc.Args), "[]")
		r.SocketIO.Event, r.SocketIO.Attachments, r.SocketIO.WantACK = doc.Event, doc.Attachments, doc.WantACK
		prepared, cfg, err := r.PrepareSocketIO(s.scope, s.vars)
		if err != nil {
			failure = fmt.Errorf("message %d: %w", i+1, err)
			break
		}
		attachments, err := request.SocketIOAttachments(cfg.Attachments)
		if err != nil {
			failure = fmt.Errorf("message %d: %w", i+1, err)
			break
		}
		message := request.SocketIOMessage{Event: cfg.Event, Args: jsontext.Value(prepared.Body), Attachments: attachments}
		ack, err := conn.Emit(cfg.Event, message.Args, attachments, cfg.WantACK)
		if err != nil {
			failure = fmt.Errorf("message %d: %w", i+1, err)
			break
		}
		message.ACKID = ack
		name, text := socketIOMessageText(message)
		if s.afterWrite != nil {
			s.afterWrite(cfg.Event)
		}
		s.mu.Lock()
		// Emit succeeded: record it even when the reader closed meanwhile.
		// The close entry may precede this Out, but the Out must not vanish.
		s.append("Out", name, text)
		s.sent = append(s.sent, doc)
		s.mu.Unlock()
		s.changed()
	}
	s.mu.Lock()
	if !s.finished {
		s.busy = false
		if failure != nil {
			s.problem = failure.Error()
		}
	}
	s.mu.Unlock()
	s.changed()
	return failure
}
