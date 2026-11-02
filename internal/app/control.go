package app

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http/cookiejar"
	"slices"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
	"golang.org/x/net/publicsuffix"
)

// The registry belongs to the UI thread; each job owns its locked report. No
// HTTP worker reads app state, and no job waits for the UI during shutdown.
type desktopControl struct {
	server *control.Server
	jobs   map[string]*controlJob
	order  []string
	wg     sync.WaitGroup
}

type controlJob struct {
	mu     sync.Mutex
	id     string
	done   bool
	cancel context.CancelFunc
	report runner.Report
}

func (a *app) startControl(path string) error {
	c := &desktopControl{jobs: map[string]*controlJob{}}
	win := a.win
	server, err := control.Start(path, func(ctx context.Context, command control.Command) (any, error) {
		reply := make(chan jsontext.Value, 1)
		win.Update(func() {
			if ctx.Err() != nil {
				return // a timed-out queued mutation must never run later
			}
			value, err := a.controlValue(command)
			if err != nil {
				value = map[string]string{"error": err.Error()}
			}
			// Snapshot on the UI thread, before HTTP encodes the response.
			data, err := json.Marshal(value)
			if err != nil {
				data = []byte(`{"error":"cannot encode result"}`)
			}
			reply <- data
		})
		select {
		case value := <-reply:
			return value, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	if err != nil {
		return err
	}
	c.server, a.control = server, c
	return nil
}

func (a *app) closeControl() {
	c := a.control
	if c == nil {
		return
	}
	if c.server != nil {
		_ = c.server.Close()
		c.server = nil
	}
	for _, job := range c.jobs {
		job.cancel()
	}
	c.wg.Wait()
}

// Each action has an explicit parameter type; no UI context, arbitrary method,
// filesystem path or menu callback can be supplied by a client.
func controlParams[T any](command control.Command) (T, error) {
	var value T
	if err := json.Unmarshal(command.Params, &value, json.RejectUnknownMembers(true)); err != nil {
		return value, errors.New("invalid parameters")
	}
	return value, nil
}

func (a *app) controlValue(command control.Command) (any, error) {
	switch command.Action {
	case "status", "collections.list", "rules.list":
		if _, err := controlParams[struct{}](command); err != nil {
			return nil, err
		}
		switch command.Action {
		case "status":
			return map[string]any{"proxyRunning": a.srv != nil && a.srv.Running(), "recording": !a.recordingPaused, "environment": a.activeEnv, "requests": len(a.requests), "flows": len(a.flows)}, nil
		case "collections.list":
			return runnableCollections(a.requests), nil
		default:
			var rows []map[string]any
			for i, rule := range a.rules {
				rows = append(rows, map[string]any{"index": i, "key": controlRuleKey(rule), "name": rule.Name, "type": rule.Type, "enabled": !rule.Disabled})
			}
			return rows, nil
		}
	case "requests.list":
		p, err := controlParams[struct {
			Collection *string `json:"collection"`
		}](command)
		if err != nil {
			return nil, err
		}
		var rows []map[string]any
		for _, r := range a.requests {
			if p.Collection == nil || r.Collection == *p.Collection || *p.Collection != "" && strings.HasPrefix(r.Collection, *p.Collection+"/") {
				rows = append(rows, controlRequest(r.Request))
			}
		}
		return rows, nil
	case "requests.get":
		p, err := controlParams[struct {
			ID      string `json:"id"`
			Details bool   `json:"details"`
		}](command)
		if err != nil {
			return nil, err
		}
		for _, r := range a.requests {
			if p.ID != "" && r.ID == p.ID {
				if p.Details {
					return r.Request, nil
				}
				return controlRequest(r.Request), nil
			}
		}
		return nil, errors.New("request not found")
	case "flows.list", "flows.get":
		p, err := controlParams[struct {
			ID      int  `json:"id"`
			Limit   int  `json:"limit"`
			Before  int  `json:"before"`
			Details bool `json:"details"`
		}](command)
		if err != nil || p.Limit < 0 || p.Limit > 200 || p.Before < 0 {
			return nil, errors.New("invalid flow parameters")
		}
		// Concurrent captures can be delivered out of ID order. Cursor paging
		// must use ID order, independent of arrival or the table's sort.
		indices := make([]int, 0, len(a.flows))
		for i, f := range a.flows {
			if f.live {
				indices = append(indices, i)
			}
		}
		slices.SortFunc(indices, func(i, j int) int { return cmp.Compare(a.flows[j].id, a.flows[i].id) })
		var rows []map[string]any
		for _, i := range indices {
			f := a.flows[i]
			if command.Action == "flows.get" {
				if p.ID > 0 && f.id == p.ID {
					return controlFlow(f, p.Details), nil
				}
				continue
			}
			if p.Before != 0 && f.id >= p.Before {
				continue
			}
			rows = append(rows, controlFlow(f, false))
			if len(rows) == cmp.Or(p.Limit, 50) {
				return map[string]any{"items": rows, "nextBefore": f.id}, nil
			}
		}
		if command.Action == "flows.get" {
			return nil, errors.New("live flow not found")
		}
		return map[string]any{"items": rows, "nextBefore": 0}, nil
	case "capture.recording":
		p, err := controlParams[struct {
			Enabled *bool `json:"enabled"`
			Confirm bool  `json:"confirm"`
		}](command)
		if err != nil || p.Enabled == nil || !p.Confirm {
			return nil, errors.New("enabled and confirm:true are required")
		}
		a.recordingPaused = !*p.Enabled
		return map[string]bool{"recording": *p.Enabled}, nil
	case "rules.set-enabled":
		p, err := controlParams[struct {
			Index   *int   `json:"index"`
			Key     string `json:"key"`
			Enabled *bool  `json:"enabled"`
			Confirm bool   `json:"confirm"`
		}](command)
		if err != nil || p.Index == nil || *p.Index < 0 || *p.Index >= len(a.rules) || p.Enabled == nil || !p.Confirm {
			return nil, errors.New("valid index, key, enabled and confirm:true are required")
		}
		if p.Key != controlRuleKey(a.rules[*p.Index]) {
			return nil, errors.New("rule changed; list rules again")
		}
		if !a.switchRule(*p.Index, *p.Enabled) {
			return nil, errors.New("could not save rule")
		}
		return map[string]bool{"enabled": *p.Enabled}, nil
	case "request.run", "collection.run":
		return a.startControlRun(command)
	case "runs.get", "runs.cancel":
		p, err := controlParams[struct {
			ID      string `json:"id"`
			Confirm bool   `json:"confirm"`
		}](command)
		if err != nil || a.control == nil || a.control.jobs[p.ID] == nil {
			return nil, errors.New("run not found")
		}
		job := a.control.jobs[p.ID]
		if command.Action == "runs.cancel" {
			if !p.Confirm {
				return nil, errors.New("confirm:true is required")
			}
			job.cancel()
		}
		job.mu.Lock()
		defer job.mu.Unlock()
		// Marshal while locked; the HTTP worker never observes mutable results.
		data, err := json.Marshal(map[string]any{"id": job.id, "done": job.done, "report": job.report})
		return jsontext.Value(data), err
	default:
		return nil, errors.New("unknown action")
	}
}

func controlRequest(r request.Request) map[string]any {
	return map[string]any{"id": r.ID, "name": r.Name, "collection": r.Collection, "method": r.Method, "url": redactExportURL(r.URL, safeExportOptions()), "bodyMode": r.BodyMode}
}

func controlFlow(f flow, details bool) map[string]any {
	value := map[string]any{"id": f.id, "method": f.method, "url": redactExportURL(f.url, safeExportOptions()), "status": f.status, "bytes": f.bytes, "millis": f.millis, "at": f.at}
	if details {
		value["url"] = f.url
		value["requestHeaders"], value["responseHeaders"] = f.reqHeader.Clone(), f.respHeader.Clone()
		for _, side := range []struct {
			name, body, file string
			size             int
		}{{"request", f.reqBody, f.reqFile, f.reqSize}, {"response", f.resBody, f.resFile, f.resSize}} {
			preview := side.body[:min(len(side.body), 64<<10)]
			value[side.name+"BodyBase64"] = base64.StdEncoding.EncodeToString([]byte(preview))
			value[side.name+"BodyTruncated"] = side.file != "" || side.size > len(preview)
		}
	}
	return value
}

func controlRuleKey(rule capture.Rule) string {
	data, _ := json.Marshal(rule)
	key := sha256.Sum256(data)
	return hex.EncodeToString(key[:])
}

func (a *app) startControlRun(command control.Command) (any, error) {
	p, err := controlParams[struct {
		ID          string  `json:"id"`
		Collection  *string `json:"collection"`
		Environment string  `json:"environment"`
		Iterations  *int    `json:"iterations"`
		Bail        bool    `json:"bail"`
		Confirm     bool    `json:"confirm"`
	}](command)
	if err != nil || !p.Confirm || p.Iterations != nil && (*p.Iterations < 1 || *p.Iterations > 1000) {
		return nil, errors.New("confirm:true and 1–1000 iterations are required")
	}
	if a.control == nil || a.srv == nil || !a.srv.Running() {
		return nil, errors.New("local proxy is not running")
	}
	for _, job := range a.control.jobs {
		job.mu.Lock()
		done := job.done
		job.mu.Unlock()
		if !done {
			return nil, errors.New("a remote run is already active")
		}
	}
	env := a.activeEnvironment()
	if p.Environment != "" {
		at := slices.IndexFunc(a.envs, func(e environment) bool { return e.Name == p.Environment })
		if at < 0 {
			return nil, errors.New("environment not found")
		}
		env = a.envs[at]
	}
	var steps []runner.Step
	tokens := &request.Tokens{}
	for _, saved := range a.requests {
		r := saved.Request
		selected := command.Action == "request.run" && p.ID != "" && r.ID == p.ID
		if command.Action == "collection.run" && p.Collection != nil {
			selected = r.Collection == *p.Collection || *p.Collection != "" && strings.HasPrefix(r.Collection, *p.Collection+"/")
		}
		if !selected {
			continue
		}
		r.Form, r.HeaderList = slices.Clone(r.Form), slices.Clone(r.HeaderList)
		in := a.inheritance(r.Collection)
		in.Environment, in.Tokens = env.Name, tokens
		for i := range in.Folders {
			in.Folders[i].Variables = slices.Clone(in.Folders[i].Variables)
		}
		// Remote runs may use an already unlocked Vault, but never unlock it,
		// enable script Vault access or persist script writes themselves.
		if a.vault.loaded {
			in.Vault = slices.Clone(a.vault.secrets).Lookup
		}
		steps = append(steps, runner.Step{Request: r, Context: in})
	}
	if len(steps) == 0 {
		return nil, errors.New("no saved requests selected")
	}
	slices.SortStableFunc(steps, func(a, b runner.Step) int { return strings.Compare(a.Request.Collection, b.Request.Collection) })
	iterations := 1
	if p.Iterations != nil {
		iterations = *p.Iterations
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	job := &controlJob{id: uuid.New().String(), cancel: cancel, report: runner.Report{Version: 1, StartedAt: time.Now().UTC()}}
	c := a.control
	if len(c.order) == 16 {
		delete(c.jobs, c.order[0])
		c.order = c.order[1:]
	}
	c.jobs[job.id], c.order = job, append(c.order, job.id)
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	srv, values := a.srv, env.values()
	c.wg.Go(func() {
		defer cancel()
		start := time.Now()
		summary, runErr := runner.Run(ctx, srv, steps, runner.Options{Iterations: iterations, Timeout: 30 * time.Second, MaxSteps: 1000, StopOnFailure: p.Bail, Vars: values, Cookies: jar}, func(result runner.Result) error {
			job.mu.Lock()
			job.report.Append(steps[result.Index].Request, result, false)
			job.mu.Unlock()
			if next := result.Execution.Next; next != nil && *next != "" && !slices.ContainsFunc(steps, func(step runner.Step) bool { return step.Request.ID == *next || step.Request.Name == *next }) {
				return errors.New("unknown next request")
			}
			return nil
		})
		job.mu.Lock()
		defer job.mu.Unlock()
		job.report.Finish(summary, runErr, time.Since(start))
		job.done = true
	})
	return map[string]string{"id": job.id}, nil
}
