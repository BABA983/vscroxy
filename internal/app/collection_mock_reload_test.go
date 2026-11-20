package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
)

func mockHTTPGet(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// The desktop mock follows the library as it is saved, and keeps answering
// from the last good examples if the library file cannot be parsed.
func TestCollectionMockReloadsSavedExamples(t *testing.T) {
	a := workspaceApp()
	const path = "Shop"
	a.requests = []savedRequest{{Request: request.Request{
		ID: "items", Name: "Items", Collection: path, Method: "GET", URL: "https://api.test/items",
		Examples: []request.Example{{ID: "e1", Name: "one", Status: 200, Body: "first",
			Request: request.ExampleRequest{Method: "GET", URL: "https://api.test/items"}}},
	}}}
	a.startCollectionMock(path)
	m := a.mocks[path]
	if m == nil {
		t.Fatal("mock did not start")
	}
	defer a.stopCollectionMock(path)

	if code, body := mockHTTPGet(t, m.url+"/items"); code != 200 || body != "first" {
		t.Fatalf("initial answer: %d %q", code, body)
	}

	edited := slices.Clone(a.requests)
	edited[0].Examples = []request.Example{
		{ID: "e1", Name: "one", Status: 200, Body: "edited", Request: request.ExampleRequest{Method: "GET", URL: "https://api.test/items"}},
		{ID: "e2", Name: "added", Status: 200, Body: "added", Request: request.ExampleRequest{Method: "GET", URL: "https://api.test/added"}},
	}
	if err := a.persistRequests(edited); err != nil {
		t.Fatal(err)
	}
	if code, body := mockHTTPGet(t, m.url+"/items"); code != 200 || body != "edited" {
		t.Errorf("edited answer: %d %q", code, body)
	}
	if code, body := mockHTTPGet(t, m.url+"/added"); code != 200 || body != "added" {
		t.Errorf("added answer: %d %q", code, body)
	}

	deleted := slices.Clone(a.requests)
	deleted[0].Examples = deleted[0].Examples[:1]
	if err := a.persistRequests(deleted); err != nil {
		t.Fatal(err)
	}
	if code, _ := mockHTTPGet(t, m.url+"/added"); code != 404 {
		t.Errorf("deleted route: %d, want 404", code)
	}

	root := t.TempDir()
	a.library.root = root
	file := filepath.Join(root, "Shop", "Items.request.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"broken":`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.loadRequestLibrary()
	if a.requestsProblem == "" {
		t.Fatal("a broken library file was not reported")
	}
	reported := slices.ContainsFunc(a.notices, func(n notice) bool {
		return strings.Contains(n.text, "kept the last working examples")
	})
	if !reported {
		t.Errorf("no notice about the stale mock: %+v", a.notices)
	}
	if code, body := mockHTTPGet(t, m.url+"/items"); code != 200 || body != "edited" {
		t.Errorf("answer after a broken file: %d %q, want the last good set", code, body)
	}

	repaired := request.Request{ID: "items", Name: "Items", Collection: path, Method: "GET", URL: "https://api.test/items",
		Examples: []request.Example{{ID: "e1", Name: "one", Status: 200, Body: "repaired",
			Request: request.ExampleRequest{Method: "GET", URL: "https://api.test/items"}}}}
	data, err := json.Marshal(repaired)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	a.loadRequestLibrary()
	if a.requestsProblem != "" {
		t.Fatalf("repaired library still reports %q", a.requestsProblem)
	}
	if code, body := mockHTTPGet(t, m.url+"/items"); code != 200 || body != "repaired" {
		t.Errorf("answer after repair: %d %q", code, body)
	}
}
