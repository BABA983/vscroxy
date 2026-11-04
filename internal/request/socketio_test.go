package request

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/gorilla/websocket"
)

// The peer uses literal protocol packets, independently of the client codec.
func socketIOPeer(t *testing.T, secure bool, serve func(*websocket.Conn, *http.Request)) (*capture.Server, string) {
	t.Helper()
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := new(websocket.Upgrader).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer ws.Close()
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		serve(ws, r)
	}))
	if secure {
		s.StartTLS()
	} else {
		s.Start()
	}
	t.Cleanup(s.Close)
	proxy := testProxy(t)
	proxy.SetSSL(capture.SSLConfig{All: true})
	return proxy, s.URL
}

func TestSocketIOProtocolThroughWSAndWSS(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint(secure), func(t *testing.T) {
			sent := make(chan error, 1)
			proxy, endpoint := socketIOPeer(t, secure, func(ws *websocket.Conn, r *http.Request) {
				check := func(typ int, want []byte) bool {
					gotType, got, err := ws.ReadMessage()
					if err != nil || gotType != typ || !bytes.Equal(got, want) {
						sent <- fmt.Errorf("wire got %d %q %v, want %d %q", gotType, got, err, typ, want)
						return false
					}
					return true
				}
				if r.URL.Path != "/custom/" || r.URL.Query().Get("EIO") != "4" || r.URL.Query().Get("transport") != "websocket" || r.URL.Query().Has("sid") || r.URL.Query().Get("client") != "73" || r.Header.Get("Authorization") != "Bearer fixture" || !strings.Contains(r.Header.Get("Cookie"), "session=fixture") {
					sent <- fmt.Errorf("handshake metadata incorrect")
					return
				}
				ws.WriteMessage(1, []byte(`0{"sid":"engine-id","pingInterval":5000,"pingTimeout":5000,"maxPayload":1048576}`))
				if !check(1, []byte(`40/admin,{"token":"fixture"}`)) {
					return
				}
				ws.WriteMessage(1, []byte(`40/admin,{"sid":"namespace-id"}`))
				if !check(1, []byte(`452-/admin,0["bytes",{"left":{"_placeholder":true,"num":1},"right":{"_placeholder":true,"num":0}}]`)) || !check(2, []byte{255, 0, 73, 195, 40}) || !check(2, []byte{}) {
					return
				}
				// Heartbeats may occur between binary header and attachments.
				ws.WriteMessage(1, []byte(`461-/admin,0[{"_placeholder":true,"num":0}]`))
				ws.WriteMessage(1, []byte("2probe"))
				if !check(1, []byte("3probe")) {
					return
				}
				ws.WriteMessage(2, []byte{0, 255, 42})
				ws.WriteMessage(1, []byte(`42/admin,7["question",{"n":9007199254740993,"text":"你好"}]`))
				if !check(1, []byte(`461-/admin,7[{"_placeholder":true,"num":0}]`)) || !check(2, []byte{73, 255, 0}) {
					return
				}
				ws.WriteMessage(1, []byte(`41/admin,`))
				sent <- nil
			})
			jar, _ := cookiejar.New(nil)
			u, _ := url.Parse(endpoint)
			jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "fixture"}})
			c, err := DialSocketIO(t.Context(), proxy.Addr(), &Prepared{URL: endpoint + "/custom/?client=73&EIO=3&transport=polling&sid=stale", Header: http.Header{"Authorization": {"Bearer fixture"}}}, SocketIOConfig{Namespace: "/admin", Auth: `{"token":"fixture"}`}, proxy.CACertificate(), jar)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if c.SID != "namespace-id" {
				t.Fatalf("namespace SID %q", c.SID)
			}
			for _, args := range []jsontext.Value{jsontext.Value(`{}`), jsontext.Value(`[{"_placeholder":true,"num":3}]`), jsontext.Value(`["` + strings.Repeat("x", 1<<20) + `"]`)} {
				if _, err := c.Emit("invalid", args, nil, true); err == nil {
					t.Fatal("invalid outbound message accepted")
				}
			}
			if err := c.Acknowledge(99, jsontext.Value(`[]`), nil); err == nil {
				t.Fatal("unknown ACK accepted")
			}
			id, err := c.Emit("bytes", jsontext.Value(`[{"left":{"_placeholder":true,"num":1},"right":{"_placeholder":true,"num":0}}]`), [][]byte{{255, 0, 73, 195, 40}, {}}, true)
			if err != nil || id == nil || *id != 0 {
				t.Fatalf("emit %v %v", id, err)
			}
			ack, err := c.Read()
			if err != nil || !ack.IsACK || ack.ACKID == nil || *ack.ACKID != 0 || len(ack.Attachments) != 1 || !bytes.Equal(ack.Attachments[0], []byte{0, 255, 42}) {
				t.Fatalf("ACK %+v %v", ack, err)
			}
			q, err := c.Read()
			if err != nil || q.Event != "question" || q.ACKID == nil || *q.ACKID != 7 || string(q.Args) != `[{"n":9007199254740993,"text":"你好"}]` {
				t.Fatalf("event %+v %v", q, err)
			}
			if err := c.Acknowledge(7, jsontext.Value(`[{"_placeholder":true,"num":0}]`), [][]byte{{73, 255, 0}}); err != nil {
				t.Fatal(err)
			}
			if err := c.Acknowledge(7, jsontext.Value(`[]`), nil); err == nil {
				t.Fatal("ACK reused")
			}
			if _, err := c.Read(); !errors.Is(err, io.EOF) {
				t.Fatalf("disconnect %v", err)
			}
			if _, err := c.Emit("later", jsontext.Value(`[]`), nil, false); err == nil {
				t.Fatal("disconnected send buffered")
			}
			if err := <-sent; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSocketIOCancellationAndInvalidPeers(t *testing.T) {
	for _, stage := range []string{"open", "namespace", "read"} {
		t.Run("cancel/"+stage, func(t *testing.T) {
			waiting := make(chan struct{})
			proxy, endpoint := socketIOPeer(t, false, func(ws *websocket.Conn, _ *http.Request) {
				if stage != "open" {
					ws.WriteMessage(1, []byte(`0{"sid":"engine","pingInterval":5000,"pingTimeout":5000}`))
					ws.ReadMessage()
					if stage == "read" {
						ws.WriteMessage(1, []byte(`40{"sid":"socket"}`))
					}
				}
				close(waiting)
				ws.ReadMessage()
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				c, err := DialSocketIO(ctx, proxy.Addr(), &Prepared{URL: endpoint}, SocketIOConfig{}, nil, nil)
				if err == nil {
					defer c.Close()
					_, err = c.Read()
				}
				result <- err
			}()
			<-waiting
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancel did not release socket")
			}
		})
	}
	for _, wire := range []string{`451-["x",{"_placeholder":true,"num":2}]`, `45999-["x"]`, `42[]`, `43[1]`, `42/other,["x"]`, `442`, `451-["x",{"_placeholder":true,"num":0}]|42["interleaved"]`} {
		t.Run(wire, func(t *testing.T) {
			proxy, endpoint := socketIOPeer(t, false, func(ws *websocket.Conn, _ *http.Request) {
				ws.WriteMessage(1, []byte(`0{"sid":"engine","pingInterval":5000,"pingTimeout":5000}`))
				ws.ReadMessage()
				ws.WriteMessage(1, []byte(`40{"sid":"socket"}`))
				for part := range strings.SplitSeq(wire, "|") {
					ws.WriteMessage(1, []byte(part))
				}
				ws.ReadMessage()
			})
			c, err := DialSocketIO(t.Context(), proxy.Addr(), &Prepared{URL: endpoint}, SocketIOConfig{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, err := c.Read(); err == nil {
				t.Fatal("malformed peer accepted")
			}
		})
	}
}

func TestSocketIOHeartbeatNotExtendedByEvents(t *testing.T) {
	proxy, endpoint := socketIOPeer(t, false, func(ws *websocket.Conn, _ *http.Request) {
		ws.WriteMessage(1, []byte(`0{"sid":"engine","pingInterval":60,"pingTimeout":60}`))
		ws.ReadMessage()
		ws.WriteMessage(1, []byte(`40{"sid":"socket"}`))
		// Intentionally violate EIO heartbeat while still producing traffic.
		for range time.Tick(5 * time.Millisecond) {
			if err := ws.WriteMessage(1, []byte(`42["traffic",73]`)); err != nil {
				return
			}
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	c, err := DialSocketIO(ctx, proxy.Addr(), &Prepared{URL: endpoint}, SocketIOConfig{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	count := 0
	for {
		_, err := c.Read()
		if err != nil {
			timeout, ok := errors.AsType[net.Error](err)
			if !ok || !timeout.Timeout() || ctx.Err() != nil {
				t.Fatalf("expected heartbeat timeout, got %v (context %v)", err, ctx.Err())
			}
			if count == 0 {
				t.Fatal("did not exercise traffic before timeout")
			}
			break
		}
		count++
	}
}

func TestSocketIOAttachmentInput(t *testing.T) {
	b, err := SocketIOAttachments(`["/wBJwyg=",""]`)
	if err != nil || len(b) != 2 || !bytes.Equal(b[0], []byte{255, 0, 73, 195, 40}) || len(b[1]) != 0 {
		t.Fatalf("%v %v", b, err)
	}
	for _, text := range []string{`null`, `[null]`, `["/w"]`, `["/x=="]`, `["_w=="]`, `["/w==\n"]`, `[3]`} {
		if _, err := SocketIOAttachments(text); err == nil {
			t.Fatalf("accepted %s", text)
		}
	}
}

// Optional interoperability against the official socket.io@4 server described
// in docs/socketio.md; regular tests above need no Node runtime or network.
func TestSocketIOOfficialServer(t *testing.T) {
	endpoint := os.Getenv("VSCROXY_SOCKETIO_URL")
	if endpoint == "" {
		t.Skip("set VSCROXY_SOCKETIO_URL for the official server fixture")
	}
	proxy := testProxy(t)
	for _, ns := range []string{"/", "/admin"} {
		t.Run(ns, func(t *testing.T) {
			c, err := DialSocketIO(t.Context(), proxy.Addr(), &Prepared{URL: endpoint + "?client=73"}, SocketIOConfig{Namespace: ns, Auth: `{"token":"fixture"}`}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, err := c.Emit("bytes", jsontext.Value(`[{"_placeholder":true,"num":0}]`), [][]byte{{255, 0, 73, 195, 40}}, true); err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for len(seen) < 5 {
				m, err := c.Read()
				if err != nil {
					t.Fatal(err)
				}
				switch m.Event {
				case "ready":
					if !strings.Contains(string(m.Args), `"transport":"websocket"`) || !strings.Contains(string(m.Args), `"query":"73"`) {
						t.Fatalf("ready %s", m.Args)
					}
					seen["ready"] = true
				case "question":
					if m.ACKID == nil {
						t.Fatal("no server ACK id")
					}
					if err := c.Acknowledge(*m.ACKID, jsontext.Value(`["answer-73"]`), nil); err != nil {
						t.Fatal(err)
					}
					seen["question"] = true
				case "answer-observed":
					if string(m.Args) != `["answer-73"]` {
						t.Fatal(string(m.Args))
					}
					seen["answer"] = true
				case "bytes-back":
					if len(m.Attachments) != 1 || !bytes.Equal(m.Attachments[0], []byte{255, 0, 73, 195, 40}) {
						t.Fatalf("binary %+v", m)
					}
					seen["binary"] = true
				default:
					if !m.IsACK || m.ACKID == nil || *m.ACKID != 0 || len(m.Attachments) != 1 || !bytes.Equal(m.Attachments[0], []byte{255, 0, 73, 195, 40}) {
						t.Fatalf("binary ACK %+v", m)
					}
					seen["ack"] = true
				}
			}
			if _, err := c.Emit("shutdown", jsontext.Value(`[]`), nil, false); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Read(); !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
		})
	}
	if _, err := DialSocketIO(t.Context(), proxy.Addr(), &Prepared{URL: endpoint}, SocketIOConfig{Namespace: "/deny"}, nil, nil); err == nil {
		t.Fatal("namespace refusal accepted")
	}
}
