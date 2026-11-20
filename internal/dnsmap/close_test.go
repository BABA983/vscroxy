package dnsmap

import (
	"context"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/baba983/vscroxy/internal/hosts"
)

// TestCloseJoinsServerGoroutines covers all three goroutine lifetimes: the
// listen loops, a TCP handler parked on its next query, and a UDP query whose
// upstream lookup is still running. Close must unblock each of them and return
// only after all of them have exited.
func TestCloseJoinsServerGoroutines(t *testing.T) {
	lookupStarted := make(chan struct{})
	var once sync.Once
	upstream := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			once.Do(func() { close(lookupStarted) })
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	s, err := Start(upstream)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if rules, err := hosts.Parse("10.9.8.7 held-tcp.test"); err != nil {
		t.Fatalf("rules: %v", err)
	} else {
		s.SetRules(rules)
	}

	// The answer proves the handler reached its loop; leaving the connection
	// open parks it on the next length prefix until Close closes the socket.
	tcp, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial tcp: %v", err)
	}
	// Keep the peer alive until the test ends; otherwise its finalizer can
	// close the socket before Close and hide a missing handler join.
	defer tcp.Close()
	writeTCPQuery(t, tcp, "held-tcp.test")
	if answer := readTCPAnswer(t, tcp); answer == 0 {
		t.Fatal("the TCP query got an empty answer")
	}

	// The blocking resolver keeps a per-query goroutine in flight. Its dial
	// signals that the query reached answer before Close begins.
	udp, err := net.Dial("udp", s.Addr())
	if err != nil {
		t.Fatalf("dial udp: %v", err)
	}
	defer udp.Close()
	if _, err := udp.Write(dnsQuery(t, "held-udp.test")); err != nil {
		t.Fatalf("write udp query: %v", err)
	}
	select {
	case <-lookupStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream lookup did not start before Close")
	}

	closed := make(chan struct{})
	closeErr := make(chan error, 1)
	go func() {
		defer close(closed)
		closeErr <- s.Close()
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatalf("Close did not return with a TCP handler and a UDP query in flight:\n%s", dnsmapStacks())
	}
	if err := <-closeErr; err != nil {
		t.Fatalf("close: %v", err)
	}
	assertNoDnsmapGoroutines(t)

	// Close is idempotent, and a connection arriving afterwards is refused.
	closeAgain := make(chan error, 1)
	go func() { closeAgain <- s.Close() }()
	select {
	case err := <-closeAgain:
		if err != nil {
			t.Fatalf("second close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second Close did not return")
	}
	if conn, err := net.DialTimeout("tcp", s.Addr(), time.Second); err == nil {
		_ = conn.Close()
		t.Fatal("a TCP connection was accepted after Close")
	}
}

func dnsQuery(t *testing.T, name string) []byte {
	t.Helper()
	q := dnsmessage.NewBuilder(nil, dnsmessage.Header{RecursionDesired: true})
	q.EnableCompression()
	if err := q.StartQuestions(); err != nil {
		t.Fatalf("questions: %v", err)
	}
	if err := q.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name + "."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}); err != nil {
		t.Fatalf("question: %v", err)
	}
	msg, err := q.Finish()
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	return msg
}

func writeTCPQuery(t *testing.T, conn net.Conn, name string) {
	t.Helper()
	query := dnsQuery(t, name)
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	if _, err := conn.Write([]byte{byte(len(query) >> 8), byte(len(query))}); err != nil {
		t.Fatalf("write query length: %v", err)
	}
	if _, err := conn.Write(query); err != nil {
		t.Fatalf("write query: %v", err)
	}
}

// readTCPAnswer returns the answer's size, leaving the connection open.
func readTCPAnswer(t *testing.T, conn net.Conn) int {
	t.Helper()
	var size [2]byte
	if _, err := io.ReadFull(conn, size[:]); err != nil {
		t.Fatalf("read answer length: %v", err)
	}
	n := int(size[0])<<8 | int(size[1])
	if n == 0 {
		return 0
	}
	if _, err := io.ReadFull(conn, make([]byte, n)); err != nil {
		t.Fatalf("read answer: %v", err)
	}
	return n
}

// assertNoDnsmapGoroutines checks the package's own stacks as well as the
// leakcheck.TestMain, so a missing join is reported at the contract test.
func assertNoDnsmapGoroutines(t *testing.T) {
	t.Helper()
	// Server method frames identify this package's workers; the test
	// goroutine itself is also in the package and must not count.
	const prefix = "internal/dnsmap.(*Server)."
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		stacks := string(buf[:n])
		if !strings.Contains(stacks, prefix) {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("dnsmap goroutine still running after Close:\n%s", stacks)
		}
	}
}

func dnsmapStacks() string {
	buf := make([]byte, 1<<20)
	return string(buf[:runtime.Stack(buf, true)])
}
