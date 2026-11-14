package capture

import (
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/procinfo"
	"github.com/google/uuid"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// A CONNECT that fails after its client has gone still names the client: the
// accepted socket's process belongs to the exchange, not to the live map.
func TestDisconnectedClientStillNamesItsFailedCONNECT(t *testing.T) {
	s, _ := startServer(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	clientConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	serverConn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()
	client := &proxy.ClientConn{Conn: serverConn}
	addr := socketOf(serverConn)
	s.clients[addr] = clientIdentity{client: client, process: procinfo.Process{Name: "nc", Path: "/usr/bin/nc"}}
	clientConn.Close()
	s.noteClient(client, false)

	req, err := http.NewRequest(http.MethodConnect, "http://127.0.0.1:1", nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &proxy.Flow{
		Id:          uuid.New(),
		Request:     &proxy.Request{Method: http.MethodConnect, URL: req.URL, Header: http.Header{}},
		ConnContext: &proxy.ConnContext{ClientConn: client},
		StartTime:   time.Now(),
	}
	s.publishError(f, errors.New("dial failed"))
	select {
	case flow := <-s.Flows():
		if flow.Process != "nc" {
			t.Fatalf("failed CONNECT process = %q, want nc", flow.Process)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("failed CONNECT published nothing")
	}
}

// A burst of connections shares one lookup pass, and every accepted socket
// gets its own answer from that pass.
func TestClientLookupBatchesAConnectionBurst(t *testing.T) {
	s, err := New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls, looked := 0, 0
	s.lookupBatch = func(endpoints []procinfo.Endpoint) []procinfo.Process {
		mu.Lock()
		calls++
		looked += len(endpoints)
		mu.Unlock()
		out := make([]procinfo.Process, len(endpoints))
		for i := range out {
			out[i] = procinfo.Process{Name: "batch", Path: "/batch"}
		}
		return out
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	clients := make([]net.Conn, 0, 20)
	accepted := make(chan net.Conn, 20)
	go func() {
		for range 20 {
			conn, err := ln.Accept()
			if err == nil {
				accepted <- conn
			}
		}
	}()
	for range 20 {
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, conn)
	}
	defer func() {
		for _, conn := range clients {
			conn.Close()
		}
	}()
	type pending struct {
		client *proxy.ClientConn
		addr   clientSocket
	}
	var all []pending
	for range 20 {
		conn := <-accepted
		defer conn.Close()
		client := &proxy.ClientConn{Conn: conn}
		all = append(all, pending{client: client, addr: socketOf(conn)})
		s.noteClient(client, true)
	}
	for _, p := range all {
		identity, ok := s.waitLookup(p.addr, p.client, time.Second)
		if !ok || identity.process.Name != "batch" {
			t.Fatalf("connection %s = %+v, want the batch answer", p.addr.remote, identity.process)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 || looked != 20 {
		t.Fatalf("lookup calls = %d, endpoints = %d; want one pass over 20", calls, looked)
	}
}

// A connection that arrives while a batch is running waits for the next
// pass; it is not left pending after the first pass ends.
func TestClientLookupDrainsArrivalsDuringAPass(t *testing.T) {
	s, err := New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := 0
	s.lookupBatch = func(endpoints []procinfo.Endpoint) []procinfo.Process {
		mu.Lock()
		calls++
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		out := make([]procinfo.Process, len(endpoints))
		for i := range out {
			out[i] = procinfo.Process{Name: "batch", Path: "/batch"}
		}
		return out
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 20)
	go func() {
		for range 20 {
			conn, err := ln.Accept()
			if err == nil {
				accepted <- conn
			}
		}
	}()
	var all []struct {
		client *proxy.ClientConn
		addr   clientSocket
	}
	add := func(n int) {
		for range n {
			conn, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			server := <-accepted
			defer server.Close()
			client := &proxy.ClientConn{Conn: server}
			all = append(all, struct {
				client *proxy.ClientConn
				addr   clientSocket
			}{client, socketOf(server)})
			s.noteClient(client, true)
		}
	}
	add(10)
	time.Sleep(50 * time.Millisecond)
	add(10)
	for _, p := range all {
		identity, ok := s.waitLookup(p.addr, p.client, time.Second)
		if !ok || identity.process.Name != "batch" {
			t.Fatalf("connection %s = %+v, want the batch answer", p.addr.remote, identity.process)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls < 2 {
		t.Fatalf("lookup calls = %d, want a second pass for the later arrivals", calls)
	}
}
