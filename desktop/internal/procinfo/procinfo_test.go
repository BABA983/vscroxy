package procinfo

import (
	"net"
	"net/netip"
	"os"
	"testing"
)

func TestLookupRequiresLocalFullSocket(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "[::1]:0"} {
		t.Run(address, func(t *testing.T) {
			ln, err := net.Listen("tcp", address)
			if err != nil {
				t.Skipf("loopback unavailable: %v", err)
			}
			defer ln.Close()
			client, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			server, err := ln.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			remote, local := server.RemoteAddr().String(), server.LocalAddr().String()
			if got := Lookup(remote, local); got.Path != exe || got.Name == "" {
				t.Fatalf("owner = %+v, want executable %q", got, exe)
			}
			clientAP := netip.MustParseAddrPort(remote)
			serverAP := netip.MustParseAddrPort(local)
			wrongServer := netip.AddrPortFrom(serverAP.Addr(), serverAP.Port()%65535+1).String()
			remoteDevice := netip.AddrPortFrom(netip.MustParseAddr("192.0.2.123"), clientAP.Port()).String()
			for _, endpoints := range [][2]string{
				{remote, wrongServer},
				{remoteDevice, local},
				{remote, remoteDevice},
				{"invalid", local},
				{remote, "invalid"},
				{"127.0.0.1:0", local},
				{remote, remote},
			} {
				if got := Lookup(endpoints[0], endpoints[1]); got != (Process{}) {
					t.Errorf("Lookup(%q, %q) = %+v, want unidentified", endpoints[0], endpoints[1], got)
				}
			}
		})
	}
}
