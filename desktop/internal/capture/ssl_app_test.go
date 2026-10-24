package capture

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json/v2"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

func TestSSLAppRulesAndPersistence(t *testing.T) {
	c := SSLConfig{Apps: []SSLApp{{Name: "Browser", Path: "/apps/Browser"}, {Name: "Name only"}}}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/apps/Browser", true},
		{"/other/Browser", false},
		{"/apps/Browser Helper", false},
		{"Browser", false},
		{"", false},
	} {
		if got := c.DecryptsProcess("unlisted.example:9443", tc.path); got != tc.want {
			t.Errorf("path %q: got %v, want %v", tc.path, got, tc.want)
		}
	}
	if c.Decrypts("unlisted.example") {
		t.Fatal("app rules changed host-only decisions")
	}
	c.Hosts = []string{"example.com:8443"}
	if !c.DecryptsProcess("api.example.com:9443", "") {
		t.Fatal("domain rule did not apply to unidentified source")
	}
	c.All = true
	if !c.DecryptsProcess("unlisted.example", "") {
		t.Fatal("All did not apply to unidentified source")
	}

	file := filepath.Join(t.TempDir(), "ssl.json")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSSL(file)
	if err != nil || !reflect.DeepEqual(loaded, c) {
		t.Fatalf("load = %+v, %v; want %+v", loaded, err, c)
	}
	for _, legacy := range []string{`{"all":false}`, `{"all":true}`, `{"all":false,"hosts":["example.com:443"]}`} {
		if err := os.WriteFile(file, []byte(legacy), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadSSL(file)
		if err != nil || len(loaded.Apps) != 0 || loaded.DecryptsProcess("example.com", "") != loaded.Decrypts("example.com") {
			t.Fatalf("legacy %s: %+v, %v", legacy, loaded, err)
		}
		encoded, err := json.Marshal(loaded)
		if err != nil || string(encoded) != legacy {
			t.Fatalf("legacy round trip = %s, %v; want %s", encoded, err, legacy)
		}
	}
	var s Server
	s.SetSSL(c)
	c.Apps[0].Path = "/mutated/input"
	c.Hosts[0] = "mutated.example"
	got := s.SSL()
	if got.Apps[0].Path != "/apps/Browser" || got.Hosts[0] != "example.com:8443" {
		t.Fatalf("SetSSL retained input slices: %+v", got)
	}
	got.Apps[0].Path = "/mutated/output"
	if s.SSL().Apps[0].Path != "/apps/Browser" {
		t.Fatal("SSL exposed stored app slice")
	}
}

func sslTestRoots(t *testing.T, caPath string) *x509.CertPool {
	t.Helper()
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("invalid proxy CA")
	}
	return roots
}

func TestSSLAppDecryptsMultipleHostsAndPorts(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s, ca := startServer(t)
	s.SetSSL(SSLConfig{Apps: []SSLApp{{Name: "display only", Path: exe}}})
	for _, host := range []string{"localhost", "127.0.0.1"} {
		upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(r.Header) != 2 || r.Header.Get("User-Agent") == "" || r.Header.Get("Accept-Encoding") == "" {
				t.Errorf("unexpected upstream headers (identity must not travel over HTTP): %v", r.Header)
			}
			io.WriteString(w, "app-selected HTTPS")
		}))
		t.Cleanup(upstream.Close)
		u, _ := url.Parse(upstream.URL)
		u.Host = host + ":" + u.Port()
		client := clientFor(t, s, sslTestRoots(t, ca))
		t.Cleanup(client.CloseIdleConnections)
		resp, err := client.Get(u.String() + "/selected")
		if err != nil {
			t.Fatalf("app-selected %s: %v", u, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		f := waitFlow(t, s)
		if f.Kind != "http" || f.Path != "/selected" || f.ResponseBody != "app-selected HTTPS" || f.ProcessPath != exe {
			t.Fatalf("capture = %+v", f)
		}
	}
}

func TestSSLAppDoesNotAttributeNonLoopbackClient(t *testing.T) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var host string
	for _, address := range addresses {
		if ip, ok := address.(*net.IPNet); ok && ip.IP.To4() != nil && !ip.IP.IsLoopback() {
			host = ip.IP.String()
			break
		}
	}
	if host == "" {
		t.Skip("no non-loopback IPv4 interface")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(net.JoinHostPort(host, "0"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.SetSSL(SSLConfig{Apps: []SSLApp{{Name: filepath.Base(exe), Path: exe}}})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "not inspected") }))
	defer upstream.Close()
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate()) // only the upstream CA, not the proxy
	client := clientFor(t, s, roots)
	defer client.CloseIdleConnections()
	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	f := waitFlow(t, s)
	if f.Kind != "connect" || f.Process != "" || f.ProcessPath != "" {
		t.Fatalf("non-loopback connection attributed to local app: %+v", f)
	}
}

// A separate executable with the same basename provides a genuine different
// process/path while the parent simultaneously opens selected sockets.
func TestSSLAppClientHelper(t *testing.T) {
	if os.Getenv("VSCROXY_SSL_APP_HELPER") != "1" {
		return
	}
	proxyURL, err := url.Parse(os.Getenv("VSCROXY_SSL_PROXY"))
	if err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: sslTestRoots(t, os.Getenv("VSCROXY_SSL_CA"))}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	fmt.Println("ready")
	io.Copy(io.Discard, os.Stdin) // parent releases all clients together
	resp, err := client.Get(os.Getenv("VSCROXY_SSL_TARGET"))
	if err == nil {
		resp.Body.Close()
		t.Fatal("different executable path was incorrectly decrypted with proxy CA")
	}
}

func TestSSLAppConcurrentClientsDoNotShareIdentity(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("executable-copy subprocess regression runs on Linux")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	otherExe := filepath.Join(t.TempDir(), filepath.Base(exe))
	src, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(otherExe, os.O_CREATE|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy executable: %v, %v", copyErr, closeErr)
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer upstream.Close()
	s, ca := startServer(t)
	s.SetSSL(SSLConfig{Apps: []SSLApp{{Name: filepath.Base(exe), Path: exe}}})
	roots := sslTestRoots(t, ca)
	type helper struct {
		cmd    *exec.Cmd
		stdin  io.WriteCloser
		stdout *bufio.Reader
	}
	var helpers []helper
	// Finish spawning before opening sockets: fork temporarily inherits the
	// parent's descriptors before exec closes them. Such shared sockets are
	// deliberately unidentified, and have their own procinfo regression test.
	for range 6 {
		cmd := exec.CommandContext(t.Context(), otherExe, "-test.run=^TestSSLAppClientHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), "VSCROXY_SSL_APP_HELPER=1", "VSCROXY_SSL_PROXY=http://"+s.Addr(), "VSCROXY_SSL_CA="+ca, "VSCROXY_SSL_TARGET="+upstream.URL)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		reader := bufio.NewReader(stdout)
		if ready, err := reader.ReadString('\n'); err != nil || ready != "ready\n" {
			stdin.Close()
			cmd.Wait()
			t.Fatalf("helper readiness = %q, %v", ready, err)
		}
		helpers = append(helpers, helper{cmd, stdin, reader})
	}
	var wg sync.WaitGroup
	for _, helper := range helpers {
		wg.Go(func() {
			client := clientFor(t, s, roots)
			defer client.CloseIdleConnections()
			resp, err := client.Get(upstream.URL + "/selected")
			if err != nil {
				t.Errorf("selected client: %v", err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		})
		wg.Go(func() {
			helper.stdin.Close()
			out, readErr := io.ReadAll(helper.stdout)
			if err := helper.cmd.Wait(); err != nil || readErr != nil {
				t.Errorf("unselected helper: %v\n%s", err, out)
			}
		})
	}
	wg.Wait()
	decrypted, tunnels := 0, 0
	for range 12 {
		f := waitFlow(t, s)
		switch {
		case f.Kind == "http" && f.ProcessPath == exe && f.Path == "/selected":
			decrypted++
		case f.Kind == "connect" && f.ProcessPath == otherExe:
			tunnels++
		default:
			t.Fatalf("mixed client identity: %+v", f)
		}
	}
	if decrypted != 6 || tunnels != 6 {
		t.Fatalf("decrypted=%d, tunnels=%d", decrypted, tunnels)
	}
}

type switchSSLOnConnect struct {
	proxy.BaseAddon
	server *Server
	next   SSLConfig
}

func (a *switchSSLOnConnect) Requestheaders(f *proxy.Flow) {
	if f.Request.Method == "CONNECT" {
		// The engine has committed its decision, but capture has not yet
		// published the CONNECT. Deterministically exercise that race window.
		a.server.SetSSL(a.next)
	}
}

func TestSSLConfigSwitchPreservesConnectionDecision(t *testing.T) {
	for _, intercept := range []bool{false, true} {
		t.Run(map[bool]string{false: "tunnel", true: "decrypted"}[intercept], func(t *testing.T) {
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "unchanged connection") }))
			defer upstream.Close()
			dir := t.TempDir()
			s, err := New("127.0.0.1:0", dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			ca := filepath.Join(dir, "mitmproxy-ca-cert.pem")
			s.SetSSL(SSLConfig{All: intercept})
			s.proxy.Addons = append([]proxy.Addon{&switchSSLOnConnect{server: s, next: SSLConfig{All: !intercept}}}, s.proxy.Addons...)
			if err := s.Start(); err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			roots.AddCert(upstream.Certificate())
			if intercept {
				roots = sslTestRoots(t, ca)
			}
			client := clientFor(t, s, roots)
			defer client.CloseIdleConnections()
			for range 2 {
				resp, err := client.Get(upstream.URL + "/unchanged")
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil || string(body) != "unchanged connection" {
					t.Fatalf("body=%q, err=%v", body, err)
				}
			}
			f := waitFlow(t, s)
			want := "connect"
			if intercept {
				want = "http"
				if second := waitFlow(t, s); second.Kind != "http" {
					t.Fatalf("second flow = %+v", second)
				}
			}
			if f.Kind != want {
				t.Fatalf("flow kind=%q, want %q after config switch", f.Kind, want)
			}
		})
	}
}
