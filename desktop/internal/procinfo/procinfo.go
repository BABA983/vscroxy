// Package procinfo tells which local process owns a TCP connection, so the UI
// can show which app a request came from.
package procinfo

import "net"

// Lookup returns the name of the local process that owns the connection whose
// remote address is addr, such as "127.0.0.1:52344". It returns "" when the
// connection is not local or the process cannot be read.
func Lookup(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return lookupPort(port)
}
