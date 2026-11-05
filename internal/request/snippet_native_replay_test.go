package request

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Execute the generated standard-library clients against a local server. The
// server checks the wire request, independently of our escaping helpers.
func TestSnippetMoreNativeClientsReplay(t *testing.T) {
	for _, tc := range []struct {
		language, executable, extension string
		args                            []string
	}{
		{"Java (java.net.http)", "java", ".java", nil},
		{"PHP (cURL)", "php", ".php", nil},
		{"Ruby (Net::HTTP)", "ruby", ".rb", nil},
	} {
		t.Run(tc.language, func(t *testing.T) {
			if _, err := exec.LookPath(tc.executable); err != nil {
				t.Skipf("%s is needed for native snippet replay", tc.executable)
			}
			switch tc.language {
			case "Java (java.net.http)":
				version, _ := exec.Command("java", "-version").CombinedOutput()
				if strings.Contains(string(version), `version "1.`) {
					t.Skip("Java 11+ is needed for java.net.http")
				}
			case "PHP (cURL)":
				modules, _ := exec.Command("php", "-m").Output()
				if !strings.Contains(strings.ToLower(string(modules)), "curl") {
					t.Skip("the PHP cURL extension is needed")
				}
			}
			for _, redirect := range []bool{false, true} {
				t.Run(map[bool]string{false: "response", true: "redirect"}[redirect], func(t *testing.T) {
					var calls atomic.Int32
					wantDup := []string{"a", "b"}
					if strings.HasPrefix(tc.language, "Ruby") {
						// Net::HTTP joins repeated header values with ", ".
						wantDup = []string{"a, b"}
					}
					const body = "{\n\"message\":\"你好 😀 ‘quote’\",\n\"literal\":\"$HOME $(throw 'executed') ` ${x} \\\\\\\"\\\"\\r\\n}"
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						data, _ := io.ReadAll(r.Body)
						if r.Method != "PATCH" || r.RequestURI != "/a%2Fb?q=%22%26" || string(data) != body ||
							!reflect.DeepEqual(r.Header.Values("X-Dup"), wantDup) ||
							!reflect.DeepEqual(r.Header.Values("X-Empty"), []string{""}) ||
							r.Header.Get("X-Literal") != "$HOME ` $(id) ' \"" ||
							r.Header.Get("Content-Type") != "application/json" ||
							r.Header.Get("Cookie") != "a=7; b=9" ||
							r.Header.Get("User-Agent") != "copy-test/1" {
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
						"Content-Type": {"application/json"}, "X-Dup": {"a", "b"}, "X-Empty": {""},
						"X-Literal": {"$HOME ` $(id) ' \""}, "Cookie": {"a=7", "b=9"}, "User-Agent": {"copy-test/1"},
					}
					code, err := Snippet(tc.language, &Prepared{Method: "PATCH", URL: server.URL + "/a%2Fb?q=%22%26", Body: body, Header: headers})
					if err != nil {
						t.Fatal(err)
					}
					file := filepath.Join(t.TempDir(), "request"+tc.extension)
					if err := os.WriteFile(file, []byte(code), 0600); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, tc.executable, append(tc.args, file)...)
					cmd.Env = append(os.Environ(), "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=*", "http_proxy=", "https_proxy=", "all_proxy=", "no_proxy=*")
					output, err := cmd.CombinedOutput()
					if err != nil || calls.Load() != 1 || !strings.Contains(string(output), "synthetic-response") {
						t.Fatalf("native replay (%v), calls=%d: %s", err, calls.Load(), output)
					}
				})
			}
		})
	}
}
