package imports

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// charlesJSONTransaction is one entry of Charles' JSON session export
// (.chlsj). The format is an array of these. Field names and the "host/ip"
// remote address come from Charles' exporter; numbers may be written as
// strings by some versions, so the numeric fields accept both.
type charlesJSONTransaction struct {
	Status          string            `json:"status"`
	Method          string            `json:"method"`
	ProtocolVersion string            `json:"protocolVersion"`
	Scheme          string            `json:"scheme"`
	Host            string            `json:"host"`
	Port            flexInt           `json:"port"`
	ActualPort      flexInt           `json:"actualPort"`
	Path            string            `json:"path"`
	Query           string            `json:"query"`
	Tunnel          bool              `json:"tunnel"`
	WebSocket       bool              `json:"webSocket"`
	RemoteAddress   string            `json:"remoteAddress"`
	ClientAddress   string            `json:"clientAddress"`
	ClientPort      flexInt           `json:"clientPort"`
	Times           charlesJSONTimes  `json:"times"`
	Durations       charlesDurations  `json:"durations"`
	TotalSize       flexInt           `json:"totalSize"`
	SSL             *charlesJSONSSL   `json:"ssl"`
	ALPN            *charlesJSONALPN  `json:"alpn"`
	Exception       string            `json:"exception"`
	Note            string            `json:"notes"`
	Request         *charlesJSONEntry `json:"request"`
	Response        *charlesJSONEntry `json:"response"`
}

type charlesJSONTimes struct {
	Start           string `json:"start"`
	RequestBegin    string `json:"requestBegin"`
	RequestComplete string `json:"requestComplete"`
	ResponseBegin   string `json:"responseBegin"`
	End             string `json:"end"`
}

type charlesDurations struct {
	Total    flexFloat `json:"total"`
	DNS      flexFloat `json:"dns"`
	Connect  flexFloat `json:"connect"`
	SSL      flexFloat `json:"ssl"`
	Request  flexFloat `json:"request"`
	Response flexFloat `json:"response"`
	Latency  flexFloat `json:"latency"`
}

type charlesJSONSSL struct {
	Protocol    string `json:"protocol"`
	CipherSuite string `json:"cipherSuite"`
}

type charlesJSONALPN struct {
	Protocol string `json:"protocol"`
}

type charlesJSONEntry struct {
	Status          flexInt            `json:"status"`
	Sizes           charlesJSONSizes   `json:"sizes"`
	MimeType        string             `json:"mimeType"`
	Charset         string             `json:"charset"`
	ContentEncoding string             `json:"contentEncoding"`
	Header          charlesJSONHeaders `json:"header"`
	Body            *charlesJSONBody   `json:"body"`
}

type charlesJSONSizes struct {
	Headers flexInt `json:"headers"`
	Body    flexInt `json:"body"`
}

type charlesJSONHeaders struct {
	FirstLine string               `json:"firstLine"`
	Headers   charlesNameValueList `json:"headers"`
}

// charlesNameValueList caps the header array while it is decoded. A hostile
// export can contain a million empty headers; the extras are skipped instead
// of materialized, and the count reaches the import report.
type charlesNameValueList struct {
	Values  []charlesNameValue
	Omitted int
}

func (l *charlesNameValueList) UnmarshalJSON(data []byte) error {
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	token, err := decoder.ReadToken()
	if err != nil {
		return err
	}
	if token.Kind() != '[' {
		return errors.New("header list is not an array")
	}
	for decoder.PeekKind() != ']' {
		if decoder.PeekKind() == 0 {
			return io.ErrUnexpectedEOF
		}
		if len(l.Values) >= maxCaptureHeaders {
			if err := decoder.SkipValue(); err != nil {
				return err
			}
			l.Omitted++
			continue
		}
		var header charlesNameValue
		if err := json.UnmarshalDecode(decoder, &header); err != nil {
			return err
		}
		l.Values = append(l.Values, header)
	}
	_, err = decoder.ReadToken()
	return err
}

type charlesJSONBody struct {
	Text    string  `json:"text"`
	Encoded string  `json:"encoded"`
	Charset string  `json:"charset"`
	Mime    string  `json:"mimeType"`
	Size    flexInt `json:"size"`
	Decoded bool    `json:"decoded"`
}

type charlesNameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// flexInt accepts a JSON number or a numeric string. Charles writes some
// counters both ways, and -1 is its "not available" sentinel.
type flexInt int

func (n *flexInt) UnmarshalJSON(data []byte) error {
	value, err := flexNumber(data)
	if err != nil {
		return err
	}
	*n = flexInt(value)
	return nil
}

// flexFloat accepts a JSON number or a numeric string.
type flexFloat float64

func (n *flexFloat) UnmarshalJSON(data []byte) error {
	value, err := flexNumber(data)
	if err != nil {
		return err
	}
	*n = flexFloat(value)
	return nil
}

func flexNumber(data []byte) (float64, error) {
	value := strings.TrimSpace(string(data))
	if value == "" || value == "null" {
		return 0, nil
	}
	if value[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return 0, err
		}
		value = strings.TrimSpace(text)
	}
	if value == "" {
		return 0, nil
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, errors.New("not a number")
	}
	return number, nil
}

func readCharlesJSON(data []byte, name string) (Capture, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	token, err := decoder.ReadToken()
	if err != nil {
		return Capture{}, fmt.Errorf("%s: malformed Charles JSON session", name)
	}
	if token.Kind() != '[' {
		return Capture{}, fmt.Errorf("%s: not a Charles JSON session", name)
	}
	capture := Capture{Format: CaptureCharlesJSON}
	var warnings captureWarnings
	read, skipped := 0, 0
	for {
		kind := decoder.PeekKind()
		if kind == ']' {
			if _, err := decoder.ReadToken(); err != nil {
				return Capture{}, fmt.Errorf("%s: malformed Charles JSON session", name)
			}
			break
		}
		if kind == 0 {
			return Capture{}, fmt.Errorf("%s: malformed Charles JSON session", name)
		}
		if read >= maxCaptureEntries {
			if err := decoder.SkipValue(); err != nil {
				return Capture{}, fmt.Errorf("%s: malformed Charles JSON session", name)
			}
			skipped++
			continue
		}
		var transaction charlesJSONTransaction
		if err := json.UnmarshalDecode(decoder, &transaction); err != nil {
			return Capture{}, fmt.Errorf("%s: malformed Charles JSON session", name)
		}
		flow, err := transaction.toCapture(&warnings, read)
		read++
		if err != nil {
			warnings.add("transaction %d skipped: %v", read, err)
			continue
		}
		capture.Flows = append(capture.Flows, flow)
	}
	if _, err := decoder.ReadToken(); err != io.EOF {
		return Capture{}, fmt.Errorf("%s: malformed Charles JSON session", name)
	}
	if skipped > 0 {
		warnings.add("%d transactions beyond the first %d were not imported", skipped, maxCaptureEntries)
	}
	if read == 0 {
		return Capture{}, fmt.Errorf("%s: the session has no transactions", name)
	}
	capture.Warnings = warnings.finish()
	if len(capture.Flows) == 0 && len(capture.Warnings) == 0 {
		return Capture{}, fmt.Errorf("%s: no HTTP transaction could be read", name)
	}
	return capture, nil
}

func (t charlesJSONTransaction) toCapture(warnings *captureWarnings, index int) (CaptureFlow, error) {
	method := strings.ToUpper(strings.TrimSpace(t.Method))
	if method == "" {
		method = http.MethodGet
	}
	scheme := strings.ToLower(strings.TrimSpace(t.Scheme))
	if scheme == "" {
		scheme = "http"
		if method == http.MethodConnect || t.Tunnel {
			scheme = "https"
		}
	}
	host := strings.TrimSpace(t.Host)
	if host == "" {
		return CaptureFlow{}, errors.New("no host")
	}
	port := int(t.ActualPort)
	if port <= 0 {
		port = int(t.Port)
	}
	path := t.Path
	if path == "" {
		path = "/"
	}
	kind := ""
	connect := method == http.MethodConnect || t.Tunnel
	if connect {
		// A failed CONNECT can have tunnel=false and no path; the method still
		// names the tunnel. A tunnel URL is the authority only.
		method = http.MethodConnect
		kind = "connect"
		if scheme != "http" && scheme != "https" {
			scheme = "https"
		}
	} else if t.WebSocket {
		kind = "ws"
		if scheme == "https" {
			scheme = "wss"
		} else if scheme == "http" {
			scheme = "ws"
		}
	}

	start := firstTime(parseCharlesTime(t.Times.Start), parseCharlesTime(t.Times.RequestBegin))
	requestBegin := parseCharlesTime(t.Times.RequestBegin)
	requestComplete := parseCharlesTime(t.Times.RequestComplete)
	responseBegin := parseCharlesTime(t.Times.ResponseBegin)
	end := parseCharlesTime(t.Times.End)
	total := captureDurationMs(float64(t.Durations.Total))
	if total <= 0 && !requestBegin.IsZero() && !end.IsZero() && end.After(requestBegin) {
		total = end.Sub(requestBegin)
	} else if total <= 0 && !start.IsZero() && !end.IsZero() && end.After(start) {
		total = end.Sub(start)
	}
	wait := captureDurationMs(float64(t.Durations.Latency))
	if wait <= 0 && !requestComplete.IsZero() && !responseBegin.IsZero() && responseBegin.After(requestComplete) {
		wait = responseBegin.Sub(requestComplete)
	}
	download := captureDurationMs(float64(t.Durations.Response))
	if download <= 0 && !responseBegin.IsZero() && !end.IsZero() && end.After(responseBegin) {
		download = end.Sub(responseBegin)
	}

	reqHeader, reqOmitted := charlesHeaders(t.Request)
	resHeader, resOmitted := charlesHeaders(t.Response)
	if reqOmitted > 0 {
		warnings.add("transaction %d: %d request headers beyond the first %d were not imported", index+1, reqOmitted, maxCaptureHeaders)
	}
	if resOmitted > 0 {
		warnings.add("transaction %d: %d response headers beyond the first %d were not imported", index+1, resOmitted, maxCaptureHeaders)
	}
	reqBody, reqSize := charlesBody(t.Request, "request", warnings, index)
	resBody, resSize := charlesBody(t.Response, "response", warnings, index)
	bodyBytes := len(reqBody) + len(resBody)
	status := charlesJSONStatus(t.Response)
	failure := charlesFailure(t.Status, t.Exception)
	proto := strings.TrimSpace(t.ProtocolVersion)
	if proto == "" && t.ALPN != nil {
		switch strings.ToLower(strings.TrimSpace(t.ALPN.Protocol)) {
		case "h2":
			proto = "HTTP/2"
		case "http/1.1":
			proto = "HTTP/1.1"
		}
	}
	totalBytes := int(t.TotalSize)
	if totalBytes <= 0 {
		totalBytes = bodyBytes
	}
	url := captureURL(scheme, host, port, path, t.Query)
	if connect {
		url = captureConnectURL(scheme, host, port)
		path = ""
		reqBody, resBody, reqSize, resSize = nil, nil, 0, 0
	}
	flow := CaptureFlow{
		Method: method, Scheme: scheme, Host: host, Path: path, URL: url,
		Status: status,
		At:     start, Millis: captureMillis(total), Wait: captureMillis(wait), Download: captureMillis(download),
		Timing:    charlesJSONTiming(start, requestBegin, requestComplete, responseBegin, end, t.Durations, total),
		ReqHeader: reqHeader, ResHeader: resHeader,
		ReqBody: reqBody, ResBody: resBody, ReqSize: reqSize, ResSize: resSize, Bytes: totalBytes,
		ReqProto: proto, ResProto: proto,
		Remote: remoteIP(t.RemoteAddress), Client: remoteIP(t.ClientAddress),
		Kind: kind, Error: failure, Note: strings.TrimSpace(t.Note),
	}
	if kind == "ws" {
		flow.Frames, _ = charlesWebSocketFrames(reqBody, resBody, charlesJSONBodyEncoded(t.Request), charlesJSONBodyEncoded(t.Response), warnings, index)
		flow.ReqBody, flow.ResBody, flow.ReqSize, flow.ResSize = nil, nil, 0, 0
	}
	if t.SSL != nil {
		flow.TLS = strings.TrimSpace(t.SSL.Protocol)
		flow.Cipher = strings.TrimSpace(t.SSL.CipherSuite)
	}
	return flow, nil
}

func charlesJSONStatus(response *charlesJSONEntry) int {
	if response == nil {
		return 0
	}
	if response.Status > 0 {
		return int(response.Status)
	}
	for _, header := range response.Header.Headers.Values {
		if strings.EqualFold(header.Name, ":status") {
			if status := parseHTTPStatus(header.Value); status > 0 {
				return status
			}
		}
	}
	return statusFromFirstLine(response.Header.FirstLine)
}

// charlesFailure prefers the exception Charles recorded. A COMPLETE status can
// still carry one when the peer closed early.
func charlesFailure(status, exception string) string {
	if exception := strings.TrimSpace(exception); exception != "" {
		return exception
	}
	if charlesFailedStatus(status) {
		return "Charles reported " + strings.TrimSpace(status)
	}
	return ""
}

func parseHTTPStatus(value string) int {
	status, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || status < 100 || status > 999 {
		return 0
	}
	return status
}

func statusFromFirstLine(line string) int {
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.HasPrefix(strings.ToUpper(fields[0]), "HTTP/") {
		return 0
	}
	return parseHTTPStatus(fields[1])
}

func charlesHeaders(entry *charlesJSONEntry) (http.Header, int) {
	if entry == nil {
		return nil, 0
	}
	headers := entry.Header.Headers.Values
	pairs := make([][2]string, 0, len(headers))
	for _, header := range headers {
		pairs = append(pairs, [2]string{header.Name, header.Value})
	}
	h := captureHeader(pairs)
	if encoding := strings.TrimSpace(entry.ContentEncoding); encoding != "" && h.Get("Content-Encoding") == "" {
		h.Set("Content-Encoding", encoding)
	}
	if mime := strings.TrimSpace(entry.MimeType); mime != "" && h.Get("Content-Type") == "" {
		h.Set("Content-Type", mime)
	}
	return h, entry.Header.Headers.Omitted
}

func charlesBody(entry *charlesJSONEntry, side string, warnings *captureWarnings, index int) ([]byte, int) {
	if entry == nil {
		return nil, 0
	}
	if entry.Body == nil {
		return nil, int(entry.Sizes.Body)
	}
	if encoded := strings.TrimSpace(entry.Body.Encoded); encoded != "" {
		if len(encoded) > maxCaptureBody*4/3+16 {
			warnings.add("transaction %d: %s body is larger than %d MiB and was not imported", index+1, side, maxCaptureBody>>20)
			return nil, int(entry.Body.Size)
		}
		data, err := decodeCaptureBase64(encoded)
		if err != nil {
			warnings.add("transaction %d: %s body is not valid base64; body not imported", index+1, side)
			return nil, int(entry.Body.Size)
		}
		if len(data) > maxCaptureBody {
			warnings.add("transaction %d: %s body is larger than %d MiB and was not imported", index+1, side, maxCaptureBody>>20)
			return nil, int(entry.Body.Size)
		}
		if !entry.Body.Decoded {
			if decoded, err := decodeCaptureBody(data, charlesContentEncoding(entry)); err != nil {
				warnings.add("transaction %d: %s body is marked %s but could not be decompressed; original bytes kept", index+1, side, charlesContentEncoding(entry))
			} else {
				data = decoded
			}
		}
		return data, captureBodySize(int(entry.Body.Size), data)
	}
	if entry.Body.Text != "" {
		data := []byte(entry.Body.Text)
		if len(data) > maxCaptureBody {
			warnings.add("transaction %d: %s body is larger than %d MiB and was not imported", index+1, side, maxCaptureBody>>20)
			return nil, int(entry.Body.Size)
		}
		return data, captureBodySize(int(entry.Body.Size), data)
	}
	return nil, int(entry.Body.Size)
}

func charlesContentEncoding(entry *charlesJSONEntry) string {
	if entry == nil {
		return ""
	}
	if encoding := strings.TrimSpace(entry.ContentEncoding); encoding != "" {
		return encoding
	}
	for _, header := range entry.Header.Headers.Values {
		if strings.EqualFold(header.Name, "Content-Encoding") {
			return strings.TrimSpace(header.Value)
		}
	}
	return ""
}

func charlesJSONBodyEncoded(entry *charlesJSONEntry) bool {
	return entry != nil && entry.Body != nil && strings.TrimSpace(entry.Body.Encoded) != ""
}

func charlesXMLBodyEncoded(entry *charlesXMLEntry) bool {
	return entry != nil && entry.Body != nil && strings.EqualFold(strings.TrimSpace(entry.Body.Encoding), "base64")
}

// charlesWebSocketFrames reads the raw frame stream Charles stores in the
// encoded request and response bodies of a webSocket transaction. The frame
// bytes are not an HTTP body and must not be shown as one.
func charlesWebSocketFrames(requestBody, responseBody []byte, requestEncoded, responseEncoded bool, warnings *captureWarnings, index int) ([]CaptureFrame, bool) {
	var frames []CaptureFrame
	truncated := false
	for _, side := range []struct {
		name      string
		data      []byte
		encoded   bool
		direction string
	}{
		{name: "request", data: requestBody, encoded: requestEncoded, direction: "out"},
		{name: "response", data: responseBody, encoded: responseEncoded, direction: "in"},
	} {
		if !side.encoded {
			continue
		}
		parsed, cut := parseWebSocketFrames(side.data, side.direction)
		for _, frame := range parsed {
			if len(frames) >= maxCaptureMessages {
				cut = true
				break
			}
			frames = append(frames, frame)
		}
		truncated = truncated || cut
	}
	if len(frames) == 0 {
		warnings.add("transaction %d: WebSocket upgrade has no retained frames", index+1)
	}
	if truncated {
		warnings.add("transaction %d: WebSocket frames were truncated or malformed; the rest were not imported", index+1)
	}
	return frames, truncated
}

func charlesJSONTiming(start, requestBegin, requestComplete, responseBegin, end time.Time, durations charlesDurations, total time.Duration) *CaptureTiming {
	origin := requestBegin
	if origin.IsZero() {
		origin = start
	}
	if origin.IsZero() {
		return nil
	}
	dns := captureDurationMs(float64(durations.DNS))
	connect := captureDurationMs(float64(durations.Connect))
	tls := captureDurationMs(float64(durations.SSL))
	origin = origin.Add(-(dns + connect + tls))
	marks := make([]captureMark, 0, 7)
	if dns > 0 {
		marks = append(marks, captureMark{name: "DNS", from: origin, to: origin.Add(dns)})
	}
	if connect > 0 {
		from := origin.Add(dns)
		marks = append(marks, captureMark{name: "Connect", from: from, to: from.Add(connect)})
	}
	if tls > 0 {
		from := origin.Add(dns + connect)
		to := requestBegin
		if to.IsZero() || to.Before(from) {
			to = from.Add(tls)
		}
		marks = append(marks, captureMark{name: "TLS", from: from, to: to})
	}
	marks = append(marks,
		captureMark{name: "Send", from: requestBegin, to: requestComplete},
		captureMark{name: "Wait", from: requestComplete, to: responseBegin},
		captureMark{name: "Receive", from: responseBegin, to: end},
	)
	return captureTiming(origin, marks, total)
}

func parseCharlesTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000-07:00"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

func firstTime(times ...time.Time) time.Time {
	for _, t := range times {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

func charlesFailedStatus(status string) bool {
	status = strings.ToUpper(status)
	return strings.Contains(status, "FAIL") || strings.Contains(status, "ERROR") ||
		strings.Contains(status, "ABORT") || strings.Contains(status, "EXCEPTION") ||
		strings.Contains(status, "TIMEOUT")
}

type charlesXMLTransaction struct {
	Status           string           `xml:"status,attr"`
	Exception        string           `xml:"exception,attr"`
	Method           string           `xml:"method,attr"`
	ProtocolVersion  string           `xml:"protocolVersion,attr"`
	Protocol         string           `xml:"protocol,attr"`
	Host             string           `xml:"host,attr"`
	Port             string           `xml:"port,attr"`
	ActualPort       string           `xml:"actualPort,attr"`
	Path             string           `xml:"path,attr"`
	Query            string           `xml:"query,attr"`
	Tunnel           bool             `xml:"tunnel,attr"`
	WebSocket        bool             `xml:"webSocket,attr"`
	RemoteAddress    string           `xml:"remoteAddress,attr"`
	ClientAddress    string           `xml:"clientAddress,attr"`
	StartTime        string           `xml:"startTime,attr"`
	StartTimeMillis  string           `xml:"startTimeMillis,attr"`
	RequestBegin     string           `xml:"requestBeginTime,attr"`
	RequestBeginMS   string           `xml:"requestBeginTimeMillis,attr"`
	RequestTime      string           `xml:"requestTime,attr"`
	RequestTimeMS    string           `xml:"requestTimeMillis,attr"`
	ResponseTime     string           `xml:"responseTime,attr"`
	ResponseTimeMS   string           `xml:"responseTimeMillis,attr"`
	EndTime          string           `xml:"endTime,attr"`
	EndTimeMS        string           `xml:"endTimeMillis,attr"`
	Duration         string           `xml:"duration,attr"`
	DNSDuration      string           `xml:"dnsDuration,attr"`
	ConnectDuration  string           `xml:"connectDuration,attr"`
	SSLDuration      string           `xml:"sslDuration,attr"`
	RequestDuration  string           `xml:"requestDuration,attr"`
	ResponseDuration string           `xml:"responseDuration,attr"`
	Latency          string           `xml:"latency,attr"`
	TotalSize        string           `xml:"totalSize,attr"`
	SSL              *charlesXMLSSL   `xml:"ssl"`
	ALPN             *charlesXMLALPN  `xml:"alpn"`
	Request          *charlesXMLEntry `xml:"request"`
	Response         *charlesXMLEntry `xml:"response"`
}

type charlesXMLSSL struct {
	Protocol    string `xml:"protocol,attr"`
	CipherSuite string `xml:"cipherSuite,attr"`
}

type charlesXMLALPN struct {
	Protocol string `xml:"protocol,attr"`
}

type charlesXMLEntry struct {
	Status          string             `xml:"status,attr"`
	HeadersSize     string             `xml:"headers,attr"`
	BodySize        string             `xml:"body,attr"`
	MimeType        string             `xml:"mime-type,attr"`
	Charset         string             `xml:"charset,attr"`
	ContentEncoding string             `xml:"contentEncoding,attr"`
	Headers         *charlesXMLHeaders `xml:"headers"`
	Body            *charlesXMLBody    `xml:"body"`
}

type charlesXMLHeaders struct {
	FirstLine string
	Headers   []charlesXMLHeader
	Omitted   int
}

// UnmarshalXML caps the header list without materializing the whole element.
func (h *charlesXMLHeaders) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch token := token.(type) {
		case xml.StartElement:
			switch token.Name.Local {
			case "first-line":
				if err := decoder.DecodeElement(&h.FirstLine, &token); err != nil {
					return err
				}
			case "header":
				if len(h.Headers) >= maxCaptureHeaders {
					if err := decoder.Skip(); err != nil {
						return err
					}
					h.Omitted++
					continue
				}
				var header charlesXMLHeader
				if err := decoder.DecodeElement(&header, &token); err != nil {
					return err
				}
				h.Headers = append(h.Headers, header)
			default:
				if err := decoder.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if token.Name == start.Name {
				return nil
			}
		}
	}
}

type charlesXMLHeader struct {
	Name  string `xml:"name"`
	Value string `xml:"value"`
}

type charlesXMLBody struct {
	Encoding string `xml:"encoding,attr"`
	Data     string `xml:",chardata"`
}

func readCharlesXML(data []byte, name string) (Capture, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	found := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Capture{}, fmt.Errorf("%s: malformed Charles XML session", name)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != "charles-session" {
			return Capture{}, fmt.Errorf("%s: not a Charles XML session", name)
		}
		found = true
		break
	}
	if !found {
		return Capture{}, fmt.Errorf("%s: not a Charles XML session", name)
	}

	capture := Capture{Format: CaptureCharlesXML}
	var warnings captureWarnings
	read, skipped := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Capture{}, fmt.Errorf("%s: malformed Charles XML session", name)
		}
		switch token := token.(type) {
		case xml.StartElement:
			if token.Name.Local != "transaction" {
				if err := decoder.Skip(); err != nil {
					return Capture{}, fmt.Errorf("%s: malformed Charles XML session", name)
				}
				continue
			}
			if read >= maxCaptureEntries {
				if err := decoder.Skip(); err != nil {
					return Capture{}, fmt.Errorf("%s: malformed Charles XML session", name)
				}
				skipped++
				continue
			}
			var transaction charlesXMLTransaction
			if err := decoder.DecodeElement(&transaction, &token); err != nil {
				return Capture{}, fmt.Errorf("%s: malformed Charles XML session", name)
			}
			flow, err := transaction.toCapture(&warnings, read)
			read++
			if err != nil {
				warnings.add("transaction %d skipped: %v", read, err)
				continue
			}
			capture.Flows = append(capture.Flows, flow)
		case xml.EndElement:
			if token.Name.Local == "charles-session" {
				goto done
			}
		}
	}
done:
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Capture{}, fmt.Errorf("%s: malformed Charles XML session", name)
		}
		switch token := token.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(token)) != "" {
				return Capture{}, fmt.Errorf("%s: malformed Charles XML session", name)
			}
		case xml.Comment, xml.Directive, xml.ProcInst:
		default:
			return Capture{}, fmt.Errorf("%s: malformed Charles XML session", name)
		}
	}
	if skipped > 0 {
		warnings.add("%d transactions beyond the first %d were not imported", skipped, maxCaptureEntries)
	}
	if read == 0 {
		return Capture{}, fmt.Errorf("%s: the session has no transactions", name)
	}
	capture.Warnings = warnings.finish()
	if len(capture.Flows) == 0 && len(capture.Warnings) == 0 {
		return Capture{}, fmt.Errorf("%s: no HTTP transaction could be read", name)
	}
	return capture, nil
}

func (t charlesXMLTransaction) toCapture(warnings *captureWarnings, index int) (CaptureFlow, error) {
	method := strings.ToUpper(strings.TrimSpace(t.Method))
	if method == "" {
		method = http.MethodGet
	}
	scheme := strings.ToLower(strings.TrimSpace(t.Protocol))
	if scheme == "" {
		scheme = "http"
		if method == http.MethodConnect || t.Tunnel {
			scheme = "https"
		}
	}
	host := strings.TrimSpace(t.Host)
	if host == "" {
		return CaptureFlow{}, errors.New("no host")
	}
	port := parseCaptureInt(t.ActualPort)
	if port <= 0 {
		port = parseCaptureInt(t.Port)
	}
	path := t.Path
	if path == "" {
		path = "/"
	}
	kind := ""
	connect := method == http.MethodConnect || t.Tunnel
	if connect {
		method = http.MethodConnect
		kind = "connect"
		if scheme != "http" && scheme != "https" {
			scheme = "https"
		}
	} else if t.WebSocket {
		kind = "ws"
		if scheme == "https" {
			scheme = "wss"
		} else if scheme == "http" {
			scheme = "ws"
		}
	}

	start := firstTime(charlesXMLTime(t.StartTime, t.StartTimeMillis), charlesXMLTime(t.RequestBegin, t.RequestBeginMS))
	requestBegin := charlesXMLTime(t.RequestBegin, t.RequestBeginMS)
	requestComplete := charlesXMLTime(t.RequestTime, t.RequestTimeMS)
	responseBegin := charlesXMLTime(t.ResponseTime, t.ResponseTimeMS)
	end := charlesXMLTime(t.EndTime, t.EndTimeMS)
	total := captureDurationMs(float64(parseCaptureInt(t.Duration)))
	if total <= 0 && !requestBegin.IsZero() && !end.IsZero() && end.After(requestBegin) {
		total = end.Sub(requestBegin)
	} else if total <= 0 && !start.IsZero() && !end.IsZero() && end.After(start) {
		total = end.Sub(start)
	}
	wait := captureDurationMs(float64(parseCaptureInt(t.Latency)))
	if wait <= 0 && !requestComplete.IsZero() && !responseBegin.IsZero() && responseBegin.After(requestComplete) {
		wait = responseBegin.Sub(requestComplete)
	}
	download := captureDurationMs(float64(parseCaptureInt(t.ResponseDuration)))
	if download <= 0 && !responseBegin.IsZero() && !end.IsZero() && end.After(responseBegin) {
		download = end.Sub(responseBegin)
	}

	reqHeader, reqOmitted := charlesXMLHeadersOf(t.Request)
	resHeader, resOmitted := charlesXMLHeadersOf(t.Response)
	if reqOmitted > 0 {
		warnings.add("transaction %d: %d request headers beyond the first %d were not imported", index+1, reqOmitted, maxCaptureHeaders)
	}
	if resOmitted > 0 {
		warnings.add("transaction %d: %d response headers beyond the first %d were not imported", index+1, resOmitted, maxCaptureHeaders)
	}
	reqBody, reqSize := charlesXMLBodyOf(t.Request, "request", warnings, index)
	resBody, resSize := charlesXMLBodyOf(t.Response, "response", warnings, index)
	bodyBytes := len(reqBody) + len(resBody)
	status := charlesXMLStatus(t.Response)
	failure := charlesFailure(t.Status, t.Exception)
	proto := strings.TrimSpace(t.ProtocolVersion)
	if proto == "" && t.ALPN != nil {
		switch strings.ToLower(strings.TrimSpace(t.ALPN.Protocol)) {
		case "h2":
			proto = "HTTP/2"
		case "http/1.1":
			proto = "HTTP/1.1"
		}
	}
	totalBytes := parseCaptureInt(t.TotalSize)
	if totalBytes <= 0 {
		totalBytes = bodyBytes
	}
	url := captureURL(scheme, host, port, path, t.Query)
	if connect {
		url = captureConnectURL(scheme, host, port)
		path = ""
		reqBody, resBody, reqSize, resSize = nil, nil, 0, 0
	}
	flow := CaptureFlow{
		Method: method, Scheme: scheme, Host: host, Path: path, URL: url,
		Status: status,
		At:     start, Millis: captureMillis(total), Wait: captureMillis(wait), Download: captureMillis(download),
		Timing:    charlesXMLTiming(start, requestBegin, requestComplete, responseBegin, end, t, total),
		ReqHeader: reqHeader, ResHeader: resHeader,
		ReqBody: reqBody, ResBody: resBody, ReqSize: reqSize, ResSize: resSize, Bytes: totalBytes,
		ReqProto: proto, ResProto: proto,
		Remote: remoteIP(t.RemoteAddress), Client: remoteIP(t.ClientAddress),
		Kind: kind, Error: failure,
	}
	if kind == "ws" {
		flow.Frames, _ = charlesWebSocketFrames(reqBody, resBody, charlesXMLBodyEncoded(t.Request), charlesXMLBodyEncoded(t.Response), warnings, index)
		flow.ReqBody, flow.ResBody, flow.ReqSize, flow.ResSize = nil, nil, 0, 0
	}
	if t.SSL != nil {
		flow.TLS = strings.TrimSpace(t.SSL.Protocol)
		flow.Cipher = strings.TrimSpace(t.SSL.CipherSuite)
	}
	return flow, nil
}

func charlesXMLStatus(response *charlesXMLEntry) int {
	if response == nil {
		return 0
	}
	if status := parseCaptureInt(response.Status); status > 0 {
		return status
	}
	if response.Headers != nil {
		for _, header := range response.Headers.Headers {
			if strings.EqualFold(header.Name, ":status") {
				if status := parseHTTPStatus(header.Value); status > 0 {
					return status
				}
			}
		}
		return statusFromFirstLine(response.Headers.FirstLine)
	}
	return 0
}

func charlesXMLHeadersOf(entry *charlesXMLEntry) (http.Header, int) {
	if entry == nil {
		return nil, 0
	}
	var pairs [][2]string
	omitted := 0
	if entry.Headers != nil {
		pairs = make([][2]string, 0, len(entry.Headers.Headers))
		for _, header := range entry.Headers.Headers {
			pairs = append(pairs, [2]string{header.Name, header.Value})
		}
		omitted = entry.Headers.Omitted
	}
	h := captureHeader(pairs)
	if encoding := strings.TrimSpace(entry.ContentEncoding); encoding != "" && h.Get("Content-Encoding") == "" {
		h.Set("Content-Encoding", encoding)
	}
	if mime := strings.TrimSpace(entry.MimeType); mime != "" && h.Get("Content-Type") == "" {
		h.Set("Content-Type", mime)
	}
	return h, omitted
}

func charlesXMLBodyOf(entry *charlesXMLEntry, side string, warnings *captureWarnings, index int) ([]byte, int) {
	if entry == nil {
		return nil, 0
	}
	if entry.Body == nil {
		return nil, parseCaptureInt(entry.BodySize)
	}
	data := []byte(entry.Body.Data)
	if strings.EqualFold(strings.TrimSpace(entry.Body.Encoding), "base64") {
		if len(data) > maxCaptureBody*4/3+16 {
			warnings.add("transaction %d: %s body is larger than %d MiB and was not imported", index+1, side, maxCaptureBody>>20)
			return nil, parseCaptureInt(entry.BodySize)
		}
		decoded, err := decodeCaptureBase64(entry.Body.Data)
		if err != nil {
			warnings.add("transaction %d: %s body is not valid base64; body not imported", index+1, side)
			return nil, parseCaptureInt(entry.BodySize)
		}
		data = decoded
		if encoding := charlesXMLContentEncoding(entry); encoding != "" {
			if decoded, err := decodeCaptureBody(data, encoding); err != nil {
				warnings.add("transaction %d: %s body is marked %s but could not be decompressed; original bytes kept", index+1, side, encoding)
			} else {
				data = decoded
			}
		}
	}
	if len(data) > maxCaptureBody {
		warnings.add("transaction %d: %s body is larger than %d MiB and was not imported", index+1, side, maxCaptureBody>>20)
		return nil, parseCaptureInt(entry.BodySize)
	}
	return data, captureBodySize(parseCaptureInt(entry.BodySize), data)
}

func charlesXMLContentEncoding(entry *charlesXMLEntry) string {
	if entry == nil {
		return ""
	}
	if encoding := strings.TrimSpace(entry.ContentEncoding); encoding != "" {
		return encoding
	}
	if entry.Headers != nil {
		for _, header := range entry.Headers.Headers {
			if strings.EqualFold(header.Name, "Content-Encoding") {
				return strings.TrimSpace(header.Value)
			}
		}
	}
	return ""
}

func charlesXMLTiming(start, requestBegin, requestComplete, responseBegin, end time.Time, transaction charlesXMLTransaction, total time.Duration) *CaptureTiming {
	origin := requestBegin
	if origin.IsZero() {
		origin = start
	}
	if origin.IsZero() {
		return nil
	}
	dns := captureDurationMs(float64(parseCaptureInt(transaction.DNSDuration)))
	connect := captureDurationMs(float64(parseCaptureInt(transaction.ConnectDuration)))
	tls := captureDurationMs(float64(parseCaptureInt(transaction.SSLDuration)))
	origin = origin.Add(-(dns + connect + tls))
	marks := make([]captureMark, 0, 7)
	if dns > 0 {
		marks = append(marks, captureMark{name: "DNS", from: origin, to: origin.Add(dns)})
	}
	if connect > 0 {
		from := origin.Add(dns)
		marks = append(marks, captureMark{name: "Connect", from: from, to: from.Add(connect)})
	}
	if tls > 0 {
		from := origin.Add(dns + connect)
		to := requestBegin
		if to.IsZero() || to.Before(from) {
			to = from.Add(tls)
		}
		marks = append(marks, captureMark{name: "TLS", from: from, to: to})
	}
	marks = append(marks,
		captureMark{name: "Send", from: requestBegin, to: requestComplete},
		captureMark{name: "Wait", from: requestComplete, to: responseBegin},
		captureMark{name: "Receive", from: responseBegin, to: end},
	)
	return captureTiming(origin, marks, total)
}

func charlesXMLTime(value, millis string) time.Time {
	if ms := parseCaptureInt(millis); ms > 0 {
		return time.UnixMilli(int64(ms))
	}
	return parseCharlesTime(value)
}

func parseCaptureInt(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return number
}
