package capture

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Written through the API the app uses: switching the path must close the
// writer a handshake already got, and later handshakes must use the new file.
func TestTLSKeyLogPathChangeClosesOldWriter(t *testing.T) {
	t.Setenv("SSLKEYLOGFILE", "")
	ClearTLSKeyLogWriter()
	t.Cleanup(ClearTLSKeyLogWriter)

	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first.log")
	secondPath := filepath.Join(dir, "second.log")
	SetTLSKeyLogPath(firstPath)
	oldWriter := TLSKeyLogWriter()
	if oldWriter == nil {
		t.Fatal("the first key-log writer was not opened")
	}

	handshake := func(writer io.Writer) {
		t.Helper()
		upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, "ok")
		}))
		defer upstream.Close()
		client := upstream.Client()
		transport := client.Transport.(*http.Transport)
		config := transport.TLSClientConfig.Clone()
		config.KeyLogWriter = writer
		transport.TLSClientConfig = config
		resp, err := client.Get(upstream.URL)
		if err != nil {
			t.Fatalf("TLS handshake: %v", err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || string(body) != "ok" {
			t.Fatalf("TLS request = %d %q, %v", resp.StatusCode, body, err)
		}
		client.CloseIdleConnections()
	}

	handshake(oldWriter)
	first, err := os.ReadFile(firstPath)
	if err != nil || len(bytes.TrimSpace(first)) == 0 {
		t.Fatalf("first handshake wrote no TLS secrets to %s: %q, %v", firstPath, first, err)
	}

	SetTLSKeyLogPath(secondPath)
	if _, err := oldWriter.Write([]byte("after switch\n")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("old key-log writer after path change = %v, want a closed file", err)
	}

	newWriter := TLSKeyLogWriter()
	if newWriter == nil || newWriter == oldWriter {
		t.Fatalf("new handshakes did not get a new key-log writer: %v", newWriter)
	}
	handshake(newWriter)
	after, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, first) {
		t.Fatalf("old key log gained secrets after the path change: %q -> %q", first, after)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil || len(bytes.TrimSpace(second)) == 0 {
		t.Fatalf("new handshake wrote no TLS secrets to %s: %q, %v", secondPath, second, err)
	}
}
