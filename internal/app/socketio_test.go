package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
	"github.com/gorilla/websocket"
)

func waitSocketIO(t *testing.T, tt *ui.Tester, ready func() bool) {
	t.Helper()
	guard, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for !ready() {
		if guard.Err() != nil {
			t.Fatalf("Socket.IO did not settle: %v", tt.Texts())
		}
		runtime.Gosched()
		tt.Frame()
	}
	tt.Frame()
}

func TestSocketIODocumentEmitACKScopeAndPersistence(t *testing.T) {
	observed := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := new(websocket.Upgrader).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer ws.Close()
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		read := func(want string) bool {
			_, b, err := ws.ReadMessage()
			if err != nil || string(b) != want {
				observed <- fmt.Errorf("got %s %v, want %s", b, err, want)
				return false
			}
			return true
		}
		if r.Header.Get("Authorization") != "Bearer local-token" {
			observed <- fmt.Errorf("wrong HTTP auth")
			return
		}
		ws.WriteMessage(1, []byte(`0{"sid":"engine","pingInterval":5000,"pingTimeout":5000}`))
		if !read(`40/admin,{"token":"local-token"}`) {
			return
		}
		ws.WriteMessage(1, []byte(`40/admin,{"sid":"socket"}`))
		ws.WriteMessage(1, []byte(`42/admin,7["question","answer me"]`))
		if !read(`42/admin,0["echo",{"scope":"local-value"}]`) {
			return
		}
		ws.WriteMessage(1, []byte(`43/admin,0[{"scope":"local-value"}]`))
		if !read(`43/admin,7[{"scope":"local-value"}]`) {
			return
		}
		ws.WriteMessage(1, []byte(`42/admin,["answer-observed",73]`))
		_, _, _ = ws.ReadMessage() // client cancellation closes this read
		observed <- nil
	}))
	t.Cleanup(server.Close)
	a := libraryApp(t)
	a.srv = requestProxy(t)
	a.envs, a.activeEnv = []environment{{Name: "Local", Current: map[string]string{"token": "local-token", "value": "local-value"}}, {Name: "Other", Current: map[string]string{"token": "wrong-token", "value": "wrong-value"}}}, "Local"
	a.addDocument(savedRequest{Protocol: "Socket.IO", Name: "Echo", URL: server.URL, Body: `[{"scope":"{{value}}"}]`, Auth: request.Auth{Type: "Bearer", Token: "{{token}}"}, SocketIO: request.SocketIOConfig{Namespace: "/admin", Auth: `{"token":"{{token}}"}`, Event: "echo", WantACK: true}}, "")
	d := a.requestDocument
	t.Cleanup(func() { d.socketIOState().stop() })
	tt := ui.NewTester(a.view, 1100, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	if !tt.HasText("No Socket.IO connection yet") {
		t.Fatal(tt.Texts())
	}
	click("Connect")
	waitSocketIO(t, tt, func() bool { return len(d.socketIOUI.session.snapshot().owed) == 1 })
	s := d.socketIOUI.session
	a.activeEnv = "Other"
	click("Emit event")
	waitSocketIO(t, tt, func() bool { v := s.snapshot(); return !v.busy && len(v.events) >= 4 })
	click("Reply ACK")
	waitSocketIO(t, tt, func() bool { v := s.snapshot(); return !v.busy && len(v.owed) == 0 && len(v.events) >= 6 })
	if v := s.snapshot(); v.problem != "" || v.state != "Connected" {
		t.Fatalf("%+v", v)
	}
	click("Search Socket.IO messages")
	tt.Type("answer-observed")
	tt.Frame()
	if !tt.HasText("In · answer-observed") || tt.HasText("In · question · #7") {
		t.Fatal("timeline search did not filter")
	}
	click("Copy shown message")
	if !strings.Contains(tt.Clipboard(), "73") {
		t.Fatal("copy did not use selected response")
	}
	click("Cancel send")
	waitSocketIO(t, tt, func() bool { return !d.composeWaiting })
	if v := s.snapshot(); v.state != "Disconnected" || len(v.owed) != 0 {
		t.Fatalf("disconnect %+v", v)
	}
	if err := <-observed; err != nil {
		t.Fatal(err)
	}
	d.socketIOConfig.Attachments = `["AP9J"]`
	value := d.value()
	loaded := libraryRoundTrip(t, []savedRequest{value})
	if len(loaded) != 1 || loaded[0].Protocol != "Socket.IO" || !reflect.DeepEqual(value.SocketIO, loaded[0].SocketIO) || loaded[0].Body != value.Body {
		t.Fatal("Socket.IO config lost on disk")
	}
	a.addDocument(loaded[0], loaded[0].key())
	tt.Frame()
	if a.requestDocument.dirty() || a.requestDocument.socketIOUI.session != nil {
		t.Fatal("restore dirtied or reconnected the document")
	}
}

func TestSocketIODocumentCloseCancelsHandshake(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := new(websocket.Upgrader).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		close(started)
		ws.ReadMessage()
	}))
	t.Cleanup(server.Close)
	a := libraryApp(t)
	a.srv = requestProxy(t)
	a.addDocument(savedRequest{Protocol: "Socket.IO", URL: server.URL}, "")
	d := a.requestDocument
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Connect"); err != nil {
		t.Fatal(err)
	}
	<-started
	s := d.socketIOUI.session
	a.closeDocument(d)
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("closing document leaked a dialing session")
	}
	for range 3 {
		tt.Frame()
	}
	if s.snapshot().state != "Disconnected" {
		t.Fatal(s.snapshot().state)
	}
}

func TestSocketIOProtocolSelectorAndReturnToHTTP(t *testing.T) {
	a := libraryApp(t)
	a.addDocument(savedRequest{Name: "Protocol choice", URL: "https://api.example.com"}, "")
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Request protocol"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Socket.IO"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		tt.Frame()
	}
	if a.protocol != "Socket.IO" || !tt.HasText("No Socket.IO connection yet") {
		t.Fatalf("protocol choice %q: %v", a.protocol, tt.Texts())
	}
	a.socketIOState().session = &socketIOSession{finished: true, state: "Disconnected", cancel: func() {}}
	tt.Frame()
	if err := tt.Click("Request protocol"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "HTTP"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		tt.Frame()
	}
	if a.protocol != "" || a.socketIOUI != nil || tt.HasText("Socket.IO messages") {
		t.Fatalf("stale Socket.IO state survived protocol switch: protocol=%q, runtime=%+v, texts=%v", a.protocol, a.socketIOUI, tt.Texts())
	}
}

func TestSocketIODocumentBoundsRetainedTimeline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := new(websocket.Upgrader).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		ws.WriteMessage(1, []byte(`0{"sid":"engine","pingInterval":5000,"pingTimeout":5000}`))
		ws.ReadMessage()
		ws.WriteMessage(1, []byte(`40{"sid":"socket"}`))
		for i := range 204 {
			if err := ws.WriteMessage(1, fmt.Appendf(nil, `42["tick",%d]`, i)); err != nil {
				return
			}
		}
		ws.WriteMessage(1, []byte(`42["large","`+strings.Repeat("界", 30000)+`"]`))
		ws.WriteMessage(1, []byte("41"))
	}))
	t.Cleanup(server.Close)
	a := libraryApp(t)
	a.srv = requestProxy(t)
	a.addDocument(savedRequest{Protocol: "Socket.IO", URL: server.URL}, "")
	a.sendComposed()
	s := a.socketIOUI.session
	t.Cleanup(s.cancel)
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		t.Fatal("burst did not finish")
	}
	v := s.snapshot()
	if v.state != "Server disconnected" || len(v.events) != 200 || v.evicted != 7 || v.events[0].seq != 8 || v.events[199].seq != 207 {
		t.Fatalf("timeline %s: %d retained, %d evicted", v.state, len(v.events), v.evicted)
	}
	large := v.events[198]
	if large.name != "large" || large.size < 90000 || len(large.text) > 66<<10 || !utf8.ValidString(large.text) || !strings.Contains(large.text, "Retained") {
		t.Fatal("large Unicode event not explicitly bounded")
	}
}
