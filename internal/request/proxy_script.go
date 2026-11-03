package request

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
)

// ProxyScript is the script a Script rule runs on the exchanges it matches:
// a file that declares onRequest, onResponse or both, which run with the
// same pm API as request scripts. onRequest edits pm.request before it goes
// upstream; onResponse edits pm.response before the client gets it:
//
//	function onRequest() { pm.request.headers.upsert("X-Debug", "1") }
//	async function onResponse() { if (pm.response.code === 500) pm.response.code = 200 }
type ProxyScript struct {
	Source                string
	OnRequest, OnResponse bool
}

// ScriptProblem is where a script does not parse.
type ScriptProblem struct {
	Line, Column int
	Message      string
}

func (p ScriptProblem) Error() string {
	return fmt.Sprintf("line %d: %s", p.Line, p.Message)
}

// ParseProxyScript finds the hooks src declares at its top level, without
// running it. A script that does not parse is a ScriptProblem.
func ParseProxyScript(src string) (*ProxyScript, error) {
	program, err := parser.ParseFile(nil, "", src, 0)
	if err != nil {
		var list parser.ErrorList
		if errors.As(err, &list) && len(list) > 0 {
			return nil, ScriptProblem{Line: list[0].Position.Line, Column: list[0].Position.Column, Message: list[0].Message}
		}
		return nil, err
	}
	s := &ProxyScript{Source: src}
	for _, statement := range program.Body {
		if fn, ok := statement.(*ast.FunctionDeclaration); ok && fn.Function.Name != nil {
			switch fn.Function.Name.Name {
			case "onRequest":
				s.OnRequest = true
			case "onResponse":
				s.OnResponse = true
			}
		}
	}
	return s, nil
}

// ProxyEnv is what rule scripts share besides their exchange: snapshots of
// the globals and the active environment, and the system's environment
// variables, process.env.
type ProxyEnv struct {
	Globals, Environment, OS map[string]string
}

// ProxyRun is what a hook printed, and the globals it set, in order.
type ProxyRun struct {
	Console []ConsoleMessage
	Writes  []Write
}

// Run runs the hook of x's phase. pm.request is x's request, editable in the
// request phase; pm.response is x's response, editable in the response
// phase. pm.variables are x's Locals; pm.globals writes are returned for the
// app to save; pm.environment is read-only. pm.sendRequest goes straight to
// the server, not through the proxy, so a script cannot trigger itself.
func (s *ProxyScript) Run(ctx context.Context, x *capture.ScriptExchange, env ProxyEnv) (ProxyRun, error) {
	hook := map[string]string{"request": "onRequest", "response": "onResponse"}[x.Phase]
	if hook == "" || hook == "onRequest" && !s.OnRequest || hook == "onResponse" && !s.OnResponse {
		return ProxyRun{}, nil
	}
	if x.Locals == nil {
		x.Locals = map[string]string{}
	}
	run := &scriptRun{
		vars: maps.Clone(env.Environment), locals: x.Locals, globals: maps.Clone(env.Globals), env: env.OS,
		readOnly: map[string]bool{"environment": true}, phase: x.Phase,
		info: scriptInfo{RequestName: x.Rule},
	}
	run.target = func() string { return x.URL }
	run.send = func(req *Prepared) (capture.Flow, error) { return sendDirect(ctx, req) }
	req := &Prepared{Method: x.Method, URL: x.URL, Header: x.Header.Clone(), Body: string(x.Body)}
	if req.Header == nil {
		req.Header = http.Header{}
	}
	objects := map[string]any{"req": req}
	var res *jsResponse
	if x.Phase == "response" {
		res = &jsResponse{Status: x.Status, Header: x.Response.Clone(), Body: string(x.ResponseBody), Bytes: len(x.ResponseBody), editable: true}
		objects["res"] = res
	}
	// The hook runs where the script's own declarations are in scope; the
	// newline keeps a last line comment from swallowing the call.
	err := runScriptContext(ctx, s.Source+"\nreturn await "+hook+"();", run, objects)
	out := ProxyRun{Console: run.console, Writes: run.writes}
	if err != nil {
		return out, err
	}
	if x.Phase == "request" {
		x.Method, x.URL, x.Header = strings.ToUpper(req.Method), req.URL, req.Header
		if !x.BinaryBody {
			x.Body = []byte(req.Body)
		}
		return out, nil
	}
	x.Status, x.Response = res.Status, res.Header
	if !x.BinaryResult {
		x.ResponseBody = []byte(res.Body)
	}
	return out, nil
}

// sendDirect sends a rule script's pm.sendRequest to the server itself,
// following redirects, for up to 30 seconds, keeping up to 8 MiB of the
// response.
func sendDirect(ctx context.Context, req *Prepared) (capture.Flow, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var body io.Reader
	if req.Body != "" {
		body = strings.NewReader(req.Body)
	}
	r, err := http.NewRequestWithContext(ctx, req.Method, req.URL, body)
	if err != nil {
		return capture.Flow{}, err
	}
	r.Header = req.Header.Clone()
	client := &http.Client{Transport: &http.Transport{Proxy: nil, ForceAttemptHTTP2: true}}
	start := time.Now()
	resp, err := client.Do(r)
	if err != nil {
		return capture.Flow{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return capture.Flow{}, err
	}
	return capture.Flow{Method: req.Method, URL: req.URL, Status: resp.StatusCode, ResponseHeader: resp.Header,
		ResponseBody: string(data), Bytes: len(data), Millis: int(time.Since(start).Milliseconds())}, nil
}
