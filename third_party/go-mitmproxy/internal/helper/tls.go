package helper

import (
	"io"
	"os"
	"sync"

	log "github.com/sirupsen/logrus"
)

// Wireshark 解析 https 设置. The app clears the cached writer when the TLS key
// log setting changes; GetTlsKeyLogWriter also closes a cached writer when the
// variable names another file, so no path change can leave that handle open.
var (
	tlsKeyLogMu     sync.Mutex
	tlsKeyLogWriter io.Writer
	tlsKeyLogPath   string
)

func GetTlsKeyLogWriter() io.Writer {
	tlsKeyLogMu.Lock()
	defer tlsKeyLogMu.Unlock()
	logfile := os.Getenv("SSLKEYLOGFILE")
	if logfile == "" {
		return nil
	}
	if tlsKeyLogWriter != nil {
		if tlsKeyLogPath == logfile {
			return tlsKeyLogWriter
		}
		// The variable changed without a clear. Close the old handle here
		// rather than dropping it when the new writer is cached.
		closeTLSKeyLogWriterLocked()
	}

	writer, err := os.OpenFile(logfile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Debugf("getTlsKeyLogWriter OpenFile error: %v", err)
		return nil
	}

	tlsKeyLogWriter, tlsKeyLogPath = writer, logfile
	return writer
}

// ClearTlsKeyLogWriter closes and forgets the writer cached for
// SSLKEYLOGFILE. New TLS configurations read the environment again; the app
// calls this after changing or unsetting the variable when the key log setting
// changes. GetTlsKeyLogWriter also closes a stale writer if a caller changed
// the variable without clearing.
func ClearTlsKeyLogWriter() {
	tlsKeyLogMu.Lock()
	defer tlsKeyLogMu.Unlock()
	closeTLSKeyLogWriterLocked()
}

// closeTLSKeyLogWriterLocked requires tlsKeyLogMu.
func closeTLSKeyLogWriterLocked() {
	if closer, ok := tlsKeyLogWriter.(io.Closer); ok {
		_ = closer.Close()
	}
	tlsKeyLogWriter, tlsKeyLogPath = nil, ""
}
