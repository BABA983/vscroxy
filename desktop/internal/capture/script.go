package capture

import (
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// scriptTimeout bounds one hook. A script that loops is interrupted rather
// than holding the proxy.
const scriptTimeout = 250 * time.Millisecond

// Script is a JavaScript file with onRequest and onResponse hooks:
//
//	function onRequest(req) { req.headers["X-Debug"] = "1" }
//	function onResponse(res) { if (res.status === 500) res.status = 200 }
//
// req and res expose method or status, url, headers and body. Only text
// bodies are edited; binary bodies are left alone.
type Script struct {
	prog        *goja.Program
	hasRequest  bool
	hasResponse bool
}

// LoadScript reads and compiles a script. A missing file is no script.
func LoadScript(path string) (*Script, error) {
	src, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	prog, err := goja.Compile(path, string(src), false)
	if err != nil {
		return nil, err
	}
	vm := goja.New()
	if _, err := vm.RunProgram(prog); err != nil {
		return nil, err
	}
	_, hasRequest := goja.AssertFunction(vm.Get("onRequest"))
	_, hasResponse := goja.AssertFunction(vm.Get("onResponse"))
	return &Script{prog: prog, hasRequest: hasRequest, hasResponse: hasResponse}, nil
}

// HasResponse reports whether the script edits responses.
func (s *Script) HasResponse() bool { return s != nil && s.hasResponse }

// applyRequest runs onRequest and writes its edits back to the flow.
func (s *Script) applyRequest(f *proxy.Flow) error {
	if s == nil || !s.hasRequest || f.Request == nil {
		return nil
	}
	vm := goja.New()
	stop := time.AfterFunc(scriptTimeout, func() { vm.Interrupt("script timed out") })
	defer stop.Stop()
	if _, err := vm.RunProgram(s.prog); err != nil {
		return err
	}
	fn, ok := goja.AssertFunction(vm.Get("onRequest"))
	if !ok {
		return nil
	}

	obj := vm.NewObject()
	obj.Set("method", f.Request.Method)
	obj.Set("url", f.Request.URL.String())
	obj.Set("body", string(f.Request.Body))
	obj.Set("headers", headersObject(vm, f.Request.Header))
	if _, err := fn(goja.Undefined(), obj); err != nil {
		return err
	}

	if v := obj.Get("method"); present(v) {
		if method := v.String(); method != "" {
			f.Request.Method = method
		}
	}
	if v := obj.Get("url"); present(v) {
		if raw := v.String(); raw != "" {
			if u, err := url.Parse(raw); err == nil {
				f.Request.URL = u
			}
		}
	}
	if v := obj.Get("body"); present(v) {
		f.Request.Body = []byte(v.String())
		f.Request.Header.Del("Content-Length")
	}
	if v := obj.Get("headers"); present(v) {
		f.Request.Header = readHeaders(vm, v)
	}
	return nil
}

// applyResponse runs onResponse and writes its edits back to the flow.
func (s *Script) applyResponse(f *proxy.Flow) error {
	if s == nil || !s.hasResponse || f.Response == nil {
		return nil
	}
	vm := goja.New()
	stop := time.AfterFunc(scriptTimeout, func() { vm.Interrupt("script timed out") })
	defer stop.Stop()
	if _, err := vm.RunProgram(s.prog); err != nil {
		return err
	}
	fn, ok := goja.AssertFunction(vm.Get("onResponse"))
	if !ok {
		return nil
	}

	obj := vm.NewObject()
	obj.Set("status", f.Response.StatusCode)
	obj.Set("body", string(f.Response.Body))
	obj.Set("headers", headersObject(vm, f.Response.Header))
	if _, err := fn(goja.Undefined(), obj); err != nil {
		return err
	}

	if v := obj.Get("status"); present(v) {
		if status := int(v.ToInteger()); status > 0 {
			f.Response.StatusCode = status
		}
	}
	if v := obj.Get("body"); present(v) {
		f.Response.Body = []byte(v.String())
	}
	if v := obj.Get("headers"); present(v) {
		f.Response.Header = readHeaders(vm, v)
	}
	return nil
}

func present(v goja.Value) bool { return v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) }

func headersObject(vm *goja.Runtime, h http.Header) *goja.Object {
	obj := vm.NewObject()
	for key, values := range h {
		obj.Set(key, strings.Join(values, ", "))
	}
	return obj
}

func readHeaders(vm *goja.Runtime, v goja.Value) http.Header {
	h := make(http.Header)
	obj := v.ToObject(vm)
	for _, key := range obj.Keys() {
		h.Set(key, obj.Get(key).String())
	}
	return h
}
