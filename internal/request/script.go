package request

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// TestResult is one assertion a request script made.
type TestResult struct {
	Name string
	Pass bool
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
	if r.editedHeaders != nil {
		r.editedHeaders[http.CanonicalHeaderKey(name)] = true
	}
}

// Remove drops a request header.
func (r *Prepared) Remove(name string) {
	r.Header.Del(name)
	if r.editedHeaders != nil {
		r.editedHeaders[http.CanonicalHeaderKey(name)] = true
	}
}

// jsResponse is the res object a post-response script gets.
type jsResponse struct {
	Status int         `json:"status"`
	Body   string      `json:"body"`
	Header http.Header `json:"-"`
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

// scriptRun is one execution of a request script: the variables it changed,
// the assertions it made and what it logged.
type scriptRun struct {
	// vars are the environment's and this run's values, what vars.set
	// writes; inherited are the folder and global values beneath them.
	vars      map[string]string
	inherited map[string]string
	written   map[string]bool
	tests     []TestResult
	logs      []string
	phase     string
	console   []ConsoleMessage
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

// runScript executes src with the objects given, which are the globals the
// script sees beside vars, test and log.
func runScript(src string, run *scriptRun, objects map[string]any) error {
	return runScriptContext(context.Background(), src, run, objects)
}

// Bound user scripts as well as network I/O. Interrupt also lets Runner cancel
// a script loop without leaving a goroutine (or the UI thread) stuck in Goja.
func runScriptContext(ctx context.Context, src string, run *scriptRun, objects map[string]any) error {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	if run.vars == nil {
		run.vars = map[string]string{}
	}
	vm := goja.New()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	defer stop()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	vm.Set("vars", map[string]any{
		"get": func(name string) string {
			if value, ok := run.vars[name]; ok {
				return value
			}
			return run.inherited[name]
		},
		"set": func(name, value string) {
			run.vars[name] = value
			if run.written == nil {
				run.written = map[string]bool{}
			}
			run.written[name] = true
			run.message("variable", "vars.set("+name+")")
		},
	})
	vm.Set("test", func(name string, pass bool) {
		run.tests = append(run.tests, TestResult{Name: name, Pass: pass})
	})
	vm.Set("log", func(values ...any) {
		parts := make([]string, len(values))
		for i, v := range values {
			parts[i] = fmt.Sprint(v)
		}
		text := strings.Join(parts, " ")
		if len(run.logs) < 200 {
			run.logs = append(run.logs, text[:min(len(text), 4096)])
		}
		run.message("log", text)
	})
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
	_, err := vm.RunScript(cmp.Or(run.phase, "request")+".js", src)
	return err
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
