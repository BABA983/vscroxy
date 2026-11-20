package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"slices"
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
	upgraded := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := new(websocket.Upgrader).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		upgraded <- ws
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
	serverConn := <-upgraded
	t.Cleanup(func() { _ = serverConn.Close() })
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
	// The protocol sits in the method menu, which is the bar's only dropdown.
	if err := tt.Click("Method"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Protocol", "Socket.IO"); err != nil {
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
	// The same control now names the protocol, and its menu goes back.
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

func TestSocketIOSavedMessagesTabAddsTheComposer(t *testing.T) {
	a := workspaceApp()
	a.openCompose()
	d := a.requestDocument
	d.protocol = "Socket.IO"
	d.socketIOConfig = request.SocketIOConfig{Namespace: "/", Event: "join", WantACK: true}
	d.composeBody = `["room-1"]`
	d.socketIOState().tab = 5
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	for _, want := range []string{"Add current", "Emit saved", "Save sent", "Saved"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	if err := tt.Click("Add current"); err != nil {
		t.Fatal(err)
	}
	want := []request.SocketIOMessageDoc{{Event: "join", Args: `["room-1"]`, WantACK: true}}
	if !slices.Equal(d.socketIOConfig.Messages, want) {
		t.Fatalf("messages: %+v", d.socketIOConfig.Messages)
	}
	if got := d.value().SocketIO.Messages; !slices.Equal(got, want) {
		t.Fatalf("saved config: %+v", got)
	}
}

func TestSocketIOEmitsTheSavedListInOrder(t *testing.T) {
	frames := make(chan string, 4)
	// Keep the socket open until the sent list has been read. Closing it as
	// soon as both frames arrive made the second emit race its own recording.
	releaseServer := make(chan struct{})
	defer close(releaseServer)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := new(websocket.Upgrader).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		ws.WriteMessage(1, []byte(`0{"sid":"engine","pingInterval":5000,"pingTimeout":5000}`))
		if _, b, err := ws.ReadMessage(); err != nil || !strings.HasPrefix(string(b), "40/admin,") {
			frames <- fmt.Sprintf("handshake %s %v", b, err)
			return
		}
		ws.WriteMessage(1, []byte(`40/admin,{"sid":"socket"}`))
		for range 2 {
			_, b, err := ws.ReadMessage()
			if err != nil {
				frames <- fmt.Sprint("read: ", err)
				return
			}
			frames <- string(b)
		}
		<-releaseServer
	}))
	t.Cleanup(server.Close)
	a := libraryApp(t)
	a.srv = requestProxy(t)
	a.addDocument(savedRequest{
		Protocol: "Socket.IO", Name: "Saved", URL: server.URL,
		SocketIO: request.SocketIOConfig{Namespace: "/admin", Messages: []request.SocketIOMessageDoc{
			{Event: "one", Args: `[1]`},
			{Event: "two", Args: `[2]`},
		}},
	}, "")
	d := a.requestDocument
	t.Cleanup(func() { d.socketIOState().stop() })
	tt := ui.NewTester(a.view, 1100, 800)
	if err := tt.Click("Connect"); err != nil {
		t.Fatal(err)
	}
	waitSocketIO(t, tt, func() bool { return d.socketIOState().session.snapshot().ready })
	d.socketIOState().tab = 5
	tt.Frame()
	if err := tt.Click("Emit saved"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for len(got) < 2 {
		select {
		case frame := <-frames:
			got = append(got, frame)
		case <-time.After(10 * time.Second):
			t.Fatalf("server saw %q", got)
		}
	}
	want := []string{`42/admin,["one",1]`, `42/admin,["two",2]`}
	if !slices.Equal(got, want) {
		t.Fatalf("frames: got %q want %q", got, want)
	}
	waitSocketIO(t, tt, func() bool { return !d.socketIOState().session.snapshot().busy })
	sent := d.socketIOState().session.sentMessages()
	if !slices.Equal(sent, d.socketIOConfig.Messages) {
		t.Fatalf("sent: %+v", sent)
	}
}

func TestSocketIORecordsEmitWhenServerClosesFirst(t *testing.T) {
	secondWrite := make(chan struct{})
	resumeWrite := make(chan struct{}, 1)
	defer func() {
		select {
		case resumeWrite <- struct{}{}:
		default:
		}
	}()
	serverRead := make(chan struct{})
	serverConn := make(chan *websocket.Conn, 1)
	releaseServer := make(chan struct{})
	defer close(releaseServer)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := new(websocket.Upgrader).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		serverConn <- ws
		ws.WriteMessage(1, []byte(`0{"sid":"engine","pingInterval":5000,"pingTimeout":5000}`))
		if _, b, err := ws.ReadMessage(); err != nil || !strings.HasPrefix(string(b), "40/admin,") {
			return
		}
		ws.WriteMessage(1, []byte(`40/admin,{"sid":"socket"}`))
		for range 2 {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
		close(serverRead)
		<-releaseServer
	}))
	t.Cleanup(server.Close)
	a := libraryApp(t)
	a.srv = requestProxy(t)
	messages := []request.SocketIOMessageDoc{
		{Event: "one", Args: `[1]`},
		{Event: "two", Args: `[2]`},
	}
	a.addDocument(savedRequest{
		Protocol: "Socket.IO", Name: "Close race", URL: server.URL,
		SocketIO: request.SocketIOConfig{Namespace: "/admin", Messages: messages},
	}, "")
	d := a.requestDocument
	t.Cleanup(func() { d.socketIOState().stop() })
	tt := ui.NewTester(a.view, 1100, 800)
	if err := tt.Click("Connect"); err != nil {
		t.Fatal(err)
	}
	waitSocketIO(t, tt, func() bool { return d.socketIOState().session.snapshot().ready })
	s := d.socketIOState().session
	// Hold the second emit after Emit returns, close the session through its
	// read loop, then let it record. No scheduling luck is involved.
	s.afterWrite = func(event string) {
		if event == "two" {
			close(secondWrite)
			<-resumeWrite
		}
	}
	emitted := make(chan error, 1)
	go func() { emitted <- s.emitAll(messages) }()
	select {
	case <-secondWrite:
	case <-time.After(10 * time.Second):
		t.Fatal("the second emit never reached the hook")
	}
	select {
	case <-serverRead:
	case <-time.After(10 * time.Second):
		t.Fatal("the server never received both frames")
	}
	ws := <-serverConn
	if err := ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the server close did not finish the session")
	}
	resumeWrite <- struct{}{}
	if err := <-emitted; err != nil {
		t.Fatal(err)
	}
	v := s.snapshot()
	if !v.finished || v.busy || v.problem != "" || v.state != "Server disconnected" {
		t.Fatalf("session after close: %+v", v)
	}
	var out []string
	closed, late := false, false
	for _, e := range v.events {
		if e.direction == "System" && e.name == "Server disconnected" {
			closed = true
		}
		if e.direction == "Out" {
			out = append(out, e.name)
			if closed {
				late = true
			}
		}
	}
	if !slices.Equal(out, []string{"one", "two"}) || !closed || !late {
		t.Fatalf("timeline after close: out=%q closed=%v late=%v events=%+v", out, closed, late, v.events)
	}
	if got := s.sentMessages(); !slices.Equal(got, messages) {
		t.Fatalf("sent after close: %+v", got)
	}
}
