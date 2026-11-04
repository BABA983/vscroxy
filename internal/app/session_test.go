package app

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
)

func TestSensitiveCaptureExportsReplacePrivately(t *testing.T) {
	for _, format := range []string{"HAR", "session"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "export.json")
			if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			flows := []flow{{method: "POST", url: "http://example.test", reqBody: "secret", reqHeader: http.Header{"Authorization": {"Bearer private"}}}}
			var err error
			if format == "HAR" {
				err = writeHAR(path, flows)
			} else {
				err = writeSession(path, flows, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Fatalf("permissions = %o, want 600", info.Mode().Perm())
			}
			data, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(data), "Bearer private") || !strings.Contains(string(data), "secret") {
				t.Fatalf("export lost data: %s, %v", data, err)
			}
		})
	}
}

func TestHARExport(t *testing.T) {
	f := flow{
		id: 1, method: "GET", scheme: "https", host: "api.example.com",
		path: "/v1/users?page=2", url: "https://api.example.com/v1/users?page=2",
		status: 200, millis: 42, resBody: `{"ok":true}`,
		respHeader: http.Header{"Content-Type": {"application/json"}},
		at:         time.Now(),
	}
	ws := flow{id: 2, method: "WS", kind: "ws", url: "ws://example.com/"}

	har := harFromFlows([]flow{f, ws})
	if len(har.Log.Entries) != 1 {
		t.Fatalf("entries = %d, want 1 (WebSocket left out)", len(har.Log.Entries))
	}
	entry := har.Log.Entries[0]
	if entry.Request.URL != f.url || entry.Response.Status != 200 {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Response.Content.Text != `{"ok":true}` || entry.Response.Content.MimeType != "application/json" {
		t.Fatalf("content = %+v", entry.Response.Content)
	}
	if len(entry.Request.QueryString) != 1 || entry.Request.QueryString[0].Name != "page" || entry.Request.QueryString[0].Value != "2" {
		t.Fatalf("query = %+v", entry.Request.QueryString)
	}
	if entry.Time != 42 {
		t.Fatalf("time = %v", entry.Time)
	}
}

func TestHARTimingRoundTrip(t *testing.T) {
	f := flow{method: "GET", url: "https://example.test/", status: 200, millis: 42, wait: 31, download: 11,
		timing: &capture.Timing{TotalNS: 42_123_456, Reused: true, Truncated: true, Phases: []capture.TimingPhase{
			{Name: "Connection", StartNS: 123, DurationNS: 30_000_000},
			{Name: "TCP", StartNS: 456, DurationNS: 10_000_000, Failed: true},
			{Name: "Receive", StartNS: 30_000_123, DurationNS: 12_123_333, Incomplete: true},
		}},
	}
	path := filepath.Join(t.TempDir(), "timing.har")
	if err := writeHAR(path, []flow{f}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var har harFile
	if err := json.Unmarshal(data, &har); err != nil {
		t.Fatal(err)
	}
	entry := har.Log.Entries[0]
	if entry.Timings.Wait != 31 || entry.Timings.Receive != 11 || entry.Time != 42 {
		t.Fatalf("aggregate HAR timings lost: %+v", entry)
	}
	got, err := readHAR(path)
	if err != nil || len(got) != 1 {
		t.Fatalf("read: %+v, %v", got, err)
	}
	if !reflect.DeepEqual(got[0].timing, f.timing) || got[0].wait != 31 || got[0].download != 11 {
		t.Fatalf("HAR lost overlapping offsets, precision or state: %+v", got[0])
	}
}

func TestSessionRoundTrip(t *testing.T) {
	flows := []flow{{
		id: 7, method: "WS", scheme: "ws", host: "example.com", path: "/",
		url: "ws://example.com/", status: 101, kind: "ws",
		reqHeaders: "CONNECT / HTTP/1.1", resHeaders: "HTTP/1.1 101",
		color: "Red", note: "the frame that breaks it",
		actions: []string{"No Caching", "Map Remote · /api → https://staging.test/api"},
		conn:    conn{log: "[TLS client] FAILED: certificate rejected\n", error: "certificate rejected", errorStage: "TLS client"},
		wait:    13, download: 7,
		timing: &capture.Timing{TotalNS: 20_500_000, Reused: true, Phases: []capture.TimingPhase{
			{Name: "Proxy", DurationNS: 500_000},
			{Name: "Wait", StartNS: 500_000, DurationNS: 13_000_000},
			{Name: "Receive", StartNS: 13_500_000, DurationNS: 7_000_000, Incomplete: true},
		}},
	}}
	frames := map[int][]frame{7: {
		{direction: "out", kind: "text", data: "hi"},
		{direction: "in", kind: "text", data: "hello"},
	}}

	path := filepath.Join(t.TempDir(), "session.json")
	if err := writeSession(path, flows, frames); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, gotFrames, err := readSession(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].id != 7 || got[0].kind != "ws" || got[0].status != 101 {
		t.Fatalf("flows = %+v", got)
	}
	if got[0].color != "Red" || got[0].note != "the frame that breaks it" {
		t.Fatalf("mark = %q / %q, want it kept", got[0].color, got[0].note)
	}
	if strings.Join(got[0].actions, "\n") != strings.Join(flows[0].actions, "\n") {
		t.Fatal("session lost applied interventions")
	}
	if got[0].conn.log != flows[0].conn.log || got[0].conn.error != "certificate rejected" || got[0].conn.errorStage != "TLS client" {
		t.Fatalf("connection diagnosis lost: %+v", got[0].conn)
	}
	if !reflect.DeepEqual(got[0].timing, flows[0].timing) || got[0].wait != 13 || got[0].download != 7 {
		t.Fatalf("timing precision or state lost: %+v", got[0].timing)
	}
	if len(gotFrames[7]) != 2 || gotFrames[7][1].data != "hello" {
		t.Fatalf("frames = %+v", gotFrames)
	}
}

func TestSessionRetainsReplaySources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	flows := []flow{{id: 17, method: "GET", status: 200}, {id: 41, replayOf: 17, method: "GET", status: 201}}
	if err := writeSession(path, flows, nil); err != nil {
		t.Fatal(err)
	}
	got, _, err := readSession(path)
	if err != nil || len(got) != 2 || got[0].replayOf != 0 || got[1].replayOf != 17 {
		t.Fatalf("lost replay relationship: %+v %v", got, err)
	}
}

func TestSessionKeepsFullBodiesAndIncompleteState(t *testing.T) {
	large := strings.Repeat("x", 9<<20) + "session-tail-marker"
	spill := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(spill, []byte(large), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "session.json")
	flows := []flow{
		{id: 1, resBody: "head", resFile: spill, resSize: len(large)},
		{id: 2, reqBody: "partial", reqSize: 100, reqError: "transfer aborted"},
		{id: 3, resBody: "head", resFile: spill + ".missing", resSize: 100},
		{id: 4, reqBody: "\xff\x00upload", resBody: "\x80\x00download"},
	}
	if err := writeSession(path, flows, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(spill); err != nil {
		t.Fatal(err)
	}
	got, _, err := readSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0].resBody != large || got[0].resSize != len(large) {
		t.Fatal("session did not preserve the entire spilled body")
	}
	if ok, err := searchBody(t.Context(), bodyJobOf(got[0]), "session-tail-marker"); err != nil || !ok {
		t.Fatalf("reopened full-body search = %v, %v", ok, err)
	}
	if got[1].reqBody != "partial" || got[1].reqSize != 100 || got[1].reqError != "transfer aborted" {
		t.Fatalf("lost incomplete state: %+v", got[1])
	}
	if ok, err := searchBody(t.Context(), bodyJobOf(got[1]), "partial"); ok || err == nil {
		t.Fatalf("partial capture treated as complete: %v, %v", ok, err)
	}
	if got[2].resBody != "head" || got[2].resError == "" || strings.Contains(got[2].resBody, "gone") {
		t.Fatal("missing spill must preserve the head and a separate error, not a display notice")
	}
	if got[3].reqBody != "\xff\x00upload" || got[3].resBody != "\x80\x00download" {
		t.Fatal("session changed non-UTF-8 body bytes")
	}
}

// TestSessionLoadsAnOlderFile covers a session written before messages carried
// their number, length and time: it opens with what it has.
func TestSessionLoadsAnOlderFile(t *testing.T) {
	const doc = `{"version":1,
		"flows":[{"id":4,"method":"WS","scheme":"ws","host":"example.com","path":"/stream",
			"url":"ws://example.com/stream","status":101,"kind":"ws"}],
		"frames":{"4":[{"direction":"out","kind":"text","data":"hi"},
			{"direction":"in","kind":"tick","data":"one"}]}}`
	path := filepath.Join(t.TempDir(), "older.json")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	flows, frames, err := readSession(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(flows) != 1 || flows[0].url != "ws://example.com/stream" {
		t.Fatalf("flows = %+v", flows)
	}
	list := frames[4]
	if len(list) != 2 {
		t.Fatalf("messages = %d, want 2", len(list))
	}
	if list[0].data != "hi" || list[0].direction != "out" || list[1].kind != "tick" {
		t.Fatalf("messages = %+v, want what the file holds", list)
	}
	if got := list[1].payload(); got != "one" {
		t.Errorf("payload = %q, want the data the file has", got)
	}
	if list[1].size != 3 || list[1].cut() != 0 {
		t.Errorf("size = %d, cut = %d, want the data it has and nothing missing", list[1].size, list[1].cut())
	}
	if !list[0].at.IsZero() {
		t.Errorf("at = %v, want none, as the file has none", list[0].at)
	}
	if note := messagesNote(list); note != "2 messages" {
		t.Errorf("note = %q, want no loss claimed", note)
	}
}

func TestSelectedFlows(t *testing.T) {
	a := &app{flows: []flow{{id: 1, path: "/a"}, {id: 2, path: "/b"}, {id: 3, path: "/c"}}}
	a.chosen.Add(3)
	a.chosen.Add(1)
	got := a.selectedFlows()
	if len(got) != 2 || got[0].id != 1 || got[1].id != 3 {
		t.Fatalf("selected = %+v, want 1 then 3 in capture order", got)
	}
	if all := (&app{flows: a.flows}).selectedFlows(); all != nil {
		t.Fatalf("selected with nothing chosen = %+v, want none", all)
	}
}

func TestReadHAR(t *testing.T) {
	// What another tool writes: a JSON body, a base64 body, and timings.
	const doc = `{"log":{"version":"1.2","creator":{"name":"other","version":"1"},"entries":[
		{"startedDateTime":"2026-10-20T10:00:00.000Z","time":42,
		 "request":{"method":"post","url":"https://api.test/v1/owners?page=2","httpVersion":"HTTP/2.0",
			"headers":[{"name":"Content-Type","value":"application/json"},{"name":"X-Trace","value":"abc"}],
			"postData":{"mimeType":"application/json","text":"{\"name\":\"vscroxy\"}"}},
		 "response":{"status":201,"httpVersion":"HTTP/2.0",
			"headers":[{"name":"Content-Type","value":"application/json"}],
			"content":{"size":13,"mimeType":"application/json","text":"{\"id\":7}"}},
		 "timings":{"wait":30,"receive":12}},
		{"startedDateTime":"2026-10-20T10:00:01.000Z","time":5,
		 "request":{"method":"GET","url":"https://api.test/logo.png","headers":[]},
		 "response":{"status":200,"headers":[{"name":"Content-Type","value":"image/png"}],
			"content":{"size":3,"mimeType":"image/png","text":"AAEC","encoding":"base64"}},
		 "timings":{"wait":4,"receive":1}},
		{"time":1,"request":{"method":"GET","url":"/relative"},"response":{"status":200}}
	]}}`
	path := filepath.Join(t.TempDir(), "other.har")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	flows, err := readHAR(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// The entry without a host is not a request the table can show.
	if len(flows) != 2 {
		t.Fatalf("flows = %d, want 2", len(flows))
	}
	f := flows[0]
	if f.method != "POST" || f.host != "api.test" || f.path != "/v1/owners?page=2" || f.status != 201 {
		t.Errorf("first flow = %s %s%s %d", f.method, f.host, f.path, f.status)
	}
	if f.millis != 42 || f.wait != 30 || f.download != 12 {
		t.Errorf("timings = %d/%d/%d, want 42/30/12", f.millis, f.wait, f.download)
	}
	if got := f.reqHeader.Get("X-Trace"); got != "abc" {
		t.Errorf("X-Trace = %q", got)
	}
	if f.reqBody != `{"name":"vscroxy"}` || f.resBody != `{"id":7}` {
		t.Errorf("bodies = %q / %q", f.reqBody, f.resBody)
	}
	if f.bytes != 13 {
		t.Errorf("bytes = %d, want the content size", f.bytes)
	}
	if !f.at.Equal(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("at = %v", f.at)
	}
	// A body the file carries as base64 is decoded, so the Body tab shows
	// the bytes the server sent.
	if got := flows[1].resBody; got != "\x00\x01\x02" {
		t.Errorf("base64 body = %q, want the decoded bytes", got)
	}
}

func TestReadHARRejectsWhatItCannotUse(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.har")
	if err := os.WriteFile(empty, []byte(`{"log":{"entries":[]}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := readHAR(empty); err == nil {
		t.Error("a HAR with no entries was accepted")
	}
	broken := filepath.Join(dir, "broken.har")
	if err := os.WriteFile(broken, []byte(`not json`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := readHAR(broken); err == nil {
		t.Error("a file that is not JSON was accepted")
	}
}

func TestSessionsAnnotateCompareAndReopenWithoutChangingLiveTraffic(t *testing.T) {
	a := workspaceApp()
	a.openCompose() // A hidden draft must not be sent from a historical tab.
	if err := a.sessions.load(filepath.Join(t.TempDir(), "sessions")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.sessions.close)
	live, _ := json.Marshal(sessionFrom(a.flows, a.frames))
	for i, name := range []string{"Before", "After"} {
		file := sessionFrom([]flow{{id: 7, method: "GET", url: "https://example.test/", status: 200 + i, resBody: []string{"before", "after"}[i]}}, nil)
		file.Title = name
		a.keepSession(file)
	}
	tt := ui.NewTester(a.view, 1280, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("Session: Before")
	click("Pin")
	click("Session exchange note")
	tt.Type("Observed once")
	click("Session color")
	if err := chooseMenu(tt, "Purple"); err != nil {
		t.Fatal(err)
	}
	click("Use as left")
	click("Session: After")
	click("Compare with left")
	for label, want := range map[string]string{"Old text": "before", "New text": "after"} {
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		if tt.Clipboard() != want {
			t.Fatalf("cross-session %s = %q, want %q", label, tt.Clipboard(), want)
		}
	}
	click("Back to session")
	click("Close session: After")
	click("Session: Before")
	click("Response")
	click("Body")
	click("Session payload")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("must not edit")
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != "before" {
		t.Fatal("historical payload was editable")
	}
	tt.Key(ui.Cmd, ui.KeyEnter)
	if a.composeWaiting {
		t.Fatal("browsing sessions sent an unrelated hidden draft")
	}
	stillLive, _ := json.Marshal(sessionFrom(a.flows, a.frames))
	if !bytes.Equal(live, stillLive) {
		t.Fatal("historical browsing replaced live traffic")
	}
	// Annotations save as they change; closing only waits for the writes.
	a.sessions.close()
	reopened := sessionWorkspace{}
	if err := reopened.load(a.sessions.dir); err != nil || len(reopened.items) != 2 {
		t.Fatal("restart lost sessions", err)
	}
	var before *savedSession
	for _, item := range reopened.items {
		if item.data.Title == "Before" {
			before = item
		}
	}
	if before == nil || before.loaded || before.data.Flows != nil || before.count != 1 {
		t.Fatalf("startup loaded session payloads: %+v", before)
	}
	file, err := readSessionFile(before.path)
	if err != nil || !file.Flows[0].Pinned || file.Flows[0].Note != "Observed once" || file.Flows[0].Color != "Purple" {
		t.Fatal("annotations did not survive a restart", err)
	}
	// Opening a session after a restart loads its payloads in the background.
	b := workspaceApp()
	if err := b.sessions.load(a.sessions.dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.sessions.close)
	b.selectSession(before.key)
	for deadline := time.Now().Add(5 * time.Second); !b.sessions.current().loaded; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("session never loaded")
		}
		b.checkpointSessions()
	}
	if got := b.sessions.current().data.Flows; len(got) != 1 || got[0].ResBody != "before" {
		t.Fatalf("opened session = %+v", got)
	}
	if err := reopened.remove(before.key); err != nil || len(reopened.items) != 1 {
		t.Fatal("delete kept the session", err)
	}
	if _, err := os.Stat(before.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("delete left the session file", err)
	}
}
