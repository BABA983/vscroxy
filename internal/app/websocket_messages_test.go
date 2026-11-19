package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
	"github.com/gorilla/websocket"
)

func TestWebSocketTranscriptShowsSentAndHeard(t *testing.T) {
	got := webSocketTranscript([]request.WebSocketExchange{
		{Message: request.WebSocketMessage{Payload: "login"}, Replies: []string{"ok", "ready"}},
		{Message: request.WebSocketMessage{Encoding: "Base64", Payload: "AAE="}},
		{Message: request.WebSocketMessage{Encoding: "Hex", Payload: "ff"}, Replies: []string{"done"}},
	})
	want := "→ login\n← ok\n← ready\n\n→ [Base64] AAE=\n\n→ [Hex] ff\n← done"
	if got != want {
		t.Fatalf("transcript:\n%q\nwant\n%q", got, want)
	}
}

func TestWebSocketBodyTabEditsTheSavedMessageList(t *testing.T) {
	a := workspaceApp()
	a.openCompose()
	d := a.requestDocument
	d.composeTab, d.composeURL = composeTabBody, "wss://socket.example.com/events"
	d.wsMessages = []request.WebSocketMessage{{Payload: "hello"}}
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	for _, want := range []string{"Add message", "Reply window (ms)", "WebSocket message 1"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	// The raw body editor belongs to HTTP requests; a ws request edits messages.
	if tt.HasText("Request body") {
		t.Fatalf("the ws Body tab still shows the raw editor: %v", tt.Texts())
	}
	if err := tt.Click("Add message"); err != nil {
		t.Fatal(err)
	}
	if len(d.wsMessages) != 2 {
		t.Fatalf("messages: %+v", d.wsMessages)
	}
	if err := tt.Click("WebSocket message 2"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("second")
	if d.wsMessages[1].Payload != "second" {
		t.Fatalf("message 2: %q", d.wsMessages[1].Payload)
	}
	if err := tt.Click("Remove message 1"); err != nil {
		t.Fatal(err)
	}
	if len(d.wsMessages) != 1 || d.wsMessages[0].Payload != "second" {
		t.Fatalf("after remove: %+v", d.wsMessages)
	}
	if got := d.value().WebSocket.Messages; len(got) != 1 || got[0].Payload != "second" {
		t.Fatalf("saved config: %+v", got)
	}
}

func TestWebSocketBodyTabIsOnlyForWebSocketRequests(t *testing.T) {
	a := workspaceApp()
	a.openCompose()
	d := a.requestDocument
	d.composeTab, d.composeURL = composeTabBody, "https://api.example.com/projects"
	d.wsMessages = []request.WebSocketMessage{{Payload: "not sent"}}
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if tt.HasText("Add message") {
		t.Fatalf("an HTTP request showed the WebSocket message list: %v", tt.Texts())
	}
	if got := d.value().WebSocket; len(got.Messages) != 0 || got.WaitMS != 0 {
		t.Fatalf("an HTTP request saved a WebSocket config: %+v", got)
	}
}

func TestWebSocketRequestLoadsItsSavedMessages(t *testing.T) {
	a := workspaceApp()
	d := a.newDocument(savedRequest{
		Method: "GET", URL: "wss://socket.example.com/events",
		WebSocket: request.WebSocketConfig{Messages: []request.WebSocketMessage{{Payload: "saved"}}, WaitMS: 1500},
	}, "")
	if !slices.Equal(d.wsMessages, []request.WebSocketMessage{{Payload: "saved"}}) || d.wsWait != 1500 {
		t.Fatalf("loaded: messages %+v wait %v", d.wsMessages, d.wsWait)
	}
	// A request saved with only its body keeps that body as its one message.
	legacy := a.newDocument(savedRequest{Method: "GET", URL: "ws://socket.example.com/events", Body: "old message"}, "")
	if !slices.Equal(legacy.wsMessages, []request.WebSocketMessage{{Payload: "old message"}}) {
		t.Fatalf("legacy body: %+v", legacy.wsMessages)
	}
}

func TestWebSocketSessionSavesSentMessagesToTheDraft(t *testing.T) {
	a := workspaceApp()
	a.openCompose()
	d := a.requestDocument
	d.composeURL = "wss://socket.example.com/events"
	d.wsMessages = []request.WebSocketMessage{{Payload: "old"}}
	a.socket = websocketWorkbench{open: true, url: d.composeURL, session: &wsSession{state: "Disconnected"}}
	a.openSocket()
	if a.socket.document != d || !slices.Equal(a.socket.saved, d.wsMessages) {
		t.Fatalf("open did not seed the session: document %v saved %+v", a.socket.document != nil, a.socket.saved)
	}
	a.socket.session.mu.Lock()
	a.socket.session.sent = []wsSessionSent{
		{Kind: websocket.TextMessage, Data: []byte("login")},
		{Kind: websocket.BinaryMessage, Data: []byte{0, 255}},
	}
	a.socket.session.mu.Unlock()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if err := tt.Click("Save sent"); err != nil {
		t.Fatal(err)
	}
	want := []request.WebSocketMessage{{Payload: "login"}, {Encoding: "Base64", Payload: "AP8="}}
	if !slices.Equal(d.wsMessages, want) || !slices.Equal(a.socket.saved, want) {
		t.Fatalf("saved: draft %+v session %+v", d.wsMessages, a.socket.saved)
	}
	if !strings.Contains(a.message, "Saved 2 messages") {
		t.Fatalf("message: %q", a.message)
	}
}

func TestSavedWebSocketMessagesRoundTripBinary(t *testing.T) {
	saved := savedWebSocketMessages([]wsSessionSent{
		{Kind: websocket.TextMessage, Data: []byte("text")},
		{Kind: websocket.BinaryMessage, Data: []byte{0, 1, 255}},
	})
	want := []request.WebSocketMessage{{Payload: "text"}, {Encoding: "Base64", Payload: "AAH/"}}
	if !slices.Equal(saved, want) {
		t.Fatalf("saved: %+v", saved)
	}
	for i, message := range saved {
		kind, data, err := message.Decode()
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if i == 0 && (kind != websocket.TextMessage || string(data) != "text") {
			t.Fatalf("message %d decoded to %d %q", i, kind, data)
		}
		if i == 1 && (kind != websocket.BinaryMessage || !slices.Equal(data, []byte{0, 1, 255})) {
			t.Fatalf("message %d decoded to %d %v", i, kind, data)
		}
	}
}

func TestWebSocketSessionSendAllUsesTheSavedOrder(t *testing.T) {
	a := workspaceApp()
	a.srv = requestProxy(t)
	upstream := websocketEchoServer(t)
	a.socket = websocketWorkbench{open: true, url: "ws" + strings.TrimPrefix(upstream.URL, "http"), session: &wsSession{state: "Disconnected"}}
	a.openSocket()
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("Connect"); err != nil {
		t.Fatal(err)
	}
	ws := a.socket.session
	waitSignal(t, ws.changedSignal, ws.done,
		func() bool { state, _, _ := ws.snapshot(); return state == "Connected" },
		func() string { return fmt.Sprintf("did not connect: %v", tt.Texts()) })
	a.socket.saved = []request.WebSocketMessage{{Payload: "one"}, {Payload: "two"}}
	a.socket.wait = 10 * time.Millisecond
	tt.Frame()
	if err := tt.Click("Send saved"); err != nil {
		t.Fatal(err)
	}
	sent := func() []string {
		_, events, _ := ws.snapshot()
		var out []string
		for _, event := range events {
			if event.Direction == "Out" {
				out = append(out, string(event.Data))
			}
		}
		return out
	}
	waitSignal(t, ws.changedSignal, ws.done,
		func() bool { return slices.Equal(sent(), []string{"one", "two"}) },
		func() string { return fmt.Sprintf("sent %q, want both saved messages", sent()) })
	a.socket.session.disconnect()
}

func websocketEchoServer(t *testing.T) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			conn.WriteMessage(kind, data)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
