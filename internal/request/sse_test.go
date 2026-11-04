package request

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
)

func TestSSEParsingThroughHTTPAndHTTPS(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint(secure), func(t *testing.T) {
			wire := "\xef\xbb\xbf:heartbeat\r\nevent: price\rdata:  leading  \rdata:second\rid: 17\r\r" +
				"event:ignored\n\ndata\n\ndata:\ndata\n\nid:bad\x00id\ndata: \xff\xff\xe2\x82A\xed\xa0\x80\n\n" +
				"id\ndata:reset\n\ndata:unterminated\n"
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("Authorization") != "Bearer test" {
					t.Error("missing prepared headers")
				}
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				for _, b := range []byte(wire) {
					w.Write([]byte{b})
					w.(http.Flusher).Flush()
				}
			})
			server := httptest.NewUnstartedServer(handler)
			if secure {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			proxy := testProxy(t)
			proxy.SetSSL(capture.SSLConfig{All: true})
			var got []SSEEvent
			err := ReceiveSSE(t.Context(), proxy.Addr(), &Prepared{URL: server.URL, Header: http.Header{"Authorization": {"Bearer test"}}}, proxy.CACertificate(), nil, false, func(u SSEUpdate) {
				if u.Event != nil {
					got = append(got, *u.Event)
				}
			})
			want := []SSEEvent{{"price", "17", " leading  \nsecond"}, {"message", "17", ""}, {"message", "17", "\n"}, {"message", "17", "\ufffd\ufffd\ufffdA\ufffd\ufffd\ufffd"}, {"message", "", "reset"}}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("events=%#v err=%v", got, err)
			}
		})
	}
}

func TestSSEReconnectIDsCookiesAnd204(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "test", Path: "/"})
			http.Redirect(w, r, "/events", http.StatusTemporaryRedirect)
			return
		}
		n := calls.Add(1)
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value != "test" {
			t.Error("redirect cookie missing")
		}
		if n == 1 {
			if r.Header.Get("Last-Event-ID") != "initial" {
				t.Error("initial id missing")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "id:old\ndata:first\n\nid:saved\n\nretry:100\nretry:2x\ndata:incomplete")
		} else {
			if r.Header.Get("Last-Event-ID") != "saved" {
				t.Errorf("reconnect id=%q", r.Header.Get("Last-Event-ID"))
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	proxy := testProxy(t)
	jar, _ := cookiejar.New(nil)
	var got []SSEUpdate
	err := ReceiveSSE(t.Context(), proxy.Addr(), &Prepared{URL: server.URL + "/start", Header: http.Header{"Last-Event-Id": {"initial"}}}, nil, jar, true, func(u SSEUpdate) { got = append(got, u) })
	if err != nil || calls.Load() != 2 || len(got) != 4 || got[1].Event == nil || got[1].Event.Data != "first" || got[2].State != "Reconnecting" || got[2].Retry != 100*time.Millisecond || got[3].State != "Stopped" {
		t.Fatalf("updates=%+v calls=%d err=%v", got, calls.Load(), err)
	}
}

func TestSSELiveDeliveryAndCancellation(t *testing.T) {
	first, stop := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data:first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-first:
		case <-stop:
			return
		}
		fmt.Fprint(w, "data:second\n\ndata:must-not-dispatch-after-cancel\n\n")
		w.(http.Flusher).Flush()
		<-stop
	}))
	defer server.Close()
	defer close(stop)
	proxy := testProxy(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var data []string
	done := make(chan error, 1)
	go func() {
		done <- ReceiveSSE(ctx, proxy.Addr(), &Prepared{URL: server.URL}, nil, nil, false, func(u SSEUpdate) {
			if u.Event == nil {
				return
			}
			data = append(data, u.Event.Data)
			if len(data) == 1 {
				close(first)
			} else {
				cancel()
			}
		})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(data, []string{"first", "second"}) {
			t.Fatalf("data=%v err=%v", data, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("events buffered until EOF or cancellation did not close the stream")
	}
}

func TestSSECancelReconnectAndResetID(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 2 && r.Header.Get("Last-Event-ID") != "" {
			t.Error("empty ID did not clear the reconnect header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id:\ndata:reset\n\nretry:100\n\n")
	}))
	defer server.Close()
	proxy := testProxy(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var reconnects int
	err := ReceiveSSE(ctx, proxy.Addr(), &Prepared{URL: server.URL, Header: http.Header{"Last-Event-Id": {"old"}}}, nil, nil, true, func(u SSEUpdate) {
		if u.State == "Reconnecting" {
			reconnects++
			if reconnects == 2 {
				cancel()
			}
		}
	})
	if !errors.Is(err, context.Canceled) || calls.Load() != 2 || reconnects != 2 {
		t.Fatalf("err=%v requests=%d reconnects=%d", err, calls.Load(), reconnects)
	}
}

func TestSSEDoesNotTrustUninstalledCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data:must-not-arrive\n\n")
	}))
	defer server.Close()
	proxy := testProxy(t)
	proxy.SetSSL(capture.SSLConfig{All: true})
	err := ReceiveSSE(t.Context(), proxy.Addr(), &Prepared{URL: server.URL}, nil, nil, false, func(u SSEUpdate) {
		if u.Event != nil {
			t.Error("accepted stream with untrusted certificate")
		}
	})
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("TLS verification bypassed: %v", err)
	}
}

func TestSSERejectsResponsesAndOversizedEvents(t *testing.T) {
	for _, tc := range []struct {
		name, content, body string
		status              int
	}{
		{"not SSE", "text/plain", "data:wrong\n\n", 200},
		{"HTTP failure", "text/event-stream", "data:wrong\n\n", 401},
		{"huge line", "text/event-stream", "data:" + strings.Repeat("x", 1<<20) + "\n\n", 200},
		{"huge event", "text/event-stream", strings.Repeat("data:"+strings.Repeat("x", 1024)+"\n", 1025) + "\n", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.content)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			proxy := testProxy(t)
			var events int
			err := ReceiveSSE(t.Context(), proxy.Addr(), &Prepared{URL: server.URL}, nil, nil, true, func(u SSEUpdate) {
				if u.Event != nil {
					events++
				}
			})
			if err == nil || events != 0 {
				t.Fatalf("err=%v events=%d", err, events)
			}
		})
	}
}
