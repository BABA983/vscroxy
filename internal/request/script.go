package request

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/vault"
	"github.com/dop251/goja"
)

// Chai 4.5.0 (MIT, see THIRD_PARTY_NOTICES.md) is pm.expect, as in Postman.
//
//go:embed js/chai.js
var chaiSource string

//go:embed js/pm.js
var pmSource string

// The libraries compile once; every script still gets a fresh runtime.
var (
	chaiProgram = sync.OnceValue(func() *goja.Program { return goja.MustCompile("chai.js", chaiSource, false) })
	pmProgram   = sync.OnceValue(func() *goja.Program { return goja.MustCompile("pm.js", pmSource, false) })
)

// TestResult is one assertion a request script or check made. Error says
// why a failed one failed; a skipped test (pm.test.skip) passes.
type TestResult struct {
	Name    string
	Pass    bool
	Error   string `json:",omitempty"`
	Skipped bool   `json:",omitempty"`
}

// Prepared is a request ready to send, and the req object scripts get: what
// a pre-request script changes is what the proxy sends.
type Prepared struct {
	Method string      `json:"method"`
	URL    string      `json:"url"`
	Body   string      `json:"body"`
	Header http.Header `json:"-"`
	// Only pre-script templates record explicit overrides of generated fields.
	editedHeaders map[string]bool
	editedBody    bool
}

// Get is the request header named.
func (r *Prepared) Get(name string) string { return r.Header.Get(name) }

// Set sets a request header.
func (r *Prepared) Set(name, value string) {
	r.Header.Set(name, value)
	r.edited(name)
}

// Add adds a request header, keeping the ones of that name.
func (r *Prepared) Add(name, value string) {
	r.Header.Add(name, value)
	r.edited(name)
}

// Remove drops a request header.
func (r *Prepared) Remove(name string) {
	r.Header.Del(name)
	r.edited(name)
}

func (r *Prepared) edited(name string) {
	if r.editedHeaders != nil {
		r.editedHeaders[http.CanonicalHeaderKey(name)] = true
	}
}

// jsResponse is the res object a post-response script gets.
type jsResponse struct {
	Status int         `json:"status"`
	Body   string      `json:"body"`
	Header http.Header `json:"-"`
	Millis int         `json:"-"`
	Bytes  int         `json:"-"`
}

// Get is the response header named.
func (r *jsResponse) Get(name string) string { return r.Header.Get(name) }

// Json is the body parsed as JSON, nil when it is not JSON.
func (r *jsResponse) Json() any {
	var v any
	if err := json.Unmarshal([]byte(r.Body), &v); err != nil {
		return nil
	}
	return v
}

// Write is a script's change to a variable scope the run does not own:
// "global", "collection" (the collection at Path) or "vault". The app saves
// them; a Runner applies them to the requests after this one.
type Write struct {
	Scope string
	Path  string
	Name  string
	Value string
	Unset bool
}

// Fields applies w to a scope's variable rows: a set updates the first
// enabled row of that name or adds one, an unset removes every row of it.
func (w Write) Fields(fields []Field) []Field {
	if w.Unset {
		return slices.DeleteFunc(slices.Clone(fields), func(f Field) bool { return strings.TrimSpace(f.Name) == w.Name })
	}
	fields = slices.Clone(fields)
	for i, f := range fields {
		if strings.TrimSpace(f.Name) == w.Name && !f.Disabled {
			fields[i].Value = w.Value
			return fields
		}
	}
	return append(fields, Field{Name: w.Name, Value: w.Value})
}

// scriptRun is the state the scripts of one send share: the variable scopes
// they read and write, the assertions they made and what they logged.
type scriptRun struct {
	// vars are the environment's and this run's values, what
	// pm.environment.set and vars.set write; locals are pm.variables.
	vars, locals map[string]string
	// data is the Runner data file's row, pm.iterationData.
	data map[string]string
	// globals and collection are written through and reported as writes;
	// folders are the values of the folders below the collection.
	globals, collection, folders map[string]string
	root                         string
	written                      map[string]bool
	writes                       []Write
	tests                        []TestResult
	logs                         []string
	phase                        string
	console                      []ConsoleMessage
	info                         scriptInfo
	skipped                      bool
	next                         *string
	// target is the URL the scripts' request goes to, for cookies and the
	// vault. send, vault and cookies are nil where a run cannot offer them.
	target  func() string
	send    func(*Prepared) (capture.Flow, error)
	vault   func(name, host string) (string, error)
	secrets map[string]*string
	cookies func(target string) []*http.Cookie
}

type scriptInfo struct {
	Iteration   int      `json:"iteration"`
	Iterations  int      `json:"iterationCount"`
	RequestName string   `json:"requestName"`
	RequestID   string   `json:"requestId"`
	Environment string   `json:"environment"`
	Location    []string `json:"location"`
}

// Console output is bounded independently of the retained response payload.
// Variable changes name the key, not its potentially secret value.
type ConsoleMessage struct {
	Phase string
	Kind  string
	Text  string
}

func (r *scriptRun) message(kind, text string) {
	if len(r.console) < 200 {
		if len(text) > 4096 {
			text = strings.ToValidUTF8(text[:4096], "�") + "… [truncated]"
		}
		r.console = append(r.console, ConsoleMessage{r.phase, kind, text})
	} else if len(r.console) == 200 {
		r.console = append(r.console, ConsoleMessage{r.phase, "warning", "Console output truncated (200 entries, 4096 bytes per entry)."})
	}
}

// effective is what {{name}} reads now: locals over the environment, over
// the nearest folder, over the collection, over globals.
func (r *scriptRun) effective() map[string]string {
	values := map[string]string{}
	for _, scope := range []map[string]string{r.globals, r.collection, r.folders, r.vars, r.data, r.locals} {
		maps.Copy(values, scope)
	}
	return values
}

// scope is the map a pm.* scope name reads and writes.
func (r *scriptRun) scope(name string) map[string]string {
	for _, m := range []*map[string]string{&r.vars, &r.locals, &r.globals, &r.collection} {
		if *m == nil {
			*m = map[string]string{}
		}
	}
	switch name {
	case "environment", "legacy":
		return r.vars
	case "variables":
		return r.locals
	case "globals":
		return r.globals
	case "collectionVariables":
		return r.collection
	case "iterationData":
		return r.data
	}
	return nil
}

func (r *scriptRun) set(scope, name, value string, unset bool) error {
	if name == "" {
		return errors.New("variable names cannot be empty")
	}
	m := r.scope(scope)
	if m == nil || scope == "iterationData" {
		return fmt.Errorf("pm.%s is read-only", scope)
	}
	if scope == "collectionVariables" && r.root == "" {
		return errors.New("pm.collectionVariables: this request is not saved in a collection")
	}
	if unset {
		delete(m, name)
	} else {
		m[name] = value
	}
	if r.written == nil {
		r.written = map[string]bool{}
	}
	r.written[name] = true
	switch scope {
	case "globals":
		r.writes = append(r.writes, Write{Scope: "global", Name: name, Value: value, Unset: unset})
	case "collectionVariables":
		r.writes = append(r.writes, Write{Scope: "collection", Path: r.root, Name: name, Value: value, Unset: unset})
	}
	call := "pm." + scope + ".set"
	if unset {
		call = "pm." + scope + ".unset"
	}
	if scope == "legacy" {
		call = "vars.set"
	}
	r.message("variable", call+"("+name+")")
	return nil
}

func (r *scriptRun) secret(op, name, value string) (goja.Value, string, error) {
	if r.vault == nil {
		return nil, "", errors.New("pm.vault: scripts may not use the vault; turn on “Let scripts use pm.vault” in Vault…")
	}
	if name == "" {
		return nil, "", errors.New("pm.vault: the secret name is empty")
	}
	if r.secrets == nil {
		r.secrets = map[string]*string{}
	}
	switch op {
	case "get":
		if v, ok := r.secrets[name]; ok {
			if v == nil {
				return goja.Undefined(), "", nil
			}
			return nil, *v, nil
		}
		v, err := r.vault(name, hostOf(r.target()))
		if errors.Is(err, vault.ErrNotFound) {
			return goja.Undefined(), "", nil
		}
		if err != nil {
			return nil, "", fmt.Errorf("pm.vault.get(%s): %w", name, err)
		}
		return nil, v, nil
	case "set", "unset":
		w := Write{Scope: "vault", Name: name, Value: value, Unset: op == "unset"}
		r.secrets[name] = &w.Value
		if w.Unset {
			r.secrets[name] = nil
		}
		r.writes = append(r.writes, w)
		r.message("variable", "pm.vault."+op+"("+name+")")
		return goja.Undefined(), "", nil
	}
	return nil, "", fmt.Errorf("pm.vault.%s is not supported", op)
}

// runScript executes src with the objects given, which are the globals the
// script sees beside pm, vars, test and log.
func runScript(src string, run *scriptRun, objects map[string]any) error {
	return runScriptContext(context.Background(), src, run, objects)
}

// budget is a script's two seconds of CPU-bound time, paused while
// pm.sendRequest waits on the network.
type budget struct {
	mu        sync.Mutex
	remaining time.Duration
	started   time.Time
	timer     *time.Timer
	expire    func()
}

func newBudget(d time.Duration, expire func()) *budget {
	b := &budget{remaining: d, expire: expire}
	b.resume()
	return b
}

func (b *budget) pause() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil && b.timer.Stop() {
		b.remaining -= time.Since(b.started)
	}
	b.timer = nil
}

func (b *budget) resume() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.started, b.timer = time.Now(), time.AfterFunc(max(b.remaining, 0), b.expire)
}

// Bound user scripts as well as network I/O. Interrupt also lets Runner cancel
// a script loop without leaving a goroutine (or the UI thread) stuck in Goja.
func runScriptContext(ctx context.Context, src string, run *scriptRun, objects map[string]any) error {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	vm := goja.New()
	clock := newBudget(2*time.Second, func() { vm.Interrupt(context.DeadlineExceeded) })
	defer clock.pause()
	stop := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	defer stop()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	if _, err := vm.RunProgram(chaiProgram()); err != nil {
		return err
	}
	chai := vm.Get("chai")
	vm.GlobalObject().Delete("chai")
	host, err := run.host(ctx, vm, clock, objects)
	if err != nil {
		return err
	}
	prelude, err := vm.RunProgram(pmProgram())
	if err != nil {
		return err
	}
	setup, _ := goja.AssertFunction(prelude)
	finishValue, err := setup(goja.Undefined(), vm.GlobalObject(), host, chai)
	if err != nil {
		return err
	}
	for name, value := range objects {
		if req, ok := value.(*Prepared); ok && req.editedHeaders != nil {
			obj := vm.NewObject()
			obj.Set("get", req.Get)
			obj.Set("set", req.Set)
			obj.Set("remove", req.Remove)
			for field, target := range map[string]*string{"method": &req.Method, "url": &req.URL, "body": &req.Body} {
				if err := obj.DefineAccessorProperty(field, vm.ToValue(func() string { return *target }), vm.ToValue(func(v string) {
					*target = v
					if field == "body" {
						req.editedBody = true
					}
				}), goja.FLAG_TRUE, goja.FLAG_TRUE); err != nil {
					return err
				}
			}
			value = obj
		}
		if err := vm.Set(name, value); err != nil {
			return err
		}
	}
	// Postman runs a script as an async function body, so top-level await
	// works. The wrapper shares the first line so error lines stay true.
	file := cmp.Or(run.phase, "request") + ".js"
	result, err := vm.RunScript(file, "(async function () {"+src+"\n})()")
	if err != nil {
		return err
	}
	finish, _ := goja.AssertFunction(finishValue)
	tests, err := finish(goja.Undefined())
	if err != nil {
		return err
	}
	results := tests.ToObject(vm)
	for i := range int(results.Get("length").ToInteger()) {
		t := results.Get(strconv.Itoa(i)).ToObject(vm)
		run.tests = append(run.tests, TestResult{Name: t.Get("name").String(), Pass: t.Get("pass").ToBoolean(),
			Error: t.Get("error").String(), Skipped: t.Get("skipped").ToBoolean()})
	}
	promise, _ := result.Export().(*goja.Promise)
	switch {
	case promise == nil:
		return nil
	case promise.State() == goja.PromiseStateRejected:
		return errors.New(scriptError(promise.Result(), file))
	case promise.State() == goja.PromiseStatePending:
		return errors.New("the script awaited something that never finished")
	}
	return nil
}

// scriptError is a rejected script's error with the line in the script
// that threw, skipping frames inside Chai and the pm library.
func scriptError(reason goja.Value, file string) string {
	obj, ok := reason.(*goja.Object)
	if !ok {
		return "Uncaught " + reason.String()
	}
	stack := obj.Get("stack")
	if stack == nil || goja.IsUndefined(stack) {
		return reason.String()
	}
	lines := strings.Split(stack.String(), "\n")
	message := cmp.Or(reason.String(), lines[0])
	for _, line := range lines[1:] {
		if at := strings.TrimSpace(line); strings.Contains(at, file+":") {
			return message + " " + at
		}
	}
	return message
}

// host is what pm.js builds pm.* from.
func (run *scriptRun) host(ctx context.Context, vm *goja.Runtime, clock *budget, objects map[string]any) (*goja.Object, error) {
	pairs := func(h http.Header) goja.Value {
		var list []any
		for _, key := range slices.Sorted(maps.Keys(h)) {
			for _, value := range h[key] {
				list = append(list, vm.NewArray(key, value))
			}
		}
		return vm.NewArray(list...)
	}
	object := func(m map[string]string) *goja.Object {
		obj := vm.NewObject()
		for _, key := range slices.Sorted(maps.Keys(m)) {
			obj.Set(key, m[key])
		}
		return obj
	}
	if run.target == nil {
		run.target = func() string { return "" }
	}
	event := "test"
	if strings.HasPrefix(run.phase, "pre-request") {
		event = "prerequest"
	}
	info := run.info
	info.Iterations = max(info.Iterations, 1)
	if info.Location == nil {
		info.Location = []string{}
	}
	h := vm.NewObject()
	set := func(name string, value any) { h.Set(name, value) }
	set("info", map[string]any{
		"eventName": event, "iteration": info.Iteration, "iterationCount": info.Iterations,
		"requestName": info.RequestName, "requestId": info.RequestID, "environment": info.Environment,
		"location": vm.NewArray(func() []any {
			var parts []any
			for _, p := range info.Location {
				parts = append(parts, p)
			}
			return parts
		}()...),
	})
	set("get", func(scope, name string) goja.Value {
		values := run.scope(scope)
		if scope == "variables" {
			values = run.effective()
		}
		if v, ok := values[name]; ok {
			return vm.ToValue(v)
		}
		return goja.Undefined()
	})
	set("object", func(scope string) *goja.Object {
		if scope == "variables" {
			return object(run.effective())
		}
		return object(run.scope(scope))
	})
	set("set", func(scope, name, value string) error { return run.set(scope, name, value, false) })
	set("unset", func(scope, name string) error { return run.set(scope, name, "", true) })
	set("clear", func(scope string) error {
		for _, name := range slices.Sorted(maps.Keys(run.scope(scope))) {
			if err := run.set(scope, name, "", true); err != nil {
				return err
			}
		}
		return nil
	})
	set("replaceIn", func(s string) string {
		values := run.effective()
		return ExpandVars(expandDynamic(s, values), values)
	})
	set("log", func(kind, text string) {
		if len(run.logs) < 200 {
			run.logs = append(run.logs, text[:min(len(text), 4096)])
		}
		run.message(kind, text)
	})
	set("skip", func() error {
		if event != "prerequest" {
			return errors.New("pm.execution.skipRequest only works in pre-request scripts")
		}
		run.skipped = true
		run.message("log", "pm.execution.skipRequest()")
		return nil
	})
	set("next", func(name goja.Value) {
		next := ""
		if !goja.IsNull(name) && !goja.IsUndefined(name) {
			next = name.String()
		}
		run.next = &next
	})
	set("cookies", func() goja.Value {
		h := http.Header{}
		if run.cookies != nil {
			for _, c := range run.cookies(run.target()) {
				h.Add(c.Name, c.Value)
			}
		}
		return pairs(h)
	})
	set("vault", func(op, name, value string) (goja.Value, error) {
		v, secret, err := run.secret(op, name, value)
		if v == nil && err == nil {
			return vm.ToValue(secret), nil
		}
		return v, err
	})
	set("send", func(method, target string, header [][]string, mode, body string, form []map[string]any) (goja.Value, error) {
		if run.send == nil {
			return nil, errors.New("pm.sendRequest is not available here")
		}
		var fields []Field
		for _, f := range form {
			file, _ := f["file"].(bool)
			fields = append(fields, Field{Name: fmt.Sprint(f["name"]), Value: fmt.Sprint(f["value"]), File: file})
		}
		var headers []string
		for _, pair := range header {
			if len(pair) == 2 {
				headers = append(headers, pair[0]+": "+pair[1])
			}
		}
		req, err := Request{Method: method, URL: target, Headers: strings.Join(headers, "\n"), Body: body, BodyMode: mode, Form: fields}.Prepare(nil)
		if err != nil {
			return nil, fmt.Errorf("pm.sendRequest: %w", err)
		}
		clock.pause()
		got, err := run.send(req)
		clock.resume()
		if err != nil {
			return nil, fmt.Errorf("pm.sendRequest %s %s: %w", req.Method, req.URL, err)
		}
		run.message("log", fmt.Sprintf("pm.sendRequest %s %s → %d", req.Method, req.URL, got.Status))
		return vm.ToValue(responseObject(vm, &jsResponse{Status: got.Status, Body: got.ResponseBody, Header: got.ResponseHeader, Millis: got.Millis, Bytes: got.Bytes}, pairs)), nil
	})
	set("btoa", func(s string) (string, error) {
		b := make([]byte, 0, len(s))
		for _, r := range s {
			if r > 0xff {
				return "", errors.New("btoa: the string contains characters outside Latin-1")
			}
			b = append(b, byte(r))
		}
		return base64.StdEncoding.EncodeToString(b), nil
	})
	set("atob", func(s string) (string, error) {
		s = strings.Map(func(r rune) rune {
			if strings.ContainsRune(" \t\n\r\f", r) {
				return -1
			}
			return r
		}, s)
		b, err := base64.StdEncoding.DecodeString(s + strings.Repeat("=", (4-len(s)%4)%4))
		if err != nil {
			return "", errors.New("atob: the string is not valid base64")
		}
		runes := make([]rune, len(b))
		for i, c := range b {
			runes[i] = rune(c)
		}
		return string(runes), nil
	})
	if req, ok := objects["req"].(*Prepared); ok {
		fields := map[string]*string{"method": &req.Method, "url": &req.URL, "body": &req.Body}
		set("request", map[string]any{
			"get": func(field string) string { return *fields[field] },
			"set": func(field, value string) {
				*fields[field] = value
				if field == "body" && req.editedHeaders != nil {
					req.editedBody = true
				}
			},
			"headers": func() goja.Value { return pairs(req.Header) },
			"add":     req.Add,
			"upsert":  req.Set,
			"remove":  req.Remove,
		})
	}
	if res, ok := objects["res"].(*jsResponse); ok {
		set("response", responseObject(vm, res, pairs))
	}
	return h, nil
}

func responseObject(vm *goja.Runtime, res *jsResponse, pairs func(http.Header) goja.Value) *goja.Object {
	obj := vm.NewObject()
	obj.Set("code", res.Status)
	obj.Set("status", http.StatusText(res.Status))
	obj.Set("time", res.Millis)
	obj.Set("size", res.Bytes)
	obj.Set("headers", pairs(res.Header))
	obj.Set("body", res.Body)
	return obj
}

// ResolvedVariable is one {{name}} a request uses: its value, and the scope
// it came from, or Missing. Vault values are never copied here.
type ResolvedVariable struct {
	Name, Value, Source string
	Missing             bool
}

// passed counts the assertions that held.
func (r *scriptRun) passed() int {
	n := 0
	for _, t := range r.tests {
		if t.Pass {
			n++
		}
	}
	return n
}
