package main

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
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
// frames, enough to reopen the table later.
type sessionFile struct {
	Version int                        `json:"version"`
	Flows   []sessionFlow              `json:"flows"`
	Frames  map[int][]sessionFrameData `json:"frames,omitempty"`
}

type sessionFlow struct {
	ID         int         `json:"id"`
	Method     string      `json:"method"`
	Scheme     string      `json:"scheme"`
	Host       string      `json:"host"`
	Path       string      `json:"path"`
	URL        string      `json:"url"`
	Status     int         `json:"status"`
	Bytes      int         `json:"bytes"`
	Millis     int         `json:"millis"`
	ReqHeaders string      `json:"reqHeaders"`
	ResHeaders string      `json:"resHeaders"`
	ReqBody    string      `json:"reqBody"`
	ResBody    string      `json:"resBody"`
	ReqHeader  http.Header `json:"reqHeader,omitempty"`
	RespHeader http.Header `json:"respHeader,omitempty"`
	At         time.Time   `json:"at"`
	Kind       string      `json:"kind,omitempty"`
}

type sessionFrameData struct {
	Direction string `json:"direction"`
	Kind      string `json:"kind"`
	Data      string `json:"data"`
}

func sessionFrom(flows []flow, frames map[int][]frame) sessionFile {
	s := sessionFile{Version: 1, Flows: make([]sessionFlow, 0, len(flows))}
	for _, f := range flows {
		s.Flows = append(s.Flows, sessionFlow{
			ID: f.id, Method: f.method, Scheme: f.scheme, Host: f.host, Path: f.path, URL: f.url,
			Status: f.status, Bytes: f.bytes, Millis: f.millis,
			ReqHeaders: f.reqHeaders, ResHeaders: f.resHeaders,
			ReqBody: f.body(false), ResBody: f.body(true),
			ReqHeader: f.reqHeader, RespHeader: f.respHeader,
			At: f.at, Kind: f.kind,
		})
	}
	if len(frames) > 0 {
		s.Frames = make(map[int][]sessionFrameData, len(frames))
		for id, list := range frames {
			for _, fr := range list {
				s.Frames[id] = append(s.Frames[id], sessionFrameData{Direction: fr.direction, Kind: fr.kind, Data: fr.data})
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
		flows = append(flows, flow{
			id: f.ID, method: f.Method, scheme: f.Scheme, host: f.Host, path: f.Path, url: f.URL,
			status: f.Status, bytes: f.Bytes, millis: f.Millis,
			reqHeaders: f.ReqHeaders, resHeaders: f.ResHeaders,
			reqBody: f.ReqBody, resBody: f.ResBody,
			reqHeader: f.ReqHeader, respHeader: f.RespHeader,
			at: f.At, kind: f.Kind,
		})
	}
	frames := make(map[int][]frame, len(s.Frames))
	for id, list := range s.Frames {
		for _, fr := range list {
			frames[id] = append(frames[id], frame{direction: fr.Direction, kind: fr.Kind, data: fr.Data})
		}
	}
	return flows, frames, nil
}

// snapshot copies the table's state for a goroutine that writes it out.
func (a *app) snapshot() ([]flow, map[int][]frame) {
	flows := slices.Clone(a.flows)
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
		a.win.Update(func() {
			a.flows = flows
			a.frames = frames
			a.picked = 0
			a.detailFor = -1
			a.query = ""
			a.filter = "all"
			a.countDomains()
			a.applyFilter()
		})
		a.setMessage(fmt.Sprintf("Opened %d flows from %s", len(flows), paths[0]))
	}()
}
