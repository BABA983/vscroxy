package main

import (
	"cmp"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/baba983/vscroxy/desktop/internal/capture"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// harFile is the HAR 1.2 document the Export HAR command writes.
type harFile struct {
	Log harLog `json:"log"`
}

type harLog struct {
	Version string     `json:"version"`
	Creator harCreator `json:"creator"`
	Entries []harEntry `json:"entries"`
}

type harCreator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type harEntry struct {
	StartedDateTime string      `json:"startedDateTime"`
	Time            float64     `json:"time"`
	Request         harRequest  `json:"request"`
	Response        harResponse `json:"response"`
	Cache           struct{}    `json:"cache"`
	Timings         harTimings  `json:"timings"`
}

type harRequest struct {
	Method      string         `json:"method"`
	URL         string         `json:"url"`
	HTTPVersion string         `json:"httpVersion"`
	Cookies     []struct{}     `json:"cookies"`
	Headers     []harNameValue `json:"headers"`
	QueryString []harNameValue `json:"queryString"`
	PostData    *harPostData   `json:"postData,omitempty"`
	HeadersSize int            `json:"headersSize"`
	BodySize    int            `json:"bodySize"`
}

type harResponse struct {
	Status      int            `json:"status"`
	StatusText  string         `json:"statusText"`
	HTTPVersion string         `json:"httpVersion"`
	Cookies     []struct{}     `json:"cookies"`
	Headers     []harNameValue `json:"headers"`
	Content     harContent     `json:"content"`
	RedirectURL string         `json:"redirectURL"`
	HeadersSize int            `json:"headersSize"`
	BodySize    int            `json:"bodySize"`
}

type harContent struct {
	Size     int    `json:"size"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text,omitempty"`
	// Encoding is "base64" when Text is not plain text, as other tools write
	// binary bodies.
	Encoding string `json:"encoding,omitempty"`
}

type harPostData struct {
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}

type harNameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type harTimings struct {
	Send    float64 `json:"send"`
	Wait    float64 `json:"wait"`
	Receive float64 `json:"receive"`
}

// harFromFlows builds a HAR document from the HTTP flows. WebSocket and SSE
// flows are left out: HAR has no place for their messages.
func harFromFlows(flows []flow) harFile {
	entries := make([]harEntry, 0, len(flows))
	for _, f := range flows {
		if f.kind != "" {
			continue
		}
		started := f.at
		if started.IsZero() {
			started = time.Now()
		}
		started = started.Add(-time.Duration(f.millis) * time.Millisecond)
		reqHeaders := headerPairs(f.reqHeaders, f.reqHeader)
		respHeaders := headerPairs(f.resHeaders, f.respHeader)
		reqText := f.body(false)
		respText := f.body(true)
		reqSize, respSize := f.reqSize, f.resSize
		if reqSize == 0 {
			reqSize = len(reqText)
		}
		if respSize == 0 {
			respSize = len(respText)
		}
		entry := harEntry{
			StartedDateTime: started.Format(time.RFC3339Nano),
			Time:            float64(f.millis),
			Request: harRequest{
				Method:      f.method,
				URL:         f.url,
				HTTPVersion: "HTTP/1.1",
				Headers:     reqHeaders,
				QueryString: queryPairs(f.url),
				HeadersSize: -1,
				BodySize:    reqSize,
			},
			Response: harResponse{
				Status:      f.status,
				StatusText:  http.StatusText(f.status),
				HTTPVersion: "HTTP/1.1",
				Headers:     respHeaders,
				Content: harContent{
					Size:     respSize,
					MimeType: headerValue(respHeaders, "Content-Type"),
					Text:     respText,
				},
				HeadersSize: -1,
				BodySize:    respSize,
			},
			Timings: harTimings{Wait: float64(f.millis)},
		}
		if reqText != "" {
			entry.Request.PostData = &harPostData{
				MimeType: headerValue(reqHeaders, "Content-Type"),
				Text:     reqText,
			}
		}
		entries = append(entries, entry)
	}
	return harFile{Log: harLog{
		Version: "1.2",
		Creator: harCreator{Name: "vscroxy", Version: "0.1.0"},
		Entries: entries,
	}}
}

// readHAR reads a HAR 1.2 file, so a capture another tool wrote can be
// inspected here: the URL, the headers, both bodies and the time each took.
func readHAR(path string) ([]flow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc harFile
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Log.Entries) == 0 {
		return nil, fmt.Errorf("%s: no entries", path)
	}
	flows := make([]flow, 0, len(doc.Log.Entries))
	for _, entry := range doc.Log.Entries {
		u, err := url.Parse(entry.Request.URL)
		if err != nil || u.Host == "" {
			continue
		}
		started, err := time.Parse(time.RFC3339Nano, entry.StartedDateTime)
		if err != nil {
			started = time.Time{}
		}
		reqHeader := harHeaders(entry.Request.Headers)
		respHeader := harHeaders(entry.Response.Headers)
		reqBody := ""
		if entry.Request.PostData != nil {
			reqBody = entry.Request.PostData.Text
		}
		resBody := harBody(entry.Response.Content)
		size := entry.Response.Content.Size
		if size == 0 {
			size = len(resBody)
		}
		f := flow{
			method:     strings.ToUpper(cmp.Or(entry.Request.Method, "GET")),
			scheme:     u.Scheme,
			host:       u.Host,
			path:       u.RequestURI(),
			url:        entry.Request.URL,
			status:     entry.Response.Status,
			bytes:      size,
			millis:     int(entry.Time),
			reqHeaders: headerBlock(reqHeader),
			resHeaders: headerBlock(respHeader),
			reqBody:    reqBody,
			resBody:    resBody,
			reqSize:    len(reqBody),
			resSize:    len(resBody),
			reqHeader:  reqHeader,
			respHeader: respHeader,
			at:         started,
		}
		f.wait = int(entry.Timings.Wait)
		f.download = int(entry.Timings.Receive)
		flows = append(flows, f)
	}
	if len(flows) == 0 {
		return nil, fmt.Errorf("%s: no request with a URL", path)
	}
	return flows, nil
}

// harHeaders turns a HAR header list into the header map the app uses.
func harHeaders(pairs []harNameValue) http.Header {
	h := http.Header{}
	for _, pair := range pairs {
		if name := strings.TrimSpace(pair.Name); name != "" {
			h.Add(name, pair.Value)
		}
	}
	return h
}

// harBody is a response body, decoded when the file carries it as base64.
func harBody(content harContent) string {
	if !strings.EqualFold(content.Encoding, "base64") {
		return content.Text
	}
	decoded, err := base64.StdEncoding.DecodeString(content.Text)
	if err != nil {
		return content.Text
	}
	return string(decoded)
}

func writeHAR(path string, flows []flow) error {
	data, err := json.Marshal(harFromFlows(flows))
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// headerPairs prefers the structured headers, and parses the formatted block
// for flows that only have that.
func headerPairs(formatted string, structured http.Header) []harNameValue {
	if len(structured) > 0 {
		pairs := make([]harNameValue, 0, len(structured))
		for _, key := range slices.Sorted(maps.Keys(structured)) {
			for _, value := range structured[key] {
				pairs = append(pairs, harNameValue{Name: key, Value: value})
			}
		}
		return pairs
	}
	var pairs []harNameValue
	for i, line := range strings.Split(formatted, "\n") {
		if i == 0 || line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		pairs = append(pairs, harNameValue{Name: name, Value: value})
	}
	return pairs
}

func headerValue(pairs []harNameValue, name string) string {
	for _, pair := range pairs {
		if strings.EqualFold(pair.Name, name) {
			return pair.Value
		}
	}
	return ""
}

func queryPairs(raw string) []harNameValue {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	var pairs []harNameValue
	for _, key := range slices.Sorted(maps.Keys(u.Query())) {
		for _, value := range u.Query()[key] {
			pairs = append(pairs, harNameValue{Name: key, Value: value})
		}
	}
	return pairs
}

// sessionFile is the saved session: the flows and their WebSocket or SSE
// messages, enough to reopen the table later. A message keeps the whole of
// what the capture retained, spilled or not, so a reopened session shows the
// same payload the live one did.
type sessionFile struct {
	Version int                        `json:"version"`
	Flows   []sessionFlow              `json:"flows"`
	Frames  map[int][]sessionFrameData `json:"frames,omitempty"`
}

type sessionFlow struct {
	ID              int               `json:"id"`
	DebugKey        string            `json:"debugKey,omitempty"`
	DebugRun        string            `json:"debugRun,omitempty"`
	ReplayOf        int               `json:"replayOf,omitzero"`
	Actions         []string          `json:"actions,omitempty"`
	Method          string            `json:"method"`
	Scheme          string            `json:"scheme"`
	Host            string            `json:"host"`
	Path            string            `json:"path"`
	URL             string            `json:"url"`
	Status          int               `json:"status"`
	Bytes           int               `json:"bytes"`
	Millis          int               `json:"millis"`
	ReqHeaders      string            `json:"reqHeaders"`
	ResHeaders      string            `json:"resHeaders"`
	ReqBody         string            `json:"reqBody"`
	ResBody         string            `json:"resBody"`
	ReqBytes        []byte            `json:"reqBytes,omitempty"`
	ResBytes        []byte            `json:"resBytes,omitempty"`
	ReqSize         int               `json:"reqSize,omitzero"`
	ResSize         int               `json:"resSize,omitzero"`
	ReqError        string            `json:"reqError,omitempty"`
	ResError        string            `json:"resError,omitempty"`
	ReqHeader       http.Header       `json:"reqHeader,omitempty"`
	RespHeader      http.Header       `json:"respHeader,omitempty"`
	ReqTrailer      http.Header       `json:"reqTrailer,omitempty"`
	ResTrailer      http.Header       `json:"resTrailer,omitempty"`
	ReqProto        string            `json:"reqProto,omitempty"`
	ResProto        string            `json:"resProto,omitempty"`
	ConnectionLog   string            `json:"connectionLog,omitempty"`
	ConnectionError string            `json:"connectionError,omitempty"`
	ConnectionStage string            `json:"connectionStage,omitempty"`
	At              time.Time         `json:"at"`
	Kind            string            `json:"kind,omitempty"`
	StreamEnd       capture.StreamEnd `json:"streamEnd,omitzero"`
	// Color and Note are what a person marked the flow with.
	Color string `json:"color,omitempty"`
	Note  string `json:"note,omitempty"`
}

// sessionFrameData is one message of a saved session: the whole of what the
// capture retained of it, and what says what it is and where it came in the
// stream. A file another version wrote has only the first three fields.
type sessionFrameData struct {
	Seq       int       `json:"seq,omitzero"`
	Direction string    `json:"direction"`
	Kind      string    `json:"kind"`
	Data      string    `json:"data"`
	Payload   []byte    `json:"payload,omitempty"`
	Size      int       `json:"size,omitzero"`
	At        time.Time `json:"at,omitzero"`
	EventID   string    `json:"eventId,omitempty"`
	EventName string    `json:"eventName,omitempty"`
}

func sessionFrom(flows []flow, frames map[int][]frame) sessionFile {
	s := sessionFile{Version: 1, Flows: make([]sessionFlow, 0, len(flows))}
	for _, f := range flows {
		// Save payloads, not the UI's bounded preview or its display notices.
		for _, side := range []struct {
			body    *string
			file    string
			problem *string
		}{{&f.reqBody, f.reqFile, &f.reqError}, {&f.resBody, f.resFile, &f.resError}} {
			if side.file == "" {
				continue
			}
			data, err := os.ReadFile(side.file)
			if err != nil {
				*side.problem = "read spilled body: " + err.Error()
				continue
			}
			*side.body = string(data)
		}
		saved := sessionFlow{
			ID: f.id, Method: f.method, Scheme: f.scheme, Host: f.host, Path: f.path, URL: f.url,
			DebugKey: f.debugKey, DebugRun: f.debugRun,
			ReplayOf: f.replayOf,
			Actions:  f.actions,
			Status:   f.status, Bytes: f.bytes, Millis: f.millis,
			ReqHeaders: f.reqHeaders, ResHeaders: f.resHeaders,
			ReqBody: f.reqBody, ResBody: f.resBody,
			ReqSize: f.reqSize, ResSize: f.resSize,
			ReqError: f.reqError, ResError: f.resError,
			ReqHeader: f.reqHeader, RespHeader: f.respHeader,
			ReqTrailer: f.reqTrailer, ResTrailer: f.resTrailer,
			ReqProto: f.conn.proto, ResProto: f.resProto,
			ConnectionLog: f.conn.log, ConnectionError: f.conn.error, ConnectionStage: f.conn.errorStage,
			At: f.at, Kind: f.kind, Color: f.color, Note: f.note,
			StreamEnd: f.streamEnd,
		}
		// JSON strings require UTF-8; byte fields use JSON's base64 encoding.
		if !utf8.ValidString(saved.ReqBody) {
			saved.ReqBytes, saved.ReqBody = []byte(saved.ReqBody), ""
		}
		if !utf8.ValidString(saved.ResBody) {
			saved.ResBytes, saved.ResBody = []byte(saved.ResBody), ""
		}
		s.Flows = append(s.Flows, saved)
	}
	if len(frames) > 0 {
		s.Frames = make(map[int][]sessionFrameData, len(frames))
		for id, list := range frames {
			for _, fr := range list {
				// The spill file holds what the message kept: read it now, so
				// the session carries the payload rather than a path to a
				// directory the capture removes when it closes.
				data := fr.payload()
				saved := sessionFrameData{
					Seq:       fr.seq,
					Direction: fr.direction,
					Kind:      fr.kind,
					Data:      data,
					Size:      max(fr.size, len(data)),
					At:        fr.at,
					EventID:   fr.eventID,
					EventName: fr.eventName,
				}
				if !utf8.ValidString(data) {
					saved.Payload, saved.Data = []byte(data), ""
				}
				s.Frames[id] = append(s.Frames[id], saved)
			}
		}
	}
	return s
}

func writeSession(path string, flows []flow, frames map[int][]frame) error {
	data, err := json.Marshal(sessionFrom(flows, frames))
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// readSession parses a session file into the flows and frames the table shows.
func readSession(path string) ([]flow, map[int][]frame, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var s sessionFile
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, nil, err
	}
	if s.Version != 1 {
		return nil, nil, fmt.Errorf("%s: unknown session version %d", path, s.Version)
	}
	flows := make([]flow, 0, len(s.Flows))
	for _, f := range s.Flows {
		if len(f.ReqBytes) > 0 {
			f.ReqBody = string(f.ReqBytes)
		}
		if len(f.ResBytes) > 0 {
			f.ResBody = string(f.ResBytes)
		}
		flows = append(flows, flow{
			id: f.ID, method: f.Method, scheme: f.Scheme, host: f.Host, path: f.Path, url: f.URL,
			debugKey: f.DebugKey, debugRun: f.DebugRun,
			replayOf: f.ReplayOf,
			actions:  f.Actions,
			status:   f.Status, bytes: f.Bytes, millis: f.Millis,
			reqHeaders: f.ReqHeaders, resHeaders: f.ResHeaders,
			reqBody: f.ReqBody, resBody: f.ResBody,
			reqSize: max(f.ReqSize, len(f.ReqBody)), resSize: max(f.ResSize, len(f.ResBody)),
			reqError: f.ReqError, resError: f.ResError,
			reqHeader: f.ReqHeader, respHeader: f.RespHeader,
			reqTrailer: f.ReqTrailer, resTrailer: f.ResTrailer,
			conn: conn{proto: f.ReqProto, log: f.ConnectionLog, error: f.ConnectionError, errorStage: f.ConnectionStage}, resProto: f.ResProto,
			at: f.At, kind: f.Kind, color: f.Color, note: f.Note,
			streamEnd: f.StreamEnd,
		})
	}
	frames := make(map[int][]frame, len(s.Frames))
	for id, list := range s.Frames {
		for _, fr := range list {
			if len(fr.Payload) > 0 {
				fr.Data = string(fr.Payload)
			}
			// A file written before messages carried a size or a time reads
			// as one that knows only what it holds.
			frames[id] = append(frames[id], frame{
				seq:       fr.Seq,
				direction: fr.Direction,
				kind:      fr.Kind,
				data:      fr.Data,
				size:      max(fr.Size, len(fr.Data)),
				kept:      len(fr.Data),
				at:        fr.At,
				eventID:   fr.EventID,
				eventName: fr.EventName,
			})
		}
	}
	return flows, frames, nil
}

// snapshot copies the table's state for a goroutine that writes it out.
func (a *app) snapshot() ([]flow, map[int][]frame) {
	flows := slices.Clone(a.flows)
	for i := range flows {
		flows[i].streamEnd = a.streamEnd(flows[i])
	}
	frames := make(map[int][]frame, len(a.frames))
	for id, list := range a.frames {
		frames[id] = slices.Clone(list)
	}
	return flows, frames
}

func (a *app) exportHAR() {
	flows, _ := a.snapshot()
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Title:       "Export HAR",
			DefaultPath: "vscroxy.har",
			Filters:     []mygo.FileFilter{{Name: "HAR", Extensions: []string{"har"}}},
		})
		if err != nil || path == "" {
			return
		}
		if err := writeHAR(path, flows); err != nil {
			a.setMessage("HAR: " + err.Error())
			return
		}
		a.setMessage(fmt.Sprintf("Exported %d flows to %s", len(flows), path))
	}()
}

// importHAR adds the flows of a HAR file another tool wrote to the table, so a
// capture someone sent can be read the way a live one is.
func (a *app) importHAR() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:   "Import HAR",
			Filters: []mygo.FileFilter{{Name: "HAR", Extensions: []string{"har", "json"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		flows, err := readHAR(paths[0])
		if err != nil {
			a.setMessage("HAR: " + err.Error())
			return
		}
		a.win.Update(func() {
			next := 0
			if n := len(a.flows); n > 0 {
				next = a.flows[n-1].id
			}
			for i := range flows {
				flows[i].id = next + i + 1
			}
			a.flows = append(a.flows, flows...)
			a.countDomains()
			a.countClients()
			a.applyFilter()
		})
		a.setMessage(fmt.Sprintf("Imported %d flows from %s", len(flows), paths[0]))
	}()
}

func (a *app) saveSession() {
	flows, frames := a.snapshot()
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Title:       "Save session",
			DefaultPath: "vscroxy-session.json",
			Filters:     []mygo.FileFilter{{Name: "Session", Extensions: []string{"json"}}},
		})
		if err != nil || path == "" {
			return
		}
		if err := writeSession(path, flows, frames); err != nil {
			a.setMessage("Session: " + err.Error())
			return
		}
		a.setMessage(fmt.Sprintf("Saved %d flows to %s", len(flows), path))
	}()
}

func (a *app) openSession() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:   "Open session",
			Filters: []mygo.FileFilter{{Name: "Session", Extensions: []string{"json"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		flows, frames, err := readSession(paths[0])
		if err != nil {
			a.setMessage("Session: " + err.Error())
			return
		}
		a.win.Update(func() { a.replaceSession(flows, frames) })
		a.setMessage(fmt.Sprintf("Opened %d flows from %s", len(flows), paths[0]))
	}()
}

// replaceSession is a data replacement, not a list refresh. IDs may be reused,
// so discard old selections, body-search answers and detail caches explicitly.
func (a *app) replaceSession(flows []flow, frames map[int][]frame) {
	a.flows, a.frames = flows, frames
	a.picked, a.pickedID, a.detailFor = 0, 0, -1
	a.analysis = captureAnalysis{}
	a.chosen.Clear()
	a.views.stopSearch()
	a.views.cache = bodyCache{}
	for _, view := range a.views.views {
		view.picked, view.pickedID = 0, 0
		view.list = ui.ListState{}
	}
	a.messageUI.stopSearch()
	a.messageUI = messagePanel{}
	a.protobuf.stop()
	a.query, a.filter = "", "all"
	a.countDomains()
	a.countClients()
	a.applyFilter()
}
