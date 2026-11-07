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
	// maxCaptureHeaders bounds the header fields read from one message. A
	// source with more is malformed or hostile; the extras are skipped and
	// counted in the report.
	maxCaptureHeaders = 1000
	// maxCaptureMessages bounds the WebSocket messages read from one flow.
	maxCaptureMessages = 100_000
	// maxCaptureArchive bounds the uncompressed members of one zip archive,
	// so a small file cannot expand without limit.
	maxCaptureArchive = 256 << 20
	// maxCaptureImportBytes bounds everything one import retains: bodies plus
	// WebSocket message bytes. A compressed file can expand far beyond its own
	// size, so per-body limits alone are not enough.
	maxCaptureImportBytes = 512 << 20
	// maxCaptureImportFrames bounds the WebSocket messages one import retains.
	maxCaptureImportFrames = 200_000
	// maxCaptureDecompressBytes bounds decompression work, not just retained
	// output. Oversized or corrupt bodies still cost CPU, so every attempt is
	// charged against this import-wide budget.
	maxCaptureDecompressBytes = 1 << 30
	// maxCaptureXMLDepth bounds element nesting before the real XML decoder
	// builds its element stack.
	maxCaptureXMLDepth = 64
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
// entry flood the report. It also carries the import-wide retention budget,
// because every parser already threads the report through.
type captureWarnings struct {
	lines  []string
	extra  int
	budget captureBudget
}

type captureBudget struct {
	maxBodyBytes   int64
	maxFrames      int
	maxWorkBytes   int64
	bodyBytes      int64
	frames         int
	workBytes      int64
	decompressions int
	bodyWarned     bool
	frameWarned    bool
	workWarned     bool
}

func (w *captureWarnings) add(format string, args ...any) {
	if len(w.lines) >= maxCaptureWarnings {
		w.extra++
		return
	}
	w.lines = append(w.lines, fmt.Sprintf(format, args...))
}

// prepend keeps a cap warning visible even when every capped entry also
// produced its own warning.
func (w *captureWarnings) prepend(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if len(w.lines) < maxCaptureWarnings {
		w.lines = append([]string{line}, w.lines...)
		return
	}
	w.lines = append([]string{line}, w.lines[:maxCaptureWarnings-1]...)
	w.extra++
}

func (w *captureWarnings) finish() []string {
	if w.extra == 0 {
		return w.lines
	}
	return append(w.lines, fmt.Sprintf("%d more issues were not listed", w.extra))
}

func (w *captureWarnings) bodyBudget() int64 {
	if w.budget.maxBodyBytes > 0 {
		return w.budget.maxBodyBytes
	}
	return maxCaptureImportBytes
}

func (w *captureWarnings) frameBudget() int {
	if w.budget.maxFrames > 0 {
		return w.budget.maxFrames
	}
	return maxCaptureImportFrames
}

// bodyLimit is the smaller of one body's cap and the import's remaining
// budget. decodeCaptureBody uses it as its output limit.
func (w *captureWarnings) bodyLimit() int64 {
	if w.budget.bodyWarned {
		return 0
	}
	remaining := w.bodyBudget() - w.budget.bodyBytes
	if remaining <= 0 {
		return 0
	}
	return min(remaining, int64(maxCaptureBody))
}

func (w *captureWarnings) takeBody(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	if w.budget.bodyBytes+int64(len(data)) > w.bodyBudget() {
		w.budgetBodyExhausted()
		return false
	}
	w.budget.bodyBytes += int64(len(data))
	return true
}

// refundBody releases a raw WebSocket body that was counted while it was
// decoded but is not retained as an HTTP body. Its frame payloads are counted
// separately by retainFrames.
func (w *captureWarnings) refundBody(n int) {
	if n <= 0 {
		return
	}
	w.budget.bodyBytes -= int64(n)
	if w.budget.bodyBytes < 0 {
		w.budget.bodyBytes = 0
	}
}

func (w *captureWarnings) workBudget() int64 {
	if w.budget.maxWorkBytes > 0 {
		return w.budget.maxWorkBytes
	}
	return maxCaptureDecompressBytes
}

func (w *captureWarnings) workRemaining() int64 {
	remaining := w.workBudget() - w.budget.workBytes
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (w *captureWarnings) chargeDecompression(n int64) {
	if n <= 0 {
		n = 1
	}
	w.budget.decompressions++
	w.budget.workBytes += n
	if w.budget.workBytes >= w.workBudget() {
		w.budgetWorkExhausted()
	}
}

func (w *captureWarnings) budgetWorkExhausted() {
	if w.budget.workWarned {
		return
	}
	w.budget.workWarned = true
	w.prepend("import decompression budget of %d MiB reached; remaining compressed bodies were not decoded", w.workBudget()>>20)
}

func (w *captureWarnings) budgetBodyExhausted() {
	if w.budget.bodyWarned {
		return
	}
	w.budget.bodyWarned = true
	w.prepend("import body budget of %d MiB reached; remaining bodies and messages were not retained", w.bodyBudget()>>20)
}

func (w *captureWarnings) frameAllowance() int {
	remaining := w.frameBudget() - w.budget.frames
	if remaining <= 0 {
		return 0
	}
	return remaining
}

// retainFrames applies both frame-count and byte budgets. Frame payloads share
// the body budget, so a stream of large messages cannot bypass it.
func (w *captureWarnings) retainFrames(frames []CaptureFrame) []CaptureFrame {
	if len(frames) == 0 {
		return frames
	}
	countLeft := w.frameAllowance()
	byteLeft := w.bodyBudget() - w.budget.bodyBytes
	if countLeft <= 0 {
		w.budgetFramesExhausted()
		return nil
	}
	if byteLeft <= 0 {
		w.budgetBodyExhausted()
		return nil
	}
	kept, bytes := 0, int64(0)
	for _, frame := range frames {
		if kept >= countLeft || int64(len(frame.Data)) > byteLeft {
			break
		}
		frames[kept] = frame
		kept++
		byteLeft -= int64(len(frame.Data))
		bytes += int64(len(frame.Data))
	}
	if kept < len(frames) {
		if kept >= countLeft {
			w.budgetFramesExhausted()
		}
		if int64(len(frames[kept].Data)) > byteLeft {
			w.budgetBodyExhausted()
		}
	}
	w.budget.frames += kept
	w.budget.bodyBytes += bytes
	return frames[:kept]
}

func (w *captureWarnings) budgetFramesExhausted() {
	if w.budget.frameWarned {
		return
	}
	w.budget.frameWarned = true
	w.prepend("import WebSocket message budget of %d reached; remaining messages were not imported", w.frameBudget())
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

// captureConnectURL is the authority a CONNECT tunnel targets. A tunnel has no
// path, and its port is part of the target even when it is the scheme default.
func captureConnectURL(scheme, host string, port int) string {
	if scheme != "http" && scheme != "https" {
		scheme = "https"
	}
	bareHost := strings.Trim(host, "[]")
	if h, _, err := net.SplitHostPort(host); err == nil {
		bareHost = h
	}
	if port > 0 {
		return scheme + "://" + net.JoinHostPort(bareHost, strconv.Itoa(port))
	}
	if ip := net.ParseIP(bareHost); ip != nil && strings.Contains(bareHost, ":") {
		return scheme + "://[" + bareHost + "]"
	}
	return scheme + "://" + bareHost
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
	// A server may answer before the upload finishes, and phases may overlap.
	// Total must cover every phase, not only the last one.
	for _, phase := range phases {
		if end := phase.Start + phase.Duration; end > total {
			total = end
		}
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

var errCaptureLimit = errors.New("capture data exceeds limit")

// readAllLimited reads at most limit bytes and reports when the source was
// longer, so a size declared by an untrusted file is never trusted blindly.
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: larger than %d MiB", errCaptureLimit, limit>>20)
	}
	return data, nil
}
