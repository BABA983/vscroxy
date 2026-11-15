//go:build !windows

package request

import (
	"context"
	"net"
)

// dialSocket opens a Unix domain socket. Linux and macOS are the platforms
// that have one.
func dialSocket(ctx context.Context, path string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", path)
}
