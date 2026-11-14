package capture

import (
	"errors"
	"net"
	"net/http"
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
