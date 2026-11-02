package capture

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
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
	resp, err := client.Get(dead)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	f := waitFlow(t, s)
	if f.Status != 502 || f.URL != dead || f.Conn.Error == "" {
		t.Fatalf("capture lost failed-request diagnostics: %+v", f)
	}
	slog.Warn("application-diagnostic-retained")
}
