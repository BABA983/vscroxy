package app

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// environment is a named set of variables a composed request uses as {{name}},
// kept with the others so a request can move between a dev and a live host.
type environment struct {
	Name string            `json:"name"`
	Vars map[string]string `json:"vars,omitempty"`
}

// envFile is what the environments file holds.
type envFile struct {
	Active string        `json:"active"`
	Envs   []environment `json:"environments"`
}

// loadEnvs reads the environments and the one in use; a missing file has none.
func loadEnvs(path string) ([]environment, string, error) {
	if path == "" {
		return nil, "", nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var f envFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, "", err
	}
	return f.Envs, f.Active, nil
}

// saveEnvs keeps the environments and which one is in use.
func saveEnvs(path string, envs []environment, active string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(envFile{Active: active, Envs: envs})
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(data, '\n'))
}

// expandVars replaces {{name}} with a variable's value, leaving a name it does
// not know as it is, so the mistake shows in the request.
func expandVars(s string, vars map[string]string) string {
	if len(vars) == 0 || !strings.Contains(s, "{{") {
		return s
	}
	var b strings.Builder
	for {
		start := strings.Index(s, "{{")
		if start < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := strings.Index(s[start:], "}}")
		if end < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:start])
		name := strings.TrimSpace(s[start+2 : start+end])
		if value, ok := vars[name]; ok {
			b.WriteString(value)
		} else {
			b.WriteString(s[start : start+end+2])
		}
		s = s[start+end+2:]
	}
}

// testResult is one assertion a request script made.
type testResult struct {
	Name string
	Pass bool
}

// jsRequest is the req object a pre-request script gets: what it changes is
// what the proxy sends.
type jsRequest struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Body   string `json:"body"`
	header http.Header
	// Only pre-script templates record explicit overrides of generated fields.
	editedHeaders map[string]bool
	editedBody    bool
}

// Get is the request header named.
func (r *jsRequest) Get(name string) string { return r.header.Get(name) }

// Set sets a request header.
func (r *jsRequest) Set(name, value string) {
	r.header.Set(name, value)
	if r.editedHeaders != nil {
		r.editedHeaders[http.CanonicalHeaderKey(name)] = true
	}
}

// Remove drops a request header.
func (r *jsRequest) Remove(name string) {
	r.header.Del(name)
	if r.editedHeaders != nil {
		r.editedHeaders[http.CanonicalHeaderKey(name)] = true
	}
}

// jsResponse is the res object a post-response script gets.
type jsResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
	header http.Header
}

// Get is the response header named.
func (r *jsResponse) Get(name string) string { return r.header.Get(name) }

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
	vars    map[string]string
	written map[string]bool
	tests   []testResult
	logs    []string
	phase   string
	console []runMessage
}

// Console output is bounded independently of the retained response payload.
// Variable changes name the key, not its potentially secret value.
type runMessage struct {
	Phase string
	Kind  string
	Text  string
}

func (r *scriptRun) message(kind, text string) {
	if len(r.console) < 200 {
		if len(text) > 4096 {
			text = strings.ToValidUTF8(text[:4096], "�") + "… [truncated]"
		}
		r.console = append(r.console, runMessage{r.phase, kind, text})
	} else if len(r.console) == 200 {
		r.console = append(r.console, runMessage{r.phase, "warning", "Console output truncated (200 entries, 4096 bytes per entry)."})
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
		"get": func(name string) string { return run.vars[name] },
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
		run.tests = append(run.tests, testResult{Name: name, Pass: pass})
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
		if req, ok := value.(*jsRequest); ok && req.editedHeaders != nil {
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

type resolvedVariable struct {
	Name, Value, Source string
	Missing             bool
}

func resolveRequestVariables(r savedRequest, vars map[string]string, written map[string]bool) []resolvedVariable {
	var rows []resolvedVariable
	for _, name := range requestVariables(r) {
		value, ok := vars[name]
		source := "Run input"
		if !ok {
			source = "Missing"
		} else if written[name] {
			source = "Pre-request script"
		}
		rows = append(rows, resolvedVariable{Name: name, Value: value, Source: source, Missing: !ok})
	}
	return rows
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
