package app

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/control"
	"github.com/google/uuid"
	"golang.org/x/net/http/httpguts"
)

// replayJobResult is all an agent gets back from flows.replay: no headers or
// bodies, which stay behind flows.get and its --allow-sensitive gate.
type replayJobResult struct {
	Kind       string `json:"kind"`
	SourceID   int    `json:"sourceId"`
	FlowID     int    `json:"flowId"`
	Status     int    `json:"status"`
	DurationMs int    `json:"durationMs"`
	Error      string `json:"error"`
}

// replayJobState adds the send start, so runs.get can name the capture this
// replay produced without treating unrelated traffic as its result.
type replayJobState struct {
	result  replayJobResult
	started time.Time
}

// startControlReplay re-sends one live HTTP flow as a job. The request runs on
// a worker; the UI thread only snapshots the flow and registers the job.
func (a *app) startControlReplay(command control.Command) (any, error) {
	p, err := controlParams[struct {
		ID             int               `json:"id"`
		Method         string            `json:"method"`
		URL            string            `json:"url"`
		Headers        map[string]string `json:"headers"`
		Body           *string           `json:"body"`
		TimeoutSeconds int               `json:"timeoutSeconds"`
		Confirm        bool              `json:"confirm"`
	}](command)
	if err != nil || p.ID <= 0 {
		return nil, flowInvalid("id", "id must be a positive flow ID", "list ids with flows.list")
	}
	if !p.Confirm {
		return nil, flowInvalid("confirm", "confirm:true is required", "the MCP write tier supplies it; the CLI uses --confirm")
	}
	timeout := 30
	if p.TimeoutSeconds != 0 {
		if p.TimeoutSeconds < 1 || p.TimeoutSeconds > 60 {
			return nil, flowInvalid("timeoutSeconds", "timeoutSeconds must be between 1 and 60", "omit timeoutSeconds for 30")
		}
		timeout = p.TimeoutSeconds
	}
	if a.control == nil || a.srv == nil || !a.srv.Running() {
		return nil, errors.New("local proxy is not running")
	}
	i := slices.IndexFunc(a.flows, func(f flow) bool { return f.live && f.id == p.ID })
	if i < 0 {
		return nil, controlFaultf("flow-not-found", "id", "live flow not found",
			"list ids with flows.list; replay works on live flows only")
	}
	f := a.flows[i]
	if f.kind != "" || f.url == "" {
		return nil, errors.New("only HTTP(S) captures with a URL can be replayed")
	}
	if p.URL != "" {
		if err := validateReplayURL(p.URL); err != nil {
			return nil, err
		}
	}
	headers, err := replayHeaders(f, p.Headers, p.URL != "", p.Body != nil)
	if err != nil {
		return nil, err
	}
	if headers != nil {
		f.reqHeader = headers
	}
	if p.Method != "" {
		method := strings.TrimSpace(p.Method)
		if method == "" || !httpguts.ValidHeaderFieldName(method) {
			return nil, errors.New("method must be an HTTP method token")
		}
		f.method = method
	}
	if p.URL != "" {
		f.url = p.URL
	}
	if p.Body != nil {
		f.reqBody, f.reqFile = *p.Body, ""
	}

	ctx, cancel := context.WithCancel(context.Background())
	job := &controlJob{id: uuid.New().String(), cancel: cancel, replay: &replayJobState{
		result:  replayJobResult{Kind: "replay", SourceID: p.ID},
		started: time.Now(),
	}}
	c := a.control
	if len(c.order) == 16 {
		delete(c.jobs, c.order[0])
		c.order = c.order[1:]
	}
	c.jobs[job.id], c.order = job, append(c.order, job.id)
	c.wg.Go(func() {
		defer cancel()
		started := time.Now()
		requestCtx, stop := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer stop()
		reply, err := a.replayFlow(requestCtx, f, false)
		job.mu.Lock()
		defer job.mu.Unlock()
		job.replay.result.DurationMs = int(time.Since(started).Milliseconds())
		if err != nil {
			job.replay.result.Error = err.Error()
		} else {
			job.replay.result.Status = reply.Status
		}
		job.done = true
	})
	return map[string]string{"id": job.id}, nil
}

// replayHeaders clones the captured headers and applies overrides. It also
// drops a captured Host when the target URL changes, because the replay layer
// would otherwise keep sending to the old host.
func replayHeaders(f flow, overrides map[string]string, newURL, newBody bool) (http.Header, error) {
	if len(overrides) == 0 && !newURL && !newBody {
		return nil, nil
	}
	h := f.reqHeader.Clone()
	if h == nil {
		h = make(http.Header)
		for _, pair := range headerPairs(f.reqHeaders, nil) {
			h.Add(pair.Name, pair.Value)
		}
	}
	hostSet := false
	for _, name := range slices.Sorted(maps.Keys(overrides)) {
		value := overrides[name]
		if !httpguts.ValidHeaderFieldName(name) || value != "" && !httpguts.ValidHeaderFieldValue(value) {
			return nil, errors.New("headers must have valid names and values")
		}
		if strings.EqualFold(name, "Host") {
			hostSet = value != ""
		}
		if value == "" {
			h.Del(name)
		} else {
			h.Set(name, value)
		}
	}
	if newURL && !hostSet {
		h.Del("Host")
	}
	if newBody {
		h.Del("Content-Length")
	}
	return h, nil
}

func validateReplayURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url must be an absolute http or https URL")
	}
	return nil
}

// controlReplayState answers runs.get for a replay job. The captured flow ID
// is resolved on the UI thread at poll time: the completed exchange may arrive
// just after the replay call returns.
func (a *app) controlReplayState(job *controlJob) (any, error) {
	job.mu.Lock()
	result, done := job.replay.result, job.done
	job.mu.Unlock()
	if done {
		result.FlowID = a.replayCapturedFlowID(result.SourceID, job.replay.started)
	}
	data, err := json.Marshal(struct {
		ID   string `json:"id"`
		Done bool   `json:"done"`
		replayJobResult
	}{ID: job.id, Done: done, replayJobResult: result})
	return jsontext.Value(data), err
}

func (a *app) replayCapturedFlowID(sourceID int, started time.Time) int {
	id := 0
	for _, f := range a.flows {
		if f.live && f.replayOf == sourceID && !f.at.Before(started) && (id == 0 || f.id < id) {
			id = f.id
		}
	}
	return id
}
