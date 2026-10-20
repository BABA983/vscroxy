package main

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

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

func TestSessionRoundTrip(t *testing.T) {
	flows := []flow{{
		id: 7, method: "WS", scheme: "ws", host: "example.com", path: "/",
		url: "ws://example.com/", status: 101, kind: "ws",
		reqHeaders: "CONNECT / HTTP/1.1", resHeaders: "HTTP/1.1 101",
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
	if len(gotFrames[7]) != 2 || gotFrames[7][1].data != "hello" {
		t.Fatalf("frames = %+v", gotFrames)
	}
}
