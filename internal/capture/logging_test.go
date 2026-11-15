package capture

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// A fresh process observes the production stderr policy without replacing a
// global logger's output while proxy workers are still using it.
func TestProxyErrorsStayInCaptureNotDiagnosticLogs(t *testing.T) {
	if os.Getenv("VSCROXY_LOG_PRIVACY_TEST") != "1" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestProxyErrorsStayInCaptureNotDiagnosticLogs$")
		cmd.Env = append(os.Environ(), "VSCROXY_LOG_PRIVACY_TEST=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("proxy scenario failed: %v\n%s", err, output)
		}
		if !strings.Contains(string(output), "application-diagnostic-retained") {
			t.Fatalf("application logs were suppressed: %s", output)
		}
		for _, private := range []string{"private-query", "private-path"} {
			if strings.Contains(string(output), private) {
				t.Fatalf("traffic escaped into diagnostic output: %s", output)
			}
		}
		return
	}
	s, _ := startServer(t)
	s.proxy.Addons = append(s.proxy.Addons, &proxy.LogAddon{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close() // fail after receiving the URL, inside attacker.attack
	}))
	defer upstream.Close()
	dead := upstream.URL + "/private-path?token=private-query"
	client := clientFor(t, s, nil)
	defer client.CloseIdleConnections()
	// A pinned upstream that dies ends the client connection without a
	// response, so the client may see a transport error instead of a 502.
	resp, err := client.Get(dead)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	f := waitFlow(t, s)
	if f.Status != 502 || f.URL != dead || f.Conn.Error == "" {
		t.Fatalf("capture lost failed-request diagnostics: %+v", f)
	}
	slog.Warn("application-diagnostic-retained")
}

// The engine's optional instance logger must not record request content
// either. It still records that the exchange happened, without the URL,
// headers or body.
func TestEngineInstanceLogNeverContainsRequestContent(t *testing.T) {
	s, _ := startServer(t)
	logPath := filepath.Join(t.TempDir(), "engine.log")
	s.proxy.Addons = append([]proxy.Addon{proxy.NewInstanceLogAddonWithFile(s.Addr(), "privacy", logPath)}, s.proxy.Addons...)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	req, err := http.NewRequest(http.MethodPost, upstream.URL+"/private-path?token=private-query", strings.NewReader("private-body"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer private-header")
	client := clientFor(t, s, nil)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	waitFlow(t, s)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logged := string(data)
	if !strings.Contains(logged, "request_completed") {
		t.Fatalf("instance log recorded no completion event: %s", logged)
	}
	for _, private := range []string{"private-path", "private-query", "private-header", "private-body"} {
		if strings.Contains(logged, private) {
			t.Fatalf("request content %q reached the engine log: %s", private, logged)
		}
	}
}
