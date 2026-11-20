package cli_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/baba983/vscroxy/internal/cli"
	"github.com/baba983/vscroxy/internal/request"
)

func TestCLIRunResolvesResponseReferenceAcrossSteps(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path+"|"+r.Header.Get("Authorization"))
		mu.Unlock()
		if r.URL.Path == "/login" {
			_, _ = w.Write([]byte(`{"token":"cli-private-token-123456"}`))
		}
	}))
	defer upstream.Close()

	root := t.TempDir()
	collection := filepath.Join(root, "collections", "Shop")
	requests := []request.Request{
		{ID: "login", Name: "Login", Collection: "Shop", Method: "GET", URL: "{{base}}/login"},
		{ID: "me", Name: "Me", Collection: "Shop", Method: "GET", URL: "{{base}}/me",
			Auth: request.Auth{Type: "Bearer", Token: `{{response "Login" "/token"}}`}},
	}
	for _, r := range requests {
		write(t, filepath.Join(collection, r.Name+".request.json"), r)
	}
	var out, errOut bytes.Buffer
	code := cli.Run(t.Context(), []string{"--user-data-dir", filepath.Join(root, "data"), "run", "--var", "base=" + upstream.URL,
		"--folder", "Shop", filepath.Join(root, "collections")}, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "2 completed · 0 failed") {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"/login|", "/me|Bearer cli-private-token-123456"}
	if len(seen) != len(want) || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("sends = %q; want %q", seen, want)
	}
}
