package request

import (
	"cmp"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
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

// Ajv 6.12.6 is the Postman-compatible JSON Schema validator (MIT, see
// THIRD_PARTY_NOTICES.md). Load its unmodified browser build as a private
// CommonJS module, only when a script uses a schema assertion.
//
//go:embed js/ajv.js
var ajvSource string

// Lodash is loaded on first require, with separate exports per script runtime.
// Its unmodified CommonJS build is MIT-licensed (see THIRD_PARTY_NOTICES.md).
//
//go:embed js/lodash.js
var lodashSource string

// CryptoJS 4.2.0 is Postman's crypto-js, loaded on first require like Lodash
// (MIT, see THIRD_PARTY_NOTICES.md). Its random numbers come from Go.
//
//go:embed js/crypto-js.js
var cryptoJSSource string

// Moment 2.31.0 is the latest 2.x release in the line Postman's sandbox pins
// (2.30.1), its unmodified minified browser build without locales (MIT, see
// THIRD_PARTY_NOTICES.md). It reads the Go process's local time zone through
// Date, and loads on first require.
//
//go:embed js/moment.min.js
var momentSource string

//go:embed js/pm.js
var pmSource string

// Libraries are the modules require loads built in; an addon cannot take
// one of these names.
var Libraries = []string{"chai", "lodash", "crypto-js", "moment"}

// The libraries compile once; every script still gets a fresh runtime.
var (
	chaiProgram = sync.OnceValue(func() *goja.Program { return goja.MustCompile("chai.js", chaiSource, false) })
	ajvProgram  = sync.OnceValue(func() *goja.Program {
		return goja.MustCompile("ajv.js", "(function () { var module = {exports: {}}, exports = module.exports;\n"+ajvSource+"\nreturn module.exports; })()", false)
	})
	lodashProgram = sync.OnceValue(func() *goja.Program {
		return goja.MustCompile("lodash.js", "(function () { var module = {exports: {}}, exports = module.exports;\n"+lodashSource+"\nreturn module.exports; })()", false)
	})
	cryptoJSProgram = sync.OnceValue(func() *goja.Program {
		return goja.MustCompile("crypto-js.js", "(function (crypto) { var module = {exports: {}}, exports = module.exports;\n"+
			"var require = function (name) { if (name === \"crypto\") return crypto; throw new Error(\"Cannot find module \" + name); };\n"+
			cryptoJSSource+"\nreturn module.exports; })", false)
	})
	momentProgram = sync.OnceValue(func() *goja.Program {
		return goja.MustCompile("moment.min.js", "(function () { var module = {exports: {}}, exports = module.exports;\n"+
			momentSource+"\nreturn module.exports; })()", false)
	})
	pmProgram = sync.OnceValue(func() *goja.Program { return goja.MustCompile("pm.js", pmSource, false) })
)

// TestResult is one assertion a request script or check made. Error says
// why a failed one failed; a skipped test (pm.test.skip) passes.
type TestResult struct {
	Name    string
	Pass    bool
	Error   string `json:",omitempty"`
	Skipped bool   `json:",omitempty"`
}

// Prepared is a request ready to send, and what pm.request edits: what a
// pre-request script changes is what the proxy sends.
type Prepared struct {
	Method string
	URL    string
	Body   string
	// BodyFile is the source of a Binary body. Code generators that can
	// reference a file read it instead of inlining the bytes.
	BodyFile string
	Header   http.Header
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

// jsResponse is the response pm.response is built from. A rule script's
// response is editable: its code, headers and body change what the client
// gets.
type jsResponse struct {
	Status   int
	Body     string
	Header   http.Header
	Millis   int
	Bytes    int
	editable bool
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

// Variables applies w to a scope's variable rows: a set writes the first
// enabled row's Current value or adds a local row, and an unset clears the
// Current value so the Initial value applies again. A row that only existed
// locally disappears. Initial values are the shared asset's and stay untouched.
func (w Write) Variables(vars []Variable) []Variable {
	if w.Unset {
		vars = slices.Clone(vars)
		var out []Variable
		for _, v := range vars {
			if strings.TrimSpace(v.Name) != w.Name {
				out = append(out, v)
				continue
			}
			v.Current = ""
			if !v.Local {
				out = append(out, v)
			}
		}
		return out
	}
	vars = slices.Clone(vars)
	for i, v := range vars {
		if strings.TrimSpace(v.Name) == w.Name && !v.Disabled {
			vars[i].Current = w.Value
			return vars
		}
	}
	return append(vars, Variable{Name: w.Name, Current: w.Value, Local: true})
}

// scriptRun is the state the scripts of one send share: the variable scopes
// they read and write, the assertions they made and what they logged.
type scriptRun struct {
	// vars are the environment's and this run's values, what
	// pm.environment.set writes; locals are pm.variables.
	vars, locals map[string]string
	// The Initial values behind the effective maps, so an unset falls back
	// to them instead of leaving the name undefined for later steps.
	environmentInitial, globalsInitial, collectionInitial map[string]string
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
	// readOnly are the scopes a script may read but not write; env is
	// process.env, which only rule scripts have.
	readOnly map[string]bool
	env      map[string]string
	// addons are the user's modules require loads by name, as source.
	addons map[string]string
	// file names the script in errors, by its phase when empty; sourceMap
	// maps a converted module's lines back to the file as written.
	file, sourceMap string
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
	case "environment":
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

// initial returns the Initial values behind a scope's effective map. Locals
// and iteration data have none.
func (r *scriptRun) initial(scope string) map[string]string {
	switch scope {
	case "environment":
		return r.environmentInitial
	case "globals":
		return r.globalsInitial
	case "collectionVariables":
		return r.collectionInitial
	}
	return nil
}

func (r *scriptRun) set(scope, name, value string, unset bool) error {
	if name == "" {
		return errors.New("variable names cannot be empty")
	}
	m := r.scope(scope)
	if m == nil || scope == "iterationData" || r.readOnly[scope] {
		return fmt.Errorf("pm.%s is read-only", scope)
	}
	if scope == "collectionVariables" && r.root == "" {
		return errors.New("pm.collectionVariables: this request is not saved in a collection")
	}
	if unset {
		if initial, ok := r.initial(scope)[name]; ok {
			m[name] = initial
		} else {
			delete(m, name)
		}
	} else {
		m[name] = value
	}
	if r.written == nil {
		r.written = map[string]bool{}
	}
	r.written[name] = true
	switch scope {
	case "environment":
		if unset {
			r.writes = append(r.writes, Write{Scope: "environment", Name: name, Unset: true})
		} else {
			// A set after an unset leaves the variable defined; do not let
			// the earlier unset delete the new Current value afterwards.
			r.writes = slices.DeleteFunc(r.writes, func(w Write) bool {
				return w.Scope == "environment" && w.Name == name && w.Unset
			})
		}
	case "globals":
		r.writes = append(r.writes, Write{Scope: "global", Name: name, Value: value, Unset: unset})
	case "collectionVariables":
		r.writes = append(r.writes, Write{Scope: "collection", Path: r.root, Name: name, Value: value, Unset: unset})
	}
	call := "pm." + scope + ".set"
	if unset {
		call = "pm." + scope + ".unset"
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

// runScript executes src. objects hands pm the request ("req", a *Prepared)
// and response ("res", a *jsResponse) behind pm.request and pm.response.
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
	// Postman runs a script as an async function body, so top-level await
	// works. The wrapper shares the first line so error lines stay true. A
	// proxy message is passed privately, never installed on pm or the global.
	file := cmp.Or(run.file, cmp.Or(run.phase, "request")+".js")
	function, err := vm.RunScript(file, "(async function (__proxyMessage) {"+src+"\n})\n"+run.sourceMap)
	if err != nil {
		return err
	}
	execute, _ := goja.AssertFunction(function)
	argument := host.Get("message")
	if argument == nil {
		argument = goja.Undefined()
	}
	result, err := execute(goja.Undefined(), argument)
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
	switch {
	case strings.HasPrefix(run.phase, "pre-request"):
		event = "prerequest"
	case run.phase == "request" || run.phase == "response" || run.phase == "message": // a rule script's hook
		event = run.phase
	}
	info := run.info
	info.Iterations = max(info.Iterations, 1)
	if info.Location == nil {
		info.Location = []string{}
	}
	h := vm.NewObject()
	set := func(name string, value any) { h.Set(name, value) }
	set("ajv", func() (goja.Value, error) { return vm.RunProgram(ajvProgram()) })
	set("lodash", func() (goja.Value, error) { return vm.RunProgram(lodashProgram()) })
	set("moment", func() (goja.Value, error) { return vm.RunProgram(momentProgram()) })
	set("cryptojs", func() (goja.Value, error) {
		load, err := vm.RunProgram(cryptoJSProgram())
		if err != nil {
			return nil, err
		}
		fn, _ := goja.AssertFunction(load)
		random := vm.NewObject()
		random.Set("getRandomValues", func(array *goja.Object) *goja.Object {
			for i := range int(array.Get("length").ToInteger()) {
				var b [4]byte
				_, _ = rand.Read(b[:])
				array.Set(strconv.Itoa(i), binary.LittleEndian.Uint32(b[:]))
			}
			return array
		})
		return fn(goja.Undefined(), random)
	})
	if run.env != nil {
		set("env", object(run.env))
	}
	// addon compiles an addon, an ES or CommonJS module, as a function of
	// module, exports and require; pm.js runs it once per script and caches
	// what it exports.
	set("addon", func(name string) (goja.Value, error) {
		src, ok := run.addons[name]
		if !ok {
			return goja.Undefined(), nil
		}
		m, err := addonModule(name, src)
		if err != nil {
			return nil, fmt.Errorf("addon %q: %w", name, err)
		}
		// goja reads the source map from the last line before "})".
		return vm.RunScript("addons/"+name+".js", "(function (module, exports, require) {"+m.code+"\n"+m.sourceMap+"\n})")
	})
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
			"readOnly": run.phase == "mock" || run.phase == "message",
			"get":      func(field string) string { return *fields[field] },
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
	if message, ok := objects["message"].(*capture.ScriptMessage); ok {
		set("message", messageObject(vm, message))
	}
	return h, nil
}

func responseObject(vm *goja.Runtime, res *jsResponse, pairs func(http.Header) goja.Value) *goja.Object {
	obj := vm.NewObject()
	obj.Set("code", res.Status)
	obj.Set("status", http.StatusText(res.Status))
	obj.Set("time", res.Millis)
	obj.Set("size", res.Bytes)
	obj.Set("headers", func() goja.Value { return pairs(res.Header) })
	obj.Set("body", res.Body)
	if res.editable {
		if res.Header == nil {
			res.Header = http.Header{}
		}
		obj.Set("setCode", func(code int) (string, error) {
			if code < 100 || code > 999 {
				return "", fmt.Errorf("pm.response.code: %d is not an HTTP status", code)
			}
			res.Status = code
			return http.StatusText(code), nil
		})
		obj.Set("setBody", func(body string) { res.Body = body })
		obj.Set("add", res.Header.Add)
		obj.Set("upsert", res.Header.Set)
		obj.Set("remove", res.Header.Del)
	}
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
