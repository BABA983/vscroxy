package app

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// Stop closes the port the client used and Start listens again: the capture
// switch is not the one-way shutdown at quit.
func TestStopCaptureClosesThePortAndStartCapturesAgain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := newApp()
	if a.srv == nil {
		t.Fatal(a.errText)
	}
	defer a.srv.Close()
	if err := a.srv.Start(); err != nil {
		t.Fatal(err)
	}
	addr := a.srv.Addr()
	a.stopCapture()
	if a.srv.Running() {
		t.Fatal("Stop left the listener running")
	}
	if conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("the stopped port still accepts connections")
	}
	if err := a.srv.Start(); err != nil {
		t.Fatalf("Start after Stop: %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()
	proxyURL, _ := url.Parse("http://" + a.srv.Addr())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 3 * time.Second}
	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("request through the restarted proxy: %v", err)
	}
	resp.Body.Close()
	select {
	case f := <-a.srv.Flows():
		if f.URL == "" {
			t.Fatal("the restarted proxy published an empty flow")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the restarted proxy captured nothing")
	}
}
