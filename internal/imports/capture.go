package imports

// Capture sessions from other proxies: Charles' JSON and XML exports and
// Proxyman's log. Unlike the request importers in this package, these produce
// captured exchanges with both sides, bodies, timing and stream messages.
//
// The readers are deliberately strict about what they accept. A capture file
// is untrusted input: its size, entry count and body sizes are bounded, zip
// members are read by name without extracting, and symlinks are not followed.

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxCaptureFileSize bounds one capture file, and one JSON entry inside a
	// Proxyman folder or archive. A session is opened whole.
	MaxCaptureFileSize = 128 << 20
	// maxCaptureEntries bounds how many exchanges one import turns into rows.
	maxCaptureEntries = 2000
	// maxCaptureBody bounds one request or response body. A larger body is
	// left out and reported; the headers and timing still import.
	maxCaptureBody = 32 << 20
	// maxCaptureArchive bounds the uncompressed members of one zip archive,
	// so a small file cannot expand without limit.
	maxCaptureArchive = 256 << 20
	// maxCaptureWarnings bounds the import report. The rest are counted.
	maxCaptureWarnings = 20
)

// CaptureFormat names the source format of a parsed capture.
type CaptureFormat string

const (
	CaptureCharlesJSON CaptureFormat = "Charles JSON session"
	CaptureCharlesXML  CaptureFormat = "Charles XML session"
	CaptureProxymanLog CaptureFormat = "Proxyman log"
)

// ErrCharlesNative is returned for a native .chls session. Charles stores it
// as a Java serialization stream; reading it would mean deserializing an
// untrusted object graph, which this app does not do.
var ErrCharlesNative = errors.New("Charles .chls is a Java-serialized session, not a readable export; in Charles choose File → Export Session → JSON Session File (.chlsj) or HAR, then import that file")

// Capture is one parsed capture session.
type Capture struct {
	Format   CaptureFormat
	Flows    []CaptureFlow
	Warnings []string
}

// CaptureFlow is one captured exchange, with the bytes and timestamps the
// source export retained. Sizes are the body lengths actually present; when a
// source says it had a larger body that it did not export, Size says so and
// Body is empty.
type CaptureFlow struct {
	Method string
	Scheme string
	Host   string
	Path   string
	URL    string
	Status int

	At       time.Time
	Millis   int
	Wait     int
	Download int
	Timing   *CaptureTiming

	ReqHeader http.Header
	ResHeader http.Header
	ReqBody   []byte
	ResBody   []byte
	ReqSize   int
	ResSize   int
	Bytes     int

	ReqProto string
	ResProto string

	Remote    string
	Client    string
	Process   string
	TLS       string
	Cipher    string
	CertCN    string
	Issuer    string
	CertUntil string

	// Kind is "" for HTTP, "ws" for a WebSocket upgrade, or "connect" for a
	// tunnel transaction.
	Kind string
	// Error is a failure the source recorded instead of an HTTP response.
	Error string
	Note  string

	Frames []CaptureFrame
}

// CaptureFrame is one WebSocket message, when the source retained messages.
type CaptureFrame struct {
	Direction string // "out" for client to server, "in" for server to client
	Kind      string // "text", "binary", "close", "ping" or "pong"
	Data      []byte
	At        time.Time
}

// CaptureTiming is a request-local timeline in the source's own offsets.
type CaptureTiming struct {
	Total  time.Duration
	Phases []CaptureTimingPhase
}

// CaptureTimingPhase is one named interval on a CaptureTiming.
type CaptureTimingPhase struct {
	Name     string
	Start    time.Duration
	Duration time.Duration
}

// ReadCapture reads one Charles or Proxyman export. The format is detected
// from the bytes, not only the file name, so a renamed export still opens.
func ReadCapture(data []byte, name string) (Capture, error) {
	if len(data) == 0 {
		return Capture{}, fmt.Errorf("%s: the file is empty", name)
	}
	if len(data) > MaxCaptureFileSize {
		return Capture{}, fmt.Errorf("%s: the file is larger than %d MiB", name, MaxCaptureFileSize>>20)
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if isJavaSerialized(data) {
		return Capture{}, fmt.Errorf("%s: %w", name, ErrCharlesNative)
	}
	trimmed := bytes.TrimSpace(data)
	switch {
	case len(trimmed) > 0 && trimmed[0] == '[':
		return readCharlesJSON(data, name)
	case len(trimmed) > 0 && trimmed[0] == '<':
		return readCharlesXML(data, name)
	case len(trimmed) >= 4 && bytes.Equal(trimmed[:2], []byte("PK")):
		return readProxymanZip(data, name)
	default:
		if strings.EqualFold(filepath.Ext(name), ".chls") {
			return Capture{}, fmt.Errorf("%s: %w", name, ErrCharlesNative)
		}
		return Capture{}, fmt.Errorf("%s: not a Charles .chlsj/.chlsx or Proxyman .proxymanlogv2 capture", name)
	}
}

// isJavaSerialized recognizes the Java Object Serialization Stream Protocol
// magic. Charles .chls files start with it.
func isJavaSerialized(data []byte) bool {
	return len(data) >= 4 && data[0] == 0xAC && data[1] == 0xED && data[2] == 0x00 && data[3] == 0x05
}

// ReadProxymanDir reads the older Proxyman log, which is a directory of
// request_<n>_<id> JSON files rather than one archive.
func ReadProxymanDir(path string) (Capture, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Capture{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Capture{}, fmt.Errorf("%s: choose a real directory, not a symbolic link", filepath.Base(path))
	}
	if !info.IsDir() {
		return Capture{}, fmt.Errorf("%s: not a Proxyman log directory", filepath.Base(path))
	}
	files, err := proxymanDirFiles(path)
	if err != nil {
		return Capture{}, err
	}
	if len(files) == 0 {
		return Capture{}, fmt.Errorf("%s: no request_ entries in this Proxyman log directory", filepath.Base(path))
	}
	return readProxymanSources(files, path, &captureWarnings{})
}

// captureWarnings collects what an import left out without letting a malformed
// entry flood the report.
type captureWarnings struct {
	lines []string
	extra int
}

func (w *captureWarnings) add(format string, args ...any) {
	if len(w.lines) >= maxCaptureWarnings {
		w.extra++
		return
	}
	w.lines = append(w.lines, fmt.Sprintf(format, args...))
}

func (w *captureWarnings) finish() []string {
	if w.extra == 0 {
		return w.lines
	}
	return append(w.lines, fmt.Sprintf("%d more issues were not listed", w.extra))
}

// captureHeader keeps every enabled header and every repeat, dropping HTTP/2
// pseudo-headers that describe the wire rather than a header field.
func captureHeader(pairs [][2]string) http.Header {
	h := make(http.Header, len(pairs))
	for _, pair := range pairs {
		name := strings.TrimSpace(pair[0])
		if name == "" || strings.HasPrefix(name, ":") {
			continue
		}
		h.Add(name, pair[1])
	}
	return h
}

// captureURL joins the parts a source records. It keeps the path exactly as
// exported, including a double slash some Charles exports contain.
func captureURL(scheme, host string, port int, path, query string) string {
	if scheme == "" {
		scheme = "http"
	}
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	bareHost := strings.Trim(host, "[]")
	authority := host
	if ip := net.ParseIP(bareHost); ip != nil && strings.Contains(bareHost, ":") {
		authority = "[" + bareHost + "]"
	}
	if port > 0 && !(scheme == "http" && port == 80) && !(scheme == "https" && port == 443) &&
		!(scheme == "ws" && port == 80) && !(scheme == "wss" && port == 443) {
		authority = net.JoinHostPort(bareHost, strconv.Itoa(port))
	}
	if query = strings.TrimPrefix(query, "?"); query != "" {
		path += "?" + query
	}
	return scheme + "://" + authority + path
}

// remoteIP takes the address from Charles' "host/ip" form and drops brackets
// around an IPv6 literal.
func remoteIP(value string) string {
	value = strings.TrimSpace(value)
	if i := strings.LastIndex(value, "/"); i >= 0 {
		value = value[i+1:]
	}
	value = strings.Trim(value, "[]")
	if ip := net.ParseIP(value); ip != nil {
		return ip.String()
	}
	return value
}

// captureBodySize is a source's declared body length, kept only when the body
// itself is absent so the inspector can say how much was not exported.
func captureBodySize(declared int, body []byte) int {
	if declared > len(body) {
		return declared
	}
	return len(body)
}

func captureMillis(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(math.Round(float64(d) / float64(time.Millisecond)))
}

func captureDurationMs(ms float64) time.Duration {
	if ms <= 0 || math.IsNaN(ms) || math.IsInf(ms, 0) {
		return 0
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// captureTiming builds a timeline only from timestamps that are present and
// ordered; it never invents an offset for a phase the source did not record.
func captureTiming(start time.Time, marks []captureMark, total time.Duration) *CaptureTiming {
	if start.IsZero() {
		return nil
	}
	var phases []CaptureTimingPhase
	for _, mark := range marks {
		if mark.from.IsZero() || mark.to.IsZero() || mark.to.Before(mark.from) {
			continue
		}
		from, to := mark.from.Sub(start), mark.to.Sub(start)
		if from < 0 {
			continue
		}
		phases = append(phases, CaptureTimingPhase{Name: mark.name, Start: from, Duration: to - from})
	}
	if len(phases) == 0 {
		return nil
	}
	last := phases[len(phases)-1]
	if end := last.Start + last.Duration; end > total {
		total = end
	}
	if total <= 0 {
		return nil
	}
	return &CaptureTiming{Total: total, Phases: phases}
}

type captureMark struct {
	name     string
	from, to time.Time
}

// decodeCaptureBase64 decodes a body a source wrote as base64. It accepts the
// padded and unpadded standard and URL alphabets so one exporter's choice does
// not lose the body.
func decodeCaptureBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if data, err := encoding.DecodeString(value); err == nil {
			return data, nil
		}
	}
	return nil, errors.New("not valid base64")
}

// readAllLimited reads at most limit bytes and reports when the source was
// longer, so a size declared by an untrusted file is never trusted blindly.
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("larger than %d MiB", limit>>20)
	}
	return data, nil
}
