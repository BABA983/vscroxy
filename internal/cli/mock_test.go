package cli_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/cli"
	"github.com/baba983/vscroxy/internal/mockserver"
	"github.com/baba983/vscroxy/internal/request"
)

// mockCollection writes a collection whose one request has two examples on
// the same route, and returns the collections root.
func mockCollection(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	items := request.Request{ID: "items", Name: "Items", Collection: "Shop", Method: "GET", URL: "http://api.example.test/items/:id",
		Examples: []request.Example{
			{ID: "first", Name: "first", Request: request.ExampleRequest{Method: "GET", URL: "http://api.example.test/items/:id"}, Status: 200, Body: `{"from":"first"}`},
			{ID: "gone", Name: "gone", Request: request.ExampleRequest{Method: "GET", URL: "http://api.example.test/items/:id"}, Status: 404, Body: `{"from":"gone"}`},
		}}
	write(t, filepath.Join(root, "collections", "Shop", "Items.request.json"), items)
	return filepath.Join(root, "collections")
}

// syncText is written by the command while the test reads it.
type syncText struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncText) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncText) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// startMock runs vscroxy-cli mock in the background. It returns the base URL
// printed on start, the stderr so far, and stop, which cancels the command the
// way Ctrl-C does and returns its exit code.
func startMock(t *testing.T, args ...string) (string, *syncText, func() int) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	var stdout syncText
	stderr := &syncText{}
	exit := make(chan int, 1)
	go func() { exit <- cli.Run(ctx, append([]string{"mock"}, args...), &stdout, stderr) }()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stdout.String(), "http://") {
		if time.Now().After(deadline) {
			t.Fatalf("mock did not start: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	line := stdout.String()
	i := strings.Index(line, "http://")
	base := strings.Fields(line[i:])[0]
	stop := func() int {
		cancel()
		select {
		case code := <-exit:
			return code
		case <-time.After(10 * time.Second):
			t.Fatal("mock did not stop after cancel")
			return 0
		}
	}
	return base, stderr, stop
}

// mockGet sends one request; header holds name, value pairs.
func mockGet(t *testing.T, method, url string, header ...string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestMockServesSavedExamplesUntilInterrupted(t *testing.T) {
	base, _, stop := startMock(t, mockCollection(t))
	if code, body := mockGet(t, "GET", base+"/items/42?x=1"); code != 200 || body != `{"from":"first"}` {
		t.Errorf("default example: %d %q; want the first example", code, body)
	}
	if code, body := mockGet(t, "GET", base+"/items/42", mockserver.SelectHeader, "gone"); code != 404 || body != `{"from":"gone"}` {
		t.Errorf("X-Mock-Response-Name: gone: %d %q; want the second example", code, body)
	}
	if code, body := mockGet(t, "GET", base+"/items/42", mockserver.CodeHeader, "404"); code != 404 || body != `{"from":"gone"}` {
		t.Errorf("X-Mock-Response-Code: 404: %d %q; want the second example", code, body)
	}
	if code, body := mockGet(t, "GET", base+"/nope"); code != 404 || !strings.Contains(body, "no example") {
		t.Errorf("unknown path: %d %q; want 404 with no example", code, body)
	}
	if code, _ := mockGet(t, "POST", base+"/items/42"); code != 404 {
		t.Errorf("POST to a GET route: %d; want 404", code)
	}
	if code := stop(); code != 130 {
		t.Errorf("exit after Ctrl-C = %d; want 130", code)
	}
}

func TestMockPortAndNonLoopbackWarning(t *testing.T) {
	// --port 0 replaces the port in --addr with an OS-assigned one, so the
	// fixed :9 is never bound and no port is picked twice.
	base, stderr, stop := startMock(t, "--addr", "127.0.0.1:9", "--port", "0", mockCollection(t))
	if !strings.HasPrefix(base, "http://127.0.0.1:") || base == "http://127.0.0.1:9" {
		t.Errorf("base = %q; want an OS-assigned port on 127.0.0.1", base)
	}
	if strings.Contains(stderr.String(), "not a loopback") {
		t.Errorf("loopback address warned: %q", stderr.String())
	}
	stop()

	_, stderr, stop = startMock(t, "--addr", "0.0.0.0:0", mockCollection(t))
	if !strings.Contains(stderr.String(), "not a loopback") {
		t.Errorf("0.0.0.0 did not warn: %q", stderr.String())
	}
	stop()
}

func TestMockRefusesCollectionWithoutExamples(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "collections", "Shop", "Plain.request.json"), request.Request{ID: "plain", Name: "Plain", Collection: "Shop", Method: "GET", URL: "http://api.example.test/"})
	var out, errOut bytes.Buffer
	code := cli.Run(t.Context(), []string{"mock", filepath.Join(root, "collections")}, &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "No saved examples") {
		t.Errorf("exit=%d stderr=%q; want 2 and the no-examples message", code, errOut.String())
	}
}
