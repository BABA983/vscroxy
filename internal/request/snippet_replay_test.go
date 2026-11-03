package request

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Execute generated programs, independently of our escaping/parser helpers.
// The receiving server checks the request, not the generated source's shape.
func TestSnippetNativeClientsReplay(t *testing.T) {
	for _, tc := range []struct {
		language, executable, extension string
		args                            []string
	}{
		{"Node.js (fetch)", "node", ".mjs", nil},
		{"PowerShell", "pwsh", ".ps1", []string{"-NoProfile", "-NonInteractive", "-File"}},
	} {
		t.Run(tc.language, func(t *testing.T) {
			if _, err := exec.LookPath(tc.executable); err != nil {
				t.Skipf("%s is needed for native snippet replay", tc.executable)
			}
			for _, redirect := range []bool{false, true} {
				t.Run(map[bool]string{false: "response", true: "redirect"}[redirect], func(t *testing.T) {
					var calls atomic.Int32
					const body = "{\n\"message\":\"你好 😀 ‘quote’\",\n\"literal\":\"$env:HOME $(throw 'executed') ` ${x} \\\"\"\r\n}"
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						data, _ := io.ReadAll(r.Body)
						if r.Method != "PATCH" || r.RequestURI != "/a%2Fb?q=%22%26" || string(data) != body || r.Header.Get("X-Literal") != "$HOME ` $(id) ' \"" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Cookie") != "a=7; b=9" || r.Header.Get("User-Agent") != "copy-test/1" {
							t.Errorf("wire request changed: %s %s body=%q headers=%v", r.Method, r.RequestURI, data, r.Header)
						}
						if redirect {
							w.Header().Set("Location", "/must-not-follow")
							w.WriteHeader(302)
						} else {
							w.WriteHeader(409)
						}
						_, _ = w.Write([]byte("synthetic-response"))
					}))
					defer server.Close()
					headers := http.Header{
						"Content-Type": {"application/json"}, "X-Literal": {"$HOME ` $(id) ' \""}, "Cookie": {"a=7", "b=9"}, "User-Agent": {"copy-test/1"},
					}
					code, err := Snippet(tc.language, &Prepared{Method: "PATCH", URL: server.URL + "/a%2Fb?q=%22%26", Body: body, Header: headers})
					if err != nil {
						t.Fatal(err)
					}
					file := filepath.Join(t.TempDir(), "request"+tc.extension)
					if err := os.WriteFile(file, []byte(code), 0600); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, tc.executable, append(tc.args, file)...)
					cmd.Env = append(os.Environ(), "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=*")
					output, err := cmd.CombinedOutput()
					if err != nil || calls.Load() != 1 || !strings.Contains(string(output), "synthetic-response") {
						t.Fatalf("native replay (%v), calls=%d: %s", err, calls.Load(), output)
					}
				})
			}
		})
	}
}

func TestSnippetNativeClientsRejectLossyInputs(t *testing.T) {
	for _, language := range []string{"JavaScript (fetch)", "Node.js (fetch)"} {
		if _, err := Snippet(language, &Prepared{URL: "https://example.test", Header: http.Header{"Host": {"virtual.example.test"}}}); err == nil {
			t.Fatalf("%s accepted an alternate Host", language)
		}
	}
	for _, language := range []string{"PowerShell", "Node.js (fetch)"} {
		for _, method := range []string{"GET", "HEAD"} {
			if _, err := Snippet(language, &Prepared{Method: method, URL: "https://example.test", Body: "body"}); err == nil {
				t.Fatalf("%s accepted %s body", language, method)
			}
		}
	}
	for _, headers := range []http.Header{{"X-Test": {"a\r\nInjected: value"}}, {"X-Test": {"中文"}}, {"Content-Type": {"a", "b"}}, {"X-Test": {"first"}, "x-test": {"second"}}} {
		if _, err := Snippet("PowerShell", &Prepared{URL: "https://example.test", Header: headers}); err == nil {
			t.Fatal("PowerShell accepted unrepresentable headers")
		}
	}
	if _, err := Snippet("PowerShell", &Prepared{Method: "POST", URL: "https://example.test", Body: "unlabeled"}); err == nil {
		t.Fatal("PowerShell accepted an unlabeled POST")
	}
}
