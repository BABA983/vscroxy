package app

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
)

// longMessage is a message the capture spills: longer than the preview it keeps
// inline, with a tail that only the whole message has.
func longMessage(t *testing.T) (head, whole, file string) {
	t.Helper()
	whole = strings.Repeat("0123456789abcdef", 700) + "END-ONLY-needle-tail"
	file = filepath.Join(t.TempDir(), "frame.bin")
	if err := os.WriteFile(file, []byte(whole), 0o600); err != nil {
		t.Fatalf("write spill: %v", err)
	}
	return whole[:4<<10], whole, file
}

// TestMessagesKeepTheWholeOfLongOnes covers what the pane gets for a message
// past the inline preview: the head in the view, the rest in the spill file the
// capture wrote, and a message the capture had to cut saying so.
func TestMessagesKeepTheWholeOfLongOnes(t *testing.T) {
	head, whole, file := longMessage(t)
	list := appendMessage(nil, capture.FrameEvent{FlowID: 3, Frame: capture.Frame{
		Seq: 1, Direction: "in", Kind: "text", Data: head, File: file,
		Size: len(whole), Kept: len(whole), At: time.Now(),
	}})
	if len(list) != 1 {
		t.Fatalf("messages = %d, want 1", len(list))
	}
	m := list[0]
	if got := m.payload(); got != whole {
		t.Fatalf("payload = %d bytes, want the whole %d", len(got), len(whole))
	}
	if m.cut() != 0 {
		t.Errorf("cut = %d, want none of the message missing", m.cut())
	}
	if preview := m.preview(); !strings.HasPrefix(preview, "0123456789abcdef") || len([]rune(preview)) > maxPreview+1 {
		t.Errorf("preview = %q, want the head of the message on one line", preview)
	}
	if !strings.Contains(m.meta(), "spilled") {
		t.Errorf("meta = %q, want it to say the message was spilled", m.meta())
	}

	// A message larger than the spill cap keeps its head and reports the rest
	// as cut, rather than pretending the message is what is here.
	cut := appendMessage(nil, capture.FrameEvent{FlowID: 3, Frame: capture.Frame{
		Seq: 2, Direction: "in", Kind: "text", Data: head, Size: len(whole) + 5<<20, Kept: len(whole),
	}})[0]
	if got, want := cut.cut(), 5<<20; got != want {
		t.Errorf("cut = %d, want %d", got, want)
	}
	if !strings.Contains(cut.meta(), "kept of") {
		t.Errorf("meta = %q, want it to say how much of the message is here", cut.meta())
	}
}

// TestMessageWindowKeepsTheNewestAndCountsTheDropped covers the bound on what
// the app keeps: the newest messages stay, and what left the window, or never
// arrived because the capture's queue was full, stays counted.
func TestMessageWindowKeepsTheNewestAndCountsTheDropped(t *testing.T) {
	var list []messageView
	for i := 1; i <= maxMessages+12; i++ {
		list = appendMessage(list, capture.FrameEvent{FlowID: 9, Frame: capture.Frame{
			Seq: i, Direction: "in", Kind: "text", Data: fmt.Sprintf("message %d", i),
		}})
	}
	if len(list) != maxMessages {
		t.Fatalf("messages = %d, want the window of %d", len(list), maxMessages)
	}
	if list[0].data != "message 13" || list[len(list)-1].data != fmt.Sprintf("message %d", maxMessages+12) {
		t.Fatalf("window = %q…%q, want the newest %d", list[0].data, list[len(list)-1].data, maxMessages)
	}
	if got := messagesDropped(list); got != 12 {
		t.Errorf("dropped = %d, want the 12 that left the window", got)
	}
	if note := messagesNote(list); note != fmt.Sprintf("%d messages · 12 dropped", maxMessages) {
		t.Errorf("note = %q, want the window and the loss", note)
	}

	// A message the capture's queue could not take leaves a gap, which counts
	// as dropped even though the window never held it.
	seq := maxMessages + 12 + 6
	list = appendMessage(list, capture.FrameEvent{FlowID: 9, Frame: capture.Frame{
		Seq: seq, Direction: "in", Kind: "text", Data: "after the gap",
	}})
	// 218 messages came, 200 are here: 13 left the window for the one that
	// arrived and 5 never arrived at all.
	if got := messagesDropped(list); got != 18 {
		t.Errorf("dropped = %d, want 18", got)
	}
	if last := list[len(list)-1]; last.seq != seq || last.data != "after the gap" {
		t.Errorf("last = %+v, want the message after the gap", last)
	}
}

// TestMessageSearchAndDirection covers the filters over a flow's messages: the
// direction a message went, and the text a search reads, which includes the
// name and id an SSE event carries.
func TestMessageSearchAndDirection(t *testing.T) {
	list := []messageView{
		messageViewOf(capture.Frame{Seq: 1, Direction: "out", Kind: "text", Data: `{"op":"subscribe"}`}),
		messageViewOf(capture.Frame{Seq: 2, Direction: "in", Kind: "text", Data: `{"tick":1}`}),
		messageViewOf(capture.Frame{Seq: 3, Direction: "in", Kind: "event", EventName: "price", EventID: "42", Data: `{"usd":7}`}),
	}
	p := &messagePanel{}
	if got := p.matching(list); len(got) != 3 {
		t.Fatalf("all = %d messages, want 3", len(got))
	}
	p.direction = 1
	if got := p.matching(list); len(got) != 2 || got[0].seq != 2 || got[1].seq != 3 {
		t.Fatalf("in = %+v, want the two the server sent", got)
	}
	p.direction = 2
	if got := p.matching(list); len(got) != 1 || got[0].seq != 1 {
		t.Fatalf("out = %+v, want the one the client sent", got)
	}
	p.direction = 0
	for query, want := range map[string]int{"PRICE": 3, "usd": 3, "42": 3, "subscribe": 1, "nothing": 0} {
		p.query = query
		got := p.matching(list)
		if len(got) != 1 && want != 0 {
			t.Errorf("query %q = %d messages, want the one", query, len(got))
			continue
		}
		if want == 0 && len(got) != 0 {
			t.Errorf("query %q = %d messages, want none", query, len(got))
			continue
		}
		if want != 0 && got[0].seq != want {
			t.Errorf("query %q chose #%d, want #%d", query, got[0].seq, want)
		}
	}
	if label := list[2].label(); label != "price" {
		t.Errorf("label = %q, want the SSE event named", label)
	}
	if line := list[2].kindLine(); line != "event · event price · id 42" {
		t.Errorf("kindLine = %q, want what the row's tooltip says", line)
	}
	if list[2].binary() {
		t.Error("a JSON SSE event read as binary")
	}
	if !messageViewOf(capture.Frame{Kind: "binary", Data: "\x00\x01"}).binary() {
		t.Error("a binary message did not read as binary")
	}
}

// messagesApp is the app a messages test drives: one WebSocket flow, chosen, so
// its detail pane shows the Messages tab.
func messagesApp(frames map[int][]frame) *app {
	a := &app{
		filter: "all", sidebarWidth: 220, tablePercent: 56,
		trafficOpen: true, domainsOpen: true, clientsOpen: true, historyOpen: true,
		collectionOpen: make(map[string]*bool), icons: make(map[string]ui.ImageSource),
		detailFor: -1, frames: frames,
		flows: []flow{{
			id: 1, method: "WS", scheme: "ws", host: "api.example.com", path: "/stream",
			url: "ws://api.example.com/stream", status: 101, kind: "ws",
			process: "Browser", at: time.Now(),
		}},
	}
	a.countDomains()
	a.countClients()
	a.applyFilter()
	return a
}

// TestMessagesPaneShowsTheWholeChosenMessage drives the tab as a person does:
// it opens the Messages tab, reads the newest message in full, copies it, then
// searches and filters the list.
func TestMessagesPaneShowsTheWholeChosenMessage(t *testing.T) {
	head, whole, file := longMessage(t)
	frames := map[int][]frame{1: {
		messageViewOf(capture.Frame{Seq: 1, Direction: "out", Kind: "text",
			Data: `{"op":"subscribe","channel":"trades"}`, Size: 39, Kept: 39, At: time.Now()}),
		messageViewOf(capture.Frame{Seq: 2, Direction: "in", Kind: "text",
			Data: head, File: file, Size: len(whole), Kept: len(whole), At: time.Now()}),
	}}
	a := messagesApp(frames)
	tt := ui.NewTester(a.view, 1100, 700)
	if err := tt.Click("Messages"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("2 messages") {
		t.Fatalf("no note on what the pane keeps: %q", tt.Texts())
	}
	// The newest message is chosen and shown whole, tail included: only the
	// spill file has that.
	if !tt.HasText(whole[len(whole)-40:]) {
		t.Fatal("the detail did not show the whole of the spilled message")
	}
	if !tt.HasText("spilled to") {
		t.Fatal("the detail did not say the message was spilled")
	}
	if err := tt.Click("Copy"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Clipboard(); got != whole {
		t.Fatalf("copied %d bytes, want the whole %d", len(got), len(whole))
	}

	// A tail-only match cannot come from the inline preview or metadata.
	if err := tt.Click("Search messages"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.messageUI.stopSearch)
	tt.Type("END-ONLY-needle-tail")
	deadline := time.Now().Add(3 * time.Second)
	for !tt.HasText("1 of 2 messages") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		tt.Frame()
	}
	if !tt.HasText("1 of 2 messages") || !tt.HasText("END-ONLY-needle-tail") {
		t.Fatal("message search did not find text beyond the 4 KiB inline head")
	}
	tt.Key(ui.Cmd, ui.KeyA)
	// Changing the search must not display a stale spill-search result.
	tt.Type("subscribe")
	if !tt.HasText("1 of 2 messages") {
		t.Fatalf("search did not narrow the list: %q", tt.Texts())
	}
	if !tt.HasText(`"channel": "trades"`) {
		t.Fatal("the chosen message is not shown as indented JSON")
	}

	// The direction filter keeps only what the client sent, once the search
	// is cleared as a person clears it.
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	if err := tt.Click("Out"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("1 of 2 messages") {
		t.Fatalf("the direction filter kept %q", tt.Texts())
	}
	if !tt.HasText(`"channel": "trades"`) || tt.HasText(`"tick"`) {
		t.Fatal("the client's message is not the one shown")
	}
}

// TestMessagesPaneShowsBinaryAsHex covers a message that is bytes: the pane
// opens it as a hex dump rather than mangling it into text.
func TestMessagesPaneShowsBinaryAsHex(t *testing.T) {
	binary := "\x00\x01\x02\xffhello\x00"
	frames := map[int][]frame{1: {
		messageViewOf(capture.Frame{Seq: 1, Direction: "in", Kind: "binary", Data: binary, Size: len(binary), Kept: len(binary)}),
	}}
	a := messagesApp(frames)
	tt := ui.NewTester(a.view, 1100, 700)
	if err := tt.Click("Messages"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("00000000  00 01 02 ff 68 65 6c 6c  6f 00") {
		t.Fatalf("the binary message did not open as hex: %q", tt.Texts())
	}
	if !tt.HasText("server → client") || !tt.HasText("10 B") {
		t.Fatalf("the detail does not say what the message is: %q", tt.Texts())
	}
	if !tt.HasText("00 01 02 ff 68 65 6c 6c 6f 00") {
		t.Fatal("the row does not preview the bytes")
	}
}

// TestMessagesSurviveASessionRoundTrip covers what saving and reopening a
// session does to messages: the whole retained payload comes back, with the
// metadata that says what it is and how many are not there.
func TestMessagesSurviveASessionRoundTrip(t *testing.T) {
	head, whole, file := longMessage(t)
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	var list []messageView
	list = appendMessage(list, capture.FrameEvent{FlowID: 7, Frame: capture.Frame{
		Seq: 4, Direction: "out", Kind: "text", Data: "hi", Size: 2, Kept: 2, At: at,
	}})
	list = appendMessage(list, capture.FrameEvent{FlowID: 7, Frame: capture.Frame{
		Seq: 5, Direction: "in", Kind: "event", Data: head, File: file,
		Size: len(whole), Kept: len(whole), At: at, EventID: "9", EventName: "tick",
	}})
	list = appendMessage(list, capture.FrameEvent{FlowID: 7, Frame: capture.Frame{
		Seq: 6, Direction: "in", Kind: "binary", Data: "\xff\x00\x80bytes", Size: 8, Kept: 8, At: at,
	}})
	flows := []flow{{id: 7, method: "WS", scheme: "wss", host: "api.example.com", path: "/stream",
		url: "wss://api.example.com/stream", status: 101, kind: "ws",
		streamEnd: capture.StreamEnd{At: at.Add(time.Second), Messages: 6}}}
	path := filepath.Join(t.TempDir(), "session.json")
	if err := writeSession(path, flows, map[int][]frame{7: list}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, gotFrames, err := readSession(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].kind != "ws" || got[0].url != "wss://api.example.com/stream" {
		t.Fatalf("flows = %+v", got)
	}
	if got[0].streamEnd != flows[0].streamEnd {
		t.Fatal("session lost stream completion evidence")
	}
	saved := gotFrames[7]
	if len(saved) != 3 {
		t.Fatalf("messages = %d, want 3", len(saved))
	}
	if saved[0].seq != 4 || saved[0].data != "hi" || !saved[0].at.Equal(at) {
		t.Errorf("first = %+v, want the message and when it came", saved[0])
	}
	long := saved[1]
	if long.seq != 5 || long.payload() != whole {
		t.Errorf("long = %d bytes at #%d, want the whole %d at #5", len(long.payload()), long.seq, len(whole))
	}
	if long.eventID != "9" || long.eventName != "tick" || long.kind != "event" {
		t.Errorf("long = %+v, want the SSE event's id and name", long)
	}
	if long.cut() != 0 || !long.at.Equal(at) {
		t.Errorf("long = %+v, want all of it and when it came", long)
	}
	if saved[2].payload() != "\xff\x00\x80bytes" || saved[2].kind != "binary" || saved[2].size != 8 {
		t.Fatal("session changed binary message bytes or metadata")
	}
	if dropped := messagesDropped(saved); dropped != 3 {
		t.Errorf("dropped = %d, want the 3 before #4", dropped)
	}
}

func TestMessagesPauseSurvivesLiveWindowEviction(t *testing.T) {
	a := messagesApp(map[int][]frame{1: {{seq: 1, kind: "text", direction: "in", data: "original payload", size: 16, kept: 16}}})
	t.Cleanup(a.messageUI.stopSearch)
	tt := ui.NewTester(a.view, 1100, 760)
	for _, label := range []string{"Messages", "Pause view"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	// More than a full window: appendMessage compacts the backing array, so
	// retaining the old slice instead of cloning it corrupts the paused view.
	for i := 2; i <= 205; i++ {
		a.addFrame(capture.FrameEvent{FlowID: 1, Frame: capture.Frame{
			Seq: i, Kind: "text", Direction: "in", Data: fmt.Sprintf("new payload %d", i), Size: 15, Kept: 15,
		}})
	}
	tt.Frame()
	if !tt.HasText("204 newer messages") || !tt.HasText("original payload") || tt.HasText("new payload 205") {
		t.Fatalf("paused view changed while capture continued: %q", tt.Texts())
	}
	if err := tt.Click("Copy"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != "original payload" {
		t.Fatal("paused selection did not preserve its payload")
	}
	if err := tt.Click("Export messages…"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("1 matching messages") || !tt.HasText("credentials") {
		t.Fatal("export did not describe the frozen window and sensitivity")
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Resume"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("new payload 205") || !tt.HasText("200 messages · 5 dropped") || tt.HasText("original payload") {
		t.Fatalf("resume did not return to the newest retained messages: %q", tt.Texts())
	}
}

func TestMessageExportKeepsBinarySpillAndMissingPayloadEvidence(t *testing.T) {
	head, whole, file := longMessage(t)
	at := time.Date(2026, 10, 23, 11, 12, 13, 0, time.UTC)
	list := []messageView{
		{seq: 4, direction: "in", kind: "event", data: head, file: file, size: len(whole), kept: len(whole), at: at, eventID: "price-7", eventName: "price"},
		{seq: 5, direction: "out", kind: "binary", data: "\xff\x00\x80", size: 3, kept: 3},
		{seq: 8, direction: "in", kind: "text", data: "head", file: file + ".gone", size: 300, kept: 300},
	}
	path := filepath.Join(t.TempDir(), "messages.json")
	archive := messageExport{URL: "wss://example.com/prices", Protocol: "ws", Paused: true, Retained: 3, NotRetained: 5, Direction: "all", End: capture.StreamEnd{At: at, Messages: 8}}
	if err := archive.write(path, list); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved messageExport
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Version != 1 || !saved.Paused || saved.NotRetained != 5 || saved.End.Messages != 8 || len(saved.Messages) != 3 {
		t.Fatalf("missing scope/completion metadata: %+v", saved)
	}
	if m := saved.Messages[0]; m.Data != whole || m.Seq != 4 || m.EventID != "price-7" || m.EventName != "price" || !m.At.Equal(at) || m.Kept != len(whole) || m.Error != "" {
		t.Fatalf("export lost full spilled payload or event metadata: %+v", m)
	}
	if string(saved.Messages[1].Payload) != "\xff\x00\x80" || saved.Messages[1].Direction != "out" {
		t.Fatal("binary bytes or direction changed during JSON export")
	}
	if m := saved.Messages[2]; m.Data != "head" || m.Kept != 4 || m.Size != 300 || m.Error == "" {
		t.Fatal("missing spill file silently exported as a complete payload")
	}
}
