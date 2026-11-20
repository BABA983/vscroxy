package cli

import (
	"bytes"
	"encoding/json/v2"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/mockserver"
	"github.com/baba983/vscroxy/internal/request"
)

// watchOutput is a writer the watcher and the test can share without races.
type watchOutput struct {
	mu      sync.Mutex
	b       bytes.Buffer
	changes chan struct{}
}

func newWatchOutput() *watchOutput {
	return &watchOutput{changes: make(chan struct{}, 1)}
}

func (o *watchOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n, err := o.b.Write(p)
	select {
	case o.changes <- struct{}{}:
	default:
	}
	return n, err
}

func (o *watchOutput) wait(t *testing.T, marker string) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		o.mu.Lock()
		found, changes := strings.Contains(o.b.String(), marker), o.changes
		text := o.b.String()
		o.mu.Unlock()
		if found {
			return
		}
		select {
		case <-changes:
		case <-timer.C:
			t.Fatalf("watcher never printed %q: stdout=%q", marker, text)
		}
	}
}

func writeCollection(t *testing.T, path string, r request.Request) {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The watch's baseline is taken before the collection is read. A change made
// between that fingerprint and the runtime starting must still be reloaded,
// even though the watcher goroutine has not run yet.
func TestMockWatchBaselinePredatesTheCollectionLoad(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "collections")
	file := filepath.Join(input, "Shop", "Items.request.json")
	original := request.Request{ID: "items", Name: "Items", Collection: "Shop", Method: "GET", URL: "http://api.test/items",
		Examples: []request.Example{{ID: "e1", Name: "one", Status: 200, Body: "original-body",
			Request: request.ExampleRequest{Method: "GET", URL: "http://api.test/items"}}}}
	writeCollection(t, file, original)

	source := mockSource{input: input}
	baseline := collectionFingerprint(input)
	set, _, err := source.load()
	if err != nil {
		t.Fatal(err)
	}

	changed := original
	changed.Examples = []request.Example{{ID: "e1", Name: "one", Status: 200, Body: "changed-body",
		Request: request.ExampleRequest{Method: "GET", URL: "http://api.test/items"}}}
	writeCollection(t, file, changed)

	stdout, stderr := newWatchOutput(), newWatchOutput()
	runtime, err := startMockRuntime(source, baseline, set, mockserver.Options{}, "127.0.0.1:0", "127.0.0.1:0", stdout, stderr, true)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	stdout.wait(t, "Reloaded 1 examples")
	resp, err := http.Get(runtime.httpURL + "/items")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "changed-body" {
		t.Fatalf("answer = %d %q, want the change made before the mock started", resp.StatusCode, body)
	}
}
