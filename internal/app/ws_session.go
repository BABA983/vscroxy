package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/gorilla/websocket"
)

// A session is explicitly opened, never restored or automatically reconnected.
// It owns one reader and serializes writes; UI frames only read snapshots.
type wsSession struct {
	mu                sync.Mutex
	write             sync.Mutex
	conn              *websocket.Conn
	cancel            context.CancelFunc
	done              chan struct{}
	notify            func()
	state             string
	events            []wsSessionEvent
	sequence, evicted int
	localClose        bool
}

type wsSessionEvent struct {
	Sequence  int
	At        time.Time
	Direction string
	Kind      int
	Data      []byte
	Size      int
	Detail    string
	PeerClose bool
	CloseCode int
}

func openWSSession(proxy string, req *request.Prepared, ca []byte, jar http.CookieJar, notify func()) *wsSession {
	ctx, cancel := context.WithCancel(context.Background())
	s := &wsSession{state: "Connecting", cancel: cancel, done: make(chan struct{}), notify: notify}
	go func() {
		defer close(s.done)
		c, err := request.DialWebSocket(ctx, proxy, req, ca, jar)
		if err != nil {
			s.finish(err)
			return
		}
		defer c.Close()
		stop := context.AfterFunc(ctx, func() { c.Close() })
		defer stop()
		c.SetReadLimit(1 << 20)
		s.mu.Lock()
		s.conn = c
		if !s.localClose {
			s.state = "Connected"
		}
		s.mu.Unlock()
		s.add(wsSessionEvent{Detail: "Handshake accepted · " + req.URL})
		for {
			kind, data, err := c.ReadMessage()
			if err != nil {
				s.finish(err)
				return
			}
			s.add(wsSessionEvent{Direction: "In", Kind: kind, Data: data, Size: len(data)})
		}
	}()
	return s
}

func (s *wsSession) add(event wsSessionEvent) {
	s.mu.Lock()
	s.sequence++
	event.Sequence = s.sequence
	event.At = time.Now()
	event.Data = slices.Clone(event.Data[:min(len(event.Data), 64<<10)])
	if len(s.events) == 200 {
		s.events = slices.Delete(s.events, 0, 1)
		s.evicted++
	}
	s.events = append(s.events, event)
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}
}

func (s *wsSession) finish(err error) {
	s.mu.Lock()
	s.conn = nil
	connecting := s.state == "Connecting"
	s.state = "Disconnected"
	local := s.localClose
	s.mu.Unlock()
	e := wsSessionEvent{Detail: err.Error()}
	if closed, ok := errors.AsType[*websocket.CloseError](err); ok && closed.Code != websocket.CloseAbnormalClosure {
		e.PeerClose = true
		if closed.Code == websocket.CloseNoStatusReceived {
			e.Detail = "Peer close frame without a status code"
		} else {
			e.CloseCode = closed.Code
			e.Detail = fmt.Sprintf("Peer close %d: %s", closed.Code, closed.Text)
		}
	} else if local {
		e.Detail = "Local disconnect requested; no peer close frame recorded"
	} else if connecting {
		e.Detail = "Handshake failed: " + err.Error()
	} else {
		e.Detail = "Connection ended without a peer close frame: " + err.Error()
	}
	s.add(e)
}

func (s *wsSession) send(kind int, data []byte) error {
	if len(data) > 1<<20 {
		return fmt.Errorf("messages are limited to 1 MiB")
	}
	s.write.Lock()
	defer s.write.Unlock()
	s.mu.Lock()
	c, ready := s.conn, s.state == "Connected"
	s.mu.Unlock()
	if c == nil || !ready {
		return fmt.Errorf("connect before sending")
	}
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := c.WriteMessage(kind, data); err != nil {
		return err
	}
	s.add(wsSessionEvent{Direction: "Out", Kind: kind, Data: data, Size: len(data)})
	return nil
}

func (s *wsSession) disconnect() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.localClose || s.state == "Disconnected" {
		s.mu.Unlock()
		return
	}
	s.localClose = true
	s.state = "Disconnecting"
	c := s.conn
	s.mu.Unlock()
	go func() {
		if c != nil {
			_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		}
		s.cancel()
	}()
}

func (s *wsSession) snapshot() (string, []wsSessionEvent, int) {
	if s == nil {
		return "Not connected", nil, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, slices.Clone(s.events), s.evicted
}
