// Package procinfo tells which local process owns a TCP connection, so the UI
// can show which app a request came from.
package procinfo

import "net"

// Process is the app behind a connection.
type Process struct {
	// Name is the executable's name, as the UI shows it.
	Name string
	// Path is the executable's path, when it could be read; the app's icon
	// comes from it.
	Path string
}

// Lookup returns the process that owns the connection whose remote address is
// addr, such as "127.0.0.1:52344". It returns the zero Process when the
// connection is not local or the process cannot be read.
func Lookup(addr string) Process {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return Process{}
	}
	return lookupPort(port)
}
