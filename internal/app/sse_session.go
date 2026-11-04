package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/request"
)

// Workers publish only to this bounded session, never to UI/editor state. A
// new connection owns a new session, so late updates cannot replace its data.
type sseSession struct {
	mu           sync.Mutex
	cancel       context.CancelFunc
	done         chan struct{}
	notify       func()
	state        string
	finished     bool
	stopping     bool
	events       []sseSessionEvent
	seq, evicted int
}

type sseSessionEvent struct {
	seq                 int
	at                  time.Time
	eventType, id, data string
	size                int
	detail              string
}

func openSSESession(proxy string, req *request.Prepared, ca []byte, jar http.CookieJar, reconnect bool, notify func()) *sseSession {
	ctx, cancel := context.WithCancel(context.Background())
	s := &sseSession{state: "Connecting", cancel: cancel, done: make(chan struct{}), notify: notify}
	go func() {
		defer close(s.done)
		defer cancel()
		err := request.ReceiveSSE(ctx, proxy, req, ca, jar, reconnect, func(u request.SSEUpdate) {
			s.mu.Lock()
			if s.stopping {
				s.mu.Unlock()
				return
			}
			e := sseSessionEvent{}
			if u.Event != nil {
				e.eventType, e.id, e.data, e.size = u.Event.Type, u.Event.ID, u.Event.Data, len(u.Event.Data)
			} else {
				s.state = u.State
				switch u.State {
				case "Connected":
					e.detail = "Connected · " + u.URL + "\n\n" + request.HeaderBlock(u.Header)
				case "Reconnecting":
					e.detail = fmt.Sprintf("Reconnecting in %s · Last-Event-ID: %s", u.Retry, u.LastEventID)
				case "Stopped":
					e.detail = "HTTP 204 · Server stopped the stream"
				}
			}
			s.append(e)
			s.mu.Unlock()
			s.changed()
		})
		s.mu.Lock()
		s.finished = true
		switch {
		case s.stopping || errors.Is(err, context.Canceled):
			s.state = "Stopped"
			s.append(sseSessionEvent{detail: "Stopped locally; no further reconnects"})
		case err != nil:
			s.state = "Failed"
			s.append(sseSessionEvent{detail: "Stream failed: " + err.Error()})
		case s.state != "Stopped":
			s.state = "Ended"
			s.append(sseSessionEvent{detail: "Stream ended · Reconnect is off"})
		}
		s.mu.Unlock()
		s.changed()
	}()
	return s
}

// append requires mu. Each field keeps a bounded UTF-8 preview, and a copy
// rather than a substring retaining the original backing storage.
func (s *sseSession) append(e sseSessionEvent) {
	s.seq++
	e.seq, e.at = s.seq, time.Now()
	e.eventType, e.id, e.data, e.detail = ssePreview(e.eventType), ssePreview(e.id), ssePreview(e.data), ssePreview(e.detail)
	if len(s.events) == 200 {
		s.events = slices.Delete(s.events, 0, 1)
		s.evicted++
	}
	s.events = append(s.events, e)
}

func ssePreview(text string) string {
	if len(text) <= 64<<10 {
		return text
	}
	preview := text[:64<<10]
	for !utf8.ValidString(preview) {
		preview = preview[:len(preview)-1]
	}
	return strings.Clone(preview) + fmt.Sprintf("\nRetained %d of %d bytes", len(preview), len(text))
}

func (s *sseSession) changed() {
	if s.notify != nil {
		s.notify()
	}
}

func (s *sseSession) stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.finished || s.stopping {
		s.mu.Unlock()
		return
	}
	s.stopping, s.state = true, "Stopping"
	s.mu.Unlock()
	s.cancel()
	s.changed()
}

func (s *sseSession) snapshot() (string, bool, []sseSessionEvent, int) {
	if s == nil {
		return "Not connected", true, nil, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.finished, slices.Clone(s.events), s.evicted
}
