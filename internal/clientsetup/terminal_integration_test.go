//go:build linux

package clientsetup

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
)

// TestTerminalLaunchReachesHTTPSThroughTheProxy is opt-in because it needs a
// real X display, xterm and internet access. Run it with:
//
//	VSCROXY_TERMINAL_TEST=1 xvfb-run -a go test ./internal/clientsetup \
//	  -run TestTerminalLaunchReachesHTTPSThroughTheProxy -count=1 -v
//
// It starts a capture proxy that decrypts only the local test host, opens a
// real terminal through OpenTerminal, and runs curl twice from that terminal:
// once to the decrypted host and once to a public host the proxy tunnels.
// Both must succeed with the system+app CA bundle.
func TestTerminalLaunchReachesHTTPSThroughTheProxy(t *testing.T) {
	if os.Getenv("VSCROXY_TERMINAL_TEST") == "" {
		t.Skip("set VSCROXY_TERMINAL_TEST=1 and run under xvfb-run")
	}
	if os.Getenv("DISPLAY") == "" {
		t.Skip("needs an X display; run under xvfb-run")
	}
	if _, err := exec.LookPath("xterm"); err != nil {
		t.Skip("xterm is not installed")
	}

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "verified decrypted terminal setup")
	}))
	defer upstream.Close()

	dir := t.TempDir()
	caPath := filepath.Join(dir, "mitmproxy-ca-cert.pem")
	srv, err := capture.New("127.0.0.1:0", dir)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetSSL(capture.SSLConfig{Hosts: []string{"127.0.0.1"}})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	bundlePath := filepath.Join(t.TempDir(), "client-ca-bundle.pem")
	if err := EnsureTrustBundle(caPath, bundlePath); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	status := filepath.Join(t.TempDir(), "terminal status.txt")
	decryptedBody := filepath.Join(t.TempDir(), "decrypted body.txt")
	publicBody := filepath.Join(t.TempDir(), "public body.txt")
	profile := fmt.Sprintf(`unset NO_PROXY no_proxy
curl -sS --max-time 20 %s > %s 2>&1
decrypted=$?
curl -sS --max-time 20 https://example.com/ > %s 2>&1
public=$?
echo "decrypted $decrypted" > %s
echo "public $public" >> %s
exit 0
`, POSIXQuote(upstream.URL), POSIXQuote(decryptedBody), POSIXQuote(publicBody), POSIXQuote(status), POSIXQuote(status))
	for _, name := range []string{".bash_profile", ".profile"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(profile), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("TERMINAL", "xterm")

	command, err := OpenTerminal(Proxy{Addr: srv.Addr(), CAPath: caPath, BundlePath: bundlePath})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("launching: %s", command.String(false))
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		data, err := os.ReadFile(status)
		if err == nil && len(data) > 0 {
			t.Logf("terminal status: %s", data)
			decrypted, readErr := os.ReadFile(decryptedBody)
			if readErr != nil {
				t.Fatalf("decrypted body: %v", readErr)
			}
			public, readErr := os.ReadFile(publicBody)
			if readErr != nil {
				t.Fatalf("public body: %v", readErr)
			}
			t.Logf("decrypted body: %s", strings.TrimSpace(string(decrypted)))
			t.Logf("public body: %d bytes", len(public))
			if string(data) != "decrypted 0\npublic 0\n" {
				t.Fatalf("unexpected terminal status: %q", data)
			}
			if !strings.Contains(string(decrypted), "verified decrypted terminal setup") {
				t.Fatalf("unexpected decrypted body: %q", decrypted)
			}
			if len(public) == 0 {
				t.Fatal("public host returned no body")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal produced no result: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
