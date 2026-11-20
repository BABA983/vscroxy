package capture

import (
	"io"
	"os"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// SetTLSKeyLogPath changes the file new TLS handshakes write their secrets to.
// An empty path turns the log off. The engine caches one writer for
// SSLKEYLOGFILE, so the old writer must be closed and forgotten before the
// environment changes: otherwise it is never closed and the file the setting
// no longer names stays open.
func SetTLSKeyLogPath(path string) {
	ClearTLSKeyLogWriter()
	if path == "" {
		_ = os.Unsetenv("SSLKEYLOGFILE")
	} else {
		_ = os.Setenv("SSLKEYLOGFILE", path)
	}
}

// ClearTLSKeyLogWriter closes the TLS key-log writer the engine cached for
// SSLKEYLOGFILE. A setting change must stop new handshakes from writing
// secrets to the old file, not only change what the next launch does.
func ClearTLSKeyLogWriter() { proxy.ClearTLSKeyLogWriter() }

// TLSKeyLogWriter exposes the engine's cached writer to the path-change
// contract test, so it can prove the old file handle is closed.
func TLSKeyLogWriter() io.Writer { return proxy.TLSKeyLogWriter() }
