package helper

import (
	"io"
	"os"
	"sync"

	log "github.com/sirupsen/logrus"
)

// Wireshark 解析 https 设置. The app clears the cached writer whenever the TLS
// key log setting changes, so a handshake cannot keep writing secrets to a
// file the setting no longer names.
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
	if tlsKeyLogWriter != nil && tlsKeyLogPath == logfile {
		return tlsKeyLogWriter
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
// calls this before changing or unsetting the variable when the key log
// setting changes.
func ClearTlsKeyLogWriter() {
	tlsKeyLogMu.Lock()
	defer tlsKeyLogMu.Unlock()
	if closer, ok := tlsKeyLogWriter.(io.Closer); ok {
		_ = closer.Close()
	}
	tlsKeyLogWriter, tlsKeyLogPath = nil, ""
}
