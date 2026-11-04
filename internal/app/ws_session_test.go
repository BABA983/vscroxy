package app

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
	"github.com/gorilla/websocket"
)

func TestWebSocketMessageProtobufViews(t *testing.T) {
	_, schema, _ := protoFixture(t)
	data := []byte("\x0a\x05alpha\x10\x03")
	file := filepath.Join(t.TempDir(), "message.bin")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, surface := range []string{"session", "capture"} {
		for _, tc := range []struct {
			name, data, file, want string
			size                   int
		}{
			{"complete", string(data), "", `title: "alpha" delta: -2`, len(data)},
			{"empty", "", "", "(empty message)", 0},
			{"truncated-valid-prefix", string(data[:7]), "", "Incomplete message: 7 of 9 bytes retained", len(data)},
			{"malformed", "\x0a\x05a", "", "cannot parse invalid wire-format data", 3},
			{"spill", "\x0a", file, `title: "alpha" delta: -2`, len(data)},
			{"missing-spill", string(data), file + ".missing", "no such file", len(data)},
		} {
			if surface == "session" && tc.file != "" {
				continue
			}
			t.Run(surface+"/"+tc.name, func(t *testing.T) {
				a := workspaceApp()
				a.protobuf.schema = schema
				m := messageView{seq: 1, kind: "binary", direction: "in", data: tc.data, file: tc.file, size: tc.size, kept: tc.size}
				var view func(*ui.Context)
				if surface == "session" {
					a.socket = websocketWorkbench{open: true, session: &wsSession{state: "Disconnected", events: []wsSessionEvent{{Sequence: 1, Direction: "In", Kind: websocket.BinaryMessage, Data: []byte(tc.data), Size: tc.size}}}}
					view = a.view
				} else {
					a.flows = []flow{{id: 10, method: "GET", kind: "ws", status: 101, host: "socket.example.com", path: "/events"}}
					a.frames = map[int][]frame{10: {m}}
					a.applyFilter()
					view = a.view
				}
				t.Cleanup(a.protobuf.stop)
				t.Cleanup(a.socket.protobuf.stop)
				tt := ui.NewTester(view, 1280, 800)
				click := func(label string) {
					t.Helper()
					if err := tt.Click(label); err != nil {
						t.Fatal(err)
					}
				}
				if surface == "session" {
					click("Session message 1")
				}
				click("Protobuf")
				click("Protobuf message type")
				tt.Key(ui.Ctrl, ui.KeyA)
				tt.Type("api.Output")
				tt.Key(0, ui.KeyEnter)
				deadline := time.Now().Add(10 * time.Second)
				for {
					text := strings.Join(strings.Fields(strings.Join(tt.Texts(), " ")), " ")
					if strings.Contains(text, tc.want) {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("missing %q: %s", tc.want, text)
					}
					runtime.Gosched()
					tt.Frame()
				}
				if tc.name == "complete" || tc.name == "spill" || tc.name == "empty" {
					click("Copy decoded")
					if got := strings.Join(strings.Fields(tt.Clipboard()), " "); got != tc.want {
						t.Fatalf("copied %q, want %q", got, tc.want)
					}
				} else if _, ok := tt.Find("Copy decoded"); ok {
					t.Fatal("incomplete or invalid message offered a decoded result")
				}
			})
		}
	}
}

func TestWebSocketSessionUIBinaryEncodingsAndSubprotocol(t *testing.T) {
	upgrader := websocket.Upgrader{Subprotocols: []string{"bytes.v2"}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			kind, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(kind, data); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.socket = websocketWorkbench{open: true, url: "ws" + strings.TrimPrefix(upstream.URL, "http"), subprotocols: "bytes.v1, bytes.v2"}
	t.Cleanup(func() { a.socket.session.disconnect() })
	tt := ui.NewTester(a.view, 1100, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !ready() {
			if time.Now().After(deadline) {
				t.Fatalf("WebSocket did not settle: %v", tt.Texts())
			}
			runtime.Gosched()
			tt.Frame()
		}
		tt.Frame()
	}
	click("Connect")
	wait(func() bool { state, _, _ := a.socket.session.snapshot(); return state == "Connected" })
	for _, tc := range []struct {
		encoding, text string
		kind           int
		want           []byte
	}{
		{"Hex", "ff 00 73 c3 28", websocket.BinaryMessage, []byte{255, 0, 115, 195, 40}},
		{"Hex", "", websocket.BinaryMessage, []byte{}},
		{"Base64", "AAH/AHM=", websocket.BinaryMessage, []byte{0, 1, 255, 0, 115}},
		{"Text", "hello 界", websocket.TextMessage, []byte("hello 界")},
	} {
		click("WebSocket message encoding")
		// Select within this popup: the capture search mode also displays
		// "Text" behind the dialog, so a global text lookup is ambiguous.
		tt.Key(0, ui.KeyHome)
		switch tc.encoding {
		case "Hex":
			tt.Key(0, ui.KeyDown)
		case "Base64":
			tt.Key(0, ui.KeyEnd)
		}
		tt.Key(0, ui.KeyEnter)
		click("WebSocket message")
		tt.Key(ui.Ctrl, ui.KeyA)
		tt.Type(tc.text)
		label := "Send binary"
		if tc.kind == websocket.TextMessage {
			label = "Send text"
		}
		click(label)
		wait(func() bool {
			_, events, _ := a.socket.session.snapshot()
			return !a.socket.sending && slices.ContainsFunc(events, func(e wsSessionEvent) bool {
				return e.Direction == "In" && e.Kind == tc.kind && bytes.Equal(e.Data, tc.want)
			})
		})
	}
	_, events, _ := a.socket.session.snapshot()
	if !strings.Contains(events[0].Detail, "subprotocol: bytes.v2") {
		t.Fatalf("negotiation not inspectable: %s", events[0].Detail)
	}
	before := len(events)
	click("WebSocket message encoding")
	click("Hex")
	click("WebSocket message")
	tt.Key(ui.Ctrl, ui.KeyA)
	tt.Type("0xz")
	click("Send binary")
	_, events, _ = a.socket.session.snapshot()
	if !tt.HasText("Invalid Hex message; nothing sent") || len(events) != before {
		t.Fatal("malformed hex was sent or no validation shown")
	}
}

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
	s := openWSSession(proxy.Addr(), &request.Prepared{URL: "ws" + strings.TrimPrefix(upstream.URL, "http"), Header: http.Header{"Authorization": {"Bearer session-token"}}}, proxy.CACertificate(), nil, func() {
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
	s := openWSSession(proxy.Addr(), &request.Prepared{URL: "ws" + strings.TrimPrefix(upstream.URL, "http")}, proxy.CACertificate(), nil, nil)
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
			s := openWSSession(proxy.Addr(), &request.Prepared{URL: "ws" + strings.TrimPrefix(upstream.URL, "http")}, proxy.CACertificate(), nil, nil)
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
