//go:build linux

package clientsetup

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
)

// TestTerminalLaunchReachesHTTPSThroughTheProxy is opt-in because it needs a
// real X display and xterm. Run it with:
//
//	VSCROXY_TERMINAL_TEST=1 xvfb-run -a go test ./internal/clientsetup \
//	  -run TestTerminalLaunchReachesHTTPSThroughTheProxy -count=1 -v
//
// It starts a capture proxy, opens a real terminal through OpenTerminal, and
// lets that terminal's login shell run curl with the exported environment.
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
		fmt.Fprint(w, "verified terminal setup")
	}))
	defer upstream.Close()

	dir := t.TempDir()
	srv, err := capture.New("127.0.0.1:0", dir)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetSSL(capture.SSLConfig{All: true})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "terminal result.txt")
	home := t.TempDir()
	profile := fmt.Sprintf(`unset NO_PROXY no_proxy
curl -sS --max-time 10 %s > %s 2>&1
status=$?
echo >> %s
echo "curl exit $status" >> %s
exit 0
`, POSIXQuote(upstream.URL), POSIXQuote(out), POSIXQuote(out), POSIXQuote(out))
	for _, name := range []string{".bash_profile", ".profile"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(profile), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("TERMINAL", "xterm")

	command, err := OpenTerminal(Proxy{Addr: srv.Addr(), CAPath: filepath.Join(dir, "mitmproxy-ca-cert.pem")})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("launching: %s", command.String(false))
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		data, err := os.ReadFile(out)
		if err == nil && len(data) > 0 {
			t.Logf("terminal output: %s", data)
			if string(data) != "verified terminal setup\ncurl exit 0\n" {
				t.Fatalf("unexpected terminal output: %q", data)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal produced no result: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
