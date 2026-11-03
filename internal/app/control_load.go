package app

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/runner"
)

func (a *app) remoteRunActive() bool {
	if a.control == nil {
		return false
	}
	for _, job := range a.control.jobs {
		job.mu.Lock()
		done := job.done
		job.mu.Unlock()
		if !done {
			return true
		}
	}
	return false
}

func (a *app) startControlLoad(command control.Command) (any, error) {
	p, err := controlParams[struct {
		Collection     *string `json:"collection"`
		Environment    string  `json:"environment"`
		Users          int     `json:"users"`
		Duration       int     `json:"durationSeconds"`
		RequestTimeout *int    `json:"requestTimeoutSeconds"`
		MaxAttempts    *int    `json:"maxAttempts"`
		Bail           bool    `json:"bail"`
		Confirm        bool    `json:"confirm"`
	}](command)
	if err != nil || !p.Confirm || p.Collection == nil || p.Duration < 1 || p.Duration > 600 {
		return nil, errors.New("collection, users, durationSeconds (1–600) and confirm:true are required")
	}
	opts := runner.LoadOptions{Users: p.Users, Duration: time.Duration(p.Duration) * time.Second, Timeout: 30 * time.Second, MaxAttempts: 100000, StopOnFailure: p.Bail}
	if p.RequestTimeout != nil {
		if *p.RequestTimeout < 1 || *p.RequestTimeout > 60 {
			return nil, errors.New("requestTimeoutSeconds must be between 1 and 60")
		}
		opts.Timeout = time.Duration(*p.RequestTimeout) * time.Second
	}
	if p.MaxAttempts != nil {
		if *p.MaxAttempts < 1 || *p.MaxAttempts > 1000000 {
			return nil, errors.New("maxAttempts must be between 1 and 1000000")
		}
		opts.MaxAttempts = *p.MaxAttempts
	}
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if a.control == nil || a.srv == nil || !a.srv.Running() {
		return nil, errors.New("local proxy is not running")
	}
	if a.remoteRunActive() || a.runner.running || a.loadRunner.running {
		return nil, errors.New("a collection run or load is already active")
	}
	steps, values, err := a.controlRunSteps("", p.Collection, p.Environment)
	if err != nil {
		return nil, err
	}
	if len(steps) > 200 {
		return nil, errors.New("select at most 200 saved requests")
	}
	opts.Vars = values
	ctx, cancel := context.WithCancel(context.Background())
	job := &controlJob{id: uuid.New().String(), cancel: cancel, load: &runner.LoadReport{Version: 1, StartedAt: time.Now().UTC(), Users: opts.Users, Reason: "running"}}
	c := a.control
	if len(c.order) == 16 {
		delete(c.jobs, c.order[0])
		c.order = c.order[1:]
	}
	c.jobs[job.id], c.order = job, append(c.order, job.id)
	srv := a.srv
	c.wg.Go(func() {
		defer cancel()
		report, err := runner.RunLoad(ctx, srv, steps, opts, func(r runner.LoadReport) {
			job.mu.Lock()
			job.load = &r
			job.mu.Unlock()
		})
		job.mu.Lock()
		defer job.mu.Unlock()
		if report.Version == 0 && err != nil {
			// Validation errors are fixed diagnostics, never saved script text.
			job.load.Done, job.load.Reason = true, "invalid-input"
		} else {
			job.load = &report
		}
		job.done = true
	})
	return map[string]string{"id": job.id}, nil
}
