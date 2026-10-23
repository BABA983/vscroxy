package main

import (
	"cmp"
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
	replayOf    int
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

// connectionExplanation interprets the recorded stage, not error-message
// substrings. Causes are possibilities, never a diagnosis of the remote host.
type connectionExplanation struct {
	title, summary, causes, steps string
}

func (cn conn) explanation() connectionExplanation {
	switch cn.errorStage {
	case "DNS":
		return connectionExplanation{
			"DNS lookup failed",
			"The proxy could not resolve a name needed for this connection.",
			"The hostname may be incorrect, the DNS resolver unavailable, or a VPN or DNS mapping may affect resolution.",
			"Check the hostname and DNS mappings. Compare name resolution with and without the VPN, and check whether an upstream proxy resolves the target itself.",
		}
	case "TCP":
		return connectionExplanation{
			"Could not establish a connection",
			"The TCP connection failed. The endpoint may be the target server or a configured upstream proxy; check the connection log.",
			"The endpoint may be unavailable, its port closed, or a firewall, VPN or upstream proxy may block the connection.",
			"Verify the host and port, then compare access directly and through the proxy. Check VPN, firewall and upstream proxy settings before changing TLS trust.",
		}
	case "TLS client":
		return connectionExplanation{
			"Client TLS handshake failed",
			"TLS negotiation between the client and vscroxy failed. This does not prove a certificate-trust or pinning problem.",
			"The client may not trust this proxy's CA, may use certificate pinning, or may have incompatible TLS settings. A disconnected client can also interrupt negotiation.",
			"Check this specific client's CA trust and TLS settings. System trust alone does not cover every app. If the app pins certificates, leave it as an encrypted tunnel or use an authorized debug build; do not disable TLS verification globally.",
		}
	case "TLS upstream":
		return connectionExplanation{
			"Upstream TLS handshake failed",
			"TLS negotiation from vscroxy toward the target or upstream proxy failed, not the client's TLS connection to vscroxy.",
			"Server TLS settings, mutual TLS requirements, an upstream proxy or a network interruption may be involved.",
			"Check the target hostname, SNI, server TLS configuration and any client-certificate requirement. Compare a direct connection with the upstream-proxy path; installing vscroxy's CA on the client does not repair this leg.",
		}
	case "CONNECT":
		return connectionExplanation{
			"Tunnel setup failed",
			"The proxy could not complete CONNECT tunnel setup. This is not a normal, successfully forwarded encrypted tunnel.",
			"An upstream proxy may reject the tunnel, or the client or remote endpoint may disconnect during setup.",
			"Inspect the raw error and connection log, verify the destination host and port, and check upstream proxy authentication and tunnel permissions.",
		}
	case "HTTP":
		return connectionExplanation{
			"HTTP exchange failed",
			"A transport error interrupted the HTTP exchange. A transport failure is separate from an HTTP error response returned by the target.",
			"The connection may have closed or timed out, or the peer may have sent an invalid HTTP response.",
			"Inspect the raw error and the last connection events. Compare direct and proxied access, and check server logs and upstream proxy settings. Retry only when it is safe to repeat this request.",
		}
	default:
		return connectionExplanation{
			"Connection failed",
			"An error was recorded, but its failure stage is unavailable or unrecognized. The raw error and connection log are the evidence.",
			"The recorded information does not establish a specific cause.",
			"Inspect the raw error and available log. Capture a new connection to collect more evidence, and compare direct and proxied access. Do not assume this is a certificate problem.",
		}
	}
}

// connectionDiagnostics stays useful for older sessions with an error but no
// log. Keep the raw log separately copyable; this report adds context and advice.
func (f flow) connectionDiagnostics() string {
	e := f.conn.explanation()
	var out strings.Builder
	fmt.Fprintf(&out, "vscroxy connection diagnostics\nRequest: %s %s\nClient app: %s\nRecorded status: %d\nFailed stage: %s\nRaw error: %s\n\n%s\n%s\n",
		f.method, cmp.Or(f.url, f.scheme+"://"+f.host+f.path), cmp.Or(f.process, "unknown"), f.status, cmp.Or(f.conn.errorStage, "unknown"), f.conn.error, e.title, e.summary)
	if f.kind == "connect" && f.status == 200 {
		fmt.Fprintln(&out, "CONNECT 200 acknowledges tunnel setup; it does not mean the later TLS handshake or target HTTP request succeeded.")
	}
	fmt.Fprintf(&out, "\nPossible causes (not confirmed)\n%s\n\nWhat to try\n%s\n\nConnection log\n%s\n", e.causes, e.steps, cmp.Or(f.conn.log, "No connection history was recorded."))
	return out.String()
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
