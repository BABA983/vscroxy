package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
)

func TestCurlCommand(t *testing.T) {
	f := flow{
		method:    "POST",
		url:       "https://example.com/a?b=1",
		reqHeader: http.Header{"Content-Type": {"application/json"}, "Host": {"example.com"}, "Cookie": {"a=1"}},
		reqBody:   `{"name":"it's"}`,
	}
	got, err := curlCommand(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"-X 'POST'",
		`-H 'Content-Type: application/json'`,
		`-H 'Cookie: a=1'`,
		`-H 'Host: example.com'`,
		`--data-raw '{"name":"it'\''s"}'`,
		"'https://example.com/a?b=1'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("curlCommand() = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "Content-Length") {
		t.Errorf("curlCommand() = %q, should leave Content-Length to curl", got)
	}
}

func TestCurlCommandGetsOnlyTheURL(t *testing.T) {
	if got, err := curlCommand(flow{method: "GET", url: "http://example.com/"}); err != nil || got != "curl -q --path-as-is -H 'Accept:' -H 'User-Agent:' -H 'Content-Type:' --url 'http://example.com/'" {
		t.Errorf("curlCommand() = %q, %v", got, err)
	}
}

func TestParseCurlRequestData(t *testing.T) {
	for _, tc := range []struct {
		name, command, method, url, body string
		headers                          http.Header
	}{
		{"browser", "curl 'https://api.example.test/items?limit=7' \\\n -H 'X-Tag: first' -H 'X-Tag: second' -b 'session=abc' --compressed --data-raw '{\"name\":\"it'\\''s\"}'", "POST", "https://api.example.test/items?limit=7", `{"name":"it's"}`, http.Header{"X-Tag": {"first", "second"}, "Cookie": {"session=abc"}, "Content-Type": {"application/x-www-form-urlencoded"}}},
		{"ansi and double quotes", `curl --url=https://api.example.test -XPATCH -H"X-Path: a\z" --data-binary $'a\nb%20c'`, "PATCH", "https://api.example.test", "a\nb%20c", http.Header{"X-Path": {`a\z`}, "Content-Type": {"application/x-www-form-urlencoded"}}},
		{"query encoding", `curl -G 'https://api.example.test/items?fixed=3#top' --data-urlencode 'q=a b&c' -d limit=7`, "GET", "https://api.example.test/items?fixed=3&q=a+b%26c&limit=7#top", "", http.Header{}},
		{"json and explicit overrides", `curl https://api.example.test --json '{"x":' --json '7}' -u 'user:pass' -H 'Authorization: Bearer literal' -H 'Accept:' -H 'X-Empty;'`, "POST", "https://api.example.test", `{"x":7}`, http.Header{"Authorization": {"Bearer literal"}, "Content-Type": {"application/json"}, "X-Empty": {""}}},
		{"basic auth and raw at", `curl https://api.example.test --user 'user:p:a ss' --data-raw '@not-a-file'`, "POST", "https://api.example.test", "@not-a-file", http.Header{"Authorization": {"Basic dXNlcjpwOmEgc3M="}, "Content-Type": {"application/x-www-form-urlencoded"}}},
		{"head query", `curl -I -G https://api.example.test --data-urlencode 'q=a+b'`, "HEAD", "https://api.example.test?q=a%2Bb", "", http.Header{}},
		{"environment placeholders", `curl '{{base_url}}/items/{{id}}' -H 'Authorization: Bearer {{token}}'`, "GET", "{{base_url}}/items/{{id}}", "", http.Header{"Authorization": {"Bearer {{token}}"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := parseCurl(tc.command)
			if err != nil {
				t.Fatal(err)
			}
			if r.Method != tc.method || r.URL != tc.url || r.Body != tc.body || !reflect.DeepEqual(request.ParseHeaderBlock(r.Headers), tc.headers) {
				t.Fatalf("got %+v, headers %v; want %s %s body %q headers %v", r, request.ParseHeaderBlock(r.Headers), tc.method, tc.url, tc.body, tc.headers)
			}
		})
	}
}

func TestParseCurlRefusesExecutableOrUnsupportedInput(t *testing.T) {
	for _, command := range []string{
		"", "wget https://example.test", "curl 'unclosed", "curl",
		"curl https://a.test https://b.test", "curl --url https://a.test --url https://b.test",
		"curl https://a.test; touch /tmp/not-executed", "curl https://a.test | cat", "curl https://a.test && echo hi", "curl https://a.test > /tmp/not-written", "curl https://a.test &", "TOKEN=secret curl https://a.test",
		"curl https://a.test -d $(touch /tmp/not-executed)", "curl https://a.test -d `id`", "curl https://a.test -H \"Authorization: $TOKEN\"", "curl https://a.test -d $((1+2))", "curl https://a.test -d <(cat /etc/passwd)",
		"curl https://a.test --data @/etc/passwd", "curl https://a.test --data-binary @-", "curl https://a.test --data-urlencode x@file", "curl https://a.test -H @file", "curl https://a.test -b cookies.txt", "curl https://a.test --config config.txt", "curl https://a.test --form file=@private", "curl https://a.test --insecure", "curl https://a.test --location", "curl https://a.test -H $'X-Test: a\\nInjected: b'", "curl https://a.test -d $'a\\0b'", "curl https://a.test -X", "curl file:///etc/passwd",
	} {
		if _, err := parseCurl(command); err == nil {
			t.Errorf("accepted unsafe or unsupported input: %q", command)
		}
	}
}

func TestCurlCopyPreservesTextBodyAndMethod(t *testing.T) {
	body := strings.Repeat("a", 20<<10) + "\nit's $HOME; $(not-executed)"
	path := filepath.Join(t.TempDir(), "spilled-body")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range []flow{
		{method: "GET", url: "https://api.example.test/?q[]=7", reqBody: "stale head", reqFile: path, reqSize: len(body), reqHeader: http.Header{"X-Empty": {""}}},
		{method: "X|id", url: "https://api.example.test", reqBody: "quotes ' and \" and \\", reqHeader: http.Header{}},
	} {
		command, err := curlCommand(f)
		if err != nil {
			t.Fatal(err)
		}
		r, err := parseCurl(command)
		if err != nil {
			t.Fatal(err)
		}
		wantBody := f.reqBody
		if f.reqFile != "" {
			wantBody = body
		}
		if r.Method != f.method || r.URL != f.url || r.Body != wantBody || !reflect.DeepEqual(request.ParseHeaderBlock(r.Headers), f.reqHeader) {
			t.Fatalf("copy/import changed method, headers, URL or body: %+v", r)
		}
	}
	if _, err := curlCommand(flow{reqFile: filepath.Join(t.TempDir(), "gone")}); err == nil {
		t.Fatal("missing body was silently exported as a truncated request")
	}
	for _, f := range []flow{
		{reqBody: "head", reqSize: 200},
		{reqBody: "partial", reqError: "upload aborted"},
		{kind: "connect"}, {kind: "ws"},
		{reqHeader: http.Header{"X-Test": {"a\r\nInjected: b"}}},
		{reqTrailer: http.Header{"X-Final": {"signature"}}},
	} {
		if _, err := curlCommand(f); err == nil {
			t.Fatal("an incomplete or unsupported request was exported as replayable")
		}
	}
}

// Execute the clipboard text with a real POSIX shell and curl, not our importer:
// otherwise exporter/importer bugs can agree while sending the wrong request.
func TestCurlCommandReplaysToServer(t *testing.T) {
	for _, tool := range []string{"curl", "sh", "base64"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required for POSIX replay verification", tool)
		}
	}
	for _, tc := range []struct {
		name, method, body string
		headers            http.Header
	}{
		{"literal", "GET", "@not-a-file\r\n' $HOME $(false)\n", http.Header{"content-type": {"text/plain"}, "X-Tag": {"first", "second"}, "Cookie": {"session=secret"}, "X-Empty": {""}, "Host": {"virtual.example"}, "Connection": {"X-Hop"}, "X-Hop": {"do not replay"}, "Transfer-Encoding": {"chunked"}, "Content-Length": {"999"}}},
		{"head", "HEAD", "", http.Header{}},
		{"empty upload", "POST", "", http.Header{"Content-Length": {"0"}}},
		{"large", "PUT", strings.Repeat("a'b\n", 80<<10), http.Header{}},
		{"binary", "POST", "\x00\xff\r\n\x01body", http.Header{"Content-Type": {"application/octet-stream"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(chan *http.Request, 1)
			bodies := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				seen <- r.Clone(t.Context())
				bodies <- string(body)
				w.Write([]byte("request accepted"))
			}))
			defer server.Close()
			f := flow{method: tc.method, url: server.URL + "/escaped%2Fpath?q[]=7&x={a,b}", reqBody: tc.body, reqHeader: tc.headers}
			if tc.name == "large" {
				f.reqFile = filepath.Join(t.TempDir(), "spilled-body")
				if err := os.WriteFile(f.reqFile, []byte(tc.body), 0o600); err != nil {
					t.Fatal(err)
				}
				f.reqBody, f.reqSize = tc.body[:64<<10], len(tc.body)
			}
			command, err := curlCommand(f)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sh")
			cmd.WaitDelay = time.Second
			cmd.Stdin = strings.NewReader(command + "\n")
			// Keep tests local and bound a broken HEAD export's wait for a body.
			cmd.Env = append(os.Environ(), "http_proxy=", "https_proxy=", "ALL_PROXY=", "NO_PROXY=*", "CURL_HOME="+t.TempDir())
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("copied command failed: %v\n%s", err, output)
			}
			select {
			case r := <-seen:
				if r.Method != tc.method || r.RequestURI != "/escaped%2Fpath?q[]=7&x={a,b}" || <-bodies != tc.body {
					t.Fatalf("request changed: %s %s", r.Method, r.RequestURI)
				}
				want := http.Header{}
				for name, values := range tc.headers {
					want[http.CanonicalHeaderKey(name)] = values
				}
				if host := want.Get("Host"); host != "" && r.Host != host {
					t.Errorf("Host = %q, want %q", r.Host, host)
				}
				if want.Get("Content-Length") == "0" && r.Header.Get("Content-Length") != "0" {
					t.Error("empty upload lost its zero Content-Length")
				}
				want.Del("X-Hop")
				want.Del("Connection")
				for _, name := range []string{"Content-Type", "Accept", "User-Agent", "X-Tag", "Cookie", "X-Empty", "X-Hop", "Connection"} {
					if !reflect.DeepEqual(r.Header.Values(name), want.Values(name)) {
						t.Errorf("%s = %#v, want %#v", name, r.Header.Values(name), want.Values(name))
					}
				}
			default:
				t.Fatal("no request reached the server")
			}
		})
	}
}

func TestCurlCommandFromCompressedCapture(t *testing.T) {
	for _, tool := range []string{"curl", "sh"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required for POSIX replay verification", tool)
		}
	}
	const body = "{\"name\":\"compressed upload\"}\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				http.Error(w, "invalid gzip", 400)
				return
			}
			defer zr.Close()
			reader = zr
		}
		data, _ := io.ReadAll(reader)
		if string(data) != body {
			http.Error(w, "wrong body", 400)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		zw.Write([]byte("accepted"))
		zw.Close()
	}))
	defer server.Close()
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	zw.Write([]byte(body))
	zw.Close()
	proxyURL, _ := url.Parse("http://" + srv.Addr())
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(t.Context(), "POST", server.URL, &compressed)
	req.Header.Set("Content-Encoding", "gzip")
	res, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	a := workspaceApp()
	a.flows = nil
	select {
	case captured := <-srv.Flows():
		a.pending = []capture.Flow{captured}
	case <-time.After(5 * time.Second):
		t.Fatal("no captured request")
	}
	a.drain()
	command, err := curlCommand(a.flows[0])
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sh")
	cmd.Stdin = strings.NewReader(command + "\n")
	cmd.Env = append(os.Environ(), "http_proxy=", "ALL_PROXY=", "NO_PROXY=*")
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "accepted") {
		t.Fatalf("replay of decoded capture failed: %v, %s", err, output)
	}
}
