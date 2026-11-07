package imports

import (
	"archive/zip"
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// proxymanEntry is one request_<n>_<id> member of a Proxyman log. The same
// JSON is used by .proxymanlogv2 archives and by the older .proxymanlog
// directory form.
type proxymanEntry struct {
	ID                      string             `json:"id"`
	Name                    string             `json:"name"`
	IsSSL                   bool               `json:"isSSL"`
	Request                 *proxymanRequest   `json:"request"`
	Response                *proxymanResponse  `json:"response"`
	Timing                  proxymanTiming     `json:"timing"`
	Summary                 proxymanSummary    `json:"summary"`
	ExtraInfo               *proxymanExtraInfo `json:"extraInfo"`
	WebSocketMessageStorage *proxymanWS        `json:"websocketMessageStorage"`
	Style                   *proxymanStyle     `json:"style"`
	Client                  *proxymanClient    `json:"client"`
}

type proxymanRequest struct {
	Scheme             string          `json:"scheme"`
	Port               flexInt         `json:"port"`
	IsSSL              bool            `json:"isSSL"`
	Version            proxymanVersion `json:"version"`
	FullPath           string          `json:"fullPath"`
	BodyData           string          `json:"bodyData"`
	URI                string          `json:"uri"`
	Header             proxymanHeader  `json:"header"`
	Host               string          `json:"host"`
	IsWebSocketUpgrade bool            `json:"isWebSocketUpgrade"`
	Method             proxymanMethod  `json:"method"`
}

type proxymanResponse struct {
	Status    *proxymanStatus `json:"status"`
	BodyData  string          `json:"bodyData"`
	Header    proxymanHeader  `json:"header"`
	CreatedAt flexFloat       `json:"createdAt"`
	Error     jsontext.Value  `json:"error"`
	Version   proxymanVersion `json:"version"`
}

type proxymanStatus struct {
	Code   flexInt `json:"code"`
	Phrase string  `json:"phrase"`
}

type proxymanMethod struct {
	Name string `json:"name"`
}

type proxymanVersion struct {
	Major flexInt `json:"major"`
	Minor flexInt `json:"minor"`
}

func (v proxymanVersion) String() string {
	if v.Major <= 0 {
		return ""
	}
	return fmt.Sprintf("HTTP/%d.%d", v.Major, v.Minor)
}

type proxymanHeader struct {
	Entries []proxymanHeaderEntry `json:"-"`
	Omitted int                   `json:"-"`
}

// UnmarshalJSON caps a header list while decoding it. A malformed export can
// name a million headers; the extras are skipped and counted for the report.
func (h *proxymanHeader) UnmarshalJSON(data []byte) error {
	var raw struct {
		Entries jsontext.Value `json:"entries"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(raw.Entries)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(trimmed))
	token, err := decoder.ReadToken()
	if err != nil {
		return err
	}
	if token.Kind() != '[' {
		return errors.New("header entries are not an array")
	}
	for decoder.PeekKind() != ']' {
		if decoder.PeekKind() == 0 {
			return io.ErrUnexpectedEOF
		}
		if len(h.Entries) >= maxCaptureHeaders {
			if err := decoder.SkipValue(); err != nil {
				return err
			}
			h.Omitted++
			continue
		}
		var entry proxymanHeaderEntry
		if err := json.UnmarshalDecode(decoder, &entry); err != nil {
			return err
		}
		h.Entries = append(h.Entries, entry)
	}
	_, err = decoder.ReadToken()
	return err
}

type proxymanHeaderEntry struct {
	Enabled *bool `json:"isEnabled"`
	Key     struct {
		Name      string `json:"name"`
		Lowercase string `json:"nameInLowercase"`
	} `json:"key"`
	Value string `json:"value"`
}

type proxymanTiming struct {
	RequestStartedAt  *float64 `json:"requestStartedAt"`
	RequestEndedAt    *float64 `json:"requestEndedAt"`
	ResponseStartedAt *float64 `json:"responseStartedAt"`
	ResponseEndedAt   *float64 `json:"responseEndedAt"`
}

type proxymanSummary struct {
	ServerIPAddress string  `json:"serverIpAddress"`
	ClientIPAddress string  `json:"clientIpAddress"`
	ServerPort      flexInt `json:"serverPort"`
	HostDNS         *string `json:"hostDNS"`
}

type proxymanExtraInfo struct {
	TLSVersion            string               `json:"tlsVersion"`
	CipherSuite           string               `json:"cipherSuite"`
	NegotiatedCipherSuite string               `json:"negotiatedCipherSuite"`
	CertificateData       *proxymanCertificate `json:"certificateData"`
}

type proxymanCertificate struct {
	CommonName     string `json:"commonName"`
	NotValidAfter  string `json:"notValidAfter"`
	IssueByPayload *struct {
		CommonName string `json:"commonName"`
		OrgName    string `json:"orgName"`
	} `json:"issueByPayload"`
}

type proxymanStyle struct {
	Comment string `json:"comment"`
}

type proxymanClient struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type proxymanWS struct {
	Messages []proxymanWSMessage `json:"-"`
	Omitted  int                 `json:"-"`
}

// UnmarshalJSON caps the retained WebSocket message list. The app cannot show
// a million frames, and materializing them first would defeat the entry limit.
func (s *proxymanWS) UnmarshalJSON(data []byte) error {
	var raw struct {
		Messages jsontext.Value `json:"messages"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(raw.Messages)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(trimmed))
	token, err := decoder.ReadToken()
	if err != nil {
		return err
	}
	if token.Kind() != '[' {
		return errors.New("WebSocket messages are not an array")
	}
	for decoder.PeekKind() != ']' {
		if decoder.PeekKind() == 0 {
			return io.ErrUnexpectedEOF
		}
		if len(s.Messages) >= maxCaptureMessages {
			if err := decoder.SkipValue(); err != nil {
				return err
			}
			s.Omitted++
			continue
		}
		var message proxymanWSMessage
		if err := json.UnmarshalDecode(decoder, &message); err != nil {
			return err
		}
		s.Messages = append(s.Messages, message)
	}
	_, err = decoder.ReadToken()
	return err
}

type proxymanWSMessage struct {
	From      string    `json:"from"`
	Type      string    `json:"type"`
	Data      string    `json:"data"`
	RawData   string    `json:"rawData"`
	CloseCode flexInt   `json:"closeCode"`
	Time      flexFloat `json:"time"`
	Injected  bool      `json:"injected"`
}

// proxymanSource is one entry file, opened only when it is read, so a
// directory or archive is not held in memory all at once.
type proxymanSource struct {
	index int
	name  string
	read  func() ([]byte, error)
}

func readProxymanZip(data []byte, name string) (Capture, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Capture{}, fmt.Errorf("%s: malformed Proxyman log archive", name)
	}
	var warnings captureWarnings
	var sources []proxymanSource
	var total uint64
	skipped := 0
	for _, member := range reader.File {
		index, ok := proxymanEntryIndex(member.Name)
		if !ok {
			continue
		}
		if strings.ContainsAny(member.Name, `/\`) || strings.Contains(member.Name, "..") {
			warnings.add("archive member %q was not read: entry names must be flat", member.Name)
			continue
		}
		if member.FileInfo().IsDir() || !member.Mode().IsRegular() {
			continue
		}
		if member.UncompressedSize64 > MaxCaptureFileSize {
			warnings.add("archive member %q is larger than %d MiB and was not read", member.Name, MaxCaptureFileSize>>20)
			continue
		}
		if member.UncompressedSize64 > maxCaptureArchive || total > maxCaptureArchive-member.UncompressedSize64 {
			return Capture{}, fmt.Errorf("%s: the archive expands beyond %d MiB", name, maxCaptureArchive>>20)
		}
		total += member.UncompressedSize64
		if len(sources) >= maxCaptureEntries {
			skipped++
			continue
		}
		member := member
		sources = append(sources, proxymanSource{
			index: index, name: member.Name,
			read: func() ([]byte, error) {
				file, err := member.Open()
				if err != nil {
					return nil, err
				}
				defer file.Close()
				return readAllLimited(file, MaxCaptureFileSize)
			},
		})
	}
	if len(sources) == 0 {
		return Capture{}, fmt.Errorf("%s: no request_ entries; Proxyman session archives (.proxymansessionv2) are not supported", name)
	}
	if skipped > 0 {
		warnings.add("%d entries beyond the first %d were not imported", skipped, maxCaptureEntries)
	}
	return readProxymanSources(sources, name, &warnings)
}

func proxymanDirFiles(dir string) ([]proxymanSource, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sources, err := proxymanSourcesIn(dir, entries)
	if err != nil {
		return nil, err
	}
	if len(sources) > 0 {
		return sources, nil
	}
	// Some v1 exports wrap the entries in one directory. Look exactly one
	// level down, never following a symlink or searching deeper.
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			continue
		}
		nestedEntries, err := os.ReadDir(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		nested, err := proxymanSourcesIn(filepath.Join(dir, entry.Name()), nestedEntries)
		if err != nil {
			return nil, err
		}
		if len(nested) > 0 {
			return nested, nil
		}
	}
	return nil, nil
}

// proxymanSourcesIn reads only the request_ files directly in dir.
func proxymanSourcesIn(dir string, entries []os.DirEntry) ([]proxymanSource, error) {
	var sources []proxymanSource
	var total int64
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		index, ok := proxymanEntryIndex(entry.Name())
		if !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > MaxCaptureFileSize {
			return nil, fmt.Errorf("%s: %s is larger than %d MiB", filepath.Base(dir), entry.Name(), MaxCaptureFileSize>>20)
		}
		if info.Size() > maxCaptureArchive || total > maxCaptureArchive-info.Size() {
			return nil, fmt.Errorf("%s: the log is larger than %d MiB", filepath.Base(dir), maxCaptureArchive>>20)
		}
		total += info.Size()
		filePath := filepath.Join(dir, entry.Name())
		sources = append(sources, proxymanSource{
			index: index, name: entry.Name(),
			read: func() ([]byte, error) {
				file, err := os.Open(filePath)
				if err != nil {
					return nil, err
				}
				defer file.Close()
				return readAllLimited(file, MaxCaptureFileSize)
			},
		})
	}
	return sources, nil
}

func readProxymanSources(sources []proxymanSource, name string, warnings *captureWarnings) (Capture, error) {
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].index < sources[j].index })
	if len(sources) > maxCaptureEntries {
		warnings.add("%d entries beyond the first %d were not imported", len(sources)-maxCaptureEntries, maxCaptureEntries)
		sources = sources[:maxCaptureEntries]
	}
	capture := Capture{Format: CaptureProxymanLog}
	for i, source := range sources {
		data, err := source.read()
		if err != nil {
			warnings.add("entry %s could not be read: %v", source.name, err)
			continue
		}
		var entry proxymanEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			warnings.add("entry %s is not valid Proxyman JSON", source.name)
			continue
		}
		flow, err := entry.toCapture(warnings, i)
		if err != nil {
			warnings.add("entry %s skipped: %v", source.name, err)
			continue
		}
		capture.Flows = append(capture.Flows, flow)
	}
	capture.Warnings = warnings.finish()
	if len(capture.Flows) == 0 && len(capture.Warnings) == 0 {
		return Capture{}, fmt.Errorf("%s: no Proxyman request could be read", name)
	}
	return capture, nil
}

func proxymanEntryIndex(name string) (int, bool) {
	base := path.Base(strings.ReplaceAll(name, `\`, "/"))
	if !strings.HasPrefix(base, "request_") {
		return 0, false
	}
	rest := strings.TrimPrefix(base, "request_")
	separator := strings.IndexByte(rest, '_')
	if separator <= 0 {
		return 0, false
	}
	index, err := strconv.Atoi(rest[:separator])
	if err != nil || index < 0 {
		return 0, false
	}
	return index, true
}

func (e proxymanEntry) toCapture(warnings *captureWarnings, index int) (CaptureFlow, error) {
	if e.Request == nil {
		return CaptureFlow{}, errors.New("no request")
	}
	request := e.Request
	scheme := strings.ToLower(strings.TrimSpace(request.Scheme))
	if scheme == "" {
		if e.IsSSL || request.IsSSL {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(request.Host)
	uri := request.URI
	if uri == "" {
		uri = "/"
	}
	fullPath := strings.TrimSpace(request.FullPath)
	if fullPath != "" {
		if parsed, ok := parseProxymanURL(fullPath); ok {
			if parsed.scheme != "" {
				scheme = strings.ToLower(parsed.scheme)
			}
			if parsed.host != "" {
				host = parsed.host
			}
			if parsed.uri != "" {
				uri = parsed.uri
			}
		}
	}
	if host == "" {
		return CaptureFlow{}, errors.New("no host")
	}
	port := int(request.Port)
	if port <= 0 {
		port = int(e.Summary.ServerPort)
	}
	kind := ""
	if request.IsWebSocketUpgrade || e.WebSocketMessageStorage != nil {
		kind = "ws"
		if scheme == "https" {
			scheme = "wss"
		} else if scheme == "http" {
			scheme = "ws"
		}
	}
	requestURL := fullPath
	if requestURL == "" {
		requestURL = captureURL(scheme, host, port, uri, "")
	} else if kind == "ws" {
		if parsed, err := url.Parse(requestURL); err == nil && parsed.Host != "" {
			parsed.Scheme = scheme
			requestURL = parsed.String()
		} else {
			requestURL = captureURL(scheme, host, port, uri, "")
		}
	}

	method := strings.ToUpper(strings.TrimSpace(request.Method.Name))
	if method == "" {
		method = http.MethodGet
	}
	reqHeader, reqOmitted := proxymanHeaders(request.Header)
	resHeader := http.Header(nil)
	resOmitted := 0
	status := 0
	if e.Response != nil {
		resHeader, resOmitted = proxymanHeaders(e.Response.Header)
		if e.Response.Status != nil {
			status = int(e.Response.Status.Code)
		}
	}
	if reqOmitted > 0 {
		warnings.add("entry %d: %d request headers beyond the first %d were not imported", index+1, reqOmitted, maxCaptureHeaders)
	}
	if resOmitted > 0 {
		warnings.add("entry %d: %d response headers beyond the first %d were not imported", index+1, resOmitted, maxCaptureHeaders)
	}
	reqBody := proxymanBody(request.BodyData, "request", warnings, index)
	resBody := []byte(nil)
	if e.Response != nil {
		resBody = proxymanBody(e.Response.BodyData, "response", warnings, index)
	}
	reqProto, resProto := request.Version.String(), ""
	if e.Response != nil {
		resProto = e.Response.Version.String()
	}
	if resProto == "" {
		resProto = reqProto
	}

	start := proxymanTime(e.Timing.RequestStartedAt)
	requestEnd := proxymanTime(e.Timing.RequestEndedAt)
	responseStart := proxymanTime(e.Timing.ResponseStartedAt)
	end := proxymanTime(e.Timing.ResponseEndedAt)
	if end.IsZero() && e.Response != nil && e.Response.CreatedAt > 0 {
		created := float64(e.Response.CreatedAt)
		end = proxymanTime(&created)
	}
	total := time.Duration(0)
	if !start.IsZero() && !end.IsZero() && end.After(start) {
		total = end.Sub(start)
	}
	wait := time.Duration(0)
	if !requestEnd.IsZero() && !responseStart.IsZero() && responseStart.After(requestEnd) {
		wait = responseStart.Sub(requestEnd)
	}
	download := time.Duration(0)
	if !responseStart.IsZero() && !end.IsZero() && end.After(responseStart) {
		download = end.Sub(responseStart)
	}
	if start.IsZero() {
		start = end
	}

	failure := ""
	if e.Response != nil {
		failure = proxymanError(e.Response.Error)
	}
	flow := CaptureFlow{
		Method: method, Scheme: scheme, Host: host, Path: uri, URL: requestURL,
		Status: status,
		At:     start, Millis: captureMillis(total), Wait: captureMillis(wait), Download: captureMillis(download),
		Timing:    proxymanTimingOf(start, requestEnd, responseStart, end, total),
		ReqHeader: reqHeader, ResHeader: resHeader,
		ReqBody: reqBody, ResBody: resBody, ReqSize: len(reqBody), ResSize: len(resBody),
		Bytes:    len(reqBody) + len(resBody),
		ReqProto: reqProto, ResProto: resProto,
		Remote: remoteIP(e.Summary.ServerIPAddress), Client: remoteIP(e.Summary.ClientIPAddress),
		Kind: kind, Error: failure,
	}
	if e.Style != nil {
		flow.Note = strings.TrimSpace(e.Style.Comment)
	}
	if e.Client != nil {
		flow.Process = strings.TrimSpace(e.Client.Name)
	}
	if e.ExtraInfo != nil {
		flow.TLS = strings.TrimSpace(e.ExtraInfo.TLSVersion)
		flow.Cipher = strings.TrimSpace(e.ExtraInfo.NegotiatedCipherSuite)
		if flow.Cipher == "" {
			flow.Cipher = strings.TrimSpace(e.ExtraInfo.CipherSuite)
		}
		if cert := e.ExtraInfo.CertificateData; cert != nil {
			flow.CertCN = strings.TrimSpace(cert.CommonName)
			flow.CertUntil = strings.TrimSpace(cert.NotValidAfter)
			if cert.IssueByPayload != nil {
				flow.Issuer = strings.TrimSpace(cert.IssueByPayload.OrgName)
				if flow.Issuer == "" {
					flow.Issuer = strings.TrimSpace(cert.IssueByPayload.CommonName)
				}
			}
		}
	}
	if kind == "ws" {
		flow.Frames = proxymanFrames(e.WebSocketMessageStorage, start, warnings, index)
		if e.WebSocketMessageStorage != nil && e.WebSocketMessageStorage.Omitted > 0 {
			warnings.add("entry %d: %d WebSocket messages beyond the first %d were not imported", index+1, e.WebSocketMessageStorage.Omitted, maxCaptureMessages)
		}
		for _, frame := range flow.Frames {
			flow.Bytes += len(frame.Data)
		}
		if len(flow.Frames) == 0 {
			warnings.add("entry %d: WebSocket upgrade has no retained messages", index+1)
		}
	}
	return flow, nil
}

type proxymanURL struct {
	scheme, host, uri string
}

func parseProxymanURL(value string) (proxymanURL, bool) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return proxymanURL{}, false
	}
	uri := parsed.RequestURI()
	if uri == "" {
		uri = "/"
	}
	return proxymanURL{scheme: parsed.Scheme, host: parsed.Host, uri: uri}, true
}

func proxymanHeaders(header proxymanHeader) (http.Header, int) {
	pairs := make([][2]string, 0, len(header.Entries))
	for _, entry := range header.Entries {
		if entry.Enabled != nil && !*entry.Enabled {
			continue
		}
		name := strings.TrimSpace(entry.Key.Name)
		if name == "" {
			name = strings.TrimSpace(entry.Key.Lowercase)
		}
		pairs = append(pairs, [2]string{name, entry.Value})
	}
	return captureHeader(pairs), header.Omitted
}

func proxymanBody(value, side string, warnings *captureWarnings, index int) []byte {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(value) > maxCaptureBody*4/3+16 {
		warnings.add("entry %d: %s body is larger than %d MiB and was not imported", index+1, side, maxCaptureBody>>20)
		return nil
	}
	data, err := decodeCaptureBase64(value)
	if err != nil {
		warnings.add("entry %d: %s body is not valid base64; body not imported", index+1, side)
		return nil
	}
	if len(data) > maxCaptureBody {
		warnings.add("entry %d: %s body is larger than %d MiB and was not imported", index+1, side, maxCaptureBody>>20)
		return nil
	}
	return data
}

func proxymanTimingOf(start, requestEnd, responseStart, end time.Time, total time.Duration) *CaptureTiming {
	if start.IsZero() {
		return nil
	}
	marks := []captureMark{
		{name: "Send", from: start, to: requestEnd},
		{name: "Wait", from: requestEnd, to: responseStart},
		{name: "Receive", from: responseStart, to: end},
	}
	return captureTiming(start, marks, total)
}

func proxymanTime(value *float64) time.Time {
	if value == nil || *value <= 0 || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return time.Time{}
	}
	// Proxyman writes Unix seconds as a float. Round to milliseconds so
	// binary floating point does not turn 0.18 into 0.1799999.
	return time.UnixMilli(int64(math.Round(*value * 1000))).UTC()
}

func proxymanFrames(storage *proxymanWS, fallback time.Time, warnings *captureWarnings, index int) []CaptureFrame {
	if storage == nil {
		return nil
	}
	frames := make([]CaptureFrame, 0, len(storage.Messages))
	for number, message := range storage.Messages {
		if number >= maxCaptureMessages {
			warnings.add("entry %d: WebSocket messages beyond the first %d were not imported", index+1, maxCaptureMessages)
			break
		}
		kind := strings.ToLower(strings.TrimSpace(message.Type))
		if kind == "" {
			kind = "text"
		}
		var data []byte
		if raw := strings.TrimSpace(message.RawData); raw != "" {
			decoded, err := decodeCaptureBase64(raw)
			if err != nil {
				warnings.add("entry %d: WebSocket message %d is not valid base64; message not imported", index+1, number+1)
				continue
			}
			data = decoded
		} else if message.Data != "" {
			data = []byte(message.Data)
			if kind == "binary" {
				if decoded, err := decodeCaptureBase64(message.Data); err == nil {
					data = decoded
				}
			}
		}
		if len(data) > maxCaptureBody {
			warnings.add("entry %d: WebSocket message %d is larger than %d MiB and was not imported", index+1, number+1, maxCaptureBody>>20)
			continue
		}
		if kind != "text" && kind != "binary" && kind != "close" && kind != "ping" && kind != "pong" {
			if utf8.Valid(data) {
				kind = "text"
			} else {
				kind = "binary"
			}
		}
		direction := "in"
		switch strings.ToLower(strings.TrimSpace(message.From)) {
		case "client", "out":
			direction = "out"
		}
		at := fallback
		if message.Time > 0 {
			value := float64(message.Time)
			at = proxymanTime(&value)
		}
		frames = append(frames, CaptureFrame{Direction: direction, Kind: kind, Data: data, At: at})
	}
	return frames
}

func proxymanError(value jsontext.Value) string {
	if len(bytes.TrimSpace(value)) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return ""
	}
	var decoded any
	if json.Unmarshal(value, &decoded) != nil {
		return "Proxyman recorded an error"
	}
	return proxymanErrorText(decoded)
}

func proxymanErrorText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		if message, ok := typed["message"].(string); ok && message != "" {
			return message
		}
		for _, key := range []string{"couldNotConnectToServer", "sslError", "sslHandshakeFailed", "timedOut", "cancelled", "invalidResponse"} {
			if nested, ok := typed[key]; ok {
				return proxymanErrorText(nested)
			}
		}
	}
	return "Proxyman recorded an error"
}
