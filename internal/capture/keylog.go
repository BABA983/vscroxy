package capture

import (
	"io"
	"os"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// SetTLSKeyLogPath changes the file new TLS handshakes write their secrets to.
// An empty path turns the log off. Change SSLKEYLOGFILE before clearing the
// engine's cached writer: a handshake between the two steps must not read the
// old path and reopen the file the setting no longer names.
func SetTLSKeyLogPath(path string) {
	if path == "" {
		_ = os.Unsetenv("SSLKEYLOGFILE")
	} else {
		_ = os.Setenv("SSLKEYLOGFILE", path)
	}
	ClearTLSKeyLogWriter()
}

// ClearTLSKeyLogWriter closes the TLS key-log writer the engine cached for
// SSLKEYLOGFILE. A setting change must stop new handshakes from writing
// secrets to the old file, not only change what the next launch does.
func ClearTLSKeyLogWriter() { proxy.ClearTLSKeyLogWriter() }

// TLSKeyLogWriter exposes the engine's cached writer to the path-change
// contract test, so it can prove the old file handle is closed.
func TLSKeyLogWriter() io.Writer { return proxy.TLSKeyLogWriter() }
