// Package procinfo tells which local process owns a TCP connection, so the UI
// can show which app a request came from.
package procinfo

import "net/netip"

// Process is the app behind a connection.
type Process struct {
	// Name is the executable's name, as the UI shows it.
	Name string
	// Path is the executable's path, when it could be read; the app's icon
	// comes from it.
	Path string
}

// Lookup returns the owner of the client side of a connection accepted by the
// proxy. Both endpoints are required: an ephemeral port alone is not an identity.
// Only loopback connections are attributed. Remote devices, ambiguous owners,
// and connections whose owner cannot be read return the zero Process.
func Lookup(clientAddr, proxyAddr string) Process {
	client, ok := loopbackEndpoint(clientAddr)
	if !ok {
		return Process{}
	}
	server, ok := loopbackEndpoint(proxyAddr)
	if !ok || client == server {
		return Process{}
	}
	return lookupSocket(client, server)
}

func endpoint(addr string) (netip.AddrPort, error) {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return netip.AddrPort{}, err
	}
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()), nil
}

func loopbackEndpoint(addr string) (netip.AddrPort, bool) {
	ap, err := endpoint(addr)
	return ap, err == nil && ap.Addr().IsLoopback() && ap.Port() != 0
}
