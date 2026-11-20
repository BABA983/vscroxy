package dnsmap

import (
	"errors"
	"net"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/baba983/vscroxy/internal/hosts"
)

// newUDPPorts holds distinct ports for the scripted binds. Keeping them all
// bound until the hooks are installed makes each attempt a different port.
func newUDPPorts(t *testing.T, n int) []*net.UDPConn {
	t.Helper()
	ports := make([]*net.UDPConn, 0, n)
	for range n {
		conn, err := listenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatalf("reserve UDP port: %v", err)
		}
		ports = append(ports, conn)
	}
	t.Cleanup(func() {
		for _, conn := range ports {
			_ = conn.Close()
		}
	})
	return ports
}

func hookListens(t *testing.T, udp func(string, *net.UDPAddr) (*net.UDPConn, error), tcp func(string) (net.Listener, error)) {
	t.Helper()
	oldUDP, oldTCP := listenUDP, listenTCP
	listenUDP, listenTCP = udp, tcp
	t.Cleanup(func() { listenUDP, listenTCP = oldUDP, oldTCP })
}

func assertUDPClosed(t *testing.T, conn *net.UDPConn) {
	t.Helper()
	_, err := conn.WriteToUDP(nil, conn.LocalAddr().(*net.UDPAddr))
	if !errors.Is(err, net.ErrClosed) {
		t.Errorf("abandoned UDP socket is still open: write = %v, want net.ErrClosed", err)
	}
}

// TestStartRetriesTCPBindOnANewUDPPort holds the first port's TCP counterpart
// through the listener hook. Start must close that UDP socket, draw another
// port and serve both protocols there; it must not run as a UDP-only server.
func TestStartRetriesTCPBindOnANewUDPPort(t *testing.T) {
	ports := newUDPPorts(t, 2)
	// The hook returns a listener already bound to a free port, so the second
	// attempt cannot fail because something else holds the chosen TCP port.
	secondTCP, err := listenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve TCP listener: %v", err)
	}
	t.Cleanup(func() { _ = secondTCP.Close() })
	held := errors.New("TCP port held")
	var udpCalls, tcpCalls int
	hookListens(t,
		func(network string, addr *net.UDPAddr) (*net.UDPConn, error) {
			if udpCalls >= len(ports) {
				return nil, errors.New("unexpected extra UDP bind")
			}
			conn := ports[udpCalls]
			udpCalls++
			return conn, nil
		},
		func(addr string) (net.Listener, error) {
			tcpCalls++
			if tcpCalls == 1 {
				return nil, held
			}
			if addr != ports[tcpCalls-1].LocalAddr().String() {
				t.Fatalf("TCP bind %d on %q, want the second UDP port %q", tcpCalls, addr, ports[1].LocalAddr())
			}
			return secondTCP, nil
		},
	)

	s, err := Start(net.DefaultResolver)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if udpCalls != 2 || tcpCalls != 2 {
		t.Fatalf("UDP binds = %d, TCP binds = %d; want two of each", udpCalls, tcpCalls)
	}
	if s.udp != ports[1] {
		t.Fatal("the server kept the UDP socket whose TCP port was held")
	}
	assertUDPClosed(t, ports[0])

	rules, _ := hosts.Parse("10.9.8.7 retried.test")
	s.SetRules(rules)
	if ip := ask(t, s.Addr(), "retried.test", dnsmessage.TypeA); ip != "10.9.8.7" {
		t.Fatalf("UDP answer = %q, want the rule's address", ip)
	}
	conn, err := net.Dial("tcp", s.tcp.Addr().String())
	if err != nil {
		t.Fatalf("dial the retried TCP listener: %v", err)
	}
	defer conn.Close()
	writeTCPQuery(t, conn, "retried.test")
	if answer := readTCPAnswer(t, conn); answer == 0 {
		t.Fatal("the TCP query got an empty answer")
	}
}

// TestStartFailsWhenNoUDPPortHasTCP covers the other end of the bound: if
// every draw has its TCP port taken, Start reports the failure and leaves no
// socket behind.
func TestStartFailsWhenNoUDPPortHasTCP(t *testing.T) {
	ports := newUDPPorts(t, maxBindAttempts)
	held := errors.New("TCP port held")
	var udpCalls, tcpCalls int
	hookListens(t,
		func(network string, addr *net.UDPAddr) (*net.UDPConn, error) {
			if udpCalls >= len(ports) {
				return nil, errors.New("unexpected extra UDP bind")
			}
			conn := ports[udpCalls]
			udpCalls++
			return conn, nil
		},
		func(addr string) (net.Listener, error) {
			if tcpCalls >= len(ports) {
				return nil, errors.New("unexpected extra TCP bind")
			}
			if addr != ports[tcpCalls].LocalAddr().String() {
				t.Fatalf("TCP bind %d on %q, want UDP port %q", tcpCalls+1, addr, ports[tcpCalls].LocalAddr())
			}
			tcpCalls++
			return nil, held
		},
	)

	s, err := Start(net.DefaultResolver)
	if s != nil {
		defer func() { _ = s.Close() }()
	}
	if !errors.Is(err, held) {
		t.Fatalf("start error = %v, want the TCP bind error", err)
	}
	if s != nil {
		t.Fatal("Start returned a UDP-only server after every TCP bind failed")
	}
	if udpCalls != maxBindAttempts || tcpCalls != maxBindAttempts {
		t.Fatalf("UDP binds = %d, TCP binds = %d; want %d of each", udpCalls, tcpCalls, maxBindAttempts)
	}
	for i, conn := range ports {
		if _, err := conn.WriteToUDP(nil, conn.LocalAddr().(*net.UDPAddr)); !errors.Is(err, net.ErrClosed) {
			t.Errorf("UDP socket %d is still open: write = %v, want net.ErrClosed", i, err)
		}
	}
}
