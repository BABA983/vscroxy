package imports

import (
	"bytes"
	"encoding/json/v2"
	"encoding/xml"
	"errors"
	"fmt"
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
	ErrorMessage    string            `json:"errorMessage"`
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
	FirstLine string             `json:"firstLine"`
	Headers   []charlesNameValue `json:"headers"`
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
	var transactions []charlesJSONTransaction
	if err := json.Unmarshal(data, &transactions); err != nil {
		return Capture{}, fmt.Errorf("%s: malformed Charles JSON session", name)
	}
	if len(transactions) == 0 {
		return Capture{}, fmt.Errorf("%s: the session has no transactions", name)
	}
	capture := Capture{Format: CaptureCharlesJSON}
	var warnings captureWarnings
	if len(transactions) > maxCaptureEntries {
		warnings.add("%d transactions beyond the first %d were not imported", len(transactions)-maxCaptureEntries, maxCaptureEntries)
		transactions = transactions[:maxCaptureEntries]
	}
	websocket := false
	for i, transaction := range transactions {
		flow, err := transaction.toCapture(&warnings, i)
		if err != nil {
			warnings.add("transaction %d skipped: %v", i+1, err)
			continue
		}
		websocket = websocket || flow.Kind == "ws"
		capture.Flows = append(capture.Flows, flow)
	}
	if websocket {
		warnings.add("WebSocket message frames are not part of Charles JSON exports; only the upgrade exchange was imported")
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
	}
	host := strings.TrimSpace(t.Host)
	if host == "" {
		return CaptureFlow{}, errors.New("no host")
	}
	path := t.Path
	if path == "" {
		path = "/"
	}
	port := int(t.ActualPort)
	if port <= 0 {
		port = int(t.Port)
	}
	kind := ""
	if t.Tunnel {
		kind = "connect"
		if method == "" || method == http.MethodGet {
			method = http.MethodConnect
		}
	}
	if t.WebSocket {
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
	if total <= 0 && !start.IsZero() && !end.IsZero() && end.After(start) {
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

	reqHeader := charlesHeaders(t.Request)
	resHeader := charlesHeaders(t.Response)
	reqBody, reqSize := charlesBody(t.Request, "request", warnings, index)
	resBody, resSize := charlesBody(t.Response, "response", warnings, index)
	status := 0
	if t.Response != nil {
		status = int(t.Response.Status)
	}
	failure := ""
	if status <= 0 && charlesFailedStatus(t.Status) {
		failure = "Charles reported " + strings.TrimSpace(t.Status)
	}
	if failure == "" {
		failure = strings.TrimSpace(t.ErrorMessage)
	}
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
		totalBytes = len(reqBody) + len(resBody)
	}
	flow := CaptureFlow{
		Method: method, Scheme: scheme, Host: host, Path: path,
		URL:    captureURL(scheme, host, port, path, t.Query),
		Status: status,
		At:     start, Millis: captureMillis(total), Wait: captureMillis(wait), Download: captureMillis(download),
		Timing:    charlesJSONTiming(start, requestBegin, requestComplete, responseBegin, end, total),
		ReqHeader: reqHeader, ResHeader: resHeader,
		ReqBody: reqBody, ResBody: resBody, ReqSize: reqSize, ResSize: resSize, Bytes: totalBytes,
		ReqProto: proto, ResProto: proto,
		Remote: remoteIP(t.RemoteAddress), Client: strings.TrimPrefix(strings.TrimSpace(t.ClientAddress), "/"),
		Kind: kind, Error: failure, Note: strings.TrimSpace(t.Note),
	}
	if t.SSL != nil {
		flow.TLS = strings.TrimSpace(t.SSL.Protocol)
		flow.Cipher = strings.TrimSpace(t.SSL.CipherSuite)
	}
	return flow, nil
}

func charlesHeaders(entry *charlesJSONEntry) http.Header {
	if entry == nil {
		return nil
	}
	pairs := make([][2]string, 0, len(entry.Header.Headers))
	for _, header := range entry.Header.Headers {
		pairs = append(pairs, [2]string{header.Name, header.Value})
	}
	h := captureHeader(pairs)
	if encoding := strings.TrimSpace(entry.ContentEncoding); encoding != "" && h.Get("Content-Encoding") == "" {
		h.Set("Content-Encoding", encoding)
	}
	if mime := strings.TrimSpace(entry.MimeType); mime != "" && h.Get("Content-Type") == "" {
		h.Set("Content-Type", mime)
	}
	return h
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

func charlesJSONTiming(start, requestBegin, requestComplete, responseBegin, end time.Time, total time.Duration) *CaptureTiming {
	if start.IsZero() {
		return nil
	}
	marks := []captureMark{
		{name: "Connection", from: start, to: requestBegin},
		{name: "Send", from: requestBegin, to: requestComplete},
		{name: "Wait", from: requestComplete, to: responseBegin},
		{name: "Receive", from: responseBegin, to: end},
	}
	return captureTiming(start, marks, total)
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

// charlesXMLSession is the root of Charles' XML session export (.chlsx).
type charlesXMLSession struct {
	XMLName      xml.Name                `xml:"charles-session"`
	Transactions []charlesXMLTransaction `xml:"transaction"`
}

type charlesXMLTransaction struct {
	Status           string           `xml:"status,attr"`
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
	FirstLine string             `xml:"first-line"`
	Headers   []charlesXMLHeader `xml:"header"`
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
	var session charlesXMLSession
	decoder := xml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&session); err != nil {
		return Capture{}, fmt.Errorf("%s: malformed Charles XML session", name)
	}
	if session.XMLName.Local != "charles-session" {
		return Capture{}, fmt.Errorf("%s: not a Charles XML session", name)
	}
	if len(session.Transactions) == 0 {
		return Capture{}, fmt.Errorf("%s: the session has no transactions", name)
	}
	capture := Capture{Format: CaptureCharlesXML}
	var warnings captureWarnings
	transactions := session.Transactions
	if len(transactions) > maxCaptureEntries {
		warnings.add("%d transactions beyond the first %d were not imported", len(transactions)-maxCaptureEntries, maxCaptureEntries)
		transactions = transactions[:maxCaptureEntries]
	}
	websocket := false
	for i, transaction := range transactions {
		flow, err := transaction.toCapture(&warnings, i)
		if err != nil {
			warnings.add("transaction %d skipped: %v", i+1, err)
			continue
		}
		websocket = websocket || flow.Kind == "ws"
		capture.Flows = append(capture.Flows, flow)
	}
	if websocket {
		warnings.add("WebSocket message frames are not part of Charles XML exports; only the upgrade exchange was imported")
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
	}
	host := strings.TrimSpace(t.Host)
	if host == "" {
		return CaptureFlow{}, errors.New("no host")
	}
	path := t.Path
	if path == "" {
		path = "/"
	}
	port := parseCaptureInt(t.ActualPort)
	if port <= 0 {
		port = parseCaptureInt(t.Port)
	}
	kind := ""
	if t.Tunnel {
		kind = "connect"
		if method == http.MethodGet {
			method = http.MethodConnect
		}
	}
	if t.WebSocket {
		kind = "ws"
		if scheme == "https" {
			scheme = "wss"
		} else if scheme == "http" {
			scheme = "ws"
		}
	}

	start := charlesXMLTime(t.StartTime, t.StartTimeMillis)
	requestBegin := charlesXMLTime(t.RequestBegin, t.RequestBeginMS)
	requestComplete := charlesXMLTime(t.RequestTime, t.RequestTimeMS)
	responseBegin := charlesXMLTime(t.ResponseTime, t.ResponseTimeMS)
	end := charlesXMLTime(t.EndTime, t.EndTimeMS)
	total := captureDurationMs(float64(parseCaptureInt(t.Duration)))
	if total <= 0 && !start.IsZero() && !end.IsZero() && end.After(start) {
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

	reqHeader := charlesXMLHeadersOf(t.Request)
	resHeader := charlesXMLHeadersOf(t.Response)
	reqBody, reqSize := charlesXMLBodyOf(t.Request, "request", warnings, index)
	resBody, resSize := charlesXMLBodyOf(t.Response, "response", warnings, index)
	status := 0
	if t.Response != nil {
		status = parseCaptureInt(t.Response.Status)
		if status == 0 && t.Response.Headers != nil {
			for _, header := range t.Response.Headers.Headers {
				if strings.EqualFold(header.Name, ":status") {
					status = parseCaptureInt(header.Value)
				}
			}
		}
	}
	failure := ""
	if status <= 0 && charlesFailedStatus(t.Status) {
		failure = "Charles reported " + strings.TrimSpace(t.Status)
	}
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
		totalBytes = len(reqBody) + len(resBody)
	}
	flow := CaptureFlow{
		Method: method, Scheme: scheme, Host: host, Path: path,
		URL:    captureURL(scheme, host, port, path, t.Query),
		Status: status,
		At:     start, Millis: captureMillis(total), Wait: captureMillis(wait), Download: captureMillis(download),
		Timing:    charlesXMLTiming(start, requestBegin, requestComplete, responseBegin, end, total),
		ReqHeader: reqHeader, ResHeader: resHeader,
		ReqBody: reqBody, ResBody: resBody, ReqSize: reqSize, ResSize: resSize, Bytes: totalBytes,
		ReqProto: proto, ResProto: proto,
		Remote: remoteIP(t.RemoteAddress), Client: strings.TrimPrefix(strings.TrimSpace(t.ClientAddress), "/"),
		Kind: kind, Error: failure,
	}
	if t.SSL != nil {
		flow.TLS = strings.TrimSpace(t.SSL.Protocol)
		flow.Cipher = strings.TrimSpace(t.SSL.CipherSuite)
	}
	return flow, nil
}

func charlesXMLHeadersOf(entry *charlesXMLEntry) http.Header {
	if entry == nil {
		return nil
	}
	var pairs [][2]string
	if entry.Headers != nil {
		pairs = make([][2]string, 0, len(entry.Headers.Headers))
		for _, header := range entry.Headers.Headers {
			pairs = append(pairs, [2]string{header.Name, header.Value})
		}
	}
	h := captureHeader(pairs)
	if encoding := strings.TrimSpace(entry.ContentEncoding); encoding != "" && h.Get("Content-Encoding") == "" {
		h.Set("Content-Encoding", encoding)
	}
	if mime := strings.TrimSpace(entry.MimeType); mime != "" && h.Get("Content-Type") == "" {
		h.Set("Content-Type", mime)
	}
	return h
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
	}
	if len(data) > maxCaptureBody {
		warnings.add("transaction %d: %s body is larger than %d MiB and was not imported", index+1, side, maxCaptureBody>>20)
		return nil, parseCaptureInt(entry.BodySize)
	}
	return data, captureBodySize(parseCaptureInt(entry.BodySize), data)
}

func charlesXMLTiming(start, requestBegin, requestComplete, responseBegin, end time.Time, total time.Duration) *CaptureTiming {
	if start.IsZero() {
		return nil
	}
	marks := []captureMark{
		{name: "Connection", from: start, to: requestBegin},
		{name: "Send", from: requestBegin, to: requestComplete},
		{name: "Wait", from: requestComplete, to: responseBegin},
		{name: "Receive", from: responseBegin, to: end},
	}
	return captureTiming(start, marks, total)
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
