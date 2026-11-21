package proxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// proxyConn starts a listener for one proxy connection and returns the client
// side plus the handler's result. The handler owns the server connection.
func proxyConn(t *testing.T, handler func(net.Conn, *bufio.Reader) error) (net.Conn, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		done <- handler(conn, bufio.NewReader(conn))
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, done
}

// TestProxyAuthTransportLeavesTheOriginResponseUnbounded proves the handshake
// bound covers the Type 1 challenge rounds only. The fake proxy answers the
// Type 3 request after a channel release, longer than the injected bound, and
// the response still arrives.
func TestProxyAuthTransportLeavesTheOriginResponseUnbounded(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	gotType3 := make(chan struct{})
	release := make(chan struct{})
	proxyErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			proxyErr <- err
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		first, err := http.ReadRequest(br)
		if err != nil {
			proxyErr <- err
			return
		}
		if got := first.Header.Get("Proxy-Authorization"); !strings.HasPrefix(got, "NTLM ") {
			proxyErr <- fmt.Errorf("first attempt has %q, want a Type 1 header", got)
			return
		}
		_, _ = io.Copy(io.Discard, first.Body)
		_, _ = io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: NTLM TlRMTVNTUAACAAAA\r\nContent-Length: 0\r\n\r\n")
		second, err := http.ReadRequest(br)
		if err != nil {
			proxyErr <- err
			return
		}
		if got := second.Header.Get("Proxy-Authorization"); !strings.HasPrefix(got, "NTLM ") {
			proxyErr <- fmt.Errorf("second attempt has %q, want a Type 3 header", got)
			return
		}
		_, _ = io.Copy(io.Discard, second.Body)
		close(gotType3)
		<-release
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nslow")
		proxyErr <- nil
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	tr := &proxyAuthTransport{
		auth: &ProxyAuth{
			Initial: "NTLM type1",
			Answer: func([]string) (string, bool, error) {
				return "NTLM type3", true, nil
			},
		},
		first: conn,
		// The Type 3 response must not see this bound. If it does, the
		// canceled context fails the attempt before the fake proxy answers.
		handshakeContext: func(ctx context.Context) (context.Context, context.CancelFunc) {
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			return canceled, func() {}
		},
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://origin.example/", nil)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		res *http.Response
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := tr.RoundTrip(req)
		done <- result{res: res, err: err}
	}()

	select {
	case <-gotType3:
	case got := <-done:
		t.Fatalf("RoundTrip failed before the Type 3 request: %v", got.err)
	case <-time.After(5 * time.Second):
		t.Fatal("the fake proxy never received the Type 3 request")
	}
	close(release)

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("RoundTrip failed after the handshake: %v", got.err)
		}
		defer got.res.Body.Close()
		if got.res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", got.res.StatusCode)
		}
		body, err := io.ReadAll(got.res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "slow" {
			t.Fatalf("body = %q, want the origin's delayed response", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RoundTrip did not return the origin's response")
	}
	if err := <-proxyErr; err != nil {
		t.Fatal(err)
	}
}

// TestProxyAuthTransportRejectsABodyOverTheLimit checks that an oversized
// plain-HTTP body fails before anything reaches the proxy, so the origin can
// never receive a truncated or empty body.
func TestProxyAuthTransportRejectsABodyOverTheLimit(t *testing.T) {
	seen := make(chan bool, 1)
	conn, done := proxyConn(t, func(conn net.Conn, br *bufio.Reader) error {
		req, err := http.ReadRequest(br)
		if err != nil {
			seen <- false
			return nil
		}
		seen <- true
		_, _ = io.Copy(io.Discard, req.Body)
		return nil
	})
	tr := &proxyAuthTransport{
		auth: &ProxyAuth{
			Initial: "NTLM type1",
			Answer: func([]string) (string, bool, error) {
				return "", false, fmt.Errorf("the proxy must not be asked to authenticate")
			},
		},
		first: conn,
	}
	body := bytes.Repeat([]byte("x"), MaxProxyAuthBody+1)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://origin.example/upload", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tr.RoundTrip(req)
	if err == nil {
		t.Fatal("RoundTrip accepted a body over the limit")
	}
	for _, want := range []string{"8 MiB", "HTTPS"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
	select {
	case saw := <-seen:
		if saw {
			t.Fatal("the over-limit request reached the proxy")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the proxy connection did not end")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestProxyAuthTransportResendsTheWholeBody checks that a body at the limit is
// buffered and delivered intact after the Type 3 handshake.
func TestProxyAuthTransportResendsTheWholeBody(t *testing.T) {
	type bodies struct {
		first, second []byte
		err           error
	}
	result := make(chan bodies, 1)
	conn, done := proxyConn(t, func(conn net.Conn, br *bufio.Reader) error {
		first, err := http.ReadRequest(br)
		if err != nil {
			result <- bodies{err: err}
			return nil
		}
		firstBody, err := io.ReadAll(first.Body)
		if err != nil {
			result <- bodies{err: err}
			return nil
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: NTLM TlRMTVNTUAACAAAA\r\nContent-Length: 0\r\n\r\n")
		second, err := http.ReadRequest(br)
		if err != nil {
			result <- bodies{err: err}
			return nil
		}
		secondBody, err := io.ReadAll(second.Body)
		if err != nil {
			result <- bodies{err: err}
			return nil
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
		result <- bodies{first: firstBody, second: secondBody}
		return nil
	})
	tr := &proxyAuthTransport{
		auth: &ProxyAuth{
			Initial: "NTLM type1",
			Answer: func([]string) (string, bool, error) {
				return "NTLM type3", true, nil
			},
		},
		first: conn,
	}
	body := bytes.Repeat([]byte("y"), MaxProxyAuthBody)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://origin.example/upload", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ok" {
		t.Fatalf("response body = %q", got)
	}
	select {
	case sent := <-result:
		if sent.err != nil {
			t.Fatal(sent.err)
		}
		if !bytes.Equal(sent.first, body) {
			t.Fatalf("the Type 1 body has %d bytes, want %d", len(sent.first), len(body))
		}
		if !bytes.Equal(sent.second, body) {
			t.Fatalf("the Type 3 body has %d bytes, want %d", len(sent.second), len(body))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the fake proxy did not receive both attempts")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
