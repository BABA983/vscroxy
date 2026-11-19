package capture

import (
	"io"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// ClearTLSKeyLogWriter closes the TLS key-log writer the engine cached for
// SSLKEYLOGFILE. Turning the setting off must stop new handshakes from
// writing secrets to the old file, not only change what the next launch does.
func ClearTLSKeyLogWriter() { proxy.ClearTLSKeyLogWriter() }

// TLSKeyLogWriter exposes the engine's cached writer to the reset contract
// test, so it can prove the old file handle is closed.
func TLSKeyLogWriter() io.Writer { return proxy.TLSKeyLogWriter() }
