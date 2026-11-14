package request

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"

	"github.com/baba983/vscroxy/internal/capture"
)

// Postman allows ten pm.execution.runRequest calls from each script. Nesting
// is limited too, so a request that calls itself, directly or through others,
// stops with an error instead of running forever.
const (
	maxRunCalls = 10
	maxRunDepth = 5
)

// Callee is a request pm.execution.runRequest runs: the request and the
// folders it lives under, which supply its auth, scripts and variables.
type Callee struct {
	Request Request
	Folders []Folder
}

// Resolver finds the request a runRequest argument names. Callers supply one
// for each run that can call requests; a run without one refuses the calls.
type Resolver func(ref string) (Callee, error)

// callRequest is pm.execution.runRequest. The named request is sent as a send
// of its own inside the calling run: its folders supply its auth, scripts and
// variables, and it reads the caller's environment and locals, with the
// call's overrides on top. Its tests and console output join the caller's,
// and its environment and global or collection writes carry on from the
// caller's run. Its own pm.variables stay with it.
func (run *scriptRun) callRequest(ctx context.Context, srv *capture.Server, in Context, jar http.CookieJar, ref string, overrides map[string]string) (Execution, error) {
	if run.calls >= maxRunCalls {
		return Execution{}, fmt.Errorf("a script may call runRequest at most %d times", maxRunCalls)
	}
	run.calls++
	if in.nested >= maxRunDepth {
		return Execution{}, fmt.Errorf("requests nest more than %d levels deep; check for a cycle", maxRunDepth)
	}
	if ref == "" {
		return Execution{}, errors.New("collection request id not provided")
	}
	callee, err := in.Resolve(ref)
	if err != nil {
		return Execution{}, err
	}
	child := in
	child.Folders, child.nested = callee.Folders, in.nested+1
	child.Locals = maps.Clone(run.locals)
	maps.Copy(child.Locals, overrides)
	out := callee.Request.Execute(ctx, srv, child, maps.Clone(run.vars), jar)
	run.vars = out.Vars
	run.writes = append(run.writes, out.Writes...)
	run.tests = append(run.tests, out.Tests...)
	run.console = append(run.console, out.Console...)
	if out.Err != nil {
		return out, fmt.Errorf("%s: %w", callee.Request.Name, out.Err)
	}
	return out, nil
}
