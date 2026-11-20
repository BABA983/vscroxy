package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"
)

// blockingUpstream is an HTTP upstream proxy that accepts CONNECT but holds
// its response until released, so the dial is known to be in flight when the
// client connection is closed.
type blockingUpstream struct {
	listener     net.Listener
	connectSeen  chan struct{}
	release      chan struct{}
	remoteClosed chan struct{}
	seeOnce      sync.Once
	releaseOnce  sync.Once
	closeOnce    sync.Once

	mu    sync.Mutex
	conns []net.Conn
}

func newBlockingUpstream(t *testing.T) *blockingUpstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	u := &blockingUpstream{
		listener:     ln,
		connectSeen:  make(chan struct{}),
		release:      make(chan struct{}),
		remoteClosed: make(chan struct{}),
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			u.mu.Lock()
			u.conns = append(u.conns, conn)
			u.mu.Unlock()
			go u.handle(conn)
		}
	}()
	t.Cleanup(func() {
		u.unblock()
		_ = ln.Close()
		u.mu.Lock()
		conns := u.conns
		u.mu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
	})
	return u
}

func (u *blockingUpstream) handle(conn net.Conn) {
	defer conn.Close()
	u.seeOnce.Do(func() { close(u.connectSeen) })
	<-u.release
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	// The dialled socket stays open until its owner closes it. This is the
	// evidence that the upstream produced by the late dial was retired.
	_, _ = io.Copy(io.Discard, conn)
	u.closeOnce.Do(func() { close(u.remoteClosed) })
}

func (u *blockingUpstream) unblock() {
	u.releaseOnce.Do(func() { close(u.release) })
}

func (u *blockingUpstream) url() *url.URL {
	return &url.URL{Scheme: "http", Host: u.listener.Addr().String()}
}

func awaitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for " + what)
	}
}

// TestClientCloseRetiresLateServerConn covers the shutdown path where the
// client socket is closed before a dial installs its upstream. The close must
// retire the socket the dial produces even though it did not exist yet.
func TestClientCloseRetiresLateServerConn(t *testing.T) {
	upstream := newBlockingUpstream(t)
	p, err := NewProxy(&Options{Addr: "127.0.0.1:0", CaRootPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	p.SetUpstreamProxy(func(*http.Request, string) ([]UpstreamProxy, error) {
		return []UpstreamProxy{{URL: upstream.url()}}, nil
	})
	t.Cleanup(func() { _ = p.Close() })

	clientConn, proxySide := net.Pipe()
	t.Cleanup(func() { _ = proxySide.Close() })
	wc := newWrapClientConn(clientConn, p)
	connCtx := newConnContext(wc, p)
	wc.connCtx = connCtx

	req, err := http.NewRequestWithContext(
		context.WithValue(t.Context(), connContextKey, connCtx),
		http.MethodGet, "http://vscroxy.test/late-dial", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	p.attacker.initHttpDialFn(req)

	dialDone := make(chan error, 1)
	go func() { dialDone <- connCtx.dialFn(t.Context()) }()
	awaitSignal(t, upstream.connectSeen, "the upstream CONNECT")

	if err := wc.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}
	upstream.unblock()
	select {
	case err := <-dialDone:
		if err != nil {
			t.Fatalf("late dial: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late dial did not return after release")
	}
	t.Cleanup(func() {
		if sc := connCtx.ServerConnection(); sc != nil {
			sc.retire()
		}
	})

	sc := connCtx.ServerConnection()
	if sc == nil {
		t.Fatal("the dial did not publish a server connection")
	}
	wrapped, ok := sc.Conn.(*wrapServerConn)
	if !ok {
		t.Fatalf("dialled upstream is %T, want *wrapServerConn", sc.Conn)
	}
	if !wrapped.isClosed() {
		t.Fatal("the upstream from the late dial was left open")
	}
	awaitSignal(t, upstream.remoteClosed, "the upstream socket to close")
}
