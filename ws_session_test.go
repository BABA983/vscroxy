package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/gorilla/websocket"
)

func TestWebSocketSessionKeepsConnectionAndRecordsPeerClose(t *testing.T) {
	upgrader := websocket.Upgrader{}
	burst := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer session-token" {
			http.Error(w, "auth", 401)
			return
		}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for range 2 {
			kind, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(kind, append([]byte("reply:"), data...)); err != nil {
				return
			}
		}
		<-burst
		for i := range 205 {
			data := []byte(fmt.Sprint(i))
			if i == 204 {
				data = bytes.Repeat([]byte{0xff, 0, 0x42}, 30000)
			}
			if err := c.WriteMessage(websocket.BinaryMessage, data); err != nil {
				return
			}
		}
		_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1008, "policy evidence"))
	}))
	defer upstream.Close()
	proxy := requestProxy(t)
	changed := make(chan struct{}, 1)
	s := openWSSession(proxy.Addr(), &jsRequest{URL: "ws" + strings.TrimPrefix(upstream.URL, "http"), header: http.Header{"Authorization": {"Bearer session-token"}}}, proxy.CACertificate(), nil, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	t.Cleanup(s.disconnect)
	guard := time.NewTimer(time.Minute)
	defer guard.Stop()
	for {
		state, _, _ := s.snapshot()
		if state == "Connected" {
			break
		}
		select {
		case <-changed:
		case <-guard.C:
			t.Fatal("no handshake")
		}
	}
	if err := s.send(websocket.TextMessage, []byte("first")); err != nil {
		t.Fatal(err)
	}
	for {
		_, events, _ := s.snapshot()
		if slices.ContainsFunc(events, func(e wsSessionEvent) bool { return string(e.Data) == "reply:first" }) {
			break
		}
		select {
		case <-changed:
		case <-guard.C:
			t.Fatal("no first reply")
		}
	}
	if err := s.send(websocket.BinaryMessage, []byte{0xff, 0, 0x73}); err != nil {
		t.Fatal(err)
	}
	close(burst)
	select {
	case <-s.done:
	case <-guard.C:
		t.Fatal("no peer close")
	}
	state, events, evicted := s.snapshot()
	if state != "Disconnected" || len(events) != 200 || evicted != 11 {
		t.Fatalf("state=%s retained=%d evicted=%d", state, len(events), evicted)
	}
	last := events[len(events)-1]
	if !last.PeerClose || last.CloseCode != 1008 || !strings.Contains(last.Detail, "policy evidence") {
		t.Fatalf("close evidence: %+v", last)
	}
	payload := events[len(events)-2]
	if payload.Size != 90000 || len(payload.Data) != 64<<10 || !bytes.Equal(payload.Data[:3], []byte{0xff, 0, 0x42}) {
		t.Fatalf("binary retained/original bytes: size=%d retained=%d", payload.Size, len(payload.Data))
	}
	if err := s.send(websocket.TextMessage, []byte("must not reconnect")); err == nil {
		t.Fatal("sent after close")
	}
	a := workspaceApp()
	a.socket = websocketWorkbench{open: true, session: s}
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click(fmt.Sprintf("Session message %d", payload.Sequence)); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Copy shown payload"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tt.Clipboard(), "ff 00 42") || !strings.Contains(tt.Clipboard(), "Retained 65536 of 90000 bytes") {
		t.Fatal("binary payload or truncation warning was not inspectable/copyable")
	}
}

func TestWebSocketSessionDisconnectCancelsConnectingHandshake(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release }))
	defer upstream.Close()
	defer close(release)
	proxy := requestProxy(t)
	s := openWSSession(proxy.Addr(), &jsRequest{URL: "ws" + strings.TrimPrefix(upstream.URL, "http")}, proxy.CACertificate(), nil, nil)
	guard := time.NewTimer(time.Minute)
	defer guard.Stop()
	select {
	case <-entered:
	case <-guard.C:
		t.Fatal("no handshake attempt")
	}
	a := workspaceApp()
	a.socket = websocketWorkbench{open: true, session: s}
	tt := ui.NewTester(a.view, 960, 600)
	tt.Key(0, ui.KeyEscape)
	select {
	case <-s.done:
	case <-guard.C:
		t.Fatal("disconnect did not cancel dial")
	}
	state, events, _ := s.snapshot()
	if state != "Disconnected" || len(events) != 1 || events[0].PeerClose || !strings.Contains(events[0].Detail, "Local disconnect requested") {
		t.Fatalf("invented peer close: %s %+v", state, events)
	}
}

func TestWebSocketSessionDistinguishesEmptyCloseFrameFromDroppedTCP(t *testing.T) {
	for _, closeFrame := range []bool{true, false} {
		t.Run(fmt.Sprint(closeFrame), func(t *testing.T) {
			upgrader := websocket.Upgrader{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer c.Close()
				if closeFrame {
					_ = c.WriteMessage(websocket.CloseMessage, nil)
				}
			}))
			defer upstream.Close()
			proxy := requestProxy(t)
			s := openWSSession(proxy.Addr(), &jsRequest{URL: "ws" + strings.TrimPrefix(upstream.URL, "http")}, proxy.CACertificate(), nil, nil)
			guard := time.NewTimer(time.Minute)
			defer guard.Stop()
			select {
			case <-s.done:
			case <-guard.C:
				t.Fatal("connection did not end")
			}
			_, events, _ := s.snapshot()
			last := events[len(events)-1]
			if last.PeerClose != closeFrame || last.CloseCode != 0 {
				t.Fatalf("fabricated close code/evidence: %+v", last)
			}
		})
	}
}
