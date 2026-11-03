package capture

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baba983/vscroxy/internal/rules"
)

func TestCodeMockAnswersWithoutUpstreamConnection(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint("https=", secure), func(t *testing.T) {
			var connections atomic.Int64
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, "real upstream")
			}))
			upstream.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			}
			if secure {
				upstream.StartTLS()
			} else {
				upstream.Start()
			}
			defer upstream.Close()
			s, ca := startServer(t)
			s.SetSSL(SSLConfig{All: true})
			s.SetScriptRunner(fakeScripts{"mock.js": {func(x *ScriptExchange) error {
				if x.Phase != "mock" || string(x.Body) != "input" {
					return errors.New("wrong hook or incomplete input")
				}
				x.Status, x.ResponseBody = 207, []byte(`{"mock":true}`)
				x.Response = http.Header{"Content-Type": {"application/json"}, "Set-Cookie": {"a=1", "b=2"},
					"Connection": {"X-Private"}, "X-Private": {"drop"}, "Content-Length": {"999"}, "Content-Encoding": {"gzip"}}
				return nil
			}, nil}})
			mock := Rule{Type: RuleCodeMock, Match: upstream.URL + "/mock", Target: "mock.js", Method: "POST",
				When: []rules.HeaderCondition{{Name: "X-Mode", Op: rules.HeaderPresent}}}
			if err := s.SetRules([]Rule{mock, {Type: RuleBlock, Match: "/mock"}}); err != nil {
				t.Fatal(err)
			}
			client := clientFor(t, s, sslTestRoots(t, ca))
			defer client.CloseIdleConnections()
			req, _ := http.NewRequestWithContext(t.Context(), "POST", upstream.URL+"/mock", strings.NewReader("input"))
			req.Header.Set("X-Mode", "mock")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 207 || string(body) != `{"mock":true}` || len(resp.Cookies()) != 2 ||
				resp.Header.Get("X-Private") != "" || resp.Header.Get("Content-Encoding") != "" || resp.ContentLength != int64(len(body)) {
				t.Fatalf("mock response = %d %q %v, %v", resp.StatusCode, body, resp.Header, err)
			}
			if connections.Load() != 0 {
				t.Fatalf("mock opened %d upstream connections (including TLS pre-dials)", connections.Load())
			}
			if f := waitFlow(t, s); f.Status != 207 || f.ResponseBody != string(body) || !strings.Contains(strings.Join(f.Actions, "\n"), "Code Mock") {
				t.Fatalf("captured mock = %+v", f)
			}
			// A later rule cannot run, and the method/header mismatch cannot
			// invoke the mock. Block also must not contact the original server.
			resp, err = client.Get(upstream.URL + "/mock")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 403 || connections.Load() != 0 {
				t.Fatalf("first matching response rule lost: status=%d connections=%d", resp.StatusCode, connections.Load())
			}
			resp, err = client.Get(upstream.URL + "/other")
			if err != nil {
				t.Fatal(err)
			}
			body, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != "real upstream" || connections.Load() == 0 {
				t.Fatalf("unmatched request did not reach upstream: %q", body)
			}
		})
	}
}

func TestCodeMockRejectsInvalidResponsesLocally(t *testing.T) {
	s, _ := startServer(t)
	for _, tc := range []struct {
		name string
		run  func(*ScriptExchange) error
	}{
		{"exception", func(x *ScriptExchange) error { return errors.New("private request token") }},
		{"no response", func(x *ScriptExchange) error { return nil }},
		{"upgrade", func(x *ScriptExchange) error { x.Status = 101; return nil }},
		{"out of range", func(x *ScriptExchange) error { x.Status = 600; return nil }},
		{"oversized body", func(x *ScriptExchange) error { x.Status = 200; x.ResponseBody = make([]byte, (1<<20)+1); return nil }},
		{"invalid header", func(x *ScriptExchange) error {
			x.Status = 200
			x.Response = http.Header{"X-Test": {"a\r\nb: c"}}
			return nil
		}},
		{"invalid name", func(x *ScriptExchange) error { x.Status = 200; x.Response = http.Header{"X Test": {"a"}}; return nil }},
		{"oversized headers", func(x *ScriptExchange) error {
			x.Status = 200
			x.Response = http.Header{"X-Test": {strings.Repeat("x", 64<<10)}}
			return nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.SetScriptRunner(fakeScripts{"mock.js": {tc.run, nil}})
			if err := s.SetRules([]Rule{{Type: RuleCodeMock, Match: "/", Target: "mock.js"}}); err != nil {
				t.Fatal(err)
			}
			// A closed loopback port makes any accidental fallback fail the
			// body assertion rather than depending on external DNS/network.
			resp, err := clientFor(t, s, nil).Get("http://127.0.0.1:1/mock")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 502 || !strings.HasPrefix(string(body), "code mock:") || strings.Contains(string(body), "private request token") {
				t.Fatalf("invalid output = %d %q", resp.StatusCode, body)
			}
			waitFlow(t, s)
		})
	}
	s.SetScriptRunner(nil)
	resp, err := clientFor(t, s, nil).Get("http://127.0.0.1:1/mock")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 502 || !strings.Contains(string(body), "script unavailable") || s.ScriptErrors() != 9 {
		t.Fatalf("missing runner = %d %q, errors=%d", resp.StatusCode, body, s.ScriptErrors())
	}
}

func TestCodeMockRefusesUnreadableInputWithoutForwarding(t *testing.T) {
	s := startServerWithScripts(t, fakeScripts{"mock.js": {func(x *ScriptExchange) error {
		t.Error("mock ran on unavailable input")
		return nil
	}, nil}}, Rule{Type: RuleCodeMock, Match: "/", Target: "mock.js"})
	for _, tc := range []struct {
		name, body, encoding, contentType string
		status                            int
	}{
		{"binary", "\x00", "", "", 415},
		{"compressed", "compressed", "gzip", "", 415},
		{"script limit", strings.Repeat("x", (1<<20)+1), "", "", 413},
		{"engine stream", strings.Repeat("x", (8<<20)+1), "", "", 413},
		{"grpc stream", "frames", "", "application/grpc", 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequestWithContext(t.Context(), "POST", "http://127.0.0.1:1/mock", strings.NewReader(tc.body))
			req.Header.Set("Content-Encoding", tc.encoding)
			req.Header.Set("Content-Type", tc.contentType)
			resp, err := clientFor(t, s, nil).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != tc.status || !strings.HasPrefix(string(body), "code mock:") {
				t.Fatalf("unreadable input = %d %q", resp.StatusCode, body)
			}
			waitFlow(t, s)
		})
	}
}

func TestCodeMockBodylessStatusesAndHEAD(t *testing.T) {
	s, _ := startServer(t)
	for _, status := range []int{200, 204, 205, 304} {
		for _, method := range []string{"GET", "HEAD"} {
			t.Run(fmt.Sprintf("%s/%d", method, status), func(t *testing.T) {
				s.SetScriptRunner(fakeScripts{"mock.js": {func(x *ScriptExchange) error {
					x.Status, x.ResponseBody = status, []byte("body")
					return nil
				}, nil}})
				if err := s.SetRules([]Rule{{Type: RuleCodeMock, Match: "/", Target: "mock.js"}}); err != nil {
					t.Fatal(err)
				}
				req, _ := http.NewRequestWithContext(t.Context(), method, "http://127.0.0.1:1/mock", nil)
				resp, err := clientFor(t, s, nil).Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				want := ""
				if method == "GET" && status == 200 {
					want = "body"
				}
				if err != nil || string(body) != want || resp.StatusCode != status {
					t.Fatalf("got %d %q, %v", resp.StatusCode, body, err)
				}
				waitFlow(t, s)
			})
		}
	}
}
