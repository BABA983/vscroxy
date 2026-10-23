package main

import (
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

// flow is one captured request/response pair, the unit the table shows and the
// detail panes inspect. It stands in for what the capture core will produce.
type flow struct {
	id          int
	method      string
	scheme      string
	host        string
	path        string
	url         string
	status      int
	bytes       int
	millis      int
	reqHeaders  string
	resHeaders  string
	reqBody     string
	reqFile     string
	reqSize     int
	reqError    string
	resBody     string
	resFile     string
	resSize     int
	resError    string
	reqHeader   http.Header
	respHeader  http.Header
	reqTrailer  http.Header
	resTrailer  http.Header
	resProto    string
	at          time.Time
	kind        string
	process     string
	processPath string
	dns         string
	conn        conn
	color       string
	note        string
	pinned      bool
	wait        int
	download    int
}

// conn is what an exchange ran over, as the connection panel shows it.
type conn struct {
	proto      string
	client     string
	proxy      string
	server     string
	resolved   string
	lookupMs   int
	tls        string
	cipher     string
	certCN     string
	issuer     string
	certUntil  string
	flows      int
	reused     bool
	dnsFresh   bool
	log        string
	error      string
	errorStage string
}

// maxBodyView bounds how much of a spilled body the Body tab reads back.
const maxBodyView = 8 << 20

// body returns the body to show: the head the flow holds in memory, or the
// whole body from the spill file when the capture core wrote it out.
func (f flow) body(response bool) string {
	head, file, size, problem := f.reqBody, f.reqFile, f.reqSize, f.reqError
	if response {
		head, file, size, problem = f.resBody, f.resFile, f.resSize, f.resError
	}
	if file != "" {
		reader, err := os.Open(file)
		if err != nil {
			return head + fmt.Sprintf("\n\n… the spilled body is gone: %v", err)
		}
		defer reader.Close()
		data, err := io.ReadAll(io.LimitReader(reader, maxBodyView+1))
		head = string(data)
		if err != nil {
			problem = err.Error()
		}
	}
	if len(head) > maxBodyView {
		head = head[:maxBodyView] + fmt.Sprintf("\n\n… truncated: %d bytes shown (recorded size: %d)", maxBodyView, size)
	}
	if problem != "" {
		head += "\n\n… incomplete body: " + problem
	}
	return head
}

// contentType is the media type of the body on the side shown, taken from the
// structured headers or the formatted block.
func (f flow) contentType(response bool) string {
	h, block := f.reqHeader, f.reqHeaders
	if response {
		h, block = f.respHeader, f.resHeaders
	}
	ct := h.Get("Content-Type")
	if ct == "" {
		for _, line := range strings.Split(block, "\n") {
			if name, value, ok := strings.Cut(line, ": "); ok && strings.EqualFold(name, "Content-Type") {
				ct = value
				break
			}
		}
	}
	ct, _, _ = strings.Cut(ct, ";")
	return strings.ToLower(strings.TrimSpace(ct))
}

var (
	methods = []string{"GET", "GET", "GET", "POST", "PUT", "DELETE", "PATCH"}
	hosts   = []string{
		"api.github.com",
		"github.com",
		"raw.githubusercontent.com",
		"registry.npmjs.org",
		"api.openai.com",
		"cdn.jsdelivr.net",
		"fonts.gstatic.com",
		"api.stripe.com",
	}
	paths = []string{
		"/v1/user/repos",
		"/v1/repos/baba983/vscroxy",
		"/v1/repos/baba983/vscroxy/pulls",
		"/-/ping?write=true",
		"/v1/chat/completions",
		"/npm/v1/security/advisories/bulk",
		"/ajax/libs/react/19.2.0/umd/react.production.min.js",
		"/css2?family=Inter:wght@400;600",
		"/v1/payment_intents",
		"/v1/customers/cus_QN1",
		"/api/v3/commit/31ee1d1",
		"/search?q=mygo&type=repositories",
	}
	statuses = []int{200, 200, 200, 200, 200, 201, 204, 301, 304, 400, 401, 404, 429, 500, 502}
)

// seedFlows makes n plausible flows so the table, the filters and the diff have
// something to work on before the proxy engine is wired in.
func seedFlows(n int) []flow {
	r := rand.New(rand.NewSource(7))
	out := make([]flow, 0, n)
	for i := range n {
		method := methods[r.Intn(len(methods))]
		status := statuses[r.Intn(len(statuses))]
		host := hosts[r.Intn(len(hosts))]
		path := paths[r.Intn(len(paths))]
		f := flow{
			id:     i + 1,
			method: method,
			scheme: "https",
			host:   host,
			path:   path,
			url:    "https://" + host + path,
			status: status,
			bytes:  int(r.ExpFloat64() * 24000),
			millis: int(r.ExpFloat64() * 180),
		}
		f.wait = f.millis / 2
		f.download = f.millis - f.wait
		if method == "GET" && r.Intn(2) == 0 {
			f.bytes = 0
		}
		f.reqHeaders = requestHeaders(f)
		f.resHeaders = responseHeaders(f)
		f.reqBody = requestBody(f)
		f.resBody = responseBody(f)
		out = append(out, f)
	}
	return out
}

func requestBody(f flow) string {
	if f.method == "GET" || f.method == "DELETE" {
		return ""
	}
	return fmt.Sprintf(`{
  "name": "vscroxy",
  "private": false,
  "owner": "baba983",
  "description": "A native HTTP debugging proxy",
  "topics": ["proxy", "debugging", "go"],
  "has_issues": true
}`)
}

func responseBody(f flow) string {
	if f.status == 204 || f.status == 304 {
		return ""
	}
	return fmt.Sprintf(`{
  "id": %d,
  "method": %q,
  "url": "https://%s%s",
  "status": %d,
  "ok": %t,
  "size": %d,
  "duration_ms": %d,
  "owner": {
    "login": "baba983",
    "id": 1024,
    "type": "User"
  },
  "meta": {
    "cached": %t,
    "region": "ap-southeast-1",
    "request_id": "req_%06x"
  }
}`, f.id, f.method, f.host, f.path, f.status, f.status < 400, f.bytes, f.millis, f.status == 304, f.id*7919)
}

func requestHeaders(f flow) string {
	return fmt.Sprintf(`%s %s HTTP/1.1
Host: %s
User-Agent: vscroxy/0.1 (darwin; arm64)
Accept: application/json
Accept-Encoding: gzip, br
Authorization: Bearer ••••••••
X-Request-Id: %06x
Connection: keep-alive`, f.method, f.path, f.host, f.id)
}

func responseHeaders(f flow) string {
	return fmt.Sprintf(`HTTP/1.1 %d %s
Content-Type: application/json; charset=utf-8
Content-Length: %d
Cache-Control: private, max-age=60
X-RateLimit-Remaining: 4983
X-GitHub-Request-Id: %06x
Server: github.com
Date: Tue, 20 Oct 2026 08:12:44 GMT`, f.status, statusText(f.status), f.bytes, f.id*31)
}

func statusText(code int) string {
	switch code {
	case 200:
		return "OK"
	case 201:
		return "Created"
	case 204:
		return "No Content"
	case 301:
		return "Moved Permanently"
	case 304:
		return "Not Modified"
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 404:
		return "Not Found"
	case 429:
		return "Too Many Requests"
	case 500:
		return "Internal Server Error"
	case 502:
		return "Bad Gateway"
	}
	return "Unknown"
}

func humanBytes(n int) string {
	switch {
	case n == 0:
		return "—"
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
