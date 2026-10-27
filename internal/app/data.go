package app

import (
	"cmp"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/capture"
)

// flow is one captured request/response pair, the unit the table shows and the
// detail panes inspect. It stands in for what the capture core will produce.
type flow struct {
	id          int
	debugKey    string // stable identity, independent of reused capture IDs
	debugRun    string // exact local send, empty for unrelated traffic
	live        bool   // only new proxy events; imported captures are not setup evidence
	replayOf    int
	actions     []string
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
	streamEnd   capture.StreamEnd
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

// requestDraft converts retained, decoded capture bytes, never display text.
// The text editor/JSON store cannot round-trip arbitrary non-UTF-8 bytes or
// request trailers; refuse those rather than silently changing the request.
func (f flow) requestDraft() (savedRequest, error) {
	if f.kind == "connect" || f.method == "CONNECT" {
		return savedRequest{}, fmt.Errorf("an encrypted tunnel has no captured HTTP request")
	}
	if f.reqError != "" {
		return savedRequest{}, fmt.Errorf("incomplete request body: %s", f.reqError)
	}
	body := f.reqBody
	if f.reqFile != "" {
		data, err := os.ReadFile(f.reqFile)
		if err != nil {
			return savedRequest{}, fmt.Errorf("request body unavailable: %w", err)
		}
		body = string(data)
	}
	if f.reqSize > 0 && len(body) != f.reqSize {
		return savedRequest{}, fmt.Errorf("incomplete request body: retained %d of %d bytes", len(body), f.reqSize)
	}
	if !utf8.ValidString(body) {
		return savedRequest{}, fmt.Errorf("non-UTF-8 request bodies cannot be opened in the text request editor")
	}
	h := f.reqHeader.Clone()
	if h == nil {
		h = make(http.Header)
		for _, pair := range headerPairs(f.reqHeaders, nil) {
			h.Add(pair.Name, pair.Value)
		}
	}
	if len(f.reqTrailer) != 0 || h.Get("Trailer") != "" {
		return savedRequest{}, fmt.Errorf("request trailers cannot be represented by the request editor")
	}
	// Capture stores decoded payloads but keeps original wire headers.
	h.Del("Content-Encoding")
	h.Del("Content-Length")
	h.Del("Transfer-Encoding")
	return savedRequest{Name: cmp.Or(f.path, f.url), Method: cmp.Or(f.method, "GET"), URL: f.url,
		Headers: headerBlock(h), Body: body}, nil
}

// body returns the body to show: the head the flow holds in memory, or the
// whole body from the spill file when the capture core wrote it out.
func (f flow) body(response bool) string {
	body, notice := f.bodyPreview(response)
	return body + notice
}

// bodyPreview keeps capture/display warnings out of the payload parsed by
// structured readers. Comparisons still include them through body.
func (f flow) bodyPreview(response bool) (body, notice string) {
	head, file, size, problem := f.reqBody, f.reqFile, f.reqSize, f.reqError
	if response {
		head, file, size, problem = f.resBody, f.resFile, f.resSize, f.resError
	}
	if file != "" {
		reader, err := os.Open(file)
		if err != nil {
			notice = fmt.Sprintf("\n\n… the spilled body is gone: %v", err)
		} else {
			defer reader.Close()
			data, err := io.ReadAll(io.LimitReader(reader, maxBodyView+1))
			head = string(data)
			if err != nil {
				problem = err.Error()
			}
		}
	}
	if len(head) > maxBodyView {
		head = head[:maxBodyView]
		notice += fmt.Sprintf("\n\n… truncated: %d bytes shown (recorded size: %d)", maxBodyView, size)
	} else if size > len(head) && problem == "" && notice == "" {
		notice = fmt.Sprintf("\n\n… incomplete body: %d of %d bytes retained", len(head), size)
	}
	if problem != "" {
		notice += "\n\n… incomplete body: " + problem
	}
	return head, notice
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
